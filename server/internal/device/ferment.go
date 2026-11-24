package device

import (
	"context"
	"net/http"
)

type FermentMenuRequest struct {
	Request     uint32 `json:"request"`
	Action      string `json:"action"`
	Page        uint32 `json:"page"`
	ListRequest uint32 `json:"list_request"`
	Slot        *uint8 `json:"slot"`
}

type FermentMenuEntry struct {
	Name   string `json:"name"`
	Linked int    `json:"linked"`
}

type FermentMenuReply struct {
	Request uint32             `json:"request"`
	Result  string             `json:"result"`
	Page    uint32             `json:"page"`
	More    int                `json:"more"`
	Entries []FermentMenuEntry `json:"entries"`
}

type FermentMenu interface {
	FermentRequest(context.Context) (FermentMenuRequest, error)
	ReplyFerment(context.Context, FermentMenuReply) error
}

func (p *pico) FermentRequest(ctx context.Context) (FermentMenuRequest, error) {
	var req FermentMenuRequest
	if err := p.lock(ctx); err != nil {
		return req, err
	}
	defer p.unlock()
	err := p.request(ctx, http.MethodGet, "/api/v1/ferment", nil, &req, 200)
	return req, err
}

func (p *pico) ReplyFerment(ctx context.Context, reply FermentMenuReply) error {
	if err := p.lock(ctx); err != nil {
		return err
	}
	defer p.unlock()
	return p.request(ctx, http.MethodPost, "/api/v1/ferment", reply, nil, 200, 202, 204)
}
