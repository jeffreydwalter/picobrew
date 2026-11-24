#include "engine_internal.hpp"

namespace picobrew { namespace standalone {
using namespace detail;
namespace {
unsigned method_error(Writer& writer) { return error(writer, Result(405, "method_not_allowed", "Unsupported method for this resource")); }
void identity_json(Writer& writer, const Identity& identity) {
    writer.text("\"model\":"); writer.quote(identity.model); writer.text(",\"model_name\":"); writer.quote(identity.model_name);
    writer.text(",\"model_family\":"); writer.quote(identity.model_family);
}
void fields_json(Writer& writer, std::uint32_t fields) {
    const char* names[] = {"recipe_id", "delta", "click", "channel", "level", "enabled", "value", "duration_seconds", "confirm"};
    writer.text("["); bool comma = false;
    for (unsigned i = 0; i < 9; ++i) if (fields & (1u << i)) {
        if (comma) writer.text(",");
        writer.quote(names[i]); comma = true;
    }
    writer.text("]");
}
void metadata_json(Writer& writer, const Session& session, bool storage_error, std::uint32_t duration) {
    static const char* const states[] = {"empty", "recording", "completed", "interrupted"};
    writer.format("\"available\":%s,\"id\":%lu,\"command\":", session.id ? "true" : "false", static_cast<unsigned long>(session.id));
    writer.quote(session.command); writer.text(",\"recipe_name\":"); writer.quote(session.recipe);
    writer.text(",\"state\":"); writer.quote(states[static_cast<unsigned>(session.state) < 4 ? static_cast<unsigned>(session.state) : 0]);
    writer.format(",\"duration_ms\":%lu,\"record_count\":%u,\"persisted_record_count\":%u,\"first_elapsed_ms\":0,\"gap_count\":%lu,\"truncated\":%s,\"storage_error\":%s,\"recovered_after_restart\":%s",
        static_cast<unsigned long>(duration), session.count, session.count, static_cast<unsigned long>(session.gaps),
        session.truncated ? "true" : "false", storage_error || session.storage_error ? "true" : "false", session.recovered ? "true" : "false");
    writer.format(",\"sample_interval_ms\":%lu", static_cast<unsigned long>(session.interval_ms));
}
}
unsigned Engine::Impl::discovery_json(Writer& writer) {
    writer.text("{"); identity_json(writer, identity);
    if (pico_http_pairing_available(false)) writer.text(",\"pairing_v1\":true");
    if (capabilities.ota_protocol) { writer.text(",\"ota_protocol\":"); writer.quote(capabilities.ota_protocol); }
    if (capabilities.ferment_menu_v1) writer.text(",\"ferment_menu_v1\":true");
    if (capabilities.z_program_maker) writer.text(",\"z_program_maker\":true");
    writer.text(",\"api_version\":1,\"release_channel\":\"stable\",\"firmware_version\":"); writer.quote(identity.firmware_version);
    writer.format(",\"mode\":\"standalone\",\"recipe_capacity\":%u,\"step_capacity\":%u,\"recipe_create_only\":%s,\"zero_minute_steps\":%s,\"restore_default_recipes\":%s,\"starter_recipe_count\":%u,\"recipe_labels\":\"printable ASCII\",\"commands\":[",
        capabilities.flags & Recipes ? unsigned(RecipeCapacity) : 0u, capabilities.flags & Recipes ? unsigned(RecipeStepCapacity) : 0u,
        capabilities.flags & Recipes ? "true" : "false", capabilities.zero_minute_steps ? "true" : "false", capabilities.starter_recipe_count ? "true" : "false", unsigned(capabilities.starter_recipe_count));
    for (std::size_t i = 0; i < capabilities.command_count; ++i) { if (i) writer.text(","); writer.quote(capabilities.commands[i].name); }
    writer.format("],\"dedicated_pause_resume\":%s,\"front_panel\":%s,\"events_persistent\":false,\"history_persistent\":true,\"history_capacity_records\":%u,\"history_capacity_sessions\":8,\"telemetry_forwarding\":true,\"capabilities\":{",
        capabilities.dedicated_pause_resume ? "true" : "false", capabilities.flags & FrontPanel ? "true" : "false", PointCapacity);
    const char* names[] = {"recipes", "brewing", "heating", "pump", "fans", "temperature", "pressure", "battery", "calibration", "fermentation", "front_panel"};
    for (unsigned i = 0; i < 11; ++i) {
        if (i) writer.text(",");
        writer.quote(names[i]); writer.text(":"); writer.boolean((capabilities.flags & (1u << i)) != 0);
    }
    writer.format("},\"history_sample_interval_ms\":%lu,\"settings_fields\":[", static_cast<unsigned long>(history_interval())); bool comma = false;
    const char* settings_names[] = {"temperature_unit", "language", "detailed_status"};
    for (unsigned i = 0; i < 3; ++i) if (capabilities.settings & (1u << i)) {
        if (comma) writer.text(",");
        writer.quote(settings_names[i]); comma = true;
    }
    writer.text("],\"command_fields\":{");
    for (std::size_t i = 0; i < capabilities.command_count; ++i) {
        const CommandDescriptor& command = capabilities.commands[i]; if (i) writer.text(",");
        writer.quote(command.name); writer.text(":{\"allowed\":"); fields_json(writer, command.allowed_fields);
        writer.text(",\"required\":"); fields_json(writer, command.required_fields); writer.text("}");
    }
    writer.text("}}"); return 200;
}
unsigned Engine::Impl::status_json(Writer& writer, bool update) {
    if (update) { const Result result = refresh(); if (!result.ok()) return error(writer, result); }
    if (!snapshot_valid) return error(writer, Result(503, "not_ready", "The device snapshot is unavailable"));
    const Snapshot& s = snapshot;
    writer.text("{"); identity_json(writer, identity); writer.text(",\"device_uid\":"); writer.quote(identity.device_uid);
    writer.format(",\"uptime_ms\":%lu,\"ready\":%s,\"busy\":%s,\"stopped\":%s,\"stop_reason\":",
        static_cast<unsigned long>(s.uptime_ms), s.ready ? "true" : "false", s.busy ? "true" : "false", s.stopped ? "true" : "false");
    writer.quote(s.stop_reason); writer.text(",\"command\":"); writer.quote(s.command); writer.text(",\"state\":"); writer.quote(s.state);
    writer.format(",\"device_state\":%lu,\"phase\":%lu,\"controller_status\":%lu,\"compartment\":%lu,\"wort_f\":",
        static_cast<unsigned long>(s.device_state), static_cast<unsigned long>(s.phase), static_cast<unsigned long>(s.controller_status), static_cast<unsigned long>(s.compartment));
    reading(writer, s.wort_f); writer.text(",\"thermoblock_f\":"); reading(writer, s.thermoblock_f);
    writer.text(",\"target_f\":"); reading(writer, s.target_f);
    writer.format(",\"step_seconds\":%ld,\"current_step\":%ld,\"step_count\":%lu,\"recipe_name\":",
        static_cast<long>(s.step_seconds), static_cast<long>(s.current_step), static_cast<unsigned long>(s.step_count));
    writer.quote(s.recipe_name); writer.text(",\"step_name\":"); writer.quote(s.step_name);
    writer.text(",\"session_id\":"); const int newest = newest_session();
    if (newest >= 0) writer.format("\"%lu\"", static_cast<unsigned long>(sessions[newest].id)); else writer.text("\"\"");
    writer.text(",\"filter_inserted\":"); if (s.filter_known) writer.boolean(s.filter_inserted); else writer.text("null");
    writer.format(",\"encoder\":{\"position\":%ld,\"min\":%ld,\"max\":%ld},\"temperature_f\":",
        static_cast<long>(s.encoder_position), static_cast<long>(s.encoder_min), static_cast<long>(s.encoder_max));
    reading(writer, s.temperature_f); writer.text(",\"pressure_kpa\":"); reading(writer, s.pressure_kpa);
    writer.text(",\"battery_v\":"); reading(writer, s.battery_v); writer.text(",\"sensors\":[");
    for (std::size_t i = 0; i < s.sensor_count; ++i) {
        const Sensor& sensor = s.sensors[i]; if (i) writer.text(",");
        writer.text("{\"id\":"); writer.quote(sensor.id); writer.text(",\"label\":"); writer.quote(sensor.label);
        writer.text(",\"kind\":"); writer.quote(kind_name(sensor.kind)); writer.text(",\"unit\":"); writer.quote(unit_name(sensor.unit));
        writer.text(",\"value\":"); reading(writer, sensor.reading); writer.text("}");
    }
    writer.text("],\"actuators\":[");
    for (std::size_t i = 0; i < s.actuator_count; ++i) {
        const Actuator& actuator = s.actuators[i]; if (i) writer.text(",");
        writer.text("{\"id\":"); writer.quote(actuator.id); writer.text(",\"label\":"); writer.quote(actuator.label);
        writer.text(",\"kind\":"); writer.quote(actuator_name(actuator.kind)); writer.text(",\"enabled\":");
        if (actuator.valid) writer.boolean(actuator.enabled); else writer.text("null");
        writer.text(",\"level\":"); if (actuator.supports_level) reading(writer, actuator.level); else writer.text("null");
        writer.text(",\"rpm\":"); if (actuator.supports_rpm) reading(writer, actuator.rpm); else writer.text("null");
        writer.text(",\"supports_level\":"); writer.boolean(actuator.supports_level);
        writer.text(",\"supports_rpm\":"); writer.boolean(actuator.supports_rpm); writer.text("}");
    }
    writer.text("],\"recovery\":");
    if (!s.recovery.supported) writer.text("null");
    else {
        writer.text("{\"available\":"); writer.boolean(s.recovery.available);
        writer.text(",\"valid\":"); writer.boolean(s.recovery.valid);
        writer.text(",\"recipe_name\":"); writer.quote(s.recovery.recipe_name);
        writer.text(",\"step_name\":"); writer.quote(s.recovery.step_name);
        writer.format(",\"current_step\":%ld,\"step_seconds\":%ld,\"elapsed_seconds\":%lu}",
            static_cast<long>(s.recovery.current_step), static_cast<long>(s.recovery.step_seconds),
            static_cast<unsigned long>(s.recovery.elapsed_seconds));
    }
    writer.text(",\"maintenance\":{\"available\":"); writer.boolean(care_available);
    writer.text(",\"due\":[");
    const char* const tasks[] = {"rinse", "clean", "clean_condenser", "sanitize_seal"};
    bool comma = false;
    for (unsigned i = 0; i < 4; ++i) if (care_available && !s.recording && (care_flags & (1u << i))) {
        if (comma) writer.text(",");
        writer.quote(tasks[i]); comma = true;
    }
    writer.text("]}");
    writer.text(",\"storage_ready\":"); writer.boolean(persistent); writer.text("}"); return 200;
}
unsigned Engine::Impl::settings_api(const PicoHttpRequest& request, Writer& writer) {
    if (!capabilities.settings) return error(writer, Result(404, "not_found", "No adjustable preferences are advertised"));
    if (request.method == PICO_HTTP_GET) { settings_json(writer, settings, capabilities.settings); return 200; }
    if (request.method != PICO_HTTP_PATCH) return method_error(writer);
    if (!idle()) return error(writer, Result(409, "busy", "Change settings only while the appliance is idle"));
    if (!persistent) return error(writer, Result(503, "storage_io", "Persistent state is unavailable"));
    Settings changed = settings; Json json(request.body, request.body_length); std::uint32_t seen = 0;
    bool valid = json.token('{') && !json.peek('}');
    while (valid) {
        char key[40], value[4]; std::uint32_t field = 0;
        if (!json.key(key)) { valid = false; break; }
        if (!std::strcmp(key, "temperature_unit")) {
            field = TemperatureUnit; valid = json.string(value, sizeof(value)) && std::strlen(value) == 1;
            if (valid) changed.temperature_unit = value[0];
        } else if (!std::strcmp(key, "language")) {
            field = Language; valid = json.string(changed.language, sizeof(changed.language));
        } else if (!std::strcmp(key, "detailed_status")) {
            field = DetailedStatus; valid = json.boolean(changed.detailed_status);
        } else valid = false;
        if ((seen & field) || !(capabilities.settings & field)) valid = false;
        seen |= field; if (!valid || !json.next('}')) break;
    }
    if (!valid || !seen || !json.token('}') || !json.done() || !settings_valid(changed))
        return error(writer, Result(400, "invalid_settings", "Invalid, duplicate or unsupported preference fields"));
    const Result saved = owner.preferences_store(changed, seen);
    if (!saved.ok()) return error(writer, saved);
    settings_json(writer, settings, capabilities.settings); return 200;
}
unsigned Engine::Impl::recipes_api(const PicoHttpRequest& request, Writer& writer) {
    if (!(capabilities.flags & Recipes)) return error(writer, Result(404, "not_found", "Recipes are unavailable on this device"));
    if (!persistent) return error(writer, Result(503, "storage_io", "The recipe catalogue is not verified"));
    const char* suffix = request.path + std::strlen("/api/v1/recipes");
    if (!*suffix) {
        if (request.method != PICO_HTTP_GET) return method_error(writer);
        writer.text("{\"recipes\":["); bool comma = false;
        for (unsigned i = 0; i < RecipeCapacity; ++i) {
            const RecipeEntry& entry = recipes[i]; if (!entry.id[0]) continue;
            if (comma) writer.text(",");
            writer.text("{\"id\":"); writer.quote(entry.id); writer.text(",\"name\":"); writer.quote(entry.name);
            writer.format(",\"step_count\":%u}", entry.steps); comma = true;
        }
        writer.format("],\"capacity\":%u,\"step_capacity\":%u}", unsigned(RecipeCapacity), unsigned(RecipeStepCapacity)); return 200;
    }
    if (*suffix++ != '/') return error(writer, Result(404, "not_found", "Unknown recipe resource"));
    if (!std::strcmp(suffix, "restore-defaults") && request.method == PICO_HTTP_POST) {
        if (request.body_length) return error(writer, Result(400, "invalid_body", "Restore defaults takes no request body"));
        unsigned added = 0, preserved = 0; const Result result = restore_defaults(added, preserved);
        if (!result.ok()) return error(writer, result);
        writer.format("{\"restored\":true,\"added\":%u,\"preserved\":%u}", added, preserved); return 200;
    }
    if (!valid_id(suffix)) return error(writer, Result(400, "invalid_recipe", "Invalid recipe ID"));
    if (request.method == PICO_HTTP_GET) {
        Recipe recipe; const Result result = owner.recipe_get(suffix, recipe); if (!result.ok()) return error(writer, result);
        recipe_json(writer, recipe); return 200;
    }
    if (request.method == PICO_HTTP_DELETE) {
        const Result result = recipe_delete(suffix); if (!result.ok()) return error(writer, result);
        writer.text("{\"deleted\":true}"); return 200;
    }
    if (request.method != PICO_HTTP_PUT) return method_error(writer);
    Recipe recipe; Json json(request.body, request.body_length);
    bool create_only = false;
    if (!parse_recipe(json, suffix, recipe, create_only)) return error(writer, Result(400, "invalid_recipe", "Invalid or duplicate recipe fields"));
    bool created = false; const Result result = recipe_write(recipe, &created, create_only); if (!result.ok()) return error(writer, result);
    recipe_json(writer, recipe); return created ? 201 : 200;
}
unsigned Engine::Impl::command_api(const PicoHttpRequest& request, Writer& writer) {
    if (request.method != PICO_HTTP_POST) return method_error(writer);
    Command command; Json json(request.body, request.body_length);
    if (!parse_command(json, command)) return error(writer, Result(400, "invalid_command", "Invalid or duplicate command fields"));
    const CommandDescriptor* descriptor = nullptr;
    for (std::size_t i = 0; i < capabilities.command_count; ++i)
        if (!std::strcmp(command.name, capabilities.commands[i].name)) descriptor = &capabilities.commands[i];
    if (!descriptor || (command.fields & ~descriptor->allowed_fields) ||
        (command.fields & descriptor->required_fields) != descriptor->required_fields ||
        ((descriptor->required_fields & Confirm) && !command.confirm))
        return error(writer, Result(400, "invalid_command", "Command arguments do not match advertised capabilities"));
    if (!persistent && std::strcmp(command.name, "stop")) return error(writer, Result(503, "storage_io", "Persistent state must be repaired before starting another operation"));
    Recipe recipe;
    if (command.fields & RecipeId) {
        const Result loaded = owner.recipe_get(command.recipe_id, recipe); if (!loaded.ok()) return error(writer, loaded);
        command.recipe = &recipe;
    }
    if (!owner.device_.execute) return error(writer, Result(503, "not_ready", "The device command port is unavailable"));
    const Result result = owner.device_.execute(owner.device_.context, command);
    if (!result.ok()) return error(writer, result);
    if (!std::strcmp(command.name, "stop")) owner.cancel_prepared_session();
    owner.event("command", command.name);
    if (refresh().ok()) history_poll();
    if (recording_blocked && std::strcmp(command.name, "stop")) return error(writer, Result(503, "history_storage", "Session start was stopped because its history identity could not be committed"));
    writer.text("{\"accepted\":true,\"command\":"); writer.quote(command.name); writer.text("}"); return result.status;
}
unsigned Engine::Impl::display_api(const PicoHttpRequest& request, Writer& writer) {
    if (!(capabilities.flags & FrontPanel) || !owner.device_.display || !owner.device_.read_display)
        return error(writer, Result(404, "not_found", "No physical front panel is available"));
    if (request.method != PICO_HTTP_GET) return method_error(writer);
    Display display; Result result = owner.device_.display(owner.device_.context, display);
    if (!result.ok()) return error(writer, result);
    std::uint32_t offset = 0, revision = display.revision; const char* path = request.path + std::strlen("/api/v1/display");
    if (*path) {
        if (*path++ != '/' || !decimal_path(path, offset) || std::strncmp(path, "?revision=", 10))
            return error(writer, Result(400, "invalid_display_page", "Use a page offset and revision"));
        path += 10;
        if (!decimal_path(path, revision) || *path || offset % DisplayPageBytes)
            return error(writer, Result(400, "invalid_display_page", "Invalid offset or revision"));
    }
    if (revision != display.revision) return error(writer, Result(409, "display_changed", "The framebuffer changed; restart the page read"));
    const std::size_t expected = display.format == DisplayFormat::PageLsb ?
        std::size_t(display.width) * ((display.height + 7) / 8) : ((display.width + 1) / 2) * std::size_t(display.height);
    if (!display.width || !display.height || !display.total_bytes || display.total_bytes != expected || display.total_bytes > 65536 ||
        (display.format != DisplayFormat::PageLsb && display.format != DisplayFormat::RowGray4Msb))
        return error(writer, Result(503, "invalid_display", "Invalid framebuffer metadata"));
    if (offset >= display.total_bytes) return error(writer, Result(400, "invalid_display_offset", "Offset exceeds framebuffer size"));
    const std::size_t count = display.total_bytes - offset > DisplayPageBytes ? DisplayPageBytes : display.total_bytes - offset;
    Bytes bytes(count); if (!bytes.data) return error(writer, Result(503, "capacity", "The display page buffer is unavailable"));
    result = owner.device_.read_display(owner.device_.context, revision, offset, bytes.data, count);
    if (!result.ok()) return error(writer, result);
    writer.format("{\"width\":%u,\"height\":%u,\"rotation\":%u,\"format\":", display.width, display.height, display.rotation);
    writer.quote(display.format == DisplayFormat::PageLsb ? "page-lsb-hex" : "row-gray4-msb-hex");
    writer.format(",\"offset\":%lu,\"total_bytes\":%lu,\"revision\":%lu,\"next_offset\":",
        static_cast<unsigned long>(offset), static_cast<unsigned long>(display.total_bytes), static_cast<unsigned long>(revision));
    if (offset + count < display.total_bytes) writer.format("%lu", static_cast<unsigned long>(offset + count)); else writer.text("null");
    writer.text(",\"data\":\""); const char hex[] = "0123456789abcdef";
    for (std::size_t i = 0; i < count; ++i) { const char pair[2] = {hex[bytes.data[i] >> 4], hex[bytes.data[i] & 15]}; writer.append(pair, 2); }
    writer.text("\"}"); return 200;
}
unsigned Engine::Impl::history_api(const PicoHttpRequest& request, Writer& writer) {
    if (request.method != PICO_HTTP_GET) return method_error(writer);
    if (!history_loaded) return error(writer, Result(503, "history_storage", "The retained history catalogue is not verified"));
    const char* path = request.path + std::strlen("/api/v1/history");
    Layout* layout = new (std::nothrow) Layout;
    if (!layout) return error(writer, Result(503, "capacity", "History schema buffer is unavailable"));
    unsigned status = 200;
    if (!*path) {
        const int newest = newest_session(); Session empty; empty.interval_ms = history_interval();
        const Session& current = newest >= 0 ? sessions[newest] : empty;
        bool valid = true;
        if (newest >= 0) { char name[12]; ::snprintf(name, sizeof(name), "m%02u", unsigned(newest)); valid = layout_load(records, name, *layout, current.metadata_generation, current.fields); }
        else valid = layout_create(snapshot, *layout);
        if (!valid) status = error(writer, Result(503, "history_storage", "The retained sensor schema could not be verified"));
        else {
            writer.text("{"); metadata_json(writer, current, !persistent, active == newest && active >= 0 ? active_elapsed() : current.duration);
            writer.format(",\"checkpoint_interval_ms\":%lu,\"capacity_records\":%u,\"capacity_sessions\":8,\"page_records\":%u,\"minimum_page_records\":2,\"fields\":",
                static_cast<unsigned long>(current.interval_ms), PointCapacity, HistoryPage);
            layout_fields(writer, *layout); writer.text(",\"sessions\":["); bool comma = false;
            // Counter IDs never wrap; descending IDs give stable newest-first order.
            std::uint32_t before = UINT32_MAX;
            for (unsigned n = 0; n < SessionCapacity; ++n) {
                int found = -1;
                for (unsigned i = 0; i < SessionCapacity; ++i)
                    if (sessions[i].id && sessions[i].id <= before && (found < 0 || sessions[i].id > sessions[found].id)) found = i;
                if (found < 0) break;
                if (comma) writer.text(",");
                writer.text("{");
                metadata_json(writer, sessions[found], false, found == active ? active_elapsed() : sessions[found].duration);
                writer.text("}"); comma = true; before = sessions[found].id - 1;
            }
            writer.text("]}");
        }
    } else {
        std::uint32_t offset = 0, id = 0; bool valid = !std::strncmp(path, "/samples/", 9);
        if (valid) { path += 9; valid = decimal_path(path, offset) && !std::strncmp(path, "?id=", 4); }
        if (valid) { path += 4; valid = decimal_path(path, id) && id && !*path; }
        int found = -1;
        if (valid) for (unsigned i = 0; i < SessionCapacity; ++i) if (sessions[i].id == id) found = i;
        if (!valid) status = error(writer, Result(400, "invalid_history_page", "Use a numeric offset and one positive id parameter"));
        else if (found < 0) status = error(writer, Result(409, "history_changed", "This session is no longer retained"));
        else if (offset > sessions[found].count) status = error(writer, Result(400, "invalid_history_offset", "Offset exceeds this session's sample count"));
        else {
            const Session& session = sessions[found]; char name[12]; ::snprintf(name, sizeof(name), "m%02u", unsigned(found));
            if (!layout_load(records, name, *layout, session.metadata_generation, session.fields)) status = error(writer, Result(503, "history_storage", "The retained sensor schema could not be verified"));
            else {
                writer.format("{\"id\":%lu,\"offset\":%lu,\"fields\":", static_cast<unsigned long>(id), static_cast<unsigned long>(offset));
                layout_fields(writer, *layout); writer.text(",\"field_metadata\":"); layout_metadata(writer, *layout);
                writer.text(",\"samples\":["); unsigned count = 0;
                if (!writer.ok) status = error(writer, Result(503, "response_capacity", "The sensor schema exceeds the response arena"));
                for (; status == 200 && count < HistoryPage && offset + count < session.count; ++count) {
                    Point point;
                    if (!point_load(records, (session.first + offset + count) % PointCapacity, id, session.fields, point)) { status = error(writer, Result(503, "history_storage", "A recorded sample could not be verified")); break; }
                    const std::size_t before = writer.length;
                    if (count) writer.text(",");
                    point_json(writer, point);
                    // A session with more named channels gets fewer rows, but
                    // never a truncated JSON object. Reserve the page trailer.
                    if (!writer.ok || writer.length + 40 >= writer.capacity) {
                        writer.length = before; writer.ok = true; writer.data[before] = 0; break;
                    }
                }
                if (status == 200 && !count && offset < session.count)
                    status = error(writer, Result(503, "response_capacity", "This sensor schema leaves no room for a sample"));
                if (status == 200) {
                    writer.text("],\"next_offset\":");
                    if (offset + count < session.count) writer.format("%lu", static_cast<unsigned long>(offset + count)); else writer.text("null");
                    writer.text("}");
                }
            }
        }
    }
    delete layout; return status;
}
unsigned Engine::Impl::dispatch(const PicoHttpRequest& request, Writer& writer) {
    const bool get = request.method == PICO_HTTP_GET;
    if (!std::strcmp(request.path, "/api/v1/health")) {
        if (!get) return method_error(writer);
        refresh(); writer.text("{"); identity_json(writer, identity); writer.text(",\"api_version\":1,\"firmware_version\":"); writer.quote(identity.firmware_version);
        writer.format(",\"mode\":\"standalone\",\"ready\":%s,\"auth_configured\":%s,\"uptime_ms\":%lu,\"storage_ready\":%s}",
            snapshot_valid && snapshot.ready ? "true" : "false", request.auth_configured ? "true" : "false", static_cast<unsigned long>(now()), persistent ? "true" : "false"); return 200;
    }
    if (!std::strcmp(request.path, "/api/v1/overview") && get) {
        const Result result = refresh();
        if (!result.ok()) return error(writer, result);
        if (!snapshot_valid) return error(writer, Result(503, "not_ready", "The device snapshot is unavailable"));
        writer.text("{"); identity_json(writer, identity);
        writer.text(",\"ready\":"); writer.boolean(snapshot.ready);
        if (pico_http_pairing_available(true)) writer.text(",\"pairing_v1\":true");
        writer.text(",\"busy\":"); writer.boolean(snapshot.busy);
        writer.text(",\"wort_f\":"); reading(writer, snapshot.wort_f);
        writer.text(",\"thermoblock_f\":"); reading(writer, snapshot.thermoblock_f);
        writer.text(",\"temperature_f\":"); reading(writer, snapshot.temperature_f);
        writer.text("}"); return 200;
    }
    if (!request.auth_configured) return error(writer, Result(503, "api_key_not_provisioned", "Provision this device's API key before using protected endpoints"));
    if (!request.authenticated) return error(writer, Result(401, "unauthorized", "A valid device bearer key is required"));
    if (!std::strcmp(request.path, "/api/v1")) return get ? discovery_json(writer) : method_error(writer);
    if (!std::strcmp(request.path, "/api/v1/status")) return get ? status_json(writer, true) : method_error(writer);
    if (!std::strcmp(request.path, "/api/v1/settings")) return settings_api(request, writer);
    if (!std::strcmp(request.path, "/api/v1/commands")) return command_api(request, writer);
    if (!std::strcmp(request.path, "/api/v1/telemetry")) return telemetry_api(request, writer);
    if (!std::strcmp(request.path, "/api/v1/recipes") || !std::strncmp(request.path, "/api/v1/recipes/", 16)) return recipes_api(request, writer);
    if (!std::strcmp(request.path, "/api/v1/display") || !std::strncmp(request.path, "/api/v1/display/", 16)) return display_api(request, writer);
    if (!std::strcmp(request.path, "/api/v1/history") || !std::strncmp(request.path, "/api/v1/history/", 16)) return history_api(request, writer);
    if (!std::strcmp(request.path, "/api/v1/events")) {
        if (!get) return method_error(writer);
        writer.text("{\"events\":[");
        for (unsigned i = 0; i < event_count; ++i) {
            const Event& e = events[(event_head + 8 - event_count + i) % 8]; if (i) writer.text(",");
            writer.format("{\"sequence\":%lu,\"uptime_ms\":%lu,\"kind\":", static_cast<unsigned long>(e.sequence), static_cast<unsigned long>(e.time));
            writer.quote(e.kind); writer.text(",\"detail\":"); writer.quote(e.detail); writer.text("}");
        }
        writer.format("],\"next_sequence\":%lu,\"capacity\":8,\"volatile\":true}", static_cast<unsigned long>(event_sequence)); return 200;
    }
    return error(writer, Result(404, "not_found", "Unknown API resource"));
}
bool Engine::handle(const PicoHttpRequest& request, PicoHttpResponse& response, char* arena, std::size_t capacity) {
    if (!std::memchr(request.path, 0, sizeof(request.path)) || std::strncmp(request.path, "/api/v1", 7)) return false;
    Writer writer(arena, capacity); unsigned status = 500;
    if ((!request.body && request.body_length) || request.body_length >= PICO_HTTP_ARENA_CAPACITY)
        status = error(writer, Result(413, "body_too_large", "Request exceeds the bounded input arena"));
    else if (!impl_) status = error(writer, Result(503, "not_ready", "Standalone initialization is incomplete"));
    else status = impl_->dispatch(request, writer);
    if (!writer.ok) status = error(writer, Result(503, "response_capacity", "The response exceeds this device's bounded arena"));
    response.status = status; response.content_type = "application/json"; response.content_encoding = nullptr;
    response.body = reinterpret_cast<const std::uint8_t*>(arena); response.body_length = writer.ok ? writer.length : 0; return true;
}
bool Engine::http_handler(const PicoHttpRequest* request, PicoHttpResponse* response, char* arena, std::size_t capacity, void* context) {
    return request && response && context && static_cast<Engine*>(context)->handle(*request, *response, arena, capacity);
}
}}
