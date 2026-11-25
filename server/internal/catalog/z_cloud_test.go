package catalog

import (
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
	"testing"
)

func TestZArchiveProgramsPreserveSourceForStandaloneOnly(t *testing.T) {
	a, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.zPrograms) != 39 {
		t.Fatalf("expected 39 archived Z programs, got %d", len(a.zPrograms))
	}
	for id, record := range a.zPrograms {
		if record.issue != "" || record.setup == nil {
			t.Fatalf("%s: %+v", id, record)
		}
		d, err := a.Detail(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, profile := range recipe.Profiles() {
			plan, err := recipe.Translate(*d.Recipe, profile, 0)
			if err != nil {
				t.Fatal(id, err)
			}
			if plan.CanDeploy != (profile.ID == "z") {
				t.Errorf("%s: %s deployment %t", id, profile.ID, plan.CanDeploy)
			}
			if plan.CanDeploy && (plan.MachineRecipe.ZCloud == nil || *plan.MachineRecipe.ZCloud != *record.setup) {
				t.Fatal(id, "lost native setup")
			}
		}
		d.NativeProgram.Steps[0].Location = 6
		if _, err := a.originalZSetup(d); err == nil {
			t.Fatal(id, "accepted a changed first step")
		}
	}
}
