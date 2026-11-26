package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
)

var errStockZDeliveryUnknown = errors.New("The Z may have received the program. Check its dial before sending another program.")

type stockZEndpoint struct {
	ip     netip.Addr
	client *http.Client
}

func stockZIP(address string) (netip.Addr, error) {
	u, err := url.Parse(strings.TrimSpace(address))
	if err == nil && u.Scheme == "http" && u.User == nil && (u.Path == "" || u.Path == "/") &&
		u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" {
		ip, err := netip.ParseAddr(u.Hostname())
		if err == nil && ip.Is4() && ip.IsPrivate() && (u.Host == ip.String() || u.Host == ip.String()+":80") {
			return ip, nil
		}
	}
	return netip.Addr{}, errors.New("Enter the stock Z’s private IPv4 address as an HTTP URL on port 80.")
}

func stockZLocal(ip netip.Addr, networks []lanInterface) (netip.Addr, error) {
	var selected lanInterface
	for _, n := range networks {
		if n.local == ip || n.network.Bits() < 31 && n.network.Contains(ip) && (ip == n.network.Addr() || !n.network.Contains(ip.Next())) {
			return netip.Addr{}, errors.New("Choose a brewer address. The Hub and network broadcast addresses are unavailable.")
		}
		if n.network.Contains(ip) && (!selected.local.IsValid() || n.network.Bits() > selected.network.Bits()) {
			selected = n
		}
	}
	if !selected.local.IsValid() {
		return netip.Addr{}, errors.New("The stock Z must be on a private IPv4 network directly attached to the Hub.")
	}
	return selected.local, nil
}

func (s *Server) openStockZ(address string) (stockZEndpoint, error) {
	ip, err := stockZIP(address)
	if err != nil {
		return stockZEndpoint{}, err
	}
	if s.cfg.Demo {
		return stockZEndpoint{}, errors.New("Physical stock Z connections are unavailable in simulation.")
	}
	if s.stockZOpen != nil {
		return s.stockZOpen("http://" + ip.String())
	}
	networks, _, err := attachedLANs()
	if err != nil {
		return stockZEndpoint{}, err
	}
	local, err := stockZLocal(ip, networks)
	if err != nil {
		return stockZEndpoint{}, err
	}
	client := newDiscoveryClient(local) // Bound interface, numeric IP, no proxy, redirects or keep-alive retries.
	client.Timeout = 3 * time.Second
	return stockZEndpoint{ip: ip, client: client}, nil
}

type stockZState struct {
	Activity    string   `json:"activity"`
	Wort        *float64 `json:"wort_c"`
	Thermoblock *float64 `json:"thermoblock_c"`
	Ambient     *float64 `json:"ambient_c"`
	Drain       *float64 `json:"drain_c"`
	Target      *float64 `json:"target_c"`
	StepName    string   `json:"step_name,omitempty"`
	Elapsed     uint32   `json:"elapsed_seconds"`
	Remaining   uint32   `json:"remaining_seconds"`
	KegPump     bool     `json:"keg_pump_on"`
	DrainPump   bool     `json:"drain_pump_on"`
	Pause       uint8    `json:"pause_reason"`
	Location    uint8    `json:"location"`
	Valve       uint8    `json:"valve_position"`
	RSSI        int32    `json:"rssi"`
	PicoPak     bool     `json:"picopak_mode"`
}

// All fifteen fields are present in the OEM document. The OEM writes null for
// invalid temperature observations and can emit null for its borrowed step name.
func parseStockZState(data []byte) (stockZState, error) {
	var state stockZState
	var keg, drain, pak uint8
	values := map[string]any{
		"WortTemp": &state.Wort, "ThermoBlockTemp": &state.Thermoblock, "AmbientTemp": &state.Ambient,
		"DrainTemp": &state.Drain, "TargetTemp": &state.Target, "StepName": &state.StepName,
		"SecondsEllapsed": &state.Elapsed, "SecondsRemaining": &state.Remaining, "KegPumpOn": &keg,
		"DrainPumpOn": &drain, "PauseReason": &state.Pause, "Location": &state.Location,
		"ValvePosition": &state.Valve, "rssi": &state.RSSI, "UsePicoPakMode": &pak,
	}
	invalid := errors.New("The brewer did not return a complete stock Z state report.")
	if !json.Valid(data) {
		return stockZState{}, invalid
	}
	reader := json.NewDecoder(bytes.NewReader(data))
	if start, _ := reader.Token(); start != json.Delim('{') {
		return stockZState{}, invalid
	}
	for reader.More() {
		key, _ := reader.Token()
		name, _ := key.(string)
		out, ok := values[name]
		var raw json.RawMessage
		if !ok || reader.Decode(&raw) != nil || json.Unmarshal(raw, out) != nil {
			return stockZState{}, invalid
		}
		if string(raw) == "null" && !strings.HasSuffix(name, "Temp") && name != "StepName" {
			return stockZState{}, invalid
		}
		delete(values, name) // A conflicting second pump or pause reading is ambiguous.
	}
	if len(values) > 0 || keg > 1 || drain > 1 || pak > 1 || state.Pause > 17 || state.Location > 6 {
		return stockZState{}, invalid
	}
	state.KegPump, state.DrainPump, state.PicoPak = keg == 1, drain == 1, pak == 1
	// /state has neither the Runtime state nor the heater enable bit. Residual
	// targets/step/timers can persist after a run; they cannot prove idle.
	switch {
	case state.Pause != 0:
		state.Activity = "paused"
	case state.KegPump || state.DrainPump:
		state.Activity = "active"
	case state.Wort == nil || state.Thermoblock == nil || state.Ambient == nil || state.Drain == nil ||
		state.Target == nil || *state.Target != 0 || state.StepName != "" || state.Elapsed != 0 || state.Remaining != 0:
		state.Activity = "unknown"
	default:
		state.Activity = "outputs_quiet"
	}
	return state, nil
}

func (z stockZEndpoint) readState(ctx context.Context) (stockZState, error) {
	candidate, ok := discoverDevice(ctx, z.client, z.ip)
	if !ok || candidate.Source != "stock" || candidate.Model != "z-series" {
		return stockZState{}, errors.New("The stock Z Program Maker could not be identified at this address.")
	}
	data, _ := discoveryResponse(ctx, z.client, "http://"+z.ip.String(), "/state", 2047, false)
	return parseStockZState(data)
}

func (z stockZEndpoint) post(ctx context.Context, program []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+z.ip.String()+"/recipe", bytes.NewReader(program))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.GetBody = nil
	resp, err := z.client.Do(req)
	if err != nil {
		return errStockZDeliveryUnknown
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest {
		return &device.Error{StatusCode: 422, Code: "program_rejected", Message: "The stock Z rejected the program. Check the dial before selecting another recipe."}
	}
	// The input is published before the socket reply. Only the OEM's empty
	// HTTP 200 acknowledges the request; none of its routes reads it back.
	if resp.StatusCode != http.StatusOK || resp.ContentLength != 0 {
		return errStockZDeliveryUnknown
	}
	ack, err := io.ReadAll(io.LimitReader(resp.Body, 1))
	if err != nil || len(ack) != 0 {
		return errStockZDeliveryUnknown
	}
	return nil
}
