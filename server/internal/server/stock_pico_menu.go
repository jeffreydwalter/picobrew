package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

const stockPicoKind = "stock-pico"

type stockPicoBrewer struct {
	UID   string          `json:"uid"`
	Name  string          `json:"name"`
	Model string          `json:"model"`
	Items []stockPicoItem `json:"items"`
}

func (s *Server) stockPicoForWire(uid string) (stockPicoBrewer, error) {
	var b stockPicoBrewer
	_, err := s.db.Get(stockPicoKind, uid, &b)
	return b, err
}

func (s *Server) stockPicos(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		machines, err := listRecords[stockPicoBrewer](s, stockPicoKind)
		if err != nil {
			s.storeError(w, err)
			return
		}
		known := make(map[string]bool, len(machines))
		for _, b := range machines {
			known[b.UID] = true
		}
		recent := []string{}
		s.discovery.mu.Lock()
		for uid, seen := range s.discovery.picos {
			if !known[uid] && time.Since(seen) <= 15*time.Minute {
				recent = append(recent, uid)
			}
		}
		s.discovery.mu.Unlock()
		sort.Strings(recent)
		rows, err := s.stockPicoWithReports(machines)
		if err != nil {
			s.storeError(w, err)
			return
		}
		s.json(w, http.StatusOK, map[string]any{"machines": rows, "recent_uids": recent})
		return
	}
	if s.cfg.Demo {
		s.fail(w, 403, "simulation", "Stock brewers connect to a live Hub")
		return
	}
	s.stockPicoMu.Lock()
	defer s.stockPicoMu.Unlock()
	if r.Method == http.MethodDelete {
		uid := stockPicoUID(chi.URLParam(r, "machine"))
		if uid == "" {
			s.fail(w, 400, "uid", "Invalid brewer UID")
			return
		}
		if err := s.db.Delete(stockPicoKind, uid); err != nil {
			s.storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var in struct {
		UID   string `json:"uid"`
		Name  string `json:"name"`
		Model string `json:"model"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	b := stockPicoBrewer{UID: stockPicoUID(in.UID), Name: strings.TrimSpace(in.Name), Model: in.Model, Items: []stockPicoItem{}}
	if b.UID == "" || b.Name == "" || len(b.Name) > 120 || strings.IndexFunc(b.Name, unicode.IsControl) >= 0 || !slices.Contains([]string{"pico-s", "pico-pro", "pico-c"}, b.Model) {
		s.fail(w, 422, "stock_pico", "Choose a Pico C, S or Pro and enter its UID and a machine name")
		return
	}
	if _, err := s.db.Put(stockPicoKind, b.UID, b, 0); err != nil {
		s.storeError(w, err)
		return
	}
	s.json(w, http.StatusCreated, b)
}

func (s *Server) stockPicoMenu(w http.ResponseWriter, r *http.Request) {
	uid := stockPicoUID(chi.URLParam(r, "machine"))
	if uid == "" || s.cfg.Demo {
		http.NotFound(w, r)
		return
	}
	s.stockPicoMu.Lock()
	defer s.stockPicoMu.Unlock()
	var b stockPicoBrewer
	version, err := s.db.Get(stockPicoKind, uid, &b)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		id := chi.URLParam(r, "recipe_id")
		b.Items = slices.DeleteFunc(b.Items, func(item stockPicoItem) bool { return item.RecipeID == id })
		if _, err = s.db.Put(stockPicoKind, uid, b, version); err != nil {
			s.storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var in struct {
		Recipes []struct {
			RecipeID string  `json:"recipe_id"`
			Revision int64   `json:"revision"`
			RFID     *string `json:"rfid,omitempty"`
		} `json:"recipes"`
		Publish    bool   `json:"publish"`
		ReviewHash string `json:"review_hash,omitempty"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	if len(in.Recipes) == 0 {
		s.fail(w, 422, "selection", "Select a saved recipe")
		return
	}
	profile, ok := s.selectProfile(b.Model)
	if !ok {
		s.fail(w, 500, "profile", "The brewer profile is unavailable")
		return
	}
	changes := make([]stockPicoItem, 0, len(in.Recipes))
	selected := map[string]bool{}
	// Resolve a batch against one set of saved library revisions.
	s.recipeMu.Lock()
	defer s.recipeMu.Unlock()
	for _, ref := range in.Recipes {
		if strings.TrimSpace(ref.RecipeID) == "" || ref.Revision < 1 {
			s.fail(w, 422, "selection", "Select a saved recipe and its revision")
			return
		}
		if selected[ref.RecipeID] {
			s.fail(w, 422, "selection", "Each recipe can be selected once")
			return
		}
		selected[ref.RecipeID] = true
		var saved domain.Recipe
		if _, err = s.db.Get("recipe", ref.RecipeID, &saved); err != nil {
			s.storeError(w, err)
			return
		}
		if saved.Revision != ref.Revision {
			s.fail(w, 409, "recipe_revision", "The recipe changed. Reopen the transfer to review it.")
			return
		}
		item, err := compileStockPicoRecipe(saved, profile)
		if err != nil {
			s.fail(w, 422, "stock_recipe", saved.Name+": "+err.Error())
			return
		}
		pos := slices.IndexFunc(b.Items, func(old stockPicoItem) bool { return old.RecipeID == item.RecipeID })
		if ref.RFID != nil {
			item.RFID = stockPicoRFID(*ref.RFID)
			if *ref.RFID != "" && item.RFID == "" {
				s.fail(w, 422, "rfid", "Enter an RFID tag with 8, 14, or 20 hexadecimal characters")
				return
			}
		} else if pos >= 0 {
			item.RFID = b.Items[pos].RFID
		}
		if pos >= 0 {
			b.Items[pos] = item
		} else {
			b.Items = append(b.Items, item)
		}
		changes = append(changes, item)
	}
	if err = validateStockPicoAssignments(b.Items); err != nil {
		s.fail(w, 422, "stock_menu", err.Error())
		return
	}
	review, err := json.Marshal(b)
	if err != nil {
		s.internal(w, err)
		return
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(review))
	if in.Publish {
		if in.ReviewHash != hash {
			s.fail(w, 409, "menu_changed", "Preview the current brewer menu before publishing")
			return
		}
		if _, err = s.db.Put(stockPicoKind, uid, b, version); err != nil {
			s.storeError(w, err)
			return
		}
	}
	s.json(w, 200, map[string]any{"items": changes, "published": in.Publish, "review_hash": hash})
}

func validateStockPicoAssignments(items []stockPicoItem) error {
	ids, names, tags := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, item := range items {
		name := strings.ToLower(stockPicoMenuName(item))
		if ids[item.ID] || names[name] || (item.RFID != "" && tags[item.RFID]) {
			return errors.New("Two recipes use the same brewer ID, displayed name, or RFID tag. Each menu entry must be distinct")
		}
		ids[item.ID], names[name], tags[item.RFID] = true, true, true
	}
	return nil
}
