package device

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestAuthenticatedGroupKitSettings(t *testing.T) {
	const source = `{"id":"1234567890abcdef","action":"brew","age_ms":0,"recipe_id":"kit","revision":12,"selected_abv":0,"selected_ibu":10}`
	wire := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/group-brew" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Method == "GET" {
			fmt.Fprintf(w, `{"source":%s,"invite":null}`, source)
			return
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		if got["op"] != "offer" || got["recipe_id"] != "kit" || got["selected_abv"] != float64(0) || got["selected_ibu"] != float64(10) {
			t.Errorf("wrong wire offer %+v", got)
		}
		fmt.Fprint(w, "{}")
	})
	ctx := context.Background()
	result, err := wire.GroupState(ctx)
	if err != nil || result.Source == nil || result.Source.SelectedABV == nil || *result.Source.SelectedABV != 0 || result.Source.SelectedIBU == nil || *result.Source.SelectedIBU != 10 {
		t.Fatal(result, err)
	}
	if err = wire.GroupRespond(ctx, GroupOffer{GroupKitSelection: result.Source.GroupKitSelection, Op: "offer", Action: "brew", ID: "0000000000000001", RecipeID: "kit", Revision: 12}); err != nil {
		t.Fatal(err)
	}
	for _, frame := range []string{
		`{"source":{"action":"brew","selected_abv":null}}`, `{"invite":{"action":"brew","selected_ibu":false}}`,
		`{"source":{"action":"brew","selected_abv":11}}`, `{"invite":{"action":"brew","selected_ibu":1.5}}`,
		`{"source":{"action":"clean","selected_abv":0}}`,
	} {
		var invalid GroupState
		if json.Unmarshal([]byte(frame), &invalid) == nil {
			t.Errorf("accepted invalid group frame %s", frame)
		}
	}
}
