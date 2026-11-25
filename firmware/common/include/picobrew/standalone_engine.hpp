// Shared standalone API engine. Platform I/O is supplied through narrow ports.
#pragma once
#include "picobrew/device_services.hpp"
#include "picobrew/http_server.h"
namespace picobrew { namespace standalone {
// Every operation is nonblocking. begin starts asynchronous DNS/connect and
// poll_connect returns 1 when connected, 0 while pending, -1 on failure.
// send/receive return positive bytes, PicoHttpIo AGAIN/ERROR, or receive=0 EOF.
// Input memory is borrowed only during the callback. A platform must not retain
// request bytes; the engine owns the complete outgoing frame until close().
struct TelemetryIo {
    void* context=nullptr;
    bool (*begin)(void*,const char* host,std::uint16_t port)=nullptr;
    int (*poll_connect)(void*)=nullptr;
    int (*send)(void*,const std::uint8_t*,std::size_t)=nullptr;
    int (*receive)(void*,std::uint8_t*,std::size_t)=nullptr;
    void (*close)(void*)=nullptr;
};
enum class CareEvent { BrewUsed, RinseNeeded, RinseFinished, CleanFinished, StillUsed, CondenserCleaned, FermUsed, SealSanitized };
enum CareTask { RinseDue = 1, CleanDue = 2, CondenserDue = 4, SealDue = 8 };
class Engine {
public:
    Engine(DeviceServices device,Storage storage,Clock clock) noexcept;
    ~Engine();
    Engine(const Engine&)=delete;Engine& operator=(const Engine&)=delete;
    // Binds callbacks without I/O in construction; begin loads verified state.
    // A failed persistent store keeps health/status/stop available, while
    // mutation/session creation fails explicitly and preserves existing files.
    Result begin();
    // The recorder continues to run when a battery device switches off its radio.
    void poll(bool network = true);
    bool forwarding_enabled() const;
    bool forwarding_active() const;
    void schedule_forwarding_now();
    Result record_care(CareEvent event);
    // The platform owns the stable string and sets it after qualifying its live boot layout.
    void set_ota_protocol(const char* protocol) noexcept;
    unsigned care_due() const noexcept;
    void set_telemetry_io(TelemetryIo io) noexcept;
    void event(const char* kind,const char* detail);
    bool handle(const PicoHttpRequest& request,PicoHttpResponse& response,char* arena,std::size_t capacity);
    static bool http_handler(const PicoHttpRequest*,PicoHttpResponse*,char*,std::size_t,void*);
    std::size_t recipe_count() const noexcept;
    Result recipe_at(std::size_t ordinal,Recipe& output) const;
    Result recipe_get(const char* id,Recipe& output) const;
    Result recipe_store(const Recipe& recipe,bool* created=nullptr);
    Result recipe_remove(const char* id);
    // Native front-panel preference changes use the same verified store. The
    // adapter validates its idle/menu context; this never bypasses the device's
    // validate_settings/apply_settings callbacks or capability field mask.
    Result preferences_store(const Settings& settings,std::uint32_t changed);
    // Call immediately before native program start, before any output can be
    // energized. A stable, nonzero token must match subsequent recording
    // snapshots. This reserves one persistent identity/schema; poll adopts it
    // instead of allocating a second session. Cancel if native start fails.
    Result prepare_session(const char* command,const char* recipe_name,std::uint32_t token);
    void cancel_prepared_session();
    bool persistence_ready() const noexcept;
private:
    struct Impl;
    DeviceServices device_;Storage storage_;Clock clock_;TelemetryIo telemetry_;
    Impl* impl_=nullptr;
};
}}
