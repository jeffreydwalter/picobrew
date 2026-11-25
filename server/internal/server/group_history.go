package server

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

type groupJournal struct {
	GroupID             string `json:"group_id"`
	GroupName           string `json:"group_name"`
	GroupSessionID      string `json:"group_session_id"`
	GroupSourceDeviceID string `json:"group_source_device_id"`
}

type groupJournalWatch struct {
	Invite   brewInvite `json:"invite"`
	Earliest time.Time  `json:"earliest"`
	Until    time.Time  `json:"until"`
	UID      string     `json:"uid"`
}

// Native firmware can accept a join before its next session is created. Track
// one short-lived observation per registration, guarded by physical UID and
// event ID. A different source or newer offer never inherits its journal link.
func (s *Server) watchGroupHistory(ctx context.Context, d *runtimeDevice, v domain.DeviceView, i brewInvite, earliest time.Time) error {
	if !v.Capabilities.HistoryPersistent {
		return nil
	}
	_, err := s.db.Put("group-journal-watch", v.ID, groupJournalWatch{i, earliest, time.Now().Add(inviteLifetime), v.Status.DeviceUID}, -1)
	if err != nil {
		return err
	}
	return s.retryGroupHistory(ctx, d, v, i.GroupSessionID)
}
func (s *Server) retryGroupHistory(ctx context.Context, d *runtimeDevice, v domain.DeviceView, event string) error {
	var candidate groupJournalWatch
	if _, err := s.db.Get("group-journal-watch", v.ID, &candidate); errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if candidate.UID != v.Status.DeviceUID || candidate.Invite.GroupSessionID != event {
		return nil
	}
	if time.Now().After(candidate.Until) {
		return s.db.Delete("group-journal-watch", v.ID)
	}
	i, earliest := candidate.Invite, candidate.Earliest

	if !v.Capabilities.HistoryPersistent || !v.Status.Busy {
		return nil
	}
	// CC3200's session_id is not the numeric persistent history ID. Resolve
	// only one actively recording session matching the observed command/name.
	h, err := d.adapter.History(ctx)
	if err != nil {
		return err
	}
	rows := h.Sessions
	if len(rows) == 0 {
		rows = []domain.HistorySession{h.HistorySession}
	}
	var hit uint64
	for _, r := range rows {
		if !r.Available || r.ID == 0 || r.State != "recording" || r.Command != i.Action || r.DurationMS < 0 || r.DurationMS > (time.Since(earliest)+5*time.Second).Milliseconds() {
			continue
		}
		if i.Action == "brew" && (i.RecipeName == "" || i.RecipeName != r.RecipeName) {
			continue
		}
		if hit != 0 {
			return nil
		}
		hit = r.ID
	}
	if hit == 0 {
		return nil
	}
	_, err = s.db.Put("group-journal", v.ID+"-"+strconv.FormatUint(hit, 10), groupJournal{i.GroupID, i.GroupName, i.GroupSessionID, i.SourceDeviceID}, 0)
	if err == nil || errors.Is(err, store.ErrConflict) {
		return s.db.Delete("group-journal-watch", v.ID)
	}
	return err
}
