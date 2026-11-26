package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

const stockZMemberPrefix = "stock-z:"
const stockZTransferKind = "stock-z-transfer"

// One receipt per registered module identifies its most recent acknowledged Hub
// upload. A later replacement retires it. Group offers use the pinned revision
// and persist their start and result so retrying never creates a second event.
type stockZTransfer struct {
	ID            string
	RecipeID      string
	Revision      int64
	ReviewHash    string
	GroupID       string
	GroupRevision int64
	Started       time.Time
	Offered       *int
}

func (s *Server) stockZInviteProgram(i brewInvite) (stockZProgramReview, error) {
	if i.Action != "brew" || i.GroupKitSelection.Present() {
		return stockZProgramReview{}, errors.New("Original Z firmware accepts compatible Program Maker recipes for dial start")
	}
	saved, err := s.groupLibrary(groupRecipe{LibraryID: i.RecipeID, Revision: i.RecipeRevision})
	if err != nil {
		return stockZProgramReview{}, err
	}
	return s.reviewStockZProgram(saved)
}

func (s *Server) prepareStockZInvite(i *brewInvite, id string) error {
	var target stockZProgramMachine
	if _, err := s.db.Get(stockZProgramKind, id, &target); err != nil {
		return err
	}
	i.StockZName = target.Name
	i.StockZURL = target.BaseURL
	if program, err := s.stockZInviteProgram(*i); err != nil {
		i.State = "unavailable"
		i.Message = err.Error()
	} else {
		i.StockZReview = &program
		i.State = "pending"
		i.Message = "Staging replaces this Z’s working recipe. Start at its dial."
	}
	return nil
}

// respondBrewInvite already holds groupMu and has checked the recipient, group
// membership, expiry and pending state. Persist intent before the OEM write.
func (s *Server) respondStockZGroup(w http.ResponseWriter, r *http.Request, i *brewInvite, id string, accept, confirm bool) {
	if s.cfg.Demo {
		http.NotFound(w, r)
		return
	}
	if !accept {
		i.State = "declined"
		i.Message = "Invitation declined."
	} else {
		if !confirm {
			s.fail(w, 422, "confirm_replace", "Confirm that the intended Z is idle and its working recipe can be replaced")
			return
		}
		var m stockZProgramMachine
		if _, err := s.db.Get(stockZProgramKind, id, &m); err != nil {
			s.storeError(w, err)
			return
		}
		if m.BaseURL != i.StockZURL {
			s.fail(w, 409, "address", "The stock Z address changed. Request a new invitation.")
			return
		}
		p, err := s.stockZInviteProgram(*i)
		if err != nil {
			s.fail(w, 422, "stock_recipe", err.Error())
			return
		}
		if i.StockZReview == nil || p.ReviewHash != i.StockZReview.ReviewHash {
			s.fail(w, 409, "recipe_changed", "The program changed. Request a new invitation.")
			return
		}
		i.State = "responding"
		if err = s.saveInvite(*i); err != nil {
			s.storeError(w, err)
			return
		}
		if err = s.stageStockZProgram(r.Context(), id, p.program, true, nil); err != nil {
			i.State = "unavailable"
			if errors.Is(err, errStockZDeliveryUnknown) {
				i.State = "unconfirmed"
			}
			i.Message = err.Error()
			if persist := s.saveInvite(*i); persist != nil {
				s.storeError(w, persist)
				return
			}
			s.fail(w, 409, "stock_z_staging", i.Message)
			return
		}
		i.State = "accepted"
		i.Message = "Program Maker acknowledged the recipe. Review and start it on this Z’s dial."
	}
	if err := s.saveInvite(*i); err != nil {
		s.storeError(w, err)
		return
	}
	s.json(w, 200, i)
}

// The stock telemetry has no recipe identity or start event. The owner can
// explicitly offer a saved library revision from this module's Hub group.
func (s *Server) offerStockZRecipe(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Demo {
		http.NotFound(w, r)
		return
	}
	var in struct {
		TransferID    string `json:"transfer_id"`
		GroupID       string `json:"group_id"`
		GroupRevision int64  `json:"group_revision"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "machine")
	member := stockZMemberPrefix + id
	s.groupMu.Lock()
	defer s.groupMu.Unlock()
	var m stockZProgramMachine
	if _, err := s.db.Get(stockZProgramKind, id, &m); err != nil {
		s.storeError(w, err)
		return
	}
	g, err := s.currentGroup(member)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if g == nil || g.ID != in.GroupID || g.Revision != in.GroupRevision {
		s.fail(w, 409, "group_changed", "Reopen the transfer to review this Z’s current group")
		return
	}
	s.stockZMu.Lock()
	defer s.stockZMu.Unlock()
	var transfer stockZTransfer
	version, err := s.db.Get(stockZTransferKind, id, &transfer)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.storeError(w, err)
		return
	}
	if err != nil || transfer.ID == "" || transfer.ID != in.TransferID {
		s.fail(w, 409, "transfer_changed", "This Z’s last acknowledged transfer has changed. Review its working recipe.")
		return
	}
	if !transfer.Started.IsZero() && (transfer.GroupID != g.ID || transfer.GroupRevision != g.Revision) {
		s.fail(w, 409, "group_changed", "The group changed after this offer.")
		return
	}
	if transfer.Offered != nil {
		s.json(w, 200, map[string]any{"offered": *transfer.Offered})
		return
	}
	src := groupRecipe{LibraryID: transfer.RecipeID, Revision: transfer.Revision}
	saved, err := s.groupLibrary(src)
	if err != nil {
		s.fail(w, 409, "revision", err.Error())
		return
	}
	review, err := s.reviewStockZProgram(saved)
	if err != nil {
		s.fail(w, 422, "stock_recipe", err.Error())
		return
	}
	if review.ReviewHash != transfer.ReviewHash {
		s.fail(w, 409, "recipe_changed", "The loading quantities changed since this program was staged.")
		return
	}
	now := time.Now().UTC()
	if transfer.Started.IsZero() {
		transfer.GroupID, transfer.GroupRevision, transfer.Started = g.ID, g.Revision, now
		version, err = s.db.Put(stockZTransferKind, id, transfer, version)
		if err != nil {
			s.storeError(w, err)
			return
		}
	}
	if !transfer.Started.Add(inviteLifetime).After(now) {
		s.fail(w, 409, "expired", "This group offer expired.")
		return
	}
	key := groupEventID(g.ID, member, transfer.ID)
	if _, err = s.db.Put("group-brew-program", key, src, 0); err != nil && !errors.Is(err, store.ErrConflict) {
		s.storeError(w, err)
		return
	}
	source := brewInvite{GroupSessionID: key, GroupID: g.ID, GroupName: g.Name, SourceDeviceID: member, SourceName: m.Name, SourceEvent: key, Action: "brew", RecipeID: transfer.RecipeID, RecipeRevision: transfer.Revision, RecipeName: saved.Name, ExpiresAt: transfer.Started.Add(inviteLifetime)}
	if err = s.offerGroupMembers(*g, source, transfer.Started, now, "", ""); err != nil {
		s.storeError(w, err)
		return
	}
	offered := 0
	for _, target := range g.DeviceIDs {
		if target == member {
			continue
		}
		invite, err := s.memberInvite(target)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.storeError(w, err)
			return
		}
		if invite != nil && invite.GroupSessionID == key && activeInvite(invite.State) {
			offered++
		}
	}
	transfer.Offered = &offered
	if _, err := s.db.Put(stockZTransferKind, id, transfer, version); err != nil {
		s.storeError(w, err)
		return
	}
	s.json(w, 200, map[string]any{"offered": offered})
}
