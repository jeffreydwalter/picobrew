// Package recipe validates canonical recipes and produces explicit, reviewable
// device plans. It never infers a valve/chamber location from a step's name.
package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

const MaxImportBytes = 1 << 20

var nativeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,20}$`)
var stepKinds = map[string]bool{"mash": true, "mash-out": true, "boil": true, "hop": true, "heat": true, "hold": true, "rest": true, "unheated": true, "cool": true, "fermentation": true}

// Profiles returns independent planning defaults, not hardware calibration.
// C/S/Pro running the standalone REST firmware have a control adapter. The Z
// entry describes one brewing module, not a combined Z2/Z4 program.
func Profiles() []domain.Profile {
	const assumptions = "70% efficiency is a planning assumption; replace it with measured efficiency. Locations must be supplied and checked against the actual machine."
	const picoAssumptions = "5 L is a nominal batch size. 50% efficiency is Small Brew Companion's community planning estimate, not a measurement of your machine. Replace it with your measured efficiency. The Hub uses a 2 kg grain planning ceiling based on Small Brew Companion guidance. The shared crafter planning allowance is 42.5 g per hop addition. Set lower working limits for your insert. Locations must be supplied and checked against the actual machine."
	picoSource := domain.ProfileSource{
		Label:       "Small Brew Companion: Pico C/S/Pro recipe scaling",
		URL:         "https://www.smallbrewcompanion.com/docs/scale-recipe-to-pico-c-s-pro-manual-steps/",
		Description: "Community starting points: 5 L batches and approximately 50% efficiency. Does not establish a maximum grain capacity.",
	}
	grainSource := domain.ProfileSource{
		Label:       "Small Brew Companion: grain loading guidance",
		URL:         "https://www.smallbrewcompanion.com/docs/adjust-recipe-advanced-feature/",
		Description: "The guide recommends staying below 4.5 lb for C/S/Pro. The Hub rounds down to a 2 kg planning ceiling.",
	}
	zFillSource := domain.ProfileSource{
		Label:       "PicoBrew Inc. Beer Crafter starting water",
		URL:         "https://github.com/jeffreydwalter/picobrew-recipes/blob/8e0289a5b9aaef8b87d94bbe2eb4b7b4f1430e6e/data/recipes/14a02ce90d1f49b7b5a75b1713344825.json",
		Description: "The Hub uses the 4.27 US gallons specified in PicoBrew’s Let It All Hang Out brew sheet as a conservative planning ceiling for one stock brew keg.",
	}
	return []domain.Profile{
		{
			ID: "pico-pro", Name: "Pico Pro (standalone)", BatchLiters: 5, Efficiency: 50,
			MaxSteps: 15, MaxTargetC: 100, MaxGrainKG: 2, HopSlots: 4, MaxHopSlotG: 42.5, ControlSupported: true, Notes: picoAssumptions,
			Sources: []domain.ProfileSource{picoSource, grainSource, {
				Label:       "Pico Pro brewing keg fill",
				URL:         "https://www.williams-sonoma.com/netstorage/pdf/113075-Picobrew-Pico-Pro-Manual.pdf",
				Description: "The PicoPak instructions specify 1 US gallon plus 6 US cups of water in the brew keg.",
			}, {
				Label:       "PicoBrew: Pico Pro specifications",
				URL:         "https://picobrew.com/store/products/pico",
				Description: "Manufacturer nominal batch size: 5 L. The separately listed 6.6 L keg volume is not the batch size.",
			}},
		},
		{
			ID: "pico-s", Name: "Pico S (standalone)", BatchLiters: 5, Efficiency: 50,
			MaxSteps: 15, MaxTargetC: 100, MaxGrainKG: 2, HopSlots: 4, MaxHopSlotG: 42.5, ControlSupported: true, Notes: picoAssumptions,
			Sources: []domain.ProfileSource{picoSource, grainSource, {
				Label:       "Original Pico brewing keg fill",
				URL:         "https://www.williams-sonoma.com/netstorage/pdf/Pico-Brew-Pico-Craft-Beer-Brewing-Appliance-89744-User-Manual.pdf",
				Description: "The PicoPak instructions identify the brew keg’s fill line as 1 US gallon plus 6 US cups.",
			}},
		},
		{
			ID: "pico-c", Name: "Pico C (standalone)", BatchLiters: 5, Efficiency: 50,
			MaxSteps: 15, MaxTargetC: 100, MaxGrainKG: 2, HopSlots: 4, MaxHopSlotG: 42.5, ControlSupported: true, Notes: picoAssumptions,
			Sources: []domain.ProfileSource{picoSource, grainSource, {
				Label:       "Pico C brewing keg fill",
				URL:         "https://www.williams-sonoma.com/netstorage/pdf/111895-Picobrew-New-Model-C-Manual.pdf",
				Description: "The PicoPak instructions fill the original brew keg to its 5.2 L mark.",
			}, {
				Label:       "PicoBrew: Pico C specifications",
				URL:         "https://picobrew.com/store/products/picoc",
				Description: "Manufacturer nominal batch size: 5 L. This specification does not establish grain capacity or efficiency.",
			}},
		},
		{ID: "z", Sources: []domain.ProfileSource{zFillSource, {
			Label: "Z Series brewing instructions", URL: "https://picobrew.com/about/manual?p=z_manual.txt",
			Description: "PicoBrew specifies a 5-US-gallon brew keg and the starting water called for by the recipe.",
		}}, Name: "PicoBrew Z (standalone canonical recipes)", BatchLiters: 9.46352946, Efficiency: 70, MaxSteps: 15, MaxTargetC: 100, MaxGrainKG: 4.08233133, HopSlots: 4, MaxHopSlotG: 42.5, ControlSupported: true, Notes: assumptions + " Nominal one-module planning batch is 2.5 US gallons; grain capacity is 9 lb. Hardware identity remains generic z-series until Z1/Z2 mapping is established. Canonical recipes require explicitly verified direct valve locations 1–6 and at most 15 steps. Archived Z/Zymatic native programs use a different routing/pause grammar and require explicit conversion before deployment."},
		{ID: "zymatic", Sources: []domain.ProfileSource{zFillSource, {
			Label: "Zymatic brewing instructions", URL: "https://picodevblob.blob.core.windows.net/content/zymatic/Zymatic_Manual.pdf",
			Description: "PicoBrew specifies a 5-US-gallon brew keg and the starting water called for by the recipe.",
		}}, Name: "Zymatic (stock menu sync)", BatchLiters: 9.46352946, Efficiency: 70, MaxSteps: 25, MaxTargetC: 100, MaxGrainKG: 4.08233133, HopSlots: 4, MaxHopSlotG: 42.5, Notes: assumptions + " Nominal batch is 2.5 US gallons; grain capacity is 9 lb. Use the stock Zymatic menu to publish a recipe for the machine to fetch."},
	}
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func text(value string, max int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && len(value) <= max && !strings.ContainsRune(value, '\x00')
}

func validateConditioning(r domain.Recipe) error {
	if len(r.Fermentation) > 32 {
		return errors.New("too many fermentation stages")
	}
	for i, phase := range r.Fermentation {
		if !text(phase.Name, 512) || !finite(phase.TargetC) || phase.TargetC < -10 || phase.TargetC > 60 || !finite(phase.Days) || phase.Days < 0 || (phase.Days == 0 && i != len(r.Fermentation)-1) || phase.Days > 3650 {
			return errors.New("invalid fermentation phase")
		}
	}
	if !finite(r.Carbonation.Volumes) || r.Carbonation.Volumes < 0 || r.Carbonation.Volumes > 10 {
		return errors.New("invalid carbonation plan")
	}
	if c := r.Carbonation.TemperatureC; c != nil && (!finite(*c) || *c < -10 || *c > 60) {
		return errors.New("invalid carbonation temperature")
	}
	if r.Carbonation.Method != "" {
		if _, err := enum(r.Carbonation.Method, "forced", "natural"); err != nil {
			return err
		}
	}
	return nil
}

// Validate accepts canonical plans, including unmapped location zero. Native
// timing, label and routing constraints are checked by Adapt/NativeJSON instead.
// Canonical units: fermentable kg; hop g; water L; yeast/misc g, mL or count.
func Validate(r domain.Recipe) error {
	if !text(r.Name, 512) {
		return errors.New("recipe name is required and must be valid text of at most 512 bytes")
	}
	if len(r.ID) > 256 || strings.ContainsRune(r.ID, '\x00') {
		return errors.New("invalid recipe id")
	}
	if err := validateOrigin(r); err != nil {
		return err
	}
	if err := validateConditioning(r); err != nil {
		return err
	}
	if IsNative(r) {
		return validateNativeLibrary(r)
	}
	if r.Kind != "" && r.Kind != "recipe" {
		return errors.New("recipe kind must be recipe, native-program or maintenance")
	}
	if r.NativeProgram != nil {
		return errors.New("a native_program requires kind native-program or maintenance")
	}
	if !finite(r.BatchLiters) || r.BatchLiters <= 0 || r.BatchLiters > 10000 {
		return errors.New("batch_liters must be positive and at most 10000")
	}
	if r.Provenance != nil && (!finite(r.Provenance.MaxBatchLiters) || r.Provenance.MaxBatchLiters < 0) {
		return errors.New("invalid source batch limit")
	}
	if !finite(r.Efficiency) || r.Efficiency <= 0 || r.Efficiency > 100 {
		return errors.New("efficiency must be a percentage greater than zero and at most 100")
	}
	if r.Type != "" {
		if _, err := enum(r.Type, "All Grain", "Partial Mash", "Extract"); err != nil {
			return err
		}
	}
	if !finite(r.BoilLiters) || r.BoilLiters < 0 || r.BoilLiters > 10000 || !finite(r.BoilMinutes) || r.BoilMinutes < 0 || r.BoilMinutes > 43200 {
		return errors.New("invalid boil volume or duration")
	}
	if len(r.Water) > 32 {
		return errors.New("too many water parameters")
	}
	for name, value := range r.Water {
		if !text(name, 64) || !finite(value) || value < 0 || name == "ph" && value > 14 {
			return errors.New("invalid water chemistry parameter")
		}
	}
	if len(r.Ingredients) > 512 || len(r.Steps) == 0 || len(r.Steps) > 256 {
		return errors.New("a recipe requires 1–256 steps and at most 512 ingredients")
	}
	if len(r.SourceYeast) > 512 {
		return errors.New("source yeast name is too long")
	}
	if len(r.Description) > 65536 || len(r.Notes) > 65536 || len(r.Tags) > 100 {
		return errors.New("recipe metadata is too large")
	}
	for i, in := range r.Ingredients {
		if !text(in.Name, 512) || !finite(in.Amount) || in.Amount <= 0 || !finite(in.Minutes) || in.Minutes < 0 || !finite(in.Alpha) || in.Alpha < 0 || in.Alpha > 100 {
			return fmt.Errorf("ingredient %d has invalid name, amount, timing or alpha percentage", i+1)
		}
		if !finite(in.Attenuation) || in.Attenuation < 0 || in.Attenuation > 100 || (in.Attenuation != 0 && in.Kind != "yeast") {
			return fmt.Errorf("ingredient %d has invalid yeast attenuation", i+1)
		}
		if !finite(in.Yield) || in.Yield < 0 || in.Yield > 100 || !finite(in.Color) || in.Color < 0 {
			return fmt.Errorf("ingredient %d has invalid yield or color", i+1)
		}
		valid := false
		switch in.Kind {
		case "fermentable":
			valid = in.Unit == "kg"
		case "hop":
			valid = in.Unit == "g"
		case "water":
			valid = in.Unit == "L"
		case "yeast", "misc":
			valid = in.Unit == "g" || in.Unit == "mL" || in.Unit == "count"
		}
		if in.SlotAddition && (in.Kind != "misc" || in.Unit != "g" || in.Use != "boil") {
			return fmt.Errorf("ingredient %d: a timed slot adjunct requires a boil addition measured in grams", i+1)
		}
		if !valid {
			return fmt.Errorf("ingredient %d has unsupported kind/unit %q/%q", i+1, in.Kind, in.Unit)
		}
	}
	for i, step := range r.Steps {
		if !text(step.Name, 512) || !stepKinds[step.Kind] {
			return fmt.Errorf("step %d has a missing name or unsupported kind %q", i+1, step.Kind)
		}
		if !finite(step.TargetC) || step.TargetC < 0 || step.TargetC > 100 {
			return fmt.Errorf("step %d target_c must be 0–100 Celsius", i+1)
		}
		if step.Minutes < 0 || step.Minutes > 43200 || step.DrainMinutes < 0 || step.DrainMinutes > 1440 {
			return fmt.Errorf("step %d has invalid duration", i+1)
		}
		if step.Minutes == 0 && (step.Kind != "heat" && step.Kind != "cool" || step.TargetC == 0) {
			return fmt.Errorf("step %d: zero minutes requires a heat/cool step with an explicit target", i+1)
		}
		if step.Location < 0 || step.Location > 6 {
			return fmt.Errorf("step %d location must be 0 (unmapped) or an explicit 1–6", i+1)
		}
		if step.Kind == "unheated" && step.TargetC != 0 {
			return fmt.Errorf("step %d unheated target must be zero", i+1)
		}
		if step.Type != "" {
			if _, err := enum(step.Type, "Temperature", "Infusion", "Decoction"); err != nil {
				return fmt.Errorf("step %d: %w", i+1, err)
			}
		}
		if !finite(step.InfuseLiters) || step.InfuseLiters < 0 || !finite(step.RampMinutes) || step.RampMinutes < 0 {
			return fmt.Errorf("step %d has invalid infusion volume or ramp time", i+1)
		}
	}
	return nil
}

func supportedProfile(id string) (domain.Profile, bool) {
	for _, profile := range Profiles() {
		if profile.ID == id {
			return profile, true
		}
	}
	return domain.Profile{}, false
}

func kettleFermentable(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "extract", "dry extract", "sugar":
		return true
	}
	return false
}

// Count the measured mass that the loading sheet assigns to the mash basket.
func mashLoadKG(in domain.Ingredient) float64 {
	use := strings.ToLower(strings.TrimSpace(in.Use))
	if in.Kind != "fermentable" {
		if use == "mash" && in.Unit == "g" {
			return in.Amount / 1000
		}
		return 0
	}
	if use == "mash" {
		return in.Amount
	}
	if use == "boil" || kettleFermentable(in.Type) {
		return 0
	}
	// Older recipes without placement metadata used grain type to identify basket loads.
	return in.Amount
}

// BeerXML water profiles, mash infusions and sparge additions can describe
// different quantities. Only sum water identified as a starting charge.
func startingKegWater(r domain.Recipe) float64 {
	var liters, legacyLiters float64
	var waterEntries int
	for _, in := range r.Ingredients {
		if in.Kind != "water" || in.Unit != "L" {
			continue
		}
		waterEntries++
		switch strings.ToLower(strings.TrimSpace(in.Use)) {
		case "starting keg":
			liters += in.Amount
		case "mash":
			switch strings.ToLower(strings.TrimSpace(in.Name)) {
			case "source starting water", "scaled source water (reference)", "starting keg water":
				liters += in.Amount
			case "brewing water":
				// Older Hub estimators created a single row with this label.
				legacyLiters = in.Amount
			}
		}
	}
	if waterEntries == 1 {
		liters += legacyLiters
	}
	return liters
}

func stockKegFillLimit(model string) float64 {
	const usGallonLiters = 3.785411784
	switch model {
	case "pico-s", "pico-pro":
		return (1 + 6.0/16) * usGallonLiters // Pico S and Pro stock brew-keg fill line.
	case "pico-c":
		return 5.2 // C brew keg mark.
	case "z", "zymatic":
		return 4.27 * usGallonLiters // Manufacturer brew sheet for a stock 5-US-gal keg.
	}
	return 0
}

// ValidateCapacity checks the quantities for one batch, after scaling when adapting.
func ValidateCapacity(r domain.Recipe, profile domain.Profile) error {
	if r.Provenance != nil && r.Provenance.MaxBatchLiters > 0 && r.BatchLiters > r.Provenance.MaxBatchLiters {
		return fmt.Errorf("batch exceeds the %.3f L source limit", r.Provenance.MaxBatchLiters)
	}
	if (!r.EstimatedYield || profile.UserConfigured) && r.BatchLiters > profile.BatchLiters+1e-9 {
		return fmt.Errorf("batch exceeds the %.3g L limit for %s", profile.BatchLiters, profile.Name)
	}

	if fill := startingKegWater(r); fill > 0 {
		limit := stockKegFillLimit(profile.ID)
		if limit == 0 {
			return fmt.Errorf("no working keg fill is available for %s", profile.Name)
		}
		if fill > limit+1e-9 {
			return fmt.Errorf("starting keg water %.2f L exceeds the %.2f L working fill for %s", fill, limit, profile.Name)
		}
	}
	if profile.MaxSteps > 0 && len(r.Steps) > profile.MaxSteps {
		return fmt.Errorf("recipe has %d steps; configured profile allows %d", len(r.Steps), profile.MaxSteps)
	}
	for i, step := range r.Steps {
		if step.TargetC > profile.MaxTargetC {
			return fmt.Errorf("step %d exceeds configured maximum temperature", i+1)
		}
	}
	grain, timings := ingredientLoads(r)
	for minute, amount := range timings {
		if profile.MaxHopSlotG > 0 && amount > profile.MaxHopSlotG+1e-9 {
			return fmt.Errorf("combined slot load at %g minutes exceeds the %.3g g slot limit for %s", minute, profile.MaxHopSlotG, profile.Name)
		}
	}
	if profile.MaxGrainKG > 0 && grain > profile.MaxGrainKG+1e-9 {
		return fmt.Errorf("grain bill %.3f kg exceeds the %.3f kg grain limit for %s", grain, profile.MaxGrainKG, profile.Name)
	}
	if profile.HopSlots > 0 && len(timings) > profile.HopSlots {
		return fmt.Errorf("%d timed hop additions exceed the configured %d slots", len(timings), profile.HopSlots)
	}
	return nil
}

// Adapt scales the whole ingredient bill by a common factor.
// It preserves source data and returns a new plan.
// It does not solve hop utilization, mash water, losses or yeast cell counts.
// A valid ingredient plan remains available when native compilation is blocked;
// deployment always requires CanDeploy and a non-nil MachineRecipe.
func Adapt(r domain.Recipe, profile domain.Profile, batchLiters float64) (domain.Adapted, error) {
	plan, err := adapt(r, profile, batchLiters, false)
	applyProgramBlocker(&plan)
	return plan, err
}

func adapt(r domain.Recipe, profile domain.Profile, batchLiters float64, planning bool) (domain.Adapted, error) {
	if err := Validate(r); err != nil {
		return domain.Adapted{}, err
	}
	builtIn, known := supportedProfile(profile.ID)
	if !known {
		return domain.Adapted{}, fmt.Errorf("unknown machine profile %q", profile.ID)
	}
	if !finite(profile.BatchLiters) || profile.BatchLiters <= 0 || !finite(profile.Efficiency) || profile.Efficiency <= 0 || profile.Efficiency > 100 || !finite(profile.MaxTargetC) || profile.MaxTargetC <= 0 || profile.MaxTargetC > 100 || !finite(profile.MaxGrainKG) || profile.MaxGrainKG < 0 || !finite(profile.MaxHopSlotG) || profile.MaxHopSlotG < 0 || profile.MaxSteps < 0 || profile.HopSlots < 0 {
		return domain.Adapted{}, errors.New("profile has invalid capacity, temperature or efficiency")
	}
	if IsNative(r) {
		return adaptNative(r, profile, builtIn, batchLiters)
	}
	automatic := planning && batchLiters == 0 && len(r.Ingredients) > 0
	var limit string
	if automatic {
		batchLiters, limit = fittingBatch(r, profile)
	} else if batchLiters == 0 {
		batchLiters = profile.BatchLiters
	}
	if !finite(batchLiters) || batchLiters <= 0 || (!automatic && batchLiters > profile.BatchLiters+1e-9) {
		return domain.Adapted{}, errors.New("requested batch exceeds the configured profile batch limit or is invalid")
	}

	r.SourceYeast = SourceYeast(r)
	plan := domain.Adapted{Recipe: r, Profile: profile, SourceRevision: r.Revision, Warnings: []string{}, DeploymentBlockers: []string{}}
	plan.Recipe.Ingredients = append([]domain.Ingredient(nil), r.Ingredients...)
	plan.Recipe.Steps = append([]domain.RecipeStep(nil), r.Steps...)
	plan.Recipe.Tags = append([]string(nil), r.Tags...)
	plan.Recipe.Fermentation = append([]domain.FermentationStep(nil), r.Fermentation...)
	if r.Water != nil {
		plan.Recipe.Water = make(map[string]float64, len(r.Water))
		for name, value := range r.Water {
			plan.Recipe.Water[name] = value
		}
	}
	plan.Recipe.BatchLiters = batchLiters
	plan.Recipe.EstimatedYield = automatic
	plan.Recipe.Efficiency = profile.Efficiency
	ratio := batchLiters / r.BatchLiters
	plan.Recipe.BoilLiters *= ratio
	for i := range plan.Recipe.Steps {
		plan.Recipe.Steps[i].InfuseLiters *= ratio
	}
	hops, yeast := false, false
	for i := range plan.Recipe.Ingredients {
		in := &plan.Recipe.Ingredients[i]
		in.Amount *= ratio

		if in.Kind == "hop" {
			hops = true
		}
		if in.Kind == "yeast" {
			yeast = true
		}
	}
	if err := Validate(plan.Recipe); err != nil {
		return domain.Adapted{}, fmt.Errorf("scaled recipe: %w", err)
	}
	if automatic {
		plan.Warnings = append(plan.Warnings, limit)
	}
	if err := ValidateCapacity(plan.Recipe, profile); err != nil {
		if !planning {
			return domain.Adapted{}, err
		}
		plan.DeploymentBlockers = append(plan.DeploymentBlockers, err.Error())
		return plan, nil
	}
	if hops {
		plan.Warnings = append(plan.Warnings, "Hop amounts keep their original proportions. Estimated bitterness depends on hop alpha acids, boil time and wort gravity; actual utilization varies by brewer.")
	}
	if yeast {
		plan.Warnings = append(plan.Warnings, "Yeast scales by volume only; verify cell count, viability and pitch rate separately.")
	}
	if len(r.Fermentation) > 0 || r.Carbonation != (domain.Carbonation{}) {
		plan.Warnings = append(plan.Warnings, "Track fermentation and carbonation manually in the brew journal.")
	}
	plan.Warnings = append(plan.Warnings, "Verify the profile efficiency and actual grain/water capacity. Water absorption, dead space and boil-off are not modeled.")
	if profile.MaxGrainKG == 0 {
		plan.Warnings = append(plan.Warnings, "No verified grain capacity is configured for this profile; this plan does not prove the grain load fits.")
	}

	if !builtIn.ControlSupported || !profile.ControlSupported {
		plan.Profile.ControlSupported = false
		if profile.ID == "zymatic" {
			plan.DeploymentBlockers = append(plan.DeploymentBlockers, "Choose Stock Zymatic · sync menu as the target to publish to a stock brewer.")
		} else {
			plan.DeploymentBlockers = append(plan.DeploymentBlockers, "This profile has no enabled Hub control adapter. The recipe is available for planning and export only.")
		}
		return plan, nil
	}
	plan.MachineRecipe, plan.DeploymentBlockers = compileMachineRecipe(r, &plan.Warnings)
	plan.CanDeploy = plan.MachineRecipe != nil && len(plan.DeploymentBlockers) == 0
	return plan, nil
}

// compileMachineRecipe is the single canonical-to-native conversion boundary.
// Missing routes and unsupported instructions are reported without discarding
// the ingredient plan. No partial machine program is returned on failure.
func compileMachineRecipe(r domain.Recipe, warnings *[]string) (*domain.MachineRecipe, []string) {
	if len(r.Steps) > 15 {
		return nil, []string{"Standalone firmware accepts at most 15 steps."}
	}
	blockers := []string{}
	machine := &domain.MachineRecipe{ID: r.ID, Steps: make([]domain.MachineStep, 0, len(r.Steps))}
	if !nativeID.MatchString(machine.ID) {
		basis := r.ID
		if basis == "" {
			data, _ := json.Marshal(r)
			basis = string(data)
		}
		sum := sha256.Sum256([]byte(basis))
		machine.ID = "hub-" + hex.EncodeToString(sum[:8])
		*warnings = append(*warnings, "Native recipe ID is "+machine.ID+" because firmware IDs allow only 1–20 ASCII letters, digits, '_' or '-'.")
	}
	machine.Name = label(r.Name, 31, "Recipe", warnings)
	for i, step := range r.Steps {
		if step.InfuseLiters > 0 || step.RampMinutes > 0 || step.Type != "" && !strings.EqualFold(strings.TrimSpace(step.Type), "Temperature") {
			blockers = append(blockers, fmt.Sprintf("Step %d has infusion, decoction or timed-ramp instructions with no verified native mapping; prepare an explicit machine program first.", i+1))
		}
		if step.Kind == "cool" || step.Kind == "fermentation" {
			blockers = append(blockers, fmt.Sprintf("Step %d kind %q has no verified standalone firmware mapping.", i+1, step.Kind))
		}
		if step.Location < 1 || step.Location > 6 {
			blockers = append(blockers, fmt.Sprintf("Choose a flow path for step %d before sending this recipe to the brewer.", i+1))
		}
		if step.Minutes > 599 || step.DrainMinutes > 120 {
			blockers = append(blockers, fmt.Sprintf("Step %d exceeds firmware duration limits (599 active / 120 drain minutes).", i+1))
		}
		target := 0
		if step.TargetC != 0 {
			exact := step.TargetC*9/5 + 32
			if exact < 60 || exact > 212 {
				blockers = append(blockers, fmt.Sprintf("Step %d is outside the firmware range of 60–212F.", i+1))
			}
			target = int(math.Round(exact))
			if math.Abs(exact-float64(target)) > 1e-9 {
				*warnings = append(*warnings, fmt.Sprintf("Step %d temperature rounds from %.3fC to %dF for the firmware integer target.", i+1, step.TargetC, target))
			}
		}
		machine.Steps = append(machine.Steps, domain.MachineStep{Name: label(step.Name, 21, fmt.Sprintf("Step %d", i+1), warnings), Location: step.Location, TargetF: target, Minutes: step.Minutes, DrainMinutes: step.DrainMinutes})
	}
	if len(blockers) > 0 {
		return nil, blockers
	}
	if _, err := NativeJSON(*machine); err != nil {
		return nil, []string{err.Error()}
	}
	return machine, blockers
}

func label(original string, max int, fallback string, warnings *[]string) string {
	var b strings.Builder
	for _, r := range original {
		if r >= 32 && r <= 126 {
			b.WriteRune(r)
		} else {
			b.WriteByte('?')
		}
	}
	value := strings.TrimSpace(b.String())
	if value == "" {
		value = fallback
	}
	if len(value) > max {
		value = value[:max]
	}
	if value != original {
		*warnings = append(*warnings, fmt.Sprintf("Device label %q becomes %q (printable ASCII, at most %d bytes); the canonical label is unchanged.", original, value, max))
	}
	return value
}

// NativeJSON validates the exact standalone API wire schema and its 4095-byte
// request cap. Its bounds come from firmware docs/standalone-api.md and
// src/local_api.c, not a stock PicoBrew cloud protocol.
func NativeJSON(r domain.MachineRecipe) ([]byte, error) {
	validLabel := func(s string, n int) bool {
		if len(s) < 1 || len(s) > n {
			return false
		}
		for _, c := range []byte(s) {
			if c < 32 || c > 126 {
				return false
			}
		}
		return true
	}
	if r.ZPak != nil {
		if r.ZProgram != nil || !nativeID.MatchString(r.ID) || !validLabel(r.Name, 31) {
			return nil, errors.New("choose a printable PicoPak device name up to 31 characters and one program format")
		}
		if err := r.ZPak.Validate(r.Steps); err != nil {
			return nil, err
		}
		for _, s := range r.Steps {
			if !validLabel(s.Name, 21) {
				return nil, errors.New("PicoPak device step names must use up to 21 printable characters")
			}
		}
		data, err := json.Marshal(r)
		if len(data) > 4095 {
			return nil, errors.New("PicoPak exceeds the device request limit")
		}
		return data, err
	}
	if r.ZProgram != nil {
		if !nativeID.MatchString(r.ID) || r.Name != r.ZProgram.Name || len(r.Steps) != 0 {
			return nil, errors.New("invalid Z program identity or conflicting steps")
		}
		if err := r.ZProgram.Validate(); err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			ID string           `json:"id"`
			Z  *domain.ZProgram `json:"z_program"`
		}{r.ID, r.ZProgram})
	}
	if !nativeID.MatchString(r.ID) || !validLabel(r.Name, 31) || len(r.Steps) < 1 || len(r.Steps) > 15 {
		return nil, errors.New("invalid native recipe id, name or step count")
	}
	for i, s := range r.Steps {
		if !validLabel(s.Name, 21) || s.Location < 1 || s.Location > 6 || (s.TargetF != 0 && (s.TargetF < 60 || s.TargetF > 212)) || s.Minutes < 0 || s.Minutes > 599 || s.DrainMinutes < 0 || s.DrainMinutes > 120 {
			return nil, fmt.Errorf("invalid native step %d", i+1)
		}
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if len(data) > 4095 {
		return nil, errors.New("native recipe exceeds the firmware 4095-byte request limit")
	}
	return data, nil
}

// Ingredients assigned to the same addition share one physical hop slot.
func ingredientLoads(r domain.Recipe) (float64, map[float64]float64) {
	grain := 0.0
	hops := map[float64]float64{}
	for _, in := range r.Ingredients {
		grain += mashLoadKG(in)
		use := strings.ToLower(strings.TrimSpace(in.Use))
		if in.SlotAddition || in.Kind == "hop" && (use == "boil" || use == "first-wort" || use == "first wort") {
			hops[in.Minutes] += in.Amount
		}
	}
	return grain, hops
}

// BatchLiters on an automatic plan is the yield implied by the source bill.
// The manufacturer's nominal finished volume is not a grain or hop capacity.
func fittingBatch(r domain.Recipe, p domain.Profile) (float64, string) {
	grain, hops := ingredientLoads(r)
	scale := math.Inf(1)
	reason := ""
	if p.MaxGrainKG > 0 && grain > 0 {
		scale = p.MaxGrainKG / grain
		reason = fmt.Sprintf("The grain bill fills the %.3g kg grain capacity.", p.MaxGrainKG)
	}
	var peak, minute float64
	for t, grams := range hops {
		if grams > peak || grams == peak && t > minute {
			peak, minute = grams, t
		}
	}
	if peak > 0 && p.MaxHopSlotG > 0 && p.MaxHopSlotG/peak < scale {
		scale = p.MaxHopSlotG / peak
		reason = fmt.Sprintf("The %g-minute addition fills its %.3g g slot.", minute, p.MaxHopSlotG)
	}
	if fill, limit := startingKegWater(r), stockKegFillLimit(p.ID); fill > 0 && limit > 0 && limit/fill < scale {
		scale = limit / fill
		reason = fmt.Sprintf("The starting water fills the stock brew keg to its %.2f L planning limit.", limit)
	}
	if math.IsInf(scale, 1) {
		return p.BatchLiters, "Using the profile batch volume because the recipe has no measured grain or timed hops with a capacity limit."
	}
	yield := r.BatchLiters * scale
	if p.UserConfigured && p.BatchLiters < yield {
		yield, reason = p.BatchLiters, "The saved usable batch volume limits this load."
	}
	if r.Provenance != nil && r.Provenance.MaxBatchLiters > 0 && r.Provenance.MaxBatchLiters < yield {
		return r.Provenance.MaxBatchLiters, "The original recipe builder’s batch-volume limit sets this load."
	}
	return yield, reason
}
