#pragma once
#include "picobrew/standalone_engine.hpp"
#include "records.hpp"
#include <new>

namespace picobrew { namespace standalone { namespace detail {
constexpr unsigned SessionCapacity = 8;
constexpr unsigned PointCapacity = 4224;
constexpr unsigned ChunkPoints = 24;
constexpr unsigned ChunkCapacity = PointCapacity / ChunkPoints;
constexpr unsigned FieldCapacity = 14 + SensorCapacity + ActuatorCapacity * 3;
constexpr unsigned SamplePeriod = 15000;
constexpr unsigned HistoryPage = 32;

struct RecipeEntry {
    char id[21] = {};
    char name[32] = {};
    std::uint8_t steps = 0;
    std::uint32_t generation = 0;
};
struct Event {
    std::uint32_t sequence = 0, time = 0;
    char kind[20] = {}, detail[64] = {};
};
struct TelemetryConfig {
    bool enabled = false;
    std::uint16_t port = 8080;
    char host[128] = {};
};
enum class SessionState : std::uint8_t { Empty, Recording, Completed, Interrupted };
struct Session {
    std::uint32_t id = 0, duration = 0, gaps = 0, metadata_generation = 0;
    std::uint32_t interval_ms = SamplePeriod;
    std::uint16_t first = 0, count = 0;
    std::uint8_t fields = 0;
    SessionState state = SessionState::Empty;
    bool truncated = false, storage_error = false, recovered = false;
    char command[32] = {}, recipe[32] = {};
};
struct Field {
    char name[40] = {}, label[32] = {};
    Unit unit = Unit::Raw;
    SensorKind kind = SensorKind::Other;
};
struct Layout {
    unsigned count = 0;
    Field* fields = nullptr;
    ~Layout() { delete[] fields; }
    Layout() = default;
    Layout(const Layout&) = delete;
    Layout& operator=(const Layout&) = delete;
    bool allocate(unsigned size) {
        if (size < 14 || size > FieldCapacity) return false;
        if (size == count && fields) return true;
        // A retained schema is replaceable scratch state. Release its previous
        // allocation before resizing, instead of temporarily owning two arrays.
        delete[] fields; fields = nullptr; count = 0;
        fields = new (std::nothrow) Field[size];
        if (!fields) return false;
        count = size; return true;
    }
};
struct Point {
    std::uint32_t id = 0;
    unsigned count = 0;
    std::uint64_t valid = 0;
    double values[FieldCapacity] = {};
};
const char* unit_name(Unit unit);
const char* kind_name(SensorKind kind);
const char* actuator_name(ActuatorKind kind);
bool valid_id(const char* value, std::size_t capacity = 21);
bool valid_text(const char* value, std::size_t capacity);
bool recipe_valid(const Recipe& recipe, bool zero_minutes, bool z_program_maker, bool z_cloud, bool z_pak);
bool settings_valid(const Settings& settings);
bool telemetry_valid(const TelemetryConfig& config);
bool parse_recipe(Json& json, const char* id, Recipe& recipe, bool& create_only, std::uint32_t& revision);
bool parse_command(Json& json, Command& command);
unsigned error(Writer& writer, Result result);
void reading(Writer& writer, Reading value);
void recipe_json(Writer& writer, const Recipe& recipe, std::uint32_t revision);
void settings_json(Writer& writer, const Settings& settings, std::uint32_t fields);
bool layout_create(const Snapshot& snapshot, Layout& layout);
bool layout_save(const Records&, const char* name, const Layout&, std::uint32_t previous,
                 std::uint32_t& generation);
bool layout_load(const Records&, const char* name, Layout&, std::uint32_t generation, unsigned fields);
void layout_fields(Writer& writer, const Layout& layout);
void layout_metadata(Writer& writer, const Layout& layout);
void point_create(const Snapshot&, const Layout&, std::uint32_t id, std::uint32_t elapsed, Point&);
bool point_save(const Records&, unsigned slot, const Point&);
bool point_load(const Records&, unsigned slot, std::uint32_t id, unsigned fields, Point&);
void point_json(Writer&, const Point&);
}}

struct standalone::Engine::Impl {
    explicit Impl(Engine& engine) : owner(engine), records(engine.storage_) {}
    ~Impl();
    Engine& owner;
    detail::Records records;
    Identity identity;
    Capabilities capabilities;
    bool has_recipe_library() const {
#ifdef PICOBREW_NO_RECIPE_LIBRARY
        // The PicoFerm only records fermentation. Its ESP8266 shares a small
        // DRAM pool between constant strings, TCP, SPIFFS and the runtime heap.
        return false;
#else
        return capabilities.flags & Recipes;
#endif
    }
    Snapshot snapshot;
    Settings settings;
    detail::TelemetryConfig telemetry;
    // Accessories have no recipe capability and need no catalogue allocation.
    detail::RecipeEntry* recipes = nullptr;
    detail::Session sessions[detail::SessionCapacity];
    detail::Event events[8];
    unsigned event_head = 0, event_count = 0;
    std::uint32_t event_sequence = 1;
    std::uint32_t config_generation = 0, catalogue_generation = 0;
    std::uint32_t sequence_generation = 0, history_generation = 0, sequence = 0;
    std::uint32_t started_at = 0, sampled_at = 0, session_token = 0, state_signature = 0;
    std::uint32_t care_generation = 0;
    std::uint8_t care_flags = 0, brews_since_clean = 0;
    bool care_available = false;
    unsigned next_point = 0;
    int active = -1;
    bool initialized = false, persistent = false, snapshot_valid = false, recording_blocked = false, prepared = false, history_loaded = false;
    // Only the currently recording layout occupies RAM. Retained layouts are
    // loaded on demand into a checked allocation, never a catalogue-sized heap.
    detail::Layout* active_layout = nullptr;
    enum class TxState { Disabled, Offline, Ready, Connecting, Sending, Receiving, Backoff };
    TxState tx_state = TxState::Disabled;
    std::uint8_t* tx = nullptr;
    std::size_t tx_length = 0, tx_offset = 0;
    char tx_response[13] = {};
    unsigned tx_received = 0, tx_http = 0;
    std::uint32_t tx_scheduled = 0, tx_started = 0, tx_backoff = 0;
    std::uint32_t tx_sent = 0, tx_failed = 0, tx_dropped = 0, tx_success = 0;
    bool tx_open = false, tx_resolved = false, tx_storage_error = false;
    const char* tx_error = "none";

    std::uint32_t now() const;
    std::uint32_t history_interval() const;
    std::uint32_t active_elapsed() const;
    bool idle() const;
    Result initialize();
    Result refresh();
    Result save_config(const Settings&, const detail::TelemetryConfig&);
    bool load_recipes();
    bool save_catalogue(const detail::RecipeEntry* entries);
    Result recipe_read(unsigned slot, Recipe& output) const;
    Result recipe_write(const Recipe&, bool* created, bool create_only = false, std::uint32_t revision = 0);
    Result recipe_delete(const char* id, std::uint32_t revision = 0);
    Result restore_defaults(unsigned& added, unsigned& preserved);
    bool load_history();
    bool save_history(const detail::Session* entries, unsigned next);
    void history_poll();
    bool history_start(bool reserve_only = false);
    bool history_sample();
    void history_finish(detail::SessionState state);
    void history_failure(const char* detail);
    int newest_session() const;
    unsigned status_json(detail::Writer& writer, bool update);
    unsigned discovery_json(detail::Writer& writer);
    unsigned settings_api(const PicoHttpRequest&, detail::Writer&);
    unsigned recipes_api(const PicoHttpRequest&, detail::Writer&);
    unsigned command_api(const PicoHttpRequest&, detail::Writer&);
    unsigned display_api(const PicoHttpRequest&, detail::Writer&);
    unsigned history_api(const PicoHttpRequest&, detail::Writer&);
    unsigned telemetry_api(const PicoHttpRequest&, detail::Writer&);
    unsigned dispatch(const PicoHttpRequest&, detail::Writer&);
    void tx_close();
    void tx_fail(const char* reason);
    bool tx_frame();
    void tx_poll();
};
} // namespace picobrew
