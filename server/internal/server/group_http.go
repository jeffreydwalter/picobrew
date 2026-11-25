package server

import (
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
)

func (s *Server) brewInvites(w http.ResponseWriter, r *http.Request) {
	s.groupMu.Lock()
	defer s.groupMu.Unlock()
	rows, err := listRecords[brewInvite](s, inviteKind)
	if err != nil {
		s.storeError(w, err)
		return
	}
	groups, err := listRecords[brewGroup](s, groupKind)
	if err != nil {
		s.storeError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	out := []brewInvite{}
	now := time.Now()
	for _, i := range rows {
		if id != "" && id != i.TargetDeviceID {
			continue
		}
		if err = s.expireInvite(&i, groupFor(groups, i.TargetDeviceID), now); err != nil {
			s.storeError(w, err)
			return
		}
		if i.ExpiresAt.After(now) && i.State != "withdrawn" && inviteMember(groupFor(groups, i.TargetDeviceID), &i) {
			out = append(out, i)
		}
	}
	if id != "" {
		var single *brewInvite
		if len(out) > 0 {
			single = &out[0]
		}
		s.json(w, 200, map[string]any{"invite": single})
		return
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ExpiresAt.After(out[b].ExpiresAt) })
	s.json(w, 200, map[string]any{"invites": out})
}
func (s *Server) respondBrewInvite(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var input struct {
		Accept *bool `json:"accept"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if input.Accept == nil {
		s.fail(w, 400, "response", "Choose accept or decline")
		return
	}
	var wire device.GroupBrewer
	d := s.groupDevice(id)
	if d != nil {
		d.commandMu.Lock()
		defer d.commandMu.Unlock()
		if !s.registeredDevice(d) {
			s.fail(w, 404, "machine", "The brewer has been removed")
			return
		}
	}
	s.groupMu.Lock()
	defer s.groupMu.Unlock()
	i, err := s.memberInvite(id)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if i == nil || i.ID != chi.URLParam(r, "invite") {
		s.fail(w, 404, "invitation", "This invitation is no longer available")
		return
	}
	g, err := s.currentGroup(id)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if err = s.expireInvite(i, g, time.Now()); err != nil {
		s.storeError(w, err)
		return
	}
	if i.State != "pending" || !inviteMember(g, i) {
		s.fail(w, 409, "invitation", "This invitation is no longer pending")
		return
	}
	var matched *device.GroupOffer
	if d != nil {
		status, err := s.verifiedStatus(r.Context(), d)
		if err != nil {
			s.deviceError(w, err)
			return
		}
		if i.TargetUID != "" && status.DeviceUID != i.TargetUID {
			s.fail(w, 409, "identity", "The brewer identity changed")
			return
		}
		if *input.Accept && (status.Stopped || ((status.Busy || !status.Ready) && (i.MenuSerial != "" || i.HubConfirmation))) {
			s.fail(w, 409, "busy", "The brewer must be ready to join")
			return
		}
		v := groupView(d)
		if i.HubConfirmation {
			if !ccGroupCapable(v) {
				s.fail(w, 409, "unsupported", "The brewer cannot verify a group start")
				return
			}
		} else if i.MenuSerial != "" {
			if status.StockSerial != i.MenuSerial {
				s.fail(w, 409, "identity", "The Zymatic serial changed")
				return
			}
		} else {
			var ok bool
			wire, ok = d.adapter.(device.GroupBrewer)
			if !ok || v.Capabilities == nil || !v.Capabilities.GroupBrewingV1 {
				s.fail(w, 409, "unsupported", "This firmware cannot accept invitations")
				return
			}
			state, err := wire.GroupState(r.Context())
			if err != nil {
				s.deviceError(w, err)
				return
			}
			matched = state.Invite
			if matched == nil || matched.ID != i.ID || matched.State != "pending" || matched.Action != i.Action || matched.RecipeID != i.TargetSlot {
				s.fail(w, 409, "invitation", "The brewer no longer has this pending invitation")
				return
			}
		}
	} else if i.MenuSerial == "" || id != stockMemberPrefix+i.MenuSerial {
		s.fail(w, 404, "machine", "The brewer is no longer available")
		return
	}
	if !i.ExpiresAt.After(time.Now()) {
		s.fail(w, 409, "expired", "The invitation expired")
		return
	}
	if i.MenuSerial != "" {
		if *input.Accept {
			if err = s.acceptGroupMenu(i); err != nil {
				s.fail(w, 409, "stock_menu", err.Error())
				return
			}
		} else {
			i.State = "declined"
			if err = s.saveInvite(*i); err != nil {
				s.storeError(w, err)
				return
			}
		}
		s.json(w, 200, i)
		return
	}
	if i.HubConfirmation {
		if !*input.Accept {
			i.State = "declined"
		} else {
			starter, ok := d.adapter.(device.GuardedGroupStarter)
			if !ok {
				s.fail(w, 409, "unsupported", "The brewer cannot verify a group start")
				return
			}
			i.State = "responding"
			if err = s.saveInvite(*i); err != nil {
				s.storeError(w, err)
				return
			}
			if err = starter.StartGroupAction(r.Context(), i.Action, i.TargetSlot, i.SlotRevision); err != nil {
				i.State = "unconfirmed"
				i.Message = "Check the brewer before starting another recipe."
				var failure *device.Error
				if errors.As(err, &failure) && !failure.UnknownOutcome {
					i.State = "unavailable"
					i.Message = failure.Message
				}
				if persistErr := s.saveInvite(*i); persistErr != nil {
					s.storeError(w, persistErr)
					return
				}
				s.deviceError(w, err)
				return
			}
			i.State = "accepted"
			i.Message = "The brewer accepted the start. Follow its normal preparation prompts."
			if err = s.watchGroupHistory(r.Context(), d, groupView(d), *i, time.Now()); err != nil {
				s.cfg.Logger.Debug("Group journal link unavailable", "error", err)
			}
		}
		if err = s.saveInvite(*i); err != nil {
			s.storeError(w, err)
			return
		}
		s.json(w, 200, i)
		return
	}

	op := "decline"
	if *input.Accept {
		op = "accept"
	}
	i.State = "responding"
	if err = s.saveInvite(*i); err != nil {
		s.storeError(w, err)
		return
	}
	if err = wire.GroupRespond(r.Context(), device.GroupOffer{Op: op, ID: i.ID}); err != nil {
		// Only an explicit device rejection is safe to retry. A lost response
		// remains in progress until the next poll reads the brewer's outcome.
		var failure *device.Error
		if errors.As(err, &failure) && !failure.UnknownOutcome {
			state, readErr := wire.GroupState(r.Context())
			if readErr == nil && state.Invite != nil && state.Invite.ID == i.ID && state.Invite.State == "pending" {
				i.State = "pending"
				i.Message = failure.Message
				if persistErr := s.saveInvite(*i); persistErr != nil {
					s.storeError(w, persistErr)
					return
				}
			}
		}
		s.deviceError(w, err)
		return
	}
	// Read back the result rather than resending a start after a lost response.
	state, err := wire.GroupState(r.Context())
	if err != nil {
		s.deviceError(w, err)
		return
	}
	if state.Invite != nil && state.Invite.ID == i.ID && (state.Invite.State == "accepted" || state.Invite.State == "declined" || state.Invite.State == "withdrawn" || state.Invite.State == "expired" || state.Invite.State == "unavailable") {
		if _, err = s.verifiedStatus(r.Context(), d); err == nil {
			err = s.reconcileGroupInvite(r.Context(), d, groupView(d), wire, state.Invite, i)
		}
		if err != nil {
			s.deviceError(w, err)
			return
		}
	}
	s.json(w, 200, i)
}
