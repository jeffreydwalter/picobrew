package server

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/catalog"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
)

// ID is the positive signed integer consumed by atoi() in the stock recipe menu.
// RFID is a physical tag explicitly associated by the owner, independently of ID.
type stockPicoItem struct {
	Ingredients      []domain.Ingredient  `json:"ingredients,omitempty"`
	BatchLiters      float64              `json:"batch_liters,omitempty"`
	HopSlotsByMinute map[int]int          `json:"hop_slots_by_minute,omitempty"`
	ID               string               `json:"id"`
	RecipeID         string               `json:"recipe_id"`
	Revision         int64                `json:"revision"`
	Program          domain.MachineRecipe `json:"program"`
	RFID             string               `json:"rfid,omitempty"`
	ABV              *float64             `json:"abv,omitempty"`
	IBU              *float64             `json:"ibu,omitempty"`
}

func stockPicoMenuName(item stockPicoItem) string {
	name := item.Program.Name
	if len(name) > 21 { // The firmware copies at most 21 printable ASCII bytes into each menu label.
		return name[:21]
	}
	return name
}

func compileStockPicoRecipe(saved domain.Recipe, profile domain.Profile) (stockPicoItem, error) {
	var item stockPicoItem
	switch profile.ID {
	case "pico-s", "pico-pro", "pico-c":
	default:
		return item, errors.New("choose a Pico S, Pro or C profile")
	}
	if saved.ID == "" || saved.Kind == "maintenance" {
		return item, errors.New("choose a saved brewing recipe for the stock Pico menu")
	}
	plan, err := recipe.Translate(saved, profile, 0)
	if err != nil {
		return item, err
	}
	if !plan.CanDeploy || plan.MachineRecipe == nil {
		return item, fmt.Errorf("stock Pico recipe unavailable: %s", strings.Join(plan.DeploymentBlockers, ", "))
	}
	item = stockPicoItem{
		RecipeID: saved.ID, Revision: saved.Revision, Program: *plan.MachineRecipe,
		Ingredients: plan.Recipe.Ingredients, BatchLiters: plan.Recipe.BatchLiters, HopSlotsByMinute: plan.HopSlotsByMinute,
	}
	// Keep the menu ID stable across edits. The owner menu rejects collisions.
	hash := sha256.Sum256([]byte(saved.ID))
	id := uint32(1_000_000_000) + binary.BigEndian.Uint32(hash[:4])%1_000_000_000
	if saved.NativeProgram != nil && saved.NativeProgram.ZPak != nil {
		// Translation first checks that the Pico can execute the original routes,
		// including rejecting any program requiring an operator pause.
		kit := saved.NativeProgram.ZPak
		if kit.RecipeID > math.MaxInt32 {
			return stockPicoItem{}, errors.New("original recipe ID exceeds the stock menu integer field")
		}
		item.Program.ZPak = kit
		id = kit.RecipeID
	}
	item.ID = strconv.FormatUint(uint64(id), 10)
	item.ABV, item.IBU = stockPicoSourceStats(saved)
	// Check the longest representation as well as the default. Timings interpolate
	// between the authored endpoints and their decimal width can change on the dial.
	for _, abv := range stockPicoCheckLevels(item.Program, true) {
		for _, ibu := range stockPicoCheckLevels(item.Program, false) {
			if _, err := item.recipeRecord(abv, ibu); err != nil {
				return stockPicoItem{}, err
			}
		}
	}
	return item, nil
}

func stockPicoAxis(p domain.MachineRecipe, strength bool) int {
	kit := p.ZPak
	if kit == nil || len(kit.StepOptions) != len(p.Steps) {
		return -1
	}
	base := kit.IBU
	if strength {
		base = kit.ABV
	}
	if base < 1 || base > 9 {
		return -1
	}
	for i, step := range p.Steps {
		opt := kit.StepOptions[i]
		if opt.Action == 0 && opt.MinTime != nil && opt.MaxTime != nil && (strength && step.Location == 2 || !strength && step.Location >= 3 && step.Location <= 6) {
			return base
		}
	}
	return -1
}

func stockPicoCheckLevels(p domain.MachineRecipe, strength bool) []int {
	if stockPicoAxis(p, strength) < 0 {
		return []int{-1}
	}
	return []int{-1, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
}

// Match the recovered kit compiler's float32 multiply followed by truncation,
// including original kits with descending min/max timing pairs.
func stockPicoMinutes(original int, opt domain.ZPakStep, base, requested int) int {
	if base < 1 || requested == -1 || requested == base || opt.MinTime == nil || opt.MaxTime == nil {
		return original
	}
	if requested < base {
		ratio := float32(requested) / float32(base)
		return *opt.MinTime + int(ratio*float32(original-*opt.MinTime))
	}
	ratio := float32(requested-base) / float32(10-base)
	return original + int(ratio*float32(*opt.MaxTime-original))
}

func (item stockPicoItem) recipeRecord(abv, ibu int) (string, error) {
	p := item.Program
	plain := p
	plain.ZPak = nil
	if _, err := recipe.NativeJSON(plain); err != nil {
		return "", err
	}
	if p.ZProgram != nil || p.ZCloud != nil || strings.ContainsAny(p.Name, "#|/,") {
		return "", errors.New("the stock Pico needs a brewing program and a name without #, |, / or comma")
	}
	if p.ZPak != nil {
		if err := p.ZPak.Validate(p.Steps); err != nil {
			return "", err
		}
		if err := p.ZPak.ValidatePicoTransfer(); err != nil {
			return "", err
		}
	}
	baseABV, baseIBU := stockPicoAxis(p, true), stockPicoAxis(p, false)
	if abv < -1 || abv > 10 || ibu < -1 || ibu > 10 || abv >= 0 && baseABV < 0 || ibu >= 0 && baseIBU < 0 {
		return "", errors.New("this recipe cannot apply that strength or bitterness setting")
	}
	var out strings.Builder
	// Keep source beer statistics distinct from dial indices. The stock display
	// requires numeric fields and has no missing-data label. For a new dose,
	// the original beer's statistics no longer describe the requested brew.
	var displayABV float64
	var displayIBU int
	if (abv < 0 || abv == baseABV) && (ibu < 0 || ibu == baseIBU) {
		if item.ABV != nil {
			displayABV = *item.ABV
		}
		if item.IBU != nil {
			displayIBU = int(math.Round(*item.IBU))
		}
	}
	fmt.Fprintf(&out, "%s/%d,%d,%s,%d,", p.Name, baseABV, baseIBU, strconv.FormatFloat(displayABV, 'f', -1, 64), displayIBU)
	for i, step := range p.Steps {
		if strings.ContainsAny(step.Name, "#|,") {
			return "", errors.New("stock Pico step names cannot contain #, | or comma")
		}
		minutes := step.Minutes
		if p.ZPak != nil {
			switch step.Location {
			case 2:
				minutes = stockPicoMinutes(minutes, p.ZPak.StepOptions[i], baseABV, abv)
			case 3, 4, 5, 6:
				minutes = stockPicoMinutes(minutes, p.ZPak.StepOptions[i], baseIBU, ibu)
			}
		}
		fmt.Fprintf(&out, "%d,%d,%d,%d,%s,", step.TargetF, minutes, step.DrainMinutes, step.Location, step.Name)
	}
	if out.Len() > 500 {
		return "", errors.New("stock Pico recipe exceeds the 500-byte response field")
	}
	return out.String(), nil
}

// Catalog statistics describe the published formulation. Edits to ingredients,
// fermentation, or the brewing schedule invalidate those values.
func stockPicoSourceStats(saved domain.Recipe) (*float64, *float64) {
	if saved.Provenance == nil || saved.Provenance.CatalogID == "" {
		return nil, nil
	}
	archive, err := catalog.Open()
	if err != nil {
		return nil, nil
	}
	entry, err := archive.Detail(saved.Provenance.CatalogID)
	if err != nil || entry.Recipe == nil || entry.Provenance.SHA256 != saved.Provenance.SHA256 || (entry.ABV == nil && entry.IBU == nil) {
		return nil, nil
	}
	r := entry.Recipe
	if r.Kind != saved.Kind || r.Type != saved.Type || r.SourceModel != saved.SourceModel ||
		r.BatchLiters != saved.BatchLiters || r.Efficiency != saved.Efficiency || r.BoilLiters != saved.BoilLiters ||
		r.BoilMinutes != saved.BoilMinutes || r.SourceYeast != saved.SourceYeast ||
		!slices.Equal(r.Ingredients, saved.Ingredients) || !slices.Equal(r.Steps, saved.Steps) ||
		!slices.Equal(r.Fermentation, saved.Fermentation) || !reflect.DeepEqual(r.Water, saved.Water) ||
		!reflect.DeepEqual(r.NativeProgram, saved.NativeProgram) {
		return nil, nil
	}
	return entry.ABV, entry.IBU
}
