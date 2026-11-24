package device

import (
	"context"
	_ "embed"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

// The S/Pro 0.1.34 reconstructed ui/assets.c supplies the five-column glyphs
// for ASCII 32–126 and the 80×21 Pico logo. Embed them here so the Hub runs
// independently of the firmware tree.
//
//go:embed sim_oled_font.bin
var simFont []byte

//go:embed sim_oled_logo.bin
var simLogo []byte

// Rendered by the S/Pro pico_display_draw_content_frame routine at rotation zero.
//
//go:embed sim_oled_frame.bin
var simContentFrame []byte

type panelItem struct{ label, action string }

var mainPanel = []panelItem{
	{"Brew Recipe", "recipes"},
	{"Sous Vide", "sous-vide"},
	{"Cold Brew", "cold-brew"},
	{"Utilities", "utilities"},
	{"Settings", "settings"},
}
var utilityPanel = []panelItem{
	{"First Rinse", "first-rinse"},
	{"Rinse", "rinse"},
	{"Rack Beer", "rack-beer"},
	{"Drain Step Filter", "drain-filter"},
	{"Drain Reservoir", "drain-reservoir"},
	{"Descale", "descale"},
	{"Circulate", "circulate"},
	{"Back", "back"},
}

func (s *simulator) panelItems() (string, []panelItem) {
	switch s.panelMenu {
	case "recipes":
		items := make([]panelItem, 0, len(s.recipes)+1)
		for id, recipe := range s.recipes {
			items = append(items, panelItem{recipe.Name, id})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].action < items[j].action })
		return "SELECT RECIPE", append(items, panelItem{"Back", "back"})
	case "settings":
		return "SETTINGS", []panelItem{{"Temperature Units", "units"}, {"Back", "back"}}
	case "units":
		return "TEMPERATURE UNITS", []panelItem{{"Fahrenheit", "F"}, {"Celsius", "C"}, {"Back", "back"}}
	case "utilities":
		return "UTILITIES", utilityPanel
	default:
		return "", mainPanel
	}
}

func (s *simulator) resetPanel(menu string) {
	s.panelMenu = menu
	_, items := s.panelItems()
	s.status.Encoder.Position = 0
	s.status.Encoder.Max = len(items) - 1
}

func (s *simulator) panelInput(command domain.Command, now time.Time) error {
	e := &s.status.Encoder
	if s.active != nil {
		e.Position = max(0, min(e.Max, e.Position+command.Delta))
		if command.Click {
			e.Position = (e.Position + 1) % (e.Max + 1)
		}
		return nil
	}
	if s.status.Stopped || (s.status.Busy && s.panelMenu == "") {
		return nil
	}
	_, items := s.panelItems()
	e.Max = len(items) - 1
	e.Position = max(0, min(e.Max, e.Position+command.Delta))
	if !command.Click {
		return nil
	}
	item := items[e.Position]
	if s.panelMenu == "recipes" && e.Position != len(items)-1 {
		return s.startProgram(domain.Command{Command: "brew", RecipeID: item.action}, now)
	}
	switch item.action {
	case "units", "recipes", "settings", "utilities":
		s.status.Command = "front-panel"
		s.status.Busy = true
		s.resetPanel(item.action)
	case "F", "C":
		s.settings["temperature_unit"] = item.action
		s.resetPanel("settings")
	case "back":
		if s.panelMenu == "units" {
			s.resetPanel("settings")
		} else {
			s.status.Busy = false
			s.status.Command = "idle"
			s.resetPanel("")
		}
	default:
		return s.startProgram(domain.Command{Command: item.action}, now)
	}
	return nil
}

type panelFrame [1024]byte

func (f *panelFrame) pixel(x, y int, on bool) {
	if x < 0 || x >= 128 || y < 0 || y >= 64 {
		return
	}
	i := y/8*128 + x
	mask := byte(1 << (y % 8))
	if on {
		f[i] |= mask
	} else {
		f[i] &^= mask
	}
}

func (f *panelFrame) bitmap(left, top, width, height int, data []byte) {
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if data[y*((width+7)/8)+x/8]&(0x80>>(x%8)) != 0 {
				f.pixel(left+x, top+y, true)
			}
		}
	}
}

func (f *panelFrame) text(row int, label string, scale int, on bool) {
	chars := []rune(label)
	chars = chars[:min(len(chars), 128/(6*scale))]
	left := (128 - len(chars)*6*scale) / 2
	for _, ch := range chars {
		if ch < 32 || ch > 126 {
			ch = '?'
		}
		for x := 0; x < 5*scale; x++ {
			bits := simFont[int(ch-32)*5+x/scale]
			for y := 0; y < 8*scale; y++ {
				if bits&(1<<(y/scale)) != 0 {
					f.pixel(left+x, row*8+y, on)
				}
			}
		}
		left += 6 * scale
	}
}

func (f *panelFrame) menu(title string, items []panelItem, selected int) {
	row := 3
	if title == "" {
		f.bitmap(24, 2, 80, 21, simLogo)
		row = 4
	} else {
		f.text(0, title, 1, true)
		for x := 16; x < 112; x++ {
			f.pixel(x, 11, true)
		}
	}
	selected = max(0, min(selected, len(items)-1))
	first := max(0, selected-(7-row))
	for i := first; i < len(items) && row < 8; i++ {
		if i == selected {
			y := row * 8
			f.bitmap(0, y, 8, 8, []byte{31, 63, 127, 127, 127, 127, 63, 31})
			for x := 8; x < 120; x++ {
				f[y/8*128+x] = 255
			}
			f.bitmap(120, y, 8, 8, []byte{248, 220, 142, 254, 254, 142, 220, 248})
		}
		f.text(row, items[i].label, 1, i != selected)
		row++
	}
}

func (s *simulator) Display(ctx context.Context) (domain.Display, error) {
	if err := simulationContext(ctx); err != nil {
		return domain.Display{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advance(time.Now())
	var frame panelFrame
	switch {
	case s.active != nil:
		copy(frame[:], simContentFrame)
		if s.status.Encoder.Position == 0 {
			frame.text(3, s.status.RecipeName, 1, true)
			frame.text(7, "NOW BREWING", 1, true)
		} else {
			frame.text(0, s.status.StepName, 1, true)
			seconds := s.status.StepSeconds
			frame.text(3, fmt.Sprintf("%d:%02d", seconds/60, seconds%60), 2, true)
		}
	case s.status.Busy && s.panelMenu == "":
		copy(frame[:], simContentFrame)
		for _, menu := range [][]panelItem{mainPanel, utilityPanel} {
			for _, item := range menu {
				if item.action == s.status.Command {
					frame.text(3, item.label, 1, true)
				}
			}
		}
	default:
		title, items := s.panelItems()
		frame.menu(title, items, s.status.Encoder.Position)
	}
	rotation := 0
	return domain.Display{Width: 128, Height: 64, Rotation: &rotation, Format: "page-lsb-hex", Data: hex.EncodeToString(frame[:])}, nil
}
