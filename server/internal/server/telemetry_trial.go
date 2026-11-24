package server

import (
	"context"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

const (
	receiverWriting       = "writing"
	receiverTesting       = "testing"
	receiverRestoring     = "restoring"
	receiverTrialDuration = 15 * time.Minute
	receiverTrialCooldown = 30 * time.Minute
)

// Desired, Owned and HubReceiver keep the last confirmed destination throughout
// the trial. Settings readback confirms the address; a later sender counter
// increment proves the accessory received a successful HTTP delivery there.
type receiverTrial struct {
	RestoreAttempts uint8           `json:"restore_attempts,omitempty"`
	RetryAt         time.Time       `json:"retry_at,omitzero"`
	Target          telemetryTarget `json:"target"`
	Phase           string          `json:"phase"`
	Deadline        time.Time       `json:"deadline"`
	BaselineAt      time.Time       `json:"baseline_at"`
	Baseline        bool            `json:"baseline"`
	Uptime          uint64          `json:"uptime"`
	Sent            uint32          `json:"sent"`
	Failed          uint32          `json:"failed"`
}

func receiverCounters(raw map[string]any) (sent, failed uint32, valid bool) {
	st, _ := raw["status"].(map[string]any)
	s, sOK := telemetryCounter(st["sent"])
	f, fOK := telemetryCounter(st["failed"])
	return s, f, sOK && fOK
}

func (s *Server) saveReceiverTrial(d *runtimeDevice, before, after telemetryRecord, state, message string) bool {
	d.mu.RLock()
	id := d.view.ID
	d.mu.RUnlock()
	if err := s.writeTelemetryRecord(id, before, after); err != nil {
		s.telemetryState(d, "error", "The Hub could not save the PicoFerm receiver trial. No further change was sent.", before.desiredTarget())
		return false
	}
	s.telemetryState(d, state, message, after.desiredTarget())
	return true
}

func (s *Server) finishReceiverTrial(d *runtimeDevice, r telemetryRecord, delivered bool) {
	before := r
	trial := r.Trial
	r.Trial = nil
	r.Apply, r.RetryPending = false, false
	message := "The PicoFerm is using its previous Hub receiver. The Hub will wait before trying a different address again."
	if delivered {
		r.Desired, r.HubReceiver, r.Owned = &trial.Target, &trial.Target, trial.Target
		r.RecoveryAfter = time.Time{}
		message = "The PicoFerm delivered data to its new Hub address."
	} else {
		r.RecoveryAfter = time.Now().UTC().Add(receiverTrialCooldown)
	}
	d.telemetryDelivery = deliveryWindow{}
	s.saveReceiverTrial(d, before, r, "configured", message)
}

func (s *Server) startReceiverTrial(ctx context.Context, d *runtimeDevice, r telemetryRecord, target telemetryTarget) {
	// Finding a candidate can take seconds. Check whether the original
	// receiver has recovered and recheck the UID and idle state before writing.
	st, err := s.verifiedStatus(ctx, d)
	if err != nil || !s.currentDevice(d) || st.Busy || st.Stopped || !st.Ready {
		return
	}
	raw, err := d.adapter.Telemetry(ctx)
	if err != nil {
		return
	}
	observed, err := readTelemetryTarget(raw)
	if err != nil || !observed.sameConfig(r.desiredTarget()) {
		return
	}
	if !d.telemetryDelivery.failing(observed, raw, st.UptimeMS) {
		s.telemetryApplied(d, r.desiredTarget())
		return
	}
	s.writeTrialAddress(ctx, d, r, receiverTrial{Target: target, Phase: receiverWriting, Deadline: time.Now().UTC().Add(receiverTrialDuration)}, st)
}

// Persist the direction before calling the device. An interrupted/uncertain
// write is reconciled from authenticated readback before another decision.
func (s *Server) writeTrialAddress(ctx context.Context, d *runtimeDevice, r telemetryRecord, trial receiverTrial, st domain.Status) {
	before := r
	r.Trial = &trial
	target := trial.Target
	if trial.Phase == receiverRestoring {
		target = r.desiredTarget()
		// Bound retries when a device keeps rejecting the exact same restore.
		// A later poll still requires fresh identity, idle and target readback.
		backoff := [...]time.Duration{time.Minute, 5 * time.Minute, receiverTrialCooldown}
		trial.RetryAt = time.Now().UTC().Add(backoff[min(int(trial.RestoreAttempts), len(backoff)-1)])
		if trial.RestoreAttempts < uint8(len(backoff)) {
			trial.RestoreAttempts++
		}
	}
	if !s.saveReceiverTrial(d, before, r, "pending", "Verifying the PicoFerm receiver change.") {
		return
	}
	_ = d.adapter.UpdateTelemetry(ctx, target.settings())
	raw, err := d.adapter.Telemetry(ctx)
	if err == nil {
		observed, readErr := readTelemetryTarget(raw)
		if readErr == nil && observed.sameConfig(target) {
			s.checkReceiverTrial(ctx, d, r, st, observed, raw)
			return
		}
	}
	s.telemetryState(d, "outcome_unknown", "The PicoFerm has not confirmed its receiver change. The Hub will check the device before taking another action.", r.desiredTarget())
}

func (s *Server) checkReceiverTrial(ctx context.Context, d *runtimeDevice, r telemetryRecord, st domain.Status, observed telemetryTarget, raw map[string]any) {
	t := *r.Trial
	if observed.sameConfig(r.desiredTarget()) {
		s.finishReceiverTrial(d, r, false)
		return
	}
	if !observed.sameConfig(t.Target) {
		s.telemetryState(d, "pending", "The receiver was changed on the PicoFerm. Save receiver settings to select the destination.", r.desiredTarget())
		return
	}
	if t.Phase == receiverRestoring {
		if !time.Now().Before(t.RetryAt) && st.Ready && !st.Busy && !st.Stopped {
			s.writeTrialAddress(ctx, d, r, t, st)
		} else {
			s.telemetryState(d, "pending", "The PicoFerm still has the trial address. The Hub will retry the saved receiver when the machine is idle.", r.desiredTarget())
		}
		return
	}
	sent, failed, valid := receiverCounters(raw)
	if t.Phase == receiverWriting || valid && (!t.Baseline || st.UptimeMS < t.Uptime || sent < t.Sent || failed < t.Failed) {
		before := r
		t.BaselineAt = time.Now().UTC()
		t.Phase, t.Baseline, t.Uptime, t.Sent, t.Failed = receiverTesting, valid, st.UptimeMS, sent, failed
		r.Trial = &t
		if !s.saveReceiverTrial(d, before, r, "pending", "Waiting for the PicoFerm to deliver data to the new Hub address.") {
			return
		}
	} else if valid && sent > t.Sent && s.confirmTrialDelivery(st, t) {
		s.finishReceiverTrial(d, r, true)
		return
	}
	if valid && failed-t.Failed >= 3 || time.Now().After(t.Deadline) {
		if st.Busy || st.Stopped || !st.Ready {
			s.telemetryState(d, "waiting_idle", "The new receiver has not received data. The Hub will restore the saved address when the PicoFerm is idle.", r.desiredTarget())
			return
		}
		t.Phase = receiverRestoring
		s.writeTrialAddress(ctx, d, r, t, st)
		return
	}
	s.telemetryState(d, "pending", "Waiting for the PicoFerm to deliver data to the new Hub address.", r.desiredTarget())
}

// Match an authenticated increase in the device's successful delivery count to
// a frame received by this Hub since the candidate was read back. A receiver
// probe alone can verify the Hub side of a Docker route that the device cannot use.
func (s *Server) confirmTrialDelivery(st domain.Status, t receiverTrial) bool {
	var receipt struct {
		ReceivedAt time.Time `json:"received_at"`
		Status     struct {
			DeviceUID string `json:"device_uid"`
			UptimeMS  uint64 `json:"uptime_ms"`
		} `json:"status"`
	}
	if _, err := s.db.Get("forwarded-telemetry", st.DeviceUID, &receipt); err != nil {
		return false
	}
	return receipt.ReceivedAt.After(t.BaselineAt) && receipt.Status.DeviceUID == st.DeviceUID &&
		receipt.Status.UptimeMS >= t.Uptime && receipt.Status.UptimeMS <= st.UptimeMS
}
