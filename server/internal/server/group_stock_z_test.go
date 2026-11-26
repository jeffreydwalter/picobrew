package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
)

func TestStockZGroupOfferAndRecipientConsent(t *testing.T) {
	s, h, c := newTestServer(t)
	const sourceID, targetID = "stock-z:source", "stock-z:target"
	source := newStockZTestPeer(t, s)
	openSource := s.stockZOpen
	target := newStockZTestPeer(t, s)
	target.address = netip.MustParseAddr("192.168.5.38")
	openTarget := s.stockZOpen
	s.stockZOpen = func(address string) (stockZEndpoint, error) {
		if address == "http://"+source.address.String() {
			return openSource(address)
		}
		return openTarget(address)
	}
	for id, ip := range map[string]string{"source": source.address.String(), "target": target.address.String(), "outsider": "192.168.5.39"} {
		if _, err := s.db.Put(stockZProgramKind, id, stockZProgramMachine{ID: id, Name: id, BaseURL: "http://" + ip}, 0); err != nil {
			t.Fatal(err)
		}
	}
	items, err := recipe.Import([]byte(stockZTestProgram), "json")
	if err != nil {
		t.Fatal(err)
	}
	r := items[0]
	r.ID, r.Revision = "group-library", 1
	if _, err := s.db.Put("recipe", r.ID, r, 0); err != nil {
		t.Fatal(err)
	}
	base := h.URL + "/api/v1"
	csrf := signIn(t, c, h.URL)
	post := func(path string, in any, want int) []byte {
		t.Helper()
		code, body := request(t, c, http.MethodPost, base+path, csrf, in, nil)
		if code != want {
			t.Fatalf("%s: got %d want %d: %s", path, code, want, body)
		}
		return body
	}
	var g brewGroup
	if err := json.Unmarshal(post("/brew-groups", map[string]any{"name": "Side by side", "device_ids": []string{sourceID, targetID}}, 200), &g); err != nil {
		t.Fatal(err)
	}
	const stage = "/stock-z-programs/source/recipes"
	const offer = "/stock-z-programs/source/group-offers"
	args := map[string]any{"transfer_id": "unacknowledged", "group_id": g.ID, "group_revision": g.Revision}
	post(offer, args, 409)
	send := map[string]any{"recipe_id": r.ID, "revision": r.Revision}
	var preview stockZProgramReview
	if err := json.Unmarshal(post(stage, send, 200), &preview); err != nil {
		t.Fatal(err)
	}
	send["program_hash"], send["review_hash"], send["confirm_replace"] = preview.ProgramHash, preview.ReviewHash, true
	var receipt struct {
		ID string `json:"transfer_id"`
	}
	if err := json.Unmarshal(post(stage, send, 200), &receipt); err != nil || receipt.ID == "" {
		t.Fatalf("missing transfer receipt %v", err)
	}
	if i, _ := s.memberInvite(targetID); i != nil {
		t.Fatal("ordinary transfer invited another machine")
	}
	args["transfer_id"] = receipt.ID
	args["group_revision"] = g.Revision + 1
	post(offer, args, 409)
	args["group_revision"] = g.Revision
	result := post(offer, args, 200)
	if !strings.Contains(string(result), `"offered":1`) || len(target.reads) != 0 || len(target.posts) != 0 {
		t.Fatalf("offer %s touched recipient", result)
	}
	if outside, err := s.memberInvite("stock-z:outsider"); err != nil || outside != nil {
		t.Fatal("unselected Z received invitation")
	}
	i, err := s.memberInvite(targetID)
	if err != nil || i == nil || i.State != "pending" || i.StockZURL != "http://"+target.address.String() || i.StockZReview == nil || i.StockZReview.ReviewHash != preview.ReviewHash {
		t.Fatalf("unexpected invite: %+v, %v", i, err)
	}
	respond := "/devices/" + targetID + "/brew-invite/" + i.ID + "/respond"
	post(respond, map[string]bool{"accept": true}, 422)
	if len(target.reads) != 0 || len(target.posts) != 0 {
		t.Fatal("unconfirmed replacement contacted receiver")
	}
	answer := map[string]bool{"accept": true, "confirm_replace": true}
	post(respond, answer, 200)
	post(respond, answer, 409)
	post(offer, args, 200)
	finished, err := s.memberInvite(targetID)
	if err != nil || finished == nil || finished.ID != i.ID || finished.State != "accepted" || len(source.posts) != 1 || len(target.posts) != 1 || target.posts[0] != source.posts[0] {
		t.Fatalf("replayed offer/answer: %+v, %v, posts: %d/%d", finished, err, len(source.posts), len(target.posts))
	}
	source.lost = true
	post(stage, send, 502)
	post(offer, args, 409)
}

// A standalone start identifies an on-device program. It can be offered to an
// original Z only when a verified transfer ties it to a saved library revision.
func TestStandaloneBrewOffersReviewedRecipeToStockZ(t *testing.T) {
	s, h, c := newTestServer(t)
	peer := newStockZTestPeer(t, s)
	d, wire := groupTestBrewer(t, s, "source", "pico-s")
	m := stockZProgramMachine{ID: "receiver", Name: "Factory Z", BaseURL: "http://" + peer.address.String()}
	if _, err := s.db.Put(stockZProgramKind, m.ID, m, 0); err != nil {
		t.Fatal(err)
	}
	token := signIn(t, c, h.URL)
	base := h.URL + "/api/v1"
	target := stockZMemberPrefix + m.ID
	code, b := request(t, c, "POST", base+"/brew-groups", token, map[string]any{"name": "Comparisons", "device_ids": []string{"source", target}}, nil)
	if code != 200 {
		t.Fatalf("group %d %s", code, b)
	}
	library := validRecipe()
	library.ID, library.Revision = "ingredient-group", 1
	p, _ := s.selectProfile("pico-s")
	plan, err := recipe.Translate(library, p, 0)
	if err != nil || plan.MachineRecipe == nil {
		t.Fatal(err)
	}
	wire.slots[plan.MachineRecipe.ID] = *plan.MachineRecipe
	if _, err = s.db.Put("recipe", library.ID, library, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Put("deployment", "receipt", groupReceipt{"source", wire.st.DeviceUID, library.ID, library.Revision, *plan.MachineRecipe, "verified"}, 0); err != nil {
		t.Fatal(err)
	}
	age := uint32(0)
	wire.state.Source = &device.GroupSource{ID: "1234567890abcdef", AgeMS: &age, Action: "brew", RecipeID: plan.MachineRecipe.ID, Revision: wire.rev}
	if err = s.syncBrewGroup(context.Background(), d, wire.st); err != nil {
		t.Fatal(err)
	}
	invite, err := s.memberInvite(target)
	if err != nil || invite == nil || invite.State != "pending" || invite.StockZReview == nil || len(invite.StockZReview.Ingredients) == 0 || len(peer.reads) != 0 {
		t.Fatalf("group preview: %+v, %v", invite, err)
	}
	if _, err := s.db.Put("profile", "z", profileCalibration{BatchLiters: 3, Efficiency: 65, MaxGrainKG: 0.25}, 0); err != nil {
		t.Fatal(err)
	}
	respond := base + "/devices/" + target + "/brew-invite/" + invite.ID + "/respond"
	code, b = request(t, c, "POST", respond, token, map[string]bool{"accept": true, "confirm_replace": true}, nil)
	if code != 409 || !strings.Contains(string(b), "recipe_changed") || len(peer.reads) != 0 || len(peer.posts) != 0 {
		t.Fatalf("accepted different load %d %s", code, b)
	}
	code, b = request(t, c, "POST", respond, token, map[string]bool{"accept": false}, nil)
	if code != 200 || !strings.Contains(string(b), "declined") || len(peer.reads) != 0 {
		t.Fatalf("decline %d %s", code, b)
	}
}
