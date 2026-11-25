package server

import (
	"context"
	"errors"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
	"strconv"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

func ccGroupCapable(v domain.DeviceView) bool {
	return v.Capabilities != nil && v.Capabilities.GroupRecipeV1 && (v.Model == "pico-s" || v.Model == "pico-pro" || v.Model == "pico-c")
}

// This firmware advertises its active slot only after brew preparation and
// suppresses starts from a group join. The history ID identifies the actual
// session across Hub restarts. Names alone never identify a stored program.
func (s *Server) syncCCGroup(ctx context.Context, d *runtimeDevice, v domain.DeviceView, wire device.GroupBrewer) error {
	s.groupMu.Lock()
	defer s.groupMu.Unlock()
	g, err := s.currentGroup(v.ID)
	if err != nil {
		return err
	}
	st := v.Status
	if g != nil && st.Busy && !st.Stopped && st.GroupAgeMS != nil && *st.GroupAgeMS < 120000 && st.GroupRevision != 0 && st.GroupRecipeID != "" {
		h, err := d.adapter.History(ctx)
		if err != nil {
			return err
		}
		rows := h.Sessions
		if len(rows) == 0 {
			rows = []domain.HistorySession{h.HistorySession}
		}
		var id uint64
		for _, r := range rows {
			if !r.Available || r.ID == 0 || r.RecoveredAfterRestart || r.State != "recording" || r.Command != "brew" || r.RecipeName == "" || r.RecipeName != st.RecipeName {
				continue
			}
			if id != 0 {
				id = 0
				break
			}
			id = r.ID
		}
		if id != 0 {
			e := device.GroupSource{ID: groupEventID(st.DeviceUID, strconv.FormatUint(id, 10)), Action: "brew", AgeMS: st.GroupAgeMS, Revision: st.GroupRevision, RecipeID: st.GroupRecipeID}
			if err = s.recordGroupStart(ctx, d, v, *g, wire, e); err != nil {
				return err
			}
		}
	}
	if g != nil && st.Busy && !st.Stopped && st.GroupRinseStartedMS != nil {
		start := *st.GroupRinseStartedMS
		age := uint32(st.UptimeMS) - start
		if start != 0 && age < 120000 {
			// A rinse has no saved history slot. Persist this observation across polls
			// and Hub restarts; an identical uptime marker on another boot may recur.
			var r struct {
				UID   string
				Start uint32
				Event string
				Until time.Time
			}
			_, err := s.db.Get("group-cc-rinse", v.ID, &r)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if r.UID != st.DeviceUID || r.Start != start || !time.Now().Before(r.Until) {
				r.UID, r.Start, r.Event = st.DeviceUID, start, randomID()[:16]
				r.Until = time.Now().Add(inviteLifetime)
				if _, err = s.db.Put("group-cc-rinse", v.ID, r, -1); err != nil {
					return err
				}
			}
			e := device.GroupSource{ID: r.Event, AgeMS: &age, Action: "rinse"}
			if err = s.recordGroupStart(ctx, d, v, *g, wire, e); err != nil {
				return err
			}
		}
	}

	i, err := s.memberInvite(v.ID)
	if err != nil || i == nil {
		return err
	}
	if err = s.expireInvite(i, g, time.Now()); err != nil {
		return err
	}
	if i.State == "accepted" && i.HubConfirmation {
		return s.retryGroupHistory(ctx, d, v, i.GroupSessionID)
	}
	if i.State != "waiting" {
		return nil
	}
	if i.Action != "brew" && i.Action != "rinse" {
		i.State = "unavailable"
		i.Message = "This brewer supports group invitations for saved recipes and rinses."
		return s.saveInvite(*i)
	}
	if _, ok := d.adapter.(device.GuardedGroupStarter); !ok {
		i.State = "unavailable"
		i.Message = "The brewer cannot verify a group start."
		return s.saveInvite(*i)
	}
	i.HubConfirmation = true
	return s.reconcileGroupInvite(ctx, d, v, wire, nil, i)
}
