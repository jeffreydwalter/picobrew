package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
)

// Model versions are derived from one saved revision and the current equipment
// calibration. Persisting separate library copies would let them drift apart.
type recipeView struct {
	domain.Recipe
	MachineVersions []domain.Adapted `json:"machine_versions,omitempty"`
}

func recipeWithVersions(v domain.Recipe, profiles []domain.Profile) recipeView {
	v.SourceYeast = recipe.SourceYeast(v)
	view := recipeView{Recipe: v}
	if recipe.IsNative(v) || len(v.Ingredients) == 0 {
		return view
	}
	for _, p := range profiles {
		plan, err := recipe.Translate(v, p, 0)
		if err != nil {
			plan = domain.Adapted{Profile: p, SourceRevision: v.Revision, DeploymentBlockers: []string{err.Error()}}
		}
		plan.ProfileFingerprint = profileFingerprint(p)
		view.MachineVersions = append(view.MachineVersions, plan)
	}
	return view
}
func (s *Server) listRecipes(w http.ResponseWriter, r *http.Request) {
	rows, e := s.db.List("recipe")
	if e != nil {
		s.internal(w, e)
		return
	}
	s.json(w, 200, rows)
}
func (s *Server) getRecipe(w http.ResponseWriter, r *http.Request) {
	var v domain.Recipe
	if _, e := s.db.Get("recipe", chi.URLParam(r, "id"), &v); e != nil {
		s.storeError(w, e)
		return
	}
	profiles, err := s.calibratedProfiles()
	if err != nil {
		s.internal(w, err)
		return
	}
	s.json(w, 200, recipeWithVersions(v, profiles))
}
func (s *Server) validateLibraryRecipe(v domain.Recipe) error {
	if err := recipe.Validate(v); err != nil {
		return err
	}
	if !recipe.IsNative(v) && v.SourceModel != "" {
		profiles, err := s.calibratedProfiles()
		if err != nil {
			return err
		}
		for _, profile := range profiles {
			if profileMatchesModel(profile.ID, v.SourceModel) {
				return recipe.ValidateCapacity(v, profile)
			}
		}
	}
	return nil
}

// Explicit authoring targets must compile before a new library revision is
// stored. Imports and planning-only archives can retain unsupported source data.
func validateAuthoredProgram(v domain.Recipe, p domain.Profile) error {
	if recipe.IsNative(v) && p.ID == "zymatic" {
		program, err := recipe.StockZymaticProgram(v, p)
		if err != nil {
			return err
		}
		for i, step := range program.Steps {
			if step.Location == 6 && (step.TargetF != 0 || step.Minutes != 0 || step.DrainMinutes != 0) {
				return fmt.Errorf("stock Zymatic pause step %d waits for the operator and cannot set heating, hold or drain", i+1)
			}
			if step.Name != v.NativeProgram.Steps[i].Name {
				return fmt.Errorf("shorten or simplify the name of step %d to fit the stock Zymatic menu", i+1)
			}
		}
		if program.Name != v.Name {
			return errors.New("shorten or simplify the program name to fit the stock Zymatic menu")
		}
		return nil
	}
	plan, err := recipe.Translate(v, p, 0)
	if err != nil {
		return err
	}
	if len(plan.DeploymentBlockers) > 0 {
		return errors.New(strings.Join(plan.DeploymentBlockers, "; "))
	}
	if plan.MachineRecipe == nil {
		return errors.New("this brewer cannot run the program")
	}
	return nil
}

func (s *Server) saveRecipe(w http.ResponseWriter, r *http.Request) {
	var input struct {
		domain.Recipe
		// Clients can round-trip a GET response. Only the canonical recipe is writable.
		MachineVersions json.RawMessage `json:"machine_versions"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	v := input.Recipe
	profiles, err := s.calibratedProfiles()
	if err != nil {
		s.internal(w, err)
		return
	}
	if e := s.validateLibraryRecipe(v); e != nil {
		s.fail(w, 400, "invalid_recipe", e.Error())
		return
	}
	if target := r.URL.Query().Get("validate_for_model"); target != "" {
		p, ok := s.selectProfile(target)
		if !ok {
			s.fail(w, 422, "profile", "Choose a supported brewing model")
			return
		}
		if err := validateAuthoredProgram(v, p); err != nil {
			s.fail(w, 422, "unsupported_program", err.Error())
			return
		}
	}
	s.recipeMu.Lock()
	defer s.recipeMu.Unlock()
	id := chi.URLParam(r, "id")
	expected := int64(0)
	code := 201
	now := time.Now().UTC()
	if id != "" {
		var old domain.Recipe
		version, e := s.db.Get("recipe", id, &old)
		if e != nil {
			s.storeError(w, e)
			return
		}
		if v.Revision != old.Revision {
			s.fail(w, 409, "revision_conflict", "The recipe was changed by another editor. Reload it before saving.")
			return
		}
		expected = version
		v.CreatedAt = old.CreatedAt
		code = 200
	} else {
		id = randomID()
		v.CreatedAt = now
	}
	v.ID = id
	v.Revision = expected + 1
	v.UpdatedAt = now
	if _, e := s.db.Put("recipe", id, v, expected); e != nil {
		s.storeError(w, e)
		return
	}
	if _, e := s.db.Put("recipe-revision", id+":"+strconv.FormatInt(v.Revision, 10), v, 0); e != nil {
		s.cfg.Logger.Error("Recipe revision archive failed", "id", id, "error", e)
	}
	s.json(w, code, recipeWithVersions(v, profiles))
}
func (s *Server) deleteRecipe(w http.ResponseWriter, r *http.Request) {
	s.recipeMu.Lock()
	defer s.recipeMu.Unlock()
	if e := s.db.Delete("recipe", chi.URLParam(r, "id")); e != nil {
		s.storeError(w, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) adaptRecipe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProfileID   string  `json:"profile_id"`
		BatchLiters float64 `json:"batch_liters"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	var v domain.Recipe
	if _, e := s.db.Get("recipe", chi.URLParam(r, "id"), &v); e != nil {
		s.storeError(w, e)
		return
	}
	p, ok := s.selectProfile(in.ProfileID)
	if !ok {
		s.fail(w, 400, "profile", "Unknown machine profile")
		return
	}
	adapted, e := recipe.Translate(v, p, in.BatchLiters)
	if e != nil {
		s.fail(w, 422, "adaptation", e.Error())
		return
	}
	adapted.ProfileFingerprint = profileFingerprint(p)
	s.json(w, 200, adapted)
}
func (s *Server) deploy(w http.ResponseWriter, r *http.Request) {
	d, ok := s.runtime(w, r)
	if !ok {
		return
	}
	var in struct {
		RecipeID           string                `json:"recipe_id"`
		ReplaceExpected    *domain.MachineRecipe `json:"replace_expected,omitempty"`
		ProfileID          string                `json:"profile_id"`
		BatchLiters        float64               `json:"batch_liters"`
		SourceRevision     int64                 `json:"source_revision"`
		ProfileFingerprint string                `json:"profile_fingerprint"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	var v domain.Recipe
	if _, e := s.db.Get("recipe", in.RecipeID, &v); e != nil {
		s.storeError(w, e)
		return
	}
	if in.SourceRevision != v.Revision {
		s.fail(w, 409, "revision_conflict", "Review adaptation again for the current recipe revision")
		return
	}
	d.commandMu.Lock()
	defer d.commandMu.Unlock()
	if !s.currentDevice(d) {
		s.fail(w, 409, "connection_changed", "The machine connection changed; reload before continuing")
		return
	}
	d.mu.RLock()
	view := d.view
	d.mu.RUnlock()
	if in.ProfileID == "" {
		in.ProfileID = view.Model
	}
	if !profileMatchesModel(in.ProfileID, view.Model) {
		s.fail(w, 422, "profile_mismatch", "Choose the profile that matches the target machine")
		return
	}
	p, ok := s.selectProfile(in.ProfileID)
	if !ok {
		s.fail(w, 400, "profile", "Unknown machine profile")
		return
	}
	if in.ProfileFingerprint != "" && in.ProfileFingerprint != profileFingerprint(p) {
		s.fail(w, 409, "profile_changed", "Equipment calibration changed; preview the adaptation again before sending it")
		return
	}
	a, e := recipe.Translate(v, p, in.BatchLiters)
	if e != nil {
		s.fail(w, 422, "adaptation", e.Error())
		return
	}
	if !a.CanDeploy || a.MachineRecipe == nil {
		message := "This recipe and profile support planning only; no machine program is available"
		if len(a.DeploymentBlockers) > 0 {
			message = strings.Join(a.DeploymentBlockers, "; ")
		}
		s.fail(w, 422, "unsupported_deployment", message)
		return
	}
	status, e := s.verifiedStatus(r.Context(), d)
	if e != nil {
		s.deviceError(w, e)
		return
	}
	if status.Busy || status.Stopped || !status.Ready {
		s.fail(w, 409, "machine_not_idle", "The machine must be ready at its main menu before recipe deployment")
		return
	}
	native := *a.MachineRecipe
	caps, e := d.adapter.Discover(r.Context())
	if e != nil {
		s.deviceError(w, e)
		return
	}
	if issue := recipeCapabilityIssue(caps, native); issue != "" {
		s.fail(w, 422, "unsupported_recipe", issue)
		return
	}
	existing, e := d.adapter.Recipes(r.Context())
	if e != nil {
		s.deviceError(w, e)
		return
	}
	deployment := map[string]any{"device_id": view.ID, "device_uid": status.DeviceUID, "recipe_id": v.ID, "revision": v.Revision, "native": native, "at": time.Now().UTC(), "state": "prepared"}
	var found bool
	for _, previous := range existing {
		if previous.ID != native.ID {
			continue
		}
		found = true
		if in.ReplaceExpected == nil {
			s.fail(w, 409, "recipe_exists", "This recipe ID is already on the machine. Preview again to review its replacement.")
			return
		}
		if !reflect.DeepEqual(previous, *in.ReplaceExpected) {
			s.fail(w, 409, "recipe_changed", "The installed recipe changed since the preview. Preview again before replacing it.")
			return
		}
		deployment["previous"] = previous
		break
	}
	if !found && in.ReplaceExpected != nil {
		s.fail(w, 409, "recipe_changed", "The installed recipe was removed since the preview. Preview again.")
		return
	}
	if !found && len(existing) >= caps.RecipeCapacity {
		s.fail(w, 409, "recipe_capacity", "No recipe slots remain on the machine")
		return
	}
	recordID := randomID()
	if _, e = s.db.Put("deployment", recordID, deployment, 0); e != nil {
		s.internal(w, e)
		return
	}
	if e = saveReviewedRecipe(r.Context(), d.adapter, native, in.ReplaceExpected); e != nil {
		deployment["state"] = "unconfirmed"
		var failure *device.Error
		if errors.As(e, &failure) && !failure.UnknownOutcome {
			deployment["state"] = "not_sent"
		}
		_, _ = s.db.Put("deployment", recordID, deployment, -1)
		s.deviceError(w, e)
		return
	}
	deployment["state"] = "verified"
	if _, e = s.db.Put("deployment", recordID, deployment, -1); e != nil {
		s.fail(w, 500, "receipt_error", "The recipe was deployed, but the deployment receipt could not be saved. Inspect the machine before retrying.")
		return
	}
	s.json(w, 200, map[string]any{"deployed": true, "started": false, "device_id": view.ID, "recipe": native, "warnings": a.Warnings, "deployment_id": recordID})
}
func (s *Server) importRecipes(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Format string `json:"format"`
		Data   string `json:"data"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	items, e := recipe.Import([]byte(in.Data), in.Format)
	if e != nil {
		s.fail(w, 400, "invalid_import", e.Error())
		return
	}
	if len(items) == 0 || len(items) > 100 {
		s.fail(w, 400, "import_count", "Import between 1 and 100 recipes at a time")
		return
	}
	for _, v := range items {
		if e = s.validateLibraryRecipe(v); e != nil {
			s.fail(w, 400, "invalid_recipe", e.Error())
			return
		}
	}
	profiles, err := s.calibratedProfiles()
	if err != nil {
		s.internal(w, err)
		return
	}
	views := make([]recipeView, 0, len(items))
	now := time.Now().UTC()
	for i := range items {
		items[i].ID = randomID()
		items[i].Revision = 1
		items[i].CreatedAt = now
		items[i].UpdatedAt = now
		if _, e = s.db.Put("recipe", items[i].ID, items[i], 0); e != nil {
			s.internal(w, e)
			return
		}
		_, _ = s.db.Put("recipe-revision", items[i].ID+":1", items[i], 0)
		views = append(views, recipeWithVersions(items[i], profiles))
	}
	s.json(w, 201, views)
}
func (s *Server) exportRecipe(w http.ResponseWriter, r *http.Request) {
	var v domain.Recipe
	if _, e := s.db.Get("recipe", chi.URLParam(r, "id"), &v); e != nil {
		s.storeError(w, e)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	var data []byte
	var e error
	if format == "z-program" {
		p, ok := s.selectProfile("z")
		if !ok {
			s.fail(w, 400, "profile", "Z equipment profile unavailable")
			return
		}
		data, e = recipe.ExportZProgram(v, p)
	} else {
		data, e = recipe.Export(v, format)
	}
	if e != nil {
		s.fail(w, 400, "format", e.Error())
		return
	}
	extension := "json"
	ct := "application/json"
	if format == "beerxml" {
		extension = "xml"
		ct = "application/xml"
	}
	w.Header().Set("Content-Disposition", `attachment; filename="recipe-`+v.ID+`.`+extension+`"`)
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(data)
}
func (s *Server) recipeRevisions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rows, e := s.db.List("recipe-revision")
	if e != nil {
		s.internal(w, e)
		return
	}
	out := []domain.Recipe{}
	for _, raw := range rows {
		var v domain.Recipe
		if json.Unmarshal(raw, &v) == nil && v.ID == id {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision > out[j].Revision })
	s.json(w, 200, out)
}
func (s *Server) shoppingList(w http.ResponseWriter, r *http.Request) {
	ids := strings.Split(r.URL.Query().Get("recipes"), ",")
	type shoppingItem struct {
		domain.Ingredient
		AmountUnspecified bool `json:"amount_unspecified,omitempty"`
	}
	type itemKey struct {
		name, kind, unit string
		unspecified      bool
	}
	out := map[itemKey]shoppingItem{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		var v domain.Recipe
		if _, e := s.db.Get("recipe", id, &v); e != nil {
			s.storeError(w, e)
			return
		}
		measuredYeast := false
		for _, x := range v.Ingredients {
			measuredYeast = measuredYeast || x.Kind == "yeast"
			k := itemKey{strings.ToLower(x.Name), x.Kind, x.Unit, false}
			old := out[k]
			if old.Name == "" {
				old.Ingredient = x
				old.Amount = 0
			}
			old.Amount += x.Amount
			out[k] = old
		}
		if yeast := recipe.SourceYeast(v); !measuredYeast && yeast != "" {
			out[itemKey{strings.ToLower(yeast), "yeast", "", true}] = shoppingItem{Ingredient: domain.Ingredient{Name: yeast, Kind: "yeast", Use: "fermentation"}, AmountUnspecified: true}
		}
	}
	list := make([]shoppingItem, 0, len(out))
	for _, v := range out {
		list = append(list, v)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	s.json(w, 200, list)
}
