#include "engine_internal.hpp"
#include "recipe_record.hpp"

namespace picobrew { namespace standalone {
using namespace detail;
namespace {
constexpr unsigned CatalogueBytes = RecipeCapacity * 58;
Result storage_error() { return Result(503, "storage_io", "Persistent state could not be read or verified"); }
bool encode_recipe(const Records& records, unsigned slot, const Recipe& recipe,
                   std::uint32_t previous, std::uint32_t& generation) {
    std::uint8_t bytes[ZRecipeBytes] = {};
    const auto size = write_recipe_record(bytes, recipe);
    char name[12]; ::snprintf(name, sizeof(name), "r%02u", slot);
    return records.save(name, bytes, size, previous, generation) == RecordResult::Ok;
}
}
Engine::Engine(DeviceServices device, Storage storage, Clock clock) noexcept
    : device_(device), storage_(storage), clock_(clock) {}
Engine::~Engine() { delete impl_; }
Engine::Impl::~Impl() { tx_close(); delete active_layout; delete[] recipes; }
std::uint32_t Engine::Impl::now() const { return owner.clock_.millis ? owner.clock_.millis(owner.clock_.context) : 0; }
std::uint32_t Engine::Impl::history_interval() const {
    return owner.device_.history_interval_ms ? owner.device_.history_interval_ms(owner.device_.context) : SamplePeriod;
}
std::uint32_t Engine::Impl::active_elapsed() const {
    const std::uint32_t elapsed = now() - started_at;
    return active >= 0 && elapsed < sessions[active].duration ? UINT32_MAX : elapsed;
}
bool Engine::Impl::idle() const { return owner.device_.idle && owner.device_.idle(owner.device_.context); }
void Engine::set_ota_protocol(const char* protocol) noexcept {
    if (impl_) impl_->capabilities.ota_protocol = protocol;
}
Result Engine::begin() {
    if (!impl_) impl_ = new (std::nothrow) Impl(*this);
    if (!impl_) return Result(503, "capacity", "The standalone engine could not allocate its bounded state");
    if (impl_->initialized) return impl_->persistent ? Result() : storage_error();
    return impl_->initialize();
}
bool Engine::persistence_ready() const noexcept { return impl_ && impl_->persistent; }
void Engine::set_telemetry_io(TelemetryIo io) noexcept {
    if (impl_) impl_->tx_close();
    telemetry_ = io;
    if (impl_) { impl_->tx_state = impl_->telemetry.enabled ? Impl::TxState::Ready : Impl::TxState::Disabled; impl_->tx_scheduled = impl_->now() - SamplePeriod; }
}
Result Engine::Impl::refresh() {
    if (!owner.device_.snapshot) return Result(503, "not_ready", "The device snapshot is unavailable");
    const Result result = owner.device_.snapshot(owner.device_.context, snapshot);
    snapshot_valid = result.ok() && snapshot.sensor_count <= SensorCapacity && snapshot.actuator_count <= ActuatorCapacity &&
        snapshot.device_state <= 255 && snapshot.phase <= 255 && snapshot.controller_status <= 255 &&
        snapshot.compartment <= 255 && snapshot.current_step >= -1 && snapshot.current_step <= 255 && snapshot.step_count <= 255 &&
        std::memchr(snapshot.recipe_name, 0, sizeof(snapshot.recipe_name)) && std::memchr(snapshot.step_name, 0, sizeof(snapshot.step_name));
    if (!result.ok()) return result;
    if (!snapshot_valid) return Result(503, "invalid_snapshot", "The device supplied an invalid bounded snapshot");
    const Recovery& recovery = snapshot.recovery;
    if (recovery.supported &&
        (!std::memchr(recovery.recipe_name, 0, sizeof(recovery.recipe_name)) ||
         !std::memchr(recovery.step_name, 0, sizeof(recovery.step_name)) ||
         (recovery.recipe_name[0] && !valid_text(recovery.recipe_name, sizeof(recovery.recipe_name))) ||
         (recovery.step_name[0] && !valid_text(recovery.step_name, sizeof(recovery.step_name))) ||
         recovery.current_step < -1 || recovery.current_step > 255 || recovery.step_seconds < 0 ||
         (recovery.available && recovery.valid && !recovery.recipe_name[0]))) {
        snapshot_valid = false; return Result(503, "invalid_snapshot", "Invalid interrupted-session metadata");
    }
    for (std::size_t i = 0; i < snapshot.sensor_count; ++i)
        if (!valid_id(snapshot.sensors[i].id) || !valid_text(snapshot.sensors[i].label, 32)) {
            snapshot_valid = false; return Result(503, "invalid_snapshot", "Invalid sensor identity");
        }
    for (std::size_t i = 0; i < snapshot.actuator_count; ++i)
        if (!valid_id(snapshot.actuators[i].id) || !valid_text(snapshot.actuators[i].label, 32)) {
            snapshot_valid = false; return Result(503, "invalid_snapshot", "Invalid actuator identity");
        }
    return Result();
}
Result Engine::Impl::save_config(const Settings& next, const TelemetryConfig& forwarding) {
    std::uint8_t bytes[136] = {};
    bytes[0] = next.temperature_unit; bytes[1] = next.detailed_status;
    bytes[2] = next.language[0]; bytes[3] = next.language[1]; bytes[4] = forwarding.enabled;
    bytes[6] = forwarding.port; bytes[7] = forwarding.port >> 8;
    std::memcpy(bytes + 8, forwarding.host, sizeof(forwarding.host));
    std::uint32_t generation = 0;
    if (records.save("cfg", bytes, sizeof(bytes), config_generation, generation) != RecordResult::Ok) return storage_error();
    config_generation = generation; settings = next; telemetry = forwarding; return Result();
}
Result Engine::Impl::initialize() {
    initialized = true;
    if (!owner.device_.identity || !owner.device_.capabilities || !owner.device_.snapshot ||
        !owner.device_.execute || !owner.device_.idle || !owner.storage_.read ||
        !owner.storage_.write_atomic || !owner.clock_.millis) return Result(503, "not_ready", "Required standalone ports are not bound");
    identity = owner.device_.identity(owner.device_.context);
    capabilities = owner.device_.capabilities(owner.device_.context);
    if (history_interval() < SamplePeriod || history_interval() > 86400000u)
        return Result(503, "invalid_capabilities", "History interval must be between 15 seconds and one day");
    if (!valid_id(identity.model, 32) || !valid_id(identity.model_family, 32) ||
        !valid_text(identity.model_name, 64) || !valid_text(identity.firmware_version, 32) ||
        !valid_text(identity.device_uid, 64) || capabilities.command_count > 24 ||
        (capabilities.command_count && !capabilities.commands) || capabilities.starter_recipe_count > RecipeCapacity ||
        (capabilities.starter_recipe_count && !capabilities.starter_recipes))
        return Result(503, "invalid_identity", "The device contract is incomplete");
    for (std::size_t i = 0; i < capabilities.command_count; ++i) {
        const CommandDescriptor& command = capabilities.commands[i];
        if (!valid_id(command.name, 32) || command.allowed_fields & ~511u ||
            command.required_fields & ~command.allowed_fields) return Result(503, "invalid_capabilities", "Invalid command descriptor");
        for (std::size_t j = 0; j < i; ++j)
            if (!std::strcmp(command.name, capabilities.commands[j].name)) return Result(503, "invalid_capabilities", "Duplicate command descriptor");
    }
    if (owner.device_.read_settings) {
        const Result read = owner.device_.read_settings(owner.device_.context, settings); if (!read.ok()) return read;
    }
    std::uint8_t bytes[136] = {}; std::size_t length = 0;
    const RecordResult config = records.load("cfg", bytes, sizeof(bytes), length, config_generation);
    if (config == RecordResult::Ok) {
        if (length != sizeof(bytes) || bytes[1] > 1 || bytes[4] > 1) return storage_error();
        settings.temperature_unit = bytes[0]; settings.detailed_status = bytes[1];
        settings.language[0] = bytes[2]; settings.language[1] = bytes[3]; settings.language[2] = 0;
        telemetry.enabled = bytes[4]; telemetry.port = bytes[6] | (unsigned(bytes[7]) << 8);
        std::memcpy(telemetry.host, bytes + 8, sizeof(telemetry.host));
        if (!settings_valid(settings) || !telemetry_valid(telemetry)) return storage_error();
    } else if (config != RecordResult::Missing) return storage_error();
    if (!settings_valid(settings)) return Result(503, "invalid_settings", "Device settings are invalid");
    if (owner.device_.validate_settings) {
        Result result = owner.device_.validate_settings(owner.device_.context, settings, capabilities.settings); if (!result.ok()) return result;
    }
    if (owner.device_.apply_settings) {
        Result result = owner.device_.apply_settings(owner.device_.context, settings, capabilities.settings); if (!result.ok()) return result;
    }
    if (config == RecordResult::Missing && !save_config(settings, telemetry).ok()) return storage_error();
    if (capabilities.flags & Recipes) {
        recipes = new (std::nothrow) RecipeEntry[RecipeCapacity];
        if (!recipes) return Result(503, "capacity", "The recipe catalogue could not allocate its bounded state");
        if (!load_recipes()) {
            for (unsigned i = 0; i < RecipeCapacity; ++i) recipes[i] = RecipeEntry();
            return storage_error();
        }
    }
    std::uint8_t seq[4] = {}; std::uint32_t generation = 0;
    const RecordResult sequence_result = records.load("seq", seq, sizeof(seq), length, sequence_generation, 0, true);
    if (sequence_result == RecordResult::Ok) {
        if (length != 4) return storage_error();
        sequence = get32(seq);
    } else if (sequence_result != RecordResult::Missing) return storage_error();
    if (!load_history()) {
        for (Session& session : sessions) session = Session();
        return storage_error();
    }
    history_loaded = true;
    // A missing/corrupt identity counter is never reconstructed from a smaller
    // retained catalogue: previously exported/evicted IDs must not be reused.
    if (sequence_result == RecordResult::Missing) {
        if (history_generation) return storage_error();
        if (records.save("seq", seq, sizeof(seq), 0, generation) != RecordResult::Ok) return storage_error();
        sequence_generation = generation;
    }
    for (const Session& s : sessions) if (s.id > sequence) return storage_error();
    if (!history_generation && !save_history(sessions, next_point)) return storage_error();
    bool interrupted = false;
    for (Session& s : sessions) if (s.state == SessionState::Recording) {
        s.state = SessionState::Interrupted; s.recovered = true; interrupted = true;
    }
    if (interrupted && !save_history(sessions, next_point)) return storage_error();
    std::uint8_t care[2] = {};
    const auto loaded_care = records.load("care", care, sizeof care, length, care_generation);
    if (loaded_care == RecordResult::Missing) {
        care_available = true;
        if (!std::strcmp(identity.model, "picoferm")) care_flags = SealDue;
    } else if (loaded_care == RecordResult::Ok && length == sizeof care && care[0] < 16 && care[1] <= 3) {
        care_flags = care[0]; brews_since_clean = care[1]; care_available = true;
    } else owner.event("maintenance", "Care record could not be read");
    persistent = true; tx_state = telemetry.enabled ? TxState::Ready : TxState::Disabled;
    tx_scheduled = now() - SamplePeriod;
    owner.event("startup", interrupted ? "interrupted session recovered" : "standalone ready");
    refresh(); return Result();
}
unsigned Engine::care_due() const noexcept {
    return impl_ && impl_->care_available ? impl_->care_flags : 0;
}
Result Engine::record_care(CareEvent change) {
    if (!impl_ || !impl_->care_available) return storage_error();
    std::uint8_t flags = impl_->care_flags, brews = impl_->brews_since_clean;
    switch (change) {
        case CareEvent::BrewUsed:
            if (brews < 3) ++brews;
            if (brews == 3) flags |= CleanDue;
            flags |= RinseDue; break;
        case CareEvent::RinseNeeded: flags |= RinseDue; break;
        case CareEvent::RinseFinished: flags &= ~RinseDue; break;
        case CareEvent::CleanFinished: flags &= ~(RinseDue | CleanDue); brews = 0; break;
        case CareEvent::StillUsed: flags |= CondenserDue; break;
        case CareEvent::CondenserCleaned: flags &= ~CondenserDue; break;
        case CareEvent::FermUsed: flags |= SealDue; break;
        case CareEvent::SealSanitized: flags &= ~SealDue; break;
    }
    if (flags == impl_->care_flags && brews == impl_->brews_since_clean) return Result();
    const std::uint8_t data[] = {flags, brews};
    std::uint32_t generation = 0;
    if (impl_->records.save("care", data, sizeof data, impl_->care_generation, generation) != RecordResult::Ok) {
        event("maintenance", "Care record could not be saved"); return storage_error();
    }
    impl_->care_flags = flags; impl_->brews_since_clean = brews; impl_->care_generation = generation;
    return Result();
}
void Engine::poll() { poll(true); }
void Engine::poll(bool network) {
    if (!impl_ || !impl_->initialized) return;
    if (impl_->refresh().ok()) impl_->history_poll();
    if (network) impl_->tx_poll();
}
bool Engine::forwarding_enabled() const { return impl_ && impl_->telemetry.enabled; }
bool Engine::forwarding_active() const { return impl_ && impl_->tx_open; }
void Engine::schedule_forwarding_now() {
    if (!impl_ || !impl_->telemetry.enabled || impl_->tx_open) return;
    impl_->tx_scheduled = impl_->now() - SamplePeriod;
    impl_->tx_state = Impl::TxState::Ready;
}
void Engine::event(const char* kind, const char* message) {
    if (!impl_) return;
    Event& event = impl_->events[impl_->event_head]; event.sequence = impl_->event_sequence++;
    if (!impl_->event_sequence) impl_->event_sequence = 1;
    event.time = impl_->now(); copy(event.kind, sizeof(event.kind), kind ? kind : "event");
    copy(event.detail, sizeof(event.detail), message);
    impl_->event_head = (impl_->event_head + 1) % 8; if (impl_->event_count < 8) ++impl_->event_count;
}
bool Engine::Impl::save_catalogue(const RecipeEntry* entries) {
    Bytes bytes(CatalogueBytes); if (!bytes.data) return false;
    for (unsigned i = 0; i < RecipeCapacity; ++i) {
        std::uint8_t* p = bytes.data + i * 58;
        std::memcpy(p, entries[i].id, 21); std::memcpy(p + 21, entries[i].name, 32);
        p[53] = entries[i].steps; put32(p + 54, entries[i].generation);
    }
    std::uint32_t generation = 0;
    if (records.save("cat", bytes.data, bytes.size, catalogue_generation, generation) != RecordResult::Ok) return false;
    catalogue_generation = generation; std::memcpy(recipes, entries, RecipeCapacity * sizeof(RecipeEntry)); return true;
}
bool Engine::Impl::load_recipes() {
    Bytes bytes(CatalogueBytes); if (!bytes.data) return false;
    std::size_t length = 0;
    const RecordResult result = records.load("cat", bytes.data, bytes.size, length, catalogue_generation);
    if (result == RecordResult::Missing) return save_catalogue(recipes);
    if (result != RecordResult::Ok || length != bytes.size) return false;
    for (unsigned i = 0; i < RecipeCapacity; ++i) {
        const std::uint8_t* p = bytes.data + i * 58; RecipeEntry& e = recipes[i];
        std::memcpy(e.id, p, 21); std::memcpy(e.name, p + 21, 32); e.steps = p[53]; e.generation = get32(p + 54);
        if (!e.id[0]) continue;
        if (!valid_id(e.id) || !valid_text(e.name, 32) || !e.steps || e.steps > RecipeStepCapacity || !e.generation) return false;
        for (unsigned j = 0; j < i; ++j) if (!std::strcmp(e.id, recipes[j].id)) return false;
        Recipe recipe; if (!recipe_read(i, recipe).ok()) return false;
    }
    return true;
}
Result Engine::Impl::recipe_read(unsigned slot, Recipe& recipe) const {
    if (!recipes || slot >= RecipeCapacity || !recipes[slot].id[0]) return Result(404, "recipe_not_found", "Recipe not found");
    std::uint8_t bytes[ZRecipeBytes]; std::size_t length = 0; std::uint32_t generation = 0;
    char name[12]; ::snprintf(name, sizeof(name), "r%02u", slot);
    if (records.load(name, bytes, sizeof(bytes), length, generation, recipes[slot].generation) != RecordResult::Ok ||
        !read_recipe_record(recipe, bytes, length)) return storage_error();
    if (!recipe_valid(recipe, capabilities.zero_minute_steps, capabilities.z_program_maker) || std::strcmp(recipe.id, recipes[slot].id) ||
        std::strcmp(recipe.name, recipes[slot].name) || recipe.step_count != recipes[slot].steps) return storage_error();
    return owner.device_.validate_recipe ? owner.device_.validate_recipe(owner.device_.context, recipe) : Result();
}
std::uint32_t Engine::recipe_revision() const noexcept {
    return impl_ && impl_->persistent ? impl_->catalogue_generation : 0;
}
std::size_t Engine::recipe_count() const noexcept {
    std::size_t count = 0;
    if (impl_ && impl_->recipes && (impl_->capabilities.flags & Recipes))
        for (unsigned i = 0; i < RecipeCapacity; ++i) if (impl_->recipes[i].id[0]) ++count;
    return count;
}
Result Engine::recipe_at(std::size_t ordinal, Recipe& output) const {
    if (!impl_ || !(impl_->capabilities.flags & Recipes)) return Result(404, "not_found", "Recipes are unavailable");
    if (!impl_->persistent) return storage_error();
    for (unsigned i = 0; i < RecipeCapacity; ++i) if (impl_->recipes[i].id[0]) {
        if (!ordinal--) return impl_->recipe_read(i, output);
    }
    return Result(404, "recipe_not_found", "Recipe not found");
}
Result Engine::recipe_get(const char* id, Recipe& output) const {
    if (!impl_ || !(impl_->capabilities.flags & Recipes)) return Result(404, "not_found", "Recipes are unavailable");
    if (!impl_->persistent) return storage_error();
    if (!valid_id(id)) return Result(400, "invalid_recipe", "Invalid recipe ID");
    for (unsigned i = 0; i < RecipeCapacity; ++i) if (!std::strcmp(impl_->recipes[i].id, id)) return impl_->recipe_read(i, output);
    return Result(404, "recipe_not_found", "Recipe not found");
}
Result Engine::Impl::recipe_write(const Recipe& recipe, bool* created, bool create_only, std::uint32_t revision) {
    if (created) *created = false;
    if (!(capabilities.flags & Recipes)) return Result(404, "not_found", "Recipes are unavailable");
    if (!persistent) return storage_error();
    if (!idle()) return Result(409, "busy", "Change recipes only while the appliance is idle");
    if (!recipe_valid(recipe, capabilities.zero_minute_steps, capabilities.z_program_maker)) return Result(400, "invalid_recipe", "Invalid recipe fields or bounds");
    if (owner.device_.validate_recipe) {
        const Result valid = owner.device_.validate_recipe(owner.device_.context, recipe); if (!valid.ok()) return valid;
    }
    int slot = -1; bool existing = false;
    for (unsigned i = 0; i < RecipeCapacity; ++i) {
        if (!std::strcmp(recipes[i].id, recipe.id)) { slot = i; existing = true; break; }
        if (!recipes[i].id[0] && slot < 0) slot = i;
    }
    if (revision && (!existing || revision != catalogue_generation)) return Result(409, "recipe_changed", "Saved recipes changed. Reopen this recipe before editing.");
    if (existing && create_only) return Result(409, "recipe_exists", "Recipe ID is already in use. Choose another ID or open the saved recipe to edit it.");
    if (slot < 0) return Result(507, "recipe_capacity", "The recipe catalogue is full");
    RecipeEntry staged[RecipeCapacity]; std::memcpy(staged, recipes, sizeof(staged));
    std::uint32_t generation = 0;
    // The catalogue's committed generation selects the opposite recipe bank.
    // An orphan from a failed catalogue write never destroys the old recipe.
    if (!encode_recipe(records, slot, recipe, recipes[slot].generation, generation)) return storage_error();
    copy(staged[slot].id, sizeof(staged[slot].id), recipe.id); copy(staged[slot].name, sizeof(staged[slot].name), recipe.name);
    staged[slot].steps = recipe.step_count; staged[slot].generation = generation;
    if (!save_catalogue(staged)) return storage_error();
    if (created) *created = !existing;
    owner.event("recipe", existing ? "updated" : "created"); return Result();
}
Result Engine::recipe_store(const Recipe& recipe, bool* created) {
    return impl_ ? impl_->recipe_write(recipe, created) : Result(503, "not_ready", "Standalone initialization is incomplete");
}
Result Engine::Impl::recipe_delete(const char* id, std::uint32_t revision) {
    if (!(capabilities.flags & Recipes)) return Result(404, "not_found", "Recipes are unavailable");
    if (!persistent) return storage_error();
    if (!idle()) return Result(409, "busy", "Change recipes only while the appliance is idle");
    if (!valid_id(id)) return Result(400, "invalid_recipe", "Invalid recipe ID");
    if (revision && revision != catalogue_generation) return Result(409, "recipe_changed", "Saved recipes changed. Reopen this recipe before editing.");
    for (unsigned i = 0; i < RecipeCapacity; ++i) if (!std::strcmp(recipes[i].id, id)) {
        RecipeEntry staged[RecipeCapacity]; std::memcpy(staged, recipes, sizeof(staged));
        // Preserve the bank generation after deletion. Reusing this slot must
        // not accidentally select an older record with a larger generation.
        staged[i].id[0] = staged[i].name[0] = 0; staged[i].steps = 0;
        if (!save_catalogue(staged)) return storage_error();
        owner.event("recipe", "deleted"); return Result();
    }
    if (revision) return Result(409, "recipe_changed", "Saved recipes changed. Reopen this recipe before editing.");
    return Result(404, "recipe_not_found", "Recipe not found");
}
Result Engine::recipe_remove(const char* id) {
    return impl_ ? impl_->recipe_delete(id) : Result(503, "not_ready", "Standalone initialization is incomplete");
}
Result Engine::preferences_store(const Settings& value, std::uint32_t changed) {
    if (!impl_ || !impl_->persistent) return storage_error();
    if (!changed || (changed & ~impl_->capabilities.settings) || !settings_valid(value))
        return Result(400, "invalid_settings", "Invalid or unsupported preference fields");
    Settings next = impl_->settings;
    if (changed & TemperatureUnit) next.temperature_unit = value.temperature_unit;
    if (changed & Language) std::memcpy(next.language, value.language, sizeof(next.language));
    if (changed & DetailedStatus) next.detailed_status = value.detailed_status;
    if (device_.validate_settings) { const Result result = device_.validate_settings(device_.context, next, changed); if (!result.ok()) return result; }
    const Settings previous = impl_->settings;
    if (device_.apply_settings) {
        const Result result = device_.apply_settings(device_.context, next, changed);
        if (!result.ok()) { device_.apply_settings(device_.context, previous, changed); return result; }
    }
    const Result saved = impl_->save_config(next, impl_->telemetry);
    if (!saved.ok()) { if (device_.apply_settings) device_.apply_settings(device_.context, previous, changed); return saved; }
    event("settings", "changed"); return Result();
}
Result Engine::Impl::restore_defaults(unsigned& added, unsigned& preserved) {
    added = preserved = 0;
    if (!(capabilities.flags & Recipes) || !capabilities.starter_recipe_count) return Result(404, "not_found", "No device-specific starter catalogue is supplied");
    if (!persistent) return storage_error();
    if (!idle()) return Result(409, "busy", "Restore recipes only while the appliance is idle");
    RecipeEntry staged[RecipeCapacity]; std::memcpy(staged, recipes, sizeof(staged));
    unsigned empty = 0, missing = 0;
    for (const RecipeEntry& entry : staged) if (!entry.id[0]) ++empty;
    for (std::size_t n = 0; n < capabilities.starter_recipe_count; ++n) {
        const Recipe& recipe = capabilities.starter_recipes[n]; bool found = false;
        if (!recipe_valid(recipe, capabilities.zero_minute_steps, capabilities.z_program_maker)) return Result(503, "invalid_defaults", "Invalid native starter recipe");
        if (owner.device_.validate_recipe) { const Result valid = owner.device_.validate_recipe(owner.device_.context, recipe); if (!valid.ok()) return valid; }
        for (const RecipeEntry& entry : staged) if (!std::strcmp(entry.id, recipe.id)) found = true;
        for (std::size_t j = 0; j < n; ++j) if (!std::strcmp(recipe.id, capabilities.starter_recipes[j].id)) return Result(503, "invalid_defaults", "Duplicate starter recipe ID");
        if (found) ++preserved; else ++missing;
    }
    if (missing > empty) return Result(507, "recipe_capacity", "Insufficient free slots for the starter catalogue");
    for (std::size_t n = 0; n < capabilities.starter_recipe_count; ++n) {
        const Recipe& recipe = capabilities.starter_recipes[n]; bool found = false;
        for (const RecipeEntry& entry : staged) if (!std::strcmp(entry.id, recipe.id)) found = true;
        if (found) continue;
        for (unsigned i = 0; i < RecipeCapacity; ++i) if (!staged[i].id[0]) {
            std::uint32_t generation = 0;
            if (!encode_recipe(records, i, recipe, staged[i].generation, generation)) return storage_error();
            copy(staged[i].id, 21, recipe.id); copy(staged[i].name, 32, recipe.name);
            staged[i].steps = recipe.step_count; staged[i].generation = generation; ++added; break;
        }
    }
    if (added && !save_catalogue(staged)) return storage_error();
    if (added) owner.event("recipe", "defaults restored");
    return Result();
}
}}
