package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

type brewGroup struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	DeviceIDs   []string             `json:"device_ids"`
	Revision    int64                `json:"revision"`
	MemberSince map[string]time.Time `json:"member_since"`
}
type brewInvite struct {
	SourceHistoryID uint64    `json:"source_history_id,omitempty"`
	HubConfirmation bool      `json:"hub_confirmation,omitempty"`
	ID              string    `json:"id"`
	GroupID         string    `json:"group_id"`
	GroupName       string    `json:"group_name"`
	SourceDeviceID  string    `json:"source_device_id"`
	SourceName      string    `json:"source_name"`
	TargetDeviceID  string    `json:"target_device_id"`
	GroupSessionID  string    `json:"group_session_id"`
	MenuSerial      string    `json:"menu_serial,omitempty"`
	SourceUID       string    `json:"source_uid"`
	TargetUID       string    `json:"target_uid,omitempty"`
	SourceEvent     string    `json:"source_event"`
	Action          string    `json:"action"`
	RecipeName      string    `json:"recipe_name,omitempty"`
	RecipeID        string    `json:"recipe_id,omitempty"`
	RecipeRevision  int64     `json:"recipe_revision,omitempty"`
	TargetSlot      string    `json:"target_slot,omitempty"`
	SlotRevision    uint32    `json:"slot_revision,omitempty"`
	State           string    `json:"state"`
	ExpiresAt       time.Time `json:"expires_at"`
	Message         string    `json:"message,omitempty"`
}

const groupKind = "brew-group"
const inviteKind = "brew-invite"

func listRecords[T any](s *Server, kind string) ([]T, error) {
	rows, err := s.db.List(kind)
	if err != nil {
		return nil, err
	}
	result := make([]T, 0, len(rows))
	for _, row := range rows {
		var v T
		if err := json.Unmarshal(row, &v); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}
func groupFor(groups []brewGroup, id string) *brewGroup {
	for i := range groups {
		if slices.Contains(groups[i].DeviceIDs, id) {
			return &groups[i]
		}
	}
	return nil
}
func (s *Server) groupDevice(id string) *runtimeDevice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.devices[id]
}
func (s *Server) brewGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.deviceMu.Lock()
		defer s.deviceMu.Unlock()
	}
	s.groupMu.Lock()
	defer s.groupMu.Unlock()
	groups, err := listRecords[brewGroup](s, groupKind)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		candidates, err := s.stockGroupCandidates()
		if err != nil {
			s.storeError(w, err)
			return
		}
		s.json(w, 200, map[string]any{"groups": groups, "stock_candidates": candidates})
		return
	}
	id := chi.URLParam(r, "group")
	var input struct {
		Name      string   `json:"name"`
		DeviceIDs []string `json:"device_ids"`
		Revision  int64    `json:"revision"`
	}
	if r.Method == http.MethodDelete {
		input.Revision, _ = strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	} else if !s.decode(w, r, &input) {
		return
	}
	var old brewGroup
	if id != "" {
		if _, err = s.db.Get(groupKind, id, &old); err != nil {
			s.storeError(w, err)
			return
		}
	}
	if input.Revision != old.Revision {
		s.fail(w, 409, "group_changed", "Refresh the group before saving changes")
		return
	}
	if r.Method == http.MethodDelete {
		if err = s.db.Delete(groupKind, id); err != nil {
			s.storeError(w, err)
			return
		}
		if err = s.cancelGroupInvites(id, nil); err != nil {
			s.storeError(w, err)
			return
		}
		s.json(w, 200, map[string]bool{"deleted": true})
		return
	}
	name := strings.TrimSpace(input.Name)
	if len(input.DeviceIDs) < 2 || len(input.DeviceIDs) > 16 || !utf8.ValidString(name) || len(name) == 0 || len(name) > 80 {
		s.fail(w, 422, "invalid_group", "Name the group and select 2 to 16 brewers")
		return
	}
	seen := map[string]bool{}
	for _, member := range input.DeviceIDs {
		if seen[member] {
			s.fail(w, 422, "invalid_member", "Choose distinct registered brewers")
			return
		}
		seen[member] = true
		if err := s.validateGroupMember(member, old.DeviceIDs); err != nil {
			s.fail(w, 422, "invalid_member", err.Error())
			return
		}
		if other := groupFor(groups, member); other != nil && other.ID != id {
			s.fail(w, 409, "already_grouped", "Remove the brewer from its existing group first")
			return
		}
	}
	if id == "" {
		id = randomID()
	}
	next := brewGroup{ID: id, Name: name, DeviceIDs: input.DeviceIDs, Revision: old.Revision + 1}
	next.MemberSince = make(map[string]time.Time, len(next.DeviceIDs))
	now := time.Now().UTC()
	for _, member := range next.DeviceIDs {
		since := old.MemberSince[member]
		if since.IsZero() {
			since = now
		}
		next.MemberSince[member] = since
	}
	if _, err = s.db.Put(groupKind, id, next, old.Revision); err != nil {
		s.storeError(w, err)
		return
	}
	if err = s.cancelGroupInvites(id, next.DeviceIDs); err != nil {
		s.storeError(w, err)
		return
	}
	s.json(w, 200, next)
}
func activeInvite(state string) bool {
	return slices.Contains([]string{"waiting", "offering", "pending", "responding"}, state)
}
func (s *Server) cancelGroupInvites(id string, members []string) error {
	list, err := listRecords[brewInvite](s, inviteKind)
	if err != nil {
		return err
	}
	for _, v := range list {
		if v.GroupID == id && activeInvite(v.State) && (!slices.Contains(members, v.SourceDeviceID) || !slices.Contains(members, v.TargetDeviceID)) {
			v.State = "withdrawn"
			v.Message = "Group membership changed."
			if err := s.saveInvite(v); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Server) saveInvite(v brewInvite) error {
	_, err := s.db.Put(inviteKind, v.TargetDeviceID, v, -1)
	return err
}

type groupCandidate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

const stockMemberPrefix = "stock-zymatic:"

func (s *Server) stockGroupCandidates() ([]groupCandidate, error) {
	machines, err := listRecords[stockBrewer](s, stockKind)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, b := range machines {
		seen[b.Machine] = true
	}
	s.discovery.mu.Lock()
	for id, at := range s.discovery.legacy {
		if time.Since(at) <= 15*time.Minute {
			seen[id] = true
		}
	}
	s.discovery.mu.Unlock()
	out := []groupCandidate{}
	for id := range seen {
		if machineSerial.MatchString(id) {
			out = append(out, groupCandidate{stockMemberPrefix + id, "Zymatic · " + id})
		}
	}
	slices.SortFunc(out, func(a, b groupCandidate) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
func (s *Server) validateGroupMember(id string, previous []string) error {
	if serial, ok := strings.CutPrefix(id, stockMemberPrefix); ok && machineSerial.MatchString(serial) {
		if slices.Contains(previous, id) {
			return nil
		}
		candidates, err := s.stockGroupCandidates()
		if err != nil {
			return err
		}
		for _, c := range candidates {
			if c.ID == id {
				return nil
			}
		}
	} else if d := s.groupDevice(id); d != nil {
		d.mu.RLock()
		v := d.view
		d.mu.RUnlock()
		if v.Adapter == "pico-standalone" && slices.Contains([]string{"pico-s", "pico-pro", "pico-c", "z-series", "zymatic"}, v.Model) {
			return nil
		}
	}
	return errors.New("Choose a registered brewer or a known Zymatic menu")
}
func (s *Server) removeGroupMember(id string) error {
	s.groupMu.Lock()
	defer s.groupMu.Unlock()
	groups, err := listRecords[brewGroup](s, groupKind)
	if err != nil {
		return err
	}
	g := groupFor(groups, id)
	if g == nil {
		return nil
	}
	g.DeviceIDs = slices.DeleteFunc(g.DeviceIDs, func(v string) bool { return v == id })
	delete(g.MemberSince, id)
	if len(g.DeviceIDs) < 2 {
		g.DeviceIDs = nil
		err = s.db.Delete(groupKind, g.ID)
	} else {
		g.Revision++
		_, err = s.db.Put(groupKind, g.ID, g, g.Revision-1)
	}
	if err != nil {
		return err
	}
	return s.cancelGroupInvites(g.ID, g.DeviceIDs)
}
