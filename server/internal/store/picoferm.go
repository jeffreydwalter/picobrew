package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

var ErrAccessoryModel = errors.New("store: choose a registered PicoFerm and a physical brewer")
var ErrFermentationBatch = errors.New("store: choose a brewing session from this brewer and a recording from this PicoFerm")
var ErrFermentationAssigned = errors.New("store: fermentation history already has a different assignment")

func loadLinkRecord(tx *sql.Tx, kind, id string, dst any) (int64, error) {
	var version int64
	var payload []byte
	err := tx.QueryRow("SELECT version,payload FROM records WHERE kind=? AND id=?", kind, id).Scan(&version, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(payload, dst)
	}
	return version, err
}

type PicoFermAssignment struct {
	BrewerID    string
	PicoFermID  string
	Revision    int64
	Unlink      bool
	BrewID      string
	RecordingID string
}

// SetPicoFermBrewer remains the accessory-side API. Its explicit empty target
// unlinks the current owner. A brewer-side unlink must match the indicated owner.
func (s *Store) SetPicoFermBrewer(id, brewerID string, revision int64) (domain.Device, error) {
	return s.AssignPicoFerm(PicoFermAssignment{BrewerID: brewerID, PicoFermID: id, Revision: revision, Unlink: brewerID == ""})
}

func (s *Store) AssignPicoFerm(a PicoFermAssignment) (domain.Device, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Device{}, err
	}
	defer tx.Rollback()
	var ferm domain.Device
	version, err := loadLinkRecord(tx, "device", a.PicoFermID, &ferm)
	if err != nil {
		return domain.Device{}, err
	}
	if !ferm.IsPicoFerm() {
		return domain.Device{}, ErrAccessoryModel
	}
	if ferm.BrewerRevision != a.Revision {
		return domain.Device{}, ErrConflict
	}
	target := a.BrewerID
	if target != "" {
		var brewer domain.Device
		if _, err := loadLinkRecord(tx, "device", target, &brewer); err != nil {
			return domain.Device{}, err
		}
		if !brewer.IsBrewer() {
			return domain.Device{}, ErrAccessoryModel
		}
	}
	if a.Unlink {
		if target != "" && ferm.BrewerID != target {
			return domain.Device{}, ErrConflict
		}
		target = ""
	} else if target == "" {
		return domain.Device{}, ErrAccessoryModel
	}
	if a.BrewID != "" {
		if target == "" {
			return domain.Device{}, ErrFermentationBatch
		}
		historical, err := assignFermentationBatch(tx, a, ferm)
		if err != nil {
			return domain.Device{}, err
		}
		if historical {
			target = ferm.BrewerID
		}
	} else if a.RecordingID != "" {
		return domain.Device{}, ErrFermentationBatch
	}
	if ferm.BrewerID != target {
		ferm.BrewerID = target
		ferm.BrewerRevision++
		payload, err := json.Marshal(ferm)
		if err != nil {
			return domain.Device{}, err
		}
		if _, err = putRecord(tx, "device", ferm.ID, payload, version); err != nil {
			return domain.Device{}, err
		}
	}
	return ferm, tx.Commit()
}

// Keep a historical snapshot of the accessory, independent of later changes to
// either registration. Claim a selected recording only once, in the same transaction.
func assignFermentationBatch(tx *sql.Tx, a PicoFermAssignment, ferm domain.Device) (historical bool, err error) {
	var batch domain.Brew
	version, err := loadLinkRecord(tx, "brew", a.BrewID, &batch)
	if err != nil {
		return false, err
	}
	if batch.DeviceID != a.BrewerID || !batch.IsFermentationBatch() {
		return false, ErrFermentationBatch
	}
	if batch.PicoFermID != "" && batch.PicoFermID != ferm.ID {
		return false, ErrFermentationAssigned
	}
	if a.RecordingID != "" {
		if batch.PicoFermRecordingID != "" && batch.PicoFermRecordingID != a.RecordingID {
			return false, ErrFermentationAssigned
		}
		var recording domain.Brew
		if _, err := loadLinkRecord(tx, "brew", a.RecordingID, &recording); err != nil {
			return false, err
		}
		if recording.DeviceID != ferm.ID || !recording.IsPicoFermRecording() {
			return false, ErrFermentationBatch
		}
		var claimed bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM records WHERE kind='brew' AND id<>? AND json_extract(CAST(payload AS TEXT),'$.picoferm_recording_id')=?)", batch.ID, a.RecordingID).Scan(&claimed); err != nil {
			return false, err
		}
		if claimed {
			return false, ErrFermentationAssigned
		}
	}
	if batch.PicoFermID == ferm.ID && (a.RecordingID == "" || a.RecordingID == batch.PicoFermRecordingID) {
		return true, nil
	}
	historical = batch.PicoFermID == ferm.ID
	if !historical {
		batch.PicoFermID, batch.PicoFermName = ferm.ID, ferm.Name
	}
	if a.RecordingID != "" {
		batch.PicoFermRecordingID = a.RecordingID
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		return false, err
	}
	_, err = putRecord(tx, "brew", batch.ID, payload, version)
	return historical, err
}
