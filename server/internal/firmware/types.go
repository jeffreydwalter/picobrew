// Package firmware validates and acquires standalone firmware packages. Its
// results establish package integrity, not physical compatibility or a safe
// installation path. This package never contacts or writes to a machine.
package firmware

import "time"

const (
	DefaultRepository = "jeffreydwalter/picobrew"
	MaxArchiveBytes   = 2 << 20
	MaxExpandedBytes  = 2 << 20
	MaxReportBytes    = 512 << 10
	MaxManifestBytes  = 16 << 10
	MaxBinaryBytes    = 184 << 10
	MaxAssetBytes     = 65024
	MaxSourceBytes    = 1 << 20
	MaxPackages       = 20
)

type Settings struct {
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
}

// Candidate describes a report. The catalog does not assert that the image
// and browser payloads have been downloaded or validated.
type Candidate struct {
	OTAProtocol     string `json:"ota_protocol,omitempty"`
	HubOTAProtocol  int    `json:"hub_ota_protocol,omitempty"`
	Model           string `json:"model"`
	ModelFamily     string `json:"model_family"`
	DisplayVariant  string `json:"display_variant,omitempty"`
	FirmwareVersion string `json:"firmware_version"`
	BinarySHA256    string `json:"binary_sha256"`
	BinaryBytes     int    `json:"binary_bytes"`
}

// SupportsHubOTA identifies packages the Hub recovery protocol can install.
func (c Candidate) SupportsHubOTA() bool {
	return c.SupportsCC3200OTA() || c.SupportsFactoryOTA() || c.SupportsFermOTA()
}

// MatchesWirelessTarget binds a downloaded package to the registered machine.
// A Z module may also report its fixed SSD1325 display in its identity.
func (c Candidate) MatchesWirelessTarget(model, display string) bool {
	if c.Model != model || !c.SupportsHubOTA() {
		return false
	}
	if model == "z-series" && display == "ssd1325" {
		return c.SupportsFactoryOTA()
	}
	return c.DisplayVariant == display
}

func (c Candidate) SupportsCC3200OTA() bool {
	return c.OTAProtocol == "" && c.HubOTAProtocol == 1 && ValidVariant(c.Model, c.DisplayVariant) && c.ModelFamily == modelFamily(c.Model)
}

const ESP32FactoryProtocol = "esp32-factory-v1"
const StillApplicationLimit = 960 << 10

// ESP32 transmit limits come from the USB-provisioned layout for each model.
func FactoryApplicationLimit(model string) int {
	switch model {
	case "picostill":
		return StillApplicationLimit
	case "z-series":
		return 0x1a1000
	}
	return 0
}

func (c Candidate) SupportsFactoryOTA() bool {
	return c.OTAProtocol == ESP32FactoryProtocol && c.ModelFamily == c.Model &&
		c.DisplayVariant == "" && c.HubOTAProtocol == 0 && c.BinaryBytes > 0 && c.BinaryBytes <= FactoryApplicationLimit(c.Model)
}

const FermABProtocol = "esp8266-ab-v1"
const FermBankLimit = 0x7d000

func (c Candidate) SupportsFermOTA() bool {
	return c.OTAProtocol == FermABProtocol && c.Model == "picoferm" && c.ModelFamily == "picoferm" &&
		c.DisplayVariant == "" && c.HubOTAProtocol == 0 && c.BinaryBytes > 0 && c.BinaryBytes <= FermBankLimit
}

func (r Release) WirelessPackage() bool {
	switch r.Kind {
	case "standalone":
		return r.SupportsCC3200OTA()
	case "reference":
		return r.SupportsFactoryOTA()
	case "picoferm-ab":
		return r.SupportsFermOTA()
	}
	return false
}

type Catalog struct {
	Repository string      `json:"repository"`
	Ref        string      `json:"ref"`
	Commit     string      `json:"commit"`
	Packages   []Candidate `json:"packages"`
}

// Provenance is produced by the Hub acquisition path, never by an uploaded
// report. A GitHub commit identifies the retrieved files, not hardware testing.
type Provenance struct {
	Source     string `json:"source"`
	Repository string `json:"repository,omitempty"`
	Commit     string `json:"commit,omitempty"`
}

type Package struct {
	Candidate
	ID           string     `json:"id"`
	PackageBytes int        `json:"package_bytes"`
	CreatedAt    time.Time  `json:"created_at"`
	Validation   string     `json:"validation"`
	Provenance   Provenance `json:"provenance"`
}

type InstallReceipt struct {
	ReplacesPackageID string    `json:"replaces_package_id,omitempty"`
	DisplayVariant    string    `json:"display_variant,omitempty"`
	Stage             string    `json:"stage,omitempty"`
	UID               string    `json:"device_uid"`
	Model             string    `json:"model"`
	PackageID         string    `json:"package_id"`
	Started           time.Time `json:"started"`
	State             string    `json:"state"`
	Message           string    `json:"message"`
}

// USBRestoreTarget identifies the physical registration for a local guide.
// A USB writer verifies the attached hardware and backup before any write.
type USBRestoreTarget struct {
	Model string `json:"model"`
	UID   string `json:"device_uid"`
}

type StockReceipt struct {
	UID     string    `json:"device_uid"`
	Model   string    `json:"model"`
	Started time.Time `json:"started"`
	Stage   string    `json:"stage"`
	State   string    `json:"state"`
	Message string    `json:"message"`
}

type MachineFirmware struct {
	RevisedPackageID       string        `json:"revised_package_id,omitempty"`
	CanConfirmStockStartup bool          `json:"can_confirm_stock_startup,omitempty"`
	StockRestoreWireless   bool          `json:"stock_restore_wireless,omitempty"`
	StockRestoreUpgrade    bool          `json:"stock_restore_upgrade,omitempty"`
	LastStockRestore       *StockReceipt `json:"last_stock_restore,omitempty"`

	StockRestoreGuide            bool              `json:"stock_restore_guide,omitempty"`
	USBRestoreTarget             *USBRestoreTarget `json:"usb_restore_target,omitempty"`
	RetryPackageID               string            `json:"retry_package_id,omitempty"`
	LastInstall                  *InstallReceipt   `json:"last_install,omitempty"`
	DeviceID                     string            `json:"device_id"`
	Model                        string            `json:"model"`
	CurrentVersion               string            `json:"current_version"`
	DisplayVariant               string            `json:"display_variant,omitempty"`
	VerifiedDisplayVariant       string            `json:"verified_display_variant,omitempty"`
	DeviceUID                    string            `json:"device_uid,omitempty"`
	HardwareVerificationRequired bool              `json:"hardware_verification_required,omitempty"`
	InstallSupported             bool              `json:"install_supported"`
	Reason                       string            `json:"reason"`
	Packages                     []Package         `json:"packages"`
}

// Error contains a bounded, user-facing diagnostic, without a remote response
// body or an uploaded filename. Transport errors are deliberately sanitized.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func invalid(message string) error {
	return &Error{Code: "firmware_package_invalid", Message: message}
}
