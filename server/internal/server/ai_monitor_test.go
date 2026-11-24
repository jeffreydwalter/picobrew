package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/ai"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

type aiBoundaryAdapter struct {
	device.Adapter
	commands      chan []byte
	uid           atomic.Value
	queued        atomic.Bool
	statusCommand atomic.Value
}

func (a *aiBoundaryAdapter) Command(ctx context.Context, command domain.Command) error {
	data, err := json.Marshal(command)
	if err != nil {
		return err
	}
	a.commands <- data
	return a.Adapter.Command(ctx, command)
}

func (a *aiBoundaryAdapter) Status(ctx context.Context) (domain.Status, error) {
	status, err := a.Adapter.Status(ctx)
	if uid := a.uid.Load(); uid != nil {
		status.DeviceUID = uid.(string)
	}
	if a.queued.Load() {
		status.Busy, status.Command = true, "idle"
	} else if command := a.statusCommand.Load(); command != nil && command.(string) != "" {
		status.Command = command.(string)
	}
	return status, err
}

func aiMonitorFixture(t *testing.T) (*Server, *http.Client, string, string, *countedAdapter, *atomic.Int32) {
	t.Helper()
	s, h, client := newTestServer(t)
	csrf := signIn(t, client, h.URL)
	boundary := &aiBoundaryAdapter{Adapter: device.NewSimulator("fresh-ai-brew", "pico-pro"), commands: make(chan []byte, 100)}
	adapter := &countedAdapter{Adapter: boundary}
	installTestDevice(t, s, "machine", adapter)
	calls := &atomic.Int32{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var wire map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
		}
		if _, ok := wire["tools"]; ok {
			t.Error("AI acquired a command tool")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "Advice for the recorded session only."}}}})
	}))
	t.Cleanup(provider.Close)
	s.aiClient = ai.New()
	s.aiClient.OpenAIURL = provider.URL
	code, body := request(t, client, "PUT", h.URL+"/api/v1/ai/settings", csrf, map[string]any{"provider": "openai", "model": "test-model", "api_key": "test-provider-key", "enabled": true, "monitor_enabled": true}, nil)
	if code != 200 {
		t.Fatalf("AI configuration: %d %s", code, body)
	}
	return s, client, h.URL, csrf, adapter, calls
}

func readAIMonitor(t *testing.T, client *http.Client, base, csrf string) aiMonitorStatus {
	t.Helper()
	code, body := request(t, client, "GET", base+"/api/v1/devices/machine/ai-monitor", csrf, nil, nil)
	var status aiMonitorStatus
	if code != 200 || json.Unmarshal(body, &status) != nil {
		t.Fatalf("AI monitor status: %d %s", code, body)
	}
	return status
}

func aiStartCommand(t *testing.T, client *http.Client, base, csrf string, command string, monitor bool) (int, []byte) {
	t.Helper()
	input := map[string]any{"command": command, "confirm": true, "ai_monitor": monitor}
	if command == "brew" {
		input["recipe_id"] = "studio-porter"
	}
	return request(t, client, "POST", base+"/api/v1/devices/machine/commands", csrf, input, map[string]string{"Idempotency-Key": "test-" + randomID()})
}

func physicalBrew(t *testing.T, adapter device.Adapter, command string) {
	t.Helper()
	start := domain.Command{Command: command}
	if command == "brew" {
		start.RecipeID = "studio-porter"
	}
	for _, input := range []domain.Command{{Command: "stop"}, {Command: "clear-stop"}, start} {
		if err := adapter.Command(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAIStartConsentBindsOnceAndNeverStartsOrControlsTheBrewer(t *testing.T) {
	s, client, base, csrf, adapter, calls := aiMonitorFixture(t)
	initial := readAIMonitor(t, client, base, csrf)
	if initial.Enabled || initial.State != "off" || !initial.Configured || initial.CurrentBrewID != "" {
		t.Fatalf("initial monitor: %+v", initial)
	}
	code, body := aiStartCommand(t, client, base, csrf, "brew", true)
	if code != 202 {
		t.Fatalf("confirmed start: %d %s", code, body)
	}
	forwarded := <-adapter.Adapter.(*aiBoundaryAdapter).commands
	if !bytes.Equal(forwarded, []byte(`{"command":"brew","recipe_id":"studio-porter"}`)) {
		t.Fatalf("Hub-only confirmation/AI fields reached the device protocol: %s", forwarded)
	}
	// Native local_control reports busy while its command queue is pending,
	// before command_name changes from idle to brew.
	boundary := adapter.Adapter.(*aiBoundaryAdapter)
	boundary.queued.Store(true)
	pending := readAIMonitor(t, client, base, csrf)
	if !pending.Enabled || pending.State != "pending" || pending.CurrentBrewID != "" || pending.ExpiresAt == nil || adapter.calls.Load() != 1 {
		t.Fatalf("pending consent did not wait for the firmware session: %+v", pending)
	}
	s.monitorTick(context.Background())
	if calls.Load() != 0 || adapter.calls.Load() != 1 {
		t.Fatal("pending consent requested advice or issued a machine command")
	}
	boundary.queued.Store(false)
	active := readAIMonitor(t, client, base, csrf)
	if active.State != "active" || active.BrewID == "" || active.BrewID != active.CurrentBrewID || active.ExpiresAt != nil {
		t.Fatalf("consent did not bind to the actual history session: %+v", active)
	}
	s.monitorTick(context.Background())
	s.monitorTick(context.Background())
	if calls.Load() != 1 || adapter.calls.Load() != 1 {
		t.Fatalf("monitor rate/control boundary: provider=%d commands=%d", calls.Load(), adapter.calls.Load())
	}
	// A subsequent physical brew can replace the current one between Hub polls.
	physicalBrew(t, adapter, "brew")
	commandCount := adapter.calls.Load()
	s.monitorTick(context.Background())
	finished := readAIMonitor(t, client, base, csrf)
	if finished.Enabled || finished.State != "finished" || finished.BrewID != active.BrewID || finished.CurrentBrewID == active.BrewID || calls.Load() != 1 || adapter.calls.Load() != commandCount {
		t.Fatalf("consent leaked into a subsequent session: %+v", finished)
	}
	code, body = request(t, client, "GET", base+"/api/v1/brews/"+active.BrewID+"/export?format=json", csrf, nil, nil)
	var archived domain.BrewDetail
	if code != 200 || json.Unmarshal(body, &archived) != nil || len(archived.AIAdvice) != 1 || archived.AIAdvice[0].BrewID != active.BrewID || archived.AIAdvice[0].DeviceID != "machine" {
		t.Fatalf("session advice not saved with the brew export: %d %s", code, body)
	}
}

func TestAIPendingConsentCannotSurviveFailureExpiryCancellationOrAmbiguousSessions(t *testing.T) {
	for _, condition := range []string{"rejected", "unknown", "expiry", "queue-timeout", "hub-restart", "stop", "maintenance", "two-new-sessions", "front-panel-start"} {
		t.Run(condition, func(t *testing.T) {
			s, client, base, csrf, adapter, calls := aiMonitorFixture(t)
			if condition == "rejected" {
				adapter.failure = &device.Error{StatusCode: 409, Code: "busy", Message: "Start rejected"}
			} else if condition == "unknown" {
				adapter.failure = context.DeadlineExceeded
			}
			code, body := aiStartCommand(t, client, base, csrf, "brew", true)
			if condition == "rejected" || condition == "unknown" {
				if code != 409 && code != 502 {
					t.Fatalf("failed start: %d %s", code, body)
				}
				adapter.failure = nil
				physicalBrew(t, adapter, "brew")
			} else {
				if code != 202 {
					t.Fatalf("start: %d %s", code, body)
				}
				switch condition {
				case "expiry":
					record, _ := s.loadAIMonitor("machine")
					record.ExpiresAt = time.Now().Add(-time.Second)
					if err := s.saveAIMonitor(record); err != nil {
						t.Fatal(err)
					}
				case "queue-timeout":
					record, _ := s.loadAIMonitor("machine")
					record.RequestedAt = time.Now().Add(-10 * time.Second)
					if err := s.saveAIMonitor(record); err != nil {
						t.Fatal(err)
					}
					adapter.Adapter.(*aiBoundaryAdapter).queued.Store(true)
				case "hub-restart":
					if err := s.recoverAIMonitor(); err != nil {
						t.Fatal(err)
					}
				case "stop":
					_, _ = aiStartCommand(t, client, base, csrf, "stop", false)
					physicalBrew(t, adapter, "brew")
				case "maintenance":
					physicalBrew(t, adapter, "rinse")
				case "two-new-sessions":
					physicalBrew(t, adapter, "brew")
				case "front-panel-start":
					adapter.Adapter.(*aiBoundaryAdapter).statusCommand.Store("front-panel")
				}
			}
			s.monitorTick(context.Background())
			status := readAIMonitor(t, client, base, csrf)
			if status.Enabled || status.State != "off" || calls.Load() != 0 {
				t.Fatalf("%s left stale consent: %+v provider=%d", condition, status, calls.Load())
			}
		})
	}
}

func TestAIExplicitLiveOptInAcceptsVerifiedFrontPanelBrewHistory(t *testing.T) {
	s, client, base, csrf, adapter, calls := aiMonitorFixture(t)
	physicalBrew(t, adapter, "brew")
	adapter.Adapter.(*aiBoundaryAdapter).statusCommand.Store("front-panel")
	current := readAIMonitor(t, client, base, csrf)
	if current.CurrentBrewID == "" || current.Enabled {
		t.Fatalf("physical brew was unavailable or automatically monitored: %+v", current)
	}
	code, body := request(t, client, "PUT", base+"/api/v1/devices/machine/ai-monitor", csrf, map[string]any{"enabled": true, "brew_id": current.CurrentBrewID}, nil)
	if code != 200 {
		t.Fatalf("explicit physical-brew consent: %d %s", code, body)
	}
	before := adapter.calls.Load()
	s.monitorTick(context.Background())
	if calls.Load() != 1 || adapter.calls.Load() != before {
		t.Fatalf("physical-brew monitoring failed or issued a command: provider=%d commands=%d", calls.Load(), adapter.calls.Load())
	}
}

func TestAIMonitorRequiresExplicitCurrentBrewAndPreservesCommandConfirmation(t *testing.T) {
	s, client, base, csrf, adapter, calls := aiMonitorFixture(t)
	code, body := request(t, client, "POST", base+"/api/v1/devices/machine/commands", csrf, map[string]any{"command": "brew", "recipe_id": "studio-porter", "ai_monitor": true}, map[string]string{"Idempotency-Key": "unconfirmed-start"})
	if code != 400 || adapter.calls.Load() != 0 {
		t.Fatalf("AI bypassed start confirmation: %d %s", code, body)
	}
	code, body = aiStartCommand(t, client, base, csrf, "rinse", true)
	if code != 400 || adapter.calls.Load() != 0 {
		t.Fatalf("AI was armed for maintenance: %d %s", code, body)
	}
	physicalBrew(t, adapter, "brew")
	current := readAIMonitor(t, client, base, csrf)
	for _, id := range []string{"", "machine-old", "other-machine-1"} {
		code, body = request(t, client, "PUT", base+"/api/v1/devices/machine/ai-monitor", csrf, map[string]any{"enabled": true, "brew_id": id}, nil)
		if code != 409 {
			t.Fatalf("unbound or stale session enabled: %s %d %s", id, code, body)
		}
	}
	s.monitorTick(context.Background())
	if calls.Load() != 0 {
		t.Fatal("a physical start inherited monitoring from the global setting")
	}
	code, body = request(t, client, "PUT", base+"/api/v1/devices/machine/ai-monitor", csrf, map[string]any{"enabled": true, "brew_id": current.CurrentBrewID}, nil)
	if code != 200 {
		t.Fatalf("explicit live consent: %d %s", code, body)
	}
	s.monitorTick(context.Background())
	code, _ = request(t, client, "PUT", base+"/api/v1/devices/machine/ai-monitor", csrf, map[string]any{"enabled": false}, nil)
	if code != 200 {
		t.Fatal("disable failed")
	}
	s.monitorTick(context.Background())
	code, _ = request(t, client, "PUT", base+"/api/v1/devices/machine/ai-monitor", csrf, map[string]any{"enabled": true, "brew_id": current.CurrentBrewID}, nil)
	if code != 200 {
		t.Fatal("re-enable failed")
	}
	s.monitorTick(context.Background())
	if calls.Load() != 1 {
		t.Fatal("toggling consent bypassed the persisted five-minute bound")
	}
}

func TestAIActiveConsentSurvivesRejectedCommandsButNeverChangesSession(t *testing.T) {
	s, client, base, csrf, adapter, _ := aiMonitorFixture(t)
	physicalBrew(t, adapter, "brew")
	current := readAIMonitor(t, client, base, csrf)
	code, body := request(t, client, "PUT", base+"/api/v1/devices/machine/ai-monitor", csrf, map[string]any{"enabled": true, "brew_id": current.CurrentBrewID}, nil)
	if code != 200 {
		t.Fatalf("enable: %d %s", code, body)
	}
	code, _ = aiStartCommand(t, client, base, csrf, "rinse", false)
	if code != 409 {
		t.Fatal("busy brew did not reject a different program")
	}
	after := readAIMonitor(t, client, base, csrf)
	if after.State != "active" || !after.Enabled || after.BrewID != current.CurrentBrewID {
		t.Fatalf("rejected program changed active consent: %+v", after)
	}
	adapter.failure = context.DeadlineExceeded
	code, _ = aiStartCommand(t, client, base, csrf, "stop", false)
	if code != 502 {
		t.Fatal("uncertain stop did not retain its unknown outcome")
	}
	adapter.failure = nil
	after = readAIMonitor(t, client, base, csrf)
	if after.State != "active" || after.BrewID != current.CurrentBrewID {
		t.Fatal("uncertain command moved the existing exact-session consent")
	}
	physicalBrew(t, adapter, "brew")
	s.monitorTick(context.Background())
	if after = readAIMonitor(t, client, base, csrf); after.Enabled || after.State != "finished" {
		t.Fatalf("later session inherited consent after uncertain command: %+v", after)
	}
}

func TestAIMonitorAndOneShotLiveAdviceRejectChangedPhysicalIdentity(t *testing.T) {
	s, client, base, csrf, adapter, calls := aiMonitorFixture(t)
	physicalBrew(t, adapter, "brew")
	boundary := adapter.Adapter.(*aiBoundaryAdapter)
	boundary.uid.Store(strings.Repeat("a", 32))
	s.mu.RLock()
	d := s.devices["machine"]
	s.mu.RUnlock()
	d.mu.Lock()
	d.view.Adapter = "pico-standalone"
	d.mu.Unlock()
	current := readAIMonitor(t, client, base, csrf)
	code, body := request(t, client, "PUT", base+"/api/v1/devices/machine/ai-monitor", csrf, map[string]any{"enabled": true, "brew_id": current.CurrentBrewID}, nil)
	if code != 200 {
		t.Fatalf("enable: %d %s", code, body)
	}
	boundary.uid.Store(strings.Repeat("b", 32))
	s.monitorTick(context.Background())
	code, body = request(t, client, "POST", base+"/api/v1/ai/assist", csrf, ai.Request{Mode: "live", DeviceID: "machine", BrewID: current.CurrentBrewID, Prompt: "Review"}, nil)
	if code != 409 || calls.Load() != 0 {
		t.Fatalf("another appliance's data reached the provider: %d %s calls=%d", code, body, calls.Load())
	}
}

type changingAIHistory struct {
	device.Adapter
	change atomic.Bool
}

type blockingAIHistory struct {
	device.Adapter
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (a *blockingAIHistory) HistoryPage(ctx context.Context, id uint64, offset int) ([]domain.Sample, *int, error) {
	a.once.Do(func() { close(a.entered) })
	select {
	case <-a.release:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	return a.Adapter.HistoryPage(ctx, id, offset)
}

func TestAIHistoryDownloadDoesNotBlockStopAndWithdrawnConsentSkipsProvider(t *testing.T) {
	s, client, base, csrf, adapter, calls := aiMonitorFixture(t)
	physicalBrew(t, adapter, "brew")
	current := readAIMonitor(t, client, base, csrf)
	code, body := request(t, client, "PUT", base+"/api/v1/devices/machine/ai-monitor", csrf, map[string]any{"enabled": true, "brew_id": current.CurrentBrewID}, nil)
	if code != 200 {
		t.Fatalf("enable: %d %s", code, body)
	}
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	blocked := &blockingAIHistory{Adapter: adapter, entered: make(chan struct{}), release: release}
	installTestDevice(t, s, "machine", blocked)
	done := make(chan struct{})
	go func() { s.monitorTick(context.Background()); close(done) }()
	select {
	case <-blocked.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not enter the history read")
	}
	stopped := make(chan int, 1)
	go func() {
		code, _ := aiStartCommand(t, client, base, csrf, "stop", false)
		stopped <- code
	}()
	select {
	case code := <-stopped:
		if code != 202 {
			t.Fatalf("stop during AI history download: %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AI history pagination held the machine command lock")
	}
	unblock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not finish after the history read")
	}
	if calls.Load() != 0 {
		t.Fatal("provider was called after the stopped brew withdrew monitoring consent")
	}
}

func (a *changingAIHistory) HistoryPage(ctx context.Context, id uint64, offset int) ([]domain.Sample, *int, error) {
	samples, next, err := a.Adapter.HistoryPage(ctx, id, offset)
	if a.change.Swap(false) {
		for _, command := range []domain.Command{{Command: "stop"}, {Command: "clear-stop"}, {Command: "brew", RecipeID: "weekend-pale"}} {
			if e := a.Adapter.Command(ctx, command); e != nil {
				return nil, nil, e
			}
		}
	}
	return samples, next, err
}

func TestAILiveAdviceRejectsStaleAndChangingNativeHistoryBeforeProviderCall(t *testing.T) {
	s, client, base, csrf, adapter, calls := aiMonitorFixture(t)
	physicalBrew(t, adapter, "brew")
	current := readAIMonitor(t, client, base, csrf)
	for _, id := range []string{"", "old-session"} {
		code, body := request(t, client, "POST", base+"/api/v1/ai/assist", csrf, ai.Request{Mode: "live", DeviceID: "machine", BrewID: id, Prompt: "Review this brew"}, nil)
		if code != 400 && code != 409 {
			t.Fatalf("stale live context accepted: %d %s", code, body)
		}
	}
	changing := &changingAIHistory{Adapter: adapter}
	changing.change.Store(true)
	installTestDevice(t, s, "machine", changing)
	code, body := request(t, client, "POST", base+"/api/v1/ai/assist", csrf, ai.Request{Mode: "live", DeviceID: "machine", BrewID: current.CurrentBrewID, Prompt: "Review this brew"}, nil)
	if code != 409 || calls.Load() != 0 {
		t.Fatalf("mixed history reached provider: %d calls=%d %s", code, calls.Load(), body)
	}
}

func TestAIAnalysisAndAdviceRemainBoundToArchivedBrewAfterDeviceRemoval(t *testing.T) {
	s, client, base, csrf, _, calls := aiMonitorFixture(t)
	brew := domain.Brew{ID: "archived-1", DeviceID: "removed-machine", DeviceHistoryID: 1, State: "completed", Model: "pico-pro"}
	if _, err := s.db.Put("brew", brew.ID, brew, 0); err != nil {
		t.Fatal(err)
	}
	code, body := request(t, client, "POST", base+"/api/v1/ai/assist", csrf, ai.Request{Mode: "analysis", BrewID: brew.ID, DeviceID: "machine", Prompt: "Review"}, nil)
	if code != 409 || calls.Load() != 0 {
		t.Fatalf("cross-device analysis accepted: %d %s", code, body)
	}
	code, body = request(t, client, "POST", base+"/api/v1/ai/assist", csrf, ai.Request{Mode: "analysis", BrewID: brew.ID, Prompt: "Review"}, nil)
	if code != 200 || calls.Load() != 1 {
		t.Fatalf("removed machine's archived brew was not analyzable: %d %s", code, body)
	}
	s.recordAdvice(ai.Request{Mode: "live", BrewID: brew.ID, DeviceID: brew.DeviceID}, ai.Result{Text: "Earlier live observation"}, nil)
	s.recordAdvice(ai.Request{Mode: "analysis", BrewID: "another-brew", DeviceID: brew.DeviceID}, ai.Result{Text: "Other brew"}, nil)
	code, body = request(t, client, "GET", base+"/api/v1/ai/advice?brew_id="+brew.ID+"&device_id="+brew.DeviceID+"&mode=analysis", csrf, nil, nil)
	var advice []adviceRecord
	if code != 200 || json.Unmarshal(body, &advice) != nil || len(advice) != 1 || advice[0].BrewID != brew.ID || advice[0].DeviceID != brew.DeviceID || advice[0].Mode != "analysis" {
		t.Fatalf("advice filters mixed contexts: %d %s", code, body)
	}
	code, body = request(t, client, "GET", base+"/api/v1/ai/advice?brew_id="+brew.ID+"&device_id=machine", csrf, nil, nil)
	if code != 200 || json.Unmarshal(body, &advice) != nil || len(advice) != 0 {
		t.Fatalf("filters were not a strict conjunction: %d %s", code, body)
	}
	code, body = request(t, client, "GET", base+"/api/v1/brews/"+brew.ID+"/export?format=json", csrf, nil, nil)
	var exported domain.BrewDetail
	if code != 200 || json.Unmarshal(body, &exported) != nil || len(exported.AIAdvice) != 2 {
		t.Fatalf("brew export lost its live/analysis advice: %d %s", code, body)
	}
}

func TestAIBrewAdviceRetentionIsIndependentOfUnboundLogAndErrors(t *testing.T) {
	s, _, _ := newTestServer(t)
	before := time.Now().Add(-time.Hour)
	put := func(id, brewID, status string, when time.Time) {
		t.Helper()
		value := adviceRecord{ID: id, BrewID: brewID, DeviceID: "machine", Mode: "live", Status: status, Text: id, CreatedAt: when}
		if _, err := s.db.Put("ai-advice", id, value, 0); err != nil {
			t.Fatal(err)
		}
	}
	put("old-brew-advice", "old-brew", "complete", before)
	for i := 0; i < 501; i++ {
		put(fmt.Sprintf("unbound-%d", i), "", "complete", before.Add(time.Duration(i)*time.Second))
	}
	s.recordAdvice(ai.Request{Mode: "recipe"}, ai.Result{Text: "A new recipe discussion"}, nil)
	retained, err := s.adviceForBrew("old-brew")
	if err != nil || len(retained) != 1 {
		t.Fatal("unbound log retention deleted archived brew advice", err)
	}
	for i := 0; i < 201; i++ {
		put(fmt.Sprintf("brew-advice-%d", i), "bounded-brew", "complete", before.Add(time.Duration(i)*time.Second))
	}
	for i := 0; i < 21; i++ {
		put(fmt.Sprintf("brew-error-%d", i), "bounded-brew", "error", before.Add(time.Duration(i)*time.Second))
	}
	input := ai.Request{Mode: "live", BrewID: "bounded-brew", DeviceID: "machine"}
	s.recordAdvice(input, ai.Result{Text: strings.Repeat("é", 40000)}, nil)
	s.recordAdvice(input, ai.Result{}, errors.New("provider unavailable"))
	retained, err = s.adviceForBrew("bounded-brew")
	if err != nil || len(retained) != 220 {
		t.Fatalf("per-brew bound: %d %v", len(retained), err)
	}
	successes, failures := 0, 0
	for _, record := range retained {
		if len(record.Text) > 65536 {
			t.Fatal("stored advice exceeded its size bound")
		}
		if record.Status == "error" {
			failures++
		} else {
			successes++
		}
	}
	if successes != 200 || failures != 20 {
		t.Fatalf("errors evicted useful advice: successes=%d errors=%d", successes, failures)
	}
}
