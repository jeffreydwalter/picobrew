package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

const stockZProgramKind = "stock-z-program-machine"

type stockZProgramMachine struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

// A stock Z has no local identity endpoint. Keep this registration separate
// from the authenticated standalone fleet. Only an owner may select its
// numeric LAN address and explicitly consent to a transfer to that address.
func (s *Server) stockZPrograms(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		machines, err := listRecords[stockZProgramMachine](s, stockZProgramKind)
		if err != nil {
			s.storeError(w, err)
			return
		}
		s.json(w, http.StatusOK, map[string]any{"machines": machines})
		return
	}
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	if r.Method == http.MethodDelete {
		id := chi.URLParam(r, "machine")
		// Group accepts acquire groupMu then stockZMu. Remove membership
		// before taking stockZMu to keep that ordering and retire invitations.
		if err := s.removeGroupMember(stockZMemberPrefix + id); err != nil {
			s.storeError(w, err)
			return
		}
		s.stockZMu.Lock()
		defer s.stockZMu.Unlock()
		if err := s.db.Delete(stockZTransferKind, id); err != nil {
			s.storeError(w, err)
			return
		}
		if err := s.db.Delete(stockZProgramKind, id); err != nil {
			s.storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var in struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) > 80 || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 {
		s.fail(w, 422, "machine", "Use a machine name of up to 80 characters.")
		return
	}
	if in.Name == "" {
		in.Name = "Pico Z"
	}
	z, err := s.openStockZ(in.BaseURL)
	if err != nil {
		s.fail(w, 422, "stock_z_address", err.Error())
		return
	}
	if _, err := z.readState(r.Context()); err != nil {
		s.deviceError(w, err)
		return
	}
	address := "http://" + z.ip.String()
	for _, dev := range s.fleet() {
		if discoveryAddressKey(dev.BaseURL) == z.ip.String() {
			s.fail(w, 409, "address_registered", "This address already has a machine registration.")
			return
		}
	}
	registered, err := listRecords[stockZProgramMachine](s, stockZProgramKind)
	if err != nil {
		s.storeError(w, err)
		return
	}
	for _, machine := range registered {
		if machine.BaseURL == address {
			s.json(w, http.StatusOK, machine)
			return
		}
	}
	machine := stockZProgramMachine{ID: randomID(), Name: in.Name, BaseURL: address}
	if _, err := s.db.Put(stockZProgramKind, machine.ID, machine, 0); err != nil {
		s.storeError(w, err)
		return
	}
	s.json(w, http.StatusCreated, machine)
}

func (s *Server) registeredStockZ(id string) (stockZEndpoint, error) {
	var machine stockZProgramMachine
	if _, err := s.db.Get(stockZProgramKind, id, &machine); err != nil {
		return stockZEndpoint{}, err
	}
	return s.openStockZ(machine.BaseURL)
}

func (s *Server) stockZProgramState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	z, err := s.registeredStockZ(chi.URLParam(r, "machine"))
	if err != nil {
		s.stockZError(w, err)
		return
	}
	state, err := z.readState(r.Context())
	if err != nil {
		s.deviceError(w, err)
		return
	}
	s.json(w, http.StatusOK, state)
}

type stockZTemperatureLimit struct {
	Step       string  `json:"step"`
	RequestedC float64 `json:"requested_c"`
	MaximumC   float64 `json:"maximum_c"`
}

type stockZProgramReview struct {
	ProgramName       string                   `json:"program_name"`
	StepCount         int                      `json:"step_count"`
	ProgramHash       string                   `json:"program_hash"`
	BatchLiters       float64                  `json:"batch_liters,omitempty"`
	Ingredients       []domain.Ingredient      `json:"ingredients,omitempty"`
	HopSlotsByMinute  map[int]int              `json:"hop_slots_by_minute,omitempty"`
	TemperatureLimits []stockZTemperatureLimit `json:"temperature_limits,omitempty"`
	ReviewHash        string                   `json:"review_hash,omitempty"`
	program           []byte
}

func (s *Server) reviewStockZProgram(saved domain.Recipe) (stockZProgramReview, error) {
	profile, ok := s.selectProfile("z")
	if !ok {
		return stockZProgramReview{}, errors.New("The Z equipment profile is unavailable.")
	}
	plan, err := recipe.Translate(saved, profile, 0)
	if err != nil {
		return stockZProgramReview{}, err
	}
	wire, err := recipe.EncodeStockZPlan(plan)
	if err != nil {
		return stockZProgramReview{}, err
	}
	var program domain.ZProgram
	if err := json.Unmarshal(wire, &program); err != nil {
		return stockZProgramReview{}, err
	}
	review := stockZProgramReview{ProgramName: program.Name, StepCount: len(program.Steps), ProgramHash: fmt.Sprintf("%x", sha256.Sum256(wire)), program: wire}
	// The stock local compiler caps the kettle route at 94.45 °C before
	// applying its own boiling-point correction. Ingredient translation uses
	// whole °F, so its 202 °F kettle target already sits at that limit.
	for _, step := range program.Steps {
		if step.Type != 1 || *step.Location != 1 {
			continue
		}
		var target float64
		if err := json.Unmarshal(step.Temp, &target); err != nil {
			return stockZProgramReview{}, err
		}
		if target >= (202.0-32)/1.8 {
			review.TemperatureLimits = append(review.TemperatureLimits,
				stockZTemperatureLimit{Step: step.Name, RequestedC: target, MaximumC: 94.45})
		}
	}
	if !recipe.IsNative(saved) && len(plan.Recipe.Ingredients) != 0 {
		review.BatchLiters, review.Ingredients, review.HopSlotsByMinute = plan.Recipe.BatchLiters, plan.Recipe.Ingredients, plan.HopSlotsByMinute
	}
	details, err := json.Marshal(review)
	if err != nil {
		return stockZProgramReview{}, err
	}
	review.ReviewHash = fmt.Sprintf("%x", sha256.Sum256(details))
	return review, nil
}

func (s *Server) compileStockZProgram(saved domain.Recipe) ([]byte, error) {
	review, err := s.reviewStockZProgram(saved)
	return review.program, err
}

// program comes only from reviewStockZProgram, never from a browser body.
// The Program Maker receiver overwrites a single pending program, and its
// runtime has no atomic "only if idle" operation. A human verifies the
// physical machine as well as the preflight immediately before the POST.
func (s *Server) stageStockZProgram(ctx context.Context, id string, program []byte, confirmed bool, transfer *stockZTransfer) error {
	if !confirmed {
		return &device.Error{StatusCode: 409, Code: "confirmation", Message: "Confirm the intended Z is idle and its pending program may be replaced."}
	}
	s.stockZMu.Lock()
	defer s.stockZMu.Unlock()
	z, err := s.registeredStockZ(id)
	if err != nil {
		return err
	}
	state, err := z.readState(ctx)
	if err != nil {
		return err
	}
	if state.Activity != "outputs_quiet" {
		return &device.Error{StatusCode: 409, Code: "activity", Message: "The Z reports activity, a pause, or an uncertain state. Check it at the physical dial before transferring."}
	}
	// Once a write is attempted, the previous receipt no longer identifies the
	// working program, including when the brewer loses the acknowledgement.
	if err := s.db.Delete(stockZTransferKind, id); err != nil {
		return err
	}
	if err := z.post(ctx, program); err != nil {
		return err
	}
	if transfer != nil {
		if _, err := s.db.Put(stockZTransferKind, id, transfer, 0); err != nil {
			return fmt.Errorf("%w: the Z acknowledged the recipe, but the Hub could not record the transfer; check the dial before sending again", errStockZDeliveryUnknown)
		}
	}
	return nil
}

func (s *Server) stockZError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrConflict):
		s.storeError(w, err)
	case errors.Is(err, errStockZDeliveryUnknown):
		s.fail(w, http.StatusBadGateway, "device_outcome_unknown", err.Error())
	default:
		s.deviceError(w, err)
	}
}

func (s *Server) stockZProgramRecipe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RecipeID       string `json:"recipe_id"`
		Revision       int64  `json:"revision"`
		ConfirmReplace bool   `json:"confirm_replace"`
		ProgramHash    string `json:"program_hash"`
		ReviewHash     string `json:"review_hash"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "machine")
	var machine stockZProgramMachine
	if _, err := s.db.Get(stockZProgramKind, id, &machine); err != nil {
		s.storeError(w, err)
		return
	}
	if strings.TrimSpace(in.RecipeID) == "" || in.Revision < 1 {
		s.fail(w, 422, "recipe", "Select a saved recipe revision.")
		return
	}
	var saved domain.Recipe
	if _, err := s.db.Get("recipe", in.RecipeID, &saved); err != nil {
		s.storeError(w, err)
		return
	}
	if saved.Revision != in.Revision {
		s.fail(w, 409, "recipe_changed", "The recipe changed. Preview its current revision before sending.")
		return
	}
	review, err := s.reviewStockZProgram(saved)
	if err != nil {
		s.fail(w, 422, "unsupported_program", err.Error())
		return
	}
	result := "preview"
	var transferID string
	if in.ConfirmReplace {
		if in.ProgramHash != review.ProgramHash || in.ReviewHash != review.ReviewHash {
			s.fail(w, http.StatusConflict, "program_changed", "Preview the current program and loading quantities before replacing the recipe on this Z.")
			return
		}
		transfer := stockZTransfer{ID: randomID(), RecipeID: saved.ID, Revision: saved.Revision, ReviewHash: review.ReviewHash}
		if err := s.stageStockZProgram(r.Context(), id, review.program, true, &transfer); err != nil {
			s.stockZError(w, err)
			return
		}
		result = "acknowledged"
		transferID = transfer.ID
	}
	s.json(w, http.StatusOK, struct {
		stockZProgramReview
		Status     string `json:"status"`
		TransferID string `json:"transfer_id,omitempty"`
	}{review, result, transferID})
}
