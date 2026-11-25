package domain

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ZPicoPak retains the kit's authored timing range independently of the nominal
// schedule. The original firmware interpolates within each supplied pair.
type ZPicoPak struct {
	RecipeID    uint32     `json:"recipe_id"`
	BeerID      uint32     `json:"beer_id"`
	ABV         int        `json:"abv"`
	IBU         int        `json:"ibu"`
	SelectedABV *int       `json:"selected_abv,omitempty"`
	SelectedIBU *int       `json:"selected_ibu,omitempty"`
	StepOptions []ZPakStep `json:"step_options"`
}
type ZPakStep struct {
	Action  int  `json:"action"`
	MinTime *int `json:"min_time,omitempty"`
	MaxTime *int `json:"max_time,omitempty"`
}

func (p *ZPicoPak) UnmarshalJSON(b []byte) error {
	if err := recipeFields(b, "Z PicoPak", "recipe_id beer_id abv ibu step_options", "selected_abv selected_ibu", ""); err != nil {
		return err
	}
	type wire ZPicoPak
	return json.Unmarshal(b, (*wire)(p))
}
func (s *ZPakStep) UnmarshalJSON(b []byte) error {
	if err := recipeFields(b, "PicoPak step options", "action", "min_time max_time", ""); err != nil {
		return err
	}
	type wire ZPakStep
	return json.Unmarshal(b, (*wire)(s))
}

func (p ZPicoPak) Validate(steps []MachineStep) error {
	if p.RecipeID == 0 || p.BeerID == 0 || p.ABV < -1 || p.ABV > 127 || p.IBU < -1 || p.IBU > 127 || len(steps) == 0 || len(steps) > 15 || len(p.StepOptions) != len(steps) {
		return errors.New("the PicoPak needs original recipe and beer IDs, strength targets and matching step options")
	}
	abv, ibu := false, false
	for i, s := range steps {
		o := p.StepOptions[i]
		if o.Action < 0 || o.Action > 1 || o.MinTime != nil && (*o.MinTime < 0 || *o.MinTime > 599) || o.MaxTime != nil && (*o.MaxTime < 0 || *o.MaxTime > 599) ||
			s.Location < 1 || s.Location > 6 || s.TargetF != 0 && (s.TargetF < 32 || s.TargetF > 212) || s.Minutes < 0 || s.Minutes > 599 || s.DrainMinutes < 0 || s.DrainMinutes > 120 {
			return fmt.Errorf("invalid PicoPak routing, action, temperature or timing on step %d", i+1)
		}
		if o.Action == 0 && s.TargetF > 0 && s.TargetF < 60 && (i == 0 || p.StepOptions[i-1].Action != 1) {
			return fmt.Errorf("add an operator pause to connect the chiller before step %d", i+1)
		}
		if o.Action == 0 && o.MinTime != nil && o.MaxTime != nil {
			abv = abv || s.Location == 2
			ibu = ibu || s.Location >= 3
		}
	}
	for _, v := range []struct {
		selected   *int
		original   int
		adjustable bool
	}{{p.SelectedABV, p.ABV, abv}, {p.SelectedIBU, p.IBU, ibu}} {
		if v.selected != nil && (*v.selected < 0 || *v.selected > 10 || *v.selected != v.original && (v.original < 1 || v.original > 9 || !v.adjustable)) {
			return errors.New("an adjusted strength requires a kit target from 1 through 9 and matching authored step times")
		}
	}
	return nil
}

// ValidatePicoTransfer checks for kit instructions that an ordinary Pico slot
// would discard. A selection equal to the kit target keeps the original time.
func (p ZPicoPak) ValidatePicoTransfer() error {
	if p.SelectedABV != nil && *p.SelectedABV != p.ABV || p.SelectedIBU != nil && *p.SelectedIBU != p.IBU {
		return errors.New("selected kit strength or bitterness requires the Z PicoPak adapter")
	}
	for _, option := range p.StepOptions {
		if option.Action != 0 {
			return errors.New("this kit has an operator pause that requires the Z PicoPak adapter")
		}
	}
	return nil
}

// OriginalPicoPak and its steps use the vendor's case-sensitive JSON fields.
type OriginalPicoPak struct {
	PicoRecipeID, PicoBeerID uint32
	Name                     string
	Abv, Ibu                 int
	Steps                    []OriginalPicoPakStep
}
type OriginalPicoPakStep struct {
	Name                                string
	Action, Location, Temp, Time, Drain int
	MinTime, MaxTime                    *int
}

func (p *OriginalPicoPak) UnmarshalJSON(data []byte) error {
	if err := recipeFields(data, "original PicoPak", "PicoRecipeID PicoBeerID Name Abv Ibu Steps", "", ""); err != nil {
		return err
	}
	type wire OriginalPicoPak
	return json.Unmarshal(data, (*wire)(p))
}

func (s *OriginalPicoPakStep) UnmarshalJSON(data []byte) error {
	if err := recipeFields(data, "original PicoPak step", "Name Action Location Temp Time Drain", "MinTime MaxTime", "MinTime MaxTime"); err != nil {
		return err
	}
	type wire OriginalPicoPakStep
	return json.Unmarshal(data, (*wire)(s))
}
