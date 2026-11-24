package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

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
