package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

// StockZymaticProgram converts an explicitly routed recipe into the AVR's valve
// numbering. Archived Zymatic programs already use that numbering, including
// location 6 for a pause. An unmapped canonical step must stay unmapped.
func StockZymaticProgram(r domain.Recipe, profile domain.Profile) (domain.MachineRecipe, error) {
	var p domain.MachineRecipe
	if err := Validate(r); err != nil {
		return p, err
	}
	if reason := programBlocker(r); reason != "" {
		return p, errors.New(reason)
	}
	if profile.ID != "zymatic" {
		return p, errors.New("choose the Zymatic profile")
	}
	if IsNative(r) {
		if r.SourceModel != "zymatic" {
			return p, errors.New("select a native Zymatic program or create a routed ingredient recipe")
		}
		p = *r.NativeProgram
		p.Steps = append([]domain.MachineStep(nil), p.Steps...)
	} else {
		if r.SourceModel != "zymatic" {
			return p, errors.New("choose each step’s Zymatic flow path before transfer")
		}
		if len(r.Ingredients) > 0 {
			r.Steps = prepareBoilStart(r, profile)
			if reason := checkRoutedIngredients(r, firstHopRoutes(r.Steps)); reason != "" {
				return p, errors.New(reason)
			}
		}
		if err := ValidateCapacity(r, profile); err != nil {
			return p, err
		}
		p.Name = r.Name
		for i, s := range r.Steps {
			if s.Location < 1 || s.Location > 6 {
				return p, fmt.Errorf("choose the flow path for step %d in the advanced editor", i+1)
			}
			if s.InfuseLiters != 0 || s.RampMinutes != 0 || s.Kind == "cool" || s.Kind == "fermentation" || (s.Type != "" && !strings.EqualFold(strings.TrimSpace(s.Type), "Temperature")) {
				return p, fmt.Errorf("step %d needs an operation the stock hot-side protocol cannot encode", i+1)
			}
			f := 0
			if s.TargetC != 0 {
				f = int(math.Round(s.TargetC*1.8 + 32))
			}
			p.Steps = append(p.Steps, domain.MachineStep{Name: s.Name, Location: s.Location - 1, TargetF: f, Minutes: s.Minutes, DrainMinutes: s.DrainMinutes})
		}
	}
	if len(p.Steps) > 25 {
		return p, errors.New("the stock recipe limit is 25 steps")
	}
	p.Name = stockLabel(p.Name, "Recipe")
	for i := range p.Steps {
		s := &p.Steps[i]
		s.Name = stockLabel(s.Name, fmt.Sprintf("Step %d", i+1))
	}
	// Pin each wire identifier to the contents so recovery cannot change after an edit.
	data, err := json.Marshal([]any{r.ID, p.Name, p.Steps})
	if err != nil {
		return p, err
	}
	digest := sha256.Sum256(data)
	p.ID = hex.EncodeToString(digest[:16])
	for excess := len(StockZymaticRecord(p)) - 511; excess > 0; excess-- {
		longest := 0
		for i := range p.Steps {
			if len(p.Steps[i].Name) > len(p.Steps[longest].Name) {
				longest = i
			}
		}
		name := p.Steps[longest].Name
		if len(name) <= 1 {
			return p, errors.New("recipe exceeds the 511-byte stock Ethernet record limit")
		}
		p.Steps[longest].Name = name[:len(name)-1]
	}
	return p, ValidateStockZymaticProgram(p)
}

// ValidateStockZymaticProgram checks the serialized native form at both
// compilation and delivery. Previously saved menus or recovery snapshots may
// predate these checks. The original 1.1.14 and custom controller use this ABI.
func ValidateStockZymaticProgram(p domain.MachineRecipe) error {
	if p.ZPak != nil || p.ZCloud != nil || p.ZProgram != nil || len(p.Steps) == 0 || len(p.Steps) > 25 {
		return errors.New("a Zymatic program needs 1–25 native steps")
	}
	if _, err := hex.DecodeString(p.ID); err != nil || len(p.ID) != 32 || p.ID != strings.ToLower(p.ID) {
		return errors.New("invalid Zymatic program ID")
	}
	validLabel := func(value string) bool {
		return len(value) <= 20 && strings.TrimSpace(value) != "" && strings.IndexFunc(value, func(c rune) bool {
			return c < ' ' || c > '~' || strings.ContainsRune("#|/,", c)
		}) < 0
	}
	if !validLabel(p.Name) {
		return errors.New("invalid Zymatic program name")
	}
	for i, s := range p.Steps {
		if !validLabel(s.Name) || s.Location < 0 || s.Location > 6 {
			return fmt.Errorf("step %d needs a valid Zymatic name and flow path", i+1)
		}
		if s.TargetF < 0 || s.TargetF > 207 || s.Minutes < 0 || s.Minutes > 255 || s.DrainMinutes < 0 || s.DrainMinutes > 255 {
			return fmt.Errorf("step %d exceeds the Zymatic limit of 207 °F or 255 minutes", i+1)
		}
		if s.Location == 6 {
			// 1.1.14 at 0xfa56 uses steps[current+1].name in the pause prompt.
			if i+1 == len(p.Steps) {
				return fmt.Errorf("Zymatic pause step %d needs a following step for the confirmation screen", i+1)
			}
		}
	}
	if len(StockZymaticRecord(p)) > 511 {
		return errors.New("recipe exceeds the 511-byte stock Ethernet record limit")
	}
	return nil
}

func stockLabel(s, fallback string) string {
	s = strings.Map(func(c rune) rune {
		if c < ' ' || c > '~' || strings.ContainsRune("#|/,", c) {
			return ' '
		}
		return c
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return fallback
	}
	if len(s) > 20 {
		s = strings.TrimSpace(s[:20])
	}
	return s
}
func StockZymaticRecord(p domain.MachineRecipe) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s/%s/", p.Name, p.ID)
	for _, s := range p.Steps {
		fmt.Fprintf(&b, "%s,%d,%d,%d,%d/", s.Name, s.TargetF, s.Minutes, s.Location, s.DrainMinutes)
	}
	return b.String()
}
