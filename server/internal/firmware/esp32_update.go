package firmware

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
)

// ReadFactoryUpdate returns only the application named by this verified package.
// The USB bootloader, recovery, partition table and filesystem never enter the OTA payload.
func ReadFactoryUpdate(archive []byte, candidate Candidate) (UpdatePayload, error) {
	var out UpdatePayload
	if !candidate.SupportsFactoryOTA() || !ValidID(candidate.BinarySHA256) || !versionPattern.MatchString(candidate.FirmwareVersion) || len(archive) > MaxReleaseBytes {
		return out, invalid("This is not a supported ESP32 application update")
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(reader.File) > 64 {
		return out, invalid("Invalid ESP32 reference package")
	}
	files := map[string][]byte{}
	for _, f := range reader.File {
		limit := 0
		switch f.Name {
		case "application.bin":
			limit = candidate.BinaryBytes
		case "manifest.json", "build-report.json":
			limit = 1 << 20
		default:
			continue
		}
		if _, exists := files[f.Name]; exists || !f.Mode().IsRegular() || f.UncompressedSize64 == 0 || f.UncompressedSize64 > uint64(limit) {
			return out, invalid("Invalid ESP32 package member")
		}
		src, err := f.Open()
		if err != nil {
			return out, invalid("Unreadable ESP32 package member")
		}
		data, err := io.ReadAll(io.LimitReader(src, int64(limit)+1))
		src.Close()
		if err != nil || uint64(len(data)) != f.UncompressedSize64 {
			return out, invalid("Damaged ESP32 package member")
		}
		files[f.Name] = data
	}
	var manifest struct {
		Model      string `json:"model"`
		Protocol   string `json:"ota_protocol"`
		Image      string `json:"ota_image"`
		ReportHash string `json:"build_report_sha256"`
		Components []struct {
			File   string `json:"file"`
			Size   int    `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"components"`
	}
	var build struct {
		Model   string `json:"model"`
		Version string `json:"firmware_version"`
	}
	if json.Unmarshal(files["manifest.json"], &manifest) != nil || manifest.Protocol != candidate.OTAProtocol ||
		manifest.Image != "application.bin" || manifest.Model != candidate.Model || manifest.ReportHash != Digest(files["build-report.json"]) ||
		json.Unmarshal(files["build-report.json"], &build) != nil || build.Model != candidate.Model || build.Version != candidate.FirmwareVersion {
		return out, invalid("ESP32 model, version or update manifest does not match the selected release")
	}
	matched := 0
	for _, component := range manifest.Components {
		if component.File != manifest.Image {
			continue
		}
		if component.SHA256 != candidate.BinarySHA256 || component.Size != candidate.BinaryBytes {
			return out, invalid("The application differs from its component record")
		}
		matched++
	}
	app := files[manifest.Image]
	var marker [64]byte
	descriptor := "PicoBrew OTA1|" + candidate.Model + "|" + candidate.FirmwareVersion
	if len(descriptor) >= len(marker) {
		return out, invalid("Invalid ESP32 build version")
	}
	copy(marker[:], descriptor)
	if matched != 1 || len(app) != candidate.BinaryBytes || Digest(app) != candidate.BinarySHA256 || len(app) < 0x160+sha256.Size || app[0] != 0xe9 || app[23] != 1 ||
		binary.LittleEndian.Uint32(app[24:]) != 0x3f400020 || binary.LittleEndian.Uint32(app[28:]) < 320 ||
		binary.LittleEndian.Uint32(app[32:]) != 0xabcd5432 || !bytes.Equal(app[0x120:0x160], marker[:]) {
		return out, invalid("The ESP32 application failed its size, digest or target check")
	}
	if !validESP32Segments(app) {
		return out, invalid("Invalid ESP32 application segments, checksum or appended digest")
	}
	out.Candidate = candidate
	out.ArchiveSHA256 = Digest(archive)
	out.Files[0] = app
	return out, nil
}

// Verify the ESP image as written to flash. An intact ZIP and matching hashes
// alone do not establish that the segment table describes a bootable image.
func validESP32Segments(image []byte) bool {
	const header = 24
	if len(image) < header+sha256.Size || image[0] != 0xe9 || image[1] == 0 || image[1] > 16 || image[23] != 1 {
		return false
	}
	payload := image[:len(image)-sha256.Size]
	pc := binary.LittleEndian.Uint32(payload[4:])
	sum := byte(0xef)
	pos := header
	entryFound := false
	for range image[1] {
		if len(payload)-pos < 8 {
			return false
		}
		start := binary.LittleEndian.Uint32(payload[pos:])
		size := binary.LittleEndian.Uint32(payload[pos+4:])
		pos += 8
		if size%4 != 0 || start+size < start || uint64(size) > uint64(len(payload)-pos) {
			return false
		}
		if pc >= 0x40080000 && pc < 0x40400000 && start <= pc && pc-start < size {
			entryFound = true
		}
		for _, b := range payload[pos : pos+int(size)] {
			sum ^= b
		}
		pos += int(size)
	}
	checkAt := pos | 15
	if !entryFound || checkAt+1 != len(payload) || payload[checkAt] != sum {
		return false
	}
	for _, b := range payload[pos:checkAt] {
		if b != 0 {
			return false
		}
	}
	digest := sha256.Sum256(payload)
	return bytes.Equal(digest[:], image[len(payload):])
}
