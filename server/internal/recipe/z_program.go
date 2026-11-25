package recipe

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

func importZProgram(data []byte) (domain.Recipe, error) {
	var p domain.ZProgram
	if err := json.Unmarshal(data, &p); err != nil {
		return domain.Recipe{}, err
	}
	if err := p.Validate(); err != nil {
		return domain.Recipe{}, err
	}
	normalized, err := json.Marshal(p)
	if err != nil {
		return domain.Recipe{}, err
	}
	digest := sha256.Sum256(normalized)
	id := fmt.Sprintf("z-%x", digest[:8])
	return domain.Recipe{Kind: "native-program", SourceModel: "z", Name: p.Name, NativeProgram: &domain.MachineRecipe{ID: id, Name: p.Name, ZProgram: &p}}, nil
}

// ExportZProgram checks both the selected Z equipment profile and the original
// 0.0.119 local receiver's 64-slot JSON pool and 2048-byte request body.
func ExportZProgram(r domain.Recipe, profile domain.Profile) ([]byte, error) {
	if profile.ID != "z" {
		return nil, errors.New("select the Z brewer profile")
	}
	plan, err := Translate(r, profile, 0)
	if err != nil {
		return nil, err
	}
	if plan.MachineRecipe == nil {
		return nil, fmt.Errorf("cannot compile this Z program: %v", plan.DeploymentBlockers)
	}
	native := plan.MachineRecipe
	p := native.ZProgram
	if p == nil {
		p = &domain.ZProgram{Name: r.Name, IsBeer: true}
		for _, s := range native.Steps {
			hold, drain, location := s.Minutes*60, s.DrainMinutes*60, s.Location
			temp := json.RawMessage("null")
			if s.TargetF > 0 {
				temp, _ = json.Marshal(float64(s.TargetF-32) / 1.8)
			}
			p.Steps = append(p.Steps, domain.ZProgramStep{Name: s.Name, Type: 1, Time: &hold, Location: &location, Temp: temp, Drain: &drain})
		}
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	// Z119's MoveValve restarts an active PID. A positive drain emits StopPid
	// and clears that state; an operator pause stops outputs temporarily and
	// retains the requested PID state for resume. Apply this to imported and
	// translated recipes alike, before producing stock-compatible JSON.
	heated := false
	for _, step := range p.Steps {
		if step.Type != 1 {
			continue
		}
		var celsius float64
		if err := json.Unmarshal(step.Temp, &celsius); err != nil {
			return nil, err
		}
		if celsius <= 0 && heated {
			return nil, fmt.Errorf("stock Z cannot turn off the previous heating target for %q; send this recipe to standalone Z firmware", step.Name)
		}
		heated = celsius > 0 && *step.Drain == 0
	}
	slots := 4
	if p.IsMetric != nil {
		slots++
	}
	for _, s := range p.Steps {
		slots += 3 // array entry, Name, StepType
		if s.Time != nil {
			slots++
		}
		if s.Type == 1 {
			slots += 3
		}
	}
	if slots > 64 {
		return nil, errors.New("this program exceeds the stock Z firmware's JSON memory limit; shorten the program or use a compatible standalone Z")
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	data := bytes.TrimSpace(b.Bytes())
	if len(data) > 2048 {
		return nil, errors.New("program exceeds the stock Z request size")
	}
	return data, nil
}
