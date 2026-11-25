package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

type transferAdapter struct {
	device.Adapter
	mu                       sync.Mutex
	caps                     domain.Capabilities
	programs                 map[string]domain.MachineRecipe
	writes, commands, failAt int
	busy                     bool
	arriveDuringCreate       *domain.MachineRecipe
	afterWrite               func(map[string]domain.MachineRecipe)
}

func (a *transferAdapter) Discover(context.Context) (domain.Capabilities, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.caps, nil
}
func (a *transferAdapter) Status(ctx context.Context) (domain.Status, error) {
	st, err := a.Adapter.Status(ctx)
	a.mu.Lock()
	st.Busy = a.busy
	a.mu.Unlock()
	return st, err
}
func (a *transferAdapter) Recipes(context.Context) ([]domain.MachineRecipe, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := []domain.MachineRecipe{}
	for _, r := range a.programs {
		r.Steps = append([]domain.MachineStep(nil), r.Steps...)
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
func (a *transferAdapter) Recipe(_ context.Context, id string) (domain.MachineRecipe, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, found := a.programs[id]
	if !found {
		return domain.MachineRecipe{}, &device.Error{StatusCode: 404, Code: "recipe_not_found"}
	}
	r.Steps = append([]domain.MachineStep(nil), r.Steps...)
	return r, nil
}
func (a *transferAdapter) PutRecipe(_ context.Context, r domain.MachineRecipe, expected *domain.MachineRecipe) error {
	return a.writeRecipe(r, false, expected)
}
func (a *transferAdapter) CreateRecipe(_ context.Context, r domain.MachineRecipe) error {
	return a.writeRecipe(r, true, nil)
}
func (a *transferAdapter) writeRecipe(r domain.MachineRecipe, create bool, expected *domain.MachineRecipe) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if expected != nil && !reflect.DeepEqual(a.programs[r.ID], *expected) {
		return &device.Error{StatusCode: 409, Code: "recipe_changed"}
	}
	if create {
		if a.arriveDuringCreate != nil {
			a.programs[a.arriveDuringCreate.ID] = *a.arriveDuringCreate
		}
		if _, exists := a.programs[r.ID]; exists {
			return &device.Error{StatusCode: 409, Code: "recipe_exists", Message: "ID already exists"}
		}
	}
	a.writes++
	r.Steps = append([]domain.MachineStep(nil), r.Steps...)
	a.programs[r.ID] = r
	if a.afterWrite != nil {
		a.afterWrite(a.programs)
	}
	if a.failAt == a.writes {
		return &device.Error{Code: "transport_error", Message: "Response lost after write", UnknownOutcome: true}
	}
	return nil
}
func (a *transferAdapter) Command(context.Context, domain.Command) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.commands++
	return nil
}

func newTransferAdapter() *transferAdapter {
	return &transferAdapter{Adapter: device.NewSimulator("bulk", "pico-pro"), caps: domain.Capabilities{RecipeCapacity: 3, StepCapacity: 15, ZeroMinuteSteps: true}, programs: map[string]domain.MachineRecipe{}}
}
func putTransferSource(t *testing.T, s *Server, id string) domain.Recipe {
	t.Helper()
	r := domain.Recipe{ID: "library-" + id, Name: "Program " + id, Kind: "native-program", SourceModel: "pico", Revision: 1,
		NativeProgram: &domain.MachineRecipe{ID: id, Name: "Program " + id, Steps: []domain.MachineStep{{Name: "Heat", TargetF: 152, Location: 1, Minutes: 0}, {Name: "Mash", TargetF: 152, Location: 1, Minutes: 60}}}}
	if _, err := s.db.Put("recipe", r.ID, r, 0); err != nil {
		t.Fatal(err)
	}
	return r
}
func transferInput(recipes ...domain.Recipe) DeploymentInput {
	in := DeploymentInput{ProfileID: "pico-pro"}
	for _, r := range recipes {
		in.Items = append(in.Items, DeploymentSelection{RecipeID: r.ID, SourceRevision: r.Revision})
	}
	return in
}
func previewTransfer(t *testing.T, c *http.Client, url, csrf string, in DeploymentInput) BulkDeployment {
	t.Helper()
	code, body := request(t, c, "POST", url+"/api/v1/devices/bulk/deployments", csrf, in, nil)
	var plan BulkDeployment
	if code != 201 || json.Unmarshal(body, &plan) != nil {
		t.Fatalf("preview %d %s", code, body)
	}
	return plan
}
func executeTransfer(t *testing.T, c *http.Client, url, csrf string, plan BulkDeployment) (int, BulkDeployment) {
	t.Helper()
	code, body := request(t, c, "POST", url+"/api/v1/devices/bulk/deployments/"+plan.ID+"/execute", csrf, map[string]any{}, nil)
	var receipt BulkDeployment
	if err := json.Unmarshal(body, &receipt); err != nil {
		t.Fatal(err)
	}
	return code, receipt
}

func TestBulkPreviewPreservesExistingAndLimitsNewSlots(t *testing.T) {
	s, h, c := newTestServer(t)
	csrf := signIn(t, c, h.URL)
	a := newTransferAdapter()
	installTestDevice(t, s, "bulk", a)
	r1, r2, r3, r4 := putTransferSource(t, s, "one"), putTransferSource(t, s, "two"), putTransferSource(t, s, "three"), putTransferSource(t, s, "four")
	a.programs["one"] = *r1.NativeProgram
	before := a.programs["one"]
	r1.NativeProgram.Steps = append([]domain.MachineStep(nil), r1.NativeProgram.Steps...)
	r1.NativeProgram.Steps[1].Minutes = 45
	if _, err := s.db.Put("recipe", r1.ID, r1, -1); err != nil {
		t.Fatal(err)
	}
	plan := previewTransfer(t, c, h.URL, csrf, transferInput(r1, r2, r3, r4))
	if plan.Available != 2 || plan.PlannedCount != 2 || plan.NewCount != 2 || plan.Items[0].Status != "skipped" || plan.Items[3].Status != "skipped" || a.writes != 0 {
		t.Fatalf("unexpected capacity preview: %+v", plan)
	}
	code, receipt := executeTransfer(t, c, h.URL, csrf, plan)
	if code != 200 || receipt.State != "complete" || receipt.VerifiedCount != 2 || a.writes != 2 || a.commands != 0 || !reflect.DeepEqual(a.programs["one"], before) {
		t.Fatalf("transfer changed existing recipe/started brew: %+v", receipt)
	}
	code, _ = executeTransfer(t, c, h.URL, csrf, plan)
	if code != 200 || a.writes != 2 {
		t.Fatal("completed transfer was repeated")
	}
	in := transferInput(r1, r2)
	in.OverwriteExisting = true
	plan = previewTransfer(t, c, h.URL, csrf, in)
	if plan.UpdateCount != 1 || plan.NewCount != 0 || plan.PlannedCount != 1 || plan.Items[1].Status != "skipped" {
		t.Fatalf("update preview: %+v", plan)
	}
	code, receipt = executeTransfer(t, c, h.URL, csrf, plan)
	if code != 200 || receipt.VerifiedCount != 1 || a.programs["one"].Steps[1].Minutes != 45 {
		t.Fatal("explicit update not applied")
	}
}

func TestBulkTransferRejectsChangedPreviewInputs(t *testing.T) {
	for _, change := range []string{"inventory", "revision", "profile", "capability", "busy", "expiry", "legacy", "program", "yield"} {
		t.Run(change, func(t *testing.T) {
			s, h, c := newTestServer(t)
			csrf := signIn(t, c, h.URL)
			a := newTransferAdapter()
			installTestDevice(t, s, "bulk", a)
			r := putTransferSource(t, s, "one")
			plan := previewTransfer(t, c, h.URL, csrf, transferInput(r))
			switch change {
			case "inventory":
				a.programs["one"] = *r.NativeProgram
			case "revision":
				r.Revision++
				_, _ = s.db.Put("recipe", r.ID, r, -1)
			case "profile":
				_, _ = s.db.Put("profile", "pico-pro", profileCalibration{BatchLiters: 4, Efficiency: 55}, 0)
			case "capability":
				a.caps.ZeroMinuteSteps = false
			case "busy":
				a.busy = true
			case "legacy", "program", "yield":
				switch change {
				case "legacy":
					plan.Items[0].RequestedBatchLiters = nil
				case "program":
					plan.Items[0].Native.Steps[0].Location = 5
				case "yield":
					plan.Items[0].BatchLiters++
				}
				_, _ = s.db.Put("batch-deployment", plan.ID, plan, -1)
			case "expiry":
				plan.ExpiresAt = time.Now().Add(-time.Minute)
				_, _ = s.db.Put("batch-deployment", plan.ID, plan, -1)
			}
			code, _ := executeTransfer(t, c, h.URL, csrf, plan)
			if code != 409 || a.writes != 0 {
				t.Fatalf("stale %s preview wrote: status=%d writes=%d", change, code, a.writes)
			}
		})
	}
}

func TestBulkTransferStopsOnUnknownWriteWithoutReplay(t *testing.T) {
	s, h, c := newTestServer(t)
	csrf := signIn(t, c, h.URL)
	a := newTransferAdapter()
	a.failAt = 2
	installTestDevice(t, s, "bulk", a)
	r1, r2, r3 := putTransferSource(t, s, "one"), putTransferSource(t, s, "two"), putTransferSource(t, s, "three")
	plan := previewTransfer(t, c, h.URL, csrf, transferInput(r1, r2, r3))
	code, receipt := executeTransfer(t, c, h.URL, csrf, plan)
	if code != 200 || receipt.State != "partial" || receipt.VerifiedCount != 1 || a.writes != 2 || receipt.Items[1].Status != "unconfirmed" || receipt.Items[2].Status != "not_sent" {
		t.Fatalf("uncertain transfer: %+v", receipt)
	}
	code, _ = executeTransfer(t, c, h.URL, csrf, plan)
	if code != 409 || a.writes != 2 {
		t.Fatal("uncertain transfer automatically retried")
	}
}

func TestBulkReceiptRecoversAfterRestartWithoutWriting(t *testing.T) {
	cfg := Config{DataDir: newTestDataDir(t), Password: ownerPassword, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	plan := BulkDeployment{ID: "interrupted", DeviceID: "bulk", State: "running", VerifiedCount: 1, Items: []DeploymentItem{{Status: "verified"}, {Status: "sending"}, {Status: "ready"}}}
	if _, err = s.db.Put("batch-deployment", plan.ID, plan, 0); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.db.Get("batch-deployment", plan.ID, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.State != "interrupted" || plan.VerifiedCount != 1 || plan.Items[0].Status != "verified" || plan.Items[1].Status != "unconfirmed" || plan.Items[2].Status != "not_sent" {
		t.Fatalf("lost restart receipt: %+v", plan)
	}
}

func TestInstalledProgramEditingChecksStaleAndBusyState(t *testing.T) {
	s, h, c := newTestServer(t)
	csrf := signIn(t, c, h.URL)
	a := newTransferAdapter()
	installTestDevice(t, s, "bulk", a)
	r := putTransferSource(t, s, "one")
	a.programs["one"] = *r.NativeProgram
	expected := *r.NativeProgram
	edited := expected
	edited.Steps = append([]domain.MachineStep(nil), expected.Steps...)
	edited.Steps[1].Minutes = 45
	url := fmt.Sprintf("%s/api/v1/devices/bulk/recipes/one", h.URL)
	in := MachineRecipeUpdate{Recipe: edited, Expected: expected}
	// An omitted hold must not become a command to advance immediately.
	wire, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	malformed := strings.Replace(string(wire), `"minutes":45,`, "", 1)
	if code, body := request(t, c, "PUT", url, csrf, json.RawMessage(malformed), nil); code != 400 || a.writes != 0 {
		t.Fatalf("incomplete slot edit: %d %s (writes %d)", code, body, a.writes)
	}
	a.busy = true
	code, body := request(t, c, "PUT", url, csrf, in, nil)
	if code != 409 || a.writes != 0 {
		t.Fatalf("busy edit: %d %s", code, body)
	}
	a.busy = false
	code, body = request(t, c, "PUT", url, csrf, in, nil)
	if code != 200 || a.writes != 1 || a.commands != 0 || a.programs["one"].Steps[1].Minutes != 45 {
		t.Fatalf("slot edit: %d %s", code, body)
	}
	code, _ = request(t, c, "PUT", url, csrf, in, nil)
	if code != 409 || a.writes != 1 {
		t.Fatal("stale slot editor replaced an updated recipe")
	}
}

func TestBulkRevalidatesAutomaticYieldAndExplicitBatch(t *testing.T) {
	for _, requested := range []float64{0, 3} {
		s, h, c := newTestServer(t)
		token := signIn(t, c, h.URL)
		a := newTransferAdapter()
		installTestDevice(t, s, "bulk", a)
		r := validRecipe()
		r.ID, r.Revision = "pale", 1
		if _, err := s.db.Put("recipe", r.ID, r, 0); err != nil {
			t.Fatal(err)
		}
		in := transferInput(r)
		in.Items[0].BatchLiters = requested
		plan := previewTransfer(t, c, h.URL, token, in)
		item := plan.Items[0]
		if item.Status != "ready" || item.RequestedBatchLiters == nil || *item.RequestedBatchLiters != requested ||
			requested == 0 && item.BatchLiters <= 5 || requested > 0 && item.BatchLiters != requested {
			t.Fatalf("unexpected preview for requested %g L: %+v", requested, item)
		}
		if code, receipt := executeTransfer(t, c, h.URL, token, plan); code != 200 || receipt.VerifiedCount != 1 || a.writes != 1 {
			t.Fatalf("unchanged preview for requested %g L did not transfer: %d %+v", requested, code, receipt)
		}
	}
}

func TestNewTransferSlotCannotReplaceAConcurrentArrival(t *testing.T) {
	for _, mode := range []string{"single", "bulk"} {
		t.Run(mode, func(t *testing.T) {
			s, h, c := newTestServer(t)
			csrf := signIn(t, c, h.URL)
			a := newTransferAdapter()
			installTestDevice(t, s, "bulk", a)
			r := putTransferSource(t, s, "one")
			incoming := *r.NativeProgram
			incoming.Name = "Created on brewer"
			a.arriveDuringCreate = &incoming
			if mode == "bulk" {
				input := transferInput(r)
				input.OverwriteExisting = true
				plan := previewTransfer(t, c, h.URL, csrf, input)
				code, receipt := executeTransfer(t, c, h.URL, csrf, plan)
				if code != 200 || receipt.State != "partial" || receipt.Items[0].Status != "not_sent" {
					t.Fatal(code, receipt)
				}
			} else {
				url := h.URL + "/api/v1/devices/bulk/deploy"
				in := map[string]any{"recipe_id": r.ID, "source_revision": r.Revision}
				code, _ := request(t, c, "POST", url, csrf, in, nil)
				if code != 409 {
					t.Fatal(code)
				}
				if code, _ = request(t, c, "POST", url, csrf, in, nil); code != 409 {
					t.Fatal("existing slot accepted", code)
				}
				changed := incoming
				changed.Name = "Stale preview"
				in["replace_expected"] = changed
				if code, _ = request(t, c, "POST", url, csrf, in, nil); code != 409 {
					t.Fatal("stale replacement accepted", code)
				}
				if !reflect.DeepEqual(a.programs[incoming.ID], incoming) || a.writes != 0 {
					t.Fatal("collision overwrote the slot")
				}
				in["replace_expected"] = incoming
				if code, _ = request(t, c, "POST", url, csrf, in, nil); code != 200 || a.writes != 1 || !reflect.DeepEqual(a.programs[incoming.ID], *r.NativeProgram) {
					t.Fatal("reviewed replacement failed", code, a.writes)
				}
				return
			}
			if !reflect.DeepEqual(a.programs[incoming.ID], incoming) || a.writes != 0 {
				t.Fatal("collision overwrote the slot")
			}
		})
	}
}

func TestBulkTransferPreservesLaterSlotChangedOnBrewer(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(fmt.Sprintf("removed=%t", removed), func(t *testing.T) {
			s, h, c := newTestServer(t)
			csrf := signIn(t, c, h.URL)
			a := newTransferAdapter()
			installTestDevice(t, s, "bulk", a)
			first := putTransferSource(t, s, "one")
			second := putTransferSource(t, s, "two")
			prior := *second.NativeProgram
			prior.Name = "Previously installed"
			a.programs[prior.ID] = prior
			in := transferInput(first, second)
			in.OverwriteExisting = true
			plan := previewTransfer(t, c, h.URL, csrf, in)
			a.afterWrite = func(programs map[string]domain.MachineRecipe) {
				if removed {
					delete(programs, prior.ID)
				} else {
					prior.Name = "Edited on brewer"
					programs[prior.ID] = prior
				}
			}
			code, receipt := executeTransfer(t, c, h.URL, csrf, plan)
			observed, exists := a.programs[prior.ID]
			if code != 200 || receipt.State != "partial" || receipt.VerifiedCount != 1 ||
				receipt.Items[1].Status != "not_sent" || a.writes != 1 || exists == removed ||
				(exists && !reflect.DeepEqual(observed, prior)) {
				t.Fatalf("unexpected slot or receipt after concurrent change: %+v, %+v", observed, receipt)
			}
		})
	}
}

func TestSingleDeployChecksAdvertisedCapabilitiesAndSlotCapacity(t *testing.T) {
	s, h, c := newTestServer(t)
	csrf := signIn(t, c, h.URL)
	a := newTransferAdapter()
	installTestDevice(t, s, "bulk", a)
	r := putTransferSource(t, s, "one")
	url := h.URL + "/api/v1/devices/bulk/deploy"
	in := map[string]any{"recipe_id": r.ID, "source_revision": r.Revision}
	a.caps.StepCapacity = 1
	if code, body := request(t, c, "POST", url, csrf, in, nil); code != 422 || a.writes != 0 {
		t.Fatalf("accepted unsupported step count: %d %s", code, body)
	}
	a.caps.StepCapacity = 15
	a.caps.ZeroMinuteSteps = false
	if code, body := request(t, c, "POST", url, csrf, in, nil); code != 422 || a.writes != 0 {
		t.Fatalf("accepted unsupported heat step: %d %s", code, body)
	}
	a.caps.ZeroMinuteSteps = true
	a.caps.RecipeCapacity = 1
	other := *r.NativeProgram
	other.ID = "other"
	a.programs[other.ID] = other
	if code, body := request(t, c, "POST", url, csrf, in, nil); code != 409 || a.writes != 0 {
		t.Fatalf("accepted new slot in full inventory: %d %s", code, body)
	}
	delete(a.programs, other.ID)
	if code, body := request(t, c, "POST", url, csrf, in, nil); code != 200 || a.writes != 1 {
		t.Fatalf("rejected compatible transfer: %d %s", code, body)
	}
}
