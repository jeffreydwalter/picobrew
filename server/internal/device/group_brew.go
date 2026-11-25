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

type GroupSource struct {
	GroupKitSelection
	HistoryID uint64  `json:"history_id,omitempty"`
	ID        string  `json:"id"`
	AgeMS     *uint32 `json:"age_ms"`
	Action    string  `json:"action"`
	RecipeID  string  `json:"recipe_id,omitempty"`
	Revision  uint32  `json:"revision,omitempty"`
}
type GroupOffer struct {
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

// An explicit null or malformed index must never turn a one-brew setting
// into a baseline brew. Older firmware omits the fields altogether.
func (s *GroupState) UnmarshalJSON(data []byte) error {
	type selection struct {
		Action string          `json:"action"`
		ABV    json.RawMessage `json:"selected_abv"`
		IBU    json.RawMessage `json:"selected_ibu"`
	}
	var fields struct{ Source, Invite *selection }
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, frame := range []*selection{fields.Source, fields.Invite} {
		if frame == nil {
			continue
		}
		for _, value := range []json.RawMessage{frame.ABV, frame.IBU} {
			if value == nil {
				continue
			}
			var index *int
			if frame.Action != "brew" || json.Unmarshal(value, &index) != nil || index == nil || *index < 0 || *index > 10 {
				return errors.New("invalid group brew kit setting")
			}
		}
	}
	type state GroupState
	return json.Unmarshal(data, (*state)(s))
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
