package recipe

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

func TestLongProgramMakerFiles(t *testing.T) {
	profile, _ := supportedProfile("z")
	program := domain.ZProgram{Name: "Long program", IsBeer: true}
	for i := range domain.MaxZProgramSteps {
		program.Steps = append(program.Steps, domain.ZProgramStep{Name: fmt.Sprintf("Pause %d", i+1), Type: 2})
	}
	raw, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	items, err := Import(raw, "json")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Translate(items[0], profile, 0)
	if err != nil || !plan.CanDeploy || !reflect.DeepEqual(plan.MachineRecipe.ZProgram, &program) {
		t.Fatalf("20-step standalone schedule changed: %+v, %v", plan, err)
	}
	out, err := ExportZProgram(items[0], profile)
	if err != nil || string(out) != string(raw) {
		t.Fatalf("stock file at exactly 64 JSON entries: %s, %v", out, err)
	}
	seconds := 0
	items[0].NativeProgram.ZProgram.Steps[0].Time = &seconds
	if _, err := ExportZProgram(items[0], profile); err == nil || !strings.Contains(err.Error(), "JSON memory") {
		t.Fatalf("stock receiver has no space for a 65th entry: %v", err)
	}
	if _, err := Translate(items[0], profile, 0); err != nil {
		t.Fatalf("standalone target inherited the stock JSON limit: %v", err)
	}
	program.Steps = append(program.Steps, program.Steps[0])
	if err := program.Validate(); err == nil {
		t.Fatal("accepted a 21st standalone step")
	}
}

func TestProgramMakerExchangeAndTargetLimits(t *testing.T) {
	const data = `{"Name":"Test","IsMetric":true,"IsBeer":false,"UsePicoPakMode":true,"Steps":[{"Name":"Adjunct 4","StepType":1,"Location":6,"Temp":64.21,"Time":5401,"Drain":37},{"Name":"Operator","StepType":2},{"Name":"Unheated","StepType":1,"Location":1,"Temp":null,"Time":0,"Drain":0}]}`
	items, err := Import([]byte(data), "json")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range Profiles() {
		plan, err := Translate(items[0], p, 0)
		if err != nil || plan.CanDeploy != (p.ID == "z") {
			t.Fatal(p.ID, err, plan.DeploymentBlockers)
		}
	}
	out, err := Export(items[0], "z-program")
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if json.Unmarshal([]byte(data), &want) != nil || json.Unmarshal(out, &got) != nil || !reflect.DeepEqual(want, got) {
		t.Fatal(string(out))
	}
	for _, bad := range []string{strings.Replace(data, `"StepType":2`, `"StepType":2,"Location":1`, 1), strings.Replace(data, `"Location":6`, `"Location":0`, 1), strings.Replace(data, `"Time":5401`, `"Time":-1`, 1), strings.Replace(data, `"Drain":37`, `"Drain":7201`, 1), strings.Replace(data, `"IsBeer":false,`, "", 1), strings.Replace(data, `"Temp":64.21`, `"Temp":100`, 1)} {
		if _, err := Import([]byte(bad), "json"); err == nil {
			t.Fatal("malformed program accepted", bad)
		}
	}
	r := items[0]
	s := r.NativeProgram.ZProgram.Steps[0]
	r.NativeProgram.ZProgram.Steps = nil
	for range 9 {
		r.NativeProgram.ZProgram.Steps = append(r.NativeProgram.ZProgram.Steps, s)
	}
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(r, "z-program"); err == nil {
		t.Fatal("stock decoder cannot fit nine fully populated active steps")
	}
}

func TestExportIngredientProgramUsesCompiledZRoutes(t *testing.T) {
	r := domain.Recipe{Name: "Test", BatchLiters: 5, Efficiency: 55, Ingredients: []domain.Ingredient{{Name: "Malt", Kind: "fermentable", Use: "mash", Unit: "kg", Amount: 1}}, Steps: []domain.RecipeStep{{Name: "Mash", Kind: "mash", TargetC: 65, Minutes: 60}, {Name: "Boil", Kind: "boil", TargetC: 100, Minutes: 60}}}
	b, err := Export(r, "z-program")
	if err != nil {
		t.Fatal(err)
	}
	var p domain.ZProgram
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Steps {
		if s.Name == "Mash" && *s.Location == 2 && *s.Time == 3600 {
			return
		}
	}
	t.Fatal("lost the mash route or timing", p)
}

func TestStockZExportPreservesUnheatedMeaning(t *testing.T) {
	p, _ := supportedProfile("z")
	rinse := domain.RecipeStep{Name: "Rinse", Kind: "unheated", Location: 1, Minutes: 5}
	mash := domain.RecipeStep{Name: "Mash", Kind: "mash", Location: 2, TargetC: 65, Minutes: 60}
	r := domain.Recipe{Name: "Test", BatchLiters: 5, Efficiency: 55, Steps: []domain.RecipeStep{rinse, mash}}
	data, err := ExportZProgram(r, p)
	if err != nil {
		t.Fatal(err)
	}
	var prog domain.ZProgram
	if err := json.Unmarshal(data, &prog); err != nil {
		t.Fatal(err)
	}
	if string(prog.Steps[0].Temp) != "null" {
		t.Fatal("unheated step became a numeric temperature", string(data))
	}
	r.Steps = []domain.RecipeStep{mash, rinse}
	if _, err := ExportZProgram(r, p); err == nil || !strings.Contains(err.Error(), "turn off") {
		t.Fatal("converted heater-off command into the stock target carryover", err)
	}
	plan, err := Translate(r, p, 0)
	if err != nil || !plan.CanDeploy || plan.MachineRecipe.Steps[1].TargetF != 0 {
		t.Fatal("standalone Z lost unheated circulation", err)
	}
}

func TestStockZExportChecksNativeHeatAcrossPauseAndDrain(t *testing.T) {
	const input = `{"Name":"Stock test","IsBeer":true,"UsePicoPakMode":false,"Steps":[{"Name":"Mash","StepType":1,"Location":2,"Temp":65,"Time":90,"Drain":0},{"Name":"Check","StepType":2},{"Name":"Cold","StepType":1,"Location":1,"Temp":null,"Time":15,"Drain":0}]}`
	for _, target := range []string{"null", "0", "-4"} {
		raw := strings.Replace(input, `"Temp":null`, `"Temp":`+target, 1)
		r, err := Import([]byte(raw), "json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Export(r[0], "z-program"); err == nil || !strings.Contains(err.Error(), "turn off") {
			t.Fatal("stock target carryover", err)
		}
		profile, _ := supportedProfile("z")
		if plan, err := Translate(r[0], profile, 0); err != nil || !plan.CanDeploy {
			t.Fatal("standalone NoTarget unavailable", err)
		}
		withDrain := strings.Replace(raw, `"Drain":0`, `"Drain":15`, 1)
		r, err = Import([]byte(withDrain), "json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Export(r[0], "z-program"); err != nil {
			t.Fatal("positive native drain already cleared PID", err)
		}
	}
}
