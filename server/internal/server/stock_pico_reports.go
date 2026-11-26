package server

import (
	"encoding/csv"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

const stockPicoReportKind = "stock-pico-reports"
const stockPicoReportSource = "stock-pico-report"

var stockPicoCommands = [...]string{"brew", "clean", "sous-vide", "distill", "coffee", "manual"}

type stockPicoReportStatus struct {
	stockPicoDiagnostics
	SessionID   string    `json:"session_id,omitempty"`
	SessionType int       `json:"session_type"`
	Started     time.Time `json:"started_at,omitempty"`
	HasReports  bool      `json:"has_reports,omitempty"`
	Ended       bool      `json:"ended,omitempty"`
}

type stockPicoDiagnostics struct {
	LastState *int               `json:"last_state,omitempty"`
	StateAt   *time.Time         `json:"state_at,omitempty"`
	LastError *domain.PicoReport `json:"last_error,omitempty"`
	ErrorAt   *time.Time         `json:"error_at,omitempty"`
}

type stockPicoWithReport struct {
	stockPicoBrewer
	Report *stockPicoDiagnostics `json:"report,omitempty"`
}

func (s *Server) stockPicoWithReports(machines []stockPicoBrewer) ([]stockPicoWithReport, error) {
	rows := make([]stockPicoWithReport, len(machines))
	for i, machine := range machines {
		rows[i].stockPicoBrewer = machine
		var report stockPicoDiagnostics
		_, err := s.db.Get(stockPicoReportKind, machine.UID, &report)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if report.LastState != nil || report.LastError != nil {
			rows[i].Report = &report
		}
	}
	return rows, nil
}

func stockPicoJournalID(uid, sid string) string {
	return "stock-pico-session-" + uid + "-" + sid
}

func picoWireInt(text string) (*int, bool) {
	value, err := strconv.ParseInt(text, 10, 32)
	n := int(value)
	return &n, err == nil
}

func picoWireDecimal(text string) (*float64, bool) {
	value, err := strconv.ParseFloat(text, 64)
	return &value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func stockPicoSessionType(q url.Values) (int, bool) {
	t, ok := picoWireInt(q.Get("sesType"))
	return *t, ok && *t >= 0 && *t < len(stockPicoCommands)
}

func stockPicoSample(op string, q url.Values, sessionType int) (*domain.Sample, bool) {
	report := &domain.PicoReport{}
	sample := &domain.Sample{Source: stockPicoReportSource, StockPico: report}
	switch op {
	case "picochangestate":
		state, ok := picoWireInt(q.Get("state"))
		if !ok || *state < 0 || *state > 255 {
			return nil, false
		}
		report.Kind, report.State = "state", state
	case "error":
		code, ok := picoWireInt(q.Get("code"))
		rfid := q.Get("rfid")
		if !ok || (rfid != "" && !reportText(rfid, 20)) {
			return nil, false
		}
		report.Kind, report.ErrorCode, report.RFID = "error", code, rfid
	case "log":
		t, typeOK := stockPicoSessionType(q)
		wort, wortOK := picoWireInt(q.Get("wort"))
		therm, thermOK := picoWireInt(q.Get("therm"))
		status, statusOK := picoWireInt(q.Get("error"))
		left, leftOK := picoWireInt(q.Get("timeLeft"))
		scale, scaleOK := picoWireDecimal(q.Get("shutScale"))
		step, event := q.Get("step"), q.Get("event")
		if !typeOK || t != sessionType || !wortOK || !thermOK || !statusOK || *status < 0 || !leftOK || !scaleOK || !reportText(step, 127) || (event != "" && !reportText(event, 127)) {
			return nil, false
		}
		wortF, thermF := float64(*wort), float64(*therm)
		sample.WortF, sample.ThermoblockF = &wortF, &thermF
		report.Kind, report.Step, report.Event = "log", step, event
		report.ControllerStatus, report.SecondsLeft, report.ShuttleScale = status, left, scale
		if t == 3 {
			sample.Readings = make(map[string]*float64, 3)
			for wire, field := range map[string]string{"t1": "sensor_still_t1", "t2": "sensor_still_t2", "p": "sensor_still_pressure"} {
				value, ok := picoWireDecimal(q.Get(wire))
				if !ok {
					return nil, false
				}
				sample.Readings[field] = value
			}
		}
	default:
		return nil, false
	}
	return sample, true
}

// getSession has no request ID. Replay its answer for an immediate retry until
// the first report, as the stock client cannot identify a lost response.
func (s *Server) stockPicoReport(w http.ResponseWriter, r *http.Request, uid, op string, brewer stockPicoBrewer) {
	// stockPicoWire serializes this report with other requests and owner changes.
	var status stockPicoReportStatus
	revision, err := s.db.Get(stockPicoReportKind, uid, &status)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		http.Error(w, "storage unavailable", 500)
		return
	}
	now, q := time.Now().UTC(), r.URL.Query()
	write := store.StockJournalWrite{
		At: now, MachineKind: stockPicoReportKind, Machine: uid, MachineRevision: revision,
	}
	if op == "getsession" {
		t, ok := stockPicoSessionType(q)
		if !ok {
			http.Error(w, "invalid session type", 400)
			return
		}
		if status.SessionID != "" && status.SessionType == t && !status.Ended && !status.HasReports && now.Sub(status.Started) >= 0 && now.Sub(status.Started) < time.Minute {
			stockReply(w, "#"+status.SessionID+"#")
			return
		}
		if status.SessionID != "" && !status.Ended {
			write.PreviousID = stockPicoJournalID(uid, status.SessionID)
		}
		status.SessionID, status.SessionType, status.Started = randomID()[:20], t, now
		status.Ended, status.HasReports = false, false
		write.Brew = domain.Brew{
			ID: stockPicoJournalID(uid, status.SessionID), DeviceID: "stock-pico:" + uid,
			DeviceName: brewer.Name, Model: brewer.Model, StockSerial: uid, Source: stockPicoReportSource,
			Command: stockPicoCommands[t], State: "recording", StartedAt: &now, ObservedAt: now,
			SampleFields: []string{"elapsed_ms", "wort_f", "thermoblock_f"},
		}
		if t == 3 {
			write.Brew.SampleFields = append(write.Brew.SampleFields, "sensor_still_t1", "sensor_still_t2", "sensor_still_pressure")
			write.Brew.FieldMetadata = map[string]domain.FieldMetadata{
				"sensor_still_t1":       {Label: "Still T1", Unit: "raw"},
				"sensor_still_t2":       {Label: "Still T2", Unit: "raw"},
				"sensor_still_pressure": {Label: "Still pressure", Unit: "psi", Kind: "pressure"},
			}
		}
	} else {
		if op == "log" && (status.SessionID == "" || q.Get("sesId") != status.SessionID) {
			http.Error(w, "unknown session", 404)
			return
		}
		sample, valid := stockPicoSample(op, q, status.SessionType)
		if !valid {
			http.Error(w, "invalid Pico report", 400)
			return
		}
		report := sample.StockPico
		switch report.Kind {
		case "state":
			status.LastState, status.StateAt = report.State, &now
		case "error":
			status.LastError, status.ErrorAt = report, &now
		}
		if status.SessionID == "" || status.Ended {
			// State and error have only a UID. Keep them with the brewer while
			// idle. A retry after session termination never reopens the journal.
			if op != "log" {
				if _, err = s.db.Put(stockPicoReportKind, uid, status, revision); err != nil {
					http.Error(w, "storage unavailable", 500)
					return
				}
			}
			stockReply(w, "")
			return
		}
		write.Brew.ID = stockPicoJournalID(uid, status.SessionID)
		write.Reading = sample
		switch {
		case report.Kind == "log" && report.Event == "Brew Complete" && report.Step == report.Event:
			write.EndState = "completed"
		case report.Kind == "log" && report.Event == "Brew Canceled" && report.Step == report.Event:
			write.EndState = "canceled"
		case report.Kind == "state" && *report.State == 2:
			write.EndState = "ended"
		}
		status.HasReports, status.Ended = true, write.EndState != ""
	}
	write.MachineRecord = status
	if err = s.db.WriteStockJournal(write); err != nil {
		http.Error(w, "storage unavailable", 500)
		return
	}
	if op == "getsession" {
		stockReply(w, "#"+status.SessionID+"#")
		return
	}
	stockReply(w, "")
}

func exportStockPicoCSV(out *csv.Writer, samples []domain.Sample) {
	_ = out.Write([]string{"elapsed_ms", "observed_at", "report", "wort_f", "thermoblock_f", "step", "event", "device_state", "controller_status", "error_code", "rfid", "seconds_left", "shuttle_scale", "still_t1", "still_t2", "still_pressure"})
	integer := func(n *int) string {
		if n == nil {
			return ""
		}
		return strconv.Itoa(*n)
	}
	for _, sample := range samples {
		r := sample.StockPico
		if r == nil {
			continue
		}
		at := ""
		if sample.ObservedAt != nil {
			at = sample.ObservedAt.Format(time.RFC3339Nano)
		}
		_ = out.Write([]string{strconv.FormatInt(sample.ElapsedMS, 10), at, r.Kind, number(sample.WortF), number(sample.ThermoblockF), r.Step, r.Event, integer(r.State), integer(r.ControllerStatus), integer(r.ErrorCode), r.RFID, integer(r.SecondsLeft), number(r.ShuttleScale), number(sample.Readings["sensor_still_t1"]), number(sample.Readings["sensor_still_t2"]), number(sample.Readings["sensor_still_pressure"])})
	}
}
