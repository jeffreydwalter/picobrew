package catalog

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
)

func TestPinnedArchiveIntegrityAndCopyCoverage(t *testing.T) {
	a, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.entries) != 5216 {
		t.Fatalf("unexpected catalogue size: %d", len(a.entries))
	}
	kinds := map[string]int{}
	copyable, deployable, originalRecords := 0, 0, 0
	for _, summary := range a.entries {
		detail, err := a.Detail(summary.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(summary.Summary, detail.Summary) {
			t.Fatalf("index/detail mismatch: %s", summary.ID)
		}
		kinds[detail.Kind]++
		for _, record := range detail.SourceRecords {
			data, got, err := a.Original(detail.ID, record.ID)
			if err != nil || got != record || !json.Valid(data) {
				t.Fatalf("invalid original %s/%s: %v", detail.ID, record.ID, err)
			}
			originalRecords++
		}
		if !detail.CanCopy {
			if detail.Recipe != nil || detail.CanDeploy {
				t.Fatalf("reference-only entry became executable: %s", detail.ID)
			}
			continue
		}
		copyable++
		if detail.Recipe == nil {
			t.Fatalf("copyable entry has no recipe: %s", detail.ID)
		}
		if err := recipe.Validate(*detail.Recipe); err != nil {
			t.Fatalf("invalid copied recipe %s: %v", detail.ID, err)
		}
		if detail.Recipe.Provenance == nil || !reflect.DeepEqual(*detail.Recipe.Provenance, detail.Provenance) {
			t.Fatalf("provenance mismatch: %s", detail.ID)
		}
		if recipe.IsNative(*detail.Recipe) {
			if detail.Recipe.BatchLiters != 0 || detail.Recipe.Efficiency != 0 || len(detail.Recipe.Ingredients) != 0 || len(detail.Recipe.Steps) != 0 || !reflect.DeepEqual(detail.NativeProgram, detail.Recipe.NativeProgram) {
				t.Fatalf("invented plan for native program %s", detail.ID)
			}
		}
		if detail.CanDeploy {
			deployable++
			if detail.NativeProgram == nil {
				t.Fatalf("deployable source has no program: %s", detail.ID)
			}
			if _, err := recipe.NativeJSON(*detail.NativeProgram); err != nil {
				t.Fatalf("invalid advertised deployment %s: %v", detail.ID, err)
			}
		}
	}
	if copyable != 5206 || deployable != 3733 || originalRecords != 10666 {
		t.Fatalf("coverage changed: %d copyable, %d deployable, %d originals", copyable, deployable, originalRecords)
	}
	if !reflect.DeepEqual(kinds, map[string]int{"recipe": 1316, "native-program": 3897, "maintenance": 3}) {
		t.Fatalf("kinds: %v", kinds)
	}
	for _, asset := range a.meta.Assets {
		if !asset.Bundled {
			continue
		}
		if _, _, err := a.AssetData(asset.Path); err != nil {
			t.Fatalf("asset %s: %v", asset.Path, err)
		}
	}
	var excluded []struct {
		SourceID string `json:"source_id"`
		Path     string `json:"path"`
		SHA256   string `json:"sha256"`
		Bytes    int64  `json:"bytes"`
	}
	if err := a.decode("excluded.json", &excluded); err != nil {
		t.Fatal(err)
	}
	if len(excluded) != 852 {
		t.Fatalf("excluded count %d", len(excluded))
	}
	for _, record := range excluded {
		data, err := a.read("originals/" + record.SourceID + "/" + record.Path)
		if err != nil || Verify(data, record.Bytes, record.SHA256) != nil {
			t.Fatalf("excluded original lost: %s", record.Path)
		}
	}
}

func TestReviewedJunkIsUnavailableButOriginalsRemain(t *testing.T) {
	a, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	var review struct {
		Removed []struct {
			ID            string         `json:"id"`
			SourceID      string         `json:"source_id"`
			Evidence      string         `json:"evidence"`
			SourceRecords []SourceRecord `json:"source_records"`
		} `json:"removed_entries"`
	}
	if err := a.decode("curation.json", &review); err != nil {
		t.Fatal(err)
	}
	if len(review.Removed) != 66 {
		t.Fatalf("reviewed removal count: %d", len(review.Removed))
	}
	count := 0
	for _, entry := range review.Removed {
		if entry.Evidence == "" {
			t.Fatalf("missing review evidence: %s", entry.ID)
		}
		if _, err := a.Detail(entry.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("removed entry is still accessible: %s (%v)", entry.ID, err)
		}
		for _, summary := range a.entries {
			if summary.ID == entry.ID {
				t.Fatalf("removed entry is still browsable: %s", entry.ID)
			}
		}
		for _, record := range entry.SourceRecords {
			data, err := a.read("originals/" + entry.SourceID + "/" + record.Path)
			if err != nil || Verify(data, record.Bytes, record.SHA256) != nil {
				t.Fatalf("curated original lost: %s/%s", entry.ID, record.ID)
			}
			count++
		}
	}
	if count != 498 {
		t.Fatalf("curated source record count: %d", count)
	}
}

func TestNativeSchedulesPreserveSourceInstructions(t *testing.T) {
	a, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range a.entries {
		if item.SourceID != "picopak-library" {
			continue
		}
		detail, err := a.Detail(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		data, _, err := a.Original(item.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		var source struct {
			Name  string
			Steps []struct {
				Name                        string
				Location, Temp, Time, Drain int
			}
		}
		if err := json.Unmarshal(data, &source); err != nil {
			t.Fatal(err)
		}
		program := detail.NativeProgram
		if program == nil || program.Name != source.Name || len(program.Steps) != len(source.Steps) {
			t.Fatalf("native schedule replaced: %s", item.ID)
		}
		for i, step := range source.Steps {
			got := program.Steps[i]
			if step.Name != got.Name || step.Location != got.Location || step.Temp != got.TargetF || step.Time != got.Minutes || step.Drain != got.DrainMinutes {
				t.Fatalf("native step changed: %s step %d", item.ID, i)
			}
		}
	}
}

func TestSearchPaginationAndIndependentResults(t *testing.T) {
	a, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	first := a.List(Query{Limit: 2})
	next := a.List(Query{Offset: 2, Limit: 2})
	if first.Total != 5216 || len(first.Items) != 2 || first.Items[0].ID == next.Items[0].ID || !first.Items[0].Featured {
		t.Fatalf("bad pages: %+v %+v", first, next)
	}
	for _, query := range []Query{{Query: "DEAD GUY"}, {Source: "public-recipes", Kind: "recipe", Model: "zymatic"}, {Kind: "maintenance", Model: "zymatic"}} {
		page := a.List(query)
		if page.Total == 0 {
			t.Fatalf("no matches for %+v", query)
		}
		for _, item := range page.Items {
			if query.Kind != "" && item.Kind != query.Kind || query.Source != "" && item.SourceID != query.Source {
				t.Fatalf("filter mismatch: %+v", item)
			}
		}
	}
	if a.List(Query{Kind: "maintenance", Model: "pico-pro"}).Total != 0 {
		t.Fatal("Zymatic maintenance advertised for Pico")
	}
	first.Items[0].Models[0] = "corrupt"
	first.Sources[1].ExcludedReasons["corrupt"] = 1
	again := a.List(Query{Limit: 2})
	if again.Items[0].Models[0] == "corrupt" || again.Sources[1].ExcludedReasons["corrupt"] != 0 {
		t.Fatal("caller mutated immutable metadata")
	}
	detail, _ := a.Detail("picopak-64")
	detail.Recipe.NativeProgram.Steps[0].Minutes = 17
	fresh, _ := a.Detail("picopak-64")
	if fresh.Recipe.NativeProgram.Steps[0].Minutes == 17 {
		t.Fatal("caller mutated source program")
	}
	if _, _, err := a.Original("picopak-64", "../../manifest.json"); !errors.Is(err, ErrNotFound) {
		t.Fatal("arbitrary source path accepted", err)
	}
	if _, _, err := a.AssetData("../manifest.json"); !errors.Is(err, ErrNotFound) {
		t.Fatal("arbitrary asset path accepted", err)
	}
	if _, err := a.Detail("../../manifest"); !errors.Is(err, ErrNotFound) {
		t.Fatal("arbitrary detail path accepted", err)
	}
	for _, item := range a.List(Query{Source: "public-recipes", Limit: 100}).Items {
		if item.ImageURL != "" && !strings.HasPrefix(item.ImageURL, "/api/v1/catalog/assets/") {
			t.Fatal("remote image dependency", item.ID)
		}
	}
}

func TestMarketplaceFactsMatchOriginalProductAndPreserveRecipe(t *testing.T) {
	a, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	for id, product := range a.marketplace {
		d, err := a.Detail(id)
		if err != nil {
			t.Fatal(err)
		}
		if product.ProductID == 50 || d.Marketplace == nil || !strings.HasPrefix(product.URL, "https://picobrew.com/brewmarketplace/BeerDetails.cshtml?Beer=") {
			t.Fatal(id, product)
		}
		for _, r := range d.SourceRecords {
			b, _, err := a.Original(id, r.ID)
			var raw struct{ PicoBeerID int }
			if err != nil || json.Unmarshal(b, &raw) != nil || raw.PicoBeerID != product.ProductID {
				t.Fatal(id, r.ID)
			}
		}
	}
	d, err := a.Detail("picopak-46")
	if err != nil || d.Style != "Helles" || d.Recipe.Style != d.Style || d.Marketplace.Hops != "Crystal" || len(d.Recipe.Ingredients) != 0 || d.Recipe.NativeProgram == nil {
		t.Fatalf("Buffalo Craft Lager: %v %+v", err, d.Marketplace)
	}
	p := a.List(Query{Query: "Buffalo Craft Lager Crystal", Kind: "brewing", Style: "Helles"})
	if p.Total == 0 {
		t.Fatal("product facts missing from search")
	}
}

func TestBrewReadyRecipesHaveStockProgramAndMeasuredBrewSheet(t *testing.T) {
	a, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	ready := a.List(Query{Kind: "brew-ready"})
	if ready.Total != len(a.sheets) || ready.Total == 0 || a.List(Query{Kind: "brew-ready", Model: "pico-s"}).Total != 0 {
		t.Fatalf("invalid qualified catalog counts: %d", ready.Total)
	}
	var stock = recipe.Profiles()[0]
	for _, profile := range recipe.Profiles() {
		if profile.ID == "zymatic" {
			stock = profile
		}
	}
	for id := range a.sheets {
		entry, err := a.Detail(id)
		if err != nil {
			t.Fatal(err)
		}
		if !entry.BrewReady || entry.BrewSheet == nil || entry.StockProgram == nil || len(entry.BrewSheet.Instructions) < 5 {
			t.Fatal(id)
		}
		if err := recipe.ValidateCapacity(*entry.Recipe, stock); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		copy := entry.StockRecipe()
		compiled, err := recipe.StockZymaticProgram(copy, stock)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if len(compiled.Steps) != len(entry.StockProgram.Steps) || !strings.Contains(copy.Notes, entry.BrewSheet.YeastGuide) {
			t.Fatal(id)
		}
		for i, s := range compiled.Steps {
			original := entry.StockProgram.Steps[i]
			if s.Minutes != original.Minutes || s.Location != original.Location || s.TargetF != original.TargetF || s.DrainMinutes != original.DrainMinutes {
				t.Fatalf("%s step %d changed", id, i+1)
			}
		}
	}
}

func TestAuthorPublishedKitEditions(t *testing.T) {
	a, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	hits := a.List(Query{Kind: "recipe", Query: "picopak", Limit: 100})
	if len(a.kitEditions) != 12 {
		t.Fatal("missing reviewed kit editions")
	}
	for id, product := range a.kitEditions {
		d, err := a.Detail(id)
		if err != nil || d.Marketplace == nil || d.Recipe == nil || len(d.Recipe.Ingredients) == 0 || d.Recipe.BatchLiters <= 0 {
			t.Fatalf("missing measured edition for %s: %v", id, err)
		}
		author := map[string]string{"PicoBrew": "PicoBrew Inc", "Lucky Envelope Brewing": "Lucky Envelope"}[product.Creator]
		if author == "" {
			author = product.Creator
		}
		if author == "" || author != d.Author {
			t.Fatalf("creator mismatch for %s", id)
		}
		found := false
		for _, hit := range hits.Items {
			if hit.ID == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("recipe search missed %s", id)
		}
	}
	for id, p := range a.marketplace {
		if p.IngredientRecipeID == "" {
			continue
		}
		d, err := a.Detail(id)
		if err != nil || d.Recipe == nil || len(d.Recipe.Ingredients) != 0 || d.Recipe.BatchLiters != 0 {
			t.Fatalf("kit bill changed %s", id)
		}
	}
}

func TestEveryIngredientRecipeHasFiveMachineVersions(t *testing.T) {
	a, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	profiles := recipe.Profiles()
	counts := map[string]int{}
	measured, editions := 0, 0
	for _, entry := range a.entries {
		if entry.Kind != "recipe" {
			continue
		}
		d, err := a.Detail(entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		source := *d.Recipe
		source.ID = d.ID
		if len(source.Ingredients) == 0 {
			t.Fatalf("%s has no ingredients", d.ID)
		}
		snapshot, _ := json.Marshal(source)
		measured++
		allModels := true
		for _, p := range profiles {
			plan, err := recipe.Translate(source, p, 0)
			if err != nil || plan.Recipe.BatchLiters <= 0 || plan.Recipe.SourceModel != p.ID || len(plan.Recipe.Ingredients) != len(source.Ingredients) {
				t.Fatalf("%s/%s: invalid plan: %v", d.ID, p.ID, err)
			}
			ratio := plan.Recipe.BatchLiters / source.BatchLiters
			for i, ingredient := range source.Ingredients {
				if math.Abs(plan.Recipe.Ingredients[i].Amount/ingredient.Amount-ratio) > 1e-9*ratio {
					t.Fatalf("%s/%s changed the proportion of %s", d.ID, p.ID, ingredient.Name)
				}
			}
			if plan.MachineRecipe == nil {
				allModels = false
				if len(plan.DeploymentBlockers) == 0 || plan.CanDeploy {
					t.Fatal("missing blocker", d.ID, p.ID)
				}
				continue
			}
			if plan.Recipe.SourceYeast == "" {
				t.Fatalf("%s/%s missing source yeast", d.ID, p.ID)
			}
			var basketKG float64
			slotGrams := map[int]float64{}
			for _, in := range plan.Recipe.Ingredients {
				if in.Use == "mash" {
					switch in.Unit {
					case "kg":
						basketKG += in.Amount
					case "g":
						basketKG += in.Amount / 1000
					}
				}
				if in.SlotAddition || in.Kind == "hop" && (in.Use == "boil" || in.Use == "first-wort") {
					slot := plan.HopSlotsByMinute[int(in.Minutes)]
					if slot < 1 || slot > p.HopSlots {
						t.Fatalf("%s/%s unassigned timed addition: %+v", d.ID, p.ID, in)
					}
					slotGrams[slot] += in.Amount
				}
			}
			if basketKG > p.MaxGrainKG+1e-8 {
				t.Fatalf("%s/%s overfilled basket: %g kg", d.ID, p.ID, basketKG)
			}
			for slot, g := range slotGrams {
				if g > p.MaxHopSlotG+1e-8 {
					t.Fatalf("%s/%s overfilled slot %d: %g g", d.ID, p.ID, slot, g)
				}
			}
			if p.ID == "zymatic" {
				if len(recipe.StockZymaticRecord(*plan.MachineRecipe)) > 511 || plan.CanDeploy {
					t.Fatal("invalid stock record", d.ID)
				}
			} else if _, err := recipe.NativeJSON(*plan.MachineRecipe); err != nil || !plan.CanDeploy {
				t.Fatal(d.ID, p.ID, err)
			}
			counts[p.ID]++
		}
		if d.Edition != "" {
			editions++
			if !allModels || d.SourceReview == nil || d.SourceReview.Evidence == "" || d.SourceReview.Blocker != "" || entry.Edition != d.Edition {
				t.Fatal("published adaptation lacks a verified program or its assumptions", entry.ID)
			}
		}
		if entry.allBrewers != allModels {
			t.Fatal("incorrect catalog eligibility", entry.ID)
		}
		after, _ := json.Marshal(source)
		if string(after) != string(snapshot) {
			t.Fatal("source mutated", d.ID)
		}
	}
	for _, profile := range profiles {
		shared := a.List(Query{Kind: "all-brewers", Model: profile.ID})
		if shared.Total != 975 || counts[profile.ID] != shared.Total {
			t.Fatalf("%s: %d shared recipes, %d working programs", profile.ID, shared.Total, counts[profile.ID])
		}
	}
	if measured != 1316 || editions != 127 || len(counts) != 5 {
		t.Fatal(measured, editions, counts)
	}
	t.Logf("%d measured recipes, compiled schedules per model: %v", measured, counts)
}
