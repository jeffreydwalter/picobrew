package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"slices"
	"strings"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

// The archived source is authoritative. Advertising conversion requires a
// byte-verified original AND agreement with every normalized step in the Hub.
type zArchiveProgram struct {
	setup *domain.ZCloudProgram
	issue string
}

type zSourceStep struct {
	Name                        string
	Temp, Time, Location, Drain *int
}
type zCommunityStep struct {
	Name     string
	Location string
	Temp     *int `json:"temperature"`
	Time     *int `json:"step_time"`
	Drain    *int `json:"drain_time"`
}

func sourceSteps(source string, data []byte) (string, float64, bool, []domain.MachineStep, error) {
	var name string
	var water float64
	var steps []zSourceStep
	beer := true
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	switch source {
	case "picopak-library":
		var kit struct {
			ID             int
			Name, TypeCode string
			StartWater     float64
			Steps          []zSourceStep
		}
		if err := decoder.Decode(&kit); err != nil {
			return "", 0, false, nil, err
		}
		name, water, steps = kit.Name, kit.StartWater, kit.Steps
		// Z119's original parser treats an absent or empty type as Beer.
		beer = kit.TypeCode == "" || kit.TypeCode == "Beer"
	case "chiefwigms":
		var kit struct {
			ID          int
			Name, Notes string
			Escaped     string  `json:"name_escaped"`
			StartWater  float64 `json:"start_water"`
			Steps       []zCommunityStep
		}
		if err := decoder.Decode(&kit); err != nil {
			return "", 0, false, nil, err
		}
		name, water = kit.Name, kit.StartWater
		locations := []string{"PassThru", "Mash", "Adjunct1", "Adjunct2", "Adjunct3", "Adjunct4", "Pause"}
		for _, s := range kit.Steps {
			location := slices.Index(locations, s.Location)
			if location < 0 {
				return "", 0, false, nil, fmt.Errorf("unknown source route %q", s.Location)
			}
			steps = append(steps, zSourceStep{s.Name, s.Temp, s.Time, &location, s.Drain})
		}
	default:
		return "", 0, false, nil, errors.New("unknown Z source grammar")
	}
	var normalized []domain.MachineStep
	for _, s := range steps {
		if s.Temp == nil || s.Time == nil || s.Location == nil || s.Drain == nil {
			return "", 0, false, nil, errors.New("source step has incomplete fields")
		}
		normalized = append(normalized, domain.MachineStep{Name: s.Name, TargetF: *s.Temp, Minutes: *s.Time, Location: *s.Location, DrainMinutes: *s.Drain})
	}
	return name, water, beer, normalized, nil
}

func (a *Archive) originalZSetup(d Detail) (*domain.ZCloudProgram, error) {
	if len(d.SourceRecords) != 1 || d.NativeProgram == nil || d.Recipe == nil || d.Recipe.NativeProgram == nil {
		return nil, errors.New("missing original Z record or schedule")
	}
	r := d.SourceRecords[0]
	if !validPath(r.Path) || r.SHA256 != d.Provenance.SHA256 {
		return nil, errors.New("original Z source provenance differs")
	}
	data, err := a.read(path.Join("originals", d.SourceID, r.Path))
	if err != nil {
		return nil, err
	}
	if err = Verify(data, r.Bytes, r.SHA256); err != nil {
		return nil, err
	}
	name, water, beer, steps, err := sourceSteps(d.SourceID, data)
	if err != nil {
		return nil, err
	}
	if name != d.Name || name != d.NativeProgram.Name || name != d.Recipe.Name || name != d.Recipe.NativeProgram.Name ||
		!slices.Equal(steps, d.NativeProgram.Steps) || !slices.Equal(steps, d.Recipe.NativeProgram.Steps) {
		return nil, errors.New("normalized schedule differs from the original")
	}
	ml := int(math.Round(water * 1000))
	if ml < 1 || ml > 16163 || math.Abs(water*1000-float64(ml)) > 1e-7 {
		return nil, errors.New("starting water is missing or outside representable stock-keg limits")
	}
	p := *d.NativeProgram
	p.ZCloud = &domain.ZCloudProgram{StartWaterML: ml, IsBeer: beer}
	p.Name = p.ZCloudDeviceName()
	if err := p.ValidateZCloud(); err != nil {
		return nil, err
	}
	return p.ZCloud, nil
}
func (a *Archive) prepareZ(entry *indexed) error {
	var d Detail
	if err := a.decode("entries/"+entry.ID+".json", &d); err != nil {
		return err
	}
	setup, err := a.originalZSetup(d)
	result := zArchiveProgram{setup: setup}
	if err != nil {
		result.issue = err.Error()
	} else {
		entry.CanDeploy = true
		entry.DeployModels = []string{"z"}
	}
	a.zPrograms[entry.ID] = result
	return nil
}
func (z zArchiveProgram) apply(d *Detail) {
	clean := func(warnings []string) []string {
		result := make([]string, 0, len(warnings))
		for _, w := range warnings {
			if strings.Contains(w, "Hub has no Z control adapter") || strings.Contains(w, "Hub has no live control adapter") {
				continue
			}
			result = append(result, w)
		}
		if z.issue != "" {
			result = append(result, "Z program requires review: "+z.issue)
		}
		return result
	}
	d.Warnings = clean(d.Warnings)
	d.Provenance.Warnings = clean(d.Provenance.Warnings)
	if d.Recipe != nil && d.Recipe.Provenance != nil {
		d.Recipe.Provenance.Warnings = clean(d.Recipe.Provenance.Warnings)
	}
	if z.setup == nil {
		return
	}
	a, b := *z.setup, *z.setup
	d.NativeProgram.ZCloud = &a
	d.Recipe.NativeProgram.ZCloud = &b
	d.CanDeploy = true
	d.DeployModels = []string{"z"}
}
