package server

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

func TestZymaticWireReportsReachJournal(t *testing.T) {
	s, h, c := newTestServer(t)
	token := signIn(t, c, h.URL)
	const serial = "Z1234"
	item := stockItem{RecipeID: "library-id", Revision: 3, Program: domain.MachineRecipe{ID: strings.Repeat("e", 32), Name: "Pale", Steps: []domain.MachineStep{{Name: "Mash in", TargetF: 151, Minutes: 20, Location: 1}}}}
	brewer := stockBrewer{Machine: serial, User: strings.Repeat("a", 32), Items: []stockItem{item}, Served: []stockItem{item}}
	if _, err := s.db.Put(stockKind, serial, brewer, 0); err != nil {
		t.Fatal(err)
	}
	wire := func(op string, q url.Values, want int) string {
		t.Helper()
		resp, err := c.Get(h.URL + "/API/" + op + "?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("%s: %s (%d) %v", op, body, resp.StatusCode, err)
		}
		return strings.TrimSpace(string(body))
	}
	start := func() string {
		return strings.Trim(wire("logSession", url.Values{"machine": {serial}, "user": {brewer.User}, "code": {"0"}, "recipe": {item.Program.ID}}, 200), "#")
	}
	sid := start()
	if got := start(); got != sid {
		t.Fatal("created two sessions for an immediate retry", sid, got)
	}
	if got := wire("zymaticFirmwareCheck", url.Values{"machine": {serial}, "ver": {"1"}, "maj": {"1"}, "min": {"14"}}, 200); got != "#F#" {
		t.Fatal("unexpected OEM update offer", got)
	}
	log := func(code, data, reset, state string) {
		wire("LogSession", url.Values{"session": {sid}, "code": {code}, "data": {data}, "step": {reset}, "state": {state}}, 200)
	}
	const checkpoint = "1/2/3/4/5/6/7/1"
	// Wi-Fi can omit its step event. Keep the readings and don't guess a step.
	log("2", "2/150|1/179|3/76|4/-1", checkpoint, "64")
	log("1", "Mash in", "", "2")
	log("2", "2/151|1/180|3/77|4/95", "corrupt", "0")
	const newer = "1/2/3/4/5/7/7/0"
	log("2", "2/5|2/55", newer, "0")
	errorReport := func(machine, session, code string, want int) {
		wire("sessionerror", url.Values{"machine": {machine}, "session": {session}, "errorcode": {code}}, want)
	}
	errorReport(serial, sid, "303", 200)
	errorReport("OTHER", sid, "302", 404)
	if got := wire("recoversession", url.Values{"session": {sid}, "code": {"1"}}, 200); got != "#"+newer+"#" {
		t.Fatal(got)
	}
	endpoint := h.URL + "/api/v1/brews/" + stockBrewID(sid)
	detail := func() domain.BrewDetail {
		t.Helper()
		code, body := request(t, c, "GET", endpoint, token, nil, nil)
		var b domain.BrewDetail
		if code != 200 || json.Unmarshal(body, &b) != nil {
			t.Fatal(code, string(body))
		}
		return b
	}
	b := detail()
	if b.State != "recording" || b.Source != stockReportSource || b.RecipeID != item.RecipeID || b.RecipeRevision != 3 || len(b.Samples) != 4 || b.StartedAt == nil || b.StockSerial != serial {
		t.Fatalf("%+v", b)
	}
	first := b.Samples[0]
	if first.CurrentStep != -1 || first.TargetF != nil || first.StockZymatic == nil || *first.StockZymatic.ErrorFlags != 64 || first.StockZymatic.ResetPoint != checkpoint || *first.WortF != 150 || *first.ThermoblockF != 179 || *first.Readings["sensor_board_f"] != 76 || *first.Readings["sensor_backup_heat_f"] != -1 {
		t.Fatal(first)
	}
	if b.Samples[1].StockZymatic.StepName != "Mash in" || *b.Samples[1].StockZymatic.State != 2 || *b.Samples[3].StockZymatic.ErrorCode != 303 {
		t.Fatal(b.Samples)
	}
	for i, r := range b.Samples {
		if r.ObservedAt == nil || (i > 0 && r.ElapsedMS <= b.Samples[i-1].ElapsedMS) {
			t.Fatal(b.Samples)
		}
	}
	if code, _ := request(t, c, "PATCH", endpoint, token, map[string]any{"notes": "Keep these notes", "rating": 4}, nil); code != 200 {
		t.Fatal(code)
	}
	if code, body := request(t, c, "GET", endpoint+"/export?format=csv", token, nil, nil); code != 200 {
		t.Fatal(code, string(body))
	} else {
		rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
		if err != nil || len(rows) != 5 || !reflect.DeepEqual(rows[1][2:7], []string{"temperatures", "150.00", "179.00", "76.00", "-1.00"}) || rows[2][7] != "Mash in" || rows[4][10] != "303" {
			t.Fatal(rows, err)
		}
	}
	// An entirely new session interrupts the previous journal. An error for
	// that older ID stays with that journal and cannot end the new session.
	next := start()
	wire("LogSession", url.Values{"code": {"2"}, "session": {sid}, "step": {checkpoint}, "data": {"2/200"}}, 404)
	if got := wire("recoversession", url.Values{"code": {"1"}, "session": {sid}}, 200); got != "#!#" {
		t.Fatal("stale recovery point", got)
	}
	errorReport(serial, sid, "302", 200)
	b = detail()
	if b.State != "interrupted" || b.Notes != "Keep these notes" || b.Rating != 4 || b.SampleCount != 5 {
		t.Fatal(b)
	}
	sid = next
	log("3", "", "", "")
	log("2", "2/200", "", "0")
	endpoint = h.URL + "/api/v1/brews/" + stockBrewID(next)
	if b = detail(); b.State != "ended" || len(b.Samples) != 0 {
		t.Fatal(b)
	}
	errorReport(serial, "", "301", 200)
	wire("FirstSetup", url.Values{"machine": {serial + "|a,1/b,2/c,3/d,4"}, "admin": {"0"}}, 200)
	wire("firstSetup", url.Values{"machine": {serial + "|x,1/y,1/z,3/w,4"}, "admin": {"0"}}, 400)
	code, body := request(t, c, "GET", h.URL+"/api/v1/stock-zymatics/"+serial+"/recipes", token, nil, nil)
	var menu struct{ Diagnostics stockDiagnostics }
	if code != 200 || json.Unmarshal(body, &menu) != nil || menu.Diagnostics.Sensors["4"] != "d" || menu.Diagnostics.RegisteredAt == nil || len(menu.Diagnostics.Errors) != 1 || menu.Diagnostics.Errors[0].Code != 301 {
		t.Fatal(code, string(body))
	}
}

func TestZymaticConcurrentReportsOnExistingSession(t *testing.T) {
	s, h, c := newTestServer(t)
	const serial = "LEGACY"
	sid := strings.Repeat("a", 32)
	// Records from the previous Hub did not have Started, recipe revision
	// or a journal. Preserve these unknowns across the first write.
	legacy := stockBrewer{Machine: serial, Session: stockSession{ID: sid, Program: domain.MachineRecipe{Name: "Old brew"}}}
	if _, err := s.db.Put(stockKind, serial, legacy, 0); err != nil {
		t.Fatal(err)
	}
	paths := []string{"sessionerror?machine=LEGACY&session=" + sid + "&errorcode=301"}
	for i := 0; i < 12; i++ {
		paths = append(paths, fmt.Sprintf("LogSession?session=%s&code=2&data=1%%2F199%%7C2%%2F%d%%7C3%%2F81%%7C4%%2F190&state=0", sid, i))
	}
	var wg sync.WaitGroup
	for _, path := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := c.Get(h.URL + "/API/" + path)
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("status %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
	b, err := s.brewDetail(stockBrewID(sid))
	if err != nil || b.StartedAt != nil || b.SampleCount != len(paths) || len(b.Samples) != len(paths) {
		t.Fatalf("%+v %v", b, err)
	}
	temps := map[int]bool{}
	reset := false
	for i, sample := range b.Samples {
		if i > 0 && b.Samples[i-1].ElapsedMS >= sample.ElapsedMS {
			t.Fatal("reports collided", b.Samples)
		}
		if sample.StockZymatic.Kind == "error" {
			reset = sample.StockZymatic.ErrorCode != nil && *sample.StockZymatic.ErrorCode == 301
		} else {
			temps[int(*sample.WortF)] = true
		}
	}
	if !reset || len(temps) != 12 {
		t.Fatal("missing report", temps, reset)
	}
}
