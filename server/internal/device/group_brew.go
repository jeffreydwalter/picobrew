package device

import (
	"context"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"net/http"
)

type GroupSource struct {
	ID       string  `json:"id"`
	AgeMS    *uint32 `json:"age_ms"`
	Action   string  `json:"action"`
	RecipeID string  `json:"recipe_id,omitempty"`
	Revision uint32  `json:"revision,omitempty"`
}
type GroupOffer struct {
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
