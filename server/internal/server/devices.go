package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

func (s *Server) attach(d domain.Device, pairingRequest *http.Request) *runtimeDevice {
	var a device.Adapter
	var err error
	var fingerprint [32]byte
	var hasFingerprint bool
	if d.Adapter == "simulator" {
		a = device.NewSimulator(d.ID, d.Model)
	} else {
		key, e := s.readSecret("device:" + d.ID)
		if e != nil {
			err = e
		} else {
			a, err = s.newDeviceAdapter(d.BaseURL, key)
			// Canonicalize only the private advisory comparison. Authentication
			// keeps the exact provisioned token, including its character case.
			if decoded, e := hex.DecodeString(key); d.Adapter == "pico-standalone" && e == nil && len(decoded) == 32 {
				fingerprint = sha256.Sum256(decoded)
				hasFingerprint = true
			}
		}
	}
	ctx, cancel := context.WithCancel(s.ctx)
	rt := &runtimeDevice{view: domain.DeviceView{Device: d}, adapter: a, cancel: cancel, identityMismatch: d.Adapter == "pico-standalone", awaitingIdentity: d.Adapter == "pico-standalone"}
	if rt.view.ModelSource == "" {
		if d.Model != "" {
			rt.view.ModelSource = "manual"
		} else {
			rt.view.ModelSource = "pending"
		}
	}
	s.initTelemetry(rt)
	rt.keyFingerprint, rt.hasKeyFingerprint = fingerprint, hasFingerprint
	if err != nil {
		rt.view.Error = "Device credentials or address could not be loaded"
	}
	s.mu.Lock()
	if old, ok := s.devices[d.ID]; ok {
		old.cancel()
	}
	s.devices[d.ID] = rt
	if !d.IsBrewer() {
		s.clearBrewerLinksLocked(d.ID)
	}
	s.updateKeyReuseLocked()
	s.mu.Unlock()
	// Save the browser's LAN origin before the first poll chooses a receiver.
	// Startup uses the receiver already saved for this registration.
	if pairingRequest != nil {
		s.requestTelemetryPairing(rt, pairingRequest)
	}
	if d.Enabled && err == nil {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.poll(ctx, rt) }()
	}
	return rt
}

// updateKeyReuseLocked derives the advisory from registered physical machines,
// including disabled ones. Call with s.mu held after registration changes.
// No credential reads are needed when serving fleet snapshots or SSE events.
func (s *Server) updateKeyReuseLocked() {
	counts := make(map[[32]byte]int)
	for _, d := range s.devices {
		if d.hasKeyFingerprint {
			counts[d.keyFingerprint]++
		}
	}
	for _, d := range s.devices {
		d.mu.Lock()
		d.view.KeyReused = d.hasKeyFingerprint && counts[d.keyFingerprint] > 1
		d.mu.Unlock()
	}
}
func (s *Server) poll(ctx context.Context, d *runtimeDevice) {
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		s.refresh(ctx, d)
		d.mu.RLock()
		online := d.view.Online && !d.identityMismatch
		d.mu.RUnlock()
		if online && time.Since(d.lastHistory) > 45*time.Second {
			d.lastHistory = time.Now()
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				if e := s.pullHistory(ctx, d); e != nil && ctx.Err() == nil {
					s.cfg.Logger.Debug("History sync deferred", "error", e)
				}
			}()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Server) refresh(ctx context.Context, d *runtimeDevice) {
	if d.adapter == nil {
		return
	}
	d.commandMu.Lock()
	defer d.commandMu.Unlock()
	if !s.registeredDevice(d) {
		return
	}
	if status, err := s.verifiedStatus(ctx, d); err == nil {
		s.syncTelemetry(ctx, d, false)
		if err := s.syncBrewGroup(ctx, d, status); err != nil && ctx.Err() == nil {
			s.cfg.Logger.Debug("Group brewing deferred", "error", err)
		}
	}
}

type deviceIdentity struct {
	UID string `json:"uid"`
}

// pinIdentity is create-only: changing an address or restarting the hub never
// changes the physical appliance associated with a registration's history.
func (s *Server) pinIdentity(d *runtimeDevice, uid string) error {
	d.mu.RLock()
	id, physical := d.view.ID, d.view.Adapter == "pico-standalone"
	d.mu.RUnlock()
	if !physical {
		return nil
	}
	if len(uid) < 1 || len(uid) > 32 || strings.IndexFunc(uid, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return &device.Error{StatusCode: 409, Code: "identity_missing", Message: "The appliance did not report a valid device identity"}
	}
	var pinned deviceIdentity
	_, err := s.db.Get("device-identity", id, &pinned)
	if errors.Is(err, store.ErrNotFound) {
		_, err = s.db.Put("device-identity", id, deviceIdentity{UID: uid}, 0)
		if err == nil {
			return nil
		}
		if errors.Is(err, store.ErrConflict) {
			_, err = s.db.Get("device-identity", id, &pinned)
		}
	}
	if err != nil || pinned.UID == "" {
		return &device.Error{StatusCode: 503, Code: "identity_storage", Message: "The saved device identity could not be verified"}
	}
	if pinned.UID != uid {
		return &device.Error{StatusCode: 409, Code: "identity_mismatch", Message: "This address now belongs to a different appliance. Restore the original connection or register the other appliance separately"}
	}
	return nil
}

// verifiedStatus accepts only an authenticated observation from the pinned
// appliance. A mismatching response never replaces its displayed status.
func (s *Server) verifiedStatus(ctx context.Context, d *runtimeDevice) (domain.Status, error) {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Status{}, err
	}
	status, e := d.adapter.Status(ctx)
	identityFailure := false
	identityVerified := false
	var caps *domain.Capabilities
	if e == nil {
		e = s.pinIdentity(d, status.DeviceUID)
		identityFailure = e != nil
		identityVerified = e == nil
	}
	if e == nil {
		d.mu.RLock()
		physical, needsDiscovery := d.view.Adapter == "pico-standalone", d.view.Capabilities == nil
		d.mu.RUnlock()
		if physical || needsDiscovery {
			var discovered domain.Capabilities
			discovered, e = d.adapter.Discover(ctx)
			if e == nil {
				caps = &discovered
			}
			if e == nil && physical {
				e = validateStatusModel(status, discovered)
			}
			if e == nil && physical {
				e = s.reconcileModel(d, discovered)
			}
			if e == nil {
				caps = &discovered
			}
		}
	}
	now := time.Now().UTC()
	d.mu.Lock()
	defer d.mu.Unlock()
	if identityVerified || identityFailure {
		d.awaitingIdentity = false
	}
	if e != nil {
		d.view.Online = false
		d.telemetryReconnect = false
		d.view.Error = safeMessage(e.Error())
		if identityFailure {
			d.identityMismatch = true
		}
		if identityVerified {
			d.identityMismatch = false
			d.view.Status = &status
			var problem *device.Error
			if errors.As(e, &problem) && (problem.Code == "unsupported_model" || problem.Code == "invalid_model") {
				d.view.ModelErrorCode, d.view.ModelError = problem.Code, problem.Message
				d.view.ModelSelectionRequired = false
				d.view.Capabilities = nil
			}
			if errors.As(e, &problem) && problem.Code == "model_required" {
				d.view.Capabilities = caps
			}
		}
		return domain.Status{}, e
	}
	if d.view.Adapter == "pico-standalone" && (!d.view.Online || d.view.Status != nil && status.UptimeMS < d.view.Status.UptimeMS) {
		d.telemetryReconnect = true
	}
	d.identityMismatch = false
	d.view.Status = &status
	d.view.Online = true
	d.view.Error = ""
	d.view.LastSeen = &now
	if caps != nil {
		d.view.Capabilities = caps
	}
	return status, nil
}
func (s *Server) fleet() []domain.DeviceView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.DeviceView, 0, len(s.devices))
	for _, d := range s.devices {
		d.mu.RLock()
		out = append(out, d.view)
		d.mu.RUnlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (s *Server) lookupRuntime(w http.ResponseWriter, r *http.Request) (*runtimeDevice, bool) {
	s.mu.RLock()
	d, ok := s.devices[chi.URLParam(r, "id")]
	s.mu.RUnlock()
	if !ok {
		s.fail(w, 404, "device_not_found", "Machine not found")
		return nil, false
	}
	return d, true
}
func (s *Server) runtime(w http.ResponseWriter, r *http.Request) (*runtimeDevice, bool) {
	return s.connectionRuntime(w, r, false)
}
func (s *Server) connectionRuntime(w http.ResponseWriter, r *http.Request, allowIdentityMismatch bool) (*runtimeDevice, bool) {
	d, ok := s.lookupRuntime(w, r)
	if !ok {
		return nil, false
	}
	if d.adapter == nil {
		s.fail(w, 503, "device_unavailable", "Update the machine address and key")
		return nil, false
	}
	d.mu.RLock()
	enabled := d.view.Enabled
	mismatch := d.identityMismatch || d.view.Adapter == "pico-standalone" && d.view.Status == nil
	modelReady := modelControlReady(d.view)
	d.mu.RUnlock()
	if !enabled {
		s.fail(w, 409, "device_disabled", "Open Edit connection and enable monitoring before connecting")
		return nil, false
	}
	if mismatch && !allowIdentityMismatch {
		s.fail(w, 409, "device_identity_mismatch", "The registered appliance identity is not verified. Test the original connection before continuing")
		return nil, false
	}
	if !modelReady && !allowIdentityMismatch {
		s.fail(w, 409, "device_model", "Identify the supported firmware model before using this machine")
		return nil, false
	}
	return d, true
}
func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) { s.json(w, 200, s.fleet()) }
func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.lookupRuntime(w, r); ok {
		d.mu.RLock()
		v := d.view
		d.mu.RUnlock()
		s.json(w, 200, v)
	}
}
func (s *Server) saveDevice(w http.ResponseWriter, r *http.Request) {
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	var in struct {
		Name      string `json:"name"`
		Model     string `json:"model"`
		Adapter   string `json:"adapter"`
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
		PairingID string `json:"pairing_id"`
		USBJobID  string `json:"usb_job_id"`
		Location  string `json:"location"`
		Enabled   *bool  `json:"enabled"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 1 || len(in.Name) > 100 {
		s.fail(w, 400, "name", "Give the machine a name up to 100 characters")
		return
	}
	if in.Adapter != "pico-standalone" && in.Adapter != "simulator" {
		s.fail(w, 400, "adapter", "Choose pico-standalone or simulator")
		return
	}
	if s.cfg.Demo && in.Adapter != "simulator" {
		s.fail(w, 403, "demo", "Demo mode only connects to simulated machines")
		return
	}
	if in.Model == "auto" {
		in.Model = ""
	}
	if in.Adapter == "pico-standalone" && in.Model != "" && !device.SupportedModel(in.Model) {
		s.fail(w, 400, "unsupported_model", "This model does not expose a supported standalone control adapter")
		return
	}
	if _, ok := s.selectProfile(in.Model); !ok && !(in.Adapter == "pico-standalone" && (in.Model == "" || device.SupportedModel(in.Model))) {
		s.fail(w, 400, "model", "Choose a supported machine profile")
		return
	}
	if len(in.Location) > 160 || len(in.BaseURL) > 1024 || len(in.APIKey) > 1024 {
		s.fail(w, 400, "connection", "Machine connection fields exceed their limits")
		return
	}
	id := chi.URLParam(r, "id")
	var d domain.Device
	var previous *runtimeDevice
	var disabledTelemetry *domain.TelemetryStatus
	manualSelection := in.Adapter == "pico-standalone" && in.Model != ""
	version := int64(0)
	status := 201
	if id != "" {
		var e error
		version, e = s.db.Get("device", id, &d)
		if e != nil {
			s.storeError(w, e)
			return
		}
		if d.Adapter != in.Adapter || d.Adapter == "simulator" && d.Model != in.Model {
			s.fail(w, 409, "identity_change", "Register a new machine to change its model or connection type; its existing brew history belongs to this identity")
			return
		}
		s.mu.RLock()
		old := s.devices[id]
		s.mu.RUnlock()
		if old != nil {
			previous = old
			old.commandMu.Lock()
			defer old.commandMu.Unlock()
			// Discovery can reconcile the saved model while this edit waits
			// for an in-flight operation. Preserve that newly verified identity.
			version, e = s.db.Get("device", id, &d)
			if e != nil {
				s.storeError(w, e)
				return
			}
			old.mu.RLock()
			busy := old.view.Status != nil && old.view.Status.Busy && !old.identityMismatch
			reported := old.view.Capabilities != nil && old.view.Capabilities.Model != ""
			manualSelection = manualSelection && !reported && old.view.ModelErrorCode != "invalid_model" && old.view.ModelErrorCode != "unsupported_model"
			if old.view.ModelErrorCode == "not_reported" {
				reported = false
				manualSelection = in.Model != ""
			}
			if reported && in.Model != "" && in.Model != old.view.Model {
				old.mu.RUnlock()
				s.fail(w, 409, "model_reported", "This firmware reports its model automatically")
				return
			}
			old.mu.RUnlock()
			if busy {
				s.fail(w, 409, "busy", "Finish the current operation before changing its connection")
				return
			}
		}
		status = 200
	} else {
		id = randomID()
		d.CreatedAt = time.Now().UTC()
	}
	key := in.APIKey
	var pairing *machinePairing
	var expectedUID string
	if in.PairingID != "" || in.USBJobID != "" {
		if status != http.StatusCreated || in.Adapter != "pico-standalone" || key != "" || in.PairingID != "" && in.USBJobID != "" {
			s.fail(w, 400, "pairing_input", "Choose one pairing method for this new machine")
			return
		}
		if in.PairingID != "" {
			pairing = s.pairings[in.PairingID]
			if pairing == nil || !time.Now().Before(pairing.expires) || pairing.base != in.BaseURL {
				s.fail(w, 409, "pairing_expired", "Start pairing again for this machine")
				return
			}
			if pairing.savedID != "" {
				s.mu.RLock()
				rt := s.devices[pairing.savedID]
				s.mu.RUnlock()
				if rt == nil {
					s.fail(w, 409, "pairing_expired", "Start pairing again for this machine")
					return
				}
				rt.mu.RLock()
				view := rt.view
				rt.mu.RUnlock()
				s.json(w, 200, view)
				return
			}
			if pairing.claimedKey == "" {
				p, err := s.pairClient(in.BaseURL)
				if err != nil {
					s.fail(w, 400, "connection", "Enter the machine’s address")
					return
				}
				key, err = p.Claim(r.Context(), pairing.nonce)
				if err != nil {
					s.deviceError(w, err)
					return
				}
				if key == "" {
					s.json(w, 202, map[string]string{"pairing": "pending"})
					return
				}
				pairing.claimedKey = key
			}
			key = pairing.claimedKey
		} else {
			var err error
			key, expectedUID, err = s.usb.InstalledCredential(in.USBJobID)
			if err != nil {
				s.fail(w, 409, "usb_key_unavailable", "USB setup has no verified pairing result for this machine")
				return
			}
		}
	}
	if in.Adapter == "simulator" {
		in.BaseURL = ""
		key = ""
		d.KeyConfigured = false
	}
	if key == "" && d.KeyConfigured {
		key, _ = s.readSecret("device:" + id)
	}
	if in.Adapter == "pico-standalone" {
		adapter, e := s.newDeviceAdapter(in.BaseURL, key)
		if e != nil {
			s.fail(w, 400, "connection", safeMessage(e.Error()))
			return
		}
		if expectedUID != "" {
			machine, e := adapter.Status(r.Context())
			if e != nil || machine.DeviceUID != expectedUID {
				s.fail(w, 409, "usb_identity", "Check that this is the address of the machine you set up over USB")
				return
			}
		}
	}
	d.ID = id
	d.Name = in.Name
	if in.Model != "" || in.Adapter == "simulator" {
		d.Model = in.Model
		if d.ModelSource != "firmware" || manualSelection {
			d.ModelSource = "manual"
		}
	} else if d.Model == "" {
		d.ModelSource = "pending"
	}
	d.Adapter = in.Adapter
	d.BaseURL = in.BaseURL
	d.Location = in.Location
	d.Enabled = true
	if in.Enabled != nil {
		d.Enabled = *in.Enabled
	}
	d.KeyConfigured = key != ""
	d.ClearInapplicableBrewer()
	var encrypted []byte
	if key != "" {
		encrypted = s.vault.seal("device:"+id, key)
	}
	if previous != nil && !d.Enabled {
		state := s.releaseTelemetry(r.Context(), previous)
		disabledTelemetry = &state
	}
	if _, e := s.db.PutWithSecret("device", id, d, version, "device:"+id, encrypted); e != nil {
		s.storeError(w, e)
		return
	}
	if in.Adapter == "simulator" {
		_ = s.db.DeleteSecret("device:" + id)
	}
	if pairing != nil {
		pairing.savedID = id
		pairing.claimedKey = ""
		pairing.nonce = ""
	}
	rt := s.attach(d, r)
	if disabledTelemetry != nil {
		rt.mu.Lock()
		rt.view.Telemetry = *disabledTelemetry
		rt.mu.Unlock()
	}
	rt.mu.RLock()
	view := rt.view
	rt.mu.RUnlock()
	s.json(w, status, view)
}
func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	d, ok := s.lookupRuntime(w, r)
	if !ok {
		return
	}
	d.commandMu.Lock()
	defer d.commandMu.Unlock()
	d.mu.RLock()
	busy := d.view.Status != nil && d.view.Status.Busy && !d.identityMismatch
	id := d.view.ID
	d.mu.RUnlock()
	if busy {
		s.fail(w, 409, "busy", "Finish the current operation before removing the machine")
		return
	}
	if err := s.removeGroupMember(id); err != nil {
		s.storeError(w, err)
		return
	}
	telemetry := s.releaseTelemetry(r.Context(), d)
	if e := s.db.DeleteDeviceRegistration(id); e != nil {
		s.storeError(w, e)
		return
	}
	d.cancel()
	s.mu.Lock()
	delete(s.devices, id)
	s.clearBrewerLinksLocked(id)
	s.updateKeyReuseLocked()
	s.mu.Unlock()
	s.json(w, 200, map[string]any{"deleted": true, "telemetry": telemetry})
}
func (s *Server) testDevice(w http.ResponseWriter, r *http.Request) {
	d, ok := s.connectionRuntime(w, r, true)
	if !ok {
		return
	}
	s.requestTelemetryPairing(d, r)
	s.refresh(r.Context(), d)
	d.mu.RLock()
	v := d.view
	d.mu.RUnlock()
	if !v.Online {
		s.fail(w, 502, "connection", v.Error)
		return
	}
	s.json(w, 200, v)
}
func (s *Server) display(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.runtime(w, r); ok {
		v, e := d.adapter.Display(r.Context())
		if e != nil {
			s.deviceError(w, e)
			return
		}
		s.json(w, 200, v)
	}
}
func (s *Server) machineRecipes(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.runtime(w, r); ok {
		v, e := d.adapter.Recipes(r.Context())
		if e != nil {
			s.deviceError(w, e)
			return
		}
		s.json(w, 200, map[string]any{"recipes": v})
	}
}
func (s *Server) deleteMachineRecipe(w http.ResponseWriter, r *http.Request) {
	d, ok := s.runtime(w, r)
	if !ok {
		return
	}
	d.commandMu.Lock()
	defer d.commandMu.Unlock()
	if !s.currentDevice(d) {
		s.fail(w, 409, "connection_changed", "The machine connection changed; reload before continuing")
		return
	}
	status, err := s.verifiedStatus(r.Context(), d)
	if err != nil {
		s.deviceError(w, err)
		return
	}
	if status.Busy || status.Stopped || !status.Ready {
		s.fail(w, 409, "machine_not_idle", "Return the machine to its main menu before removing recipes")
		return
	}
	id := chi.URLParam(r, "recipe_id")
	var in struct {
		Expected *domain.MachineRecipe `json:"expected"`
	}
	if r.ContentLength != 0 {
		if !s.decode(w, r, &in) {
			return
		}
		if in.Expected == nil || in.Expected.ID != id {
			s.fail(w, 400, "recipe_id", "Include the saved recipe you selected for removal")
			return
		}
	}
	if err = d.adapter.DeleteRecipe(r.Context(), id, in.Expected); err != nil {
		s.deviceError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) currentDevice(d *runtimeDevice) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d.mu.RLock()
	defer d.mu.RUnlock()
	return s.devices[d.view.ID] == d && s.ctx.Err() == nil && d.view.Enabled && !d.identityMismatch && modelControlReady(d.view) && (d.view.Adapter != "pico-standalone" || d.view.Status != nil)
}
func (s *Server) machineSettings(w http.ResponseWriter, r *http.Request) {
	d, ok := s.runtime(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPatch {
		d.commandMu.Lock()
		defer d.commandMu.Unlock()
		if !s.currentDevice(d) {
			s.fail(w, 409, "connection_changed", "The machine connection changed; reload before continuing")
			return
		}
		var in map[string]any
		if !s.decode(w, r, &in) {
			return
		}
		if _, e := s.verifiedStatus(r.Context(), d); e != nil {
			s.deviceError(w, e)
			return
		}
		if e := d.adapter.UpdateSettings(r.Context(), in); e != nil {
			s.deviceError(w, e)
			return
		}
	}
	v, e := d.adapter.Settings(r.Context())
	if e != nil {
		s.deviceError(w, e)
		return
	}
	s.json(w, 200, v)
}

type commandReceipt struct {
	ID        string         `json:"id"`
	DeviceID  string         `json:"device_id"`
	Command   domain.Command `json:"command"`
	State     string         `json:"state"`
	At        time.Time      `json:"at"`
	Message   string         `json:"message,omitempty"`
	AIMonitor bool           `json:"ai_monitor,omitempty"`
}

func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	d, ok := s.runtime(w, r)
	if !ok {
		return
	}
	var in struct {
		domain.Command
		Confirm   bool `json:"confirm"`
		AIMonitor bool `json:"ai_monitor"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 || strings.ContainsAny(key, "/\\\r\n") {
		s.fail(w, 400, "idempotency_key", "Send a unique Idempotency-Key for each intended command")
		return
	}
	d.commandMu.Lock()
	defer d.commandMu.Unlock()
	if !s.currentDevice(d) {
		s.fail(w, 409, "connection_changed", "The machine connection changed; reload before continuing")
		return
	}
	d.mu.RLock()
	view := d.view
	d.mu.RUnlock()
	if view.Capabilities != nil {
		if descriptor, ok := view.Capabilities.CommandFields[in.Command.Command]; ok {
			for _, field := range descriptor.Allowed {
				if field == "confirm" {
					value := in.Confirm
					in.Command.Confirm = &value
				}
			}
		}
	}
	receiptID := view.ID + ":" + key
	var prior commandReceipt
	if _, e := s.db.Get("command", receiptID, &prior); e == nil {
		if !reflect.DeepEqual(prior.Command, in.Command) || prior.AIMonitor != in.AIMonitor {
			s.fail(w, 409, "idempotency_conflict", "This key belongs to a different command")
			return
		}
		s.json(w, 200, prior)
		return
	} else if !errors.Is(e, store.ErrNotFound) {
		s.internal(w, e)
		return
	}
	if view.Capabilities == nil {
		s.fail(w, 409, "discovery", "Connect to the machine before sending commands")
		return
	}
	supported := false
	for _, c := range view.Capabilities.Commands {
		if c == in.Command.Command {
			supported = true
		}
	}
	if !supported {
		s.fail(w, 400, "unsupported_command", "The machine does not advertise that command")
		return
	}
	if in.Command.Command != "encoder" && in.Command.Command != "stop" && in.Command.Command != "clear-stop" && !in.Confirm {
		s.fail(w, 400, "confirm", "Confirm that you intend to open this program")
		return
	}
	if view.Adapter == "pico-standalone" {
		status, err := s.verifiedStatus(r.Context(), d)
		if err != nil {
			s.deviceError(w, err)
			return
		}
		switch in.Command.Command {
		case "restart", "restore-starter-recipes":
			if status.Busy || status.Stopped || !status.Ready || (in.Command.Command == "restart" && status.Phase != 4) {
				s.fail(w, 409, "machine_not_idle", "Return to the main menu and unlock the idle brewer first")
				return
			}
		case "set-fan":
			if view.Model == "picostill" && in.Channel != nil && *in.Channel == 2 && in.Level != nil && (*in.Level < 61 || (in.Enabled != nil && !*in.Enabled)) {
				for _, actuator := range status.Actuators {
					if actuator.ID == "pump" && actuator.Enabled != nil && *actuator.Enabled {
						s.fail(w, 409, "pump_requires_fan", "Keep Fan 2 enabled at 61% or higher while the pump is running")
						return
					}
				}
			}
		}
	}
	var monitor aiMonitorRecord
	if in.AIMonitor {
		var err error
		monitor, err = s.prepareAIMonitor(r.Context(), d, in.Command, receiptID)
		if err != nil {
			s.deviceError(w, err)
			return
		}
	}
	// A new program or an explicit stop cancels any previous pending intent.
	// Dial input is retained because it acknowledges the same preparation flow.
	if in.Command.Command != "encoder" {
		if err := s.clearAIMonitor(view.ID, true); err != nil {
			s.internal(w, err)
			return
		}
	}
	receipt := commandReceipt{ID: receiptID, DeviceID: view.ID, Command: in.Command, State: "pending", At: time.Now().UTC(), AIMonitor: in.AIMonitor}
	if _, e := s.db.Put("command", receiptID, receipt, 0); e != nil {
		s.storeError(w, e)
		return
	}
	e := d.adapter.Command(r.Context(), in.Command)
	if e != nil {
		var remote *device.Error
		if errors.As(e, &remote) && !remote.UnknownOutcome {
			receipt.State = "rejected"
			receipt.Message = safeMessage(remote.Message)
			_, _ = s.db.Put("command", receiptID, receipt, -1)
			s.deviceError(w, e)
			return
		}
		receipt.State = "unknown"
		receipt.Message = "Delivery was not confirmed. Inspect the machine before issuing another command."
		_, _ = s.db.Put("command", receiptID, receipt, -1)
		s.fail(w, 502, "command_outcome_unknown", receipt.Message)
		return
	}
	receipt.State = "accepted"
	receipt.Message = "Queued by the machine; observe its front panel for progress and prompts."
	if _, e = s.db.Put("command", receiptID, receipt, -1); e != nil {
		s.fail(w, 500, "receipt_error", "The machine accepted the command, but its receipt could not be saved. Do not retry.")
		return
	}
	if in.AIMonitor {
		if err := s.saveAIMonitor(monitor); err != nil {
			s.fail(w, 500, "ai_monitor_receipt_error", "The machine accepted the command, but AI monitoring was not saved. Do not repeat the start command; enable AI from the live brew once its session appears.")
			return
		}
	} else if in.Command.Command != "encoder" {
		if err := s.clearAIMonitor(view.ID, false); err != nil {
			s.fail(w, 500, "ai_monitor_receipt_error", "The machine accepted the command, but AI monitoring state could not be saved. Do not repeat the command; review the live brew's monitoring setting.")
			return
		}
	}
	s.json(w, 202, receipt)
}
func (s *Server) ingestTelemetry(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SchemaVersion   int           `json:"schema_version"`
		FirmwareVersion string        `json:"firmware_version"`
		Status          domain.Status `json:"status"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	if in.SchemaVersion != 1 || in.Status.DeviceUID == "" {
		s.fail(w, 400, "schema", "Unsupported telemetry envelope")
		return
	}
	s.mu.RLock()
	var found *runtimeDevice
	for _, d := range s.devices {
		d.mu.RLock()
		match := d.view.Enabled && !d.identityMismatch && modelControlReady(d.view) && d.view.Status != nil && d.view.Status.DeviceUID == in.Status.DeviceUID
		d.mu.RUnlock()
		if match {
			found = d
			break
		}
	}
	s.mu.RUnlock()
	if found == nil {
		s.fail(w, 403, "unknown_device", "Register and connect this device first")
		return
	} // Unsigned firmware forwarding is an observation only, never control or an authority for live state.
	entry := map[string]any{"received_at": time.Now().UTC(), "status": in.Status, "firmware_version": in.FirmwareVersion, "trust": "unsigned"}
	if _, e := s.db.Put("forwarded-telemetry", in.Status.DeviceUID, entry, -1); e != nil {
		s.internal(w, e)
		return
	}
	s.json(w, 202, map[string]bool{"accepted": true})
}
