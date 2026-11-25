package device

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

func ptr[T any](value T) *T { return &value }

func TestFamilyIdentityAndUnsupportedSurfaces(t *testing.T) {
	for _, model := range []string{"z-series", "picostill", "picoferm", "zymatic"} {
		t.Run(model, func(t *testing.T) {
			caps := domain.Capabilities{Model: model, ModelFamily: model, Features: map[string]bool{"recipes": false, "front_panel": false}}
			if got, err := ReportedModel(caps); err != nil || got != model {
				t.Fatalf("identity %q: %v", got, err)
			}
			bad := caps
			bad.ModelFamily = "pico-s-pro"
			if _, err := ReportedModel(bad); err == nil {
				t.Fatal("accepted inferred family")
			}
			if model != "z-series" {
				bad = caps
				bad.DisplayVariant = "ssd1325"
				if _, err := ReportedModel(bad); err == nil {
					t.Fatal("accessory accepted a front panel")
				}
			}
			p := testPico(t, func(http.ResponseWriter, *http.Request) { t.Error("unsupported operation reached device") })
			p.capabilities = &caps
			_, display := p.Display(context.Background())
			_, recipes := p.Recipes(context.Background())
			for _, err := range []error{display, recipes, p.PutRecipe(context.Background(), nativeRecipe(), nil), p.DeleteRecipe(context.Background(), "Ale_01", nil)} {
				if deviceFailure(t, err).Code != "unsupported_capability" {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestZymaticDiscoveryHasNoDevicePreferencesOrForwarding(t *testing.T) {
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1" {
			t.Errorf("unsupported operation reached Zymatic: %s %s", r.Method, r.URL.Path)
		}
		replyJSON(w, 200, map[string]any{"api_version": 1, "mode": "standalone", "model": "zymatic", "model_family": "zymatic", "commands": []string{"encoder"}})
	})
	ctx := context.Background()
	caps, err := p.Discover(ctx)
	if err != nil || caps.SettingsFields == nil || len(caps.SettingsFields) != 0 {
		t.Fatalf("expected explicitly empty preferences, got %+v, %v", caps.SettingsFields, err)
	}
	_, settings := p.Settings(ctx)
	_, telemetry := p.Telemetry(ctx)
	writeTelemetry := p.UpdateTelemetry(ctx, map[string]any{"enabled": false, "host": "", "port": 8080})
	for _, err := range []error{settings, telemetry, writeTelemetry} {
		if deviceFailure(t, err).Code != "unsupported_capability" {
			t.Fatal(err)
		}
	}
	if err := p.UpdateSettings(ctx, map[string]any{"language": "en"}); err == nil {
		t.Fatal("expected unsupported preference to be rejected")
	}
}

func TestLegacyPicoDiscoveryKeepsDevicePreferences(t *testing.T) {
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1":
			replyJSON(w, 200, map[string]any{"api_version": 1, "mode": "standalone", "recipe_capacity": 16})
		case "/api/v1/settings":
			replyJSON(w, 200, map[string]any{"language": "en"})
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	})
	ctx := context.Background()
	if _, err := p.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	prefs, err := p.Settings(ctx)
	if err != nil || prefs["language"] != "en" {
		t.Fatalf("missing legacy settings: %+v, %v", prefs, err)
	}
	if err := p.UpdateSettings(ctx, map[string]any{"language": "es"}); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveredAccessoryCommandArgumentsAndFalseValues(t *testing.T) {
	caps := domain.Capabilities{Commands: []string{"set-pump", "set-fan", "calibrate", "set-sampling-interval"}, CommandFields: map[string]domain.CommandFields{
		"set-pump":              {Allowed: []string{"enabled"}, Required: []string{"enabled"}},
		"set-fan":               {Allowed: []string{"channel", "level", "enabled"}, Required: []string{"channel", "level"}},
		"calibrate":             {Allowed: []string{"confirm"}, Required: []string{"confirm"}},
		"set-sampling-interval": {Allowed: []string{"value"}, Required: []string{"value"}},
	}}
	var sent []map[string]any
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		sent = append(sent, body)
		replyJSON(w, 202, map[string]any{"accepted": true, "command": body["command"]})
	})
	p.capabilities = &caps
	valid := []domain.Command{{Command: "set-pump", Enabled: ptr(false)}, {Command: "set-fan", Channel: ptr(2), Level: ptr(0.0), Enabled: ptr(false)}, {Command: "calibrate", Confirm: ptr(true)}, {Command: "set-sampling-interval", Value: ptr(86400.0)}}
	for _, command := range valid {
		if err := p.Command(context.Background(), command); err != nil {
			t.Fatal(err)
		}
	}
	if value, exists := sent[0]["enabled"]; !exists || value != false {
		t.Fatal("false pump setting was lost", sent)
	}
	if sent[1]["level"] != 0.0 || sent[1]["channel"] != 2.0 || sent[1]["enabled"] != false {
		t.Fatal("fan fields changed", sent[1])
	}
	invalid := []domain.Command{{Command: "set-pump"}, {Command: "set-pump", Enabled: ptr(true), Level: ptr(1.0)}, {Command: "set-fan", Channel: ptr(0), Level: ptr(10.0)}, {Command: "set-fan", Channel: ptr(1), Level: ptr(math.NaN())}, {Command: "calibrate", Confirm: ptr(false)}, {Command: "calibrate"}, {Command: "set-sampling-interval", Value: ptr(59.0)}, {Command: "set-sampling-interval", Value: ptr(60.5)}, {Command: "set-sampling-interval", Value: ptr(86401.0)}}
	for _, command := range invalid {
		if err := p.Command(context.Background(), command); err == nil {
			t.Fatalf("accepted %+v", command)
		}
	}
	if len(sent) != len(valid) {
		t.Fatal("invalid command reached device")
	}
}

func zPage(offset int, revision uint32) map[string]any {
	var next any
	if offset < 3072 {
		next = offset + 1024
	}
	return map[string]any{"width": 128, "height": 64, "rotation": 0, "format": "row-gray4-msb-hex", "offset": offset, "total_bytes": 4096, "revision": revision, "next_offset": next, "data": strings.Repeat(fmt.Sprintf("%02x", offset/1024+1), 1024)}
}
func displayOffset(r *http.Request) int {
	offset, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/v1/display/"))
	return offset
}

func TestZDisplayPagesKeepOneRevision(t *testing.T) {
	var paths []string
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		offset := displayOffset(r)
		if offset > 0 && r.URL.Query().Get("revision") != "17" {
			t.Error("display revision not pinned")
		}
		replyJSON(w, 200, zPage(offset, 17))
	})
	frame, err := p.Display(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := ""
	for i := 1; i <= 4; i++ {
		want += strings.Repeat(fmt.Sprintf("%02x", i), 1024)
	}
	if frame.Data != want || frame.NextOffset != nil || frame.Offset != nil || frame.Revision == nil || *frame.Revision != 17 {
		t.Fatal("partial or mixed frame", frame)
	}
	if len(paths) != 4 || paths[3] != "/api/v1/display/3072?revision=17" {
		t.Fatal(paths)
	}
}

func TestZDisplayChangedFrameRetriesBoundedWithoutMixing(t *testing.T) {
	for _, always := range []bool{false, true} {
		t.Run(fmt.Sprint(always), func(t *testing.T) {
			starts := 0
			p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
				offset := displayOffset(r)
				if offset == 0 {
					starts++
				}
				if offset == 1024 && (always || starts == 1) {
					replyJSON(w, 409, map[string]any{"error": "display_changed", "message": "Frame changed"})
					return
				}
				replyJSON(w, 200, zPage(offset, uint32(starts)))
			})
			frame, err := p.Display(context.Background())
			if always {
				if err == nil || starts != 3 {
					t.Fatalf("unbounded/missing rejection %d %v", starts, err)
				}
			} else if err != nil || starts != 2 || *frame.Revision != 2 || len(frame.Data) != 8192 {
				t.Fatalf("retry %d %+v %v", starts, frame, err)
			}
		})
	}
}

func TestZDisplayRejectsMalformedOrChangedPageMetadata(t *testing.T) {
	for _, change := range []string{"revision", "offset", "data", "next_offset", "width", "rotation", "total_bytes"} {
		t.Run(change, func(t *testing.T) {
			calls := 0
			p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				offset := displayOffset(r)
				page := zPage(offset, 7)
				if offset == 1024 {
					switch change {
					case "data":
						page[change] = "zz"
					case "next_offset":
						page[change] = 3072
					default:
						page[change] = 999
					}
				}
				replyJSON(w, 200, page)
			})
			if _, err := p.Display(context.Background()); err == nil || calls != 2 {
				t.Fatalf("accepted or retried malformed page: %v calls=%d", err, calls)
			}
		})
	}
}

func TestHistoryPageUsesStoredSessionFieldsAndUnits(t *testing.T) {
	fields := append(append([]string(nil), historyFields...), "temperature_f", "pressure_kpa", "battery_v", "sensor_pressure_native", "actuator_fan1_level")
	metadata := map[string]domain.FieldMetadata{"sensor_pressure_native": {Label: "Native pressure", Unit: "raw", Kind: "pressure"}, "actuator_fan1_level": {Label: "Fan 1", Unit: "%", Kind: "output"}}
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		replyJSON(w, 200, map[string]any{"id": 12, "offset": 0, "next_offset": nil, "fields": fields, "field_metadata": metadata, "samples": [][]any{{15000, nil, nil, nil, 0, 0, 0, 0, 255, 3, 2, 71.5, nil, 3.8, 532.125, 45}}})
	})
	p.historyFields = append(append([]string(nil), historyFields...), "sensor_new_field") // Current catalog differs from this archived session.
	page, err := p.HistoryPageWithSchema(context.Background(), 12, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(page.Fields, fields) || !reflect.DeepEqual(page.FieldMetadata, metadata) || len(page.Samples) != 1 {
		t.Fatalf("lost schema: %+v", page)
	}
	sample := page.Samples[0]
	if *sample.TemperatureF != 71.5 || sample.PressureKPa != nil || *sample.BatteryV != 3.8 || *sample.Readings["sensor_pressure_native"] != 532.125 || *sample.Readings["actuator_fan1_level"] != 45 {
		t.Fatal("lost named readings", sample)
	}
}

func TestHistoryRejectsDuplicateAndUnknownNamedFields(t *testing.T) {
	for _, tail := range [][]string{{"sensor_pressure", "sensor_pressure"}, {"temperature_keg"}, {"sensor_"}, {"actuator_pump_unknown"}} {
		t.Run(strings.Join(tail, ","), func(t *testing.T) {
			fields := append(append([]string(nil), historyFields...), tail...)
			p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
				replyJSON(w, 200, map[string]any{"id": 1, "offset": 0, "fields": fields, "samples": [][]any{}})
			})
			if _, _, err := p.HistoryPage(context.Background(), 1, 0); err == nil {
				t.Fatal("accepted invalid channel schema")
			}
		})
	}
}

func TestExplicitEmptyHistorySchemaIsNotLegacy(t *testing.T) {
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		replyJSON(w, 200, map[string]any{"id": 1, "offset": 0, "fields": []string{}, "samples": [][]any{}})
	})
	if _, _, err := p.HistoryPage(context.Background(), 1, 0); err == nil {
		t.Fatal("accepted an explicitly empty schema as an old firmware page")
	}
}

func TestExplicitEmptySettingsSurvivesHubSerialization(t *testing.T) {
	encoded, err := json.Marshal(domain.Capabilities{SettingsFields: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded domain.Capabilities
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SettingsFields == nil {
		t.Fatal("empty capability list became legacy fallback")
	}
}

func TestHistoryRetainsEachSessionCadenceAndRejectsInvalidBounds(t *testing.T) {
	for _, interval := range []int64{0, 15000, 60000, 300000, 86400000, -1, 1, 14999, 86400001} {
		p := testPico(t, func(w http.ResponseWriter, _ *http.Request) {
			replyJSON(w, 200, domain.History{HistorySession: domain.HistorySession{ID: 2, SampleIntervalMS: 300000}, Fields: historyFields,
				Sessions: []domain.HistorySession{{ID: 2, SampleIntervalMS: 300000}, {ID: 1, SampleIntervalMS: interval}}})
		})
		history, err := p.History(context.Background())
		valid := interval == 0 || interval >= 15000 && interval <= 86400000
		if !valid {
			if err == nil {
				t.Fatalf("accepted interval%d", interval)
			}
			continue
		}
		if err != nil || history.SampleIntervalMS != 300000 || history.Sessions[1].SampleIntervalMS != interval {
			t.Fatalf("lost interval%d: %+v %v", interval, history, err)
		}
	}
}
