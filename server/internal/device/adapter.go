// Package device adapts explicitly configured appliances and isolated simulators.
package device

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

type Adapter interface {
	Discover(context.Context) (domain.Capabilities, error)
	Status(context.Context) (domain.Status, error)
	Display(context.Context) (domain.Display, error)
	Recipes(context.Context) ([]domain.MachineRecipe, error)
	Recipe(context.Context, string) (domain.MachineRecipe, error)
	CreateRecipe(context.Context, domain.MachineRecipe) error
	PutRecipe(context.Context, domain.MachineRecipe, *domain.MachineRecipe) error
	DeleteRecipe(context.Context, string, *domain.MachineRecipe) error
	Command(context.Context, domain.Command) error
	Settings(context.Context) (map[string]any, error)
	UpdateSettings(context.Context, map[string]any) error
	Telemetry(context.Context) (map[string]any, error)
	UpdateTelemetry(context.Context, map[string]any) error
	History(context.Context) (domain.History, error)
	HistoryPage(context.Context, uint64, int) ([]domain.Sample, *int, error)
}

// HistorySchemaReader is optional so existing adapters and simulators retain
// their stable interface while newer firmware preserves per-session channels.
type HistorySchemaReader interface {
	HistoryPageWithSchema(context.Context, uint64, int) (domain.HistoryPage, error)
}

// ReportedModel validates a firmware build identity. It does not establish
// physical hardware compatibility. An empty identity denotes older firmware
// and must never be interpreted as a particular S/Pro model.
func SupportedModel(model string) bool {
	return model == "pico-s" || model == "pico-pro" || model == "pico-c" || model == "z-series" || model == "picoferm" || model == "picostill" || model == "zymatic"
}

func ReportedModel(c domain.Capabilities) (string, error) {
	if c.Model == "" {
		if c.ModelName != "" || c.ModelFamily != "" || c.DisplayVariant != "" {
			return "", &Error{StatusCode: 409, Code: "invalid_model", Message: "The firmware reported an incomplete machine model identity"}
		}
		return "", nil
	}
	if !SupportedModel(c.Model) {
		return "", &Error{StatusCode: 409, Code: "unsupported_model", Message: "The firmware reports a machine model that this Hub cannot control"}
	}
	if c.Model == "pico-c" {
		if c.ModelFamily != "pico-c" || (c.DisplayVariant != "ssd1325" && c.DisplayVariant != "ssd1309") {
			return "", &Error{StatusCode: 409, Code: "invalid_model", Message: "Pico C firmware must report its C family and an explicit supported display variant"}
		}
	} else if c.Model == "z-series" || c.Model == "picoferm" || c.Model == "picostill" || c.Model == "zymatic" {
		if c.ModelFamily != c.Model || (c.Model != "z-series" && c.DisplayVariant != "") ||
			(c.Model == "z-series" && c.DisplayVariant != "" && c.DisplayVariant != "ssd1325") {
			return "", &Error{StatusCode: 409, Code: "invalid_model", Message: "The firmware must report a consistent explicit machine family and display identity"}
		}
	} else if c.ModelFamily != "" && c.ModelFamily != "pico-s-pro" {
		return "", &Error{StatusCode: 409, Code: "invalid_model", Message: "The firmware model and model family are inconsistent"}
	}
	return c.Model, nil
}

// Error never includes a device address, credential or raw transport diagnostic.
// UnknownOutcome means a mutation may have reached the device; observe/read back
// before deciding what to do. It must never trigger an automatic write retry.
type Error struct {
	StatusCode     int    `json:"status"`
	Code           string `json:"code"`
	Message        string `json:"message"`
	UnknownOutcome bool   `json:"unknown_outcome"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func existsError() error {
	return &Error{StatusCode: 409, Code: "recipe_exists", Message: "This recipe ID is already on the machine. Review the existing program before replacing it."}
}

func recipeChanged() error {
	return &Error{StatusCode: 409, Code: "recipe_changed", Message: "The installed recipe changed. Reload it before trying again."}
}

func invalid(message string) error {
	return &Error{StatusCode: 400, Code: "invalid_request", Message: message}
}

var recipeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,20}$`)
var recipeBrewerCommands = []string{"brew", "sous-vide", "cold-brew", "rinse", "first-rinse", "deep-clean", "descale", "circulate", "drain-reservoir", "drain-filter", "rack-beer", "utilities", "settings", "stop", "clear-stop", "encoder"}

func validLabel(value string, max int) bool {
	if len(value) == 0 || len(value) > max {
		return false
	}
	for _, ch := range value {
		if ch < 32 || ch > 126 {
			return false
		}
	}
	return true
}

func validateRecipe(recipe domain.MachineRecipe) error {
	if recipe.ZPak != nil {
		if !recipeID.MatchString(recipe.ID) || !validLabel(recipe.Name, 31) || recipe.ZProgram != nil || recipe.ZCloud != nil {
			return invalid("invalid PicoPak identity or conflicting program")
		}
		for _, step := range recipe.Steps {
			if !validLabel(step.Name, 21) {
				return invalid("invalid PicoPak step name")
			}
		}
		if err := recipe.ZPak.Validate(recipe.Steps); err != nil {
			return invalid(err.Error())
		}
		return nil
	}
	if recipe.ZCloud != nil {
		if !recipeID.MatchString(recipe.ID) {
			return invalid("invalid Z cloud recipe ID")
		}
		if err := recipe.ValidateZCloud(); err != nil {
			return invalid(err.Error())
		}
		return nil
	}
	if recipe.ZProgram != nil {
		if !recipeID.MatchString(recipe.ID) || len(recipe.Steps) != 0 || recipe.Name != recipe.ZProgram.Name {
			return invalid("check the Z program identity and schedule")
		}
		if err := recipe.ZProgram.Validate(); err != nil {
			return invalid(err.Error())
		}
		return nil
	}
	if !recipeID.MatchString(recipe.ID) || !validLabel(recipe.Name, 31) || len(recipe.Steps) < 1 || len(recipe.Steps) > 15 {
		return invalid("Recipe ID, name or step count is invalid")
	}
	for i, step := range recipe.Steps {
		if !validLabel(step.Name, 21) || step.Location < 1 || step.Location > 6 || (step.TargetF != 0 && (step.TargetF < 60 || step.TargetF > 212)) || step.Minutes < 0 || step.Minutes > 599 || step.DrainMinutes < 0 || step.DrainMinutes > 120 {
			return invalid(fmt.Sprintf("Recipe step %d is outside the firmware limits", i+1))
		}
	}
	return nil
}

func validateCommand(command domain.Command, legacyManual bool) error {
	if command.DisplayRevision != nil || command.Channel != nil || command.Level != nil || command.Enabled != nil || command.Value != nil || command.DurationSeconds != nil || command.Confirm != nil {
		return invalid("This firmware does not advertise these command arguments")
	}
	if !includes(recipeBrewerCommands, command.Command) && !(legacyManual && command.Command == "manual") {
		return invalid("Command is unsupported; dedicated pause/resume is unavailable")
	}
	switch command.Command {
	case "brew":
		if !recipeID.MatchString(command.RecipeID) || command.Delta != 0 || command.Click {
			return invalid("Brew requires only a valid recipe_id")
		}
	case "encoder":
		if command.RecipeID != "" || command.Delta < -100 || command.Delta > 100 || command.Delta == 0 && !command.Click {
			return invalid("Encoder requires movement from -100 to 100 or one click")
		}
	default:
		if command.RecipeID != "" || command.Delta != 0 || command.Click {
			return invalid("This command does not accept additional parameters")
		}
	}
	return nil
}

func validateSettings(settings map[string]any) error {
	if len(settings) == 0 {
		return invalid("At least one preference is required")
	}
	for key, value := range settings {
		switch key {
		case "temperature_unit":
			if value != "F" && value != "C" {
				return invalid("Temperature unit must be F or C")
			}
		case "language":
			if value != "en" && value != "es" && value != "fr" {
				return invalid("Language must be en, es or fr")
			}
		case "detailed_status", "auto_cycle":
			if _, ok := value.(bool); !ok {
				return invalid("Display preferences must be boolean")
			}
		default:
			return invalid("Unknown preference")
		}
	}
	return nil
}

func validateTelemetry(settings map[string]any) error {
	if len(settings) != 3 {
		return invalid("Telemetry requires enabled, host and port")
	}
	enabled, ok := settings["enabled"].(bool)
	if !ok {
		return invalid("Telemetry enabled must be boolean")
	}
	host, ok := settings["host"].(string)
	if !ok || len(host) > 127 || enabled && host == "" || host != "" && !validTelemetryHost(host) {
		return invalid("Telemetry host must be a hostname or IPv4 address without a scheme or port")
	}
	var port float64
	switch value := settings["port"].(type) {
	case int:
		port = float64(value)
	case float64:
		port = value
	default:
		return invalid("Telemetry port must be an integer")
	}
	if port < 1 || port > 65535 || port != float64(int(port)) {
		return invalid("Telemetry port must be from 1 to 65535")
	}
	return nil
}

// ValidateTelemetry checks a persisted Hub override using the same wire limits
// enforced immediately before a device write.
func ValidateTelemetry(settings map[string]any) error { return validateTelemetry(settings) }

func validTelemetryHost(host string) bool {
	labels := strings.Split(host, ".")
	if len(labels) == 4 {
		var address uint32
		valid := true
		for _, label := range labels {
			number, err := strconv.Atoi(label)
			if err != nil || len(label) < 1 || len(label) > 3 || number < 0 || number > 255 || strings.IndexFunc(label, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
				valid = false
				break
			}
			address = address<<8 | uint32(number)
		}
		if valid {
			return address != 0 && address != 0xffffffff
		}
	}
	numeric := true
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
			if ch < '0' || ch > '9' {
				numeric = false
			}
		}
	}
	return !numeric
}

var historyFields = []string{"elapsed_ms", "wort_f", "thermoblock_f", "target_f", "step_seconds", "phase", "controller_status", "compartment", "current_step", "flags", "device_state"}
