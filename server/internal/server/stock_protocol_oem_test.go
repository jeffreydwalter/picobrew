package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
)

// The standalone Hub test suite needs no firmware checkout. Repository CI enables
// this test to run the original Pico clients against an isolated Hub, plus the
// reconstructed stock Z receiver against the exact JSON the Hub would stage.
func TestStockRecipesWithOEMFirmware(t *testing.T) {
	driver := os.Getenv("PICO_HUB_STOCK_PROTOCOL_TEST")
	if driver == "" {
		t.Skip("set PICO_HUB_STOCK_PROTOCOL_TEST to the repository's firmware test driver")
	}
	s, h, _ := newTestServer(t)
	kit, err := recipe.ImportPicoPak([]byte(`{"PicoRecipeID":420,"PicoBeerID":21,"Name":"Dial test","Abv":5,"Ibu":2,"Steps":[{"Name":"Mash","Action":0,"Location":2,"Temp":150,"Time":30,"Drain":2,"MinTime":20,"MaxTime":40},{"Name":"Hops","Action":0,"Location":3,"Temp":203,"Time":10,"Drain":1,"MinTime":18,"MaxTime":6}]}`))
	if err != nil {
		t.Fatal(err)
	}
	kit.ID, kit.Revision = "dial-test", 1
	for _, tc := range []struct{ model, variant, uid string }{
		{"pico-s", "s-pro", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"pico-pro", "s-pro", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		{"pico-c", "ssd1325", "cccccccccccccccccccccccccccccccc"},
		{"pico-c", "ssd1309", "dddddddddddddddddddddddddddddddd"},
	} {
		t.Run(tc.model+"-"+tc.variant, func(t *testing.T) {
			profile, _ := s.selectProfile(tc.model)
			item, err := compileStockPicoRecipe(kit, profile)
			if err != nil {
				t.Fatal(err)
			}
			item.RFID = "aabbccdd"
			abv, ibu := 5.5, 35.0
			item.ABV, item.IBU = &abv, &ibu
			p := domain.MachineRecipe{ID: "max-fixture", Name: "Fifteen step menu example"}
			for n := 1; n <= 15; n++ {
				p.Steps = append(p.Steps, domain.MachineStep{Name: fmt.Sprintf("Step %d", n), Location: 2, TargetF: 150, Minutes: n})
			}
			maxItem, err := compileStockPicoRecipe(domain.Recipe{ID: p.ID, Kind: "native-program", SourceModel: tc.model, Name: p.Name, NativeProgram: &p, Revision: 1}, profile)
			if err != nil {
				t.Fatal(err)
			}
			b := stockPicoBrewer{UID: tc.uid, Model: tc.model, Name: "Bench Pico", Items: []stockPicoItem{item, maxItem}}
			if _, err = s.db.Put(stockPicoKind, tc.uid, b, 0); err != nil {
				t.Fatal(err)
			}
			runStockOEMTest(t, driver, tc.variant, map[string]string{"url": h.URL, "uid": tc.uid})
		})
	}
	t.Run("Z119", func(t *testing.T) {
		items, err := recipe.Import([]byte(stockZTestProgram), "json")
		if err != nil {
			t.Fatal(err)
		}
		var programs []string
		// The third program reaches the OEM kettle cap and the local boiling limit.
		hot, err := recipe.Import([]byte(`{"Name":"Stock boil limits","IsBeer":true,"UsePicoPakMode":false,"Steps":[{"Name":"Kettle","StepType":1,"Time":900,"Location":1,"Temp":96,"Drain":0},{"Name":"Hops","StepType":1,"Time":900,"Location":3,"Temp":96,"Drain":60}]}`), "json")
		if err != nil {
			t.Fatal(err)
		}
		for _, saved := range append(append(items, validRecipe()), hot...) {
			review, err := s.reviewStockZProgram(saved)
			if err != nil {
				t.Fatal(err)
			}
			programs = append(programs, string(review.program))
		}
		runStockOEMTest(t, driver, "z", programs)
	})
}

func runStockOEMTest(t *testing.T, driver, variant string, fixture any) {
	t.Helper()
	body, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", driver, variant)
	cmd.Stdin = bytes.NewReader(body)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("OEM receiver: %v\n%s", err, out)
	}
}
