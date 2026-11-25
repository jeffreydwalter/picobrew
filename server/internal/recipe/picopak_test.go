package recipe

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestOriginalKitSelectsTargetGrammar(t *testing.T) {
	data := []byte(`{"PicoRecipeID":1,"PicoBeerID":47,"Name":"Test kit","Abv":5,"Ibu":6,"Steps":[{"Name":"Mash","Location":2,"Temp":152,"Time":20,"Drain":2,"Action":0,"MinTime":25,"MaxTime":15},{"Name":"Hop","Location":3,"Temp":204,"Time":30,"Drain":0,"Action":0,"MinTime":null,"MaxTime":null}]}`)
	items, err := Import(data, "json")
	if err != nil || len(items) != 1 {
		t.Fatal(err)
	}
	r := items[0]
	for _, model := range []string{"pico-s", "pico-c", "pico-pro", "z"} {
		profile, _ := supportedProfile(model)
		p, err := Translate(r, profile, 0)
		if err != nil || !p.CanDeploy || p.MachineRecipe == nil {
			t.Fatalf("%s: %v %v", model, err, p.DeploymentBlockers)
		}
		if (p.MachineRecipe.ZPak != nil) != (model == "z") {
			t.Fatalf("wrong program format for %s", model)
		}
	}
	picoJSON, err := Export(r, "native")
	if err != nil || strings.Contains(string(picoJSON), "z_pak") {
		t.Fatalf("Pico slot export: %s %v", picoJSON, err)
	}
	unchanged := r.NativeProgram.ZPak.IBU
	r.NativeProgram.ZPak.SelectedIBU = &unchanged
	if _, err := Export(r, "native"); err != nil {
		t.Fatal(err)
	}
	target := 8
	r.NativeProgram.ZPak.SelectedABV = &target
	profile, _ := supportedProfile("z")
	if _, err := ExportZProgram(r, profile); err == nil {
		t.Fatal("unsafe stock export")
	}
	if _, err := Export(r, "native"); err == nil {
		t.Fatal("Pico export silently dropped a strength change")
	}
	encoded, err := Export(r, "z-picopak")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Import(encoded, "json")
	if err != nil || !reflect.DeepEqual(decoded[0].NativeProgram, r.NativeProgram) {
		t.Fatalf("lost original program semantics: %v", err)
	}
	pico, _ := supportedProfile("pico-pro")
	if plan, err := Translate(r, pico, 0); err != nil || plan.CanDeploy {
		t.Fatalf("accepted an adjustment on a controller that ignores it: %v", err)
	}
	r.NativeProgram.ZPak.SelectedIBU = &target
	if _, err := NativeJSON(*r.NativeProgram); err == nil {
		t.Fatal("accepted a bitterness adjustment without a range")
	}
	for _, bad := range []string{strings.Replace(string(data), `"Action":0,`, "", 1), strings.Replace(string(data), `"Location":2`, `"Location":7`, 1), strings.Replace(string(data), `"Action":0`, `"Action":0,"action":1`, 1), strings.Replace(string(data), `"Temp":152`, `"Temp":55`, 1)} {
		if _, err := Import([]byte(bad), "json"); err == nil {
			t.Fatal("invalid kit accepted")
		}
	}
	snapshot, _ := json.Marshal(r.NativeProgram)
	if !strings.Contains(string(snapshot), `"min_time":25,"max_time":15`) {
		t.Fatal("descending range changed")
	}
}
