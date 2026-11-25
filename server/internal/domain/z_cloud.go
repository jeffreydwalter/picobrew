package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ZCloudProgram selects the original Z cloud compiler and its zero-based routing.
// An absent field retains the common standalone recipe grammar.
type ZCloudProgram struct {
	StartWaterML int  `json:"start_water_ml"`
	IsBeer       bool `json:"is_beer"`
}

func (z *ZCloudProgram) UnmarshalJSON(b []byte) error {
	if err := recipeFields(b, "Z cloud setup", "start_water_ml is_beer", "", ""); err != nil {
		return err
	}
	type plain ZCloudProgram
	return json.Unmarshal(b, (*plain)(z))
}

// Library names remain intact. Two original community names have a redundant
// collection prefix that exceeds the device's 31-byte program label.
func (r MachineRecipe) ZCloudDeviceName() string {
	if len(r.Name) > 31 {
		return strings.TrimPrefix(r.Name, "Base Recipe - ")
	}
	return r.Name
}

func (r MachineRecipe) ValidateZCloud() error {
	if r.ZCloud == nil || r.ZProgram != nil || r.ZCloud.StartWaterML < 1 || r.ZCloud.StartWaterML > 16163 {
		return fmt.Errorf("Z cloud schedules require explicit starting water up to 16.163 L and one native schedule")
	}
	label := func(s string, n int) bool {
		return strings.TrimSpace(s) != "" && len(s) <= n && strings.IndexFunc(s, func(r rune) bool { return r < 32 || r > 126 }) < 0
	}
	if !label(r.Name, 31) || len(r.Steps) < 1 || len(r.Steps) > 15 {
		return fmt.Errorf("Z cloud schedules require a printable device name and 1–15 steps")
	}
	for i, s := range r.Steps {
		if s.Location != 6 && s.TargetF > 0 && s.TargetF < 60 && (i == 0 || r.Steps[i-1].Location != 6) {
			return fmt.Errorf("add a pause to connect the chiller before step %d", i+1)
		}
		if !label(s.Name, 21) || s.Location < 0 || s.Location > 6 || s.TargetF != 0 && (s.TargetF < 32 || s.TargetF > 212) || s.Minutes < 0 || s.Minutes > 599 || s.DrainMinutes < 0 || s.DrainMinutes > 120 {
			return fmt.Errorf("invalid Z cloud source fields at step %d", i+1)
		}
	}
	return nil
}
