package recipe

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

// ImportPicoPak reads the OEM kit record, including descending timing pairs
// found in the original recipes. Missing routing or action must never default.
func ImportPicoPak(data []byte) (domain.Recipe, error) {
	var source domain.OriginalPicoPak
	if err := uniqueJSON(data); err != nil {
		return domain.Recipe{}, err
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return domain.Recipe{}, err
	}
	if source.Name == "" {
		return domain.Recipe{}, errors.New("original PicoPak name is required")
	}
	pak := &domain.ZPicoPak{RecipeID: source.PicoRecipeID, BeerID: source.PicoBeerID, ABV: source.Abv, IBU: source.Ibu}
	program := domain.MachineRecipe{ID: fmt.Sprintf("pico-pak-%d", source.PicoRecipeID), Name: source.Name, ZPak: pak}
	for i, step := range source.Steps {
		if step.Name == "" {
			return domain.Recipe{}, fmt.Errorf("original PicoPak step %d is incomplete", i+1)
		}
		program.Steps = append(program.Steps, domain.MachineStep{Name: step.Name, Location: step.Location, TargetF: step.Temp, Minutes: step.Time, DrainMinutes: step.Drain})
		pak.StepOptions = append(pak.StepOptions, domain.ZPakStep{Action: step.Action, MinTime: step.MinTime, MaxTime: step.MaxTime})
	}
	if err := pak.Validate(program.Steps); err != nil {
		return domain.Recipe{}, err
	}
	return domain.Recipe{Name: source.Name, Kind: "native-program", SourceModel: "pico", NativeProgram: &program}, nil
}
