package domain

import (
	"encoding/json"
	"time"
)

type Device struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Model          string    `json:"model"`
	ModelSource    string    `json:"model_source"`
	Adapter        string    `json:"adapter"`
	BaseURL        string    `json:"base_url"`
	Location       string    `json:"location"`
	BrewerID       string    `json:"brewer_id,omitempty"`
	BrewerRevision int64     `json:"brewer_revision,omitempty"`
	Enabled        bool      `json:"enabled"`
	KeyConfigured  bool      `json:"key_configured"`
	CreatedAt      time.Time `json:"created_at"`
}
type Maintenance struct {
	Available *bool    `json:"available,omitempty"`
	Due       []string `json:"due"`
}
type Status struct {
	CanPause            *bool   `json:"can_pause,omitempty"`
	CanResume           *bool   `json:"can_resume,omitempty"`
	GroupJoined         bool    `json:"group_joined,omitempty"`
	GroupRinseStartedMS *uint32 `json:"group_rinse_started_ms,omitempty"`
	GroupAgeMS          *uint32 `json:"group_age_ms,omitempty"`
	GroupRevision       uint32  `json:"group_revision,omitempty"`
	GroupRecipeID       string  `json:"group_recipe_id,omitempty"`

	Model            string     `json:"model,omitempty"`
	ModelName        string     `json:"model_name,omitempty"`
	ModelFamily      string     `json:"model_family,omitempty"`
	DisplayVariant   string     `json:"display_variant,omitempty"`
	UptimeMS         uint64     `json:"uptime_ms"`
	Ready            bool       `json:"ready"`
	StorageReady     *bool      `json:"storage_ready,omitempty"`
	DeviceUID        string     `json:"device_uid"`
	StockSerial      string     `json:"stock_serial,omitempty"`
	Busy             bool       `json:"busy"`
	Stopped          bool       `json:"stopped"`
	Command          string     `json:"command"`
	DeviceState      int        `json:"device_state"`
	Phase            int        `json:"phase"`
	ControllerStatus int        `json:"controller_status"`
	Compartment      int        `json:"compartment"`
	WortF            *float64   `json:"wort_f"`
	ThermoblockF     *float64   `json:"thermoblock_f"`
	TargetF          *float64   `json:"target_f"`
	StepSeconds      int        `json:"step_seconds"`
	CurrentStep      int        `json:"current_step"`
	StepCount        int        `json:"step_count"`
	RecipeName       string     `json:"recipe_name"`
	StepName         string     `json:"step_name"`
	SessionID        string     `json:"session_id"`
	FilterInserted   *bool      `json:"filter_inserted"`
	StopReason       string     `json:"stop_reason"`
	State            string     `json:"state,omitempty"`
	TemperatureF     *float64   `json:"temperature_f,omitempty"`
	PressureKPa      *float64   `json:"pressure_kpa,omitempty"`
	BatteryV         *float64   `json:"battery_v,omitempty"`
	Sensors          []Sensor   `json:"sensors,omitempty"`
	Actuators        []Actuator `json:"actuators,omitempty"`
	Recovery         *Recovery  `json:"recovery,omitempty"`
	Encoder          struct {
		Position int `json:"position"`
		Min      int `json:"min"`
		Max      int `json:"max"`
	} `json:"encoder"`

	Maintenance *Maintenance `json:"maintenance,omitempty"`
}
type Recovery struct {
	Available      bool   `json:"available"`
	Valid          bool   `json:"valid"`
	RecipeName     string `json:"recipe_name"`
	StepName       string `json:"step_name"`
	CurrentStep    int    `json:"current_step"`
	StepSeconds    uint32 `json:"step_seconds"`
	ElapsedSeconds uint32 `json:"elapsed_seconds"`
}
type Sensor struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Kind  string   `json:"kind"`
	Unit  string   `json:"unit"`
	Value *float64 `json:"value"`
}
type Actuator struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	Kind          string   `json:"kind"`
	Enabled       *bool    `json:"enabled"`
	Level         *float64 `json:"level"`
	RPM           *float64 `json:"rpm"`
	SupportsLevel *bool    `json:"supports_level,omitempty"`
	SupportsRPM   *bool    `json:"supports_rpm,omitempty"`
}
type CommandFields struct {
	Allowed  []string `json:"allowed"`
	Required []string `json:"required"`
}
type FieldMetadata struct {
	Label string `json:"label"`
	Unit  string `json:"unit"`
	Kind  string `json:"kind"`
}
type Capabilities struct {
	ZPicoPakV1            bool `json:"z_picopak_v1,omitempty"`
	PicoStillBrewerLinkV1 bool `json:"picostill_brewer_link_v1,omitempty"`
	GroupRecipeV1         bool `json:"group_recipe_v1,omitempty"`

	GroupBrewingV1          bool                     `json:"group_brewing_v1,omitempty"`
	ReleaseChannel          string                   `json:"release_channel,omitempty"`
	EventsPersistent        *bool                    `json:"events_persistent,omitempty"`
	SystemRestart           bool                     `json:"system_restart,omitempty"`
	HistorySampleIntervalMS uint32                   `json:"history_sample_interval_ms,omitempty"`
	StockRestoreOTAV1       bool                     `json:"stock_restore_ota_v1,omitempty"`
	PairingV1               bool                     `json:"pairing_v1,omitempty"`
	ZProgramMaker           bool                     `json:"z_program_maker,omitempty"`
	OTAProtocol             string                   `json:"ota_protocol,omitempty"`
	HubOTAV1                bool                     `json:"hub_ota_v1,omitempty"`
	APIVersion              int                      `json:"api_version"`
	FirmwareVersion         string                   `json:"firmware_version"`
	Model                   string                   `json:"model,omitempty"`
	ModelName               string                   `json:"model_name,omitempty"`
	ModelFamily             string                   `json:"model_family,omitempty"`
	DisplayVariant          string                   `json:"display_variant,omitempty"`
	Commands                []string                 `json:"commands"`
	RecipeCapacity          int                      `json:"recipe_capacity"`
	StepCapacity            int                      `json:"step_capacity"`
	FrontPanel              bool                     `json:"front_panel"`
	HistoryPersistent       bool                     `json:"history_persistent"`
	TelemetryForwarding     bool                     `json:"telemetry_forwarding"`
	DedicatedPauseResume    bool                     `json:"dedicated_pause_resume"`
	RecipeConditionalWrite  bool                     `json:"recipe_conditional_write,omitempty"`
	RecipeCreateOnly        bool                     `json:"recipe_create_only"`
	ZeroMinuteSteps         bool                     `json:"zero_minute_steps"`
	RestoreDefaultRecipes   bool                     `json:"restore_default_recipes"`
	StarterRecipeCount      int                      `json:"starter_recipe_count"`
	Features                map[string]bool          `json:"capabilities,omitempty"`
	SettingsFields          []string                 `json:"settings_fields"`
	CommandFields           map[string]CommandFields `json:"command_fields,omitempty"`
}

// New firmware reports explicit capabilities. Older brewing firmware keeps
// its established discovery contract and derives only its existing features.
func (c Capabilities) Supports(name string) bool {
	if value, ok := c.Features[name]; ok {
		return value
	}
	switch name {
	case "recipes":
		return c.RecipeCapacity > 0
	case "front_panel":
		return c.FrontPanel
	case "pairing_v1":
		return c.PairingV1
	case "brewing", "heating", "temperature", "pump":
		return c.RecipeCapacity > 0 && (c.Model == "" || c.Model == "pico-s" || c.Model == "pico-pro" || c.Model == "pico-c")
	case "telemetry_forwarding":
		return c.TelemetryForwarding
	case "history_persistent":
		return c.HistoryPersistent
	}
	return false
}

type DeviceView struct {
	Device
	Online                 bool            `json:"online"`
	KeyReused              bool            `json:"key_reused"`
	LastSeen               *time.Time      `json:"last_seen"`
	Error                  string          `json:"error,omitempty"`
	Status                 *Status         `json:"status"`
	Capabilities           *Capabilities   `json:"capabilities"`
	ModelError             string          `json:"model_error,omitempty"`
	ModelErrorCode         string          `json:"model_error_code,omitempty"`
	ModelSelectionRequired bool            `json:"model_selection_required"`
	Telemetry              TelemetryStatus `json:"telemetry"`
}

// TelemetryStatus describes receiver configuration, not the authority of an
// observation. Authenticated polling remains the source of live machine state.
type TelemetryStatus struct {
	Enabled     bool       `json:"enabled"`
	State       string     `json:"state"`
	Message     string     `json:"message"`
	Host        string     `json:"host"`
	Port        int        `json:"port"`
	LastChecked *time.Time `json:"last_checked,omitempty"`
}
type Display struct {
	Dial           string  `json:"dial,omitempty"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	Rotation       *int    `json:"rotation,omitempty"`
	DisplayVariant string  `json:"display_variant,omitempty"`
	StorageBytes   int     `json:"storage_bytes,omitempty"`
	TransferBytes  int     `json:"transfer_bytes,omitempty"`
	Format         string  `json:"format"`
	Data           string  `json:"data"`
	Offset         *int    `json:"offset,omitempty"`
	TotalBytes     int     `json:"total_bytes,omitempty"`
	NextOffset     *int    `json:"next_offset,omitempty"`
	Revision       *uint32 `json:"revision,omitempty"`
}
type Command struct {
	DisplayRevision *uint32  `json:"display_revision,omitempty"`
	Command         string   `json:"command"`
	RecipeID        string   `json:"recipe_id,omitempty"`
	Delta           int      `json:"delta,omitempty"`
	Click           bool     `json:"click,omitempty"`
	Channel         *int     `json:"channel,omitempty"`
	Level           *float64 `json:"level,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
	Value           *float64 `json:"value,omitempty"`
	DurationSeconds *uint32  `json:"duration_seconds,omitempty"`
	Confirm         *bool    `json:"confirm,omitempty"`
}
type MachineStep struct {
	Name         string `json:"name"`
	Location     int    `json:"location"`
	TargetF      int    `json:"target_f"`
	Minutes      int    `json:"minutes"`
	DrainMinutes int    `json:"drain_minutes"`
}
type MachineRecipe struct {
	ZPak     *ZPicoPak     `json:"z_pak,omitempty"`
	ZProgram *ZProgram     `json:"z_program,omitempty"`
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Steps    []MachineStep `json:"steps"`
}
type Ingredient struct {
	SlotAddition bool    `json:"slot_addition,omitempty"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	Amount       float64 `json:"amount"`
	Unit         string  `json:"unit"`
	Use          string  `json:"use"`
	Minutes      float64 `json:"minutes"`
	Alpha        float64 `json:"alpha,omitempty"`
	Type         string  `json:"type,omitempty"`
	Form         string  `json:"form,omitempty"`
	Attenuation  float64 `json:"attenuation,omitempty"`
	Yield        float64 `json:"yield,omitempty"`
	Color        float64 `json:"color,omitempty"`
}
type RecipeStep struct {
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	TargetC      float64 `json:"target_c"`
	Minutes      int     `json:"minutes"`
	DrainMinutes int     `json:"drain_minutes"`
	Location     int     `json:"location"`
	Type         string  `json:"type,omitempty"`
	InfuseLiters float64 `json:"infuse_liters,omitempty"`
	RampMinutes  float64 `json:"ramp_minutes,omitempty"`
}
type FermentationStep struct {
	Name    string  `json:"name"`
	TargetC float64 `json:"target_c"`
	Days    float64 `json:"days"`
}
type Carbonation struct {
	Method       string   `json:"method,omitempty"`
	Volumes      float64  `json:"volumes,omitempty"`
	TemperatureC *float64 `json:"temperature_c,omitempty"`
}

// RecipeProvenance identifies the immutable original record from which an
// editable library copy was made. Warnings describe incomplete conversions.
type RecipeProvenance struct {
	MaxBatchLiters float64  `json:"max_batch_liters,omitempty"`
	CatalogID      string   `json:"catalog_id"`
	SourceID       string   `json:"source_id"`
	Repository     string   `json:"repository"`
	Commit         string   `json:"commit"`
	Path           string   `json:"path"`
	SHA256         string   `json:"sha256"`
	SourceURL      string   `json:"source_url"`
	License        string   `json:"license"`
	Warnings       []string `json:"warnings"`
}
type Recipe struct {
	SourceYeast    string             `json:"source_yeast,omitempty"`
	ID             string             `json:"id"`
	Name           string             `json:"name"`
	Style          string             `json:"style"`
	Description    string             `json:"description"`
	EstimatedYield bool               `json:"estimated_yield,omitempty"`
	BatchLiters    float64            `json:"batch_liters"`
	Efficiency     float64            `json:"efficiency"`
	Ingredients    []Ingredient       `json:"ingredients"`
	Steps          []RecipeStep       `json:"steps"`
	Tags           []string           `json:"tags"`
	Notes          string             `json:"notes"`
	Revision       int64              `json:"revision"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	Type           string             `json:"type,omitempty"`
	BoilLiters     float64            `json:"boil_liters,omitempty"`
	BoilMinutes    float64            `json:"boil_minutes,omitempty"`
	Water          map[string]float64 `json:"water,omitempty"`
	Fermentation   []FermentationStep `json:"fermentation,omitempty"`
	Carbonation    Carbonation        `json:"carbonation,omitempty"`
	Kind           string             `json:"kind,omitempty"`
	SourceModel    string             `json:"source_model,omitempty"`
	NativeProgram  *MachineRecipe     `json:"native_program,omitempty"`
	Provenance     *RecipeProvenance  `json:"provenance,omitempty"`
}
type Profile struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	BatchLiters      float64         `json:"batch_liters"`
	Efficiency       float64         `json:"efficiency"`
	MaxSteps         int             `json:"max_steps"`
	MaxTargetC       float64         `json:"max_target_c"`
	MaxGrainKG       float64         `json:"max_grain_kg"`
	MaxHopSlotG      float64         `json:"max_hop_slot_g"`
	HopSlots         int             `json:"hop_slots"`
	ControlSupported bool            `json:"control_supported"`
	Notes            string          `json:"notes"`
	Sources          []ProfileSource `json:"sources,omitempty"`
	UserConfigured   bool            `json:"user_configured"`
}
type ProfileSource struct {
	Label       string `json:"label"`
	URL         string `json:"url"`
	Description string `json:"description"`
}
type Adapted struct {
	HopSlotsByMinute   map[int]int    `json:"hop_slots_by_minute,omitempty"`
	Recipe             Recipe         `json:"recipe"`
	MachineRecipe      *MachineRecipe `json:"machine_recipe"`
	Profile            Profile        `json:"profile"`
	Warnings           []string       `json:"warnings"`
	DeploymentBlockers []string       `json:"deployment_blockers"`
	CanDeploy          bool           `json:"can_deploy"`
	SourceRevision     int64          `json:"source_revision"`
	ProfileFingerprint string         `json:"profile_fingerprint,omitempty"`
}
type Sample struct {
	// ZymaticHistory selects the named channels recorded by Zymatic EEPROM.
	ZymaticHistory   bool                `json:"zymatic_history,omitempty"`
	ElapsedMS        int64               `json:"elapsed_ms"`
	ObservedAt       *time.Time          `json:"observed_at,omitempty"`
	WortF            *float64            `json:"wort_f"`
	ThermoblockF     *float64            `json:"thermoblock_f"`
	TargetF          *float64            `json:"target_f"`
	StepSeconds      int                 `json:"step_seconds"`
	Phase            int                 `json:"phase"`
	ControllerStatus int                 `json:"controller_status"`
	Compartment      int                 `json:"compartment"`
	CurrentStep      int                 `json:"current_step"`
	Flags            int                 `json:"flags"`
	DeviceState      int                 `json:"device_state"`
	Source           string              `json:"source"`
	TemperatureF     *float64            `json:"temperature_f,omitempty"`
	PressureKPa      *float64            `json:"pressure_kpa,omitempty"`
	BatteryV         *float64            `json:"battery_v,omitempty"`
	Readings         map[string]*float64 `json:"readings,omitempty"`
}
type HistorySession struct {
	Available             bool   `json:"available"`
	ID                    uint64 `json:"id"`
	Command               string `json:"command"`
	RecipeName            string `json:"recipe_name"`
	State                 string `json:"state"`
	DurationMS            int64  `json:"duration_ms"`
	SampleIntervalMS      int64  `json:"sample_interval_ms,omitempty"`
	RecordCount           int    `json:"record_count"`
	PersistedRecordCount  int    `json:"persisted_record_count"`
	FirstElapsedMS        int64  `json:"first_elapsed_ms"`
	GapCount              int    `json:"gap_count"`
	Truncated             bool   `json:"truncated"`
	StorageError          bool   `json:"storage_error"`
	RecoveredAfterRestart bool   `json:"recovered_after_restart"`
}
type History struct {
	HistorySession
	Sessions      []HistorySession         `json:"sessions"`
	Fields        []string                 `json:"fields"`
	FieldMetadata map[string]FieldMetadata `json:"field_metadata,omitempty"`
}

// HistoryPage retains the schema stored with a specific session. Older devices
// omit it on the wire; their adapter supplies the catalog's legacy schema.
type HistoryPage struct {
	Samples       []Sample
	Next          *int
	Fields        []string
	FieldMetadata map[string]FieldMetadata
}
type ConditioningStage struct {
	Name        string     `json:"name"`
	TargetC     *float64   `json:"target_c,omitempty"`
	Days        float64    `json:"days,omitempty"`
	Volumes     float64    `json:"volumes,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}
type Conditioning struct {
	Revision       int64               `json:"revision"`
	RecipeName     string              `json:"recipe_name,omitempty"`
	RecipeRevision int64               `json:"recipe_revision,omitempty"`
	Stages         []ConditioningStage `json:"stages"`
}
type Brew struct {
	GroupID             string `json:"group_id,omitempty"`
	GroupName           string `json:"group_name,omitempty"`
	GroupSessionID      string `json:"group_session_id,omitempty"`
	GroupSourceDeviceID string `json:"group_source_device_id,omitempty"`

	PicoFermID            string                   `json:"picoferm_id,omitempty"`
	PicoFermName          string                   `json:"picoferm_name,omitempty"`
	PicoFermRecordingID   string                   `json:"picoferm_recording_id,omitempty"`
	Conditioning          *Conditioning            `json:"conditioning,omitempty"`
	StepNames             map[int]string           `json:"step_names,omitempty"`
	ID                    string                   `json:"id"`
	DeviceID              string                   `json:"device_id"`
	DeviceName            string                   `json:"device_name"`
	Model                 string                   `json:"model"`
	RecipeName            string                   `json:"recipe_name"`
	RecipeID              string                   `json:"recipe_id,omitempty"`
	RecipeRevision        int64                    `json:"recipe_revision,omitempty"`
	State                 string                   `json:"state"`
	Command               string                   `json:"command"`
	StartedAt             *time.Time               `json:"started_at"`
	ObservedAt            time.Time                `json:"observed_at"`
	DurationMS            int64                    `json:"duration_ms"`
	RecoveredAfterRestart bool                     `json:"recovered_after_restart,omitempty"`
	DeviceHistoryID       uint64                   `json:"device_history_id"`
	GapCount              int                      `json:"gap_count"`
	Truncated             bool                     `json:"truncated"`
	Notes                 string                   `json:"notes"`
	Rating                int                      `json:"rating"`
	SampleCount           int                      `json:"sample_count"`
	Source                string                   `json:"source"`
	SampleFields          []string                 `json:"sample_fields,omitempty"`
	SampleIntervalMS      int64                    `json:"sample_interval_ms,omitempty"`
	FieldMetadata         map[string]FieldMetadata `json:"field_metadata,omitempty"`
}
type BrewDetail struct {
	Brew
	Samples  []Sample   `json:"samples"`
	AIAdvice []AIAdvice `json:"ai_advice,omitempty"`
}

type AIAdvice struct {
	ID          string          `json:"id"`
	CreatedAt   time.Time       `json:"created_at"`
	DeviceID    string          `json:"device_id,omitempty"`
	BrewID      string          `json:"brew_id,omitempty"`
	Mode        string          `json:"mode"`
	Status      string          `json:"status"`
	Text        string          `json:"text"`
	Recipe      json.RawMessage `json:"recipe,omitempty"`
	Limitations []string        `json:"limitations,omitempty"`
	Error       string          `json:"error,omitempty"`
}
