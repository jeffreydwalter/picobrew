package recipe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

// ImportPicoPak reads the OEM kit record, including descending timing pairs
// found in the original recipes. Missing routing or action must never default.
func ImportPicoPak(data []byte) (domain.Recipe, error) {
	var source struct {
		RecipeID uint32 `json:"PicoRecipeID"`
		BeerID   uint32 `json:"PicoBeerID"`
		Name     string
		Abv, Ibu *int
		Steps    []struct {
			Name                                string
			Action, Location, Temp, Time, Drain *int
			MinTime, MaxTime                    *int
		}
	}
	if err := uniqueJSON(data); err != nil {
		return domain.Recipe{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&source); err != nil {
		return domain.Recipe{}, err
	}
	if source.Abv == nil || source.Ibu == nil || source.Name == "" {
		return domain.Recipe{}, errors.New("original PicoPak name and both strength targets are required")
	}
	pak := &domain.ZPicoPak{RecipeID: source.RecipeID, BeerID: source.BeerID, ABV: *source.Abv, IBU: *source.Ibu}
	program := domain.MachineRecipe{ID: fmt.Sprintf("pico-pak-%d", source.RecipeID), Name: source.Name, ZPak: pak}
	for i, step := range source.Steps {
		if step.Name == "" || step.Action == nil || step.Location == nil || step.Temp == nil || step.Time == nil || step.Drain == nil {
			return domain.Recipe{}, fmt.Errorf("original PicoPak step %d is incomplete", i+1)
		}
		program.Steps = append(program.Steps, domain.MachineStep{Name: step.Name, Location: *step.Location, TargetF: *step.Temp, Minutes: *step.Time, DrainMinutes: *step.Drain})
		pak.StepOptions = append(pak.StepOptions, domain.ZPakStep{Action: *step.Action, MinTime: step.MinTime, MaxTime: step.MaxTime})
	}
	if err := pak.Validate(program.Steps); err != nil {
		return domain.Recipe{}, err
	}
	return domain.Recipe{Name: source.Name, Kind: "native-program", SourceModel: "pico", NativeProgram: &program}, nil
}
