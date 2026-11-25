package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// A stock Z request can hold at most 20 named pauses in its 64-slot JSON pool.
// Standalone firmware has room for 20 steps of either type.
const MaxZProgramSteps = 20

// ZProgram is the local Program Maker format. The archived cloud format
// assigns different meanings to the same numeric locations.
type ZProgram struct {
	Name           string         `json:"Name"`
	IsBeer         bool           `json:"IsBeer"`
	UsePicoPakMode bool           `json:"UsePicoPakMode"`
	IsMetric       *bool          `json:"IsMetric,omitempty"`
	Steps          []ZProgramStep `json:"Steps"`
}

type ZProgramStep struct {
	Name     string          `json:"Name"`
	Type     int             `json:"StepType"`
	Time     *int            `json:"Time,omitempty"`
	Location *int            `json:"Location,omitempty"`
	Temp     json.RawMessage `json:"Temp,omitempty"`
	Drain    *int            `json:"Drain,omitempty"`
}

func (p *ZProgram) UnmarshalJSON(b []byte) error {
	if err := recipeFields(b, "Z program", "Name IsBeer UsePicoPakMode Steps", "IsMetric", ""); err != nil {
		return err
	}
	type plain ZProgram
	return json.Unmarshal(b, (*plain)(p))
}
func (s *ZProgramStep) UnmarshalJSON(b []byte) error {
	if err := recipeFields(b, "Z program step", "Name StepType", "Time Location Temp Drain", "Temp"); err != nil {
		return err
	}
	type plain ZProgramStep
	if err := json.Unmarshal(b, (*plain)(s)); err != nil {
		return err
	}
	if len(s.Temp) > 0 && string(s.Temp) != "null" {
		var c float64
		if err := json.Unmarshal(s.Temp, &c); err != nil {
			return errors.New("Z temperature must be a number or null")
		}
		s.Temp, _ = json.Marshal(c)
	}
	return nil
}

func (p ZProgram) Validate() error {
	validName := func(s string) bool {
		return strings.TrimSpace(s) != "" && len(s) <= 20 && strings.IndexFunc(s, func(c rune) bool { return c < 32 || c > 126 }) < 0
	}
	if !validName(p.Name) || len(p.Steps) < 1 || len(p.Steps) > MaxZProgramSteps {
		return fmt.Errorf("Z programs require a printable name of up to 20 characters and 1–%d steps", MaxZProgramSteps)
	}
	for i, s := range p.Steps {
		if !validName(s.Name) || s.Time != nil && (*s.Time < 0 || *s.Time > 86400) {
			return fmt.Errorf("check the name or duration of Z step %d", i+1)
		}
		switch s.Type {
		case 1:
			if s.Time == nil || s.Location == nil || *s.Location < 1 || *s.Location > 6 || s.Drain == nil || *s.Drain < 0 || *s.Drain > 7200 || len(s.Temp) == 0 {
				return fmt.Errorf("check the flow path, hold, temperature and drain on Z step %d", i+1)
			}
			var c float64
			if err := json.Unmarshal(s.Temp, &c); err != nil || math.IsNaN(c) || c < -29 || c > 98 {
				return fmt.Errorf("invalid temperature on Z step %d", i+1)
			}
		case 2:
			if s.Location != nil || len(s.Temp) > 0 || s.Drain != nil {
				return fmt.Errorf("pause step %d cannot heat, route or drain", i+1)
			}
		default:
			return fmt.Errorf("unsupported Z step type at step %d", i+1)
		}
	}
	return nil
}

func (r MachineRecipe) StepCount() int {
	if r.ZProgram != nil {
		return len(r.ZProgram.Steps)
	}
	return len(r.Steps)
}

func (c Capabilities) RecipeStepLimit(r MachineRecipe) int {
	if r.ZProgram != nil && c.ZProgramStepCapacity != 0 {
		return c.ZProgramStepCapacity
	}
	return c.StepCapacity
}
