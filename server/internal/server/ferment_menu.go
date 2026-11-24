package server

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

const fermentPageSize = 4

type fermentMenuState struct {
	lastUptime  uint64
	listRequest uint32
	page        uint32
	offered     []picoFermOption
	lastInput   device.FermentMenuRequest
	lastReply   device.FermentMenuReply
}

func (m *fermentMenuState) repeated(r device.FermentMenuRequest) bool {
	last := m.lastInput
	if last.Request != r.Request || last.Action != r.Action || last.Page != r.Page || last.ListRequest != r.ListRequest {
		return false
	}
	if r.Slot == nil || last.Slot == nil {
		return r.Slot == last.Slot
	}
	return *r.Slot == *last.Slot
}

// The UID-pinned Hub polls the brewer's authenticated API. No inbound call,
// Hub password, or device credential is sent to a telemetry receiver.
func (s *Server) syncFermentMenu(ctx context.Context, d *runtimeDevice) {
	mailbox, ok := d.adapter.(device.FermentMenu)
	d.mu.RLock()
	available := ok && d.view.Online && d.view.IsBrewer() && d.view.Capabilities != nil && d.view.Capabilities.Supports("ferment_menu_v1") && d.view.Status != nil && !d.identityMismatch && !d.updatingFirmware
	brewerID := d.view.ID
	var uptime uint64
	if available {
		uptime = d.view.Status.UptimeMS
	}
	d.mu.RUnlock()
	m := &d.fermentMenu
	available = available && s.currentDevice(d)
	if !available || uptime < m.lastUptime {
		*m = fermentMenuState{}
	}
	if !available {
		return
	}
	m.lastUptime = uptime
	req, err := mailbox.FermentRequest(ctx)
	if err != nil {
		return
	}
	if req.Request == 0 {
		m.lastInput = device.FermentMenuRequest{}
		return
	}
	if m.repeated(req) {
		_ = mailbox.ReplyFerment(ctx, m.lastReply)
		return
	}
	reply := device.FermentMenuReply{Request: req.Request, Result: "error", Page: req.Page, Entries: []device.FermentMenuEntry{}}
	var offered []picoFermOption
	switch req.Action {
	case "list":
		options, err := s.picoFermOptions()
		if err == nil {
			offered = menuPage(brewerID, options, &reply)
		}
	case "link", "unlink":
		// Another Hub might have supplied the displayed page. Only its
		// acknowledged supplier can identify the requested slot. An orphaned
		// selection times out on the brewer and can be reopened.
		if req.ListRequest == 0 || req.ListRequest != m.listRequest {
			return
		}
		reply.Result = "stale"
		if req.Page != m.page || req.Slot == nil || int(*req.Slot) >= len(m.offered) {
			break
		}
		f := m.offered[*req.Slot]
		unlink := req.Action == "unlink"
		if unlink && f.BrewerID != brewerID || !unlink && f.BrewerID != "" {
			break
		}
		s.deviceMu.Lock()
		if ctx.Err() == nil && s.currentDevice(d) {
			_, err = s.assignPicoFerm(store.PicoFermAssignment{BrewerID: brewerID, PicoFermID: f.ID, Revision: f.Revision, Unlink: unlink})
			switch {
			case err == nil:
				if unlink {
					reply.Result = "unlinked"
				} else {
					reply.Result = "linked"
				}
			case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrAccessoryModel):
			default:
				reply.Result = "error"
			}
		}
		s.deviceMu.Unlock()
	}
	if req.Action == "list" {
		// A cancelled menu or competing Hub can reject the reply. Keep choices
		// only for a page whose delivery was acknowledged by this brewer.
		m.listRequest, m.offered = 0, nil
		if err := mailbox.ReplyFerment(ctx, reply); err != nil {
			return
		}
		if reply.Result == "list" {
			m.listRequest, m.page, m.offered = req.Request, req.Page, offered
		}
		m.lastInput, m.lastReply = req, reply
		return
	}
	// Cache the decision before sending. Lost replies retry the same outcome
	// without committing a second link or treating our own revision as stale.
	m.listRequest, m.offered = 0, nil
	m.lastInput, m.lastReply = req, reply
	_ = mailbox.ReplyFerment(ctx, reply)
}

func menuLabel(name string) string {
	var b strings.Builder
	for _, r := range name {
		if b.Len() >= 16 {
			break
		}
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune(" -._", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	if name := strings.TrimSpace(b.String()); name != "" {
		return name
	}
	return "PicoFerm"
}

func menuPage(brewerID string, options []picoFermOption, reply *device.FermentMenuReply) []picoFermOption {
	eligible := make([]picoFermOption, 0, len(options))
	for _, opt := range options {
		if opt.BrewerID == "" || opt.BrewerID == brewerID {
			eligible = append(eligible, opt)
		}
	}
	start := uint64(reply.Page) * fermentPageSize
	if start > 0 && start >= uint64(len(eligible)) {
		reply.Result = "stale"
		return nil
	}
	end := min(start+fermentPageSize, uint64(len(eligible)))
	page := eligible[start:end]
	// Two names may become identical after OLED truncation. Include the
	// displayed number to distinguish them, even across different pages.
	used := map[string]bool{}
	labels := make([]string, len(eligible))
	for i, o := range eligible {
		base := menuLabel(o.Name)
		label := base
		for n := 2; used[label]; n++ {
			suffix := " " + strconv.Itoa(n)
			label = base[:min(len(base), 16-len(suffix))] + suffix
		}
		used[label] = true
		labels[i] = label
	}
	reply.Result = "list"
	if end < uint64(len(eligible)) {
		reply.More = 1
	}
	for i, f := range page {
		entry := device.FermentMenuEntry{Name: labels[int(start)+i]}
		if f.BrewerID == brewerID {
			entry.Linked = 1
		}
		reply.Entries = append(reply.Entries, entry)
	}
	return page
}
