package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type deliveryAdapter struct {
	*pairingAdapter
	sent, failed float64
	reason       string
}

func (a *deliveryAdapter) Telemetry(ctx context.Context) (map[string]any, error) {
	v, err := a.pairingAdapter.Telemetry(ctx)
	if err == nil {
		v["status"] = map[string]any{"sent": a.sent, "failed": a.failed, "last_error": a.reason}
	}
	return v, err
}

func picoFermDeliveryFixture(t *testing.T) (*Server, *runtimeDevice, *deliveryAdapter) {
	t.Helper()
	s, d, p := pairingFixture(t, "picoferm")
	p.status.Model, p.status.ModelFamily = "picoferm", "picoferm"
	p.caps.Model, p.caps.ModelFamily, p.caps.ModelName = "picoferm", "picoferm", "PicoFerm"
	a := &deliveryAdapter{pairingAdapter: p, reason: "none"}
	d.adapter = a
	pairedRefresh(t, s, d)
	if a.writes != 1 {
		t.Fatal("initial Hub receiver not assigned")
	}
	return s, d, a
}

func TestPicoFermRecoversChangedHubAfterFailedDelivery(t *testing.T) {
	s, d, a := picoFermDeliveryFixture(t)
	old := a.target
	next := telemetryTarget{Enabled: true, Host: "192.0.2.23", Port: 8282}
	s.cfg.DeviceURL = ""
	s.cfg.ListenAddress = "192.0.2.23:8282"
	before, err := s.readTelemetryRecord(d.view.ID)
	if err != nil {
		t.Fatal(err)
	}
	withOrigin := before
	withOrigin.Origin = "http://192.0.2.20:8080"
	if err := s.writeTelemetryRecord(d.view.ID, before, withOrigin); err != nil {
		t.Fatal(err)
	}
	probes := 0
	canReach := false
	s.telemetryClient = &http.Client{Transport: receiverTransport(func(r *http.Request) (*http.Response, error) {
		probes++
		w := httptest.NewRecorder()
		if r.URL.Host == "192.0.2.20:8080" {
			t.Fatal("retried failed candidate during rediscovery")
		}
		if r.URL.Host == "192.0.2.23:8282" && canReach {
			s.telemetryReceiverProbe(w, r)
		} else {
			w.WriteHeader(404)
		}
		return w.Result(), nil
	})}
	refresh := func() { t.Helper(); pairedRefresh(t, s, d) }
	// Counters are lifetime totals. Establish a baseline and ignore old faults.
	a.sent, a.failed, a.reason = 7, 18, "timeout"
	refresh()
	a.failed += 2
	refresh()
	if probes != 0 {
		t.Fatal("transient failures changed the receiver")
	}
	a.sent++
	a.failed += 3
	refresh()
	if probes != 0 {
		t.Fatal("missed a successful delivery during the observation window")
	}
	a.failed += 3
	refresh()
	if a.target != old || a.writes != 1 || probes == 0 {
		t.Fatal("unreachable replacement displaced the saved address")
	}
	record, _ := s.readTelemetryRecord(d.view.ID)
	if *record.Desired != old {
		t.Fatal("absence of a replacement changed persisted destination")
	}
	a.status.Busy = true
	a.failed++
	canReach = true
	refresh()
	if a.target != old || d.view.Telemetry.State != "waiting_idle" {
		t.Fatalf("active monitor was changed: %+v", d.view.Telemetry)
	}
	a.status.Busy = false
	refresh()
	record, err = s.readTelemetryRecord(d.view.ID)
	if err != nil || a.writes != 2 || a.target != next || record.Pending != nil || record.Owned != next || *record.Desired != next || !record.FollowHub {
		t.Fatalf("did not recover: %+v %+v %v", a.target, record, err)
	}
	refresh()
	if a.writes != 2 {
		t.Fatal("verified receiver was resent")
	}
}

func TestPicoFermReceiverPreservesOwnerChoices(t *testing.T) {
	for _, mode := range []string{"manual", "legacy", "disabled", "unowned", "pending", "changed on machine", "other UID"} {
		t.Run(mode, func(t *testing.T) {
			s, d, a := picoFermDeliveryFixture(t)
			before, _ := s.readTelemetryRecord(d.view.ID)
			record := before
			switch mode {
			case "manual":
				// An explicitly saved manual address stays manual even if it
				// happens to equal the Hub's last automatic address.
				savedReceiverRequest(t, s, http.MethodPut, a.target)
				record, _ = s.readTelemetryRecord(d.view.ID)
				if record.FollowHub {
					t.Fatal("manual choice kept following the Hub")
				}
				before = record
			case "legacy":
				record.FollowHub = false
			case "disabled":
				tgt := a.target
				tgt.Enabled = false
				record.Desired, record.Owned = &tgt, tgt
				a.target = tgt
			case "unowned":
				record.Owned = telemetryTarget{}
				record.Apply = true // a Hub restart requests reconciliation
				a.target.Host = "custom.example"
			case "pending":
				unknown := telemetryTarget{Enabled: true, Host: "unknown.example", Port: 8080}
				record.Pending = &unknown
				a.target.Host = "custom.example"
			case "changed on machine":
				record.Apply = true
				a.target.Host = "custom.example"
			case "other UID":
				a.status.DeviceUID = "other"
			}
			if err := s.writeTelemetryRecord(d.view.ID, before, record); err != nil {
				t.Fatal(err)
			}
			target, writes := a.target, a.writes
			s.cfg.DeviceURL = "http://192.0.2.23:8282"
			s.telemetryClient = &http.Client{Transport: receiverTransport(func(r *http.Request) (*http.Response, error) {
				t.Errorf("rediscovery for %s", mode)
				w := httptest.NewRecorder()
				s.telemetryReceiverProbe(w, r)
				return w.Result(), nil
			})}
			a.reason = "timeout"
			for i := 0; i < 3; i++ {
				a.failed += 3
				d.commandMu.Lock()
				s.syncTelemetry(context.Background(), d, true)
				d.commandMu.Unlock()
			}
			if a.writes != writes || a.target != target {
				t.Fatalf("%s changed: %+v", mode, a.target)
			}
		})
	}
}

func TestDeliveryFailuresResetOnRebootOrInvalidCounters(t *testing.T) {
	w := deliveryWindow{}
	target := telemetryTarget{Enabled: true, Host: "local.example", Port: 8080}
	st := map[string]any{"sent": float64(7), "failed": float64(20), "last_error": "connect"}
	raw := map[string]any{"status": st}
	if w.failing(target, raw, 900) {
		t.Fatal("old failures triggered recovery")
	}
	st["failed"] = float64(22)
	if w.failing(target, raw, 1000) {
		t.Fatal("two failures triggered recovery")
	}
	st["sent"], st["failed"] = float64(0), float64(3)
	if w.failing(target, raw, 20) {
		t.Fatal("device restart reused prior failure window")
	}
	st["failed"] = float64(6)
	if !w.failing(target, raw, 100) {
		t.Fatal("three fresh failures did not trigger recovery")
	}
	st["failed"] = "unexpected"
	if w.failing(target, raw, 200) {
		t.Fatal("accepted malformed counters")
	}
}
