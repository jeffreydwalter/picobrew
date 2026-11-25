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

func (s *Server) bindGroupHistory(ctx context.Context, d *runtimeDevice, v domain.DeviceView, i brewInvite, earliest time.Time) error {
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
	if errors.Is(err, store.ErrConflict) {
		return nil
	}
	return err
}
