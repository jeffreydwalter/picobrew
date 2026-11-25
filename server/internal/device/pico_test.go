package device

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testPico(t *testing.T, handler http.HandlerFunc) *pico {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Error("missing device authentication")
		}
		if len(r.TransferEncoding) != 0 || !r.Close {
			t.Errorf("unsupported framing: transfer=%v close=%v", r.TransferEncoding, r.Close)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if int64(len(body)) != r.ContentLength {
			t.Errorf("Content-Length=%d, bytes=%d", r.ContentLength, len(body))
		}
		if len(body) > 0 && r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing JSON Content-Type")
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	p, err := newPico(server.URL, testKey, true)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func replyJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func capabilitiesReply(w http.ResponseWriter, allowed ...string) {
	replyJSON(w, 200, map[string]any{"api_version": 1, "mode": "standalone", "firmware_version": "1.0.1", "commands": allowed, "recipe_capacity": 16, "step_capacity": 15, "front_panel": true, "history_persistent": true, "telemetry_forwarding": true})
}

func nativeRecipe() domain.MachineRecipe {
	return domain.MachineRecipe{ID: "Ale_01", Name: "Test Ale", Steps: []domain.MachineStep{{Name: "Mash", Location: 1, TargetF: 152, Minutes: 60, DrainMinutes: 2}}}
}

func deviceFailure(t *testing.T, err error) *Error {
	t.Helper()
	var result *Error
	if !errors.As(err, &result) {
		t.Fatalf("expected device error, got %v", err)
	}
	return result
}

func TestProductionEndpointValidation(t *testing.T) {
	for _, address := range []string{"http://192.168.1.50", "http://10.0.1.143:80/", "https://brewer.local", "http://[fd00::1234]:8080"} {
		if _, err := NewPico(address, testKey); err != nil {
			t.Errorf("valid address %q: %v", address, err)
		}
	}
	for _, address := range []string{"ftp://brewer.local", "http://user:pass@brewer.local", "http://brewer.local/api", "http://brewer.local?secret=1", "http://brewer.local?", "http://brewer.local/#fragment", "http://127.0.0.1", "http://[::1]", "http://localhost", "http://sub.localhost", "http://169.254.169.254", "http://100.100.100.200", "http://0.0.0.0", "http://224.0.0.1", "http://brewer.local:0", "http://brewer.local:65536"} {
		if _, err := NewPico(address, testKey); err == nil {
			t.Errorf("accepted prohibited endpoint %q", address)
		}
	}
	for _, key := range []string{"", "short", strings.Repeat("x", 64), testKey + "\n"} {
		if _, err := NewPico("http://192.168.1.50", key); err == nil {
			t.Error("accepted invalid API key")
		}
	}
}

func TestRecipeWriteReadbackAndSerialization(t *testing.T) {
	recipe := nativeRecipe()
	entered, release := make(chan struct{}), make(chan struct{})
	var orderMu sync.Mutex
	var order []string
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		orderMu.Lock()
		order = append(order, r.Method+" "+r.URL.Path)
		orderMu.Unlock()
		switch {
		case r.Method == "PUT":
			var sent domain.MachineRecipe
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil || !reflect.DeepEqual(sent, recipe) || r.URL.Path != "/api/v1/recipes/Ale_01" {
				t.Errorf("recipe payload differs: %+v, %v", sent, err)
			}
			close(entered)
			<-release
			replyJSON(w, 201, recipe)
		case r.URL.Path == "/api/v1/recipes/Ale_01":
			replyJSON(w, 200, recipe)
		default:
			replyJSON(w, 200, domain.Status{Ready: true})
		}
	})
	writeDone, readDone := make(chan error, 1), make(chan error, 1)
	go func() { writeDone <- p.PutRecipe(context.Background(), recipe, nil) }()
	<-entered
	go func() { _, err := p.Status(context.Background()); readDone <- err }()
	close(release)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	want := []string{"PUT /api/v1/recipes/Ale_01", "GET /api/v1/recipes/Ale_01", "GET /api/v1/status"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("write verification was interleaved: %v", order)
	}
}

func TestRecipeReadbackMismatchIsUnknown(t *testing.T) {
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			replyJSON(w, 200, map[string]bool{"saved": true})
			return
		}
		other := nativeRecipe()
		other.Steps[0].Minutes++
		replyJSON(w, 200, other)
	})
	err := deviceFailure(t, p.PutRecipe(context.Background(), nativeRecipe(), nil))
	if !err.UnknownOutcome || err.Code != "verification_failed" {
		t.Fatalf("incorrect verification result: %+v", err)
	}
}

func TestCommandsDiscoverCapabilitiesAndEncodeExactly(t *testing.T) {
	var count atomic.Int32
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.URL.Path == "/api/v1" {
			capabilitiesReply(w, "encoder", "stop")
			return
		}
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/api/v1/commands" || string(body) != `{"command":"encoder","delta":-1,"click":true}` {
			t.Errorf("unexpected command: %s %s %s", r.Method, r.URL.Path, body)
		}
		replyJSON(w, 202, map[string]any{"accepted": true, "command": "encoder"})
	})
	if err := p.Command(context.Background(), domain.Command{Command: "encoder", Delta: -1, Click: true}); err != nil {
		t.Fatal(err)
	}
	if err := p.Command(context.Background(), domain.Command{Command: "brew", RecipeID: "Ale_01"}); deviceFailure(t, err).Code != "unsupported_command" {
		t.Fatal(err)
	}
	for _, command := range []domain.Command{{Command: "pause"}, {Command: "stop", RecipeID: "Ale_01"}, {Command: "encoder"}, {Command: "encoder", Delta: 101}} {
		if err := p.Command(context.Background(), command); err == nil {
			t.Errorf("accepted invalid command: %+v", command)
		}
	}
	if count.Load() != 2 {
		t.Fatalf("invalid or unadvertised commands reached the device: %d requests", count.Load())
	}
}

func TestDefiniteRejectionAndUnknownOutcomeNeverRetry(t *testing.T) {
	for _, status := range []int{400, 401, 409, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var count atomic.Int32
			p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				replyJSON(w, status, map[string]string{"error": "busy", "message": "Cannot change this setting"})
			})
			err := deviceFailure(t, p.UpdateSettings(context.Background(), map[string]any{"temperature_unit": "C"}))
			if err.StatusCode != status || err.UnknownOutcome != (status == 503) || count.Load() != 1 {
				t.Fatalf("unexpected error or retry: %+v; calls=%d", err, count.Load())
			}
		})
	}
	t.Run("lost response", func(t *testing.T) {
		var calls atomic.Int32
		p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			connection, _, _ := w.(http.Hijacker).Hijack()
			_ = connection.Close()
		})
		err := deviceFailure(t, p.DeleteRecipe(context.Background(), "Ale_01", nil))
		if !err.UnknownOutcome || calls.Load() != 1 {
			t.Fatalf("lost mutation result was retried or treated as certain: %+v", err)
		}
	})
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var calls atomic.Int32
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "/other")
		w.WriteHeader(302)
	})
	_, err := p.Status(context.Background())
	if deviceFailure(t, err).StatusCode != 302 || calls.Load() != 1 {
		t.Fatalf("redirect was followed: %d requests, %v", calls.Load(), err)
	}
}

func TestConcurrentCallsAndQueuedCancellation(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var active, maximum, calls atomic.Int32
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for previous := maximum.Load(); current > previous && !maximum.CompareAndSwap(previous, current); previous = maximum.Load() {
		}
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			<-release
		}
		replyJSON(w, 200, domain.Status{Ready: true})
	})
	first := make(chan error, 1)
	go func() { _, err := p.Status(context.Background()); first <- err }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() { _, err := p.Status(ctx); queued <- err }()
	cancel()
	if err := deviceFailure(t, <-queued); err.Code != "cancelled" || err.UnknownOutcome {
		t.Fatal(err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Status(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maximum.Load() != 1 || calls.Load() != 21 {
		t.Fatalf("serialization/cancellation failed: concurrent=%d calls=%d", maximum.Load(), calls.Load())
	}
	if p.client.Timeout != 3*time.Second {
		t.Error("physical request timeout changed")
	}
}

func TestReadBoundsAndCredentialRedaction(t *testing.T) {
	t.Run("bounded body", func(t *testing.T) {
		p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", maxResponseBytes+1))
		})
		_, err := p.Status(context.Background())
		if deviceFailure(t, err).Code != "invalid_response" {
			t.Fatal(err)
		}
	})
	t.Run("redaction", func(t *testing.T) {
		p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
			replyJSON(w, 503, map[string]string{"error": strings.ToUpper(testKey), "message": strings.ToUpper(testKey) + " " + r.Host + "\n" + strings.Repeat("🔥", 200)})
		})
		err := deviceFailure(t, p.UpdateSettings(context.Background(), map[string]any{"temperature_unit": "F"}))
		if strings.Contains(strings.ToLower(err.Error()), testKey) || strings.Contains(err.Error(), p.base.Host) || len(err.Message) > 256 || !utf8.ValidString(err.Message) || strings.Contains(err.Message, "\n") {
			t.Fatalf("unsanitized device error: %+v", err)
		}
	})
}

func TestRecipeInventoryAndDelete(t *testing.T) {
	var paths []string
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Method == "DELETE" {
			w.WriteHeader(204)
		} else if r.URL.Path == "/api/v1/recipes" {
			replyJSON(w, 200, map[string]any{"recipes": []map[string]any{{"id": "Ale_01", "name": "Test Ale", "steps": 1}}, "capacity": 16})
		} else {
			replyJSON(w, 200, nativeRecipe())
		}
	})
	recipes, err := p.Recipes(context.Background())
	if err != nil || !reflect.DeepEqual(recipes, []domain.MachineRecipe{nativeRecipe()}) {
		t.Fatalf("recipe inventory: %+v %v", recipes, err)
	}
	if err := p.DeleteRecipe(context.Background(), "Ale_01", nil); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteRecipe(context.Background(), "../other", nil); err == nil {
		t.Fatal("accepted path traversal")
	}
	want := []string{"GET /api/v1/recipes", "GET /api/v1/recipes/Ale_01", "DELETE /api/v1/recipes/Ale_01"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatal(paths)
	}
}

func TestHistoryPageDecodesIdentityNullsAndContinuation(t *testing.T) {
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/api/v1/history/samples/32?id=42" {
			t.Errorf("incorrect history request: %s", r.URL.RequestURI())
		}
		replyJSON(w, 200, map[string]any{"id": 42, "offset": 32, "next_offset": 33, "samples": [][]any{{480000, nil, 163.4, 152, -1, 2, 3, 4, 255, 27, 9}}})
	})
	rows, next, err := p.HistoryPage(context.Background(), 42, 32)
	if err != nil || len(rows) != 1 || next == nil || *next != 33 {
		t.Fatalf("history response: %+v %v %v", rows, next, err)
	}
	row := rows[0]
	if row.ElapsedMS != 480000 || row.WortF != nil || *row.ThermoblockF != 163.4 || row.StepSeconds != -1 || row.CurrentStep != 255 || row.Flags != 27 || row.Source != "device" || row.ObservedAt != nil {
		t.Fatalf("history field mapping: %+v", row)
	}
}

func TestHistoryRejectsInconsistentPages(t *testing.T) {
	for name, body := range map[string]string{
		"wrong ID":           `{"id":43,"offset":0,"next_offset":null,"samples":[]}`,
		"wrong offset":       `{"id":42,"offset":1,"next_offset":null,"samples":[]}`,
		"empty continuation": `{"id":42,"offset":0,"next_offset":0,"samples":[]}`,
		"wrong count":        `{"id":42,"offset":0,"next_offset":null,"samples":[[0]]}`,
		"invalid time":       `{"id":42,"offset":0,"next_offset":null,"samples":[[-1,72,76,0,0,1,0,0,0,11,0]]}`,
		"invalid byte":       `{"id":42,"offset":0,"next_offset":null,"samples":[[0,72,76,0,0,256,0,0,0,11,0]]}`,
		"fractional integer": `{"id":42,"offset":0,"next_offset":null,"samples":[[0,72,76,0,0.1,1,0,0,0,11,0]]}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := testPico(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) })
			_, _, err := p.HistoryPage(context.Background(), 42, 0)
			if deviceFailure(t, err).Code != "invalid_response" {
				t.Fatal(err)
			}
		})
	}
}

func TestTelemetryValidationMatchesFirmwareHostRules(t *testing.T) {
	for _, host := range []string{"brewbox.local", "10.0.1.10", "010.000.001.010", "brew-box", "abc123"} {
		if err := validateTelemetry(map[string]any{"enabled": true, "host": host, "port": 8080}); err != nil {
			t.Errorf("valid telemetry host %q: %v", host, err)
		}
	}
	for _, host := range []string{"", "http://brewbox.local", "brewbox.local:8080", "brewbox/path", "bad_host", "bad;host", "-host", "host-", "host.", "host..local", "123", "256.1.2.3", "0.0.0.0", "255.255.255.255"} {
		if err := validateTelemetry(map[string]any{"enabled": true, "host": host, "port": 8080}); err == nil {
			t.Errorf("accepted invalid telemetry host %q", host)
		}
	}
}

func TestRecipeCreateNegotiatesAndNeverRetriesRejectedWrites(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, collision := range []bool{false, true} {
			t.Run(fmt.Sprintf("modern=%t/existing=%t", modern, collision), func(t *testing.T) {
				recipe := nativeRecipe()
				calls := []string{}
				p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
					calls = append(calls, r.Method+" "+r.URL.Path)
					if r.URL.Path == "/api/v1" {
						caps := map[string]any{"api_version": 1, "mode": "standalone", "recipe_capacity": 16}
						if modern {
							caps["recipe_create_only"] = true
						}
						replyJSON(w, 200, caps)
						return
					}
					if r.URL.Path != "/api/v1/recipes/"+recipe.ID {
						t.Errorf("wrong path %s", r.URL.Path)
					}
					switch r.Method {
					case http.MethodGet:
						if len(calls) == 2 && !collision {
							replyJSON(w, 404, map[string]any{"error": "not_found", "message": "Recipe not found"})
						} else {
							replyJSON(w, 200, recipe)
						}
					case http.MethodPut:
						var sent map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
							t.Error(err)
						}
						raw, specified := sent["create_only"]
						if modern != specified || specified && string(raw) != "true" {
							t.Errorf("wrong create-only payload %s", raw)
						}
						if collision {
							replyJSON(w, 409, map[string]any{"error": "recipe_exists", "message": "ID exists"})
						} else {
							replyJSON(w, 201, recipe)
						}
					}
				})
				err := p.CreateRecipe(context.Background(), recipe)
				if collision {
					failure := deviceFailure(t, err)
					if failure.Code != "recipe_exists" || failure.UnknownOutcome {
						t.Fatal(failure)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				methods := []string{}
				for _, call := range calls {
					method, _, _ := strings.Cut(call, " ")
					methods = append(methods, method)
				}
				want := []string{"GET"}
				if !modern {
					want = append(want, "GET")
				}
				if modern || !collision {
					want = append(want, "PUT")
				}
				if !collision {
					want = append(want, "GET")
				}
				if !reflect.DeepEqual(methods, want) {
					t.Fatal(calls, want)
				}
			})
		}
	}
}

func TestSimulatorCreateRecipePreservesExistingProgram(t *testing.T) {
	ctx := context.Background()
	sim := NewSimulator("create-test", "pico-pro")
	caps, err := sim.Discover(ctx)
	if err != nil || !caps.RecipeCreateOnly {
		t.Fatal(caps, err)
	}
	original := nativeRecipe()
	if err = sim.CreateRecipe(ctx, original); err != nil {
		t.Fatal(err)
	}
	collision := original
	collision.Name = "Unrelated replacement"
	if failure := deviceFailure(t, sim.CreateRecipe(ctx, collision)); failure.Code != "recipe_exists" {
		t.Fatal(failure)
	}
	items, err := sim.Recipes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == original.ID && reflect.DeepEqual(item, original) {
			return
		}
	}
	t.Fatalf("original missing from inventory %+v", items)
}

func TestZProgramWriteRequiresLiveCapabilityAndExactReadback(t *testing.T) {
	const program = `{"Name":"Pale","IsBeer":true,"UsePicoPakMode":false,"Steps":[{"Name":"Adjunct 4","StepType":1,"Location":6,"Time":47,"Drain":4,"Temp":64.25},{"Name":"Sample","StepType":2}]}`
	var z domain.ZProgram
	if err := json.Unmarshal([]byte(program), &z); err != nil {
		t.Fatal(err)
	}
	for _, capable := range []bool{false, true} {
		writes := 0
		p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1" {
				replyJSON(w, 200, map[string]any{"mode": "standalone", "api_version": 1, "model": "z-series", "model_family": "z-series", "recipe_capacity": 16, "step_capacity": 15, "recipe_create_only": true, "z_program_maker": capable})
				return
			}
			if r.Method == "PUT" {
				writes++
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body) != 3 || string(body["create_only"]) != "true" || string(body["id"]) != "\"local\"" {
					t.Errorf("wrong Z wire body %s", body)
				}
				var sent domain.ZProgram
				if err := json.Unmarshal(body["z_program"], &sent); err != nil || !reflect.DeepEqual(sent, z) {
					t.Errorf("Z program changed %v %v", sent, err)
				}
				replyJSON(w, 201, map[string]any{})
				return
			}
			replyJSON(w, 200, map[string]any{"id": "local", "z_program": z})
		})
		err := p.CreateRecipe(context.Background(), domain.MachineRecipe{ID: "local", Name: "Pale", ZProgram: &z})
		if capable && (err != nil || writes != 1) || !capable && (err == nil || writes != 0) {
			t.Fatal(capable, writes, err)
		}
	}
}

func TestLongZProgramChecksLiveFormatCapacity(t *testing.T) {
	flow, hold, drain := 6, 86400, 7200
	label := strings.Repeat("&", 20)
	program := domain.ZProgram{Name: label, IsBeer: true}
	for range domain.MaxZProgramSteps {
		program.Steps = append(program.Steps, domain.ZProgramStep{Name: label, Type: 1, Location: &flow, Temp: json.RawMessage("67.12345678901234"), Time: &hold, Drain: &drain})
	}
	recipe := domain.MachineRecipe{ID: "long", Name: label, ZProgram: &program}
	for _, limit := range []int{0, 19, domain.MaxZProgramSteps} {
		writes := 0
		p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/v1":
				caps := domain.Capabilities{APIVersion: 1, Model: "z-series", ModelFamily: "z-series", RecipeCapacity: 16, StepCapacity: 15, ZProgramStepCapacity: limit, RecipeCreateOnly: true, ZProgramMaker: true}
				replyJSON(w, 200, struct {
					domain.Capabilities
					Mode string `json:"mode"`
				}{caps, "standalone"})
			case r.Method == "PUT":
				writes++
				var body struct {
					Program domain.ZProgram `json:"z_program"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body.Program, program) || r.ContentLength > maxRequestBytes {
					t.Errorf("20-step program changed or exceeded the request limit: %v", err)
				}
				replyJSON(w, 201, map[string]any{})
			default:
				replyJSON(w, 200, map[string]any{"id": recipe.ID, "z_program": program})
			}
		})
		err := p.CreateRecipe(context.Background(), recipe)
		if limit < len(program.Steps) {
			if deviceFailure(t, err).Code != "recipe_step_capacity" || writes != 0 {
				t.Fatalf("wrote to incompatible device (limit %d): %v", limit, err)
			}
		} else if err != nil || writes != 1 {
			t.Fatalf("20-step write and readback failed: %v", err)
		}
	}
}

func TestDedicatedBrewerServiceOperations(t *testing.T) {
	for _, tc := range []struct {
		name, flag, path, body, response string
		code                             int
	}{
		{"restart", "system_restart", "/api/v1/system/restart", "{}", `{"accepted":true}`, 202},
		{"restore-starter-recipes", "restore_default_recipes", "/api/v1/recipes/restore-defaults", "", `{"added":2,"preserved":3,"capacity":16}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			supported, validReply, calls := false, true, 0
			p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1" {
					replyJSON(w, 200, map[string]any{"api_version": 1, "mode": "standalone", "model": "pico-s", "commands": []string{"encoder"}, "recipe_capacity": 16, tc.flag: supported})
					return
				}
				body, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.Path != tc.path || string(body) != tc.body {
					t.Fatalf("unexpected device request %s %s %q", r.Method, r.URL.Path, body)
				}
				calls++
				w.WriteHeader(tc.code)
				if validReply {
					fmt.Fprint(w, tc.response)
				} else {
					fmt.Fprint(w, "{}")
				}
			})
			ctx := context.Background()
			request := domain.Command{Command: tc.name}
			for _, enabled := range []bool{false, true} {
				supported = enabled
				caps, err := p.Discover(ctx)
				if err != nil || includes(caps.Commands, tc.name) != enabled {
					t.Fatalf("discovery: %v %v", caps, err)
				}
				if err := p.Command(ctx, request); (err == nil) != enabled {
					t.Fatalf("command: %v", err)
				}
			}
			if err := p.Command(ctx, domain.Command{Command: tc.name, Click: true}); err == nil {
				t.Fatal("accepted unrelated arguments")
			}
			validReply = false
			if err := p.Command(ctx, request); err == nil || !deviceFailure(t, err).UnknownOutcome {
				t.Fatalf("unconfirmed reply: %v", err)
			}
			if calls != 2 {
				t.Fatalf("issued %d writes", calls)
			}
		})
	}
}

func TestMaintenanceStatusFromFirmware(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		want         *domain.Maintenance
	}{
		{"old firmware", "{}", nil},
		{"due", `{"maintenance":{"due":["rinse","clean_filter"]}}`, &domain.Maintenance{Due: []string{"rinse", "clean_filter"}}},
		{"clear", `{"maintenance":{"due":[]}}`, &domain.Maintenance{Due: []string{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			brewer := testPico(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/status" {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.fields)
			})
			status, err := brewer.Status(context.Background())
			if err != nil || !reflect.DeepEqual(status.Maintenance, tc.want) {
				t.Fatalf("maintenance = %#v, error = %v", status.Maintenance, err)
			}
		})
	}
}

func TestReviewedRecipeChecksTheRevisionReadWithItsContents(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		for _, tc := range []struct {
			name                                        string
			revision                                    uint32
			changedBeforeRead, changedAfterRead, legacy bool
			wantCode                                    string
			wantWrites                                  int
		}{
			{"current", 43, false, false, false, "", 1},
			{"other client already saved", 44, true, false, false, "recipe_changed", 0},
			{"other client saved during write", 43, false, true, false, "recipe_changed", 1},
			{"advertised but missing revision", 0, false, false, false, "invalid_response", 0},
			{"older firmware", 0, false, false, true, "", 1},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				original := nativeRecipe()
				revised := original
				revised.Name = "Reviewed change"
				installed := original
				if tc.changedBeforeRead {
					installed.Name = "Other edit"
				}
				writes := 0
				p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v1" {
						replyJSON(w, 200, map[string]any{"api_version": 1, "mode": "standalone", "recipe_capacity": 16, "recipe_conditional_write": !tc.legacy})
						return
					}
					if r.Method == http.MethodGet {
						replyJSON(w, 200, recipeSlot{installed, tc.revision})
						return
					}
					writes++
					if r.Method != method {
						t.Errorf("unexpected method %s", r.Method)
					}
					var fields map[string]json.RawMessage
					if r.ContentLength != 0 {
						if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
							t.Error(err)
						}
					}
					if tc.legacy {
						if fields["if_revision"] != nil {
							t.Error("sent conditional write to legacy firmware")
						}
					} else if string(fields["if_revision"]) != "43" {
						t.Errorf("wrong revision on write: %s", fields["if_revision"])
					}
					if tc.changedAfterRead {
						replyJSON(w, 409, map[string]string{"error": "recipe_changed", "message": "Catalog changed"})
						return
					}
					if method == http.MethodDelete {
						w.WriteHeader(204)
						return
					}
					installed = revised
					replyJSON(w, 200, installed)
				})
				var err error
				if method == http.MethodDelete {
					err = p.DeleteRecipe(context.Background(), original.ID, &original)
				} else {
					err = p.PutRecipe(context.Background(), revised, &original)
				}
				if tc.wantCode == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else if failure := deviceFailure(t, err); failure.Code != tc.wantCode || failure.UnknownOutcome {
					t.Fatal(failure)
				}
				if writes != tc.wantWrites {
					t.Errorf("sent %d writes, want %d", writes, tc.wantWrites)
				}
			})
		}
	}
}

func TestZNativeWriteRequiresConfirmedFormatAndMatchingReadback(t *testing.T) {
	cloud := domain.MachineRecipe{ID: "archived", Name: "Archive", ZCloud: &domain.ZCloudProgram{StartWaterML: 13170, IsBeer: true},
		Steps: []domain.MachineStep{{Name: "Pass through", TargetF: 152, Location: 0, Minutes: 4}, {Name: "Connect chiller", Location: 6, TargetF: 68}, {Name: "Chill", Location: 0, TargetF: 50, Minutes: 5}}}
	minTime, maxTime, selected := 25, 15, 8
	kit := domain.MachineRecipe{ID: "original", Name: "Original kit", Steps: []domain.MachineStep{{Name: "Mash", Location: 2, TargetF: 152, Minutes: 20}}, ZPak: &domain.ZPicoPak{RecipeID: 1, BeerID: 47, ABV: 5, IBU: 6, SelectedABV: &selected, StepOptions: []domain.ZPakStep{{MinTime: &minTime, MaxTime: &maxTime}}}}
	mixed := kit
	mixed.ZCloud = cloud.ZCloud
	if validateRecipe(mixed) == nil {
		t.Fatal("multiple program formats accepted")
	}
	for capability, r := range map[string]domain.MachineRecipe{"z_cloud_program_v1": cloud, "z_picopak_v1": kit} {
		for _, capable := range []bool{false, true} {
			writes := 0
			p := testPico(t, func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/api/v1" {
					replyJSON(w, 200, map[string]any{"mode": "standalone", "api_version": 1, "model": "z-series", "model_family": "z-series", "recipe_capacity": 16, "step_capacity": 15, "recipe_create_only": true, "zero_minute_steps": true, capability: capable})
					return
				}
				if req.Method == "PUT" {
					writes++
					var got domain.MachineRecipe
					if err := json.NewDecoder(req.Body).Decode(&got); err != nil || !reflect.DeepEqual(got, r) {
						t.Errorf("program changed: %+v %v", got, err)
					}
					replyJSON(w, 201, map[string]any{})
					return
				}
				replyJSON(w, 200, r)
			})
			err := p.CreateRecipe(context.Background(), r)
			if capable && (err != nil || writes != 1) || !capable && (err == nil || writes != 0) {
				t.Errorf("capability %t, writes %d, %v", capable, writes, err)
			}
		}
	}
}
