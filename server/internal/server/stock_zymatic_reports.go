package server

import (
	"encoding/csv"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

func exportStockZymaticCSV(out *csv.Writer, samples []domain.Sample) {
	_ = out.Write([]string{"elapsed_ms", "observed_at", "report", "wort_f", "primary_heat_f", "board_f", "backup_heat_f", "step_name", "step_state", "error_flags", "error_code", "reset_point"})
	integer := func(v *int) string {
		if v == nil {
			return ""
		}
		return strconv.Itoa(*v)
	}
	for _, s := range samples {
		report := s.StockZymatic
		if report == nil {
			continue
		}
		at := ""
		if s.ObservedAt != nil {
			at = s.ObservedAt.Format(time.RFC3339Nano)
		}
		_ = out.Write([]string{
			strconv.FormatInt(s.ElapsedMS, 10), at, report.Kind,
			number(s.WortF), number(s.ThermoblockF), number(s.Readings["sensor_board_f"]), number(s.Readings["sensor_backup_heat_f"]),
			report.StepName, integer(report.State), integer(report.ErrorFlags), integer(report.ErrorCode), report.ResetPoint,
		})
	}
}

const stockReportSource = "zymatic-report"
const stockDiagnosticsKind = "stock-zymatic-diagnostics"

func stockBrewID(session string) string { return "zymatic-session-" + session }

var stockSampleFields = []string{"elapsed_ms", "wort_f", "thermoblock_f", "sensor_board_f", "sensor_backup_heat_f"}
var stockFieldMetadata = map[string]domain.FieldMetadata{
	"thermoblock_f":        {Label: "Primary heater", Unit: "F", Kind: "temperature"},
	"sensor_board_f":       {Label: "Control board", Unit: "F", Kind: "temperature"},
	"sensor_backup_heat_f": {Label: "Backup heater", Unit: "F", Kind: "temperature"},
}

func (s *Server) stockJournal(b stockBrewer, version int64, at time.Time) store.StockJournalWrite {
	session := b.Session
	brew := domain.Brew{
		ID: stockBrewID(session.ID), DeviceID: stockMemberPrefix + b.Machine,
		DeviceName: "Zymatic · " + b.Machine, Model: "zymatic", StockSerial: b.Machine, Source: stockReportSource,
		RecipeID: session.RecipeID, RecipeRevision: session.Revision, RecipeName: session.Program.Name,
		Command: "brew", State: "recording", ObservedAt: at, SampleFields: stockSampleFields, FieldMetadata: stockFieldMetadata,
	}
	if !session.Started.IsZero() {
		brew.StartedAt = &session.Started
	}
	if session.Maintenance {
		brew.Command = "maintenance"
	}
	for _, d := range s.fleet() {
		if d.Model == "zymatic" && d.Status != nil && d.Status.StockSerial == b.Machine {
			brew.DeviceID, brew.DeviceName = d.ID, d.Name
			break
		}
	}
	return store.StockJournalWrite{MachineKind: stockKind, Machine: b.Machine, MachineRecord: b, MachineRevision: version, Brew: brew, At: at}
}

func reportNumber(value string) *int {
	n, err := strconv.ParseUint(value, 10, 31)
	if err != nil {
		return nil
	}
	v := int(n)
	return &v
}
func reportText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}
func newStockReading(report domain.ZymaticReport) *domain.Sample {
	return &domain.Sample{ZymaticHistory: true, StockZymatic: &report, Source: stockReportSource, CurrentStep: -1, Compartment: -1}
}
func stockReading(code string, q url.Values) *domain.Sample {
	switch code {
	case "1":
		if !reportText(q.Get("data"), 128) {
			return nil
		}
		report := domain.ZymaticReport{Kind: "step", StepName: q.Get("data")}
		if n, err := strconv.ParseInt(q.Get("state"), 10, 32); err == nil {
			value := int(n)
			report.State = &value
		}
		return newStockReading(report)
	case "2":
		s := newStockReading(domain.ZymaticReport{Kind: "temperatures", ErrorFlags: reportNumber(q.Get("state"))})
		if point := q.Get("step"); resetPoint.MatchString(point) {
			s.StockZymatic.ResetPoint = point
		}
		raw := q.Get("data")
		if len(raw) > 128 {
			return nil
		}
		seen := map[string]bool{}
		for _, pair := range strings.Split(strings.TrimSuffix(raw, "|"), "|") {
			key, rawTemp, ok := strings.Cut(pair, "/")
			temp, err := strconv.ParseInt(rawTemp, 10, 32)
			if !ok || err != nil || seen[key] {
				return nil
			}
			seen[key] = true
			f := float64(temp)
			switch key {
			case "1":
				s.ThermoblockF = &f
			case "2":
				s.WortF = &f
			case "3", "4":
				if s.Readings == nil {
					s.Readings = map[string]*float64{}
				}
				name := "sensor_board_f"
				if key == "4" {
					name = "sensor_backup_heat_f"
				}
				s.Readings[name] = &f
			default:
				return nil
			}
		}
		return s
	}
	return nil
}

type stockDiagnostic struct {
	At      time.Time `json:"at"`
	Session string    `json:"session,omitempty"`
	Code    int       `json:"code"`
}
type stockDiagnostics struct {
	Sensors      map[string]string `json:"sensors,omitempty"`
	RegisteredAt *time.Time        `json:"registered_at,omitempty"`
	Errors       []stockDiagnostic `json:"recent_errors,omitempty"`
}

// Error reporting can precede a session or arrive after its successor starts.
// An unassociated report belongs to the serial's diagnostics, never to another brew.
func (s *Server) stockDiagnosticsReport(w http.ResponseWriter, op string, q url.Values) {
	machine, sensors, _ := strings.Cut(q.Get("machine"), "|")
	machine = serial(machine)
	if !machineSerial.MatchString(machine) {
		http.Error(w, "invalid machine", 400)
		return
	}
	s.stockMu.Lock()
	defer s.stockMu.Unlock()
	var d stockDiagnostics
	v, err := s.db.Get(stockDiagnosticsKind, machine, &d)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		http.Error(w, "storage unavailable", 500)
		return
	}
	if op == "firstsetup" {
		d.Sensors = map[string]string{}
		for _, entry := range strings.Split(sensors, "/") {
			id, index, ok := strings.Cut(entry, ",")
			if !ok || !reportText(id, 64) || len(index) != 1 || index[0] < '1' || index[0] > '4' || d.Sensors[index] != "" {
				http.Error(w, "invalid sensor registration", 400)
				return
			}
			d.Sensors[index] = id
		}
		if len(d.Sensors) != 4 || q.Get("admin") != "0" {
			http.Error(w, "invalid sensor registration", 400)
			return
		}
		now := time.Now().UTC()
		d.RegisteredAt = &now
		s.rememberZymatic(machine)
	} else {
		code := reportNumber(q.Get("errorcode"))
		sid := q.Get("session")
		if !q.Has("session") || code == nil || (sid != "" && !reportText(sid, 32)) {
			http.Error(w, "invalid error report", 400)
			return
		}
		now := time.Now().UTC()
		var b domain.Brew
		_, err := s.db.Get("brew", stockBrewID(sid), &b)
		var write store.StockJournalWrite
		switch {
		case err == nil:
			if b.Source != stockReportSource || b.StockSerial != machine {
				http.Error(w, "unknown session", 404)
				return
			}
			write = store.StockJournalWrite{Brew: b, At: now}
		case errors.Is(err, store.ErrNotFound):
			// An in-flight session created by an older Hub has no journal yet.
			var legacy stockBrewer
			version, err := s.db.Get(stockKind, machine, &legacy)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				http.Error(w, "storage unavailable", 500)
				return
			}
			if sid != "" && err == nil && legacy.Session.ID == sid {
				write = s.stockJournal(legacy, version, now)
				if legacy.Session.Ended {
					write.Brew.State = "ended"
				}
			}
		default:
			http.Error(w, "storage unavailable", 500)
			return
		}
		if write.Brew.ID != "" {
			write.Reading = newStockReading(domain.ZymaticReport{Kind: "error", ErrorCode: code})
			if err := s.db.WriteStockJournal(write); err != nil {
				http.Error(w, "storage unavailable", 500)
				return
			}
			stockReply(w, "")
			return
		}
		d.Errors = append(d.Errors, stockDiagnostic{At: now, Session: sid, Code: *code})
		if len(d.Errors) > 32 {
			d.Errors = d.Errors[len(d.Errors)-32:]
		}
	}
	if _, err := s.db.Put(stockDiagnosticsKind, machine, d, v); err != nil {
		http.Error(w, "storage unavailable", 500)
		return
	}
	stockReply(w, "")
}
