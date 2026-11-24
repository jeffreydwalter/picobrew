package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

type pairingAdapter struct {
	device.Adapter
	mu                                   sync.Mutex
	status                               domain.Status
	caps                                 domain.Capabilities
	target                               telemetryTarget
	writes, reads, discoveries, commands int
	writeErr                             error
	statusErr                            error
	statusCalls                          int
	onStatus                             func()
	applyWrite                           bool
}

func (a *pairingAdapter) Status(context.Context) (domain.Status, error) {
	a.mu.Lock()
	a.statusCalls++
	status, err, check := a.status, a.statusErr, a.onStatus
	a.mu.Unlock()
	if check != nil {
		check()
	}
	return status, err
}
func (a *pairingAdapter) Discover(context.Context) (domain.Capabilities, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.discoveries++
	return a.caps, nil
}
func (a *pairingAdapter) Telemetry(context.Context) (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reads++
	return a.target.settings(), nil
}
func (a *pairingAdapter) UpdateTelemetry(_ context.Context, v map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.writes++
	if a.applyWrite {
		target, err := readTelemetryTarget(v)
		if err != nil {
			return err
		}
		a.target = target
	}
	return a.writeErr
}
func (a *pairingAdapter) Command(context.Context, domain.Command) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.commands++
	return nil
}

type receiverTransport func(*http.Request) (*http.Response, error)

func (f receiverTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func pairingFixture(t *testing.T, model string) (*Server, *runtimeDevice, *pairingAdapter) {
	t.Helper()
	path := filepath.Join(newTestDataDir(t), "pairing.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{db: db, ctx: ctx, cancel: cancel, cfg: Config{DataDir: filepath.Dir(path), DeviceURL: "http://192.0.2.20:8080", ListenAddress: "0.0.0.0:8080", PollInterval: time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, devices: map[string]*runtimeDevice{}, telemetryProbeKey: secretToken()}
	a := &pairingAdapter{status: domain.Status{DeviceUID: strings.Repeat("a", 32), Ready: true, Command: "idle"}, caps: domain.Capabilities{APIVersion: 1, FirmwareVersion: "1.2.0", Model: "pico-s", ModelName: "Pico S", ModelFamily: "pico-s-pro", TelemetryForwarding: true, Commands: []string{"stop", "encoder"}}, target: telemetryTarget{Port: 8080}, applyWrite: true}
	d := &runtimeDevice{adapter: a, cancel: func() {}, identityMismatch: true, view: domain.DeviceView{Device: domain.Device{ID: "registered", Name: "My machine", Adapter: "pico-standalone", Model: model, Enabled: true, BaseURL: "http://192.0.2.10"}}}
	if model != "" {
		d.view.ModelSource = "manual"
	} else {
		d.view.ModelSource = "pending"
	}
	if _, err := db.Put("device", d.view.ID, d.view.Device, 0); err != nil {
		t.Fatal(err)
	}
	s.devices[d.view.ID] = d
	s.initTelemetry(d)
	s.telemetryClient = &http.Client{Transport: receiverTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		if r.URL.Host != "192.0.2.20:8080" {
			w.WriteHeader(404)
			return w.Result(), nil
		}
		s.telemetryReceiverProbe(w, r)
		return w.Result(), nil
	})}
	t.Cleanup(func() { cancel(); _ = s.db.Close() })
	return s, d, a
}
func pairedRefresh(t *testing.T, s *Server, d *runtimeDevice) {
	t.Helper()
	d.commandMu.Lock()
	defer d.commandMu.Unlock()
	if _, err := s.verifiedStatus(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	s.syncTelemetry(context.Background(), d, true)
}
func pairingRequest() *http.Request {
	return identityRequest("POST", "/api/v1/devices/registered/test", "")
}

func TestFirmwareModelReconcilesOnlyPinnedApplianceAndPreservesHistory(t *testing.T) {
	s, d, a := pairingFixture(t, "pico-pro")
	brew := domain.Brew{ID: "old-brew", DeviceID: d.view.ID, Model: "pico-pro", Notes: "original notes"}
	if _, err := s.db.Put("brew", brew.ID, brew, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.verifiedStatus(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	var saved domain.Device
	_, _ = s.db.Get("device", d.view.ID, &saved)
	if d.view.Model != "pico-s" || saved.Model != "pico-s" || saved.ModelSource != "firmware" {
		t.Fatalf("detection: %+v %+v", d.view.Device, saved)
	}
	var old domain.Brew
	_, _ = s.db.Get("brew", brew.ID, &old)
	if old.Model != brew.Model || old.Notes != brew.Notes {
		t.Fatal("model reconciliation relabeled brew history")
	}
	discoveries := a.discoveries
	a.status.DeviceUID = strings.Repeat("b", 32)
	a.caps.Model = "pico-pro"
	if _, err := s.verifiedStatus(context.Background(), d); err == nil {
		t.Fatal("accepted wrong UID")
	}
	if a.discoveries != discoveries || d.view.Model != "pico-s" || !d.identityMismatch || s.currentDevice(d) {
		t.Fatal("wrong UID influenced model or control")
	}
	a.status.DeviceUID = strings.Repeat("a", 32)
	if _, err := s.verifiedStatus(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if d.view.Model != "pico-pro" || !s.currentDevice(d) {
		t.Fatal("reconnect did not rediscover firmware build")
	}
	a.caps.Model, a.caps.ModelName, a.caps.ModelFamily = "", "", ""
	_, _ = s.verifiedStatus(context.Background(), d)
	if d.view.ModelErrorCode != "not_reported" || d.view.Capabilities == nil || d.view.Capabilities.Model != "" {
		t.Fatal("older firmware retained stale identity capabilities")
	}
}

func TestFirmwareModelMissingUnsupportedAndInvalidNeverDefaultToPro(t *testing.T) {
	for _, tc := range []struct {
		name, manual, reported, family, wantCode string
		ready, selection                         bool
	}{
		{"old unselected", "", "", "", "not_reported", false, true},
		{"old selected", "pico-s", "", "", "", true, false},
		{"unsupported", "pico-pro", "z", "z-series", "unsupported_model", false, false},
		{"invalid family", "pico-pro", "pico-s", "z", "invalid_model", false, false},
		{"incomplete", "", "", "pico-s-pro", "invalid_model", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, d, a := pairingFixture(t, tc.manual)
			a.caps.Model, a.caps.ModelName, a.caps.ModelFamily = tc.reported, "", tc.family
			_, err := s.verifiedStatus(context.Background(), d)
			if (err == nil) != tc.ready || s.currentDevice(d) != tc.ready || d.view.ModelErrorCode != tc.wantCode || d.view.ModelSelectionRequired != tc.selection {
				t.Fatalf("detection err=%v view=%+v", err, d.view)
			}
			if tc.manual == "" && d.view.Model != "" {
				t.Fatal("missing model was guessed")
			}
			if !tc.ready {
				w := httptest.NewRecorder()
				if _, ok := s.runtime(w, identityRequest("POST", "/api/v1/devices/registered/commands", "")); ok {
					t.Fatal("unsupported model activated controls")
				}
			}
			if a.writes != 0 || a.commands != 0 {
				t.Fatal("identification caused a write")
			}
		})
	}
}

func TestStatusModelMustAgreeWithDiscoveryBeforeReconciliation(t *testing.T) {
	s, d, a := pairingFixture(t, "pico-pro")
	a.status.Model, a.status.ModelFamily = "pico-pro", "pico-s-pro"
	if _, err := s.verifiedStatus(context.Background(), d); err == nil {
		t.Fatal("inconsistent status/discovery model accepted")
	}
	var saved domain.Device
	_, _ = s.db.Get("device", d.view.ID, &saved)
	if saved.Model != "pico-pro" || saved.ModelSource != "manual" || s.currentDevice(d) || d.view.ModelErrorCode != "invalid_model" || a.writes != 0 {
		t.Fatal("inconsistent identity relabeled registration or enabled control")
	}
	a.status.Model = "pico-s"
	if _, err := s.verifiedStatus(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if d.view.Model != "pico-s" || !s.currentDevice(d) {
		t.Fatal("consistent status/discovery did not recover")
	}
}

func TestPhysicalRegistrationAcceptsAutomaticAndPreservesIdentifiedEmptyEdit(t *testing.T) {
	s, h, c := newTestServer(t)
	csrf := signIn(t, c, h.URL)
	input := keyDeviceInput("Automatic machine", strings.Repeat("ab", 32))
	input["model"] = ""
	v := saveKeyDevice(t, c, h.URL+"/api/v1/devices", csrf, "", input)
	if v.Model != "" || v.ModelSource != "pending" {
		t.Fatalf("registration guessed model: %+v", v)
	}
	var saved domain.Device
	version, _ := s.db.Get("device", v.ID, &saved)
	saved.Model, saved.ModelSource = "pico-s", "firmware"
	_, _ = s.db.Put("device", v.ID, saved, version)
	s.mu.RLock()
	d := s.devices[v.ID]
	s.mu.RUnlock()
	d.mu.Lock()
	d.view.Device = saved
	d.mu.Unlock()
	input["api_key"] = ""
	v = saveKeyDevice(t, c, h.URL+"/api/v1/devices", csrf, v.ID, input)
	if v.Model != "pico-s" || v.ModelSource != "firmware" {
		t.Fatalf("empty edit erased identity: %+v", v)
	}
}

func TestAutomaticPairingClaimsOnConnectAndDoesNotFightAnotherHub(t *testing.T) {
	s, d, a := pairingFixture(t, "")
	a.target = telemetryTarget{Enabled: true, Host: "192.0.2.30", Port: 9000}
	s.requestTelemetryPairing(d, pairingRequest())
	pairedRefresh(t, s, d)
	if a.writes != 1 || a.commands != 0 || a.target.Host != "192.0.2.20" || d.view.Telemetry.State != "configured" {
		t.Fatalf("pairing writes=%d status=%+v", a.writes, d.view.Telemetry)
	}
	pairedRefresh(t, s, d)
	if a.writes != 1 {
		t.Fatal("matching settings written again")
	}
	a.target = telemetryTarget{Enabled: true, Host: "192.0.2.40", Port: 8080}
	pairedRefresh(t, s, d)
	if a.writes != 1 || d.view.Telemetry.State != "pending" {
		t.Fatal("background refresh fought another Hub")
	}
	s.requestTelemetryPairing(d, pairingRequest())
	pairedRefresh(t, s, d)
	if a.writes != 2 || a.target.Host != "192.0.2.20" {
		t.Fatal("explicit reconnect failed to reclaim pairing")
	}
}

func TestTelemetryDefersUntilIdleAndWrongUIDCannotWrite(t *testing.T) {
	s, d, a := pairingFixture(t, "")
	a.status.Busy = true
	s.requestTelemetryPairing(d, pairingRequest())
	pairedRefresh(t, s, d)
	if a.writes != 0 || d.view.Telemetry.State != "waiting_idle" {
		t.Fatal("busy machine received receiver configuration")
	}
	a.status.Busy = false
	a.status.DeviceUID = strings.Repeat("b", 32)
	s.refresh(context.Background(), d)
	if a.writes != 0 || !d.identityMismatch {
		t.Fatal("wrong UID received receiver configuration")
	}
	a.status.DeviceUID = strings.Repeat("a", 32)
	pairedRefresh(t, s, d)
	if a.writes != 1 || d.view.Telemetry.State != "configured" {
		t.Fatal("idle original machine did not pair")
	}
}

func TestTelemetryUnknownWriteIsReadBackWithoutBlindRetry(t *testing.T) {
	for _, applied := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "applied"}[applied], func(t *testing.T) {
			s, d, a := pairingFixture(t, "")
			a.writeErr = &device.Error{Code: "transport_error", UnknownOutcome: true, Message: "response lost"}
			a.applyWrite = applied
			s.requestTelemetryPairing(d, pairingRequest())
			pairedRefresh(t, s, d)
			if a.writes != 1 || a.reads < 2 {
				t.Fatal("ambiguous write not read back")
			}
			state := "outcome_unknown"
			if applied {
				state = "configured"
			}
			if d.view.Telemetry.State != state {
				t.Fatalf("state=%s", d.view.Telemetry.State)
			}
			pairedRefresh(t, s, d)
			if a.writes != 1 {
				t.Fatal("ambiguous mutation retried blindly")
			}
			if !applied {
				a.applyWrite = true
				s.requestTelemetryPairing(d, pairingRequest())
				pairedRefresh(t, s, d)
				if a.writes != 2 || d.view.Telemetry.State != "configured" {
					t.Fatal("explicit retry failed")
				}
			}
		})
	}
}

func TestTelemetryKnownFailuresStopAfterThreeCheckedAttempts(t *testing.T) {
	s, d, a := pairingFixture(t, "")
	a.applyWrite = false
	a.writeErr = &device.Error{StatusCode: 409, Code: "busy", Message: "deferred"}
	s.requestTelemetryPairing(d, pairingRequest())
	for i := 0; i < 7; i++ {
		pairedRefresh(t, s, d)
	}
	if a.writes != 3 || a.reads < 6 || d.view.Telemetry.State != "error" {
		t.Fatalf("unbounded retries writes=%d reads=%d", a.writes, a.reads)
	}
}

func TestSavedReceiverPersistsAcrossReconnectRestartAndLegacyReseed(t *testing.T) {
	s, d, a := pairingFixture(t, "")
	pairedRefresh(t, s, d)
	w := httptest.NewRecorder()
	s.machineTelemetry(w, identityRequest("PUT", "/api/v1/devices/registered/telemetry", "{\"mode\":\"manual\",\"enabled\":true,\"host\":\"custom.example\",\"port\":9123}"))
	if w.Code != 200 || a.target.Host != "custom.example" || d.view.Telemetry.Host != "custom.example" {
		t.Fatalf("manual setting: %d %s", w.Code, w.Body.String())
	}
	writes := a.writes
	s.requestTelemetryPairing(d, pairingRequest())
	pairedRefresh(t, s, d)
	if a.writes != writes || a.target.Host != "custom.example" {
		t.Fatal("reconnect replaced manual preference")
	}
	var saved domain.Device
	_, _ = s.db.Get("device", d.view.ID, &saved)
	// Reopen the persisted database, as after a Hub restart.
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(s.cfg.DataDir, "pairing.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	s.db = reopened
	a.target = telemetryTarget{Enabled: true, Host: "changed.example", Port: 8123}
	next := &runtimeDevice{adapter: a, cancel: func() {}, identityMismatch: true, view: domain.DeviceView{Device: saved}}
	s.devices[saved.ID] = next
	s.initTelemetry(next)
	pairedRefresh(t, s, next)
	if next.view.Telemetry.Host != "custom.example" || a.writes != writes+1 || a.target.Host != "custom.example" {
		t.Fatal("restored registration lost persisted override")
	}
	w = httptest.NewRecorder()
	s.machineTelemetry(w, identityRequest("GET", "/api/v1/devices/registered/telemetry", ""))
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if _, present := got["mode"]; present || got["host"] != "custom.example" {
		t.Fatal("saved override not visible")
	}
	w = httptest.NewRecorder()
	s.machineTelemetry(w, identityRequest("PUT", "/api/v1/devices/registered/telemetry", "{\"mode\":\"automatic\"}"))
	if w.Code != 200 || a.target.Host != "192.0.2.20" || next.view.Telemetry.Host != "192.0.2.20" {
		t.Fatalf("automatic return: %d %s", w.Code, w.Body.String())
	}
}

func TestTelemetryReleaseOnlyDisablesOwnedAutomaticTarget(t *testing.T) {
	for _, mode := range []string{"owned", "other", "custom"} {
		t.Run(mode, func(t *testing.T) {
			s, d, a := pairingFixture(t, "")
			pairedRefresh(t, s, d)
			if mode == "other" {
				a.target.Host = "192.0.2.99"
			}
			if mode == "custom" {
				record, _ := s.readTelemetryRecord(d.view.ID)
				before := record
				target := telemetryTarget{Enabled: true, Host: "custom.example", Port: 8123}
				record.Desired, record.Owned = &target, target
				a.target = target
				_ = s.writeTelemetryRecord(d.view.ID, before, record)
			}
			writes := a.writes
			d.commandMu.Lock()
			state := s.releaseTelemetry(context.Background(), d)
			d.commandMu.Unlock()
			if state.State != "disabled" {
				t.Fatal(state)
			}
			if mode == "owned" {
				if a.writes != writes+1 || a.target.Enabled {
					t.Fatal("owned receiver not disabled")
				}
			} else if a.writes != writes || !a.target.Enabled {
				t.Fatal("unowned/manual receiver changed")
			}
		})
	}
}

func TestUnavailableRegistrationCanReleaseWithoutUnsafeWrite(t *testing.T) {
	s, d, a := pairingFixture(t, "")
	pairedRefresh(t, s, d)
	writes := a.writes
	d.adapter = nil
	d.commandMu.Lock()
	state := s.releaseTelemetry(context.Background(), d)
	d.commandMu.Unlock()
	if a.writes != writes || state.State != "disabled" || !strings.Contains(state.Message, "unavailable") {
		t.Fatal("missing credentials caused unsafe or misleading cleanup")
	}
}

func TestReceiverProofRejectsForeignHostAndForwardingHeaders(t *testing.T) {
	s, d, a := pairingFixture(t, "")
	s.cfg.DeviceURL = ""
	s.cfg.ListenAddress = "127.0.0.1:8080"
	r := identityRequest("POST", "http://foreign.example:8080/api/v1/devices/registered/test", "")
	r.Header.Set("Forwarded", "host=192.0.2.20:8080")
	r.Header.Set("X-Forwarded-Host", "192.0.2.20:8080")
	s.requestTelemetryPairing(d, r)
	pairedRefresh(t, s, d)
	if a.writes != 0 || d.view.Telemetry.State != "needs_hub_url" {
		t.Fatal("foreign or forwarded host used as receiver")
	}
	r = identityRequest("POST", "http://192.0.2.20:8080/api/v1/devices/registered/test", "")
	s.requestTelemetryPairing(d, r)
	pairedRefresh(t, s, d)
	if a.writes != 1 || a.target.Host != "192.0.2.20" {
		t.Fatal("verified reverse-proxy host did not pair")
	}
}

func TestReceiverURLsRejectMachineLoopbackAndUnsupportedTransport(t *testing.T) {
	for _, value := range []string{"http://127.0.0.1:8080", "http://localhost:8080", "http://[::1]:8080", "http://0.0.0.0:8080", "http://169.254.169.254", "http://100.100.100.200", "https://hub.example", "http://user:secret@hub.example", "http://hub.example/path", "http://hub.example?x=1", "http://hub.example:0"} {
		if _, err := parseReceiverURL(value); err == nil {
			t.Errorf("accepted %s", value)
		}
	}
	for _, value := range []string{"http://192.168.1.20:8080", "http://nas.local:9123"} {
		if _, err := parseReceiverURL(value); err != nil {
			t.Errorf("rejected %s: %v", value, err)
		}
	}
}

func TestSimulatedMachineNeverPerformsReceiverNetworkRequests(t *testing.T) {
	s, d, _ := pairingFixture(t, "")
	d.view.Adapter = "simulator"
	d.view.Model = "pico-pro"
	d.view.ModelSource = "manual"
	d.identityMismatch = false
	d.adapter = device.NewSimulator(d.view.ID, d.view.Model)
	s.telemetryClient = &http.Client{Transport: receiverTransport(func(*http.Request) (*http.Response, error) {
		t.Error("simulator used receiver network")
		return nil, errors.New("network forbidden")
	})}
	s.initTelemetry(d)
	s.requestTelemetryPairing(d, pairingRequest())
	s.refresh(context.Background(), d)
	if d.view.Telemetry.State != "simulator" || !d.view.Online {
		t.Fatalf("simulation status: %+v", d.view)
	}
}

func TestConcurrentFirmwareRefreshAndFleetReads(t *testing.T) {
	s, d, _ := pairingFixture(t, "")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.refresh(context.Background(), d); _ = s.fleet() }()
	}
	wg.Wait()
	if d.view.Model != "pico-s" || !d.view.Online {
		t.Fatal("concurrent identification lost model")
	}
}

func TestAddPicoFermProvisionsBrowserReceiverBeforeFirstPoll(t *testing.T) {
	for _, host := range []string{"brewery.example", "192.0.2.23"} {
		t.Run(host, func(t *testing.T) {
			s, h, client := newTestServer(t)
			csrf := signIn(t, client, h.URL)
			target := telemetryTarget{Enabled: true, Host: host, Port: 8181}
			a := &pairingAdapter{
				Adapter: device.NewSimulator("unused", "pico-s"),
				caps: domain.Capabilities{Model: "picoferm", ModelFamily: "picoferm",
					APIVersion: 1, FirmwareVersion: "1.0.0", TelemetryForwarding: true},
				status: domain.Status{Model: "picoferm", ModelFamily: "picoferm",
					DeviceUID: "fermentation-monitor", Ready: true},
				applyWrite: true, target: telemetryTarget{Port: 8080},
			}
			s.newDeviceAdapter = func(_, _ string) (device.Adapter, error) { return a, nil }
			origin := host + ":8181"
			s.telemetryClient = &http.Client{Transport: receiverTransport(func(r *http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				if r.URL.Host == origin {
					s.telemetryReceiverProbe(w, r)
				} else {
					w.WriteHeader(404)
				}
				return w.Result(), nil
			})}
			client.Transport = receiverTransport(func(r *http.Request) (*http.Response, error) {
				r = r.Clone(r.Context())
				r.Host = origin
				return http.DefaultTransport.RoundTrip(r)
			})
			in := keyDeviceInput("Keg monitor", strings.Repeat("ab", 32))
			in["enabled"], in["model"] = true, ""
			added := saveKeyDevice(t, client, h.URL+"/api/v1/devices", csrf, "", in)
			deadline := time.Now().Add(4 * time.Second)
			for {
				record, err := s.readTelemetryRecord(added.ID)
				if err != nil {
					t.Fatal(err)
				}
				if record.Owned.sameConfig(target) && record.Desired != nil && record.Pending == nil {
					if !record.Desired.sameConfig(target) || record.Origin != "http://"+origin {
						t.Fatalf("incorrect paired destination: %+v", record)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("first poll did not confirm the PicoFerm destination: %+v", record)
				}
				time.Sleep(10 * time.Millisecond)
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.writes != 1 || a.commands != 0 || !a.target.sameConfig(target) {
				t.Fatalf("pairing wrote %d settings, issued %d commands, destination %+v", a.writes, a.commands, a.target)
			}
		})
	}
}
