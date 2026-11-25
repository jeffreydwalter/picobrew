// Portable standalone appliance contract. No SDK, filesystem or heap ownership.
#pragma once
#include <cstddef>
#include <cstdint>

namespace picobrew { namespace standalone {

constexpr std::size_t RecipeCapacity = 16;
constexpr std::size_t RecipeStepCapacity = 15;
constexpr std::size_t SensorCapacity = 8;
constexpr std::size_t ActuatorCapacity = 4;
constexpr std::size_t DisplayPageBytes = 1024;

enum Capability : std::uint32_t {
    Recipes = 1u << 0, Brewing = 1u << 1, Heating = 1u << 2,
    Pump = 1u << 3, Fans = 1u << 4, Temperature = 1u << 5,
    Pressure = 1u << 6, Battery = 1u << 7, Calibration = 1u << 8,
    Fermentation = 1u << 9, FrontPanel = 1u << 10, IntermittentWifi = 1u << 11
};
enum SettingField : std::uint32_t {
    TemperatureUnit = 1u << 0, Language = 1u << 1, DetailedStatus = 1u << 2, AutoCycle = 1u << 3
};
enum CommandField : std::uint32_t {
    RecipeId = 1u << 0, Delta = 1u << 1, Click = 1u << 2,
    Channel = 1u << 3, Level = 1u << 4, Enabled = 1u << 5,
    Value = 1u << 6, DurationSeconds = 1u << 7, Confirm = 1u << 8
};
struct CommandDescriptor {
    const char* name;
    std::uint32_t allowed_fields;
    std::uint32_t required_fields;
};
struct Identity {
    const char* model = nullptr;
    const char* model_name = nullptr;
    const char* model_family = nullptr;
    const char* firmware_version = nullptr;
    const char* device_uid = nullptr;
};
struct Capabilities {
    std::uint32_t flags = 0;
    std::uint32_t settings = 0;
    const CommandDescriptor* commands = nullptr;
    std::size_t command_count = 0;
    bool zero_minute_steps = false;
    bool dedicated_pause_resume = false;
    bool z_program_maker = false;
    bool z_state_v1 = false;
    bool group_brewing_v1 = false;
    bool picostill_brewer_link_v1 = false;
    const char* ota_protocol = nullptr;
    // Advertise only when the adapter supplies attributed, validated defaults.
    const struct Recipe* starter_recipes = nullptr;
    std::size_t starter_recipe_count = 0;
};
struct Reading {
    double value = 0;
    bool valid = false;
};
enum class SensorKind : std::uint8_t { Temperature, Pressure, Voltage, Battery, Speed, Other };
enum class Unit : std::uint8_t { Fahrenheit, Kilopascal, Volt, Percent, Rpm, Raw };
struct Sensor {
    const char* id = nullptr;
    const char* label = nullptr;
    SensorKind kind = SensorKind::Other;
    Unit unit = Unit::Raw;
    Reading reading;
};
enum class ActuatorKind : std::uint8_t { Pump, Fan, Heater };
struct Actuator {
    const char* id = nullptr;
    const char* label = nullptr;
    ActuatorKind kind = ActuatorKind::Pump;
    bool valid = false;
    bool enabled = false;
    // Structural support is independent of a temporarily invalid reading.
    // History omits only unsupported columns, keeping each session's schema
    // fixed through sensor/controller faults.
    bool supports_level = false;
    bool supports_rpm = false;
    // Normalized percent; invalid/unmeasured speed remains null.
    Reading level;
    Reading rpm;
};
struct Recovery {
    bool supported = false, available = false, valid = true;
    char recipe_name[32] = {};
    char step_name[32] = {};
    std::int32_t current_step = -1, step_seconds = 0;
    std::uint32_t elapsed_seconds = 0;
};
struct Snapshot {
    std::uint32_t uptime_ms = 0;
    bool ready = false, busy = false, stopped = false;
    const char* stop_reason = "none";
    const char* command = "";
    const char* state = "initializing";
    std::uint32_t device_state = 0, phase = 0, controller_status = 0;
    std::uint32_t compartment = 0;
    Reading wort_f, thermoblock_f, target_f;
    Reading temperature_f, pressure_kpa, battery_v;
    std::int32_t step_seconds = 0, current_step = -1;
    std::uint32_t step_count = 0;
    char recipe_name[32] = {};
    char step_name[32] = {};
    bool filter_known = false, filter_inserted = false;
    bool can_pause = false, can_resume = false;
    std::int32_t encoder_position = 0, encoder_min = 0, encoder_max = 0;
    Sensor sensors[SensorCapacity];
    std::size_t sensor_count = 0;
    Actuator actuators[ActuatorCapacity];
    std::size_t actuator_count = 0;
    // Set for real brew/accessory sessions, not menus or network maintenance.
    // A changed nonzero token starts a distinct session even without an idle tick.
    bool recording = false;
    std::uint32_t session_token = 0;
    // Optional verified interrupted-session checkpoint. Loading a checkpoint
    // never starts outputs; resumption requires its advertised explicit command.
    Recovery recovery;
};
// Only the Z Program Maker format carries exact Celsius, seconds and explicit
// pause steps. Keep its values separate from the legacy integer-F/minute API.
struct ZProgramStep {
    double target_c = 0;
    std::uint32_t seconds = 0;
    std::uint16_t drain_seconds = 0;
    bool pause = false, null_target = false, has_time = false;
};
struct RecipeStep {
    char name[22] = {};
    std::uint8_t location = 0, target_f = 0;
    std::uint16_t minutes = 0, drain_minutes = 0;
    ZProgramStep z;
};
struct Recipe {
    char id[21] = {};
    char name[32] = {};
    RecipeStep steps[RecipeStepCapacity];
    std::uint8_t step_count = 0;
    bool z_program = false, is_beer = true, use_pico_pak = false;
    bool has_metric = false, is_metric = false;
};
struct Settings {
    char temperature_unit = 'F';
    char language[3] = {'e', 'n', 0};
    bool detailed_status = false;
    bool auto_cycle = true;
};
struct Command {
    char name[32] = {};
    std::uint32_t fields = 0;
    char recipe_id[21] = {};
    std::int32_t delta = 0, channel = 0;
    bool click = false, enabled = false, confirm = false;
    double level = 0, value = 0;
    std::uint32_t duration_seconds = 0;
    // Borrowed only during execute(); never keep this pointer or request bytes.
    const Recipe* recipe = nullptr;
};
struct Result {
    unsigned status;
    const char* error;
    const char* message;
    Result(unsigned code = 200, const char* name = nullptr, const char* detail = nullptr) noexcept
        : status(code), error(name), message(detail) {}
    bool ok() const noexcept { return status >= 200 && status < 300; }
};
enum class DisplayFormat : std::uint8_t { PageLsb, RowGray4Msb };
struct Display {
    std::uint16_t width = 0, height = 0;
    DisplayFormat format = DisplayFormat::PageLsb;
    std::uint16_t rotation = 0;
    std::size_t total_bytes = 0;
    // Changes only when pixel content changes. All pages must match revision.
    std::uint32_t revision = 0;
};

// All callbacks run in the foreground. Copy shared ISR data under the device's
// short lock, then release it before formatting, storage or network operations.
// String pointers in identity/capabilities/snapshot must survive the request.
// Initialization/read paths must never energize an output.
struct DeviceServices {
    void* context = nullptr;
    Identity (*identity)(void*) = nullptr;
    Capabilities (*capabilities)(void*) = nullptr;
    Result (*snapshot)(void*, Snapshot&) = nullptr;
    // A 202 acknowledges a copied/queued command, not execution or completion.
    // Stop must latch output inhibition before returning acceptance.
    Result (*execute)(void*, const Command&) = nullptr;
    bool (*idle)(void*) = nullptr;
    Result (*read_settings)(void*, Settings&) = nullptr;
    Result (*validate_settings)(void*, const Settings&, std::uint32_t changed) = nullptr;
    Result (*apply_settings)(void*, const Settings&, std::uint32_t changed) = nullptr;
    // Common validation applies first; the adapter verifies native routing,
    // program capacity and actual supported recipe semantics without running it.
    Result (*validate_recipe)(void*, const Recipe&) = nullptr;
    Result (*display)(void*, Display&) = nullptr;
    Result (*read_display)(void*, std::uint32_t revision, std::size_t offset,
                           std::uint8_t* destination, std::size_t length) = nullptr;
    // Optional device cadence, captured and validated at session start. Missing
    // means 15000 ms. State transitions may still add samples between ticks.
    std::uint32_t (*history_interval_ms)(void*) = nullptr;
};

enum class StorageResult : std::uint8_t { Ok, Missing, Error };
struct Storage {
    void* context = nullptr;
    // Names are engine-owned fixed names, never user IDs or untrusted paths.
    // read distinguishes absent files from corruption/I/O failure; total_bytes
    // reports complete file size and bytes_read reports actual copied bytes.
    StorageResult (*read)(void*, const char* name, std::size_t offset,
                          std::uint8_t* destination, std::size_t capacity,
                          std::size_t& bytes_read, std::size_t& total_bytes) = nullptr;
    // Must close and verify exact bytes before committing a replacement. The
    // engine also uses versioned generations and checksums for recovery.
    StorageResult (*write_atomic)(void*, const char* name,
                                  const std::uint8_t* data, std::size_t length) = nullptr;
    StorageResult (*remove)(void*, const char* name) = nullptr;
};
struct Clock {
    void* context = nullptr;
    std::uint32_t (*millis)(void*) = nullptr;
};

}} // namespace picobrew::standalone
