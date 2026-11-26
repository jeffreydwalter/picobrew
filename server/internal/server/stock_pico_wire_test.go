package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/catalog"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
)

func TestStockPicoWireProtocol(t *testing.T) {
	s, h, client := newTestServer(t)
	const uid = "0123456789abcdef0123456789abcdef"
	call := func(path string, code int) string {
		t.Helper()
		conn, err := net.Dial("tcp", h.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		// CC3200 sends CRLF on the request line and bare LF for headers.
		fmt.Fprintf(conn, "GET /API/pico/%s HTTP/1.0\r\nHost: picobrew.com\nConnection: close\n\n", path)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != code {
			t.Fatalf("%s returned %d: %s", path, resp.StatusCode, body)
		}
		return strings.TrimSpace(string(body))
	}
	for _, headers := range []map[string]string{{"Origin": "https://other.example"}, {"Sec-Fetch-Site": "cross-site"}} {
		if code, _ := request(t, client, "GET", h.URL+"/API/pico/register?uid="+uid, "", nil, headers); code != http.StatusForbidden {
			t.Fatal("unexpected browser registration response", code)
		}
	}
	if got := call("register?uid="+uid, 200); got != "#F:89abcdef#" {
		t.Fatal(got)
	}
	call("recipelist?uid="+uid, 404)
	call("register?uid="+uid+"&uid="+uid, 400)
	call("register?uid="+uid[:24], 400)
	profile, _ := s.selectProfile("pico-s")
	saved := validRecipe()
	saved.Revision = 3
	saved.ID = "pale-ale"
	item, err := compileStockPicoRecipe(saved, profile)
	if err != nil {
		t.Fatal(err)
	}
	item.RFID = "aabbccdd"
	b := stockPicoBrewer{UID: uid, Model: "pico-s", Name: "Pico", Items: []stockPicoItem{item}}
	if _, err = s.db.Put(stockPicoKind, uid, b, 0); err != nil {
		t.Fatal(err)
	}
	q := "?uid=" + uid
	if got := call("register"+q, 200); got != "#T#" {
		t.Fatal(got)
	}
	if got := call("checkFirmware"+q+"&version=0.1.34", 200); got != "#F#" {
		t.Fatal(got)
	}
	if got := call("getActionsNeeded"+q, 200); got != "##" {
		t.Fatal(got)
	}
	if got := call("recipelist"+q, 200); got != "#"+item.ID+",Test Pale|#" {
		t.Fatal(got)
	}
	if got := call("getAssociatedPaks"+q, 200); got != "#hub-"+item.ID+",Test Pale|#" {
		t.Fatal(got)
	}
	if got := call("createPicoPak"+q+"&recipeid="+item.ID, 200); got != "#hub-"+item.ID+"#" {
		t.Fatal(got)
	}
	if got := call("createPicoPak"+q+"&recipeid=0", 200); got != "##" {
		t.Fatal(got)
	}
	wire := call("getRecipe"+q+"&abv=-1.0&ibu=-1&rfid=hub-"+item.ID, 200)
	if !strings.HasPrefix(wire, "#Test Pale/-1,-1,0,0,") || !strings.HasSuffix(wire, ",|#") {
		t.Fatal(wire)
	}
	if got := call("getRecipe"+q+"&abv=-1.0&ibu=-1&rfid=AABBCCDD", 200); got != wire {
		t.Fatal(got)
	}
	for _, fields := range []string{"&abv=5.0&ibu=-1&rfid=aabbccdd", "&abv=-1&ibu=-1&rfid=00000001", "&abv=-1&ibu=-1&rfid=" + item.ID, "&abv=1.5&ibu=1&rfid=aabbccdd"} {
		if got := call("getRecipe"+q+fields, 200); got != "#Invalid|#" {
			t.Fatal(got)
		}
	}
	call("getRecipe?uid=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&abv=-1.0&ibu=-1&rfid=aabbccdd", 404)
	call("getFirmware"+q, 404)
	if got := call("hasCleanedAck?picoUid="+uid+"&picoStillUid=123", 200); got != "#F#" {
		t.Fatal(got)
	}
}

func TestStockPicoOriginalKitTimings(t *testing.T) {
	s, _, _ := newTestServer(t)
	saved, err := recipe.ImportPicoPak([]byte(`{"PicoRecipeID":420,"PicoBeerID":21,"Name":"Original kit","Abv":5,"Ibu":2,"Steps":[{"Name":"Mash","Action":0,"Location":2,"Temp":150,"Time":30,"Drain":2,"MinTime":20,"MaxTime":40},{"Name":"Hop","Action":0,"Location":3,"Temp":203,"Time":10,"Drain":2,"MinTime":18,"MaxTime":6}]}`))
	if err != nil {
		t.Fatal(err)
	}
	saved.ID = "kit-copy"
	saved.Revision = 2
	for _, model := range []string{"pico-s", "pico-pro", "pico-c"} {
		profile, _ := s.selectProfile(model)
		item, err := compileStockPicoRecipe(saved, profile)
		if err != nil {
			t.Fatal(model, err)
		}
		if item.ID != "420" || item.Revision != 2 {
			t.Fatal(item)
		}
		for _, test := range []struct{ abv, ibu, mash, hop int }{{-1, -1, 30, 10}, {5, 2, 30, 10}, {0, 0, 20, 18}, {10, 10, 40, 6}, {7, 5, 34, 9}} {
			query := url.Values{"rfid": {"hub-420"}, "abv": {strconv.Itoa(test.abv) + ".0"}, "ibu": {strconv.Itoa(test.ibu)}}
			got := stockPicoGetRecipe([]stockPicoItem{item}, query)
			want := fmt.Sprintf("#Original kit/5,2,0,0,150,%d,2,2,Mash,203,%d,2,3,Hop,|#", test.mash, test.hop)
			if got != want {
				t.Fatalf("%s: got %q want %q", model, got, want)
			}
		}
	}
}

func TestStockPicoProgramLimits(t *testing.T) {
	program := domain.MachineRecipe{ID: "test", Name: "Pale", Steps: []domain.MachineStep{{Name: "Mash", TargetF: 155, Location: 2, Minutes: 30}}}
	for _, name := range []string{"Pale/IPA", "Pale,IPA", "Pale#IPA", "Pale|IPA"} {
		p := program
		p.Name = name
		if _, err := (stockPicoItem{Program: p}).recipeRecord(-1, -1); err == nil {
			t.Fatal(name)
		}
	}
	long := program
	long.Steps = nil
	for i := 0; i < 15; i++ {
		long.Steps = append(long.Steps, domain.MachineStep{Name: strings.Repeat("x", 21), TargetF: 212, Location: 3, Minutes: 599, DrainMinutes: 120})
	}
	if _, err := (stockPicoItem{Program: long}).recipeRecord(-1, -1); err == nil {
		t.Fatal("accepted oversized recipe")
	}
	for _, step := range []domain.MachineStep{{Name: "Bad,field", Location: 2}, {Name: "Bad route", Location: 7}, {Name: "Temp", Location: 1, TargetF: 300}} {
		p := program
		p.Steps = []domain.MachineStep{step}
		if _, err := (stockPicoItem{Program: p}).recipeRecord(-1, -1); err == nil {
			t.Fatal(step)
		}
	}
}

func TestStockPicoSourceStats(t *testing.T) {
	a, err := catalog.Open()
	if err != nil {
		t.Fatal(err)
	}
	d, err := a.Detail("public-a3c65757fb284c14ba6fb8f924b3f879")
	if err != nil {
		t.Fatal(err)
	}
	copy := *d.Recipe
	copy.ID = "local-id"
	copy.Name = "Alpine Delight"
	abv, ibu := stockPicoSourceStats(copy)
	if abv == nil || ibu == nil || *abv != 5.5 || *ibu != 35 {
		t.Fatalf("expected original published statistics, got %v %v", abv, ibu)
	}
	p := stockPicoItem{Program: domain.MachineRecipe{ID: "test", Name: "Alpine Delight", Steps: []domain.MachineStep{{Name: "Mash", Location: 2, TargetF: 150, Minutes: 30}}}, ABV: abv, IBU: ibu}
	wire, err := p.recipeRecord(-1, -1)
	if err != nil || !strings.HasPrefix(wire, "Alpine Delight/-1,-1,5.5,35,") {
		t.Fatal(wire, err)
	}
	copy.BatchLiters *= 2
	if abv, ibu := stockPicoSourceStats(copy); abv != nil || ibu != nil {
		t.Fatal("retained statistics after the formulation changed")
	}
}
