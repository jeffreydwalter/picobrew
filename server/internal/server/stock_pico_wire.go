package server

import (
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

func stockPicoUID(value string) string {
	if len(value) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return strings.ToLower(value)
}

func stockPicoRFID(value string) string {
	// ISO 14443 UID lengths handled by the original cascade/select routine.
	switch len(value) {
	case 8, 14, 20:
	default:
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return strings.ToLower(value)
}

func stockPicoSetting(query url.Values, key string) (int, bool) {
	v := query.Get(key)
	if key == "abv" {
		v = strings.TrimSuffix(v, ".0")
	}
	n, err := strconv.Atoi(v)
	return n, err == nil && strconv.Itoa(n) == v && n >= -1 && n <= 10
}

func stockPicoList(items []stockPicoItem, associated bool) string {
	var b strings.Builder
	b.WriteByte('#')
	for _, item := range items {
		if associated {
			b.WriteString("hub-")
		}
		b.WriteString(item.ID)
		b.WriteByte(',')
		b.WriteString(stockPicoMenuName(item))
		b.WriteByte('|')
	}
	b.WriteByte('#')
	return b.String()
}

func stockPicoGetRecipe(items []stockPicoItem, query url.Values) string {
	abv, validABV := stockPicoSetting(query, "abv")
	ibu, validIBU := stockPicoSetting(query, "ibu")
	identifier := query.Get("rfid")
	if !validABV || !validIBU || identifier == "" {
		return "#Invalid|#"
	}
	tag := stockPicoRFID(identifier)
	for _, item := range items {
		if identifier != "hub-"+item.ID && (tag == "" || tag != item.RFID) {
			continue
		}
		record, err := item.recipeRecord(abv, ibu)
		if err != nil {
			return "#Invalid|#"
		}
		// A final hash terminates the optional image reader immediately.
		return "#" + record + "|#"
	}
	return "#Invalid|#"
}

func (s *Server) stockPicoWire(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Demo || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	// Original Pico sockets never carry browser origin or fetch metadata.
	if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		http.Error(w, "stock brewer request required", http.StatusForbidden)
		return
	}
	op := strings.ToLower(strings.TrimPrefix(r.URL.Path, "/API/pico/"))
	uidParam := "uid"
	switch op {
	case "register", "checkfirmware", "recipelist", "getassociatedpaks", "createpicopak", "getrecipe", "getactionsneeded", "getsession", "log", "error":
	case "picochangestate":
		uidParam = "picoUID"
	case "canusepicostill", "hascleanedack", "setcleanedack":
		uidParam = "picoUid"
	default:
		http.NotFound(w, r)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid stock Pico request", http.StatusBadRequest)
		return
	}
	for _, values := range query {
		if len(values) != 1 {
			http.Error(w, "duplicate stock Pico query field", http.StatusBadRequest)
			return
		}
	}
	uid := stockPicoUID(query.Get(uidParam))
	if uid == "" {
		http.Error(w, "invalid stock Pico UID", http.StatusBadRequest)
		return
	}
	s.stockPicoMu.Lock()
	defer s.stockPicoMu.Unlock()
	brewer, err := s.stockPicoForWire(uid)
	if errors.Is(err, store.ErrNotFound) {
		if op == "register" {
			s.rememberStockPico(uid)
			// The stock display interprets F:<code> as registration pending.
			stockReply(w, "#F:"+uid[len(uid)-8:]+"#")
			return
		}
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.storeError(w, err)
		return
	}
	switch op {
	case "register":
		stockReply(w, "#T#")
	case "getactionsneeded":
		stockReply(w, "##") // No owner-requested maintenance.
	case "checkfirmware", "canusepicostill", "hascleanedack", "setcleanedack":
		stockReply(w, "#F#") // No update, accessory permission or acknowledged cleaning on this adapter.
	case "recipelist", "getassociatedpaks":
		stockReply(w, stockPicoList(brewer.Items, op == "getassociatedpaks"))
	case "createpicopak":
		for _, item := range brewer.Items {
			if item.ID == query.Get("recipeid") {
				stockReply(w, "#hub-"+item.ID+"#")
				return
			}
		}
		stockReply(w, "##")
	case "getrecipe":
		stockReply(w, stockPicoGetRecipe(brewer.Items, query))
	case "getsession", "log", "picochangestate", "error":
		s.stockPicoReport(w, r, uid, op, brewer)
	}
}
