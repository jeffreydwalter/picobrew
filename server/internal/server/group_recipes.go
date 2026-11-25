package server

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

// An on-device recipe ID is a slot name. Only a verified transfer receipt
// can identify its source library revision across different machine models.
type groupRecipe struct {
	Model     string               `json:"model"`
	Program   domain.MachineRecipe `json:"program"`
	LibraryID string               `json:"library_id,omitempty"`
	Revision  int64                `json:"revision,omitempty"`
}
type groupReceipt struct {
	DeviceID  string               `json:"device_id"`
	DeviceUID string               `json:"device_uid"`
	RecipeID  string               `json:"recipe_id"`
	Revision  int64                `json:"revision"`
	Native    domain.MachineRecipe `json:"native"`
	State     string               `json:"state"`
}

func (s *Server) identifyGroupRecipe(d domain.DeviceView, program domain.MachineRecipe) (groupRecipe, error) {
	src := groupRecipe{Model: d.Model, Program: program}
	refs := map[stockReferenceKey]bool{}
	receipts, err := listRecords[groupReceipt](s, "deployment")
	if err != nil {
		return src, err
	}
	for _, r := range receipts {
		if r.State == "verified" && r.DeviceID == d.ID && (r.DeviceUID == "" || r.DeviceUID == d.Status.DeviceUID) && reflect.DeepEqual(r.Native, program) {
			refs[stockReferenceKey{r.RecipeID, r.Revision}] = true
		}
	}
	bulk, err := listRecords[BulkDeployment](s, "batch-deployment")
	if err != nil {
		return src, err
	}
	for _, r := range bulk {
		if r.DeviceID != d.ID || r.DeviceUID != d.Status.DeviceUID {
			continue
		}
		for _, item := range r.Items {
			if item.Status == "verified" && item.Native != nil && reflect.DeepEqual(*item.Native, program) {
				refs[stockReferenceKey{item.RecipeID, item.SourceRevision}] = true
			}
		}
	}
	if len(refs) == 1 {
		for ref := range refs {
			src.LibraryID, src.Revision = ref.ID, ref.Revision
		}
	}
	return src, nil
}

type stockReferenceKey struct {
	ID       string
	Revision int64
}

func (s *Server) groupLibrary(src groupRecipe) (domain.Recipe, error) {
	var saved domain.Recipe
	if src.LibraryID == "" || src.Revision < 1 {
		return saved, errors.New("Send the original library recipe to the source brewer before inviting a different model")
	}
	_, err := s.db.Get("recipe-revision", src.LibraryID+":"+strconv.FormatInt(src.Revision, 10), &saved)
	if errors.Is(err, store.ErrNotFound) {
		_, err = s.db.Get("recipe", src.LibraryID, &saved)
	}
	if err != nil || saved.ID != src.LibraryID || saved.Revision != src.Revision {
		return saved, errors.New("The matching library recipe revision is unavailable")
	}
	return saved, nil
}
func sameGroupProgram(a, b domain.MachineRecipe) bool { a.ID = b.ID; return reflect.DeepEqual(a, b) }
func (s *Server) groupTargetRecipe(ctx context.Context, d *runtimeDevice, v domain.DeviceView, wire device.GroupBrewer, src groupRecipe) (string, uint32, error) {
	desired := src.Program
	if src.Model != v.Model {
		saved, err := s.groupLibrary(src)
		if err != nil {
			return "", 0, err
		}
		profile, ok := s.selectProfile(v.Model)
		if !ok {
			return "", 0, errors.New("The target machine has no equipment profile")
		}
		plan, err := recipe.Translate(saved, profile, 0)
		if err != nil || !plan.CanDeploy || plan.MachineRecipe == nil {
			return "", 0, errors.New("This recipe has no valid program for the target brewer")
		}
		desired = *plan.MachineRecipe
	}
	if issue := recipeCapabilityIssue(*v.Capabilities, desired); issue != "" {
		return "", 0, errors.New(issue)
	}
	// First try its exact ID. Reading one target slot avoids downloading a whole
	// library in the common case. Other on-device IDs may hold identical programs.
	read, rev, err := wire.GroupRecipe(ctx, desired.ID)
	if err == nil && rev != 0 && sameGroupProgram(read, desired) {
		return read.ID, rev, nil
	}
	var missing *device.Error
	if err != nil && !(errors.As(err, &missing) && missing.StatusCode == 404) {
		return "", 0, err
	}
	inventory, err := d.adapter.Recipes(ctx)
	if err != nil {
		return "", 0, err
	}
	for _, p := range inventory {
		if sameGroupProgram(p, desired) {
			read, rev, err = wire.GroupRecipe(ctx, p.ID)
			if err != nil {
				return "", 0, err
			}
			if rev != 0 && sameGroupProgram(read, desired) {
				return p.ID, rev, nil
			}
			return "", 0, errors.New("The target program changed during verification")
		}
	}
	// Sending a grouped invitation must not overwrite a brewer's existing recipe.
	if src.LibraryID == "" || !v.Capabilities.RecipeCreateOnly || len(inventory) >= v.Capabilities.RecipeCapacity || slices.ContainsFunc(inventory, func(p domain.MachineRecipe) bool { return p.ID == desired.ID }) {
		return "", 0, errors.New("Send a matching recipe to an available slot before joining")
	}
	if err = saveReviewedRecipe(ctx, d.adapter, desired, nil); err != nil {
		return "", 0, err
	}
	read, rev, err = wire.GroupRecipe(ctx, desired.ID)
	if err != nil || rev == 0 || !reflect.DeepEqual(read, desired) {
		return "", 0, errors.New("The target recipe write could not be verified")
	}
	_, err = s.db.Put("deployment", randomID(), groupReceipt{v.ID, v.Status.DeviceUID, src.LibraryID, src.Revision, read, "verified"}, 0)
	return read.ID, rev, err
}
