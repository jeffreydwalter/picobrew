package server

import (
	"net/http"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
)

// The brewer persists the association. The Hub holds no second copy that can drift from it.
func (s *Server) picoStillLink(w http.ResponseWriter, r *http.Request) {
	brewer, ok := s.runtime(w, r)
	if !ok {
		return
	}
	brewer.commandMu.Lock()
	defer brewer.commandMu.Unlock()
	link, ok := brewer.adapter.(device.PicoStillBrewer)
	if !ok {
		s.fail(w, 409, "unsupported_link", "This connection does not support PicoStill linking")
		return
	}
	if !s.checkStillLinkDevice(w, r, brewer, "z-series") {
		return
	}
	var result device.PicoStillLink
	var err error
	switch r.Method {
	case http.MethodGet:
		result, err = link.PicoStillLink(r.Context())
	case http.MethodPatch:
		var input struct {
			SessionLogging *bool `json:"session_logging"`
		}
		if !s.decode(w, r, &input) {
			return
		}
		if input.SessionLogging == nil {
			s.fail(w, 400, "logging", "Choose whether to record PicoStill sessions")
			return
		}
		result, err = link.SetPicoStillLogging(r.Context(), *input.SessionLogging)
	case http.MethodDelete:
		result, err = link.UnpairPicoStill(r.Context())
	case http.MethodPut:
		var input struct {
			DeviceID string `json:"picostill_id"`
		}
		if !s.decode(w, r, &input) {
			return
		}
		s.mu.RLock()
		still := s.devices[input.DeviceID]
		s.mu.RUnlock()
		if still == nil || still == brewer {
			s.fail(w, 422, "picostill", "Choose a registered PicoStill")
			return
		}
		// A PicoStill is always the second lock. Reject another Z before acquiring it.
		still.mu.RLock()
		model := still.view.Model
		still.mu.RUnlock()
		if model != "picostill" {
			s.fail(w, 422, "picostill", "Choose a registered PicoStill")
			return
		}
		still.commandMu.Lock()
		defer still.commandMu.Unlock()
		if !s.checkStillLinkDevice(w, r, still, "picostill") {
			return
		}
		still.mu.RLock()
		uid := still.view.Status.DeviceUID
		still.mu.RUnlock()
		result, err = link.PairPicoStill(r.Context(), still.adapter, uid)
	}
	if err != nil {
		s.deviceError(w, err)
		return
	}
	s.json(w, 200, result)
}

func (s *Server) checkStillLinkDevice(w http.ResponseWriter, r *http.Request, d *runtimeDevice, model string) bool {
	if !s.currentDevice(d) {
		s.fail(w, 409, "connection_changed", "Refresh the machine connection before continuing")
		return false
	}
	// Freshly authenticate both endpoints before passing a device credential to the Z.
	if r.Method != http.MethodGet {
		if _, err := s.verifiedStatus(r.Context(), d); err != nil {
			s.deviceError(w, err)
			return false
		}
	}
	d.mu.RLock()
	v := d.view
	updating := d.updatingFirmware
	d.mu.RUnlock()
	if v.Model != model || v.Adapter != "pico-standalone" || v.Capabilities == nil || !v.Capabilities.PicoStillBrewerLinkV1 {
		s.fail(w, 409, "unsupported_link", "Update this device to firmware with PicoStill linking")
		return false
	}
	if r.Method != http.MethodGet && (v.Status == nil || v.Status.Busy || updating) {
		s.fail(w, 409, "busy", "Wait for the device to finish its current operation")
		return false
	}
	return true
}
