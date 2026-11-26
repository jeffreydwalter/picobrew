package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
)

// The OEM spelling and numeric pump flags come from Z119's local /state writer.
const stockZQuietSample = `{"ThermoBlockTemp":22.5,"WortTemp":21,"AmbientTemp":18,"DrainTemp":20,"ValvePosition":2,"KegPumpOn":0,"DrainPumpOn":0,"PauseReason":0,"rssi":-63,"TargetTemp":0,"StepName":"","SecondsEllapsed":0,"SecondsRemaining":0,"UsePicoPakMode":0,"Location":1}`
const stockZTestProgram = `{"Name":"Bench recipe","IsBeer":true,"UsePicoPakMode":false,"Steps":[{"Name":"Mash","StepType":1,"Time":30,"Location":2,"Temp":65,"Drain":60}]}`
const stockZTestPage = `<title>Z Program Maker</title><button onclick="pushToZ()">PUSH TO Z</button><input id="z-pak" type="checkbox">`

type stockZTestPeer struct {
	address      netip.Addr
	page, sample string
	status       int
	ack          string
	lost         bool
	posts        []string
	reads        []string
}

func newStockZTestPeer(t *testing.T, s *Server) *stockZTestPeer {
	t.Helper()
	peer := &stockZTestPeer{address: netip.MustParseAddr("192.168.5.37"), page: stockZTestPage, sample: stockZQuietSample, status: 200}
	s.stockZOpen = func(address string) (stockZEndpoint, error) {
		if address != "http://"+peer.address.String() {
			return stockZEndpoint{}, errors.New("unexpected brewer address")
		}
		client := &http.Client{Transport: receiverTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != peer.address.String() {
				t.Fatalf("unexpected host %s", r.URL.Host)
			}
			resp := &http.Response{StatusCode: 200}
			body := ""
			switch {
			case r.Method == "GET":
				peer.reads = append(peer.reads, r.URL.Path)
				switch r.URL.Path {
				case "/api/v1/health":
					resp.StatusCode = 404
				case "/":
					body = peer.page
				case "/ping":
					body = "PONG!"
				case "/state":
					body = peer.sample
				default:
					t.Fatalf("unexpected read %s", r.URL.Path)
				}
			case r.Method == "POST" && r.URL.Path == "/recipe":
				data, err := io.ReadAll(r.Body)
				if err != nil || r.Header.Get("Content-Type") != "application/json" || r.ContentLength != int64(len(data)) {
					t.Fatalf("invalid OEM request: %#v, %v", r.Header, err)
				}
				peer.posts = append(peer.posts, string(data))
				if peer.lost {
					return nil, errors.New("lost acknowledgement")
				}
				body, resp.StatusCode = peer.ack, peer.status
			default:
				t.Fatalf("unexpected machine operation %s %s", r.Method, r.URL.Path)
			}
			resp.ContentLength = int64(len(body))
			resp.Body = io.NopCloser(strings.NewReader(body))
			return resp, nil
		})}
		return stockZEndpoint{ip: peer.address, client: client}, nil
	}
	return peer
}

func TestStockZProgramTransferAndObservation(t *testing.T) {
	s, h, c := newTestServer(t)
	peer := newStockZTestPeer(t, s)
	base := h.URL + "/api/v1/stock-z-programs"
	reg := map[string]string{"name": "Z by sink", "base_url": "http://" + peer.address.String() + ":80/"}
	if code, _ := request(t, c, "POST", base, "", reg, nil); code != 401 {
		t.Fatalf("owner route returned %d", code)
	}
	token := signIn(t, c, h.URL)
	if code, _ := request(t, c, "POST", base, "", reg, nil); code != 403 {
		t.Fatalf("missing CSRF accepted: %d", code)
	}
	code, b := request(t, c, "POST", base, token, reg, nil)
	var machine stockZProgramMachine
	if code != 201 || json.Unmarshal(b, &machine) != nil || machine.ID == "" || machine.Name != reg["name"] || machine.BaseURL != "http://"+peer.address.String() {
		t.Fatalf("registration %d %s", code, b)
	}
	if code, b := request(t, c, "POST", base, token, reg, nil); code != 200 || !strings.Contains(string(b), machine.ID) {
		t.Fatalf("duplicate registration %d %s", code, b)
	}
	if code, b := request(t, c, "GET", base, "", nil, nil); code != 200 || strings.Count(string(b), machine.ID) != 1 {
		t.Fatalf("registered machines %d %s", code, b)
	}
	done := make(chan struct{})
	close(done)
	s.discovery.mu.Lock()
	s.discovery.current = &discoveryRun{done: done, finished: time.Now(), result: LANDiscovery{Candidates: []DiscoveryCandidate{
		{BaseURL: machine.BaseURL, Source: "stock", Model: "z-series"},
		{BaseURL: machine.BaseURL, Source: "standalone", Model: "z-series"},
	}}}
	s.discovery.mu.Unlock()
	code, b = request(t, c, "GET", h.URL+"/api/v1/devices/discover", "", nil, nil)
	var discovered LANDiscovery
	if code != 200 || json.Unmarshal(b, &discovered) != nil || len(discovered.Candidates) != 1 || discovered.Candidates[0].Source != "standalone" {
		t.Fatalf("registered Z discovery filtering %d %s", code, b)
	}
	target := base + "/" + machine.ID
	peer.sample = strings.ReplaceAll(stockZQuietSample, `"KegPumpOn":0`, `"KegPumpOn":1`)
	peer.sample = strings.ReplaceAll(peer.sample, `"StepName":""`, `"StepName":"Mash"`)
	peer.sample = strings.ReplaceAll(peer.sample, `"SecondsEllapsed":0`, `"SecondsEllapsed":16`)
	peer.sample = strings.ReplaceAll(peer.sample, `"SecondsRemaining":0`, `"SecondsRemaining":110`)
	code, b = request(t, c, "GET", target+"/state", token, nil, nil)
	var state stockZState
	if code != 200 || json.Unmarshal(b, &state) != nil || state.Activity != "active" || state.StepName != "Mash" || state.Elapsed != 16 || state.Remaining != 110 || state.Wort == nil || *state.Wort != 21 {
		t.Fatalf("telemetry %d %s", code, b)
	}
	items, err := recipe.Import([]byte(stockZTestProgram), "json")
	if err != nil {
		t.Fatal(err)
	}
	r := items[0]
	r.ID = "library"
	r.Revision = 1
	if _, err := s.db.Put("recipe", r.ID, r, 0); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"recipe_id": r.ID, "revision": 1, "confirm_replace": false}
	peer.reads = nil
	code, b = request(t, c, "POST", target+"/recipes", token, input, nil)
	if code != 200 || !strings.Contains(string(b), `"status":"preview"`) || !strings.Contains(string(b), `"step_count":1`) || len(peer.reads) != 0 || len(peer.posts) != 0 {
		t.Fatalf("preview %d %s, reads=%v, writes=%d", code, b, peer.reads, len(peer.posts))
	}
	input["confirm_replace"] = true
	var preview stockZProgramReview
	if json.Unmarshal(b, &preview) != nil || preview.ProgramHash != fmt.Sprintf("%x", sha256.Sum256([]byte(stockZTestProgram))) || len(preview.ReviewHash) != 64 {
		t.Fatalf("preview hashes: %s", b)
	}
	if code, b := request(t, c, "POST", target+"/recipes", token, input, nil); code != 409 || !strings.Contains(string(b), "program_changed") || len(peer.reads) != 0 {
		t.Fatalf("accepted unreviewed program %d %s", code, b)
	}
	input["program_hash"] = strings.Repeat("a", 64)
	input["review_hash"] = preview.ReviewHash
	if code, b := request(t, c, "POST", target+"/recipes", token, input, nil); code != 409 || !strings.Contains(string(b), "program_changed") || len(peer.reads) != 0 {
		t.Fatalf("accepted different wire digest %d %s", code, b)
	}
	input["program_hash"] = preview.ProgramHash
	if code, b := request(t, c, "POST", target+"/recipes", token, input, nil); code != 409 || len(peer.posts) != 0 {
		t.Fatalf("busy %d %s", code, b)
	}
	peer.sample = stockZQuietSample
	input["revision"] = 2
	if code, _ := request(t, c, "POST", target+"/recipes", token, input, nil); code != 409 || len(peer.posts) != 0 {
		t.Fatalf("stale revision %d", code)
	}
	input["revision"] = 1
	peer.page = "<html>Some other machine</html>"
	if code, _ := request(t, c, "POST", target+"/recipes", token, input, nil); code != 502 || len(peer.posts) != 0 {
		t.Fatalf("address reassignment %d", code)
	}
	peer.page = stockZTestPage
	peer.reads = nil
	code, b = request(t, c, "POST", target+"/recipes", token, input, nil)
	if code != 200 || !strings.Contains(string(b), `"status":"acknowledged"`) || !slices.Equal(peer.posts, []string{stockZTestProgram}) || !slices.Equal(peer.reads, []string{"/api/v1/health", "/", "/ping", "/state"}) {
		t.Fatalf("stage %d %s; requests %v, POSTs %v", code, b, peer.reads, peer.posts)
	}
	peer.lost = true
	if code, b := request(t, c, "POST", target+"/recipes", token, input, nil); code != 502 || !strings.Contains(string(b), "device_outcome_unknown") || len(peer.posts) != 2 {
		t.Fatalf("lost reply or automatic retry: %d %s, %d posts", code, b, len(peer.posts))
	}
	peer.lost = false
	peer.status = 400
	peer.ack = "program build error 3"
	if code, b := request(t, c, "POST", target+"/recipes", token, input, nil); code != 422 || !strings.Contains(string(b), "program_rejected") || len(peer.posts) != 3 {
		t.Fatalf("rejected %d %s", code, b)
	}
	peer.status = 200
	peer.ack = "injected text"
	if code, b := request(t, c, "POST", target+"/recipes", token, input, nil); code != 502 || !strings.Contains(string(b), "device_outcome_unknown") || len(peer.posts) != 4 {
		t.Fatalf("invalid acknowledgement %d %s", code, b)
	}
	if code, _ := request(t, c, "DELETE", target, token, nil, nil); code != 204 {
		t.Fatalf("remove %d", code)
	}
	if code, _ := request(t, c, "POST", target+"/recipes", token, input, nil); code != 404 || len(peer.posts) != 4 {
		t.Fatalf("removed target %d", code)
	}
}

func TestStockZCalibrationInvalidatesReviewedLoad(t *testing.T) {
	s, h, c := newTestServer(t)
	peer := newStockZTestPeer(t, s)
	machine := stockZProgramMachine{ID: "fixture-z", Name: "Z", BaseURL: "http://" + peer.address.String()}
	if _, err := s.db.Put(stockZProgramKind, machine.ID, machine, 0); err != nil {
		t.Fatal(err)
	}
	r := domain.Recipe{ID: "fitted-recipe", Revision: 1, Name: "Test", BatchLiters: 5, Efficiency: 55,
		Ingredients: []domain.Ingredient{{Name: "Malt", Kind: "fermentable", Use: "mash", Unit: "kg", Amount: 1}, {Name: "Cascade", Kind: "hop", Use: "boil", Unit: "g", Amount: 5, Minutes: 15}},
		Steps:       []domain.RecipeStep{{Name: "Mash", Kind: "mash", TargetC: 65, Minutes: 60}, {Name: "Boil", Kind: "boil", TargetC: 100, Minutes: 60}},
	}
	if _, err := s.db.Put("recipe", r.ID, r, 0); err != nil {
		t.Fatal(err)
	}
	preview, err := s.reviewStockZProgram(r)
	if err != nil || preview.BatchLiters <= 0 || len(preview.Ingredients) != 2 || preview.HopSlotsByMinute[15] == 0 {
		t.Fatalf("first review: %#v, %v", preview, err)
	}
	if _, err := s.db.Put("profile", "z", profileCalibration{BatchLiters: 3, Efficiency: 65, MaxGrainKG: 0.5}, 0); err != nil {
		t.Fatal(err)
	}
	updated, err := s.reviewStockZProgram(r)
	if err != nil || updated.BatchLiters >= preview.BatchLiters || updated.Ingredients[0].Amount >= preview.Ingredients[0].Amount || updated.ReviewHash == preview.ReviewHash || updated.ProgramHash != preview.ProgramHash {
		t.Fatalf("different load must invalidate confirmation even when instructions match: first %+v, then %+v, %v", preview, updated, err)
	}
	input := map[string]any{"recipe_id": r.ID, "revision": 1, "program_hash": preview.ProgramHash, "review_hash": preview.ReviewHash, "confirm_replace": true}
	code, b := request(t, c, "POST", h.URL+"/api/v1/stock-z-programs/"+machine.ID+"/recipes", signIn(t, c, h.URL), input, nil)
	if code != 409 || !strings.Contains(string(b), "program_changed") || len(peer.reads) != 0 || len(peer.posts) != 0 {
		t.Fatalf("stale load: %d %s, reads %v, posts %v", code, b, peer.reads, peer.posts)
	}
}

func TestStockZStateFailsClosed(t *testing.T) {
	for _, tc := range []struct{ from, to, activity string }{
		{`"KegPumpOn":0`, `"KegPumpOn":1`, "active"},
		{`"PauseReason":0`, `"PauseReason":2`, "paused"},
		{`"TargetTemp":0`, `"TargetTemp":94.4`, "unknown"},
		{`"WortTemp":21`, `"WortTemp":null`, "unknown"},
		{`"SecondsEllapsed":0`, `"SecondsEllapsed":10`, "unknown"},
		{`"StepName":""`, `"StepName":"Mash"`, "unknown"},
		{`"PauseReason":0,`, "", ""},
		{`"DrainPumpOn":0`, `"DrainPumpOn":null`, ""},
		{`"PauseReason":0`, `"PauseReason":18`, ""},
		{`"KegPumpOn":0`, `"KegPumpOn":-1`, ""},
		{`"KegPumpOn":0`, `"KegPumpOn":1,"KegPumpOn":0`, ""},
		{`"SecondsEllapsed":0`, `"SecondsElapsed":0`, ""},
	} {
		t.Run(tc.to+tc.from, func(t *testing.T) {
			state, err := parseStockZState([]byte(strings.Replace(stockZQuietSample, tc.from, tc.to, 1)))
			if tc.activity == "" && err == nil || tc.activity != "" && (err != nil || state.Activity != tc.activity) {
				t.Fatalf("state %#v, err %v", state, err)
			}
		})
	}
}

func TestStockZProgramLANBoundary(t *testing.T) {
	for _, address := range []string{"http://127.0.0.1", "http://192.168.1.1:8080/", "https://10.2.3.4", "http://169.254.169.254", "http://example.local", "http://10.2.3.4/admin", "http://10.2.3.4?", "http://10.2.3.4/%2f", "http://example:password@10.2.3.4", "http://[::ffff:10.2.3.4]", "http://224.1.2.3", "http://8.8.8.8", "http://10.2.3.4/#here"} {
		if _, err := stockZIP(address); err == nil {
			t.Errorf("accepted %q", address)
		}
	}
	networks := []lanInterface{{local: netip.MustParseAddr("192.168.1.2"), network: netip.MustParsePrefix("192.168.1.0/24")}}
	for _, address := range []string{"192.168.1.2", "192.168.1.0", "192.168.1.255", "192.168.2.44"} {
		if _, err := stockZLocal(netip.MustParseAddr(address), networks); err == nil {
			t.Errorf("accepted %s", address)
		}
	}
	if local, err := stockZLocal(netip.MustParseAddr("192.168.1.8"), networks); err != nil || local != networks[0].local {
		t.Fatalf("local: %v %v", local, err)
	}
	client := newDiscoveryClient(networks[0].local)
	if client.Transport.(*http.Transport).Proxy != nil || client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("stock Z must bypass proxies and reject redirects")
	}
}

func TestStockZProgramRejectsBeforeTransfer(t *testing.T) {
	s, _, _ := newTestServer(t)
	peer := newStockZTestPeer(t, s)
	if err := s.stageStockZProgram(context.Background(), "missing", nil, false, nil); err == nil || len(peer.reads) != 0 || len(peer.posts) != 0 {
		t.Fatal("unconfirmed program attempted")
	}
	items, err := recipe.Import([]byte(stockZTestProgram), "json")
	if err != nil {
		t.Fatal(err)
	}
	p := items[0].NativeProgram.ZProgram
	for len(p.Steps) < 9 {
		p.Steps = append(p.Steps, p.Steps[0])
	}
	if _, err := s.compileStockZProgram(items[0]); err == nil || !strings.Contains(err.Error(), "JSON memory limit") {
		t.Fatalf("stock receiver bound: %v", err)
	}
	sim := Server{cfg: Config{Demo: true}, stockZOpen: func(string) (stockZEndpoint, error) {
		t.Fatal("simulation attempted LAN access")
		return stockZEndpoint{}, nil
	}}
	if _, err := sim.openStockZ("http://192.168.1.40"); err == nil {
		t.Fatal("simulation accepted physical stock Z")
	}
}

func TestStockZKettleLimitPreview(t *testing.T) {
	s, _, _ := newTestServer(t)
	native, err := recipe.Import([]byte(`{"Name":"Limit check","IsBeer":true,"UsePicoPakMode":false,"Steps":[{"Name":"Kettle","StepType":1,"Time":900,"Location":1,"Temp":96,"Drain":0},{"Name":"Hops","StepType":1,"Time":900,"Location":3,"Temp":96,"Drain":60},{"Name":"Pause","StepType":2}]}`), "json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		saved  domain.Recipe
		name   string
		target float64
	}{
		{native[0], "Kettle", 96},
		{validRecipe(), "Boil", (202.0 - 32) / 1.8},
	} {
		review, err := s.reviewStockZProgram(tc.saved)
		if err != nil || len(review.TemperatureLimits) != 1 {
			t.Fatalf("expected one affected kettle step: %+v, %v", review.TemperatureLimits, err)
		}
		limit := review.TemperatureLimits[0]
		if limit.Step != tc.name || math.Abs(limit.RequestedC-tc.target) > 1e-7 || limit.MaximumC != 94.45 {
			t.Fatalf("incorrect kettle limit: %+v", limit)
		}
	}
}
