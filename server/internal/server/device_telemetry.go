package server

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

type telemetryRecord struct {
	Trial         *receiverTrial   `json:"receiver_trial,omitempty"`
	RecoveryAfter time.Time        `json:"recovery_after,omitzero"`
	SchemaVersion int              `json:"schema_version"`
	FollowHub     bool             `json:"follow_hub,omitempty"`
	Desired       *telemetryTarget `json:"desired,omitempty"`
	Owned         telemetryTarget  `json:"owned"`
	HubReceiver   *telemetryTarget `json:"hub_receiver,omitempty"`
	Origin        string           `json:"origin,omitempty"`
	Apply         bool             `json:"apply"`
	RetryPending  bool             `json:"retry_pending,omitempty"`
	Pending       *telemetryTarget `json:"pending,omitempty"`

	// Read compatibility for records saved before desired settings became the
	// sole source of configuration. Migration clears these fields.
	Mode   string          `json:"mode,omitempty"`
	Manual telemetryTarget `json:"manual,omitzero"`
	Claim  bool            `json:"claim,omitempty"`
}

func (r telemetryRecord) desiredTarget() telemetryTarget {
	if r.Desired != nil {
		return *r.Desired
	}
	return telemetryTarget{Port: 8080}
}

func (s *Server) readTelemetryRecord(id string) (telemetryRecord, error) {
	for attempt := 0; attempt < 3; attempt++ {
		var record telemetryRecord
		version, err := s.db.Get("device-telemetry", id, &record)
		if errors.Is(err, store.ErrNotFound) {
			return telemetryRecord{SchemaVersion: 2}, nil
		}
		if err != nil {
			return record, err
		}
		if record.SchemaVersion < 2 {
			// Presence matters: a saved disabled value is never replaced with
			// an enabled default. An in-flight legacy target takes precedence
			// over an older observed automatic target.
			if record.Desired == nil {
				if record.Mode == "manual" {
					target := record.Manual
					if err := device.ValidateTelemetry(target.settings()); err != nil {
						return record, err
					}
					record.Desired = &target
				} else if record.Pending != nil {
					target := *record.Pending
					if err := device.ValidateTelemetry(target.settings()); err != nil {
						return record, err
					}
					// Legacy automatic mode always desired enabled=true.
					// A disabled pending/owned value recorded monitor cleanup.
					target.Enabled = true
					record.Desired, record.HubReceiver = &target, &target
				} else if record.Owned.Host != "" {
					target := record.Owned
					if err := device.ValidateTelemetry(target.settings()); err != nil {
						return record, err
					}
					target.Enabled = true
					record.Desired, record.HubReceiver = &target, &target
				}
			}
			record.FollowHub = record.Mode == "automatic" && record.HubReceiver != nil
			record.SchemaVersion = 2
			record.Apply = record.Apply || record.Claim || record.Desired != nil
			record.Mode, record.Manual, record.Claim = "", telemetryTarget{}, false
			if _, err := s.db.Put("device-telemetry", id, record, version); errors.Is(err, store.ErrConflict) {
				continue
			} else if err != nil {
				return record, err
			}
		}
		if record.Desired != nil {
			if err := device.ValidateTelemetry(record.Desired.settings()); err != nil {
				return record, err
			}
		}
		return record, nil
	}
	return telemetryRecord{}, store.ErrConflict
}

func (s *Server) writeTelemetryRecord(id string, before, after telemetryRecord) error {
	if reflect.DeepEqual(before, after) {
		return nil
	}
	after.SchemaVersion = 2
	_, err := s.db.Put("device-telemetry", id, after, -1)
	return err
}

func telemetryStatus(state, message string, target telemetryTarget) domain.TelemetryStatus {
	now := time.Now().UTC()
	return domain.TelemetryStatus{Enabled: target.Enabled, Host: target.Host, Port: target.Port, State: state, Message: message, LastChecked: &now}
}

func (s *Server) telemetryState(d *runtimeDevice, state, message string, target telemetryTarget) {
	d.mu.Lock()
	d.view.Telemetry = telemetryStatus(state, message, target)
	d.mu.Unlock()
}

func (s *Server) initTelemetry(d *runtimeDevice) {
	if d.view.Adapter == "simulator" {
		d.view.Telemetry = telemetryStatus("simulator", "Simulation stays local and does not send network telemetry.", telemetryTarget{Port: 8080})
		return
	}
	record, err := s.readTelemetryRecord(d.view.ID)
	if err == nil {
		before := record
		record.Apply = true // A Hub restart reconnects the saved configuration.
		err = s.writeTelemetryRecord(d.view.ID, before, record)
	}
	state, message := "pending", "Saved receiver settings will be sent when the machine connects."
	if record.Desired == nil {
		message = "The Hub receiver address will be filled in when available."
	}
	if err != nil {
		state, message = "error", "The saved receiver settings could not be read."
	}
	if !d.view.Enabled {
		state, message = "disabled", "Monitoring is disabled. Saved receiver settings are retained."
	}
	d.view.Telemetry = telemetryStatus(state, message, record.desiredTarget())
}

// connectionStillRegistered allows saving settings while disabled or offline,
// but rejects a handler that waited on a replaced connection's command lock.
func (s *Server) connectionStillRegistered(d *runtimeDevice) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d.mu.RLock()
	defer d.mu.RUnlock()
	return s.devices[d.view.ID] == d && s.ctx.Err() == nil
}

// queueTelemetryApply runs under commandMu. Only an explicit owner action can
// authorize retrying an ambiguous mutation; a reconnect first observes it.
func (s *Server) queueTelemetryApply(d *runtimeDevice, explicit bool, origin string) error {
	d.mu.RLock()
	id, physical := d.view.ID, d.view.Adapter == "pico-standalone"
	d.mu.RUnlock()
	if !physical {
		return nil
	}
	record, err := s.readTelemetryRecord(id)
	if err != nil {
		return err
	}
	before := record
	record.Apply = true
	record.RetryPending = record.RetryPending || explicit
	if record.Desired == nil && origin != "" {
		record.Origin = origin
	}
	if err := s.writeTelemetryRecord(id, before, record); err != nil {
		return err
	}
	d.telemetryAttempts, d.telemetryNext = 0, time.Time{}
	s.telemetryState(d, "pending", "Saved receiver settings will be sent when the machine is connected and idle.", record.desiredTarget())
	return nil
}

func (s *Server) requestTelemetryPairing(d *runtimeDevice, r *http.Request) {
	d.commandMu.Lock()
	defer d.commandMu.Unlock()
	if !s.connectionStillRegistered(d) {
		return
	}
	if err := s.queueTelemetryApply(d, true, requestReceiverOrigin(r)); err != nil {
		d.mu.RLock()
		target := telemetryTarget{Enabled: d.view.Telemetry.Enabled, Host: d.view.Telemetry.Host, Port: d.view.Telemetry.Port}
		d.mu.RUnlock()
		s.telemetryState(d, "error", "The connection request could not be saved.", target)
	}
}

// seedTelemetryDesired chooses a verified default exactly once. It never
// changes an existing preference.
func (s *Server) seedTelemetryDesired(ctx context.Context, d *runtimeDevice, record *telemetryRecord) (string, error) {
	if record.Desired != nil {
		return "", nil
	}
	d.mu.RLock()
	id, baseURL := d.view.ID, d.view.BaseURL
	d.mu.RUnlock()
	target, reason := s.automaticReceiver(ctx, baseURL, record.Origin)
	if reason != "" {
		return reason, nil
	}
	before := *record
	record.Desired, record.HubReceiver, record.Apply, record.FollowHub = &target, &target, true, true
	return "", s.writeTelemetryRecord(id, before, *record)
}

func readTelemetryTarget(value map[string]any) (telemetryTarget, error) {
	enabled, a := value["enabled"].(bool)
	host, b := value["host"].(string)
	var port int
	switch n := value["port"].(type) {
	case int:
		port = n
	case float64:
		if n != float64(int(n)) {
			return telemetryTarget{}, errors.New("invalid telemetry port")
		}
		port = int(n)
	}
	target := telemetryTarget{Enabled: enabled, Host: host, Port: port}
	if !a || !b || device.ValidateTelemetry(target.settings()) != nil {
		return telemetryTarget{}, errors.New("invalid telemetry configuration")
	}
	return target, nil
}

func (s *Server) observedTelemetry(ctx context.Context, d *runtimeDevice) (telemetryTarget, error) {
	value, err := d.adapter.Telemetry(ctx)
	if err != nil {
		return telemetryTarget{}, err
	}
	return readTelemetryTarget(value)
}

func (s *Server) telemetryApplied(d *runtimeDevice, target telemetryTarget) {
	message := "Saved receiver settings are applied to the machine."
	if !target.Enabled {
		message = "Sending live data to the saved receiver is disabled on the machine."
	}
	s.telemetryState(d, "configured", message, target)
}

// syncTelemetry runs under commandMu. The Hub's saved desired triple is the
// only configuration source. Polling may observe drift, but only connection
// events and owner saves authorize applying it again. A PicoFerm may also
// recover a previously verified, automatically selected Hub receiver.
func (s *Server) syncTelemetry(ctx context.Context, d *runtimeDevice, force bool) {
	d.mu.Lock()
	view, reconnected := d.view, d.telemetryReconnect
	d.telemetryReconnect = false
	d.mu.Unlock()
	if view.Adapter != "pico-standalone" || !s.connectionStillRegistered(d) {
		return
	}
	if reconnected {
		if err := s.queueTelemetryApply(d, false, ""); err != nil {
			s.telemetryState(d, "error", "The reconnect could not be recorded; no receiver change was sent.", telemetryTarget{Enabled: view.Telemetry.Enabled, Host: view.Telemetry.Host, Port: view.Telemetry.Port})
			return
		}
	}
	if !force && time.Now().Before(d.telemetryNext) {
		return
	}
	d.telemetryNext = time.Now().Add(time.Minute)
	record, err := s.readTelemetryRecord(view.ID)
	if err != nil {
		s.telemetryState(d, "error", "The saved receiver settings could not be read.", telemetryTarget{Port: 8080})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	reason, err := s.seedTelemetryDesired(ctx, d, &record)
	if err != nil {
		s.telemetryState(d, "error", "The initial receiver settings could not be saved.", record.desiredTarget())
		return
	}
	if reason != "" {
		s.telemetryState(d, "needs_hub_url", reason, record.desiredTarget())
		return
	}
	desired := record.desiredTarget()
	if !view.Enabled || !view.Online || d.adapter == nil || !s.currentDevice(d) {
		s.telemetryState(d, "pending", "Saved in the Hub. These settings will be sent after the registered machine connects.", desired)
		return
	}
	if view.Capabilities == nil || !view.Capabilities.TelemetryForwarding {
		s.telemetryState(d, "unsupported", "Settings are saved in the Hub. This firmware does not support sending live data; live readings and history remain available.", desired)
		return
	}
	status, err := s.verifiedStatus(ctx, d)
	if err != nil || !s.currentDevice(d) {
		s.telemetryState(d, "pending", "Settings are saved and will be sent after the registered machine identity is verified.", desired)
		return
	}
	raw, err := d.adapter.Telemetry(ctx)
	var observed telemetryTarget
	if err == nil {
		observed, err = readTelemetryTarget(raw)
	}
	if err != nil {
		s.telemetryState(d, "pending", "Saved in the Hub. The machine's receiver settings could not be read, so no change was sent.", desired)
		return
	}
	if record.Pending != nil {
		before := record
		if observed.sameConfig(*record.Pending) || observed.sameConfig(desired) {
			record.Owned, record.Pending = observed, nil
		} else if record.RetryPending {
			// A fresh read plus a new explicit owner request allows a checked
			// attempt. Reconnects alone never retry an ambiguous write.
			record.Pending, record.RetryPending = nil, false
		} else {
			s.telemetryState(d, "outcome_unknown", "The previous receiver change was not confirmed. Save these settings again to authorize another checked attempt.", desired)
			return
		}
		if err := s.writeTelemetryRecord(view.ID, before, record); err != nil {
			s.telemetryState(d, "error", "The receiver readback could not be recorded; no further change was sent.", desired)
			return
		}
	}
	// On a registered PicoFerm, an automatically chosen address follows the Hub
	// only after this exact configured receiver has actually failed delivery.
	// Explicit saves and older preferences without that provenance stay fixed.
	isManaged := view.IsPicoFerm() && record.FollowHub && desired.Enabled && record.HubReceiver != nil && desired.sameConfig(*record.HubReceiver)
	if record.Trial != nil {
		if isManaged && record.Pending == nil {
			s.checkReceiverTrial(ctx, d, record, status, observed, raw)
		} else {
			s.telemetryState(d, "pending", "Save receiver settings to resolve the previous PicoFerm receiver change.", desired)
		}
		return
	}

	if isManaged && !record.RetryPending && (record.Owned.Enabled || observed.Enabled || observed.Host != "") &&
		!observed.sameConfig(desired) && !observed.sameConfig(record.Owned) {
		d.telemetryDelivery = deliveryWindow{}
		s.telemetryState(d, "pending", "The PicoFerm receiver was changed on the machine. Save the receiver settings to apply them again.", desired)
		return
	}
	if isManaged && record.Pending == nil && record.Owned.sameConfig(desired) && observed.sameConfig(desired) {
		if d.telemetryDelivery.failing(desired, raw, status.UptimeMS) && !time.Now().Before(record.RecoveryAfter) {
			if status.Busy || status.Stopped || !status.Ready {
				s.telemetryState(d, "waiting_idle", "The PicoFerm could not reach its saved Hub address. Receiver recovery will run when it is idle.", desired)
				return
			}
			replacement, reason := s.findReceiver(ctx, view.BaseURL, record.Origin, &desired)
			if reason != "" {
				s.telemetryState(d, "pending", "The PicoFerm could not reach its saved Hub address. It will keep trying while the Hub looks for a reachable address.", desired)
				return
			}
			s.startReceiverTrial(ctx, d, record, replacement)
			return
		}
	} else {
		d.telemetryDelivery = deliveryWindow{}
	}

	if observed.sameConfig(desired) {
		before := record
		record.Owned, record.Apply, record.RetryPending = desired, false, false
		if err := s.writeTelemetryRecord(view.ID, before, record); err != nil {
			s.telemetryState(d, "error", "The settings match, but their readback could not be saved.", desired)
			return
		}
		d.telemetryAttempts = 0
		s.telemetryApplied(d, desired)
		return
	}
	if !record.Apply {
		s.telemetryState(d, "pending", "Saved in the Hub. These settings will be applied the next time this machine connects, or when you save them again.", desired)
		return
	}
	if d.telemetryAttempts >= 3 {
		s.telemetryState(d, "error", "Settings are saved. Applying them paused after repeated failures; reconnect or save again to retry.", desired)
		return
	}
	if status.Busy || status.Stopped || !status.Ready {
		d.telemetryNext = time.Now().Add(s.cfg.PollInterval)
		s.telemetryState(d, "waiting_idle", "Saved in the Hub. These settings will be sent when the machine returns to its main menu.", desired)
		return
	}
	before := record
	record.Pending, record.Apply, record.RetryPending = &desired, false, false
	if err := s.writeTelemetryRecord(view.ID, before, record); err != nil {
		s.telemetryState(d, "error", "The receiver change could not be recorded, so no change was sent.", desired)
		return
	}
	d.telemetryAttempts++
	writeErr := d.adapter.UpdateTelemetry(ctx, desired.settings())
	readback, readErr := s.observedTelemetry(ctx, d)
	if readErr == nil && readback.sameConfig(desired) {
		before = record
		record.Owned, record.Pending = desired, nil
		if err := s.writeTelemetryRecord(view.ID, before, record); err != nil {
			s.telemetryState(d, "error", "Settings were verified on the machine but their readback could not be saved.", desired)
			return
		}
		d.telemetryAttempts = 0
		s.telemetryApplied(d, desired)
		return
	}
	var remote *device.Error
	if errors.As(writeErr, &remote) && !remote.UnknownOutcome {
		before = record
		record.Pending, record.Apply = nil, true
		if err := s.writeTelemetryRecord(view.ID, before, record); err != nil {
			s.telemetryState(d, "error", "The rejected receiver change could not be recorded; save again before retrying.", desired)
			return
		}
		s.telemetryState(d, "error", "The machine rejected the saved receiver settings. The Hub will make at most three checked attempts.", desired)
		return
	}
	s.telemetryState(d, "outcome_unknown", "Settings are saved, but the machine did not confirm the change. The Hub will read them back; save again to authorize another checked attempt.", desired)
}

func (s *Server) machineTelemetry(w http.ResponseWriter, r *http.Request) {
	d, ok := s.lookupRuntime(w, r)
	if !ok {
		return
	}
	d.commandMu.Lock()
	defer d.commandMu.Unlock()
	if !s.connectionStillRegistered(d) {
		s.fail(w, 409, "connection_changed", "The machine connection changed; reload before continuing")
		return
	}
	d.mu.RLock()
	view := d.view
	d.mu.RUnlock()
	record, err := s.readTelemetryRecord(view.ID)
	if err != nil {
		s.internal(w, err)
		return
	}
	if r.Method == http.MethodGet {
		if view.Adapter == "pico-standalone" && record.Desired == nil {
			record.Origin = requestReceiverOrigin(r)
			reason, err := s.seedTelemetryDesired(r.Context(), d, &record)
			if err != nil {
				s.internal(w, err)
				return
			}
			if reason != "" {
				s.telemetryState(d, "needs_hub_url", reason, record.desiredTarget())
			} else {
				s.telemetryState(d, "pending", "Receiver settings are saved and will be sent when the machine connects.", record.desiredTarget())
				d.telemetryNext = time.Time{}
			}
		}
		d.mu.RLock()
		result := d.view.Telemetry
		d.mu.RUnlock()
		target := record.desiredTarget()
		result.Enabled, result.Host, result.Port = target.Enabled, target.Host, target.Port
		s.json(w, 200, result)
		return
	}
	if view.Adapter != "pico-standalone" {
		s.fail(w, 409, "simulator", "Simulation stays local and has no network receiver")
		return
	}
	var in struct {
		Mode    string  `json:"mode"`
		Enabled *bool   `json:"enabled"`
		Host    *string `json:"host"`
		Port    *int    `json:"port"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	var desired telemetryTarget
	var verifiedHub bool
	switch in.Mode {
	case "", "manual":
		if in.Enabled == nil || in.Host == nil || in.Port == nil {
			s.fail(w, 400, "telemetry", "Receiver settings require enabled, host and port")
			return
		}
		desired = telemetryTarget{Enabled: *in.Enabled, Host: *in.Host, Port: *in.Port}
		if err := device.ValidateTelemetry(desired.settings()); err != nil {
			s.deviceError(w, err)
			return
		}
	case "automatic":
		// Select this Hub as the receiver. PicoFerm can recover this address if it moves.
		if in.Enabled != nil || in.Host != nil || in.Port != nil {
			s.fail(w, 400, "telemetry", "Send only mode to fill in the Hub receiver address")
			return
		}
		var reason string
		desired, reason = s.automaticReceiver(r.Context(), view.BaseURL, requestReceiverOrigin(r))
		if reason != "" {
			s.fail(w, 409, "receiver_address", reason)
			return
		}
		verifiedHub = true
	default:
		s.fail(w, 400, "telemetry", "Save enabled, host and port")
		return
	}
	before := record
	record.Trial, record.RecoveryAfter = nil, time.Time{}
	record.Desired, record.Apply, record.RetryPending, record.FollowHub = &desired, true, true, verifiedHub
	if verifiedHub {
		record.HubReceiver = &desired
	}
	// Saving the Hub's desired settings never depends on an online appliance.
	// No device I/O occurs before this durable write succeeds.
	if err := s.writeTelemetryRecord(view.ID, before, record); err != nil {
		s.internal(w, err)
		return
	}
	d.telemetryAttempts, d.telemetryNext = 0, time.Time{}
	s.telemetryState(d, "pending", "Saved in the Hub. These settings will be sent when the machine is connected and idle.", desired)
	s.syncTelemetry(r.Context(), d, true)
	d.mu.RLock()
	result := d.view.Telemetry
	d.mu.RUnlock()
	s.json(w, 200, result)
}

// Disable only a receiver previously verified as this Hub and subsequently
// applied by it. Desired settings remain intact for the next connection.
// Custom external destinations are not disabled when monitoring is removed.
func (s *Server) releaseTelemetry(ctx context.Context, d *runtimeDevice) domain.TelemetryStatus {
	d.mu.RLock()
	view := d.view
	d.mu.RUnlock()
	record, err := s.readTelemetryRecord(view.ID)
	result := telemetryStatus("disabled", "Monitoring is disabled. Saved receiver settings are retained.", record.desiredTarget())
	if view.Adapter != "pico-standalone" {
		return result
	}
	if err != nil {
		result.Message = "Monitoring is disabled; the previous receiver settings could not be checked."
		return result
	}
	if record.HubReceiver == nil || record.HubReceiver.Host == "" {
		return result
	}
	if record.Owned.Host == "" && (record.Pending == nil || record.Pending.Host == "") {
		return result
	}
	if d.adapter == nil {
		result.Message = "Monitoring is disabled; the machine connection is unavailable, so its previous Hub receiver could not be checked."
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status, err := s.verifiedStatus(ctx, d)
	if err != nil || status.Busy || status.Stopped || !status.Ready {
		result.Message = "Monitoring is disabled; the machine could not confirm that its previous Hub receiver was disabled."
		return result
	}
	observed, err := s.observedTelemetry(ctx, d)
	if err != nil {
		result.Message = "Monitoring is disabled; the previous Hub receiver could not be read."
		return result
	}
	owned := observed.sameReceiver(record.Owned) || record.Pending != nil && observed.sameReceiver(*record.Pending)
	trial := record.Trial != nil && observed.sameConfig(record.Trial.Target)
	if !trial && (!owned || !observed.sameReceiver(*record.HubReceiver)) {
		return result
	}
	if !observed.Enabled {
		return result
	}
	disabled := observed
	disabled.Enabled = false
	before := record
	record.Trial = nil
	record.Pending, record.Apply, record.RetryPending = &disabled, false, false
	if err := s.writeTelemetryRecord(view.ID, before, record); err != nil {
		result.Message = "Monitoring is disabled; the machine receiver could not safely be changed."
		return result
	}
	_ = d.adapter.UpdateTelemetry(ctx, disabled.settings())
	readback, err := s.observedTelemetry(ctx, d)
	if err == nil && readback.sameConfig(disabled) {
		before = record
		record.Owned, record.Pending = disabled, nil
		if err := s.writeTelemetryRecord(view.ID, before, record); err == nil {
			result.Message = "Monitoring and this Hub's machine receiver are disabled. Saved settings are retained."
		} else {
			result.Message = "The machine receiver is disabled, but the cleanup result could not be saved."
		}
	} else {
		result.Message = "Monitoring is disabled; the machine did not confirm that its previous Hub receiver was disabled."
	}
	return result
}
