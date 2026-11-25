package device

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// PicoStillLink contains only fields safe to return to the browser.
type PicoStillLink struct {
	Paired         bool   `json:"paired"`
	Verified       bool   `json:"verified"`
	Checking       bool   `json:"checking"`
	Host           string `json:"host"`
	Port           uint16 `json:"port"`
	UID            string `json:"uid"`
	SessionLogging bool   `json:"session_logging"`
	Error          string `json:"error,omitempty"`
}

type PicoStillBrewer interface {
	PicoStillLink(context.Context) (PicoStillLink, error)
	PairPicoStill(context.Context, Adapter, string) (PicoStillLink, error)
	UnpairPicoStill(context.Context) (PicoStillLink, error)
	SetPicoStillLogging(context.Context, bool) (PicoStillLink, error)
}

var stillHost = regexp.MustCompile(`^[A-Za-z0-9.-]{1,63}$`)
var stillUID = regexp.MustCompile(`^[0-9a-f]{12}$`)

func (p *pico) stillRequest(ctx context.Context, method string, body any) (PicoStillLink, error) {
	var result PicoStillLink
	if err := p.lock(ctx); err != nil {
		return result, err
	}
	defer p.unlock()
	err := p.request(ctx, method, "/api/v1/picostill", body, &result, 200, 202)
	return result, err
}

func (p *pico) PicoStillLink(ctx context.Context) (PicoStillLink, error) {
	return p.stillRequest(ctx, http.MethodGet, nil)
}
func (p *pico) UnpairPicoStill(ctx context.Context) (PicoStillLink, error) {
	return p.stillRequest(ctx, http.MethodDelete, nil)
}
func (p *pico) SetPicoStillLogging(ctx context.Context, enabled bool) (PicoStillLink, error) {
	return p.stillRequest(ctx, http.MethodPatch, map[string]bool{"session_logging": enabled})
}
func (p *pico) PairPicoStill(ctx context.Context, peer Adapter, uid string) (PicoStillLink, error) {
	still, ok := peer.(*pico)
	if !ok || still.base.Scheme != "http" || !stillHost.MatchString(still.base.Hostname()) || !stillUID.MatchString(uid) || !apiKey.MatchString(still.key) {
		return PicoStillLink{}, invalid("The Z needs a registered PicoStill with a local HTTP address and verified device identity")
	}
	port := 80
	if text := still.base.Port(); text != "" {
		value, err := strconv.Atoi(text)
		if err != nil || value < 1 || value > 65535 {
			return PicoStillLink{}, invalid("Invalid PicoStill port")
		}
		port = value
	}
	link, err := p.stillRequest(ctx, http.MethodPut, map[string]any{
		"host": still.base.Hostname(), "port": port, "uid": uid, "key": strings.ToLower(still.key),
	})
	if err != nil {
		// The device received another appliance's credential. Never propagate a response that could echo it.
		return PicoStillLink{}, &Error{StatusCode: 409, Code: "pairing_unconfirmed", Message: "The Z did not confirm the pairing request. Refresh its link status before retrying."}
	}
	link.Error = ""
	return link, nil
}
