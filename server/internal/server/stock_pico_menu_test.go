package server

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

const picoMenuUID = "0123456789abcdef0123456789abcdef"

type picoMenuAPI struct {
	t     *testing.T
	c     *http.Client
	url   string
	token string
}

func (a picoMenuAPI) call(method, path string, body any, want int) []byte {
	a.t.Helper()
	code, b := request(a.t, a.c, method, a.url+path, a.token, body, nil)
	if code != want {
		a.t.Fatalf("%s %s: got %d, want %d: %s", method, path, code, want, b)
	}
	return b
}

func (a picoMenuAPI) register(uid, model string) {
	a.t.Helper()
	a.call("POST", "/api/v1/stock-picos", map[string]string{"uid": uid, "model": model, "name": "Kitchen Pico"}, 201)
}

func (a picoMenuAPI) save(r domain.Recipe) domain.Recipe {
	a.t.Helper()
	method, path, status := "POST", "/api/v1/recipes", 201
	if r.ID != "" {
		method, path, status = "PUT", path+"/"+r.ID, 200
	}
	var saved domain.Recipe
	b := a.call(method, path, r, status)
	if err := json.Unmarshal(b, &saved); err != nil || saved.ID == "" || saved.Revision != r.Revision+1 {
		a.t.Fatalf("invalid saved recipe: %s; %v", b, err)
	}
	return saved
}

type picoMenuRef struct {
	ID       string  `json:"recipe_id"`
	Revision int64   `json:"revision"`
	RFID     *string `json:"rfid,omitempty"`
}

type picoMenuResult struct {
	Hash      string          `json:"review_hash"`
	Published bool            `json:"published"`
	Items     []stockPicoItem `json:"items"`
}

func (a picoMenuAPI) submit(uid string, refs []picoMenuRef, publish bool, hash string, want int) picoMenuResult {
	a.t.Helper()
	b := a.call("POST", "/api/v1/stock-picos/"+uid+"/recipes", map[string]any{"recipes": refs, "publish": publish, "review_hash": hash}, want)
	var result picoMenuResult
	if want == 200 {
		err := json.Unmarshal(b, &result)
		digest, decodeErr := hex.DecodeString(result.Hash)
		if err != nil || decodeErr != nil || len(digest) != 32 || result.Published != publish || len(result.Items) != len(refs) {
			a.t.Fatalf("invalid transfer result: %s", b)
		}
	}
	return result
}

func (a picoMenuAPI) publish(uid string, refs []picoMenuRef) picoMenuResult {
	a.t.Helper()
	preview := a.submit(uid, refs, false, "", 200)
	return a.submit(uid, refs, true, preview.Hash, 200)
}

func (a picoMenuAPI) wire(uid, opAndQuery string) string {
	a.t.Helper()
	op, q, _ := strings.Cut(opAndQuery, "?")
	path := "/API/pico/" + op + "?uid=" + uid
	if q != "" {
		path += "&" + q
	}
	return strings.TrimSpace(string(a.call("GET", path, nil, 200)))
}

func TestStockPicoOwnerMenuRoundTripAndRestart(t *testing.T) {
	data := newTestDataDir(t)
	const other = "12341234123412341234123412341234"
	numericID, recipeID := "", ""
	t.Run("preview publish edit and tag", func(t *testing.T) {
		_, h, c := newTestServerIn(t, data)
		a := picoMenuAPI{t: t, c: c, url: h.URL}
		a.call("GET", "/api/v1/stock-picos", nil, 401)
		a.call("POST", "/api/v1/stock-picos/"+picoMenuUID+"/recipes", map[string]any{}, 401)
		a.token = signIn(t, c, h.URL)
		if got := a.wire(picoMenuUID, "register"); got != "#F:89abcdef#" {
			t.Fatal("expected pending registration", got)
		}
		var fleet struct {
			Machines []stockPicoBrewer `json:"machines"`
			Recent   []string          `json:"recent_uids"`
		}
		if err := json.Unmarshal(a.call("GET", "/api/v1/stock-picos", nil, 200), &fleet); err != nil || len(fleet.Machines) != 0 || len(fleet.Recent) != 1 || fleet.Recent[0] != picoMenuUID {
			t.Fatal("unregistered machine was not shown as pending", fleet, err)
		}
		a.register(strings.ToUpper(picoMenuUID), "pico-pro")
		a.register(other, "pico-c")
		if got := a.wire(picoMenuUID, "register"); got != "#T#" {
			t.Fatal("registration", got)
		}
		r := a.save(validRecipe())
		recipeID = r.ID
		tag := "AABBCCDD"
		refs := []picoMenuRef{{r.ID, r.Revision, &tag}}
		preview := a.submit(picoMenuUID, refs, false, "", 200)
		item := preview.Items[0]
		numericID = item.ID
		if item.RecipeID != r.ID || item.Revision != 1 || item.RFID != "aabbccdd" || item.Program.Name != r.Name || item.BatchLiters <= 0 || len(item.Program.Steps) == 0 {
			t.Fatal("incomplete review", item)
		}
		grain := r.Ingredients[0]
		grain.Amount *= item.BatchLiters / r.BatchLiters
		if len(item.Ingredients) != 1 || item.Ingredients[0] != grain {
			t.Fatal("preview did not carry the translated grain loading", item.Ingredients)
		}
		if got := a.wire(picoMenuUID, "recipelist"); got != "##" {
			t.Fatal("preview changed the brewer", got)
		}
		a.submit(picoMenuUID, refs, true, "", 409)
		a.submit(other, refs, true, preview.Hash, 409)
		tag = "11223344"
		a.submit(picoMenuUID, refs, true, preview.Hash, 409)
		tag = "AABBCCDD"
		a.submit(picoMenuUID, refs, true, preview.Hash, 200)
		fetch := "getRecipe?abv=-1.0&ibu=-1&rfid="
		wire := a.wire(picoMenuUID, fetch+tag)
		if !strings.HasPrefix(wire, "#Test Pale/") || a.wire(picoMenuUID, fetch+"hub-"+numericID) != wire || a.wire(other, fetch+tag) != "#Invalid|#" {
			t.Fatal("tag lookup must match the selected brewer's menu recipe", wire)
		}

		r.Name = "Renamed Pale"
		r = a.save(r)
		a.submit(picoMenuUID, refs, true, preview.Hash, 409)
		if got := a.wire(picoMenuUID, fetch+tag); got != wire {
			t.Fatal("an unpublished library edit changed the stock recipe", got)
		}
		refs[0] = picoMenuRef{ID: r.ID, Revision: r.Revision}
		updated := a.publish(picoMenuUID, refs).Items[0]
		if updated.ID != numericID || updated.Revision != 2 || updated.RFID != "aabbccdd" || updated.Program.Name != r.Name {
			t.Fatal("renaming changed the lookup identity", updated)
		}
	})
	t.Run("reopen remove mapping and brewer", func(t *testing.T) {
		_, h, c := newTestServerIn(t, data)
		a := picoMenuAPI{t: t, c: c, url: h.URL, token: signIn(t, c, h.URL)}
		wantList := "#" + numericID + ",Renamed Pale|#"
		if got := a.wire(picoMenuUID, "recipelist"); got != wantList {
			t.Fatalf("reopened menu %q, want %q", got, wantList)
		}
		fetch := "getRecipe?abv=-1.0&ibu=-1&rfid="
		if got := a.wire(picoMenuUID, fetch+"AABBCCDD"); !strings.HasPrefix(got, "#Renamed Pale/") {
			t.Fatal("reopened brewer tag", got)
		}
		empty := ""
		a.publish(picoMenuUID, []picoMenuRef{{ID: recipeID, Revision: 2, RFID: &empty}})
		if got := a.wire(picoMenuUID, fetch+"AABBCCDD"); got != "#Invalid|#" || a.wire(picoMenuUID, "recipelist") != wantList {
			t.Fatal("clearing a tag removed the menu entry or retained the physical mapping", got)
		}
		machine := "/api/v1/stock-picos/" + picoMenuUID
		a.call("DELETE", machine+"/recipes/"+recipeID, nil, 204)
		if got := a.wire(picoMenuUID, "recipelist"); got != "##" || a.wire(picoMenuUID, fetch+"hub-"+numericID) != "#Invalid|#" {
			t.Fatal("removed stock recipe still served", got)
		}
		a.call("GET", "/api/v1/recipes/"+recipeID, nil, 200)
		a.call("DELETE", machine, nil, 204)
		a.call("GET", "/API/pico/recipelist?uid="+picoMenuUID, nil, 404)
		if got := a.wire(picoMenuUID, "register"); got != "#F:89abcdef#" {
			t.Fatal("removed stock brewer still registered", got)
		}
	})
}

func TestStockPicoOwnerMenuAtomicBatchAndConflicts(t *testing.T) {
	_, h, c := newTestServer(t)
	a := picoMenuAPI{t: t, c: c, url: h.URL, token: signIn(t, c, h.URL)}
	a.register(picoMenuUID, "pico-s")
	pale := a.save(validRecipe())
	amber := validRecipe()
	amber.Name = "Amber Ale"
	amber = a.save(amber)
	paleTag, amberTag := "aabbccdd", "11223344"
	first := picoMenuRef{pale.ID, 1, &paleTag}
	next := picoMenuRef{amber.ID, 1, &amberTag}
	a.submit("11111111111111111111111111111111", []picoMenuRef{first}, false, "", 404)
	a.submit("invalid", []picoMenuRef{first}, false, "", 404)
	for _, invalid := range []struct {
		refs []picoMenuRef
		code int
	}{
		{nil, 422},
		{[]picoMenuRef{first, {ID: "", Revision: 1}}, 422},
		{[]picoMenuRef{first, {ID: amber.ID}}, 422},
		{[]picoMenuRef{first, first}, 422},
		{[]picoMenuRef{first, {ID: "missing", Revision: 1}}, 404},
		{[]picoMenuRef{first, {ID: amber.ID, Revision: 2}}, 409},
		{[]picoMenuRef{first, {ID: amber.ID, Revision: 1, RFID: &paleTag}}, 422},
	} {
		a.submit(picoMenuUID, invalid.refs, true, "", invalid.code)
	}
	invalidTag := "123Z"
	a.submit(picoMenuUID, []picoMenuRef{first, {amber.ID, 1, &invalidTag}}, true, "", 422)
	if got := a.wire(picoMenuUID, "recipelist"); got != "##" {
		t.Fatal("partial batch assignment", got)
	}

	stale := a.submit(picoMenuUID, []picoMenuRef{next}, false, "", 200)
	a.publish(picoMenuUID, []picoMenuRef{first})
	a.submit(picoMenuUID, []picoMenuRef{next}, true, stale.Hash, 409)
	both := []picoMenuRef{first, next}
	published := a.publish(picoMenuUID, both)
	want := "#" + published.Items[0].ID + ",Test Pale|" + published.Items[1].ID + ",Amber Ale|#"
	if got := a.wire(picoMenuUID, "recipelist"); got != want {
		t.Fatalf("joint publish returned menu %q, want %q", got, want)
	}

	// A collision during an independent update leaves the original map intact.
	// Reassigning both tags together is valid once the whole batch is considered.
	both[0].RFID, both[1].RFID = &amberTag, &paleTag
	a.submit(picoMenuUID, both[:1], true, "", 422)
	a.publish(picoMenuUID, both)
	if got := a.wire(picoMenuUID, "getRecipe?abv=-1&ibu=-1&rfid="+paleTag); !strings.HasPrefix(got, "#Amber Ale/") {
		t.Fatal("joint RFID exchange lost the association", got)
	}
	amber.Name = pale.Name
	amber = a.save(amber)
	both[1].Revision = amber.Revision
	a.submit(picoMenuUID, both, true, "", 422)
	if got := a.wire(picoMenuUID, "recipelist"); got != want {
		t.Fatal("name collision overwrote a published entry", got)
	}
}
