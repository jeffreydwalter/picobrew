package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Field names are case-sensitive on the original appliance. Reject duplicates,
// omitted required fields, and unexpected fields before decoding Go values.
func recipeFields(b []byte, object, required, optional, nullable string) error {
	d := json.NewDecoder(bytes.NewReader(b))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return fmt.Errorf("expected a %s object", object)
	}
	fields := strings.Fields(required + " " + optional)
	seen := map[string]bool{}
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return err
		}
		key, ok := t.(string)
		if !ok || seen[key] || !slices.Contains(fields, key) {
			return fmt.Errorf("unexpected or repeated %s field %q", object, key)
		}
		seen[key] = true
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return err
		}
		if bytes.Equal(raw, []byte("null")) && !slices.Contains(strings.Fields(nullable), key) {
			return fmt.Errorf("%s cannot be null", key)
		}
	}
	for _, key := range strings.Fields(required) {
		if !seen[key] {
			return fmt.Errorf("missing %s field %s", object, key)
		}
	}
	return nil
}

// Missing or null numbers cannot stand in for an explicit unheated target or
// zero-minute heating step. The device permits omission of the drain only.
func (s *MachineStep) UnmarshalJSON(data []byte) error {
	if err := recipeFields(data, "machine step", "name location target_f minutes", "drain_minutes", ""); err != nil {
		return err
	}
	type plain MachineStep
	var step plain
	if err := json.Unmarshal(data, &step); err != nil {
		return err
	}
	*s = MachineStep(step)
	return nil
}
