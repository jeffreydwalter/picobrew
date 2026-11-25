package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/ai"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/catalog"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/usbservice"
)

type endpoint struct {
	method, path, summary string
	body                  any
}

var endpoints = []endpoint{
	{"GET", "/usb/devices", "List compatible USB adapters attached to the Hub computer and the latest firmware job", nil},
	{"POST", "/usb/jobs", "Begin an attended USB backup, installation or stock restoration", map[string]any{"action": "backup", "model": "pico-s", "port": "adapter identifier from /usb/devices"}},
	{"GET", "/usb/jobs/{id}", "Read physical firmware job progress and the current confirmation prompt", nil},
	{"POST", "/usb/jobs/{id}/confirm", "Accept this job’s current physical operation", map[string]any{"confirm": "current prompt identifier"}},
	{"POST", "/usb/jobs/{id}/cancel", "Cancel an unanswered physical operation", map[string]any{"confirm": "current prompt identifier"}},
	{"POST", "/stock-zymatics/preview", "Compile saved recipe revisions for offline Zymatic USB setup", map[string]any{"recipes": []map[string]any{{"recipe_id": "saved-recipe", "revision": 1}}}},
	{"GET", "/stock-zymatics/{machine}/recipes", "Read stock Zymatic sync menu and last request", nil},
	{"POST", "/stock-zymatics/{machine}/recipes", "Preview or publish saved recipe revisions to one stock Zymatic menu", map[string]any{"recipes": []map[string]any{{"recipe_id": "saved-recipe", "revision": 1}}, "publish": false}},
	{"DELETE", "/stock-zymatics/{machine}/recipes/{recipe_id}", "Remove a recipe from a stock Zymatic sync menu", nil},
	{"GET", "/telemetry/receiver", "Prove that an inferred receiver reaches this Hub instance", nil},
	{"GET", "/auth/session", "Read owner session and CSRF token", nil}, {"POST", "/auth/setup", "Create the first owner account", map[string]any{"token": "server-printed-setup-token", "password": "a-long-private-password"}}, {"POST", "/auth/login", "Sign in", map[string]any{"password": "your-password"}}, {"POST", "/auth/logout", "Sign out", map[string]any{}},
	{"POST", "/devices/pair", "Request pairing at the brewer", map[string]any{"base_url": "http://picobrew.local"}},
	{"GET", "/devices/discover", "Find PicoBrew machines on the Hub's directly connected local network", nil},
	{"POST", "/devices/{id}/key", "Retrieve the saved device API key for this machine", nil},
	{"POST", "/devices/{id}/website", "Open the machine website with a single-use sign-in", nil},
	{"GET", "/devices", "List fleet status", nil}, {"POST", "/devices", "Register a machine", map[string]any{"name": "My Pico", "model": "", "adapter": "pico-standalone", "base_url": "http://192.168.1.50", "api_key": "64-hex-character-device-key", "location": "Brew room"}},
	{"GET", "/devices/{id}/events", "Read recent volatile events from the connected machine", nil},
	{"GET", "/devices/{id}/picostill", "Read the Z’s paired PicoStill and verification status", nil},
	{"PUT", "/devices/{id}/picostill", "Pair a registered PicoStill or a stock 0.0.35 PicoStill by LAN address and UID", map[string]any{"picostill_id": "registered-accessory-id"}},
	{"PATCH", "/devices/{id}/picostill", "Choose whether to record PicoStill sessions", map[string]any{"session_logging": true}},
	{"DELETE", "/devices/{id}/picostill", "Remove the PicoStill connection from an idle Z", nil},
	{"GET", "/devices/{id}/picoferms", "List PicoFerms, batches and fermentation recordings for a brewer", nil},
	{"PUT", "/devices/{id}/picoferms", "Assign or unlink a PicoFerm, optionally attaching an explicit batch and recording", map[string]any{"picoferm_id": "registered-picoferm-id", "revision": 0}},
	{"GET", "/devices/{id}/brewer", "Read the brewer associated with this PicoFerm", nil},
	{"PUT", "/devices/{id}/brewer", "Choose a brewer for this PicoFerm, or clear its association", map[string]any{"brewer_id": "registered-brewer-id", "revision": 0}},
	{"GET", "/devices/{id}", "Read one machine", nil}, {"PUT", "/devices/{id}", "Update a machine connection", map[string]any{"name": "My Pico", "model": "", "adapter": "pico-standalone", "base_url": "http://192.168.1.50", "location": "Brew room"}}, {"DELETE", "/devices/{id}", "Remove an idle machine", nil}, {"POST", "/devices/{id}/test", "Probe connection and discover capabilities", map[string]any{}}, {"GET", "/devices/{id}/display", "Read raw OLED framebuffer and display geometry", nil}, {"POST", "/devices/{id}/commands", "Queue a capability-checked command once", map[string]any{"command": "encoder", "delta": 1}}, {"GET", "/devices/{id}/recipes", "Read recipes stored on the appliance", nil}, {"GET", "/devices/{id}/settings", "Read device preferences", nil}, {"PATCH", "/devices/{id}/settings", "Update device preferences", map[string]any{"temperature_unit": "F"}}, {"GET", "/devices/{id}/telemetry", "Read the saved live-data receiver settings", nil}, {"PUT", "/devices/{id}/telemetry", "Save live-data receiver settings and apply them when the machine is available", map[string]any{"enabled": true, "host": "192.168.1.20", "port": 8080}}, {"POST", "/devices/{id}/sync-history", "Archive retained device history", map[string]any{}}, {"POST", "/devices/{id}/deploy", "Compile and deploy a reviewed recipe without starting it", map[string]any{"recipe_id": "recipe-id", "profile_id": "pico-pro", "batch_liters": 5, "source_revision": 1}},
	{"GET", "/profiles", "List planning and control capabilities by model", nil}, {"GET", "/recipes", "List shared recipes", nil}, {"POST", "/recipes", "Create a recipe", map[string]any{"name": "New recipe", "batch_liters": 5, "efficiency": 55, "ingredients": []any{}, "steps": []any{map[string]any{"name": "Mash", "kind": "mash", "target_c": 67, "minutes": 60, "location": 0, "drain_minutes": 0}}}}, {"GET", "/recipes/{id}", "Read a recipe", nil}, {"PUT", "/recipes/{id}", "Save a recipe using its current revision", map[string]any{"revision": 1}}, {"DELETE", "/recipes/{id}", "Remove a recipe from the library", nil}, {"POST", "/recipes/import", "Import JSON or BeerXML", map[string]any{"format": "beerxml", "data": "<RECIPES>...</RECIPES>"}}, {"POST", "/recipes/{id}/adapt", "Preview a machine-specific recipe adaptation", map[string]any{"profile_id": "pico-pro", "batch_liters": 5}}, {"GET", "/recipes/{id}/export", "Export recipe as JSON or BeerXML (?format=)", nil}, {"GET", "/recipes/{id}/revisions", "Read saved recipe revisions", nil},
	{"GET", "/brew-groups", "List brewing groups and known Zymatic menus", nil},
	{"POST", "/brew-groups", "Create a brewing group", map[string]any{"name": "Brew day", "device_ids": []string{"first", "second"}, "revision": 0}},
	{"PUT", "/brew-groups/{group}", "Edit a brewing group", map[string]any{"name": "Brew day", "device_ids": []string{"first", "second"}, "revision": 1}},
	{"DELETE", "/brew-groups/{group}", "Remove a brewing group (?revision=N)", nil},
	{"GET", "/brew-invites", "List current group invitations", nil},
	{"GET", "/devices/{id}/brew-invite", "Read a brewer's group invitation", nil},
	{"POST", "/devices/{id}/brew-invite/{invite}/respond", "Accept or decline a pending invitation", map[string]any{"accept": false}},
	{"GET", "/brews", "List aggregated brew history", nil}, {"GET", "/brews/{id}", "Read all recorded sensors and phases for a brew", nil}, {"PATCH", "/brews/{id}", "Save tasting notes and rating", map[string]any{"notes": "", "rating": 4}}, {"POST", "/brews/{id}/conditioning", "Update manual fermentation and carbonation progress", nil}, {"GET", "/brews/{id}/export", "Export full telemetry (?format=csv|json)", nil}, {"GET", "/events", "Stream fleet updates using server-sent events", nil},
	{"POST", "/telemetry", "Receive unsigned observation-only Pico forwarding", map[string]any{"schema_version": 1, "firmware_version": "1.0.0", "status": map[string]any{"device_uid": "known-uid"}}}, {"GET", "/ai/settings", "Read redacted AI provider configuration", nil}, {"PUT", "/ai/settings", "Configure optional subscription or API-key access", map[string]any{"provider": "openai", "model": "your-model", "api_key": "your-api-key", "enabled": true, "monitor_enabled": false}}, {"POST", "/ai/assist", "Generate a draft, assess live telemetry, or analyze a completed brew", map[string]any{"mode": "recipe", "prompt": "Draft a five-liter porter"}}, {"GET", "/backup", "Export records and telemetry without credentials", nil}, {"GET", "/shopping-list", "Aggregate ingredients (?recipes=id1,id2)", nil}, {"GET", "/openapi.json", "Read this API catalog", nil},
	{"GET", "/ai/advice", "Read up to 500 retained AI advice records, filtered by machine, brew and mode", nil},
	{"GET", "/devices/{id}/ai-monitor", "Read session-specific AI monitoring and current verified brew", nil},
	{"PUT", "/devices/{id}/ai-monitor", "Enable or disable AI monitoring for the exact current brew", map[string]any{"enabled": true, "brew_id": "device-native-history-id"}},
	{"GET", "/ai/subscription", "Read subscription connection status without credentials", nil},
	{"DELETE", "/ai/subscription", "Disconnect the local ChatGPT subscription while preserving its app registration", nil},
	{"POST", "/ai/subscription/login", "Start ChatGPT browser sign-in", map[string]any{"provider": "openai", "redirect_uri": chatGPTCallbackURL}},
	{"POST", "/ai/subscription/callback", "Complete ChatGPT sign-in using its full callback address", map[string]any{"provider": "openai", "callback_url": chatGPTCallbackURL + "?code=authorization-code&state=state&client_id=issued-client-id"}},
	{"GET", "/ai/models", "List models for the selected subscription or API key", nil},
	{"POST", "/ai/models", "Connect and list models without saving credentials or enabling AI", map[string]any{"provider": "openai", "api_key": "your-api-key"}},
	{"PUT", "/profiles/{id}", "Save measured batch, efficiency and grain limits", map[string]any{"batch_liters": 5, "efficiency": 70, "max_grain_kg": 0}},
	{"DELETE", "/profiles/{id}", "Restore the model's default planning profile", nil},
	{"DELETE", "/devices/{id}/recipes/{recipe_id}", "Remove an installed recipe, optionally checking the selected copy", map[string]any{"expected": domain.MachineRecipe{}}},
	{"PUT", "/devices/{id}/recipes/{recipe_id}", "Edit an installed program after checking the previous value", map[string]any{"recipe": domain.MachineRecipe{}, "expected": domain.MachineRecipe{}}},
	{"POST", "/devices/{id}/deployments", "Preview a capacity-limited bulk recipe transfer", map[string]any{"profile_id": "pico-pro", "overwrite_existing": false, "items": []any{map[string]any{"recipe_id": "library-recipe-id", "source_revision": 1, "batch_liters": 0}}}},
	{"GET", "/devices/{id}/deployments/{deployment_id}", "Read the saved transfer preview or receipt", nil},
	{"POST", "/devices/{id}/deployments/{deployment_id}/execute", "Execute a current transfer preview once, without starting a brew", map[string]any{}},
	{"GET", "/catalog", "Browse pinned community recipes and native programs", nil},
	{"GET", "/catalog/references", "List attributed support references and bundled artwork", nil},
	{"GET", "/catalog/assets/{asset_path}", "Read a manifest-listed local image or support reference", nil},
	{"GET", "/catalog/{id}", "Read an immutable source entry and conversion provenance", nil},
	{"POST", "/catalog/{id}/copy", "Copy the ingredient recipe or its matched stock Zymatic program", map[string]any{"stock_program": false}},
	{"GET", "/catalog/{id}/source", "Download the exact original source JSON", nil},
	{"GET", "/firmware/updates", "Read the daily version check and registered machine update notices", nil},
	{"POST", "/firmware/updates", "Refresh firmware version metadata now", nil},
	{"POST", "/firmware/releases/{packageID}/stage", "Download and verify a selected release or reuse its cached archive", nil},
	{"GET", "/firmware/settings", "Read the saved GitHub firmware source", nil},
	{"PUT", "/firmware/settings", "Save the public GitHub repository and optional revision", map[string]any{"repository": "jeffreydwalter/picobrew", "ref": ""}},
	{"GET", "/firmware/github", "Inspect available firmware packages at one resolved GitHub commit", nil},
	{"POST", "/firmware/github/import", "Fetch and validate a model package from a pinned GitHub commit", map[string]any{"repository": "jeffreydwalter/picobrew", "commit": "2bab80ef8f88daf0c450fa1b6c4684a8c2d08669", "model": "pico-s"}},
	{"GET", "/firmware/packages", "List validated firmware packages saved in the Hub", nil},
	{"POST", "/firmware/packages", "Upload and validate a complete firmware ZIP", nil},
	{"GET", "/firmware/packages/{packageID}", "Read saved package metadata and acquisition provenance", nil},
	{"DELETE", "/firmware/packages/{packageID}", "Remove a firmware package from the Hub", nil},
	{"GET", "/firmware/packages/{packageID}/download", "Download the package after verifying its cached archive hash", nil},
	{"POST", "/devices/{id}/firmware/stock-started", "Record owner observation of OEM startup after both original firmware images were verified", map[string]any{"device_uid": "authenticated-device-uid", "started": "2026-01-01T00:00:00Z"}},
	{"POST", "/devices/{id}/firmware/restore-stock", "Restore verified original firmware over Wi-Fi on a qualified Pico S/Pro", map[string]any{"device_uid": "authenticated-device-uid", "acknowledge_loss_of_hub": true, "confirm_restore": true}},
	{"POST", "/devices/{id}/firmware/install", "Install a validated OTA package on an idle, recovery-equipped Pico S/Pro", map[string]any{"device_uid": "authenticated-device-uid", "package_id": strings.Repeat("a", 64), "confirm": true}},
	{"GET", "/devices/{id}/firmware", "Read model-scoped firmware packages and installation availability", nil},
	{"PUT", "/devices/{id}/firmware/hardware", "Record an independently verified C display controller for this pinned appliance", map[string]any{"device_uid": "authenticated-device-uid", "display_variant": "ssd1325"}},
}

func (s *Server) openapi(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, openAPIDocument())
}

type schemaMap = map[string]any

func schemaRef(name string) schemaMap       { return schemaMap{"$ref": "#/components/schemas/" + name} }
func schemaArray(items schemaMap) schemaMap { return schemaMap{"type": "array", "items": items} }
func schemaObject(properties schemaMap, required ...string) schemaMap {
	v := schemaMap{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		v["required"] = required
	}
	return v
}
func stringSchema() schemaMap { return schemaMap{"type": "string"} }
func numberSchema() schemaMap { return schemaMap{"type": "number", "format": "double"} }
func boolSchema() schemaMap   { return schemaMap{"type": "boolean"} }

// Schemas follow the JSON tags, including flattened embedded structs. Response
// types and request constraints are kept separate: clients do not need to submit
// server-assigned IDs/timestamps just because responses contain those fields.
type schemaRegistry struct{ values schemaMap }

func (r *schemaRegistry) describe(t reflect.Type) schemaMap {
	if t == reflect.TypeOf(time.Time{}) {
		return schemaMap{"type": "string", "format": "date-time"}
	}
	if t == reflect.TypeOf(json.RawMessage{}) {
		return schemaMap{}
	}
	if t.Kind() == reflect.Pointer {
		inner := r.describe(t.Elem())
		if _, ok := inner["$ref"]; ok {
			return schemaMap{"allOf": []any{inner}, "nullable": true}
		}
		copy := schemaMap{}
		for k, v := range inner {
			copy[k] = v
		}
		copy["nullable"] = true
		return copy
	}
	switch t.Kind() {
	case reflect.Struct:
		if t.Name() != "" {
			if _, exists := r.values[t.Name()]; exists {
				return schemaRef(t.Name())
			}
			r.values[t.Name()] = schemaMap{}
		}
		props := schemaMap{}
		required := []string{}
		var fields func(reflect.Type)
		fields = func(structure reflect.Type) {
			for i := 0; i < structure.NumField(); i++ {
				field := structure.Field(i)
				if field.PkgPath != "" {
					continue
				}
				parts := strings.Split(field.Tag.Get("json"), ",")
				name := parts[0]
				if name == "-" {
					continue
				}
				if field.Anonymous && name == "" {
					embedded := field.Type
					if embedded.Kind() == reflect.Pointer {
						embedded = embedded.Elem()
					}
					if embedded.Kind() == reflect.Struct {
						fields(embedded)
						continue
					}
				}
				if name == "" {
					name = field.Name
				}
				props[name] = r.describe(field.Type)
				optional := false
				for _, option := range parts[1:] {
					if option == "omitempty" || option == "omitzero" {
						optional = true
					}
				}
				if !optional {
					required = append(required, name)
				}
			}
		}
		fields(t)
		result := schemaObject(props, required...)
		if t.Name() != "" {
			r.values[t.Name()] = result
			return schemaRef(t.Name())
		}
		return result
	case reflect.Slice, reflect.Array:
		result := schemaArray(r.describe(t.Elem()))
		if t.Kind() == reflect.Slice {
			result["nullable"] = true
		}
		return result
	case reflect.Map:
		return schemaMap{"type": "object", "additionalProperties": r.describe(t.Elem()), "nullable": true}
	case reflect.Bool:
		return boolSchema()
	case reflect.String:
		return stringSchema()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return schemaMap{"type": "integer", "format": "int64"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return schemaMap{"type": "integer", "minimum": 0}
	case reflect.Float32, reflect.Float64:
		return numberSchema()
	default:
		return schemaMap{}
	}
}

func (r *schemaRegistry) properties(name string) schemaMap {
	return r.values[name].(schemaMap)["properties"].(schemaMap)
}

func buildSchemas() schemaMap {
	r := &schemaRegistry{values: schemaMap{}}
	r.describe(reflect.TypeOf(LANDiscovery{}))
	for _, value := range []any{brewGroup{}, brewInvite{}, groupCandidate{}, device.PicoStillLink{}, usbservice.USBDevices{}, usbservice.USBInput{}, usbservice.USBAnswer{}, ConditioningInput{}, domain.Device{}, domain.DeviceView{}, domain.DeviceEvents{}, domain.Capabilities{}, domain.Status{}, domain.Display{}, domain.Recipe{}, domain.MachineRecipe{}, domain.Profile{}, domain.Adapted{}, domain.Brew{}, domain.BrewDetail{}, domain.Sample{}, domain.History{}, domain.Command{}, commandReceipt{}, ai.Config{}, ai.Request{}, ai.Result{}, adviceRecord{}, aiMonitorStatus{}, aiModelList{}, ai.SubscriptionStatus{}, BulkDeployment{}, DeploymentInput{}, MachineRecipeUpdate{}, catalog.Page{}, catalog.Detail{}, catalog.References{}} {
		r.describe(reflect.TypeOf(value))
	}
	// Preserve the previously published schema name while sharing this record
	// with individual brew details and exports.
	r.values["adviceRecord"] = schemaRef("AIAdvice")
	for _, name := range []string{"Recipe", "Detail"} {
		r.properties(name)["machine_versions"] = schemaMap{"type": "array", "items": schemaRef("Adapted"), "readOnly": true, "description": "Current model adaptations on ingredient recipe detail, save and import responses."}
	}
	r.properties("DeviceView")["key_reused"] = schemaMap{"type": "boolean", "readOnly": true, "description": "Advisory: another registered physical machine has the same recorded hexadecimal API key, including disabled machines. Derived in memory; no key or fingerprint is returned. Use a separately provisioned key for each brewer. Saving a connection does not change its firmware key."}
	r.describe(reflect.TypeOf(brewerFermentationOptions{}))
	r.describe(reflect.TypeOf(brewerPicoFermInput{}))
	r.values["BrewGroupInput"] = schemaObject(schemaMap{
		"name":       schemaMap{"type": "string", "minLength": 1, "maxLength": 80},
		"device_ids": schemaMap{"type": "array", "items": stringSchema(), "minItems": 2, "maxItems": 16, "uniqueItems": true},
		"revision":   schemaMap{"type": "integer", "minimum": 0, "description": "Use the current revision when editing a group."},
	}, "name", "device_ids")
	r.values["PicoFermBrewer"] = schemaObject(schemaMap{
		"brewer_id": schemaMap{"type": "string", "description": "Registered physical brewer ID. An empty string clears the association."},
		"revision":  schemaMap{"type": "integer", "minimum": 0, "description": "Use the current association revision when saving."},
	}, "brewer_id", "revision")
	r.values["Error"] = schemaObject(schemaMap{"error": schemaObject(schemaMap{"code": stringSchema(), "message": stringSchema()}, "code", "message")}, "error")
	r.values["AuthSession"] = schemaObject(schemaMap{"authenticated": boolSchema(), "setup_required": boolSchema(), "version": stringSchema(), "demo": boolSchema(), "csrf_token": stringSchema()}, "authenticated", "setup_required", "version", "demo")
	r.values["Authenticated"] = schemaObject(schemaMap{"authenticated": boolSchema()}, "authenticated")
	r.values["Deleted"] = schemaObject(schemaMap{"deleted": boolSchema()}, "deleted")
	r.values["Synced"] = schemaObject(schemaMap{"synced": boolSchema()}, "synced")
	r.values["Accepted"] = schemaObject(schemaMap{"accepted": boolSchema()}, "accepted")
	r.values["DeviceRecipes"] = schemaObject(schemaMap{"recipes": schemaArray(schemaRef("MachineRecipe"))}, "recipes")
	r.values["MachineRecipeUpdated"] = schemaObject(schemaMap{"updated": boolSchema(), "started": boolSchema(), "recipe": schemaRef("MachineRecipe")}, "updated", "started", "recipe")
	r.values["Deployment"] = schemaObject(schemaMap{"deployed": boolSchema(), "started": boolSchema(), "device_id": stringSchema(), "recipe": schemaRef("MachineRecipe"), "warnings": schemaArray(stringSchema()), "deployment_id": stringSchema()}, "deployed", "started", "device_id", "recipe", "warnings", "deployment_id")
	r.values["Settings"] = schemaMap{"type": "object", "properties": schemaMap{"temperature_unit": schemaMap{"type": "string", "enum": []string{"F", "C"}}, "language": schemaMap{"type": "string", "enum": []string{"en", "es", "fr"}}, "detailed_status": boolSchema(), "auto_cycle": boolSchema()}, "additionalProperties": true}
	r.values["Telemetry"] = schemaMap{"type": "object", "properties": schemaMap{"enabled": boolSchema(), "host": stringSchema(), "port": schemaMap{"type": "integer", "minimum": 1, "maximum": 65535}, "state": stringSchema()}, "additionalProperties": true}
	r.values["Login"] = schemaObject(schemaMap{"password": schemaMap{"type": "string", "maxLength": 256, "writeOnly": true}}, "password")
	r.values["Setup"] = schemaObject(schemaMap{"password": schemaMap{"type": "string", "minLength": 12, "maxLength": 256, "writeOnly": true}, "token": schemaMap{"type": "string", "writeOnly": true}}, "password", "token")
	r.values["DeviceInput"] = schemaObject(schemaMap{"name": schemaMap{"type": "string", "minLength": 1, "maxLength": 100}, "model": schemaMap{"type": "string", "enum": []string{"", "auto", "pico-pro", "pico-s", "pico-c", "z-series", "picostill", "picoferm", "z", "zymatic"}, "description": "Omit/empty for automatic physical firmware identification. Simulator requires an explicit profile. Manual S/Pro selection supports older firmware that does not report a model."}, "adapter": schemaMap{"type": "string", "enum": []string{"pico-standalone", "simulator"}}, "base_url": schemaMap{"type": "string", "description": "Explicit appliance HTTP base URL; required for pico-standalone. Supported standalone families include C/S/Pro, generic Z-series, PicoStill, PicoFerm and custom Zymatic over Ethernet or supported Wi-Fi; only discovered capabilities are enabled."}, "api_key": schemaMap{"type": "string", "writeOnly": true, "description": "Standalone firmware key; omit/leave blank on update to preserve the stored key."}, "location": stringSchema(), "enabled": boolSchema()}, "name", "adapter")
	recipeProps := schemaMap{}
	for k, v := range r.properties("Recipe") {
		recipeProps[k] = v
	}
	for _, name := range []string{"id", "created_at", "updated_at"} {
		recipeProps[name] = schemaMap{"type": "string", "readOnly": true}
	}
	r.values["RecipeInput"] = schemaObject(recipeProps, "name", "batch_liters", "efficiency", "steps")
	r.properties("Recipe")["batch_liters"] = schemaMap{"type": "number", "minimum": 0, "maximum": 10000, "description": "Positive for an ingredient recipe; exactly zero for a native program with no inferred batch volume."}
	r.properties("Recipe")["efficiency"] = schemaMap{"type": "number", "minimum": 0, "maximum": 100, "description": "Positive measured percentage for an ingredient recipe; exactly zero for a native program."}
	r.properties("Recipe")["steps"] = schemaMap{"type": "array", "maxItems": 256, "items": schemaRef("RecipeStep"), "description": "Ingredient recipes require 1–256 canonical steps. Native programs use native_program and leave this list empty."}
	for _, name := range []string{"batch_liters", "efficiency", "steps"} {
		recipeProps[name] = r.properties("Recipe")[name]
	}
	r.properties("RecipeStep")["location"] = schemaMap{"type": "integer", "minimum": 0, "maximum": 6, "description": "Zero is unmapped. Deployment needs an explicitly verified 1–6; the hub never infers plumbing."}
	r.properties("Adapted")["deployment_blockers"] = schemaMap{"type": "array", "items": stringSchema(), "description": "Reasons the valid planning recipe cannot be compiled or deployed. Missing chambers and unsupported native instructions preserve the scaled recipe and schedule; machine_recipe remains null and can_deploy is false."}
	r.properties("Adapted")["profile_fingerprint"] = schemaMap{"type": "string", "readOnly": true, "description": "Identity of the profile used for this preview. Send it with deployment to reject a preview whose equipment calibration has changed."}
	r.properties("RecipeStep")["target_c"] = schemaMap{"type": "number", "minimum": 0, "maximum": 100, "description": "Canonical Celsius; zero is the unheated program sentinel."}
	r.properties("RecipeStep")["minutes"] = schemaMap{"type": "integer", "minimum": 0, "maximum": 43200, "description": "Zero is allowed only for explicit heat/cool targets. Native S/Pro deployment limits holds to 599 minutes and does not map cooling steps."}
	r.properties("RecipeStep")["drain_minutes"] = schemaMap{"type": "integer", "minimum": 0, "maximum": 1440, "description": "Native S/Pro deployment limits this to 120 minutes."}
	r.properties("Ingredient")["attenuation"] = schemaMap{"type": "number", "minimum": 0, "maximum": 100, "description": "Expected apparent yeast attenuation, as a percentage."}
	r.properties("Ingredient")["kind"] = schemaMap{"type": "string", "enum": []string{"fermentable", "hop", "water", "yeast", "misc"}}
	r.properties("Ingredient")["unit"] = schemaMap{"type": "string", "enum": []string{"kg", "g", "L", "mL", "count"}, "description": "Fermentable: kg; hop: g; water: L; yeast/misc: g, mL, count."}
	r.properties("MachineRecipe")["id"] = schemaMap{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,20}$"}
	r.properties("MachineRecipe")["name"] = schemaMap{"type": "string", "minLength": 1, "maxLength": 31, "pattern": "^[ -~]+$"}
	r.properties("MachineRecipe")["steps"] = schemaMap{"type": "array", "minItems": 1, "maxItems": 15, "items": schemaRef("MachineStep")}
	r.values["MachineStep"].(schemaMap)["required"] = []string{"name", "location", "target_f", "minutes"}
	r.properties("MachineStep")["name"] = schemaMap{"type": "string", "minLength": 1, "maxLength": 21, "pattern": "^[ -~]+$"}
	r.properties("MachineStep")["location"] = schemaMap{"type": "integer", "minimum": 1, "maximum": 6}
	r.properties("MachineStep")["target_f"] = schemaMap{"oneOf": []any{schemaMap{"type": "integer", "enum": []int{0}}, schemaMap{"type": "integer", "minimum": 60, "maximum": 212}}}
	r.properties("MachineStep")["minutes"] = schemaMap{"type": "integer", "minimum": 0, "maximum": 599, "description": "Zero advances after the target is reached; requires firmware zero_minute_steps capability."}
	r.properties("MachineStep")["drain_minutes"] = schemaMap{"type": "integer", "minimum": 0, "maximum": 120}
	r.values["NativeSourceStep"] = schemaObject(schemaMap{"name": schemaMap{"type": "string", "minLength": 1, "maxLength": 512}, "location": schemaMap{"type": "integer", "minimum": 0, "maximum": 6}, "target_f": schemaMap{"type": "integer", "minimum": 0, "maximum": 212}, "minutes": schemaMap{"type": "integer", "minimum": 0, "maximum": 43200}, "drain_minutes": schemaMap{"type": "integer", "minimum": 0, "maximum": 1440}}, "name", "location", "target_f", "minutes")
	r.values["NativeSourceProgram"] = schemaObject(schemaMap{"id": schemaMap{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,20}$"}, "name": schemaMap{"type": "string", "minLength": 1, "maxLength": 512}, "steps": schemaMap{"type": "array", "minItems": 1, "maxItems": 256, "items": schemaRef("NativeSourceStep")}}, "id", "name", "steps")
	r.properties("Recipe")["native_program"] = schemaMap{"allOf": []any{schemaRef("NativeSourceProgram")}, "nullable": true, "description": "Exact archived source schedule, which can exceed a target brewer's limits. Adaptation validates the selected machine before deployment."}
	r.properties("Recipe")["kind"] = schemaMap{"type": "string", "enum": []string{"", "recipe", "native-program", "maintenance"}}
	recipeProps["native_program"] = r.properties("Recipe")["native_program"]
	recipeProps["kind"] = r.properties("Recipe")["kind"]
	command := schemaMap{}
	for k, v := range r.properties("Command") {
		command[k] = v
	}
	command["confirm"] = schemaMap{"type": "boolean", "description": "Explicit Hub confirmation for a requested operation. PicoFerm calibrate additionally forwards confirm:true to the device only after the user confirms the unit is removed from the keg/adapter, its GRAY seal is removed and it is depressurized."}
	command["ai_monitor"] = schemaMap{"type": "boolean", "default": false, "description": "Explicit opt-in for this confirmed brew/manual start only. An accepted command creates a 15-minute pending consent bound to the appliance and next matching native history session. No AI request or machine command is performed by the monitor until that session is verified."}
	command["command"] = schemaMap{"type": "string", "description": "Must appear in this device's capabilities.commands. Accessory arguments must match command_fields.allowed and command_fields.required. Devices without recipes or front_panel do not accept those operations."}
	r.values["CommandInput"] = schemaObject(command, "command")
	r.values["AIMonitorInput"] = schemaObject(schemaMap{"enabled": boolSchema(), "brew_id": schemaMap{"type": "string", "description": "Required when enabling; must exactly match current_brew_id returned by the live monitor endpoint."}}, "enabled")
	r.properties("aiMonitorStatus")["state"] = schemaMap{"type": "string", "enum": []string{"off", "pending", "active", "finished"}}
	r.properties("Config")["monitor_enabled"] = schemaMap{"type": "boolean", "deprecated": true, "description": "Retained for older saved configurations; this flag alone never enables monitoring. Consent is explicit for each verified brew."}
	r.properties("Request")["brew_id"] = schemaMap{"type": "string", "description": "Required for live and analysis modes. Live mode also requires device_id and verifies that this exact native history session is currently recording."}
	r.values["CatalogCopyInput"] = schemaObject(schemaMap{"stock_program": schemaMap{"type": "boolean", "default": false, "description": "Save the matched stock Zymatic program and its brew sheet."}})
	r.values["AdaptInput"] = schemaObject(schemaMap{"profile_id": stringSchema(), "batch_liters": numberSchema()}, "profile_id")
	r.values["DeployInput"] = schemaObject(schemaMap{"recipe_id": stringSchema(), "replace_expected": schemaRef("MachineRecipe"), "profile_id": stringSchema(), "batch_liters": numberSchema(), "source_revision": schemaMap{"type": "integer", "minimum": 1}, "profile_fingerprint": schemaMap{"type": "string", "description": "Copy from the reviewed adaptation. A changed profile returns 409 before any machine write. Optional for compatibility with existing direct API clients."}}, "recipe_id", "source_revision")
	r.values["ProfileInput"] = schemaObject(schemaMap{"batch_liters": schemaMap{"type": "number", "minimum": 0, "exclusiveMinimum": true, "description": "Maximum is the model batch volume."}, "efficiency": schemaMap{"type": "number", "minimum": 0, "exclusiveMinimum": true, "maximum": 100}, "max_grain_kg": schemaMap{"type": "number", "minimum": 0, "description": "Up to the model planning ceiling in kilograms. Zero restores the default."}, "max_hop_slot_g": schemaMap{"type": "number", "minimum": 0, "description": "Combined grams per hop slot. Zero restores the model default. Omit to preserve the current setting."}}, "batch_liters", "efficiency", "max_grain_kg")
	r.values["ImportInput"] = schemaObject(schemaMap{"format": schemaMap{"type": "string", "enum": []string{"json", "beerxml"}}, "data": schemaMap{"type": "string", "maxLength": 1048576, "description": "Canonical recipe JSON object/array or supported BeerXML 1 text. DTDs and external entities are rejected."}}, "format", "data")
	r.values["BrewNotes"] = schemaObject(schemaMap{"notes": schemaMap{"type": "string", "maxLength": 32000}, "rating": schemaMap{"type": "integer", "minimum": 0, "maximum": 5}}, "notes", "rating")
	r.values["TelemetryInput"] = schemaObject(schemaMap{"mode": schemaMap{"type": "string", "enum": []string{"automatic", "manual"}, "deprecated": true, "description": "Deprecated compatibility field. New clients send enabled, host and port without a mode. Legacy automatic requests only seed the default receiver; all saved values persist afterward."}, "enabled": boolSchema(), "host": schemaMap{"type": "string", "maxLength": 127, "description": "Hostname or IPv4 without scheme, port or path; empty only when disabled."}, "port": schemaMap{"type": "integer", "minimum": 1, "maximum": 65535}})
	settingsProps := r.values["Settings"].(schemaMap)["properties"].(schemaMap)
	r.values["SettingsInput"] = schemaObject(settingsProps)
	r.values["SettingsInput"].(schemaMap)["minProperties"] = 1
	r.values["ForwardingEnvelope"] = schemaObject(schemaMap{"schema_version": schemaMap{"type": "integer", "enum": []int{1}}, "firmware_version": stringSchema(), "status": schemaRef("Status")}, "schema_version", "status")
	aiConfig := schemaMap{}
	for k, v := range r.properties("Config") {
		aiConfig[k] = v
	}
	aiConfig["provider"] = schemaMap{"type": "string", "enum": []string{"openai", "anthropic", "gemini"}}
	aiConfig["api_key"] = schemaMap{"type": "string", "maxLength": 1024, "writeOnly": true, "description": "Provider API key. Leave blank to preserve the stored key for this provider."}
	aiConfig["auth_mode"] = schemaMap{"type": "string", "enum": []string{"api_key", "subscription"}, "default": "api_key", "description": "Explicit billing mode. Subscription failures never fall back to an API key."}
	aiConfig["model"] = schemaMap{"type": "string", "maxLength": 512, "description": "Provider model ID. Model discovery is available before choosing a model or enabling AI."}
	r.values["AISettingsInput"] = schemaObject(aiConfig, "provider")
	r.values["AIModelsInput"] = schemaObject(schemaMap{
		"provider":  schemaMap{"type": "string", "enum": []string{"openai", "anthropic", "gemini"}},
		"auth_mode": schemaMap{"type": "string", "enum": []string{"api_key", "subscription"}, "default": "api_key"},
		"api_key":   schemaMap{"type": "string", "maxLength": 1024, "writeOnly": true, "description": "API-key mode only. Optional unsaved key, used only for model discovery. Omit to use this provider's saved key. This operation does not change settings or credentials."},
	}, "provider")
	r.values["AISubscriptionLoginInput"] = schemaObject(schemaMap{"provider": schemaMap{"type": "string", "enum": []string{"openai"}}, "redirect_uri": schemaMap{"type": "string", "enum": []string{chatGPTCallbackURL}}}, "provider")
	r.values["AISubscriptionCallbackInput"] = schemaObject(schemaMap{"provider": schemaMap{"type": "string", "enum": []string{"openai"}}, "callback_url": schemaMap{"type": "string", "maxLength": 16384, "writeOnly": true, "description": "Full loopback callback address containing a single-use authorization code. Never logged or exported."}}, "provider", "callback_url")
	r.properties("aiModelList")["source"] = schemaMap{"type": "string", "enum": []string{"api", "subscription", "subscription-aliases"}, "description": "Claude Code exposes stable native model aliases; these are labeled distinctly from a live account catalog."}
	r.properties("aiModelList")["models"] = schemaMap{"type": "array", "items": schemaRef("Model"), "description": "Provider model IDs or documented Claude Code model aliases, as indicated by source. Availability does not establish plan eligibility or inference compatibility."}
	r.properties("Request")["mode"] = schemaMap{"type": "string", "enum": []string{"recipe", "live", "analysis"}}
	r.properties("Request")["prompt"] = schemaMap{"type": "string", "maxLength": 16000}
	r.properties("Result")["recipe"] = schemaRef("Recipe")
	r.properties("AIAdvice")["recipe"] = schemaRef("Recipe")
	addFirmwareSchemas(r.values)
	return r.values
}

func addFirmwareSchemas(values schemaMap) {
	hash := schemaMap{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	commit := schemaMap{"type": "string", "pattern": "^[0-9a-f]{40}$"}
	model := schemaMap{"type": "string", "enum": []string{"pico-s", "pico-pro", "pico-c"}}
	displayVariant := schemaMap{"type": "string", "enum": []string{"ssd1325", "ssd1309"}, "description": "Required for C firmware. A compiled display identity is not physical hardware detection."}
	sourceProperties := schemaMap{
		"repository": schemaMap{"type": "string", "maxLength": 300, "description": "Public GitHub owner/repository; an exact GitHub repository HTTPS URL is also accepted on save. Defaults to jeffreydwalter/picobrew."},
		"ref":        schemaMap{"type": "string", "maxLength": 200, "default": "", "description": "Branch, tag, or commit; empty uses the repository's default branch."},
	}
	values["FirmwareSettings"] = schemaObject(sourceProperties, "repository", "ref")
	values["FirmwareSettingsInput"] = schemaObject(sourceProperties, "repository")
	values["FirmwareStockObservation"] = schemaObject(schemaMap{"device_uid": stringSchema(), "started": stringSchema()}, "device_uid", "started")
	values["FirmwareStockInput"] = schemaObject(schemaMap{"device_uid": stringSchema(), "acknowledge_loss_of_hub": schemaMap{"type": "boolean", "enum": []bool{true}}, "confirm_restore": schemaMap{"type": "boolean", "enum": []bool{true}}}, "device_uid", "acknowledge_loss_of_hub", "confirm_restore")
	values["FirmwareStockReceipt"] = schemaObject(schemaMap{"device_uid": stringSchema(), "model": stringSchema(), "stage": stringSchema(), "state": stringSchema(), "started": stringSchema(), "message": stringSchema()}, "device_uid", "model", "stage", "state", "started", "message")
	values["FirmwareInstallInput"] = schemaObject(schemaMap{"device_uid": stringSchema(), "package_id": hash, "confirm": schemaMap{"type": "boolean", "enum": []bool{true}}}, "device_uid", "package_id", "confirm")
	values["FirmwareInstallReceipt"] = schemaObject(schemaMap{"replaces_package_id": hash, "device_uid": stringSchema(), "model": stringSchema(), "package_id": hash, "started": stringSchema(), "stage": stringSchema(), "state": stringSchema(), "message": stringSchema()}, "device_uid", "model", "package_id", "started", "state", "message")
	values["FirmwareImportInput"] = schemaObject(schemaMap{"repository": stringSchema(), "commit": commit, "model": model, "display_variant": displayVariant}, "repository", "commit", "model")
	values["FirmwareHardwareInput"] = schemaObject(schemaMap{"device_uid": stringSchema(), "display_variant": displayVariant}, "device_uid", "display_variant")
	values["FirmwareHardware"] = schemaObject(schemaMap{"device_uid": stringSchema(), "display_variant": displayVariant, "verified_at": schemaMap{"type": "string", "format": "date-time"}}, "device_uid", "display_variant", "verified_at")
	values["FirmwareUploadInput"] = schemaObject(schemaMap{"file": schemaMap{"type": "string", "format": "binary", "description": "Complete firmware ZIP, at most 2 MiB, with the report, model image, browser manifest and five matching gzip assets."}}, "file")
	candidate := schemaMap{
		"hub_ota_protocol": schemaMap{"type": "integer", "enum": []int{0, 1}, "description": "CC3200 recovery protocol for Pico S, Pro and C."},
		"ota_protocol":     schemaMap{"type": "string", "enum": []string{firmware.ESP32FactoryProtocol, firmware.FermABProtocol}, "description": "ESP application transfer protocol. Present on wireless-capable packages."},
		"model":            schemaMap{"type": "string", "enum": []string{"pico-s", "pico-pro", "pico-c", "z-series", "picostill", "picoferm"}},
		"model_family":     schemaMap{"type": "string", "enum": []string{"pico-s-pro", "pico-c", "z-series", "picostill", "picoferm"}},
		"display_variant":  displayVariant,
		"firmware_version": stringSchema(),
		"binary_sha256":    hash,
		"binary_bytes":     schemaMap{"type": "integer", "minimum": 1, "maximum": firmware.MaxReleaseBytes, "description": "Bytes in the cataloged application image. The installer also enforces the target partition limit. PicoFerm A/B releases identify bank zero here and record both bank images in the package."},
	}
	required := []string{"model", "model_family", "firmware_version", "binary_sha256", "binary_bytes"}
	values["FirmwareCandidate"] = schemaObject(candidate, required...)
	values["FirmwareProvenance"] = schemaObject(schemaMap{
		"source":     schemaMap{"type": "string", "enum": []string{"upload", "github", "library"}},
		"repository": stringSchema(),
		"commit":     commit,
	}, "source")
	properties := schemaMap{}
	for key, value := range candidate {
		properties[key] = value
	}
	properties["id"] = hash
	properties["package_bytes"] = schemaMap{"type": "integer", "minimum": 1, "maximum": firmware.MaxReleaseBytes}
	properties["created_at"] = schemaMap{"type": "string", "format": "date-time"}
	properties["validation"] = schemaMap{"type": "string", "enum": []string{"passed"}, "description": "Package structure, sizes, hashes and image bounds passed. This is not a publisher signature, physical hardware identification, or installation result."}
	properties["provenance"] = schemaRef("FirmwareProvenance")
	values["FirmwarePackage"] = schemaObject(properties, append(required, "id", "package_bytes", "created_at", "validation", "provenance")...)
	releaseProps := schemaMap{}
	for k, v := range candidate {
		releaseProps[k] = v
	}
	releaseProps["model"] = stringSchema()
	releaseProps["model_family"] = stringSchema()
	releaseProps["kind"] = schemaMap{"type": "string", "enum": []string{"standalone", "reference", "picoferm-ab", "historical"}}
	releaseProps["source_commit"] = commit
	releaseProps["archive_sha256"] = hash
	values["FirmwareRelease"] = schemaObject(releaseProps, append(append([]string{}, required...), "kind", "source_commit", "archive_sha256")...)
	values["FirmwareUpdates"] = schemaObject(schemaMap{"checked": schemaMap{"type": "string", "format": "date-time"}, "releases": schemaArray(schemaRef("FirmwareRelease")), "notices": schemaArray(schemaObject(schemaMap{"name": stringSchema(), "version": stringSchema(), "device_id": stringSchema(), "revised": boolSchema()}, "name", "version", "device_id"))}, "releases", "notices")
	values["FirmwareCatalog"] = schemaObject(schemaMap{"repository": stringSchema(), "ref": stringSchema(), "commit": commit, "packages": schemaArray(schemaRef("FirmwareCandidate"))}, "repository", "ref", "commit", "packages")
	values["MachineFirmware"] = schemaObject(schemaMap{
		"device_id":                      stringSchema(),
		"model":                          stringSchema(),
		"current_version":                stringSchema(),
		"display_variant":                displayVariant,
		"verified_display_variant":       displayVariant,
		"device_uid":                     stringSchema(),
		"hardware_verification_required": boolSchema(),
		"stock_restore_guide":            boolSchema(),
		"can_confirm_stock_startup":      boolSchema(), "stock_restore_wireless": boolSchema(), "stock_restore_upgrade": boolSchema(), "last_stock_restore": schemaRef("FirmwareStockReceipt"),
		"install_supported":  boolSchema(),
		"retry_package_id":   hash,
		"revised_package_id": hash,
		"last_install":       schemaRef("FirmwareInstallReceipt"),
		"reason":             stringSchema(),
		"packages":           schemaArray(schemaRef("FirmwarePackage")),
	}, "device_id", "model", "current_version", "install_supported", "reason", "packages")
}

var pathParameters = regexp.MustCompile(`\{([^}]+)\}`)

func operationParameters(e endpoint) []any {
	params := []any{}
	for _, match := range pathParameters.FindAllStringSubmatch(e.path, -1) {
		params = append(params, schemaMap{"name": match[1], "in": "path", "required": true, "schema": stringSchema()})
	}
	if strings.HasSuffix(e.path, "/commands") {
		params = append(params, schemaMap{"name": "Idempotency-Key", "in": "header", "required": true, "description": "A unique 8–128 character key for one intended command. Reusing it returns its receipt without resending; a different command with the same key conflicts.", "schema": schemaMap{"type": "string", "minLength": 8, "maxLength": 128}})
	}
	query := func(name, description string, schema schemaMap) {
		params = append(params, schemaMap{"name": name, "in": "query", "description": description, "required": false, "schema": schema})
	}
	switch e.path {
	case "/brew-groups/{group}":
		if e.method == "DELETE" {
			query("revision", "Current group revision.", schemaMap{"type": "integer", "minimum": 1})
			params[len(params)-1].(schemaMap)["required"] = true
		}
	case "/telemetry/receiver":
		query("challenge", "A random 16-byte challenge encoded as 32 hexadecimal characters.", schemaMap{"type": "string", "pattern": "^[0-9a-fA-F]{32}$"})
		params[len(params)-1].(schemaMap)["required"] = true
	case "/catalog":
		for _, name := range []string{"q", "source", "model", "style"} {
			query(name, "Catalog filter.", stringSchema())
		}
		query("kind", "Select a catalog collection. The reviewed collection includes author records with unresolved instructions.", schemaMap{"type": "string", "default": "all-brewers", "enum": []string{"all-brewers", "archive", "reviewed", "brew-ready", "brewing", "recipe", "native-program", "maintenance"}})
		query("offset", "Zero-based result offset.", schemaMap{"type": "integer", "minimum": 0, "default": 0})
		query("limit", "Results per page.", schemaMap{"type": "integer", "minimum": 1, "maximum": 100, "default": 24})
	case "/catalog/references":
		query("model", "Filter by machine model.", stringSchema())
	case "/catalog/{id}/source":
		query("record", "Original record ID within this grouped catalog entry.", stringSchema())
	case "/catalog/assets/{asset_path}":
		params[0].(schemaMap)["description"] = "Full manifest asset path, including its source prefix and nested directories."
	case "/recipes", "/recipes/{id}":
		if e.method == "POST" || e.method == "PUT" {
			query("validate_for_model", "Require the recipe to compile for this brewer before saving its revision.", stringSchema())
		}
	case "/recipes/{id}/export":
		query("format", "JSON preserves every library field; native-json exports a compatible Pico program, z-picopak retains original kit settings for the Z adapter, and z-program exports original Z Program Maker JSON. BeerXML requires a complete recipe.", schemaMap{"type": "string", "enum": []string{"json", "beerxml", "native-json", "z-program", "z-picopak"}, "default": "json"})
	case "/brews/{id}/export":
		query("format", "Full recorded samples without graph downsampling.", schemaMap{"type": "string", "enum": []string{"json", "csv"}, "default": "json"})
	case "/shopping-list":
		query("recipes", "Comma-separated canonical recipe IDs, for example id1,id2. Empty yields an empty list.", stringSchema())
	case "/ai/advice":
		query("device_id", "Limit advice to one registered machine.", stringSchema())
		query("brew_id", "Limit advice to this exact saved brew. All supplied filters are applied together.", stringSchema())
		query("mode", "Limit advice to one stage of the brewing workflow.", schemaMap{"type": "string", "enum": []string{"recipe", "live", "analysis"}})
	case "/ai/models":
		if e.method == "GET" {
			query("provider", "Provider to query. Defaults to the selected provider.", schemaMap{"type": "string", "enum": []string{"openai", "anthropic", "gemini"}})
			query("auth_mode", "Choose subscription or API-key access. An omitted mode uses saved settings only when provider is also omitted; otherwise API-key mode applies.", schemaMap{"type": "string", "enum": []string{"api_key", "subscription"}})
		}
	case "/ai/subscription":
		query("provider", "Subscription provider; disconnect is supported for OpenAI. Claude is managed by its native CLI.", schemaMap{"type": "string", "enum": []string{"openai", "anthropic"}})
		params[len(params)-1].(schemaMap)["required"] = true
	}
	return params
}

func requestSchema(e endpoint) (schemaMap, bool) {
	name := ""
	switch e.path {
	case "/brew-groups", "/brew-groups/{group}":
		if e.method == "POST" || e.method == "PUT" {
			return schemaRef("BrewGroupInput"), true
		}
	case "/devices/{id}/brew-invite/{invite}/respond":
		return schemaObject(schemaMap{"accept": boolSchema()}, "accept"), true
	case "/devices/{id}/picostill":
		if e.method == "PATCH" {
			return schemaObject(schemaMap{"session_logging": boolSchema()}, "session_logging"), true
		}
		if e.method == "PUT" {
			standalone := schemaObject(schemaMap{"picostill_id": stringSchema()}, "picostill_id")
			stock := schemaObject(schemaMap{"stock": schemaObject(schemaMap{"host": schemaMap{"type": "string", "format": "ipv4"}, "uid": schemaMap{"type": "string", "pattern": "^[a-fA-F0-9]{12}$"}}, "host", "uid")}, "stock")
			return schemaMap{"oneOf": []any{standalone, stock}}, true
		}
	case "/catalog/{id}/copy":
		name = "CatalogCopyInput"
	case "/auth/setup":
		name = "Setup"
	case "/auth/login":
		name = "Login"
	case "/devices", "/devices/{id}":
		name = "DeviceInput"
	case "/devices/{id}/commands":
		name = "CommandInput"
	case "/devices/{id}/picoferms":
		name = "brewerPicoFermInput"
	case "/devices/{id}/brewer":
		name = "PicoFermBrewer"
	case "/devices/{id}/ai-monitor":
		name = "AIMonitorInput"
	case "/devices/{id}/settings":
		name = "SettingsInput"
	case "/devices/{id}/telemetry":
		name = "TelemetryInput"
	case "/devices/{id}/deploy":
		name = "DeployInput"
	case "/devices/{id}/deployments":
		name = "DeploymentInput"
	case "/devices/{id}/recipes/{recipe_id}":
		if e.method == "DELETE" {
			return schemaObject(schemaMap{"expected": schemaRef("MachineRecipe")}, "expected"), false
		}
		name = "MachineRecipeUpdate"
	case "/profiles/{id}":
		name = "ProfileInput"
	case "/recipes", "/recipes/{id}":
		name = "RecipeInput"
	case "/recipes/{id}/adapt":
		name = "AdaptInput"
	case "/recipes/import":
		name = "ImportInput"
	case "/brews/{id}/conditioning":
		name = "ConditioningInput"
	case "/brews/{id}":
		name = "BrewNotes"
	case "/telemetry":
		name = "ForwardingEnvelope"
	case "/ai/subscription/login":
		name = "AISubscriptionLoginInput"
	case "/ai/subscription/callback":
		name = "AISubscriptionCallbackInput"
	case "/ai/settings":
		name = "AISettingsInput"
	case "/ai/models":
		name = "AIModelsInput"
	case "/ai/assist":
		name = "Request"
	case "/usb/jobs":
		name = "USBInput"
	case "/usb/jobs/{id}/confirm", "/usb/jobs/{id}/cancel":
		name = "USBAnswer"
	case "/firmware/settings":
		name = "FirmwareSettingsInput"
	case "/firmware/github/import":
		name = "FirmwareImportInput"
	case "/devices/{id}/firmware/stock-started":
		name = "FirmwareStockObservation"
	case "/devices/{id}/firmware/restore-stock":
		name = "FirmwareStockInput"
	case "/devices/{id}/firmware/install":
		name = "FirmwareInstallInput"
	case "/devices/{id}/firmware/hardware":
		name = "FirmwareHardwareInput"
	}
	if name != "" {
		return schemaRef(name), true
	}
	return schemaMap{"type": "object", "description": "No JSON fields are required by this action."}, false
}

func successResponses(e endpoint) schemaMap {
	schema := schemaMap{"type": "object"}
	code := "200"
	description := "Successful response"
	switch e.path {
	case "/brew-groups":
		if e.method == "GET" {
			schema = schemaObject(schemaMap{"groups": schemaArray(schemaRef("brewGroup")), "stock_candidates": schemaArray(schemaRef("groupCandidate"))}, "groups", "stock_candidates")
		} else {
			schema = schemaRef("brewGroup")
		}
	case "/brew-groups/{group}":
		if e.method == "DELETE" {
			schema = schemaRef("Deleted")
		} else {
			schema = schemaRef("brewGroup")
		}
	case "/brew-invites":
		schema = schemaObject(schemaMap{"invites": schemaArray(schemaRef("brewInvite"))}, "invites")
	case "/devices/{id}/brew-invite":
		schema = schemaObject(schemaMap{"invite": schemaMap{"type": "object", "nullable": true, "allOf": []any{schemaRef("brewInvite")}}}, "invite")
	case "/devices/{id}/brew-invite/{invite}/respond":
		schema = schemaRef("brewInvite")
	case "/devices/{id}/picostill":
		schema = schemaRef("PicoStillLink")
	case "/usb/devices":
		schema = schemaRef("USBDevices")
	case "/usb/jobs":
		code = "202"
		schema = schemaRef("USBJob")
	case "/usb/jobs/{id}", "/usb/jobs/{id}/confirm", "/usb/jobs/{id}/cancel":
		schema = schemaRef("USBJob")
	case "/auth/session":
		schema = schemaRef("AuthSession")
	case "/auth/setup", "/auth/login":
		schema = schemaRef("Authenticated")
	case "/auth/logout":
		code = "204"
	case "/devices/discover":
		schema = schemaRef("LANDiscovery")
	case "/devices":
		if e.method == "GET" {
			schema = schemaArray(schemaRef("DeviceView"))
		} else {
			code = "201"
			schema = schemaRef("DeviceView")
		}
	case "/devices/{id}":
		if e.method == "DELETE" {
			schema = schemaRef("Deleted")
		} else if e.method == "PUT" {
			schema = schemaRef("DeviceView")
		} else {
			schema = schemaRef("DeviceView")
		}
	case "/devices/{id}/test":
		schema = schemaRef("DeviceView")
	case "/devices/{id}/events":
		schema = schemaRef("DeviceEvents")
	case "/devices/{id}/picoferms":
		if e.method == "GET" {
			schema = schemaRef("brewerFermentationOptions")
		} else {
			schema = schemaRef("PicoFermBrewer")
		}
	case "/devices/{id}/brewer":
		schema = schemaRef("PicoFermBrewer")
	case "/devices/{id}/display":
		schema = schemaRef("Display")
	case "/devices/{id}/commands":
		code = "202"
		schema = schemaRef("commandReceipt")
		description = "Accepted into the device queue, not proof of physical execution. Observe the machine/front panel."
	case "/devices/{id}/recipes":
		schema = schemaRef("DeviceRecipes")
	case "/devices/{id}/recipes/{recipe_id}":
		if e.method == "DELETE" {
			code = "204"
		} else {
			schema = schemaRef("MachineRecipeUpdated")
		}
	case "/devices/{id}/settings":
		schema = schemaRef("Settings")
	case "/devices/{id}/telemetry":
		schema = schemaRef("Telemetry")
		if e.method == "PUT" {
			schema = schemaRef("TelemetryStatus")
		}
	case "/devices/{id}/ai-monitor":
		schema = schemaRef("aiMonitorStatus")
	case "/devices/{id}/sync-history":
		schema = schemaRef("Synced")
	case "/devices/{id}/deploy":
		schema = schemaRef("Deployment")
	case "/devices/{id}/deployments":
		code = "201"
		schema = schemaRef("BulkDeployment")
	case "/devices/{id}/deployments/{deployment_id}", "/devices/{id}/deployments/{deployment_id}/execute":
		schema = schemaRef("BulkDeployment")
	case "/catalog":
		schema = schemaRef("Page")
	case "/catalog/{id}":
		schema = schemaRef("Detail")
	case "/catalog/references":
		schema = schemaRef("References")
	case "/catalog/{id}/copy":
		code = "201"
		schema = schemaRef("Recipe")
	case "/profiles":
		schema = schemaArray(schemaRef("Profile"))
	case "/profiles/{id}":
		schema = schemaRef("Profile")
	case "/recipes":
		if e.method == "GET" {
			schema = schemaArray(schemaRef("Recipe"))
		} else {
			code = "201"
			schema = schemaRef("Recipe")
		}
	case "/recipes/{id}":
		schema = schemaRef("Recipe")
		if e.method == "DELETE" {
			code = "204"
		}
	case "/recipes/import":
		code = "201"
		schema = schemaArray(schemaRef("Recipe"))
	case "/recipes/{id}/adapt":
		schema = schemaRef("Adapted")
	case "/recipes/{id}/revisions":
		schema = schemaArray(schemaRef("Recipe"))
	case "/brews":
		schema = schemaArray(schemaRef("Brew"))
	case "/brews/{id}/conditioning":
		schema = schemaRef("Conditioning")
	case "/brews/{id}":
		if e.method == "PATCH" {
			schema = schemaRef("Brew")
		} else {
			schema = schemaRef("BrewDetail")
		}
	case "/telemetry/receiver":
		schema = schemaObject(schemaMap{"proof": stringSchema()}, "proof")
	case "/telemetry":
		code = "202"
		schema = schemaRef("Accepted")
		description = "Unsigned observation stored; it cannot command a device or override authoritative live state."
	case "/ai/subscription", "/ai/subscription/login", "/ai/subscription/callback":
		schema = schemaRef("SubscriptionStatus")
	case "/ai/settings":
		schema = schemaRef("Config")
	case "/ai/models":
		schema = schemaRef("aiModelList")
	case "/ai/assist":
		schema = schemaRef("Result")
	case "/ai/advice":
		schema = schemaArray(schemaRef("AIAdvice"))
	case "/shopping-list":
		schema = schemaArray(schemaRef("Ingredient"))
	case "/firmware/settings":
		schema = schemaRef("FirmwareSettings")
	case "/firmware/updates":
		schema = schemaRef("FirmwareUpdates")
	case "/firmware/releases/{packageID}/stage":
		schema = schemaRef("FirmwarePackage")
	case "/firmware/github":
		schema = schemaRef("FirmwareCatalog")
	case "/firmware/packages":
		if e.method == "GET" {
			schema = schemaArray(schemaRef("FirmwarePackage"))
		} else {
			code, schema = "201", schemaRef("FirmwarePackage")
		}
	case "/firmware/github/import":
		code, schema = "201", schemaRef("FirmwarePackage")
	case "/firmware/packages/{packageID}":
		if e.method == "DELETE" {
			schema = schemaRef("Deleted")
		} else {
			schema = schemaRef("FirmwarePackage")
		}
	case "/devices/{id}/firmware/stock-started":
		schema = schemaRef("FirmwareStockReceipt")
	case "/devices/{id}/firmware/restore-stock":
		code, schema = "202", schemaRef("FirmwareStockReceipt")
	case "/devices/{id}/firmware/install":
		code, schema = "202", schemaRef("FirmwareInstallReceipt")
	case "/devices/{id}/firmware":
		schema = schemaRef("MachineFirmware")
	case "/devices/{id}/firmware/hardware":
		schema = schemaRef("FirmwareHardware")
	}
	content := schemaMap{"application/json": schemaMap{"schema": schema}}
	switch e.path {
	case "/catalog/assets/{asset_path}":
		content = schemaMap{"image/png": schemaMap{"schema": schemaMap{"type": "string", "format": "binary"}}, "image/jpeg": schemaMap{"schema": schemaMap{"type": "string", "format": "binary"}}, "application/pdf": schemaMap{"schema": schemaMap{"type": "string", "format": "binary"}}, "image/svg+xml": schemaMap{"schema": stringSchema()}, "text/plain": schemaMap{"schema": stringSchema()}}
	case "/catalog/{id}/source":
		content = schemaMap{"application/json": schemaMap{"schema": schemaMap{}}}
	case "/events":
		content = schemaMap{"text/event-stream": schemaMap{"schema": stringSchema(), "example": "event: fleet\ndata: []\n\n"}}
		description = "SSE fleet events; each data field is a JSON DeviceView array."
	case "/backup":
		content = schemaMap{"application/zip": schemaMap{"schema": schemaMap{"type": "string", "format": "binary"}}}
		description = "Data export ZIP. Credentials and master.key are excluded; this is not a full disaster-recovery backup."
	case "/firmware/packages/{packageID}/download":
		content = schemaMap{"application/zip": schemaMap{"schema": schemaMap{"type": "string", "format": "binary"}}}
		description = "Complete validated package ZIP. Cached bytes are checked against the saved archive hash before being served."
	case "/recipes/{id}/export":
		content = schemaMap{"application/json": schemaMap{"schema": schemaRef("Recipe")}, "application/xml": schemaMap{"schema": stringSchema()}}
	case "/brews/{id}/export":
		content = schemaMap{"application/json": schemaMap{"schema": schemaRef("BrewDetail")}, "text/csv": schemaMap{"schema": stringSchema()}}
	}
	response := schemaMap{"description": description}
	if code != "204" {
		response["content"] = content
	}
	result := schemaMap{code: response}
	if strings.HasSuffix(e.path, "/commands") {
		result["200"] = schemaMap{"description": "Existing receipt for this Idempotency-Key; the command was not resent.", "content": content}
	}
	if e.method == "POST" && (e.path == "/firmware/packages" || e.path == "/firmware/github/import") {
		result["200"] = schemaMap{"description": "This exact archive is already saved; its original acquisition provenance is retained.", "content": content}
	}
	return result
}

func operationResponses(e endpoint, public, requiresJSON bool) schemaMap {
	responses := successResponses(e)
	statuses := map[int]string{500: "Storage or internal operation failed."}
	if !public {
		statuses[401] = "Owner session required."
		statuses[403] = "Request origin/CSRF rejected, or demo mode does not permit this host/action."
	}
	if requiresJSON {
		statuses[400] = "Malformed JSON, unknown fields or invalid values."
		statuses[413] = "JSON body exceeds the 2 MiB request limit."
		statuses[415] = "Use application/json."
	}
	if strings.Contains(e.path, "{id}") || e.path == "/shopping-list" || e.path == "/ai/assist" {
		statuses[404] = "Requested record or machine does not exist."
	}
	if strings.HasPrefix(e.path, "/devices/{id}") {
		for code, message := range map[int]string{400: "Invalid device command or configuration.", 409: "Machine state, device file or command key conflict.", 413: "Device rejected an oversized payload.", 422: "Device/target profile rejected this program.", 499: "The device request was canceled.", 502: "Device unavailable, rejected transport response or mutation outcome unknown; writes are not automatically retried.", 503: "The machine connection/adapter is not configured."} {
			statuses[code] = message
		}
	}
	if e.path == "/auth/login" || e.path == "/auth/setup" {
		statuses[401] = "Incorrect password or setup token."
		statuses[403] = "Invalid request origin."
		statuses[409] = "Owner already configured or action unavailable in demo mode."
		statuses[429] = "Too many sign-in attempts; try again in a minute."
	}
	if e.method == "PUT" || e.method == "PATCH" || e.path == "/devices" && e.method == "POST" {
		statuses[409] = "Record revision or current state conflicts with this request."
	}
	if e.path == "/recipes/{id}/adapt" {
		statuses[422] = "Recipe, volume or configured capacity is invalid. Valid planning recipes with unsupported native conversion return 200 with deployment_blockers."
	}
	if e.path == "/profiles/{id}" && e.method == "PUT" {
		statuses[422] = "Batch volume, efficiency, grain or hop capacity is outside the calibration bounds."
	}
	if e.path == "/recipes/{id}/export" {
		statuses[400] = "Format unsupported, or required BeerXML metadata is missing."
	}
	if strings.HasPrefix(e.path, "/catalog") {
		statuses[400] = "Invalid catalog filters, pagination or request body."
		statuses[404] = "Entry, original record or local asset is unavailable."
		if e.path == "/catalog/{id}/copy" {
			statuses[422] = "This entry has no verified conversion for the requested copy type."
		}
	}
	if e.path == "/ai/assist" {
		statuses[409] = "AI provider/key/model is not enabled, or the requested brew/device session changed."
		statuses[429] = "Another AI request is in progress."
		statuses[502] = "Provider failed or returned an invalid recipe."
	}
	if e.path == "/ai/models" {
		statuses[400] = "Unsupported provider, authentication mode or invalid provider API key."
		statuses[409] = "No API key is saved or supplied for this provider."
		statuses[502] = "Provider model discovery failed; saved credentials and model selection remain unchanged."
	}
	if strings.HasPrefix(e.path, "/ai/subscription") {
		statuses[400] = "Invalid provider, callback or expired authorization."
		statuses[409] = "Start a new browser sign-in before submitting its callback."
		statuses[502] = "Subscription provider failed; API-key billing is never selected automatically."
	}
	if e.path == "/telemetry" {
		statuses[403] = "Device UID has not been registered and discovered."
	}
	if strings.HasPrefix(e.path, "/usb/") {
		statuses[400] = "Unknown or unsupported USB option."
		statuses[404] = "USB job or registered machine does not exist."
		statuses[409] = "Another job is active, the machine identity could not be verified or the current confirmation changed."
		statuses[503] = "The Hub has no USB worker for this platform."
	}
	if strings.HasPrefix(e.path, "/firmware/") {
		statuses[400] = "Invalid source, model, commit, archive, or package metadata."
		statuses[409] = "The saved source changed, the cache is full, or saved package integrity failed."
		statuses[422] = "Package structure, metadata, sizes, hashes or image bounds failed validation."
		statuses[429] = "Another firmware acquisition is in progress; retry shortly."
		statuses[499] = "The firmware request was canceled."
		statuses[504] = "The firmware request exceeded its time limit."
		if strings.Contains(e.path, "{packageID}") {
			statuses[404] = "The saved firmware package does not exist."
		}
		if strings.HasPrefix(e.path, "/firmware/github") {
			statuses[404] = "The public repository, revision or firmware files were not found."
			statuses[429] = "GitHub refused the public request, its rate limit was reached, or another firmware acquisition is in progress."
			statuses[502] = "GitHub source lookup or download failed; saved packages remain unchanged."
		}
		if e.path == "/firmware/packages" && e.method == "POST" {
			statuses[413] = "The archive or multipart body exceeds the bounded upload limit."
			statuses[415] = "Use multipart/form-data with one file field containing the firmware ZIP."
		}
	}
	for code, description := range statuses {
		responses[strconv.Itoa(code)] = schemaMap{"description": description, "content": schemaMap{"application/json": schemaMap{"schema": schemaRef("Error")}}}
	}
	return responses
}

func openAPIDocument() schemaMap {
	paths := map[string]any{}
	for _, e := range endpoints {
		path, ok := paths[e.path].(map[string]any)
		if !ok {
			path = map[string]any{}
			paths[e.path] = path
		}
		params := operationParameters(e)
		security := []any{map[string]any{"session": []string{}}}
		if e.method != "GET" {
			security = []any{map[string]any{"session": []string{}, "csrf": []string{}}}
		}
		public := strings.HasPrefix(e.path, "/auth/") && e.path != "/auth/logout" || e.path == "/telemetry" || e.path == "/telemetry/receiver"
		if public {
			security = []any{}
		}
		request, requiresJSON := requestSchema(e)
		requiresJSON = requiresJSON && e.body != nil
		op := map[string]any{"operationId": strings.ToLower(e.method) + "_" + strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(strings.TrimPrefix(e.path, "/")), "summary": e.summary, "security": security, "parameters": params, "responses": operationResponses(e, public, requiresJSON)}
		if e.body != nil {
			example := e.body
			if e.path == "/recipes/{id}" && e.method == "PUT" {
				example = recipeAPIExample(1)
			}
			if e.path == "/recipes/import" {
				data, _ := json.Marshal(recipeAPIExample(0))
				example = schemaMap{"format": "json", "data": string(data)}
			}
			op["requestBody"] = map[string]any{"required": requiresJSON, "content": map[string]any{"application/json": map[string]any{"schema": request, "example": example}}}
		}
		if e.path == "/firmware/packages" && e.method == "POST" {
			op["requestBody"] = schemaMap{"required": true, "content": schemaMap{"multipart/form-data": schemaMap{"schema": schemaRef("FirmwareUploadInput")}}}
		}
		path[strings.ToLower(e.method)] = op
	}
	return schemaMap{"openapi": "3.0.3", "info": map[string]any{"title": "PicoBrew Hub API", "version": Version, "description": "Same-origin owner API. Obtain the session cookie at login, then read /auth/session for X-CSRF-Token on protected writes. Status codes describe HTTP acceptance, not proof of physical execution. The Hub offers pause and resume when the brewer advertises them. Installed recipes can be edited while the brewer is idle. AI is advisory and cannot issue commands. JSON decoding accepts one value, rejects unknown fields and limits bodies to 2 MiB. Firmware ZIP uploads use bounded multipart requests and do not install firmware. Recipe files additionally have a 1 MiB import limit. Optional device forwarding is unsigned observation data, not control or authoritative status."}, "servers": []any{map[string]string{"url": "/api/v1"}}, "paths": paths, "components": map[string]any{"schemas": buildSchemas(), "securitySchemes": map[string]any{"session": map[string]string{"type": "apiKey", "in": "cookie", "name": "pico_hub_session"}, "csrf": map[string]string{"type": "apiKey", "in": "header", "name": "X-CSRF-Token"}}}}
}

func recipeAPIExample(revision int64) schemaMap {
	return schemaMap{"name": "New recipe", "batch_liters": 5, "efficiency": 55, "ingredients": []any{}, "steps": []any{schemaMap{"name": "Mash", "kind": "mash", "target_c": 67, "minutes": 60, "location": 0, "drain_minutes": 0}}, "revision": revision}
}
