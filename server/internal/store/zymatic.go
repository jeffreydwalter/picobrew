package store

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

// A Zymatic sends each report once. Commit the recovery pointer, journal metadata
// and reading together. Read the journal under the write lock to preserve owner edits.
type ZymaticJournalWrite struct {
	Machine         string
	MachineRecord   any
	MachineRevision int64
	Brew            domain.Brew
	PreviousID      string
	Reading         *domain.Sample
	At              time.Time
	End             bool
}

func (s *Store) WriteZymaticJournal(w ZymaticJournalWrite) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if w.MachineRecord != nil {
		payload, err := json.Marshal(w.MachineRecord)
		if err != nil {
			return err
		}
		if _, err := putRecord(tx, "stock-zymatic", w.Machine, payload, w.MachineRevision); err != nil {
			return err
		}
	}
	saveBrew := func(b domain.Brew, v int64) error {
		payload, err := json.Marshal(b)
		if err != nil {
			return err
		}
		_, err = putRecord(tx, "brew", b.ID, payload, v)
		return err
	}
	if w.PreviousID != "" && w.PreviousID != w.Brew.ID {
		var previous domain.Brew
		v, err := loadLinkRecord(tx, "brew", w.PreviousID, &previous)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil && previous.State == "recording" {
			previous.State = "interrupted"
			if err := saveBrew(previous, v); err != nil {
				return err
			}
		}
	}
	var b domain.Brew
	v, err := loadLinkRecord(tx, "brew", w.Brew.ID, &b)
	if errors.Is(err, ErrNotFound) {
		b, v = w.Brew, 0
	} else if err != nil {
		return err
	}
	start := b.ObservedAt
	if b.StartedAt != nil {
		start = *b.StartedAt
	}
	elapsed := max(int64(0), w.At.Sub(start).Milliseconds())
	if w.Reading != nil {
		// The wire has no event timestamp or sequence. Distinct reports can
		// arrive within one millisecond, including after a host clock change.
		var last int64
		if err := tx.QueryRow("SELECT COALESCE(MAX(elapsed_ms),-1) FROM samples WHERE brew_id=?", b.ID).Scan(&last); err != nil {
			return err
		}
		elapsed = max(elapsed, last+1)
		w.Reading.ElapsedMS = elapsed
		w.Reading.ObservedAt = &w.At
		payload, err := json.Marshal(w.Reading)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO samples(brew_id,source,elapsed_ms,payload) VALUES(?,?,?,?)", b.ID, w.Reading.Source, elapsed, payload); err != nil {
			return err
		}
		b.SampleCount++
	}
	if b.State == "recording" {
		b.DurationMS = max(b.DurationMS, elapsed)
		if w.End {
			// The termination message carries no outcome or stop reason.
			b.State = "ended"
		}
	}
	if err := saveBrew(b, v); err != nil {
		return err
	}
	return tx.Commit()
}
