package catalog

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
)

//go:embed data/catalog.zip
var bundled []byte

//go:embed data/brew-sheets.json
var brewSheetData []byte

//go:embed data/recovered-recipes.json
var recoveryData []byte

//go:embed data/marketplace.json
var marketplaceData []byte

var (
	ErrNotFound   = errors.New("catalog: not found")
	ErrNotBundled = errors.New("catalog: asset is an optional reference")
	archiveOnce   sync.Once
	archive       *Archive
	archiveError  error
	identifier    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

const MaxFileBytes = 32 << 20

type manifest struct {
	Version    int         `json:"version"`
	EntryCount int         `json:"entry_count"`
	Sources    []Source    `json:"sources"`
	Assets     []Asset     `json:"assets"`
	References []Reference `json:"references"`
}

type indexed struct {
	Summary
	Search     string `json:"search"`
	allBrewers bool
}

// Archive is an immutable index over the embedded, pinned source snapshot.
// Details and raw files are decompressed only when requested; nothing is
// fetched from upstream and no records are automatically added to a library.
type Archive struct {
	recovered   map[string]recipeRecovery
	sheets      map[string]BrewSheet
	marketplace map[string]MarketplaceIngredients
	kitEditions map[string]MarketplaceIngredients
	files       map[string]*zip.File
	entries     []indexed
	byID        map[string]Summary
	assets      map[string]Asset
	meta        manifest
	styles      []string
}

func Open() (*Archive, error) {
	archiveOnce.Do(func() { archive, archiveError = openBytes(bundled) })
	return archive, archiveError
}

func validPath(name string) bool {
	return fs.ValidPath(name) && name != "." && !strings.ContainsAny(name, "\\\x00")
}

func openBytes(data []byte) (*Archive, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("catalog: open bundle: %w", err)
	}
	a := &Archive{files: make(map[string]*zip.File, len(z.File)), byID: map[string]Summary{}, assets: map[string]Asset{}}
	for _, file := range z.File {
		if !validPath(file.Name) || !file.Mode().IsRegular() || file.UncompressedSize64 > MaxFileBytes {
			return nil, fmt.Errorf("catalog: unsupported archive file %q", file.Name)
		}
		if _, exists := a.files[file.Name]; exists {
			return nil, fmt.Errorf("catalog: duplicate file %q", file.Name)
		}
		a.files[file.Name] = file
	}
	if err := a.decode("manifest.json", &a.meta); err != nil {
		return nil, err
	}
	if a.meta.Version != 1 {
		return nil, fmt.Errorf("catalog: unsupported manifest version %d", a.meta.Version)
	}
	if err := a.decode("index.json", &a.entries); err != nil {
		return nil, err
	}
	if a.meta.EntryCount != len(a.entries) || a.meta.EntryCount < 1 {
		return nil, errors.New("catalog: invalid entry count")
	}
	sources := map[string]Source{}
	for _, source := range a.meta.Sources {
		if !identifier.MatchString(source.ID) || sources[source.ID].ID != "" || len(source.Commit) != 40 || source.EntryCount < 0 || source.RecordCount != source.ValidCount+source.ExcludedCount {
			return nil, fmt.Errorf("catalog: invalid source %q", source.ID)
		}
		sources[source.ID] = source
	}
	var products []struct {
		MarketplaceIngredients
		CatalogIDs []string `json:"catalog_ids"`
	}
	if err := json.Unmarshal(marketplaceData, &products); err != nil {
		return nil, err
	}
	a.marketplace = make(map[string]MarketplaceIngredients)
	a.kitEditions = make(map[string]MarketplaceIngredients)
	for _, product := range products {
		if product.IngredientRecipeID != "" {
			a.kitEditions[product.IngredientRecipeID] = product.MarketplaceIngredients
		}
		for _, id := range product.CatalogIDs {
			a.marketplace[id] = product.MarketplaceIngredients
		}
	}
	if err := json.Unmarshal(recoveryData, &a.recovered); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(brewSheetData, &a.sheets); err != nil {
		return nil, err
	}
	styles := map[string]bool{}
	counts := map[string]int{}
	for i := range a.entries {
		entry := &a.entries[i]
		if !identifier.MatchString(entry.ID) || a.byID[entry.ID].ID != "" || sources[entry.SourceID].ID == "" || a.files["entries/"+entry.ID+".json"] == nil {
			return nil, fmt.Errorf("catalog: invalid entry %q", entry.ID)
		}
		if entry.Kind != "recipe" && entry.Kind != "native-program" && entry.Kind != "maintenance" {
			return nil, fmt.Errorf("catalog: invalid kind for %q", entry.ID)
		}
		if product, ok := a.marketplace[entry.ID]; ok {
			if entry.SourceID != "picopak-library" || !strings.EqualFold(strings.Join(strings.Fields(entry.Name), " "), strings.Join(strings.Fields(product.Name), " ")) {
				return nil, fmt.Errorf("catalog: unexpected marketplace product for %s", entry.ID)
			}
			if entry.Style == "" {
				entry.Style = product.Style
			}
			entry.Search += " " + strings.ToLower(product.Style+" "+product.Grains+" "+product.Hops)
		}
		if product, ok := a.kitEditions[entry.ID]; ok {
			if entry.SourceID != "public-recipes" || entry.Kind != "recipe" || !strings.EqualFold(entry.Name, product.Name) {
				return nil, fmt.Errorf("catalog: mismatched ingredient recipe for %s", entry.ID)
			}
			entry.Search += " picopak " + strings.ToLower(product.Creator)
		}
		entry.Edition = a.recovered[entry.ID].Edition
		if entry.Edition != "" {
			entry.Search += " " + strings.ToLower(entry.Edition)
		}
		_, hasStockSheet := a.sheets[entry.ID]
		entry.BrewReady = hasStockSheet && entry.Edition == "" && a.recovered[entry.ID].Review == ""
		a.byID[entry.ID] = entry.Summary
		counts[entry.SourceID]++
		if entry.Style != "" {
			styles[entry.Style] = true
		}
	}
	// Do the compatibility check once per bundled recipe, before pagination or search.
	profiles := recipe.Profiles()
	for i := range a.entries {
		entry := &a.entries[i]
		if entry.Kind != "recipe" {
			continue
		}
		detail, err := a.Detail(entry.ID)
		if err != nil {
			return nil, err
		}
		if detail.Recipe == nil || len(detail.Recipe.Ingredients) == 0 {
			continue
		}
		source := *detail.Recipe
		source.ID = entry.ID
		entry.allBrewers = true
		for _, profile := range profiles {
			plan, err := recipe.Translate(source, profile, 0)
			if err != nil || plan.MachineRecipe == nil || len(plan.DeploymentBlockers) != 0 {
				entry.allBrewers = false
				break
			}
		}
	}
	for id, source := range sources {
		if counts[id] != source.EntryCount {
			return nil, fmt.Errorf("catalog: source entry count mismatch: %s", id)
		}
	}
	for _, asset := range a.meta.Assets {
		if !validPath(asset.Path) || a.assets[asset.Path].Path != "" || sources[asset.SourceID].ID == "" || !validDigest(asset.SHA256) || asset.Bytes <= 0 || asset.Bytes > MaxFileBytes {
			return nil, fmt.Errorf("catalog: invalid asset %q", asset.Path)
		}
		if asset.Bundled && a.files["assets/"+asset.Path] == nil {
			return nil, fmt.Errorf("catalog: missing asset %q", asset.Path)
		}
		a.assets[asset.Path] = asset
	}
	for _, ref := range a.meta.References {
		asset, ok := a.assets[ref.AssetPath]
		if !ok || ref.SHA256 != asset.SHA256 || ref.Bundled != asset.Bundled {
			return nil, fmt.Errorf("catalog: invalid reference %q", ref.ID)
		}
	}
	for style := range styles {
		a.styles = append(a.styles, style)
	}
	sort.Slice(a.styles, func(i, j int) bool { return strings.ToLower(a.styles[i]) < strings.ToLower(a.styles[j]) })
	return a, nil
}

func validDigest(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == sha256.Size
}

func Verify(data []byte, size int64, digest string) error {
	if int64(len(data)) != size || !validDigest(digest) {
		return errors.New("catalog: source size mismatch")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return errors.New("catalog: source checksum mismatch")
	}
	return nil
}

func (a *Archive) read(name string) ([]byte, error) {
	file := a.files[name]
	if file == nil {
		return nil, ErrNotFound
	}
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, errors.New("catalog: file exceeds limit")
	}
	return data, nil
}

func (a *Archive) decode(name string, value any) error {
	data, err := a.read(name)
	if err != nil {
		return fmt.Errorf("catalog: read %s: %w", name, err)
	}
	if err = json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("catalog: decode %s: %w", name, err)
	}
	return nil
}

func copySummary(v Summary) Summary {
	v.Models = slices.Clone(v.Models)
	v.DeployModels = slices.Clone(v.DeployModels)
	if v.ABV != nil {
		value := *v.ABV
		v.ABV = &value
	}
	if v.IBU != nil {
		value := *v.IBU
		v.IBU = &value
	}
	if v.SRM != nil {
		value := *v.SRM
		v.SRM = &value
	}
	return v
}

func (a *Archive) Sources() []Source {
	out := slices.Clone(a.meta.Sources)
	for i := range out {
		out[i].ExcludedReasons = maps.Clone(out[i].ExcludedReasons)
	}
	return out
}

// List retains the archive's deterministic featured/name/ID ordering. Search
// terms match case-insensitively across labels, authors and known ingredients.
func (a *Archive) List(q Query) Page {
	if q.Limit <= 0 {
		q.Limit = 24
	}
	if q.Limit > 100 {
		q.Limit = 100
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	result := Page{Items: []Summary{}, Offset: q.Offset, Limit: q.Limit, Sources: a.Sources(), Styles: slices.Clone(a.styles)}
	if q.Kind == "all-brewers" && q.Model != "" && !slices.ContainsFunc(recipe.Profiles(), func(p domain.Profile) bool { return p.ID == q.Model }) {
		return result
	}
	terms := strings.Fields(strings.ToLower(q.Query))
	for _, entry := range a.entries {
		if q.Kind == "all-brewers" {
			if !entry.allBrewers {
				continue
			}
		} else if q.Kind == "reviewed" {
			if a.recovered[entry.ID].Evidence == "" {
				continue
			}
		} else if q.Kind == "brew-ready" {
			if !entry.BrewReady || q.Model != "" && q.Model != "zymatic" {
				continue
			}
		} else if q.Kind == "brewing" && entry.Kind == "maintenance" || q.Kind != "" && q.Kind != "brewing" && q.Kind != entry.Kind {
			continue
		}
		if q.Source != "" && q.Source != entry.SourceID || q.Model != "" && q.Kind != "all-brewers" && !slices.Contains(entry.Models, q.Model) || q.Style != "" && q.Style != entry.Style {
			continue
		}
		match := true
		for _, term := range terms {
			if !strings.Contains(entry.Search, term) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if result.Total >= q.Offset && len(result.Items) < q.Limit {
			result.Items = append(result.Items, copySummary(entry.Summary))
		}
		result.Total++
	}
	return result
}

func (a *Archive) Detail(id string) (Detail, error) {
	if a.byID[id].ID == "" {
		return Detail{}, ErrNotFound
	}
	var result Detail
	if err := a.decode("entries/"+id+".json", &result); err != nil {
		return Detail{}, err
	}
	if result.ID != id || result.Provenance.CatalogID != id || result.Provenance.SourceID != result.SourceID || len(result.SourceRecords) == 0 || result.SourceRecordCount != len(result.SourceRecords) {
		return Detail{}, fmt.Errorf("catalog: invalid detail %q", id)
	}
	if result.Recipe != nil {
		result.Recipe.SourceYeast = recipe.SourceYeast(*result.Recipe)
		if result.SourceID == "public-recipes" {
			preserveFinalFermentationTarget(&result, a.recovered[id].Fermentation)
		}
	}
	if fix, ok := a.recovered[id]; ok {
		if err := fix.apply(&result); err != nil {
			return Detail{}, err
		}
		if fix.Evidence != "" {
			result.SourceReview = &SourceReview{Evidence: fix.Evidence, Blocker: fix.Review}
		}
	}
	if sheet, ok := a.sheets[id]; ok && result.Edition == "" {
		if sheet.SourceSHA256 != result.SourceRecords[0].SHA256 || result.SourceID != "public-recipes" || result.Recipe == nil {
			return Detail{}, fmt.Errorf("catalog: brew sheet source mismatch for %s", id)
		}
		sheet.Instructions = slices.Clone(sheet.Instructions)
		sheet.NoteIngredients = slices.Clone(sheet.NoteIngredients)
		result.Recipe.Ingredients = append(result.Recipe.Ingredients, sheet.NoteIngredients...)
		program, err := publicStockProgram(result)
		if err != nil {
			return Detail{}, err
		}
		result.BrewSheet = &sheet
		result.BrewReady = a.byID[id].BrewReady
		if result.BrewReady {
			result.StockProgram = program
		}
	}
	if product, ok := a.marketplace[id]; ok {
		result.Marketplace = &product
		result.Style = a.byID[id].Style
		if result.Recipe != nil && result.Recipe.Style == "" {
			result.Recipe.Style = result.Style
		}
	}
	if result.SourceID == "picopak-library" && result.SourceModel == "pico" && result.NativeProgram != nil && result.Recipe != nil && result.Recipe.NativeProgram != nil {
		data, _, err := a.originalRecord(result, "")
		if err != nil {
			return Detail{}, err
		}
		source, err := recipe.ImportPicoPak(data)
		if err != nil {
			return Detail{}, fmt.Errorf("catalog PicoPak %s: %w", id, err)
		}
		p := source.NativeProgram
		if p.Name != result.NativeProgram.Name || !slices.Equal(p.Steps, result.NativeProgram.Steps) || !slices.Equal(p.Steps, result.Recipe.NativeProgram.Steps) {
			return Detail{}, fmt.Errorf("catalog PicoPak %s differs from the original schedule", id)
		}
		result.NativeProgram.ZPak = p.ZPak
		result.Recipe.NativeProgram.ZPak = p.ZPak
	}
	if product, ok := a.kitEditions[id]; ok {
		result.Marketplace = &product
	}
	return result, nil
}

// Original allows only the original files listed on the selected entry. Caller
// supplied paths never reach the archive reader.
func (a *Archive) Original(id, recordID string) ([]byte, SourceRecord, error) {
	detail, err := a.Detail(id)
	if err != nil {
		return nil, SourceRecord{}, err
	}
	return a.originalRecord(detail, recordID)
}

func (a *Archive) originalRecord(detail Detail, recordID string) ([]byte, SourceRecord, error) {
	if recordID == "" {
		recordID = detail.SourceRecords[0].ID
	}
	for _, record := range detail.SourceRecords {
		if record.ID != recordID {
			continue
		}
		if !validPath(record.Path) {
			return nil, SourceRecord{}, errors.New("catalog: invalid source path")
		}
		data, err := a.read(path.Join("originals", detail.SourceID, record.Path))
		if err != nil {
			return nil, SourceRecord{}, err
		}
		if err = Verify(data, record.Bytes, record.SHA256); err != nil {
			return nil, SourceRecord{}, err
		}
		return data, record, nil
	}
	return nil, SourceRecord{}, ErrNotFound
}

func (a *Archive) Asset(name string) (Asset, bool) {
	value, ok := a.assets[name]
	value.Models = slices.Clone(value.Models)
	return value, ok
}

func (a *Archive) AssetData(name string) ([]byte, Asset, error) {
	asset, ok := a.Asset(name)
	if !ok {
		return nil, Asset{}, ErrNotFound
	}
	if !asset.Bundled {
		return nil, asset, ErrNotBundled
	}
	data, err := a.read("assets/" + asset.Path)
	if err != nil {
		return nil, asset, err
	}
	if err = Verify(data, asset.Bytes, asset.SHA256); err != nil {
		return nil, asset, err
	}
	return data, asset, nil
}

func (a *Archive) References(model string) References {
	result := References{Items: []Reference{}, Assets: []Asset{}}
	for _, ref := range a.meta.References {
		if model != "" && len(ref.Models) > 0 && !slices.Contains(ref.Models, model) {
			continue
		}
		ref.Models = slices.Clone(ref.Models)
		result.Items = append(result.Items, ref)
	}
	for _, asset := range a.meta.Assets {
		if model != "" && len(asset.Models) > 0 && !slices.Contains(asset.Models, model) {
			continue
		}
		asset.Models = slices.Clone(asset.Models)
		result.Assets = append(result.Assets, asset)
	}
	return result
}
