package server

import "math"

// The sender's counters cover its entire uptime, including previous receivers.
// Count new delivery failures only after observing the selected target, and
// restart the window on any success, switch of receiver, or device restart.
type deliveryWindow struct {
	target   telemetryTarget
	uptime   uint64
	sent     uint32
	failed   uint32
	baseline uint32
}

func telemetryCounter(value any) (uint32, bool) {
	n, ok := value.(float64)
	if !ok || n < 0 || n > math.MaxUint32 || n != math.Trunc(n) {
		return 0, false
	}
	return uint32(n), true
}

func (w *deliveryWindow) failing(target telemetryTarget, raw map[string]any, uptime uint64) bool {
	status, _ := raw["status"].(map[string]any)
	sent, okSent := telemetryCounter(status["sent"])
	failed, okFailed := telemetryCounter(status["failed"])
	if !okSent || !okFailed {
		*w = deliveryWindow{}
		return false
	}
	if !w.target.sameConfig(target) || uptime < w.uptime || w.sent != sent || failed < w.failed {
		w.baseline = failed
	}
	w.target, w.uptime, w.sent, w.failed = target, uptime, sent, failed
	switch status["last_error"] {
	case "connect", "send", "receive", "http", "timeout":
		return failed-w.baseline >= 3
	default:
		w.baseline = failed
		return false
	}
}
