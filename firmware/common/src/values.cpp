#include "engine_internal.hpp"
#include <cfloat>

namespace picobrew { namespace standalone { namespace detail {
const char* unit_name(Unit unit) {
    static const char* const names[] = {"F", "kPa", "V", "%", "rpm", "raw"};
    return static_cast<unsigned>(unit) < 6 ? names[static_cast<unsigned>(unit)] : "raw";
}
const char* kind_name(SensorKind kind) {
    static const char* const names[] = {"temperature", "pressure", "voltage", "battery", "speed", "other"};
    return static_cast<unsigned>(kind) < 6 ? names[static_cast<unsigned>(kind)] : "other";
}
const char* actuator_name(ActuatorKind kind) {
    static const char* const names[] = {"pump", "fan", "heater"};
    return static_cast<unsigned>(kind) < 3 ? names[static_cast<unsigned>(kind)] : "pump";
}
bool valid_id(const char* value, std::size_t capacity) {
    if (!value || !value[0]) return false;
    for (std::size_t i = 0; i < capacity; ++i) {
        const unsigned char c = value[i];
        if (!c) return true;
        if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
              (c >= '0' && c <= '9') || c == '_' || c == '-')) return false;
    }
    return false;
}
bool valid_text(const char* value, std::size_t capacity) {
    if (!value || !value[0]) return false;
    for (std::size_t i = 0; i < capacity; ++i) {
        const unsigned char c = value[i];
        if (!c) return true;
        if (c < 32 || c > 126) return false;
    }
    return false;
}
bool recipe_valid(const Recipe& recipe, bool zero_minutes, bool z_program_maker) {
    if (!valid_id(recipe.id) || !valid_text(recipe.name, sizeof(recipe.name)) ||
        !recipe.step_count || recipe.step_count > RecipeStepCapacity) return false;
    if (recipe.z_program && (!z_program_maker || !valid_text(recipe.name, 21))) return false;
    for (unsigned i = 0; i < recipe.step_count; ++i) {
        const RecipeStep& s = recipe.steps[i];
        if (recipe.z_program) {
            const auto& z = s.z;
            if (!valid_text(s.name, 21) || z.seconds > 86400 || (!z.has_time && z.seconds)) return false;
            if (z.pause) {
                if (s.location || z.target_c != 0 || z.null_target || z.drain_seconds) return false;
            } else if (s.location < 1 || s.location > 6 || !std::isfinite(z.target_c) ||
                       z.target_c < -29 || z.target_c > 98 || (z.null_target && z.target_c != 0) ||
                       !z.has_time || z.drain_seconds > 7200) return false;
            continue;
        }
        if (!valid_text(s.name, sizeof(s.name)) || s.location < 1 || s.location > 6 ||
            (s.target_f && (s.target_f < 60 || s.target_f > 212)) ||
            s.minutes > 599 || (!s.minutes && !zero_minutes) || s.drain_minutes > 120) return false;
    }
    return true;
}
bool settings_valid(const Settings& settings) {
    return (settings.temperature_unit == 'F' || settings.temperature_unit == 'C') &&
           settings.language[2] == 0 && (!std::strcmp(settings.language, "en") ||
           !std::strcmp(settings.language, "es") || !std::strcmp(settings.language, "fr"));
}
bool telemetry_valid(const TelemetryConfig& config) {
    if (!config.port || !std::memchr(config.host, 0, sizeof(config.host))) return false;
    if (!config.host[0]) return !config.enabled;
    bool numeric = true;
    unsigned label = 0;
    unsigned char previous = 0;
    for (const unsigned char* p = reinterpret_cast<const unsigned char*>(config.host); *p; ++p) {
        const unsigned c = *p;
        if (c == '.') {
            if (!label || previous == '-') return false;
            label = 0;
        } else {
            if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
                  (c >= '0' && c <= '9') || c == '-') || (!label && c == '-') || ++label > 63) return false;
            if (c < '0' || c > '9') numeric = false;
        }
        previous = c;
    }
    if (!label || previous == '-') return false;
    if (!numeric) return true;
    const char* p = config.host;
    std::uint32_t address = 0;
    for (unsigned part = 0; part < 4; ++part) {
        unsigned n = 0, digits = 0;
        while (*p >= '0' && *p <= '9') {
            n = n * 10 + (*p++ - '0');
            if (++digits > 3 || n > 255) return false;
        }
        if (!digits || (part < 3 ? *p++ != '.' : *p != 0)) return false;
        address = (address << 8) | n;
    }
    return address && address != UINT32_MAX;
}
namespace {
bool parse_z_step(Json& json, RecipeStep& step) {
    if (!json.token('{') || json.peek('}')) return false;
    enum { Name=1, Type=2, Time=4, Location=8, Temp=16, Drain=32 };
    unsigned seen = 0;
    do {
        char key[40];
        unsigned bit = 0;
        std::int64_t value = 0;
        if (!json.key(key)) return false;
        if (!std::strcmp(key, "Name")) {
            bit = Name; if (!json.string(step.name, 21)) return false;
        } else if (!std::strcmp(key, "StepType")) {
            bit = Type; if (!json.integer(1, 2, value)) return false;
            step.z.pause = value == 2;
        } else if (!std::strcmp(key, "Time")) {
            bit = Time; if (!json.integer(0, 86400, value)) return false;
            step.z.seconds = value; step.z.has_time = true;
        } else if (!std::strcmp(key, "Location")) {
            bit = Location; if (!json.integer(1, 6, value)) return false;
            step.location = value;
        } else if (!std::strcmp(key, "Temp")) {
            bit = Temp;
            if (json.peek('n')) {
                if (json.end - json.p < 4 || std::memcmp(json.p, "null", 4)) return false;
                json.p += 4; step.z.null_target = true;
            } else if (!json.number(step.z.target_c)) return false;
        } else if (!std::strcmp(key, "Drain")) {
            bit = Drain; if (!json.integer(0, 7200, value)) return false;
            step.z.drain_seconds = value;
        } else return false;
        if (seen & bit) return false;
        seen |= bit;
    } while (json.next('}'));
    if (!json.token('}')) return false;
    if (step.z.pause) return (seen & ~Time) == (Name | Type);
    return seen == (Name | Type | Time | Location | Temp | Drain);
}

bool parse_z_program(Json& json, Recipe& recipe) {
    if (!json.token('{') || json.peek('}')) return false;
    unsigned seen = 0;
    recipe.z_program = true;
    do {
        char key[40]; unsigned bit = 0;
        if (!json.key(key)) return false;
        if (!std::strcmp(key, "Name")) {
            bit = 1; if (!json.string(recipe.name, 21)) return false;
        } else if (!std::strcmp(key, "IsBeer")) {
            bit = 2; if (!json.boolean(recipe.is_beer)) return false;
        } else if (!std::strcmp(key, "UsePicoPakMode")) {
            bit = 4; if (!json.boolean(recipe.use_pico_pak)) return false;
        } else if (!std::strcmp(key, "Steps")) {
            bit = 8;
            if (!json.token('[') || json.peek(']')) return false;
            do {
                if (recipe.step_count >= RecipeStepCapacity || !parse_z_step(json, recipe.steps[recipe.step_count++])) return false;
            } while (json.next(']'));
            if (!json.token(']')) return false;
        } else if (!std::strcmp(key, "IsMetric")) {
            bit = 16; if (!json.boolean(recipe.is_metric)) return false;
            recipe.has_metric = true;
        } else return false;
        if (seen & bit) return false;
        seen |= bit;
    } while (json.next('}'));
    return json.token('}') && (seen & 15) == 15;
}
void z_program_json(Writer& writer, const Recipe& recipe) {
    writer.text("{\"Name\":"); writer.quote(recipe.name);
    writer.text(",\"IsBeer\":"); writer.boolean(recipe.is_beer);
    writer.text(",\"UsePicoPakMode\":"); writer.boolean(recipe.use_pico_pak);
    if (recipe.has_metric) { writer.text(",\"IsMetric\":"); writer.boolean(recipe.is_metric); }
    writer.text(",\"Steps\":[");
    for (unsigned i = 0; i < recipe.step_count; ++i) {
        if (i) writer.text(",");
        const auto& step = recipe.steps[i];
        writer.text("{\"Name\":"); writer.quote(step.name);
        writer.format(",\"StepType\":%u", step.z.pause ? 2u : 1u);
        if (step.z.has_time) writer.format(",\"Time\":%lu", static_cast<unsigned long>(step.z.seconds));
        if (!step.z.pause) {
            writer.format(",\"Location\":%u,\"Drain\":%u,\"Temp\":", unsigned(step.location), unsigned(step.z.drain_seconds));
            if (step.z.null_target) writer.text("null"); else writer.format("%.17g", step.z.target_c);
        }
        writer.text("}");
    }
    writer.text("]}");
}
}

bool parse_recipe(Json& json, const char* id, Recipe& recipe, bool& create_only, std::uint32_t& revision) {
    if (!valid_id(id) || !json.token('{') || json.peek('}')) return false;
    copy(recipe.id, sizeof(recipe.id), id);
    unsigned seen = 0;
    do {
        char key[40]; unsigned bit = 0;
        if (!json.key(key)) return false;
        if (!std::strcmp(key, "id")) {
            char supplied[21]; bit = 1;
            if (!json.string(supplied, sizeof(supplied)) || std::strcmp(supplied, id)) return false;
        } else if (!std::strcmp(key, "create_only")) {
            bit = 8; if (!json.boolean(create_only)) return false;
        } else if (!std::strcmp(key, "if_revision") || !std::strcmp(key, "revision")) {
            std::int64_t number;
            bit = 32; if (!json.integer(1, UINT32_MAX, number)) return false;
            revision = static_cast<std::uint32_t>(number);
        } else if (!std::strcmp(key, "name")) {
            bit = 2; if (!json.string(recipe.name, sizeof(recipe.name))) return false;
        } else if (!std::strcmp(key, "z_program")) {
            bit = 16; if ((seen & 6) || !parse_z_program(json, recipe)) return false;
        } else if (!std::strcmp(key, "steps")) {
            bit = 4;
            if (!json.token('[') || json.peek(']')) return false;
            do {
                if (recipe.step_count >= RecipeStepCapacity || !json.token('{') || json.peek('}')) return false;
                RecipeStep& step = recipe.steps[recipe.step_count++];
                unsigned fields = 0;
                do {
                    char name[40]; unsigned field = 0; std::int64_t number = 0;
                    if (!json.key(name)) return false;
                    if (!std::strcmp(name, "name")) {
                        field = 1; if (!json.string(step.name, sizeof(step.name))) return false;
                    } else if (!std::strcmp(name, "location")) {
                        field = 2; if (!json.integer(1, 6, number)) return false; step.location = number;
                    } else if (!std::strcmp(name, "target_f")) {
                        field = 4; if (!json.integer(0, 212, number)) return false; step.target_f = number;
                    } else if (!std::strcmp(name, "minutes")) {
                        field = 8; if (!json.integer(0, 599, number)) return false; step.minutes = number;
                    } else if (!std::strcmp(name, "drain_minutes")) {
                        field = 16; if (!json.integer(0, 120, number)) return false; step.drain_minutes = number;
                    } else return false;
                    if (fields & field) return false;
                    fields |= field;
                } while (json.next('}'));
                if (!json.token('}') || (fields & 15) != 15) return false;
            } while (json.next(']'));
            if (!json.token(']')) return false;
        } else return false;
        if (seen & bit) return false;
        seen |= bit;
    } while (json.next('}'));
    return json.token('}') && json.done() && !(create_only && revision) && ((seen & 22) == 6 || (seen & 22) == 16);
}
bool parse_command(Json& json, Command& command) {
    if (!json.token('{') || json.peek('}')) return false;
    bool name_seen = false;
    do {
        char key[40]; std::uint32_t field = 0; std::int64_t value = 0;
        if (!json.key(key)) return false;
        if (!std::strcmp(key, "command")) {
            if (name_seen || !json.string(command.name, sizeof(command.name))) return false;
            name_seen = true;
        } else if (!std::strcmp(key, "recipe_id")) {
            field = RecipeId; if (!json.string(command.recipe_id, sizeof(command.recipe_id))) return false;
        } else if (!std::strcmp(key, "delta")) {
            field = Delta; if (!json.integer(-100, 100, value)) return false; command.delta = value;
        } else if (!std::strcmp(key, "click")) {
            field = Click; if (!json.boolean(command.click)) return false;
        } else if (!std::strcmp(key, "channel")) {
            field = Channel; if (!json.integer(0, INT32_MAX, value)) return false; command.channel = value;
        } else if (!std::strcmp(key, "level")) {
            field = Level; if (!json.number(command.level) || command.level < 0 || command.level > 100) return false;
        } else if (!std::strcmp(key, "enabled")) {
            field = Enabled; if (!json.boolean(command.enabled)) return false;
        } else if (!std::strcmp(key, "value")) {
            field = Value; if (!json.number(command.value)) return false;
        } else if (!std::strcmp(key, "duration_seconds")) {
            field = DurationSeconds; if (!json.integer(0, UINT32_MAX, value)) return false; command.duration_seconds = value;
        } else if (!std::strcmp(key, "confirm")) {
            field = Confirm; if (!json.boolean(command.confirm)) return false;
        } else return false;
        if (field && (command.fields & field)) return false;
        command.fields |= field;
    } while (json.next('}'));
    return json.token('}') && json.done() && name_seen && command.name[0];
}
unsigned error(Writer& writer, Result result) {
    writer.reset(); writer.text("{\"error\":"); writer.quote(result.error ? result.error : "error");
    writer.text(",\"message\":"); writer.quote(result.message ? result.message : "Operation failed"); writer.text("}");
    return result.status;
}
void reading(Writer& writer, Reading value) {
    if (value.valid) writer.number(value.value); else writer.text("null");
}
void recipe_json(Writer& writer, const Recipe& recipe, std::uint32_t revision) {
    writer.format("{\"revision\":%lu,\"id\":", static_cast<unsigned long>(revision)); writer.quote(recipe.id);
    if (recipe.z_program) {
        writer.text(",\"z_program\":"); z_program_json(writer, recipe); writer.text("}"); return;
    }
    writer.text(",\"name\":"); writer.quote(recipe.name);
    writer.text(",\"steps\":[");
    for (unsigned i = 0; i < recipe.step_count; ++i) {
        const RecipeStep& step = recipe.steps[i];
        if (i) writer.text(",");
        writer.text("{\"name\":"); writer.quote(step.name);
        writer.format(",\"location\":%u,\"target_f\":%u,\"minutes\":%u,\"drain_minutes\":%u}",
                      step.location, step.target_f, step.minutes, step.drain_minutes);
    }
    writer.text("]}");
}
void settings_json(Writer& writer, const Settings& settings, std::uint32_t fields) {
    writer.text("{"); bool comma = false;
    if (fields & TemperatureUnit) { writer.format("\"temperature_unit\":\"%c\"", settings.temperature_unit); comma = true; }
    if (fields & Language) { if (comma) writer.text(","); writer.text("\"language\":"); writer.quote(settings.language); comma = true; }
    if (fields & DetailedStatus) { if (comma) writer.text(","); writer.text("\"detailed_status\":"); writer.boolean(settings.detailed_status); comma = true; }
    if (fields & AutoCycle) { if (comma) writer.text(","); writer.text("\"auto_cycle\":"); writer.boolean(settings.auto_cycle); }
    writer.text("}");
}
}}}
