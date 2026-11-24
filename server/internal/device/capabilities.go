package device

import (
	"math"
	"regexp"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

var commandName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,31}$`)

func includes(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func unsupportedCapability(name string) error {
	return &Error{StatusCode: 404, Code: "unsupported_capability", Message: "This device does not advertise " + name}
}

func validateDiscoveredCommand(command domain.Command, caps domain.Capabilities) error {
	if command.Command == "encoder" && caps.Supports("panel_revision") {
		if command.DisplayRevision == nil || command.Click && command.Delta != 0 {
			return invalid("Use the current display revision and send one dial action")
		}
		// Validate the normal encoder fields on this copy. The caller sends the revision.
		command.DisplayRevision = nil
	} else if command.DisplayRevision != nil {
		return invalid("This command does not accept a display revision")
	}
	descriptor, described := caps.CommandFields[command.Command]
	if !described {
		return validateCommand(command, includes(caps.Commands, "manual"))
	}
	if !commandName.MatchString(command.Command) {
		return invalid("Command name is invalid")
	}
	present := map[string]bool{
		"recipe_id": command.RecipeID != "", "delta": command.Delta != 0, "click": command.Click,
		"channel": command.Channel != nil, "level": command.Level != nil, "enabled": command.Enabled != nil,
		"value": command.Value != nil, "duration_seconds": command.DurationSeconds != nil, "confirm": command.Confirm != nil,
	}
	for field, value := range present {
		if value && !includes(descriptor.Allowed, field) {
			return invalid("This command does not advertise the " + field + " argument")
		}
	}
	for _, field := range descriptor.Required {
		if !present[field] {
			return invalid("This command requires the " + field + " argument")
		}
	}
	if command.RecipeID != "" && !recipeID.MatchString(command.RecipeID) || command.Delta < -100 || command.Delta > 100 {
		return invalid("Recipe ID or encoder movement is invalid")
	}
	if command.Channel != nil && (*command.Channel < 0 || *command.Channel > math.MaxInt32) {
		return invalid("Channel must be a nonnegative device channel")
	}
	if command.Level != nil && (math.IsNaN(*command.Level) || math.IsInf(*command.Level, 0) || *command.Level < 0 || *command.Level > 100) {
		return invalid("Output level must be from 0 to 100 percent")
	}
	if command.Value != nil && (math.IsNaN(*command.Value) || math.IsInf(*command.Value, 0)) {
		return invalid("Command value must be finite")
	}
	if command.Command == "set-fan" && (command.Channel == nil || *command.Channel < 1 || *command.Channel > 2) {
		return invalid("Fan channel must be 1 or 2")
	}
	if command.Command == "set-sampling-interval" && (command.Value == nil || *command.Value < 60 || *command.Value > 86400 || *command.Value != math.Trunc(*command.Value)) {
		return invalid("Sampling interval must be an integer from 60 to 86400 seconds")
	}
	if command.Command == "calibrate" && (command.Confirm == nil || !*command.Confirm) {
		return invalid("Confirm that PicoFerm is removed from the keg and depressurized before calibrating")
	}
	if command.Command == "resume-interrupted" && (command.Confirm == nil || !*command.Confirm) {
		return invalid("Verify the water, vessel and filter and explicitly confirm resuming the interrupted brew")
	}
	return nil
}

func validateRecovery(recovery *domain.Recovery) error {
	if recovery == nil {
		return nil
	}
	if recovery.CurrentStep < -1 || recovery.CurrentStep > 255 || recovery.StepSeconds > math.MaxInt32 ||
		recovery.RecipeName != "" && !validLabel(recovery.RecipeName, 31) ||
		recovery.StepName != "" && !validLabel(recovery.StepName, 31) ||
		recovery.Available && recovery.Valid && recovery.RecipeName == "" {
		return &Error{StatusCode: 502, Code: "invalid_response", Message: "Device returned invalid interrupted-session metadata"}
	}
	return nil
}
