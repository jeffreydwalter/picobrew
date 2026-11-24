package server

import (
	"testing"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

func TestFermentationChoicesIdentifySessionsImportedTogether(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, d := range []domain.Device{
		{ID: "brewer", Model: "pico-s", Adapter: "pico-standalone"},
		{ID: "monitor", Model: "picoferm", Adapter: "pico-standalone"},
	} {
		if _, err := s.db.Put("device", d.ID, d, 0); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 11, 24, 12, 30, 0, 0, time.UTC)
	start := at.Add(-48 * time.Hour)
	for _, b := range []domain.Brew{
		{ID: "brew", DeviceID: "brewer", Model: "pico-s", Source: "device", Command: "brew", DeviceHistoryID: 13, ObservedAt: at, StartedAt: &start},
		{ID: "a", DeviceID: "monitor", Model: "picoferm", Source: "device", Command: "start", DeviceHistoryID: 42, ObservedAt: at, StartedAt: &start},
		{ID: "b", DeviceID: "monitor", Model: "picoferm", Source: "device", Command: "start", DeviceHistoryID: 43, ObservedAt: at},
	} {
		if _, err := s.db.Put("brew", b.ID, b, 0); err != nil {
			t.Fatal(err)
		}
	}
	opts, err := s.fermentationOptions("brewer")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Brews) != 1 || opts.Brews[0].SessionNumber != "13" || opts.Brews[0].StartedAt == nil || !opts.Brews[0].StartedAt.Equal(start) {
		t.Fatalf("incorrect batch %+v", opts.Brews)
	}
	if len(opts.Recordings) != 2 {
		t.Fatalf("unexpected recordings %+v", opts.Recordings)
	}
	for _, r := range opts.Recordings {
		if !r.ObservedAt.Equal(at) {
			t.Fatal("observation time lost")
		}
		switch r.ID {
		case "a":
			if r.SessionNumber != "42" || r.StartedAt == nil || !r.StartedAt.Equal(start) {
				t.Fatalf("incorrect known start %+v", r)
			}
		case "b":
			if r.SessionNumber != "43" || r.StartedAt != nil {
				t.Fatalf("invented start for offline import %+v", r)
			}
		default:
			t.Fatalf("unexpected session: %+v", r)
		}
	}
}
