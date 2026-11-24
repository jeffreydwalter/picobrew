package firmware

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,30}$`)
)

const (
	imageBase = uint64(0x20004000)
	ramEnd    = uint64(0x20040000)
)

type imageLayout struct {
	Passed              *bool             `json:"passed"`
	LoadAddress         string            `json:"load_address"`
	RAMEnd              string            `json:"ram_end"`
	InitialStackPointer string            `json:"initial_stack_pointer"`
	ResetVector         string            `json:"reset_vector"`
	Entry               string            `json:"entry"`
	BinaryBytes         int               `json:"binary_bytes"`
	BSSBytes            int               `json:"bss_bytes"`
	HeapBytes           int               `json:"heap_bytes"`
	SDKStackBytes       int               `json:"sdk_stack_bytes"`
	UnallocatedRAMBytes int               `json:"unallocated_ram_bytes"`
	LoadSegments        []loadSegment     `json:"load_segments"`
	RequiredSymbols     map[string]string `json:"required_entry_symbols"`
	UndefinedSymbols    *[]string         `json:"undefined_symbols"`
	TestSymbols         *[]string         `json:"test_symbols"`
}

type loadSegment struct {
	Start       string `json:"start"`
	MemoryBytes int    `json:"memory_bytes"`
	FileBytes   int    `json:"file_bytes"`
}

type report struct {
	HubOTAProtocol  int    `json:"hub_ota_protocol"`
	Profile         string `json:"profile"`
	Model           string `json:"model"`
	ModelFamily     string `json:"model_family"`
	DisplayVariant  string `json:"display_variant"`
	FirmwareUpdate  *bool  `json:"firmware_update"`
	DisplayGeometry struct {
		Width         int  `json:"width"`
		Height        int  `json:"height"`
		Rotation      *int `json:"rotation"`
		StorageBytes  int  `json:"storage_bytes"`
		TransferBytes int  `json:"transfer_bytes"`
	} `json:"display_geometry"`
	FirmwareVersion       string          `json:"firmware_version"`
	BinarySHA256          string          `json:"binary_sha256"`
	BrowserManifestSHA256 string          `json:"browser_manifest_sha256"`
	BrowserStorage        json.RawMessage `json:"browser_storage"`
	SDKLinkPassed         *bool           `json:"sdk_link_passed"`
	APIKeyEmbedded        *bool           `json:"api_key_embedded"`
	ImageLayout           imageLayout     `json:"image_layout"`
	Files                 struct {
		Binary string `json:"binary"`
	} `json:"files"`
}

type webManifest struct {
	SchemaVersion   int        `json:"schema_version"`
	BlockSize       int        `json:"block_size"`
	MetadataPerFile int        `json:"metadata_per_file"`
	FailSafe        *bool      `json:"fail_safe"`
	Assets          []webAsset `json:"assets"`
}

type webAsset struct {
	Source           string `json:"source"`
	FilePath         string `json:"file_path"`
	Payload          string `json:"payload"`
	ContentType      string `json:"content_type"`
	ContentEncoding  string `json:"content_encoding"`
	Length           int    `json:"length"`
	MaxSize          int    `json:"max_size"`
	AllocationBlocks int    `json:"allocation_blocks"`
	SHA256           string `json:"sha256"`
	SourceLength     int    `json:"source_length"`
	SourceSHA256     string `json:"source_sha256"`
}

var webFiles = []struct{ source, destination, contentType string }{
	{"index.html", "/local/web/i.gz", "text/html; charset=utf-8"},
	{"app.js", "/local/web/a.gz", "application/javascript; charset=utf-8"},
	{"api-explorer.js", "/local/web/e.gz", "application/javascript; charset=utf-8"},
	{"history.js", "/local/web/h.gz", "application/javascript; charset=utf-8"},
	{"style.css", "/local/web/s.gz", "text/css; charset=utf-8"},
}

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ValidID(value string) bool     { return digestPattern.MatchString(value) }
func ValidCommit(value string) bool { return commitPattern.MatchString(value) }
func ValidModel(value string) bool {
	return value == "pico-s" || value == "pico-pro" || value == "pico-c"
}

func ValidVariant(model, variant string) bool {
	if model == "pico-c" {
		return variant == "ssd1325" || variant == "ssd1309"
	}
	return ValidModel(model) && variant == ""
}

func modelFamily(model string) string {
	if model == "pico-c" {
		return "pico-c"
	}
	return "pico-s-pro"
}

// checkJSON rejects duplicate object members and excessive nesting before the
// typed decoder is used. This prevents different consumers from interpreting
// the same package's security-relevant fields differently.
func checkJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 32 {
			return errors.New("JSON nesting limit exceeded")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			keys := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				folded := strings.ToLower(name)
				if !ok || keys[folded] {
					return errors.New("duplicate JSON field")
				}
				keys[folded] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			token, err = d.Token()
			if err != nil || token != json.Delim('}') {
				return errors.New("invalid JSON object")
			}
		case json.Delim('['):
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			token, err = d.Token()
			if err != nil || token != json.Delim(']') {
				return errors.New("invalid JSON array")
			}
		case json.Delim('}'), json.Delim(']'):
			return errors.New("invalid JSON delimiter")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

func decodeJSON(data []byte, dst any, strict bool) error {
	if err := checkJSON(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if strict {
		d.DisallowUnknownFields()
	}
	return d.Decode(dst)
}

func parseAddress(value string) (uint64, bool) {
	if len(value) < 3 || len(value) > 10 || !strings.HasPrefix(value, "0x") {
		return 0, false
	}
	n, err := strconv.ParseUint(value[2:], 16, 32)
	return n, err == nil
}

func validateLayout(v imageLayout, image []byte) error {
	bad := func() error { return invalid("The firmware image layout or entry vectors are invalid") }
	if v.Passed == nil || !*v.Passed || v.BinaryBytes < 16 || v.BinaryBytes > MaxBinaryBytes ||
		v.UndefinedSymbols == nil || len(*v.UndefinedSymbols) != 0 || v.TestSymbols == nil || len(*v.TestSymbols) != 0 {
		return bad()
	}
	base, baseOK := parseAddress(v.LoadAddress)
	end, endOK := parseAddress(v.RAMEnd)
	stack, stackOK := parseAddress(v.InitialStackPointer)
	reset, resetOK := parseAddress(v.ResetVector)
	entry, entryOK := parseAddress(v.Entry)
	imageEnd := imageBase + uint64(v.BinaryBytes)
	if !baseOK || base != imageBase || !endOK || end != ramEnd ||
		!stackOK || stack < imageEnd+8 || stack > ramEnd || stack%8 != 0 ||
		!resetOK || reset&1 != 1 || reset&^1 < imageBase+8 || reset&^1 > imageEnd-2 ||
		!entryOK || entry != reset {
		return bad()
	}
	if image != nil && (len(image) != v.BinaryBytes ||
		uint64(binary.LittleEndian.Uint32(image[:4])) != stack ||
		uint64(binary.LittleEndian.Uint32(image[4:8])) != reset) {
		return bad()
	}
	for _, n := range []int{v.BSSBytes, v.HeapBytes, v.SDKStackBytes, v.UnallocatedRAMBytes} {
		if n < 0 || uint64(n) > ramEnd-imageBase {
			return bad()
		}
	}
	type span struct{ start, end uint64 }
	spans := make([]span, 0, len(v.LoadSegments))
	var lastFileEnd uint64
	if len(v.LoadSegments) == 0 || len(v.LoadSegments) > 16 {
		return bad()
	}
	for _, segment := range v.LoadSegments {
		start, ok := parseAddress(segment.Start)
		if !ok || start < imageBase || start >= ramEnd || segment.FileBytes < 0 ||
			segment.MemoryBytes <= 0 || segment.MemoryBytes < segment.FileBytes ||
			uint64(segment.MemoryBytes) > ramEnd-start || uint64(segment.FileBytes) > imageEnd-imageBase ||
			start+uint64(segment.FileBytes) > imageEnd {
			return bad()
		}
		spans = append(spans, span{start, start + uint64(segment.MemoryBytes)})
		lastFileEnd = max(lastFileEnd, start+uint64(segment.FileBytes))
	}
	if lastFileEnd != imageEnd {
		return bad()
	}
	slices.SortFunc(spans, func(a, b span) int {
		if a.start < b.start {
			return -1
		}
		if a.start > b.start {
			return 1
		}
		return 0
	})
	if spans[0].start != imageBase {
		return bad()
	}
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return bad()
		}
	}
	for name, expected := range map[string]uint64{"ResetISR": reset, "g_pfnVectors": imageBase} {
		if text, exists := v.RequiredSymbols[name]; exists {
			address, ok := parseAddress(text)
			if !ok || address != expected {
				return bad()
			}
		}
	}
	return nil
}

func parseReport(data []byte) (report, error) {
	var r report
	if len(data) == 0 || len(data) > MaxReportBytes || decodeJSON(data, &r, false) != nil {
		return r, invalid("The firmware report is missing, oversized or invalid JSON")
	}
	if !ValidModel(r.Model) || !ValidVariant(r.Model, r.DisplayVariant) || r.ModelFamily != modelFamily(r.Model) || r.Profile != "standalone" ||
		!versionPattern.MatchString(r.FirmwareVersion) || !ValidID(r.BinarySHA256) ||
		!ValidID(r.BrowserManifestSHA256) || r.Files.Binary != r.Model+"-standalone.bin" ||
		r.SDKLinkPassed == nil || !*r.SDKLinkPassed || r.APIKeyEmbedded == nil || *r.APIKeyEmbedded {
		return r, invalid("The report must describe a supported C/S/Pro standalone image with an explicit C display variant and no embedded API key")
	}
	if r.HubOTAProtocol != 0 && r.HubOTAProtocol != 1 {
		return r, invalid("The package specifies an unsupported wireless update protocol")
	}
	if r.Model == "pico-c" {
		height, rotation := 64, 2
		if r.DisplayVariant == "ssd1309" {
			height, rotation = 32, 0
		}
		g := r.DisplayGeometry
		if g.Width != 128 || g.Height != height || g.Rotation == nil || *g.Rotation != rotation ||
			g.StorageBytes != 1024 || g.TransferBytes != 1024 || r.FirmwareUpdate == nil || *r.FirmwareUpdate {
			return r, invalid("The C display geometry or installer capability is inconsistent with this package")
		}
	}
	if err := validateLayout(r.ImageLayout, nil); err != nil {
		return r, err
	}
	if _, err := parseManifest(r.BrowserStorage); err != nil {
		return r, invalid("The firmware report's browser storage description is invalid")
	}
	return r, nil
}

func candidate(r report) Candidate {
	return Candidate{HubOTAProtocol: r.HubOTAProtocol, Model: r.Model, ModelFamily: r.ModelFamily, DisplayVariant: r.DisplayVariant, FirmwareVersion: r.FirmwareVersion,
		BinarySHA256: r.BinarySHA256, BinaryBytes: r.ImageLayout.BinaryBytes}
}

func parseManifest(data []byte) (webManifest, error) {
	var m webManifest
	bad := func() (webManifest, error) {
		return m, invalid("The browser manifest schema, paths or allocation is invalid")
	}
	if len(data) == 0 || len(data) > MaxManifestBytes || decodeJSON(data, &m, true) != nil ||
		m.SchemaVersion != 1 || m.BlockSize != 4096 || m.MetadataPerFile != 512 ||
		m.FailSafe == nil || *m.FailSafe || len(m.Assets) != len(webFiles) {
		return bad()
	}
	for i, a := range m.Assets {
		expected := webFiles[i]
		if a.Source != expected.source || a.FilePath != expected.destination ||
			a.Payload != expected.source+".gz" || a.ContentType != expected.contentType || a.ContentEncoding != "gzip" ||
			a.Length < 1 || a.Length > MaxAssetBytes || a.SourceLength < 1 || a.SourceLength > MaxSourceBytes ||
			!ValidID(a.SHA256) || !ValidID(a.SourceSHA256) {
			return bad()
		}
		blocks := (a.Length + 512 + 4095) / 4096
		if a.AllocationBlocks != blocks || a.MaxSize != blocks*4096-512 {
			return bad()
		}
	}
	return m, nil
}

func archiveNames(model string) []string {
	names := []string{"firmware-report.json", model + "-standalone.bin", "web/manifest.json"}
	for _, f := range webFiles {
		names = append(names, "web/"+f.source+".gz")
	}
	return names
}

func fileLimit(name string) int {
	switch name {
	case "firmware-report.json":
		return MaxReportBytes
	case "pico-s-standalone.bin", "pico-pro-standalone.bin", "pico-c-standalone.bin":
		return MaxBinaryBytes
	case "web/manifest.json":
		return MaxManifestBytes
	}
	for _, f := range webFiles {
		if name == "web/"+f.source+".gz" {
			return MaxAssetBytes
		}
	}
	return 0
}

// ValidateArchive reads all eight files with independent limits and checks
// their complete hash chain. No archive path is extracted to the filesystem.
func archiveFiles(data []byte) (map[string][]byte, error) {
	if len(data) == 0 || len(data) > MaxArchiveBytes {
		return nil, invalid("Upload a firmware ZIP no larger than 2 MiB")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(z.File) != 8 {
		return nil, invalid("The firmware ZIP must contain exactly the report, image, browser manifest and five browser files")
	}
	files := make(map[string][]byte, 8)
	total := uint64(0)
	for _, f := range z.File {
		limit := fileLimit(f.Name)
		if limit == 0 || path.Clean(f.Name) != f.Name || strings.ContainsAny(f.Name, "\\:\x00") ||
			f.Flags&1 != 0 || !f.Mode().IsRegular() || files[f.Name] != nil ||
			(f.Method != zip.Store && f.Method != zip.Deflate) ||
			f.UncompressedSize64 == 0 || f.UncompressedSize64 > uint64(limit) ||
			f.UncompressedSize64 > uint64(MaxExpandedBytes)-total {
			return nil, invalid("The firmware ZIP contains an unsafe, duplicate, unexpected or oversized file")
		}
		total += f.UncompressedSize64
		source, err := f.Open()
		if err != nil {
			return nil, invalid("The firmware ZIP could not be read")
		}
		payload, readErr := io.ReadAll(io.LimitReader(source, int64(limit)+1))
		closeErr := source.Close()
		if readErr != nil || closeErr != nil || len(payload) > limit || uint64(len(payload)) != f.UncompressedSize64 {
			return nil, invalid("A firmware ZIP entry is truncated, oversized or has an invalid checksum")
		}
		files[f.Name] = payload
	}
	return files, nil
}

func ValidateArchive(data []byte) (Candidate, error) {
	var empty Candidate
	files, err := archiveFiles(data)
	if err != nil {
		return empty, err
	}
	return validateFiles(files)
}
func validateFiles(files map[string][]byte) (Candidate, error) {
	var empty Candidate
	r, err := parseReport(files["firmware-report.json"])
	if err != nil {
		return empty, err
	}
	for _, name := range archiveNames(r.Model) {
		if files[name] == nil {
			return empty, invalid("The firmware ZIP does not contain the complete package for its reported model")
		}
	}
	image := files[r.Files.Binary]
	if Digest(image) != r.BinarySHA256 || len(image) != r.ImageLayout.BinaryBytes {
		return empty, invalid("The firmware image size or SHA-256 does not match its report")
	}
	if err := validateLayout(r.ImageLayout, image); err != nil {
		return empty, err
	}
	if r.Model == "pico-c" && r.HubOTAProtocol == 1 {
		const markerOffset = 1024 // Linker reserves the first KiB for the CC3200 vectors.
		marker := []byte("PicoBrew:pico-c:" + r.DisplayVariant + "\x00")
		if len(image) < markerOffset+len(marker) || !bytes.Equal(image[markerOffset:markerOffset+len(marker)], marker) {
			return empty, invalid("The Pico C image does not match the package display controller")
		}
	}
	manifestBytes := files["web/manifest.json"]
	if Digest(manifestBytes) != r.BrowserManifestSHA256 {
		return empty, invalid("The browser manifest SHA-256 does not match the firmware report")
	}
	m, err := parseManifest(manifestBytes)
	if err != nil {
		return empty, err
	}
	expected, _ := parseManifest(r.BrowserStorage) // Already checked by parseReport.
	actualJSON, _ := json.Marshal(m)
	expectedJSON, _ := json.Marshal(expected)
	if !bytes.Equal(actualJSON, expectedJSON) {
		return empty, invalid("The browser manifest and firmware report describe different storage")
	}
	for _, asset := range m.Assets {
		payload := files["web/"+asset.Payload]
		if len(payload) != asset.Length || Digest(payload) != asset.SHA256 {
			return empty, invalid("A browser payload size or SHA-256 does not match its manifest")
		}
		compressed := bytes.NewReader(payload)
		z, err := gzip.NewReader(compressed)
		if err != nil {
			return empty, invalid("A browser payload is not valid gzip")
		}
		z.Multistream(false)
		expanded, readErr := io.ReadAll(io.LimitReader(z, int64(asset.SourceLength)+1))
		closeErr := z.Close()
		if readErr != nil || closeErr != nil || compressed.Len() != 0 ||
			len(expanded) != asset.SourceLength || Digest(expanded) != asset.SourceSHA256 {
			return empty, invalid("A browser payload has invalid gzip contents, size or SHA-256")
		}
	}
	return candidate(r), nil
}

// buildArchive creates a deterministic portable package after fetching only
// fixed paths. ValidateArchive is still required before publishing its result.
func buildArchive(files map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Store, ModifiedDate: 33}
		header.SetMode(0644)
		w, err := z.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	if out.Len() > MaxArchiveBytes {
		return nil, invalid("The firmware package exceeds 2 MiB")
	}
	return out.Bytes(), nil
}

// UpdatePayload contains only fixed, validated destinations in the order used by recovery.
type UpdatePayload struct {
	Candidate
	ArchiveSHA256     string
	ReplacesPackageID string // Set by the Hub from its persisted receipt, never from package metadata.
	Files             [6][]byte
	PicoFerm          *PicoFermUpdate
}

func ReadUpdatePayload(archive []byte) (UpdatePayload, error) {
	var payload UpdatePayload
	files, err := archiveFiles(archive)
	if err != nil {
		return payload, err
	}
	c, err := validateFiles(files)
	if err != nil {
		return payload, err
	}
	if !c.SupportsHubOTA() {
		return payload, invalid("Choose a package that supports Hub wireless installation")
	}
	payload.Candidate = c
	payload.ArchiveSHA256 = Digest(archive)
	payload.Files[0] = files[c.Model+"-standalone.bin"]
	for i, f := range webFiles {
		payload.Files[i+1] = files["web/"+f.source+".gz"]
	}
	return payload, nil
}
