package server

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

func TestStockPicoWireJournal(t *testing.T) {
	s, hub, c := newTestServer(t)
	uid, other := strings.Repeat("a", 32), strings.Repeat("b", 32)
	for _, id := range []string{uid, other} {
		if _, err := s.db.Put(stockPicoKind, id, stockPicoBrewer{UID: id, Model: "pico-pro", Name: "Kitchen Pico"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	wire := func(op, query string, want int) string {
		t.Helper()
		code, body := request(t, c, "GET", hub.URL+"/API/pico/"+op+"?"+query, "", nil, nil)
		if code != want {
			t.Fatalf("%s %d: %s", op, code, body)
		}
		return strings.TrimSpace(string(body))
	}
	// These paths and parameters are emitted by both stock network/api.c implementations.
	start := func(kind int) string {
		t.Helper()
		id := wire("getSession", fmt.Sprintf("uid=%s&sesType=%d", uid, kind), 200)
		if !strings.HasPrefix(id, "#") || !strings.HasSuffix(id, "#") || len(id) < 3 || len(id) > 23 {
			t.Fatal(id)
		}
		return strings.Trim(id, "#")
	}
	wire("error", "uid="+uid+"&code=13&rfid=", 200)
	brews, err := s.db.List("brew")
	if err != nil || len(brews) != 0 {
		t.Fatal(brews, err)
	}
	sid := start(0)
	if start(0) != sid {
		t.Fatal("retry allocated second session")
	}
	log := func(id, owner, rest string, want int) {
		t.Helper()
		wire("log", "uid="+owner+"&sesId="+id+"&wort=151&therm=199&error=0&sesType=0&timeLeft=63&shutScale=0.25&"+rest, want)
	}
	log(sid, other, "step=Mash", 404)
	wire("picoChangeState", "picoUID="+uid+"&state=3", 200)
	log(sid, uid, "step=Mash%20in&event=Mash%20in", 200)
	log(sid, uid, "step=Mash%20in", 200)
	wire("error", "uid="+uid+"&code=14&rfid=12345", 200)
	id := stockPicoJournalID(uid, sid)
	var saved domain.Brew
	version, err := s.db.Get("brew", id, &saved)
	if err != nil {
		t.Fatal(err)
	}
	saved.Notes = "Owner note"
	if _, err = s.db.Put("brew", id, saved, version); err != nil {
		t.Fatal(err)
	}
	log(sid, uid, "step=Brew%20Complete&event=Brew%20Complete", 200)
	log(sid, uid, "step=Brew%20Complete&event=Brew%20Complete", 200)
	wire("picoChangeState", "picoUID="+uid+"&state=2", 200)
	wire("error", "uid="+uid+"&code=7&rfid=12345", 200)
	b, err := s.brewDetail(id)
	if err != nil || b.State != "completed" || b.SampleCount != 5 || len(b.Samples) != 5 || b.DeviceName != "Kitchen Pico" || b.Notes != "Owner note" || b.RecipeName != "" || b.StockSerial != uid {
		t.Fatal(b, err)
	}
	p := b.Samples[1]
	if p.StockPico.Step != "Mash in" || p.StockPico.Event != "Mash in" || *p.WortF != 151 || *p.ThermoblockF != 199 || *p.StockPico.SecondsLeft != 63 || *p.StockPico.ShuttleScale != 0.25 || b.Samples[3].StockPico.RFID != "12345" || *b.Samples[3].StockPico.ErrorCode != 14 {
		t.Fatal(b.Samples)
	}
	for i := 1; i < len(b.Samples); i++ {
		if b.Samples[i].ElapsedMS <= b.Samples[i-1].ElapsedMS {
			t.Fatal(b.Samples)
		}
	}
	raw, _ := json.Marshal(b.Samples[0])
	if strings.Contains(string(raw), "controller_status") || strings.Contains(string(raw), "wort_f") {
		t.Fatal(string(raw))
	}
	var status stockPicoReportStatus
	if _, err = s.db.Get(stockPicoReportKind, uid, &status); err != nil || *status.LastState != 2 || *status.LastError.ErrorCode != 7 {
		t.Fatal(status, err)
	}
	signIn(t, c, hub.URL)
	code, data := request(t, c, "GET", hub.URL+"/api/v1/stock-picos", "", nil, nil)
	var devices struct {
		Machines []stockPicoWithReport `json:"machines"`
	}
	if code != 200 || json.Unmarshal(data, &devices) != nil || len(devices.Machines) != 2 {
		t.Fatalf("%d %s", code, data)
	}
	for _, machine := range devices.Machines {
		if machine.UID == uid && (machine.Report == nil || *machine.Report.LastState != 2 || *machine.Report.LastError.ErrorCode != 7) {
			t.Fatal(machine)
		}
		if machine.UID == other && machine.Report != nil {
			t.Fatal(machine)
		}
	}
	code, data = request(t, c, "GET", hub.URL+"/api/v1/brews/"+id+"/export?format=csv", "", nil, nil)
	if code != 200 {
		t.Fatalf("export %d %s", code, data)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(rows) != 6 || rows[2][3] != "151.00" || rows[2][5] != "Mash in" || rows[1][3] != "" {
		t.Fatal(rows, err)
	}
	// An idle transition alone has no measured outcome; a replacement marks
	// the previous active session interrupted while preserving its data.
	idle := start(5)
	wire("picoChangeState", "picoUID="+uid+"&state=2", 200)
	if b, err = s.brewDetail(stockPicoJournalID(uid, idle)); err != nil || b.State != "ended" {
		t.Fatal(b, err)
	}
	manual := start(5)
	wire("picoChangeState", "picoUID="+uid+"&state=3", 200)
	fresh := start(0)
	if b, err = s.brewDetail(stockPicoJournalID(uid, manual)); err != nil || b.State != "interrupted" {
		t.Fatal(b, err)
	}
	log(manual, uid, "step=Mash", 404)
	log(fresh, uid, "step=Brew%20Canceled&event=Brew%20Canceled", 200)
	if b, err = s.brewDetail(stockPicoJournalID(uid, fresh)); err != nil || b.State != "canceled" {
		t.Fatal(b, err)
	}
}

func TestStockPicoStillReportFields(t *testing.T) {
	// The stock brewer forwards T1 and T2 in the accessory's own temperature
	// unit; the log provides no unit selector. Keep the original values.
	query, err := url.ParseQuery("step=Still&sesType=3&wort=124&therm=170&error=1&timeLeft=-1&shutScale=0.20&t1=91.1&t2=60.0&p=3.25")
	if err != nil {
		t.Fatal(err)
	}
	p, ok := stockPicoSample("log", query, 3)
	if !ok || *p.Readings["sensor_still_t1"] != 91.1 || *p.Readings["sensor_still_pressure"] != 3.25 {
		t.Fatal(p)
	}
	query.Set("p", "NaN")
	if _, ok = stockPicoSample("log", query, 3); ok {
		t.Fatal("accepted invalid pressure")
	}
	if _, ok = stockPicoSample("log", query, 0); ok {
		t.Fatal("accepted different session type")
	}
}
