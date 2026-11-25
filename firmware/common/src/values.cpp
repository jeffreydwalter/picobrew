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
bool recipe_valid(const Recipe& recipe, bool zero_minutes, bool z_program_maker, bool z_cloud, bool z_pak) {
    if (!valid_id(recipe.id) || !valid_text(recipe.name, sizeof(recipe.name)) ||
        !recipe.step_count || recipe.step_count > (recipe.z_program ? ZProgramStepCapacity : RecipeStepCapacity)) return false;
    const auto& kit=recipe.pak;
    if(kit.recipe_id && (!z_pak || recipe.z_program || recipe.z_cloud_water_ml || !kit.beer_id ||
       kit.abv < -1 || kit.ibu < -1 || kit.selected_abv < -1 || kit.selected_abv>10 ||
       kit.selected_ibu < -1 || kit.selected_ibu>10))return false;
    bool allow_abv=false,allow_ibu=false;
    if (recipe.z_cloud_water_ml && (!z_cloud || recipe.z_program || recipe.z_cloud_water_ml > 16163)) return false;
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
        if(kit.recipe_id){
            if(s.pak.action>1||s.location<1||s.location>6||s.pak.minimum < -1||s.pak.minimum>599||
               s.pak.maximum < -1||s.pak.maximum>599)return false;
            if(!s.pak.action && s.pak.minimum>=0 && s.pak.maximum>=0){
                if(s.location==2)allow_abv=true;
                if(s.location>=3)allow_ibu=true;
            }
        }
        // An active sub-60 F source step requires an acknowledged chiller
        // connection. The Z has no way to refrigerate the wort by itself.
        if (s.target_f && s.target_f < 60 &&
            ((recipe.z_cloud_water_ml && s.location!=6 && (!i || recipe.steps[i-1].location!=6)) ||
             (kit.recipe_id && !s.pak.action && (!i || !recipe.steps[i-1].pak.action)))) return false;
        if (!valid_text(s.name, sizeof(s.name)) || (!recipe.z_cloud_water_ml && s.location < 1) || s.location > 6 ||
            (s.target_f && (s.target_f < ((recipe.z_cloud_water_ml || kit.recipe_id) ? 32 : 60) || s.target_f > 212)) ||
            s.minutes > 599 || (!s.minutes && !zero_minutes) || s.drain_minutes > 120) return false;
    }
    if(kit.recipe_id){
        if(kit.selected_abv>=0 && kit.selected_abv!=kit.abv && (!allow_abv||kit.abv<1||kit.abv>9))return false;
        if(kit.selected_ibu>=0 && kit.selected_ibu!=kit.ibu && (!allow_ibu||kit.ibu<1||kit.ibu>9))return false;
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

bool pak_steps(Json& json,Recipe& recipe,unsigned& count){
    if(!json.token('[')||json.peek(']'))return false;
    do{
        if(count>=RecipeStepCapacity||!json.token('{')||json.peek('}'))return false;
        auto& step=recipe.steps[count++].pak;
        unsigned seen=0;
        do{
            char key[40];std::int64_t value;
            if(!json.key(key))return false;
            unsigned bit=!std::strcmp(key,"action")?1:!std::strcmp(key,"min_time")?2:!std::strcmp(key,"max_time")?4:0;
            if(!bit||(seen&bit)||!json.integer(0,bit==1?1:599,value))return false;
            if(bit==1)step.action=value;else if(bit==2)step.minimum=value;else step.maximum=value;
            seen|=bit;
        }while(json.next('}'));
        if(!json.token('}')||!(seen&1))return false;
    }while(json.next(']'));
    return json.token(']');
}
bool parse_z_pak(Json& json,Recipe& recipe,unsigned& count){
    if(!json.token('{')||json.peek('}'))return false;
    auto& p=recipe.pak;unsigned seen=0;
    do{
        char key[40];if(!json.key(key))return false;
        const char* const keys[]={"recipe_id","beer_id","abv","ibu","selected_abv","selected_ibu","step_options"};
        unsigned field=0;while(field<7&&std::strcmp(key,keys[field]))++field;
        if(field==7||(seen&(1u<<field)))return false;
        seen|=1u<<field;
        if(field==6){if(!pak_steps(json,recipe,count))return false;continue;}
        std::int64_t value;
        if(!json.integer(field<2?1:field<4?-1:0,field<2?UINT32_MAX:field<4?127:10,value))return false;
        switch(field){
        case 0:p.recipe_id=value;break;case 1:p.beer_id=value;break;
        case 2:p.abv=value;break;case 3:p.ibu=value;break;
        case 4:p.selected_abv=value;break;case 5:p.selected_ibu=value;break;
        }
    }while(json.next('}'));
    const unsigned required=(1u<<0)|(1u<<1)|(1u<<2)|(1u<<3)|(1u<<6);
    return json.token('}')&&(seen&required)==required;
}
bool parse_z_cloud(Json& json, Recipe& recipe) {
    if (!json.token('{') || json.peek('}')) return false;
    unsigned seen = 0;
    do {
        char key[40];
        if (!json.key(key)) return false;
        if (!std::strcmp(key, "start_water_ml") && !(seen & 1)) {
            std::int64_t amount;
            if (!json.integer(1, 16163, amount)) return false;
            recipe.z_cloud_water_ml = static_cast<std::uint16_t>(amount);
            seen |= 1;
        } else if (!std::strcmp(key, "is_beer") && !(seen & 2)) {
            if (!json.boolean(recipe.is_beer)) return false;
            seen |= 2;
        } else return false;
    } while (json.next('}'));
    return json.token('}') && seen == 3;
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
                if (recipe.step_count >= ZProgramStepCapacity || !parse_z_step(json, recipe.steps[recipe.step_count++])) return false;
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
    unsigned seen = 0, pak_count = 0;
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
        } else if (!std::strcmp(key,"z_pak")) {
            bit=128; if((seen&80)||!parse_z_pak(json,recipe,pak_count))return false;
        } else if (!std::strcmp(key, "z_cloud")) {
            bit = 64; if ((seen & 144) || !parse_z_cloud(json, recipe)) return false;
        } else if (!std::strcmp(key, "z_program")) {
            if (seen & 192) return false;
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
                        field = 2; if (!json.integer(0, 6, number)) return false; step.location = number;
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
    return json.token('}') && json.done() && (!recipe.pak.recipe_id || pak_count==recipe.step_count) && !(create_only && revision) && ((seen & 22) == 6 || (seen & 22) == 16);
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
    if (recipe.pak.recipe_id) {
        const auto& p=recipe.pak;
        writer.format(",\"z_pak\":{\"recipe_id\":%lu,\"beer_id\":%lu,\"abv\":%d,\"ibu\":%d",
                      static_cast<unsigned long>(p.recipe_id),static_cast<unsigned long>(p.beer_id),p.abv,p.ibu);
        if(p.selected_abv>=0)writer.format(",\"selected_abv\":%d",p.selected_abv);
        if(p.selected_ibu>=0)writer.format(",\"selected_ibu\":%d",p.selected_ibu);
        writer.text(",\"step_options\":[");
        for(unsigned i=0;i<recipe.step_count;++i){
            const auto& opt=recipe.steps[i].pak;if(i)writer.text(",");
            writer.format("{\"action\":%u",unsigned(opt.action));
            if(opt.minimum>=0)writer.format(",\"min_time\":%d",opt.minimum);
            if(opt.maximum>=0)writer.format(",\"max_time\":%d",opt.maximum);
            writer.text("}");
        }
        writer.text("]}");
    }
    if (recipe.z_cloud_water_ml) {
        writer.format(",\"z_cloud\":{\"start_water_ml\":%u,\"is_beer\":", recipe.z_cloud_water_ml);
        writer.boolean(recipe.is_beer); writer.text("}");
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
