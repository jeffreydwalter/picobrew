package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

func TestBrewerPicoFermJournal(t *testing.T) {
	s, h, client := newTestServer(t)
	csrf := signIn(t, client, h.URL)
	devices := h.URL + "/api/v1/devices"
	add := func(name, model string) domain.DeviceView {
		in := keyDeviceInput(name, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
		in["model"] = model
		return saveKeyDevice(t, client, devices, csrf, "", in)
	}
	brewer, other := add("Pico S", "pico-s"), add("Zymatic", "zymatic")
	ferm, spare := add("Cellar", "picoferm"), add("Spare", "picoferm")
	putBrew := func(id string, m domain.DeviceView, cmd string) {
		t.Helper()
		b := domain.Brew{ID: id, DeviceID: m.ID, Model: m.Model, Source: "device", Command: cmd, RecipeName: "Ale", Notes: "retain"}
		if _, err := s.db.Put("brew", id, b, 0); err != nil {
			t.Fatal(err)
		}
	}
	putBrew("ale", brewer, "brew")
	putBrew("lager", brewer, "manual")
	putBrew("clean", brewer, "clean")
	putBrew("elsewhere", other, "brew")
	putBrew("ferm-1", ferm, "start")
	putBrew("spare-1", spare, "start")
	path := devices + "/" + brewer.ID + "/picoferms"
	call := func(method, url, token string, in any, expected int) []byte {
		t.Helper()
		code, b := request(t, client, method, url, token, in, nil)
		if code != expected {
			t.Fatalf("%s %s got %d want %d: %s", method, url, code, expected, b)
		}
		return b
	}
	if code, _ := request(t, http.DefaultClient, "GET", path, "", nil, nil); code != 401 {
		t.Fatalf("unprotected read %d", code)
	}
	call("PUT", path, "", map[string]any{}, 403)
	call("GET", devices+"/"+ferm.ID+"/picoferms", csrf, nil, 400)
	call("PUT", path, csrf, map[string]any{"picoferm_id": ferm.ID}, 400)
	link := map[string]any{"picoferm_id": ferm.ID, "revision": 0, "brew_id": "ale", "recording_id": "spare-1"}
	call("PUT", path, csrf, link, 400)
	link["recording_id"] = "ferm-1"
	for _, id := range []string{"clean", "elsewhere"} {
		link["brew_id"] = id
		call("PUT", path, csrf, link, 400)
	}
	link["brew_id"] = "ale"
	call("PUT", path, csrf, link, 200)
	call("PUT", path, csrf, link, 409)
	link["revision"] = 1
	call("PUT", path, csrf, link, 200) // same association and source recording
	link["brew_id"] = "lager"
	call("PUT", path, csrf, link, 409)
	call("PUT", path, csrf, map[string]any{"picoferm_id": spare.ID, "revision": 0, "brew_id": "ale"}, 409)
	call("PUT", devices+"/"+other.ID+"/picoferms", csrf, map[string]any{"picoferm_id": ferm.ID, "revision": 1, "unlink": true}, 409)
	call("PUT", path, csrf, map[string]any{"picoferm_id": ferm.ID, "revision": 1, "unlink": true}, 200)
	link["brew_id"] = "ale"
	link["revision"] = 2
	call("PUT", path, csrf, link, 200)
	var options brewerFermentationOptions
	if err := json.Unmarshal(call("GET", path, csrf, nil, 200), &options); err != nil {
		t.Fatal(err)
	}
	if len(options.PicoFerms) != 2 || len(options.Brews) != 2 || len(options.Recordings) != 2 {
		t.Fatalf("unexpected choices %+v", options)
	}
	idx := slices.IndexFunc(options.PicoFerms, func(f picoFermOption) bool { return f.ID == ferm.ID })
	if idx < 0 || options.PicoFerms[idx].BrewerID != "" || options.PicoFerms[idx].Revision != 2 {
		t.Fatalf("link did not clear: %+v", options.PicoFerms)
	}
	i := slices.IndexFunc(options.Recordings, func(r fermentationRecording) bool { return r.ID == "ferm-1" })
	if i < 0 || options.Recordings[i].LinkedBrewID != "ale" {
		t.Fatal("expected recording linked to ale")
	}
	var detail domain.BrewDetail
	if err := json.Unmarshal(call("GET", h.URL+"/api/v1/brews/ale", csrf, nil, 200), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.PicoFermID != ferm.ID || detail.PicoFermName != "Cellar" || detail.PicoFermRecordingID != "ferm-1" || detail.Notes != "retain" {
		t.Fatalf("lost batch provenance or notes: %+v", detail.Brew)
	}
}

type fakeFermentMailbox struct {
	device.Adapter
	input     device.FermentMenuRequest
	reply     device.FermentMenuReply
	readCount int
	drop      bool
}

func (f *fakeFermentMailbox) FermentRequest(context.Context) (device.FermentMenuRequest, error) {
	f.readCount++
	return f.input, nil
}
func (f *fakeFermentMailbox) ReplyFerment(_ context.Context, r device.FermentMenuReply) error {
	f.reply = r
	if f.drop {
		return errors.New("reply lost")
	}
	return nil
}

func TestPhysicalFermentMenuLink(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	mail := &fakeFermentMailbox{}
	add := func(id, name, model, owner string) *runtimeDevice {
		m := domain.Device{ID: id, Name: name, Model: model, Adapter: "pico-standalone", Enabled: true, BrewerID: owner}
		if _, err := s.db.Put("device", id, m, 0); err != nil {
			t.Fatal(err)
		}
		d := &runtimeDevice{view: domain.DeviceView{Device: m, Online: true, Status: &domain.Status{UptimeMS: 1000}, Capabilities: &domain.Capabilities{FermentMenuV1: true}}, cancel: func() {}}
		s.devices[id] = d
		return d
	}
	b := add("brewer", "Pico S", "pico-s", "")
	b.adapter = mail
	other := add("other", "Other brewer", "pico-c", "")
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		add(id, "Long same device name "+id, "picoferm", "")
	}
	add("owned", "Other brewer's accessory", "picoferm", other.view.ID)
	menu := func(id, page uint32, action string, list uint32, slot *uint8) device.FermentMenuReply {
		t.Helper()
		mail.reply = device.FermentMenuReply{}
		mail.input = device.FermentMenuRequest{Request: id, Page: page, Action: action, ListRequest: list, Slot: slot}
		s.syncFermentMenu(ctx, b)
		return mail.reply
	}
	slot := uint8(0)
	b.view.Capabilities.FermentMenuV1 = false
	menu(1, 0, "list", 0, nil)
	if mail.readCount != 0 {
		t.Fatal("old firmware was polled")
	}
	b.view.Capabilities.FermentMenuV1 = true
	r := menu(1, 0, "list", 0, nil)
	if len(r.Entries) != 4 || r.Result != "list" || r.More != 1 || r.Entries[0].Name == r.Entries[1].Name || len(r.Entries[1].Name) > 16 {
		t.Fatalf("invalid first page %+v", r)
	}
	r = menu(2, 1, "list", 0, nil)
	if len(r.Entries) != 1 || r.More != 0 {
		t.Fatalf("invalid second page %+v", r)
	}
	if r = menu(3, 1, "link", 1, &slot); r.Request != 0 {
		t.Fatalf("old page accepted %+v", r)
	}
	menu(4, 0, "list", 0, nil)
	if _, err := s.db.SetPicoFermBrewer("a", other.view.ID, 0); err != nil {
		t.Fatal(err)
	}
	if r = menu(5, 0, "link", 4, &slot); r.Result != "stale" {
		t.Fatalf("concurrent change overwritten %+v", r)
	}
	mail.drop = true
	menu(6, 0, "list", 0, nil)
	mail.drop = false
	if r = menu(7, 0, "link", 6, &slot); r.Request != 0 {
		t.Fatal("unacknowledged page could make an assignment")
	}
	menu(8, 0, "list", 0, nil)
	mail.drop = true
	if r = menu(9, 0, "link", 8, &slot); r.Result != "linked" {
		t.Fatalf("link failed %+v", r)
	}
	mail.drop = false
	if r = menu(9, 0, "link", 8, &slot); r.Result != "linked" {
		t.Fatalf("lost decision acknowledgment was not replayed %+v", r)
	}
	r = menu(10, 0, "list", 0, nil)
	if r.Entries[0].Linked != 1 {
		t.Fatalf("Hub link not reflected on OLED %+v", r)
	}
	if r = menu(11, 0, "unlink", 10, &slot); r.Result != "unlinked" {
		t.Fatalf("unlink: %+v", r)
	}
	var saved domain.Device
	if _, err := s.db.Get("device", "b", &saved); err != nil || saved.BrewerID != "" || saved.BrewerRevision != 2 {
		t.Fatalf("link revisions %+v %v", saved, err)
	}
	menu(12, 0, "list", 0, nil)
	b.view.Status.UptimeMS = 50
	if r = menu(13, 0, "link", 12, &slot); r.Request != 0 {
		t.Fatalf("pre-reboot list accepted: %+v", r)
	}
}
