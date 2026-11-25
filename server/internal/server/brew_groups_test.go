package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
)

type groupTestDevice struct {
	device.Adapter
	st    domain.Status
	caps  domain.Capabilities
	state device.GroupState
	slots map[string]domain.MachineRecipe
	hist  domain.History
	calls []device.GroupOffer
	rev   uint32
}

func (a *groupTestDevice) Discover(context.Context) (domain.Capabilities, error) { return a.caps, nil }
func (a *groupTestDevice) Status(context.Context) (domain.Status, error)         { return a.st, nil }
func (a *groupTestDevice) GroupState(context.Context) (device.GroupState, error) { return a.state, nil }
func (a *groupTestDevice) GroupRespond(_ context.Context, p device.GroupOffer) error {
	a.calls = append(a.calls, p)
	if p.Op == "offer" {
		p.State = "pending"
		a.state.Invite = &p
		return nil
	}
	if p.ID != a.state.Invite.ID {
		panic("mismatched group operation")
	}
	a.state.Invite.State = map[string]string{"accept": "accepted", "decline": "declined", "withdraw": "withdrawn"}[p.Op]
	if p.Op == "accept" {
		a.st.Busy = true
	}
	return nil
}
func (a *groupTestDevice) GroupRecipe(_ context.Context, id string) (domain.MachineRecipe, uint32, error) {
	if v, ok := a.slots[id]; ok {
		return v, a.rev, nil
	}
	return domain.MachineRecipe{}, 0, &device.Error{StatusCode: 404}
}
func (a *groupTestDevice) Recipes(context.Context) ([]domain.MachineRecipe, error) {
	var p []domain.MachineRecipe
	for _, v := range a.slots {
		p = append(p, v)
	}
	return p, nil
}
func (a *groupTestDevice) CreateRecipe(_ context.Context, p domain.MachineRecipe) error {
	if _, ok := a.slots[p.ID]; ok {
		panic("overwriting group program")
	}
	a.slots[p.ID] = p
	return nil
}
func (a *groupTestDevice) History(context.Context) (domain.History, error) { return a.hist, nil }
func groupTestBrewer(t *testing.T, s *Server, id, model string) (*runtimeDevice, *groupTestDevice) {
	t.Helper()
	family := model
	if model == "pico-pro" || model == "pico-s" {
		family = "pico-s-pro"
	}
	a := &groupTestDevice{st: domain.Status{DeviceUID: id, Model: model, ModelFamily: family, Ready: true}, caps: domain.Capabilities{APIVersion: 1, GroupBrewingV1: true, Model: model, ModelFamily: family, RecipeCapacity: 16, StepCapacity: 15, RecipeCreateOnly: true, HistoryPersistent: true, ZeroMinuteSteps: true, Commands: []string{"brew", "rinse", "clean"}}, rev: 2, slots: map[string]domain.MachineRecipe{}}
	if model == "pico-c" {
		a.st.DisplayVariant = "ssd1325"
		a.caps.DisplayVariant = "ssd1325"
	}
	if model == "z-series" {
		a.caps.ZProgramMaker = true
	}
	d := &runtimeDevice{adapter: a, cancel: func() {}, view: domain.DeviceView{Device: domain.Device{ID: id, Name: id, Model: model, ModelSource: "firmware", Adapter: "pico-standalone", Enabled: true}, Status: &a.st, Capabilities: &a.caps, Online: true}}
	s.mu.Lock()
	s.devices[id] = d
	s.mu.Unlock()
	return d, a
}
func TestGroupConsentAndIsolation(t *testing.T) {
	ctx := context.Background()
	s, h, c := newTestServer(t)
	base := h.URL + "/api/v1"
	a, source := groupTestBrewer(t, s, "one", "pico-s")
	b, target := groupTestBrewer(t, s, "two", "pico-s")
	_, other := groupTestBrewer(t, s, "other", "pico-c")
	input := map[string]any{"name": "Double", "device_ids": []string{"one", "two"}}
	if code, _ := request(t, c, "POST", base+"/brew-groups", "", input, nil); code != 401 {
		t.Fatal(code)
	}
	csrf := signIn(t, c, h.URL)
	code, raw := request(t, c, "POST", base+"/brew-groups", csrf, input, nil)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	var g brewGroup
	json.Unmarshal(raw, &g)
	if code, _ = request(t, c, "POST", base+"/brew-groups", csrf, input, nil); code != 409 {
		t.Fatal(code)
	}
	age := uint32(0)
	source.state.Source = &device.GroupSource{ID: "1234567890abcdef", AgeMS: &age, Action: "rinse"}
	source.st.Busy = true
	source.hist = domain.History{Sessions: []domain.HistorySession{{Available: true, ID: 12, Command: "rinse", State: "recording"}}}
	sync := func(d *runtimeDevice, x *groupTestDevice) {
		t.Helper()
		d.mu.Lock()
		d.view.Status = &x.st
		d.mu.Unlock()
		if err := s.syncBrewGroup(ctx, d, x.st); err != nil {
			t.Fatal(err)
		}
	}
	sync(a, source)
	if len(target.calls) != 0 || len(other.calls) != 0 {
		t.Fatal("source sent direct commands")
	}
	i, _ := s.memberInvite("two")
	if i == nil || i.State != "waiting" {
		t.Fatalf("%#v", i)
	}
	sync(b, target)
	sync(a, source)
	sync(b, target)
	if len(target.calls) != 1 || target.calls[0].Op != "offer" || target.calls[0].TTLMS < 1 || target.calls[0].TTLMS > 120000 || target.st.Busy {
		t.Fatalf("%+v", target.calls)
	}
	post := base + "/devices/two/brew-invite/" + i.ID + "/respond"
	target.st.Busy = true
	if code, _ = request(t, c, "POST", post, csrf, map[string]bool{"accept": true}, nil); code != 409 {
		t.Fatal(code)
	}
	target.st.Busy = false
	target.hist = domain.History{Sessions: []domain.HistorySession{{Available: true, ID: 21, Command: "rinse", State: "recording"}}}
	if code, raw = request(t, c, "POST", post, csrf, map[string]bool{"accept": true}, nil); code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	if code, _ = request(t, c, "POST", post, csrf, map[string]bool{"accept": true}, nil); code != 409 || len(target.calls) != 2 {
		t.Fatal(code, target.calls)
	}
	var srcLink, dstLink groupJournal
	if _, err := s.db.Get("group-journal", "one-12", &srcLink); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Get("group-journal", "two-21", &dstLink); err != nil || srcLink != dstLink || dstLink.GroupID != g.ID {
		t.Fatalf("%+v %+v %v", srcLink, dstLink, err)
	}
	target.state.Invite = nil
	target.st.Busy = false
	source.state.Source.ID = "1234567890abcdee"
	sync(a, source)
	sync(b, target)
	if code, _ = request(t, c, "DELETE", base+"/brew-groups/"+g.ID+"?revision=1", csrf, nil, nil); code != 200 {
		t.Fatal(code)
	}
	sync(b, target)
	if target.calls[len(target.calls)-1].Op != "withdraw" {
		t.Fatal(target.calls)
	}
	if i, _ := s.memberInvite("other"); i != nil {
		t.Fatal(i)
	}
}
func TestGroupRecipeProofAndZymaticMenu(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	_, src := groupTestBrewer(t, s, "source", "pico-s")
	dst, wire := groupTestBrewer(t, s, "target", "pico-c")
	peer, same := groupTestBrewer(t, s, "peer", "pico-s")
	library := validRecipe()
	library.ID = "original"
	library.Revision = 2
	p, _ := s.selectProfile("pico-s")
	plan, err := recipe.Translate(library, p, 0)
	if err != nil || plan.MachineRecipe == nil {
		t.Fatalf("%v %+v", err, plan.DeploymentBlockers)
	}
	program := *plan.MachineRecipe
	ref := groupRecipe{Model: "pico-s", Program: program}
	if _, _, err = s.groupTargetRecipe(ctx, dst, dst.view, wire, ref); err == nil || len(wire.slots) != 0 {
		t.Fatal("unproven mixed recipe")
	}
	other := program
	other.ID = "localcopy"
	same.slots[other.ID] = other
	if slot, _, err := s.groupTargetRecipe(ctx, peer, peer.view, same, ref); err != nil || slot != other.ID {
		t.Fatalf("local program: %s %v", slot, err)
	}
	other.Steps = append([]domain.MachineStep(nil), other.Steps...)
	other.Steps[0].Minutes++
	same.slots[other.ID] = other
	if _, _, err = s.groupTargetRecipe(ctx, peer, peer.view, same, ref); err == nil {
		t.Fatal("different same-model schedule accepted")
	}
	_, _ = s.db.Put("recipe-revision", "original:2", library, 0)
	_, _ = s.db.Put("deployment", "r", groupReceipt{"source", src.st.DeviceUID, "original", 2, program, "verified"}, 0)
	ref, err = s.identifyGroupRecipe(groupView(s.groupDevice("source")), program)
	if err != nil || ref.LibraryID != library.ID {
		t.Fatal(ref, err)
	}
	slot, revision, err := s.groupTargetRecipe(ctx, dst, dst.view, wire, ref)
	if err != nil || revision != 2 || len(wire.slots) != 1 {
		t.Fatal(slot, err)
	}
	cp, _ := s.selectProfile("pico-c")
	cplan, _ := recipe.Translate(library, cp, 0)
	if !reflect.DeepEqual(wire.slots[slot], *cplan.MachineRecipe) {
		t.Fatal("target was not compiled for Pico C")
	}
	_, _ = s.db.Put("group-brew-program", "session", ref, 0)
	i := brewInvite{ID: "0123456789abcdef", Action: "brew", GroupSessionID: "session", TargetDeviceID: stockMemberPrefix + "ABC"}
	if err = s.prepareMenuInvite(&i, "ABC"); err != nil || i.State != "pending" {
		t.Fatal(i, err)
	}
	if err = s.acceptGroupMenu(&i); err != nil {
		t.Fatal(err)
	}
	var stock stockBrewer
	if _, err = s.db.Get(stockKind, "ABC", &stock); err != nil || len(stock.Items) != 1 || stock.Session.ID != "" {
		t.Fatal(stock, err)
	}
}
func TestGroupRejectsOldAndInvalidEvents(t *testing.T) {
	age := uint32(120000)
	e := device.GroupSource{ID: "deadbeefdeadbeef", Action: "clean", AgeMS: &age}
	if validEvent(&e) {
		t.Fatal("expired")
	}
	age = 1
	e.RecipeID = "x"
	if validEvent(&e) {
		t.Fatal("unexpected recipe")
	}
	e.Action = "brew"
	if validEvent(&e) {
		t.Fatal("missing revision")
	}
	e.Revision = 1
	e.ID = strings.ToUpper(e.ID)
	if validEvent(&e) {
		t.Fatal("upper-case id")
	}
	s, h, c := newTestServer(t)
	_, _ = groupTestBrewer(t, s, "one", "pico-s")
	_, _ = groupTestBrewer(t, s, "two", "pico-s")
	csrf := signIn(t, c, h.URL)
	s.rememberZymatic("Z123")
	input := map[string]any{"name": "group", "device_ids": []string{"one", stockMemberPrefix + "Z123"}}
	if code, p := request(t, c, http.MethodPost, h.URL+"/api/v1/brew-groups", csrf, input, nil); code != 200 {
		t.Fatalf("%d %s", code, p)
	}
	if err := s.removeGroupMember("one"); err != nil {
		t.Fatal(err)
	}
	groups, _ := listRecords[brewGroup](s, groupKind)
	if len(groups) != 0 {
		t.Fatal(groups)
	}
	now := time.Now()
	i := brewInvite{GroupID: "g", TargetDeviceID: "two", SourceDeviceID: "one", State: "pending", ExpiresAt: now}
	if err := s.expireInvite(&i, &brewGroup{ID: "g", DeviceIDs: []string{"one", "two"}}, now); err != nil || i.State != "expired" {
		t.Fatal(err, i)
	}
}
