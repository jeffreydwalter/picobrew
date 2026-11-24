package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/ai"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

// A consent belongs to one verified appliance and one native history session.
// Pending consent can be created only by an accepted, explicitly opted-in Hub
// start command. It is never a preference that rolls into a later brew.
type aiMonitorRecord struct {
	DeviceID       string    `json:"device_id"`
	DeviceUID      string    `json:"device_uid"`
	State          string    `json:"state"`
	BrewID         string    `json:"brew_id,omitempty"`
	HistoryID      uint64    `json:"history_id,omitempty"`
	SessionID      string    `json:"session_id,omitempty"`
	Command        string    `json:"command,omitempty"`
	RecipeName     string    `json:"recipe_name,omitempty"`
	CommandReceipt string    `json:"command_receipt,omitempty"`
	PriorHistory   []uint64  `json:"prior_history,omitempty"`
	RequestedAt    time.Time `json:"requested_at"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	LastReviewAt   time.Time `json:"last_review_at,omitempty"`
}

type aiMonitorStatus struct {
	Enabled       bool       `json:"enabled"`
	State         string     `json:"state"`
	Configured    bool       `json:"configured"`
	BrewID        string     `json:"brew_id,omitempty"`
	CurrentBrewID string     `json:"current_brew_id,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

type aiBrewObservation struct {
	View    domain.DeviceView
	Status  domain.Status
	History domain.History
	BrewID  string
}

func brewCommand(command string) bool { return command == "brew" || command == "manual" }

func (s *Server) loadAIMonitor(deviceID string) (aiMonitorRecord, error) {
	record := aiMonitorRecord{DeviceID: deviceID, State: "off"}
	_, err := s.db.Get("ai-monitor", deviceID, &record)
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	return record, err
}

func (s *Server) saveAIMonitor(record aiMonitorRecord) error {
	_, err := s.db.Put("ai-monitor", record.DeviceID, record, -1)
	return err
}

// Never resurrect a start intent after the Hub was absent from its boundary.
// Already bound sessions survive restart and still require exact live identity.
func (s *Server) recoverAIMonitor() error {
	rows, err := s.db.List("ai-monitor")
	if err != nil {
		return err
	}
	for _, raw := range rows {
		var record aiMonitorRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		if record.State == "pending" {
			record.State = "off"
			if err := s.saveAIMonitor(record); err != nil {
				return err
			}
		}
	}
	return nil
}

// Caller holds d.commandMu. Only authenticated adapter status/history establish
// identity; cached fleet state and unsigned telemetry cannot grant consent.
func (s *Server) observeAIBrew(ctx context.Context, d *runtimeDevice) (aiBrewObservation, error) {
	var observed aiBrewObservation
	if d.adapter == nil || !s.currentDevice(d) {
		return observed, &device.Error{StatusCode: 409, Code: "connection_changed", Message: "The machine connection changed; reconnect before using live AI"}
	}
	status, err := s.verifiedStatus(ctx, d)
	if err != nil {
		return observed, err
	}
	if status.DeviceUID == "" {
		return observed, &device.Error{StatusCode: 409, Code: "device_identity", Message: "Live AI requires a verified machine identity"}
	}
	history, err := d.adapter.History(ctx)
	if err != nil {
		return observed, err
	}
	d.mu.RLock()
	view := d.view
	d.mu.RUnlock()
	if !view.Enabled {
		return observed, &device.Error{StatusCode: 409, Code: "device_disabled", Message: "Connect to the machine before requesting live AI advice"}
	}
	observed = aiBrewObservation{View: view, Status: status, History: history}
	if status.Busy && !status.Stopped && history.Available && history.ID != 0 && history.State == "recording" && brewCommand(history.Command) && (history.Command == status.Command || status.Command == "front-panel") {
		observed.BrewID = fmt.Sprintf("%s-%d", view.ID, history.ID)
		var brew domain.Brew
		if _, err := s.db.Get("brew", observed.BrewID, &brew); errors.Is(err, store.ErrNotFound) {
			// The normal history importer will fill the samples. Creating this
			// exact metadata record lets the user opt in before its next poll.
			_, err = s.mergeHistoryBrew(view.Device, history.HistorySession, false, nil)
			if err != nil {
				return observed, err
			}
		} else if err != nil {
			return observed, err
		} else if brew.DeviceID != view.ID || brew.DeviceHistoryID != history.ID {
			return observed, errors.New("recorded brew identity does not match the native history session")
		}
	}
	return observed, nil
}

func historySessions(history domain.History) []domain.HistorySession {
	if len(history.Sessions) > 0 {
		return history.Sessions
	}
	if history.Available {
		return []domain.HistorySession{history.HistorySession}
	}
	return nil
}

func (s *Server) reconcileAIMonitor(record aiMonitorRecord, observed aiBrewObservation) (aiMonitorRecord, error) {
	previous := record.State
	if record.State == "pending" {
		startupTransition := time.Since(record.RequestedAt) <= 5*time.Second && (observed.Status.Command == "idle" || observed.Status.Command == "front-panel")
		switch {
		case time.Now().After(record.ExpiresAt), record.DeviceUID != observed.Status.DeviceUID:
			record.State = "off"
		case observed.Status.Stopped:
			record.State = "off"
		case observed.Status.Busy && observed.Status.Command != record.Command && !startupTransition:
			record.State = "off"
		case !observed.Status.Busy && time.Since(record.RequestedAt) > 5*time.Second:
			// A queued command may need one poll to enter its preparation screen.
			record.State = "off"
		case observed.BrewID != "":
			seen := make(map[uint64]bool, len(record.PriorHistory))
			for _, id := range record.PriorHistory {
				seen[id] = true
			}
			newSessions := 0
			for _, h := range historySessions(observed.History) {
				if h.Available && h.ID != 0 && !seen[h.ID] {
					newSessions++
				}
			}
			if seen[observed.History.ID] {
				break // The preceding session is still selected during startup.
			}
			var receipt commandReceipt
			_, receiptErr := s.db.Get("command", record.CommandReceipt, &receipt)
			if receiptErr != nil || receipt.State != "accepted" || !receipt.AIMonitor || receipt.DeviceID != record.DeviceID || receipt.Command.Command != record.Command || newSessions != 1 || observed.Status.Command != record.Command || observed.History.Command != record.Command || record.RecipeName != "" && observed.History.RecipeName != record.RecipeName {
				record.State = "off"
				break
			}
			record.State, record.BrewID = "active", observed.BrewID
			record.HistoryID, record.SessionID = observed.History.ID, observed.Status.SessionID
			record.ExpiresAt = time.Time{}
		}
	} else if record.State == "active" && (record.DeviceUID != observed.Status.DeviceUID || record.BrewID != observed.BrewID || record.HistoryID != observed.History.ID || record.SessionID != observed.Status.SessionID) {
		record.State = "finished"
	}
	if previous != record.State {
		if err := s.saveAIMonitor(record); err != nil {
			return record, err
		}
	}
	return record, nil
}

func monitorStatus(record aiMonitorRecord, currentBrewID string, configured bool) aiMonitorStatus {
	result := aiMonitorStatus{Enabled: record.State == "pending" || record.State == "active", State: record.State, Configured: configured, BrewID: record.BrewID, CurrentBrewID: currentBrewID}
	if record.State == "pending" {
		result.ExpiresAt = &record.ExpiresAt
	}
	return result
}

func (s *Server) aiMonitor(w http.ResponseWriter, r *http.Request) {
	d, ok := s.lookupRuntime(w, r)
	if !ok {
		return
	}
	var input struct {
		Enabled *bool  `json:"enabled"`
		BrewID  string `json:"brew_id"`
	}
	if r.Method == http.MethodPut {
		if !s.decode(w, r, &input) {
			return
		}
		if input.Enabled == nil {
			s.fail(w, 400, "enabled", "Choose whether to monitor this brew")
			return
		}
	}
	d.commandMu.Lock()
	defer d.commandMu.Unlock()
	d.mu.RLock()
	id, enabled := d.view.ID, d.view.Enabled
	d.mu.RUnlock()
	record, err := s.loadAIMonitor(id)
	if err != nil {
		s.internal(w, err)
		return
	}
	_, _, configErr := s.aiConfiguration()
	if input.Enabled != nil && !*input.Enabled {
		record.State = "off"
		if err := s.saveAIMonitor(record); err != nil {
			s.internal(w, err)
			return
		}
		s.json(w, 200, monitorStatus(record, "", configErr == nil))
		return
	}
	if !enabled {
		s.fail(w, 409, "device_disabled", "Connect to the machine before enabling live AI monitoring")
		return
	}
	observed, err := s.observeAIBrew(r.Context(), d)
	if err != nil {
		s.deviceError(w, err)
		return
	}
	record, err = s.reconcileAIMonitor(record, observed)
	if err != nil {
		s.internal(w, err)
		return
	}
	if input.Enabled != nil {
		if configErr != nil {
			s.fail(w, 409, "ai_not_configured", configErr.Error())
			return
		}
		if input.BrewID == "" || observed.BrewID == "" || input.BrewID != observed.BrewID {
			s.fail(w, 409, "brew_changed", "Choose the current recording brew before enabling AI monitoring")
			return
		}
		if record.State != "active" || record.BrewID != observed.BrewID {
			lastReview := time.Time{}
			if record.BrewID == observed.BrewID && record.DeviceUID == observed.Status.DeviceUID {
				lastReview = record.LastReviewAt
			}
			record = aiMonitorRecord{DeviceID: id, DeviceUID: observed.Status.DeviceUID, State: "active", BrewID: observed.BrewID, HistoryID: observed.History.ID, SessionID: observed.Status.SessionID, Command: observed.History.Command, RequestedAt: time.Now().UTC(), LastReviewAt: lastReview}
			if err := s.saveAIMonitor(record); err != nil {
				s.internal(w, err)
				return
			}
		}
	}
	s.json(w, 200, monitorStatus(record, observed.BrewID, configErr == nil))
}

// Caller holds d.commandMu and has already checked the command confirmation.
// This only prepares consent data; it does not send an AI request or a command.
func (s *Server) prepareAIMonitor(ctx context.Context, d *runtimeDevice, command domain.Command, receiptID string) (aiMonitorRecord, error) {
	if !brewCommand(command.Command) {
		return aiMonitorRecord{}, &device.Error{StatusCode: 400, Code: "ai_monitor_command", Message: "AI monitoring is available only for brewing sessions"}
	}
	if _, _, err := s.aiConfiguration(); err != nil {
		return aiMonitorRecord{}, &device.Error{StatusCode: 409, Code: "ai_not_configured", Message: err.Error()}
	}
	observed, err := s.observeAIBrew(ctx, d)
	if err != nil {
		return aiMonitorRecord{}, err
	}
	if observed.Status.Busy || observed.Status.Stopped || !observed.Status.Ready {
		return aiMonitorRecord{}, &device.Error{StatusCode: 409, Code: "machine_not_idle", Message: "Return to the ready main menu before opening a new brew"}
	}
	now := time.Now().UTC()
	record := aiMonitorRecord{DeviceID: observed.View.ID, DeviceUID: observed.Status.DeviceUID, State: "pending", Command: command.Command, CommandReceipt: receiptID, RequestedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	for _, h := range historySessions(observed.History) {
		if h.Available && h.ID != 0 {
			record.PriorHistory = append(record.PriorHistory, h.ID)
		}
	}
	if command.Command == "brew" {
		programs, err := d.adapter.Recipes(ctx)
		if err != nil {
			return record, err
		}
		for _, program := range programs {
			if program.ID == command.RecipeID {
				record.RecipeName = program.Name
				return record, nil
			}
		}
		return record, &device.Error{StatusCode: 404, Code: "recipe_not_found", Message: "Choose a recipe already installed on this machine"}
	}
	return record, nil
}

func (s *Server) clearAIMonitor(deviceID string, pendingOnly bool) error {
	record, err := s.loadAIMonitor(deviceID)
	if err != nil {
		return err
	}
	if record.State == "pending" || !pendingOnly && record.State == "active" {
		record.State = "off"
		return s.saveAIMonitor(record)
	}
	return nil
}

// History is fetched for this exact session before any provider call, then
// selected identity is checked again. Never hold the command mutex across
// pagination: a user's stop/control request must not wait for AI context work.
func (s *Server) liveAIContext(ctx context.Context, d *runtimeDevice, input ai.Request) (map[string]any, error) {
	d.commandMu.Lock()
	observed, err := s.observeAIBrew(ctx, d)
	d.commandMu.Unlock()
	if err != nil {
		return nil, err
	}
	if input.BrewID == "" || input.BrewID != observed.BrewID || input.DeviceID != observed.View.ID {
		return nil, &device.Error{StatusCode: 409, Code: "brew_changed", Message: "The recording brew changed; review the current session before asking for live advice"}
	}
	d.historyMu.Lock()
	err = s.pullHistorySession(ctx, d, observed.View.Device, observed.History.HistorySession)
	d.historyMu.Unlock()
	if err != nil {
		return nil, err
	}
	d.commandMu.Lock()
	latest, err := s.observeAIBrew(ctx, d)
	d.commandMu.Unlock()
	if err != nil {
		return nil, err
	}
	if latest.BrewID != input.BrewID || latest.Status.DeviceUID != observed.Status.DeviceUID || latest.Status.SessionID != observed.Status.SessionID {
		return nil, &device.Error{StatusCode: 409, Code: "brew_changed", Message: "The brew changed while its history was being read; no AI request was sent"}
	}
	data, err := s.aiContext(input)
	if err != nil {
		return nil, err
	}
	data["device"] = latest.View
	data["observed_at"] = time.Now().UTC()
	return data, nil
}
