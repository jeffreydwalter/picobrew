package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

const inviteLifetime = 2 * time.Minute

func validGroupAction(action string) bool {
	return action == "brew" || action == "clean" || action == "rinse"
}
func validEvent(e *device.GroupSource) bool {
	if e == nil || len(e.ID) != 16 || strings.ToLower(e.ID) != e.ID || e.AgeMS == nil || time.Duration(*e.AgeMS)*time.Millisecond >= inviteLifetime || !validGroupAction(e.Action) {
		return false
	}
	if _, err := hex.DecodeString(e.ID); err != nil {
		return false
	}
	if e.Action == "brew" {
		return e.RecipeID != "" && e.Revision != 0
	}
	return e.RecipeID == "" && e.Revision == 0
}
func groupEventID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}
func groupWireName(s string) string {
	v := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return ' '
		}
		return r
	}, s)
	v = strings.TrimSpace(v)
	if v == "" {
		return "Brewer"
	}
	if len(v) > 20 {
		v = v[:20]
	}
	return v
}
func groupView(d *runtimeDevice) domain.DeviceView { d.mu.RLock(); defer d.mu.RUnlock(); return d.view }
func (s *Server) currentGroup(id string) (*brewGroup, error) {
	groups, err := listRecords[brewGroup](s, groupKind)
	return groupFor(groups, id), err
}
func (s *Server) memberInvite(id string) (*brewInvite, error) {
	var v brewInvite
	_, err := s.db.Get(inviteKind, id, &v)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return &v, err
}
func inviteMember(g *brewGroup, i *brewInvite) bool {
	return g != nil && g.ID == i.GroupID && slices.Contains(g.DeviceIDs, i.TargetDeviceID) && slices.Contains(g.DeviceIDs, i.SourceDeviceID)
}
func (s *Server) expireInvite(v *brewInvite, g *brewGroup, now time.Time) error {
	if !activeInvite(v.State) {
		return nil
	}
	if !inviteMember(g, v) {
		v.State = "withdrawn"
		v.Message = "Group membership changed."
	} else if !v.ExpiresAt.After(now) {
		v.State = "expired"
		v.Message = "The invitation expired."
	} else {
		return nil
	}
	return s.saveInvite(*v)
}

// Call under the target command lock after verifying the pinned device. Source
// polling only queues records. Each recipient owns delivery, consent and start.
func (s *Server) syncBrewGroup(ctx context.Context, d *runtimeDevice, status domain.Status) error {
	view := groupView(d)
	if view.Adapter != "pico-standalone" || view.Capabilities == nil {
		return nil
	}
	g, err := s.currentGroup(view.ID)
	if err != nil {
		return err
	}
	invite, err := s.memberInvite(view.ID)
	if err != nil {
		return err
	}
	if g == nil && (invite == nil || !activeInvite(invite.State) && invite.State != "withdrawn") {
		return nil
	}
	wire, ok := d.adapter.(device.GroupBrewer)
	if ok && !view.Capabilities.GroupBrewingV1 && ccGroupCapable(view) {
		return s.syncCCGroup(ctx, d, view, wire)
	}
	if !ok || !view.Capabilities.GroupBrewingV1 {
		if invite == nil || invite.State != "waiting" {
			return nil
		}
		s.groupMu.Lock()
		defer s.groupMu.Unlock()
		invite, err = s.memberInvite(view.ID)
		if err != nil || invite == nil || invite.State != "waiting" {
			return err
		}
		g, err = s.currentGroup(view.ID)
		if err != nil {
			return err
		}
		if err = s.expireInvite(invite, g, time.Now()); err != nil || invite.State != "waiting" {
			return err
		}
		if view.Model == "zymatic" && machineSerial.MatchString(status.StockSerial) {
			return s.prepareMenuInvite(invite, status.StockSerial)
		}
		invite.State = "unavailable"
		invite.Message = "This firmware does not support group invitations."
		return s.saveInvite(*invite)
	}
	live, err := wire.GroupState(ctx)
	if err != nil {
		return err
	}
	s.groupMu.Lock()
	defer s.groupMu.Unlock()
	g, err = s.currentGroup(view.ID)
	if err != nil {
		return err
	}
	if g != nil && validEvent(live.Source) {
		if err = s.recordGroupStart(ctx, d, view, *g, wire, *live.Source); err != nil {
			return err
		}
	}
	invite, err = s.memberInvite(view.ID)
	if err != nil || invite == nil {
		return err
	}
	if activeInvite(invite.State) && live.Invite != nil && live.Invite.ID == invite.ID && (live.Invite.State == "accepted" || live.Invite.State == "declined") {
		return s.reconcileGroupInvite(ctx, d, view, wire, live.Invite, invite)
	}
	if err = s.expireInvite(invite, g, time.Now()); err != nil {
		return err
	}
	return s.reconcileGroupInvite(ctx, d, view, wire, live.Invite, invite)
}
func (s *Server) recordGroupStart(ctx context.Context, d *runtimeDevice, view domain.DeviceView, g brewGroup, wire device.GroupBrewer, e device.GroupSource) error {
	now := time.Now().UTC()
	started := now.Add(-time.Duration(*e.AgeMS) * time.Millisecond)
	if started.Before(g.MemberSince[view.ID]) {
		return nil
	}
	key := groupEventID(g.ID, view.ID, view.Status.DeviceUID, e.ID)
	if err := s.retryGroupHistory(ctx, d, view, key); err != nil {
		return err
	}
	var processed bool
	if _, err := s.db.Get("group-brew-event", key, &processed); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	var src groupRecipe
	var problem string
	if e.Action == "brew" {
		p, rev, err := wire.GroupRecipe(ctx, e.RecipeID)
		if err != nil {
			return err
		}
		if rev != e.Revision || p.ID != e.RecipeID {
			problem = "The source recipe changed after the brew started."
		} else {
			src, err = s.identifyGroupRecipe(view, p)
			if err != nil {
				return err
			}
			if _, err = s.db.Put("group-brew-program", key, src, -1); err != nil {
				return err
			}
		}
	}
	source := brewInvite{SourceHistoryID: e.HistoryID, GroupID: g.ID, GroupName: g.Name, GroupSessionID: key, SourceDeviceID: view.ID, SourceName: view.Name, SourceUID: view.Status.DeviceUID, SourceEvent: e.ID, Action: e.Action, RecipeName: src.Program.Name, RecipeID: src.LibraryID, RecipeRevision: src.Revision, ExpiresAt: started.Add(inviteLifetime)}
	for _, id := range g.DeviceIDs {
		if id == view.ID || (view.Status.StockSerial != "" && id == stockMemberPrefix+view.Status.StockSerial) || started.Before(g.MemberSince[id]) {
			continue
		}
		current, err := s.memberInvite(id)
		if err != nil {
			return err
		}
		next := source
		next.ID = groupEventID(key, id)
		next.TargetDeviceID = id
		next.State = "waiting"
		if current != nil && (current.ID == next.ID || activeInvite(current.State) && current.ExpiresAt.After(now)) {
			continue
		}
		if d := s.groupDevice(id); d != nil {
			target := groupView(d)
			if target.Status != nil {
				next.TargetUID = target.Status.DeviceUID
			}
			if target.Status != nil && target.Status.DeviceUID == source.SourceUID {
				continue
			}
		}
		if problem != "" {
			next.State = "unavailable"
			next.Message = problem
		} else if serial, ok := strings.CutPrefix(id, stockMemberPrefix); ok {
			if err = s.prepareMenuInvite(&next, serial); err != nil {
				return err
			}
			continue
		}
		if err = s.saveInvite(next); err != nil {
			return err
		}
	}
	if err := s.watchGroupHistory(ctx, d, view, source, started); err != nil {
		s.cfg.Logger.Debug("Group journal link unavailable", "error", err)
	}
	_, err := s.db.Put("group-brew-event", key, true, 0)
	return err
}
func (s *Server) reconcileGroupInvite(ctx context.Context, d *runtimeDevice, v domain.DeviceView, w device.GroupBrewer, live *device.GroupOffer, i *brewInvite) error {
	if i.TargetUID != "" && i.TargetUID != v.Status.DeviceUID {
		return nil
	}
	matched := live != nil && live.ID == i.ID
	if i.State == "withdrawn" || i.State == "expired" {
		if matched && live.State == "pending" {
			return w.GroupRespond(ctx, device.GroupOffer{Op: "withdraw", ID: i.ID})
		}
		return nil
	}
	if !activeInvite(i.State) {
		if i.State == "accepted" && matched && live.State == "accepted" {
			return s.retryGroupHistory(ctx, d, v, i.GroupSessionID)
		}
		return nil
	}
	if matched {
		switch live.State {
		case "pending":
			if i.State == "responding" {
				return nil
			}
			if i.State == "pending" {
				return nil
			}
			i.State = "pending"
		case "accepted", "declined", "expired", "withdrawn":
			i.State = live.State
		default:
			return nil
		}
		if i.State == "accepted" {
			if err := s.watchGroupHistory(ctx, d, v, *i, i.ExpiresAt.Add(-inviteLifetime)); err != nil {
				s.cfg.Logger.Debug("Group journal link unavailable", "error", err)
			}
		}
		return s.saveInvite(*i)
	}
	if i.State != "waiting" {
		i.State = "unconfirmed"
		i.Message = "The brewer did not confirm the invitation. Check its front panel."
		return s.saveInvite(*i)
	}
	if v.Status.Busy || v.Status.Stopped || !v.Status.Ready || live != nil && live.State == "pending" {
		i.State = "unavailable"
		i.Message = "The brewer is busy or unavailable."
		return s.saveInvite(*i)
	}
	if !slices.Contains(v.Capabilities.Commands, i.Action) {
		i.State = "unavailable"
		i.Message = "This brewer does not support the requested routine."
		return s.saveInvite(*i)
	}
	i.TargetUID = v.Status.DeviceUID
	if i.Action == "brew" {
		var src groupRecipe
		if _, err := s.db.Get("group-brew-program", i.GroupSessionID, &src); err != nil {
			return err
		}
		slot, rev, err := s.groupTargetRecipe(ctx, d, v, w, src)
		if err != nil {
			i.State = "unavailable"
			i.Message = safeMessage(err.Error())
			return s.saveInvite(*i)
		}
		i.TargetSlot, i.SlotRevision = slot, rev
	}
	remaining := time.Until(i.ExpiresAt).Milliseconds()
	if remaining < 1 || remaining > inviteLifetime.Milliseconds() {
		i.State = "expired"
		return s.saveInvite(*i)
	}
	if i.HubConfirmation {
		i.State = "pending"
		i.Message = "Accept in the Hub to start on this brewer."
		return s.saveInvite(*i)
	}
	i.State = "offering"
	if err := s.saveInvite(*i); err != nil {
		return err
	}
	offer := device.GroupOffer{Op: "offer", TTLMS: uint32(remaining), ID: i.ID, Action: i.Action, From: groupWireName(i.SourceName), RecipeID: i.TargetSlot, Revision: i.SlotRevision}
	if err := w.GroupRespond(ctx, offer); err != nil {
		var problem *device.Error
		if errors.As(err, &problem) && !problem.UnknownOutcome {
			i.State = "unavailable"
			i.Message = problem.Message
			return s.saveInvite(*i)
		}
		return err
	}
	i.State = "pending"
	return s.saveInvite(*i)
}
