package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

type picoFermOption struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BrewerID string `json:"brewer_id"`
	Revision int64  `json:"revision"`
}

type fermentationBatch struct {
	ID                  string    `json:"id"`
	RecipeName          string    `json:"recipe_name"`
	ObservedAt          time.Time `json:"observed_at"`
	PicoFermID          string    `json:"picoferm_id,omitempty"`
	PicoFermName        string    `json:"picoferm_name,omitempty"`
	PicoFermRecordingID string    `json:"picoferm_recording_id,omitempty"`
}

type fermentationRecording struct {
	ID           string    `json:"id"`
	DeviceID     string    `json:"device_id"`
	ObservedAt   time.Time `json:"observed_at"`
	LinkedBrewID string    `json:"linked_brew_id,omitempty"`
}

type brewerFermentationOptions struct {
	PicoFerms  []picoFermOption        `json:"picoferms"`
	Brews      []fermentationBatch     `json:"brews"`
	Recordings []fermentationRecording `json:"recordings"`
}

type brewerPicoFermInput struct {
	PicoFermID  string `json:"picoferm_id"`
	Revision    *int64 `json:"revision"`
	Unlink      bool   `json:"unlink,omitempty"`
	BrewID      string `json:"brew_id,omitempty"`
	RecordingID string `json:"recording_id,omitempty"`
}

func (s *Server) picoFermOptions() ([]picoFermOption, error) {
	devices, err := s.db.List("device")
	if err != nil {
		return nil, err
	}
	out := []picoFermOption{}
	for _, row := range devices {
		var d domain.Device
		if err := json.Unmarshal(row, &d); err != nil {
			return nil, err
		}
		if d.IsPicoFerm() {
			out = append(out, picoFermOption{d.ID, d.Name, d.BrewerID, d.BrewerRevision})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *Server) fermentationOptions(brewerID string) (brewerFermentationOptions, error) {
	var out = brewerFermentationOptions{Brews: []fermentationBatch{}, Recordings: []fermentationRecording{}}
	var brewer domain.Device
	if _, err := s.db.Get("device", brewerID, &brewer); err != nil {
		return out, err
	}
	if !brewer.IsBrewer() {
		return out, store.ErrAccessoryModel
	}
	options, err := s.picoFermOptions()
	if err != nil {
		return out, err
	}
	out.PicoFerms = options
	ferms := make(map[string]bool, len(options))
	for _, f := range options {
		ferms[f.ID] = true
	}
	rows, err := s.db.List("brew")
	if err != nil {
		return out, err
	}
	claimed := map[string]string{}
	for _, row := range rows {
		var b domain.Brew
		if err := json.Unmarshal(row, &b); err != nil {
			return out, err
		}
		if b.PicoFermRecordingID != "" {
			claimed[b.PicoFermRecordingID] = b.ID
		}
		if b.DeviceID == brewerID && b.IsFermentationBatch() {
			out.Brews = append(out.Brews, fermentationBatch{b.ID, b.RecipeName, b.ObservedAt, b.PicoFermID, b.PicoFermName, b.PicoFermRecordingID})
		}
		if ferms[b.DeviceID] && b.IsPicoFermRecording() {
			out.Recordings = append(out.Recordings, fermentationRecording{ID: b.ID, DeviceID: b.DeviceID, ObservedAt: b.ObservedAt})
		}
	}
	for i := range out.Recordings {
		out.Recordings[i].LinkedBrewID = claimed[out.Recordings[i].ID]
	}
	sort.SliceStable(out.Brews, func(i, j int) bool { return out.Brews[i].ObservedAt.After(out.Brews[j].ObservedAt) })
	sort.SliceStable(out.Recordings, func(i, j int) bool { return out.Recordings[i].ObservedAt.After(out.Recordings[j].ObservedAt) })
	return out, nil
}

// Caller serializes device registration edits with deviceMu. Holding the
// runtime lock across the write keeps model reconciliation and the view in sync.
func (s *Server) assignPicoFerm(a store.PicoFermAssignment) (picoFermBrewerLink, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := s.devices[a.PicoFermID]
	if d == nil {
		return picoFermBrewerLink{}, store.ErrNotFound
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	saved, err := s.db.AssignPicoFerm(a)
	if err != nil {
		return picoFermBrewerLink{}, err
	}
	d.view.BrewerID, d.view.BrewerRevision = saved.BrewerID, saved.BrewerRevision
	return picoFermBrewerLink{saved.BrewerID, saved.BrewerRevision}, nil
}

func (s *Server) brewerPicoFerms(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var reply any
	var err error
	if r.Method == http.MethodGet {
		reply, err = s.fermentationOptions(id)
	} else {
		var input brewerPicoFermInput
		if !s.decode(w, r, &input) {
			return
		}
		if input.PicoFermID == "" || input.Revision == nil || *input.Revision < 0 {
			s.fail(w, 400, "picoferm_link", "Choose a PicoFerm and send its current link revision.")
			return
		}
		s.deviceMu.Lock()
		reply, err = s.assignPicoFerm(store.PicoFermAssignment{BrewerID: id, PicoFermID: input.PicoFermID, Revision: *input.Revision, Unlink: input.Unlink, BrewID: input.BrewID, RecordingID: input.RecordingID})
		s.deviceMu.Unlock()
	}
	switch {
	case errors.Is(err, store.ErrAccessoryModel):
		s.fail(w, 400, "brewer_required", "Choose a registered brewer and PicoFerm.")
	case errors.Is(err, store.ErrFermentationBatch):
		s.fail(w, 400, "fermentation_batch", "Choose a batch from this brewer and a recording from the selected PicoFerm.")
	case errors.Is(err, store.ErrFermentationAssigned):
		s.fail(w, 409, "fermentation_assigned", "This batch or fermentation recording already has a different assignment.")
	case err != nil:
		s.storeError(w, err)
	default:
		s.json(w, 200, reply)
	}
}
