package device

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

// GroupKitSelection holds the adjustments chosen for this brew only. Zero is
// a valid setting; omitted values retain the selection in the saved slot.
type GroupKitSelection struct {
	SelectedABV *int `json:"selected_abv,omitempty"`
	SelectedIBU *int `json:"selected_ibu,omitempty"`
}

func (s GroupKitSelection) Present() bool { return s.SelectedABV != nil || s.SelectedIBU != nil }

// Z owner-program IDs correspond to the normal stock group-session chooser.
// Factory tests, the demonstration and Manual Brew are excluded. Rinse and
// Clean use their existing cross-family actions.
var groupZProgramNames = map[int]string{
	2: "Drain", 3: "Rack beer", 4: "Circulate", 5: "Sous vide without keg",
	6: "Sous vide with keg", 7: "PicoStill manual", 20: "Cold brew coffee",
	21: "Hybrid coffee", 22: "Hot coffee", 23: "Custom coffee",
	26: "PicoStill boil point", 27: "Chill",
}

type GroupZProgram struct {
	ProgramID    int      `json:"program_id,omitempty"`
	WaterL       *float64 `json:"water_l,omitempty"`
	TemperatureF *float64 `json:"temperature_f,omitempty"`
	Intensity    *int     `json:"intensity,omitempty"`
	TimeSeconds  *int     `json:"time_seconds,omitempty"`
}

func (p GroupZProgram) Name() string { return groupZProgramNames[p.ProgramID] }

func (p GroupZProgram) ValidFor(action string) bool {
	if action != "z_program" {
		return p == (GroupZProgram{})
	}
	if p.Name() == "" {
		return false
	}
	if p.ProgramID < 20 || p.ProgramID > 23 {
		return p.WaterL == nil && p.TemperatureF == nil && p.Intensity == nil && p.TimeSeconds == nil
	}
	return p.WaterL != nil && *p.WaterL >= 3.78541-1e-9 && *p.WaterL <= 15.898722+1e-9 &&
		p.TemperatureF != nil && *p.TemperatureF >= 100 && *p.TemperatureF <= 200 &&
		p.Intensity != nil && *p.Intensity >= 1 && *p.Intensity <= 15 &&
		p.TimeSeconds != nil && *p.TimeSeconds >= 60 && *p.TimeSeconds <= 5400 && *p.TimeSeconds%60 == 0
}

type GroupSource struct {
	GroupZProgram
	GroupKitSelection
	HistoryID uint64  `json:"history_id,omitempty"`
	ID        string  `json:"id"`
	AgeMS     *uint32 `json:"age_ms"`
	Action    string  `json:"action"`
	RecipeID  string  `json:"recipe_id,omitempty"`
	Revision  uint32  `json:"revision,omitempty"`
}
type GroupOffer struct {
	GroupZProgram
	GroupKitSelection
	TTLMS    uint32 `json:"ttl_ms,omitempty"`
	Op       string `json:"op,omitempty"`
	ID       string `json:"id"`
	From     string `json:"from,omitempty"`
	Action   string `json:"action,omitempty"`
	RecipeID string `json:"recipe_id,omitempty"`
	Revision uint32 `json:"revision,omitempty"`
	State    string `json:"state,omitempty"`
}
type GroupState struct {
	Source *GroupSource `json:"source"`
	Invite *GroupOffer  `json:"invite"`
}

// A malformed extension must never become an invitation to run the defaults.
// Older firmware omits the fields entirely.
func (s *GroupState) UnmarshalJSON(data []byte) error {
	type state GroupState
	var value state
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var raw struct{ Source, Invite map[string]json.RawMessage }
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	check := func(fields map[string]json.RawMessage, action string, kit GroupKitSelection, program GroupZProgram) bool {
		for _, key := range []string{"selected_abv", "selected_ibu", "program_id", "water_l", "temperature_f", "intensity", "time_seconds"} {
			if string(fields[key]) == "null" {
				return false
			}
		}
		for _, index := range []*int{kit.SelectedABV, kit.SelectedIBU} {
			if index != nil && (action != "brew" || *index < 0 || *index > 10) {
				return false
			}
		}
		if action == "z_program" && (fields["recipe_id"] != nil || fields["revision"] != nil) {
			return false
		}
		_, hasID := fields["program_id"]
		return hasID == (action == "z_program") && program.ValidFor(action)
	}
	if src := value.Source; src != nil && !check(raw.Source, src.Action, src.GroupKitSelection, src.GroupZProgram) {
		return errors.New("invalid group source program settings")
	}
	if i := value.Invite; i != nil && !check(raw.Invite, i.Action, i.GroupKitSelection, i.GroupZProgram) {
		return errors.New("invalid group invitation settings")
	}
	*s = GroupState(value)
	return nil
}

type GroupBrewer interface {
	GroupState(context.Context) (GroupState, error)
	GroupRespond(context.Context, GroupOffer) error
	GroupRecipe(context.Context, string) (domain.MachineRecipe, uint32, error)
}

func (p *pico) GroupState(ctx context.Context) (v GroupState, err error) {
	if err = p.lock(ctx); err != nil {
		return
	}
	defer p.unlock()
	err = p.request(ctx, http.MethodGet, "/api/v1/group-brew", nil, &v, 200)
	return
}
func (p *pico) GroupRespond(ctx context.Context, v GroupOffer) error {
	if !v.GroupZProgram.ValidFor(v.Action) || v.Action == "z_program" && (v.Op != "offer" || v.GroupKitSelection.Present() || v.RecipeID != "" || v.Revision != 0) {
		return invalid("Invalid Z group program settings")
	}
	if err := p.lock(ctx); err != nil {
		return err
	}
	defer p.unlock()
	return p.request(ctx, http.MethodPost, "/api/v1/group-brew", v, nil, 200)
}
func (p *pico) GroupRecipe(ctx context.Context, id string) (domain.MachineRecipe, uint32, error) {
	if err := p.lock(ctx); err != nil {
		return domain.MachineRecipe{}, 0, err
	}
	defer p.unlock()
	slot, err := p.readRecipeSlot(ctx, id)
	return slot.MachineRecipe, slot.Revision, err
}

// CC3200 group starts reuse the brewer's ordinary command path. A changed
// slot revision is rejected by firmware before the command can be queued.
type GuardedGroupStarter interface {
	StartGroupAction(context.Context, string, string, uint32) error
}

func (p *pico) StartGroupAction(ctx context.Context, action, id string, revision uint32) error {
	args := map[string]any{"command": action}
	switch {
	case action == "brew" && recipeID.MatchString(id) && revision > 0:
		args["recipe_id"] = id
		args["if_revision"] = revision
	case action == "rinse" && id == "" && revision == 0:
		args["group_join"] = true
	default:
		return invalid("A saved recipe revision or rinse is required")
	}

	if err := p.lock(ctx); err != nil {
		return err
	}
	defer p.unlock()
	return p.request(ctx, http.MethodPost, "/api/v1/commands", args, nil, 202)
}
