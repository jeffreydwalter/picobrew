package device

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
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

func TestZGroupProgramWire(t *testing.T) {
	const parameters = `,"water_l":5.678115123456789,"temperature_f":194.72,"intensity":7,"time_seconds":420`
	for _, id := range []int{2, 3, 4, 5, 6, 7, 20, 21, 22, 23, 26, 27} {
		extra := ""
		if id >= 20 && id <= 23 {
			extra = parameters
		}
		data := fmt.Sprintf(`{"invite":{"id":"1234567890abcdef","action":"z_program","program_id":%d%s,"state":"pending"}}`, id, extra)
		var state GroupState
		if err := json.Unmarshal([]byte(data), &state); err != nil || state.Invite.ProgramID != id || state.Invite.Name() == "" {
			t.Fatalf("%s: %+v %v", data, state, err)
		}
	}
	const action = `"action":"z_program"`
	for _, fields := range []string{
		action, action + `,"program_id":null`, action + `,"program_id":2.0`, action + `,"program_id":0`,
		action + `,"program_id":1`, action + `,"program_id":12`, action + `,"program_id":13`, action + `,"program_id":14`,
		action + `,"program_id":24`, action + `,"program_id":25`, action + `,"program_id":28`, action + `,"program_id":29`,
		action + `,"program_id":2,"time_seconds":null`, action + `,"program_id":4,"water_l":4`,
		action + `,"program_id":2,"recipe_id":"slot"`, action + `,"program_id":6,"selected_ibu":0`,
		action + `,"program_id":20`, action + `,"program_id":23,"water_l":2,"intensity":1,"temperature_f":190,"time_seconds":60`,
		action + `,"program_id":23,"water_l":4,"intensity":16,"temperature_f":190,"time_seconds":60`,
		action + `,"program_id":23,"water_l":4,"intensity":1,"temperature_f":201,"time_seconds":60`,
		action + `,"program_id":23,"water_l":4,"intensity":1,"temperature_f":190,"time_seconds":61`,
		`"action":"rinse","program_id":0`, `"action":"brew","program_id":20` + parameters,
	} {
		var state GroupState
		if json.Unmarshal([]byte(`{"source":{`+fields+`}}`), &state) == nil {
			t.Error("unexpected acceptance", fields)
		}
	}
	var sent GroupOffer
	peer := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/group-brew" {
			t.Error(r.URL.Path)
		}
		if r.Method == "GET" {
			fmt.Fprint(w, `{"source":{"id":"1234567890abcdef","age_ms":10,"action":"z_program","program_id":20`+parameters+`},"invite":null}`)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, "{}")
	})
	state, err := peer.GroupState(context.Background())
	if err != nil || state.Source == nil {
		t.Fatal(state, err)
	}
	offer := GroupOffer{Op: "offer", From: "Other Z", ID: state.Source.ID, Action: state.Source.Action, GroupZProgram: state.Source.GroupZProgram}
	if err := peer.GroupRespond(context.Background(), offer); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sent, offer) || sent.WaterL == nil || *sent.WaterL != 5.678115123456789 || *sent.TemperatureF != 194.72 {
		t.Fatal(sent)
	}
}
