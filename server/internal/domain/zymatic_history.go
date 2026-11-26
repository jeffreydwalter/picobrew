package domain

import (
	"encoding/json"
	"slices"
	"time"
)

// Zymatic stores one native recipe route and step with the temperatures. It
// does not record S/Pro controller flags, phases or elapsed step time.
func IsZymaticHistory(fields []string) bool {
	return slices.Equal(fields, []string{"elapsed_ms", "wort_f", "thermoblock_f", "target_f", "compartment", "current_step"})
}

type fullSample Sample

// The step event state and the periodic temperature error mask are separate OEM fields.
type ZymaticReport struct {
	Kind       string `json:"kind"`
	StepName   string `json:"step_name,omitempty"`
	State      *int   `json:"state,omitempty"`
	ErrorFlags *int   `json:"error_flags,omitempty"`
	ErrorCode  *int   `json:"error_code,omitempty"`
	ResetPoint string `json:"reset_point,omitempty"`
}

func (s Sample) MarshalJSON() ([]byte, error) {
	if s.StockPico != nil {
		return json.Marshal(struct {
			ElapsedMS    int64               `json:"elapsed_ms"`
			ObservedAt   *time.Time          `json:"observed_at,omitempty"`
			Source       string              `json:"source"`
			StockPico    *PicoReport         `json:"stock_pico"`
			WortF        *float64            `json:"wort_f,omitempty"`
			ThermoblockF *float64            `json:"thermoblock_f,omitempty"`
			Readings     map[string]*float64 `json:"readings,omitempty"`
		}{s.ElapsedMS, s.ObservedAt, s.Source, s.StockPico, s.WortF, s.ThermoblockF, s.Readings})
	}
	if !s.ZymaticHistory {
		return json.Marshal(fullSample(s))
	}
	type compact struct {
		StockZymatic   *ZymaticReport      `json:"stock_zymatic,omitempty"`
		Readings       map[string]*float64 `json:"readings,omitempty"`
		ZymaticHistory bool                `json:"zymatic_history"`
		ElapsedMS      int64               `json:"elapsed_ms"`
		ObservedAt     *time.Time          `json:"observed_at,omitempty"`
		WortF          *float64            `json:"wort_f"`
		ThermoblockF   *float64            `json:"thermoblock_f"`
		TargetF        *float64            `json:"target_f"`
		Compartment    *int                `json:"compartment"`
		CurrentStep    *int                `json:"current_step"`
		Source         string              `json:"source"`
	}
	row := compact{ZymaticHistory: true, ElapsedMS: s.ElapsedMS, ObservedAt: s.ObservedAt, WortF: s.WortF, ThermoblockF: s.ThermoblockF, TargetF: s.TargetF, Source: s.Source}
	row.StockZymatic, row.Readings = s.StockZymatic, s.Readings
	if s.Compartment >= 0 {
		row.Compartment = &s.Compartment
	}
	if s.CurrentStep >= 0 {
		row.CurrentStep = &s.CurrentStep
	}
	return json.Marshal(row)
}

func (s *Sample) UnmarshalJSON(data []byte) error {
	var row struct {
		fullSample
		Compartment *int `json:"compartment"`
		CurrentStep *int `json:"current_step"`
	}
	if err := json.Unmarshal(data, &row); err != nil {
		return err
	}
	*s = Sample(row.fullSample)
	if s.ZymaticHistory {
		s.Compartment, s.CurrentStep = -1, -1
	}
	if row.Compartment != nil {
		s.Compartment = *row.Compartment
	}
	if row.CurrentStep != nil {
		s.CurrentStep = *row.CurrentStep
	}
	return nil
}
