package server

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

const stockKind = "stock-zymatic"
const cleaningUser = "00000000000000000000000000000000"

var machineSerial = regexp.MustCompile(`^[A-Z0-9]{1,32}$`)
var resetPoint = regexp.MustCompile(`^-?[0-9]{1,10}(/-?[0-9]{1,10}){6}/[01]$`)

type stockItem struct {
	BatchLiters      float64              `json:"batch_liters,omitempty"`
	HopSlotsByMinute map[int]int          `json:"hop_slots_by_minute,omitempty"`
	RecipeID         string               `json:"recipe_id"`
	Revision         int64                `json:"revision"`
	Program          domain.MachineRecipe `json:"program"`
	Maintenance      bool                 `json:"maintenance,omitempty"`
}
type stockSession struct {
	ID          string
	Program     domain.MachineRecipe
	Snapshot    string
	Ended       bool
	Started     time.Time
	RecipeID    string
	Revision    int64
	Maintenance bool
}
type stockBrewer struct {
	Machine        string
	User           string
	Items          []stockItem
	Served         []stockItem
	ServedCleaning []stockItem
	Session        stockSession
	Requested      time.Time
}
type stockReference struct {
	Locations []int  `json:"locations,omitempty"`
	RecipeID  string `json:"recipe_id"`
	Revision  int64  `json:"revision"`
}
type stockSubmission struct {
	Recipes []stockReference `json:"recipes"`
	Publish bool             `json:"publish"`
}

func serial(value string) string { return strings.ToUpper(strings.TrimSpace(value)) }

// The owner explicitly selects each saved library revision for one brewer.
// Rebuilding the whole selection before committing keeps a batch all-or-nothing.
func (s *Server) stockMenu(w http.ResponseWriter, r *http.Request) {
	id := serial(chi.URLParam(r, "machine"))
	if !machineSerial.MatchString(id) {
		s.fail(w, 400, "machine", "Enter the serial shown in About Zymatic")
		return
	}
	s.stockMu.Lock()
	defer s.stockMu.Unlock()
	var b stockBrewer
	v, err := s.db.Get(stockKind, id, &b)
	if errors.Is(err, store.ErrNotFound) {
		b.Machine = id
		b.Items = []stockItem{}
	} else if err != nil {
		s.internal(w, err)
		return
	}
	if r.Method == http.MethodGet {
		var diagnostics stockDiagnostics
		if _, err := s.db.Get(stockDiagnosticsKind, id, &diagnostics); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.storeError(w, err)
			return
		}
		s.json(w, 200, map[string]any{"machine": id, "items": b.Items, "last_requested": b.Requested, "diagnostics": diagnostics})
		return
	}
	if r.Method == http.MethodDelete {
		rid := chi.URLParam(r, "recipe_id")
		for i, item := range b.Items {
			if item.RecipeID == rid {
				b.Items = append(b.Items[:i], b.Items[i+1:]...)
				if _, err = s.db.Put(stockKind, id, b, v); err != nil {
					s.storeError(w, err)
					return
				}
				break
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var in stockSubmission
	if !s.decode(w, r, &in) {
		return
	}
	changes, ok := s.compileStockSelection(w, in.Recipes)
	if !ok {
		return
	}
	for _, item := range changes {
		replaced := false
		for i, existing := range b.Items {
			if existing.RecipeID == item.RecipeID {
				b.Items[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			b.Items = append(b.Items, item)
		}
	}
	counts := map[bool]int{}
	for _, item := range b.Items {
		if recipe.ValidateStockZymaticProgram(item.Program) == nil {
			counts[item.Maintenance]++
		}
	}
	if counts[false] > 10 || counts[true] > 10 {
		s.fail(w, 422, "stock_limit", "A stock menu holds ten programs. Remove an entry before adding another.")
		return
	}
	if in.Publish {
		if b.User == "" {
			b.User = randomID()
		}
		if _, err := s.db.Put(stockKind, id, b, v); err != nil {
			s.storeError(w, err)
			return
		}
	}
	s.json(w, 200, map[string]any{"items": changes, "published": in.Publish})
}

// Offline provisioning compiles saved revisions without looking up or mutating a brewer.
func (s *Server) stockOfflinePreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Recipes []stockReference `json:"recipes"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	items, ok := s.compileStockSelection(w, in.Recipes)
	if ok {
		s.json(w, http.StatusOK, map[string]any{"items": items})
	}
}

func (s *Server) compileStockSelection(w http.ResponseWriter, refs []stockReference) ([]stockItem, bool) {
	if len(refs) == 0 || len(refs) > 10 {
		s.fail(w, 422, "stock_selection", "Select one to ten saved recipes")
		return nil, false
	}
	profile, ok := s.selectProfile("zymatic")
	if !ok {
		s.fail(w, 500, "profile", "Could not load the Zymatic profile")
		return nil, false
	}
	changes := make([]stockItem, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		var saved domain.Recipe
		if _, err := s.db.Get("recipe", ref.RecipeID, &saved); err != nil {
			s.storeError(w, err)
			return nil, false
		}
		if ref.Revision != saved.Revision {
			s.fail(w, 409, "recipe_revision", "The recipe changed. Reopen the transfer to review it.")
			return nil, false
		}
		if seen[ref.RecipeID] {
			s.fail(w, 400, "stock_selection", "A recipe was selected twice")
			return nil, false
		}
		seen[ref.RecipeID] = true
		if ref.Locations != nil {
			if recipe.IsNative(saved) || len(ref.Locations) != len(saved.Steps) {
				s.fail(w, 422, "stock_routing", "Provide one flow path for each ingredient recipe step")
				return nil, false
			}
			for i, location := range ref.Locations {
				if location < 1 || location > 6 {
					s.fail(w, 422, "stock_routing", "Choose each Zymatic flow path")
					return nil, false
				}
				saved.Steps[i].Location = location
			}
			if len(saved.Ingredients) == 0 {
				saved.SourceModel = "zymatic"
			}
		}
		item, err := compileStockRecipe(saved, profile)
		if err != nil {
			s.fail(w, 422, "stock_program", saved.Name+": "+err.Error())
			return nil, false
		}
		changes = append(changes, item)
	}
	return changes, true
}

func compileStockRecipe(saved domain.Recipe, profile domain.Profile) (stockItem, error) {
	item := stockItem{RecipeID: saved.ID, Revision: saved.Revision, Maintenance: saved.Kind == "maintenance"}
	var p domain.MachineRecipe
	var err error
	if len(saved.Ingredients) > 0 && !recipe.IsNative(saved) {
		var plan domain.Adapted
		plan, err = recipe.Translate(saved, profile, 0)
		if err == nil {
			if plan.MachineRecipe == nil {
				err = errors.New(strings.Join(plan.DeploymentBlockers, "; "))
			} else {
				p = *plan.MachineRecipe
				item.BatchLiters = plan.Recipe.BatchLiters
				item.HopSlotsByMinute = plan.HopSlotsByMinute
			}
		}
	} else {
		p, err = recipe.StockZymaticProgram(saved, profile)
	}
	if err == nil {
		err = recipe.ValidateStockZymaticProgram(p)
	}
	item.Program = p
	return item, err
}
func stockReply(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=us-ascii")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("\r\n" + body))
}

func (s *Server) stockMachineForSession(sid string) (string, error) {
	var b domain.Brew
	_, err := s.db.Get("brew", stockBrewID(sid), &b)
	if err == nil && b.Source == stockReportSource {
		return b.StockSerial, nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	// Sessions predating the journal only have their ID in the brewer record.
	brewers, err := listRecords[stockBrewer](s, stockKind)
	if err != nil {
		return "", err
	}
	for _, brewer := range brewers {
		if brewer.Session.ID == sid {
			return brewer.Machine, nil
		}
	}
	return "", nil
}

func (s *Server) stockWire(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Demo {
		http.NotFound(w, r)
		return
	}
	op := strings.ToLower(strings.TrimPrefix(r.URL.Path, "/API/"))
	q := r.URL.Query()
	switch op {
	case "zymaticfirmwarecheck":
		// This route is an OEM release notification. A custom USB image is
		// a different distribution channel and is never an OEM update offer.
		stockReply(w, "#F#")
		return
	case "firstsetup", "sessionerror":
		s.stockDiagnosticsReport(w, op, q)
		return
	case "checksync":
		stockReply(w, "#T#")
		return
	case "logsession", "recoversession", "usersetup", "syncuser":
	default:
		http.NotFound(w, r)
		return
	}
	s.stockMu.Lock()
	defer s.stockMu.Unlock()
	var b stockBrewer
	var version int64
	var err error
	bySession := op == "recoversession" || (op == "logsession" && q.Get("code") != "0")
	missingSession := func() {
		if op == "recoversession" && q.Get("code") == "1" {
			stockReply(w, "#!#")
			return
		}
		http.Error(w, "unknown session", 404)
	}
	if bySession {
		sid := q.Get("session")
		if len(sid) != 32 {
			http.Error(w, "unknown session", 404)
			return
		}
		b.Machine, err = s.stockMachineForSession(sid)
		if err != nil {
			http.Error(w, "storage unavailable", 500)
			return
		}
		if b.Machine == "" {
			missingSession()
			return
		}
	} else {
		b.Machine = serial(q.Get("machine"))
	}
	if !machineSerial.MatchString(b.Machine) {
		http.Error(w, "invalid machine", 400)
		return
	}
	if op == "usersetup" || op == "syncuser" {
		s.rememberZymatic(b.Machine)
	}
	version, err = s.db.Get(stockKind, b.Machine, &b)
	if errors.Is(err, store.ErrNotFound) {
		stockReply(w, "##")
		return
	}
	if err != nil {
		http.Error(w, "storage unavailable", 500)
		return
	}
	if bySession && b.Session.ID != q.Get("session") {
		missingSession()
		return
	}
	save := func() bool {
		if _, e := s.db.Put(stockKind, b.Machine, b, version); e != nil {
			http.Error(w, "storage unavailable", 500)
			return false
		}
		return true
	}
	switch op {
	case "usersetup":
		stockReply(w, "#"+b.User+"/PicoBrew Hub|#")
	case "syncuser":
		user := q.Get("user")
		if user != b.User && user != cleaningUser {
			stockReply(w, "##")
			return
		}
		var list strings.Builder
		list.WriteByte('#')
		var served []stockItem
		for _, item := range b.Items {
			if item.Maintenance == (user == cleaningUser) && recipe.ValidateStockZymaticProgram(item.Program) == nil {
				served = append(served, item)
				list.WriteString(recipe.StockZymaticRecord(item.Program))
				list.WriteByte('|')
			}
		}
		list.WriteByte('#')
		if user == cleaningUser {
			b.ServedCleaning = served
		} else {
			b.Served = served
		}
		b.Requested = time.Now().UTC()
		if save() {
			stockReply(w, list.String())
		}
	case "recoversession":
		if b.Session.Ended || recipe.ValidateStockZymaticProgram(b.Session.Program) != nil {
			if q.Get("code") == "1" {
				stockReply(w, "#!#")
			} else {
				http.Error(w, "no recoverable Zymatic program", http.StatusNotFound)
			}
			return
		}
		if q.Get("code") == "0" {
			stockReply(w, "#"+recipe.StockZymaticRecord(b.Session.Program)+"|!#")
		} else if q.Get("code") == "1" {
			if b.Session.Snapshot == "" {
				stockReply(w, "#!#")
			} else {
				stockReply(w, "#"+b.Session.Snapshot+"#")
			}
		} else {
			http.Error(w, "unknown recovery operation", 400)
		}
	case "logsession":
		switch q.Get("code") {
		case "0":
			user := q.Get("user")
			if user != b.User && user != cleaningUser {
				http.Error(w, "unknown user", 404)
				return
			}
			served := b.Served
			if user == cleaningUser {
				served = b.ServedCleaning
			}
			for _, item := range served {
				if item.Program.ID == q.Get("recipe") && recipe.ValidateStockZymaticProgram(item.Program) == nil {
					previous := b.Session
					now := time.Now().UTC()
					// The create request has no nonce. Return the outstanding ID on
					// an immediate retry before the brewer has reported any activity.
					sameProgram := previous.Program.ID == item.Program.ID && previous.RecipeID == item.RecipeID && previous.Revision == item.Revision && previous.Maintenance == item.Maintenance
					age := now.Sub(previous.Started)
					if !previous.Ended && sameProgram && age >= 0 && age < time.Minute && previous.Snapshot == "" {
						var journal domain.Brew
						if _, err := s.db.Get("brew", stockBrewID(previous.ID), &journal); err == nil && journal.SampleCount == 0 {
							stockReply(w, "#"+previous.ID+"#")
							return
						} else if err != nil && !errors.Is(err, store.ErrNotFound) {
							http.Error(w, "storage unavailable", 500)
							return
						}
					}
					b.Session = stockSession{ID: randomID(), Program: item.Program, RecipeID: item.RecipeID, Revision: item.Revision, Maintenance: item.Maintenance, Started: now}
					write := s.stockJournal(b, version, now)
					if previous.ID != "" && !previous.Ended {
						write.PreviousID = stockBrewID(previous.ID)
					}
					if err := s.db.WriteStockJournal(write); err != nil {
						http.Error(w, "storage unavailable", 500)
						return
					}
					stockReply(w, "#"+b.Session.ID+"#")
					return
				}
			}
			http.Error(w, "unknown recipe", 404)
		case "1", "2", "3":
			if b.Session.Ended {
				stockReply(w, "")
				return
			}
			code := q.Get("code")
			newCheckpoint := code == "2" && resetPoint.MatchString(q.Get("step")) && b.Session.Snapshot != q.Get("step")
			if newCheckpoint {
				b.Session.Snapshot = q.Get("step")
			}
			b.Session.Ended = code == "3"
			write := s.stockJournal(b, version, time.Now().UTC())
			if b.Session.Ended {
				write.EndState = "ended"
			}
			write.Reading = stockReading(code, q)
			if write.EndState == "" && !newCheckpoint && write.Reading == nil {
				stockReply(w, "")
				return
			}
			if err := s.db.WriteStockJournal(write); err != nil {
				http.Error(w, "storage unavailable", 500)
				return
			}
			stockReply(w, "")
		default:
			http.Error(w, "unknown session operation", 400)
		}
	}
}
