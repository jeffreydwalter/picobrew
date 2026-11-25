#pragma once
#include "picobrew/standalone_engine.hpp"
#include "zseries/core_application.hpp"
#include "zseries/head_application.hpp"
#include "zseries/brew_recovery.hpp"

namespace zseries { namespace standalone {
namespace api = picobrew::standalone;
struct Ports {
    void* context = nullptr;
    std::uint32_t (*millis)(void*) = nullptr;
    void (*http_poll)(void*, std::uint32_t) = nullptr;
    void (*serial_poll)(void*) = nullptr;
    bool (*lan_ready)(void*, std::uint32_t network) = nullptr;
    std::uint32_t (*wired_address)(void*) = nullptr;
    void (*apply_interface)(void*) = nullptr;
    bool (*save_network)(void*, const char* ssid, const char* password) = nullptr;
    bool (*reconnect_network)(void*) = nullptr;
    bool (*save_configuration)(void*) = nullptr;
    std::uint32_t (*pair_prompt)(void*, std::uint32_t, char code[9]) = nullptr;
    bool (*pair_decide)(void*, std::uint32_t, std::uint32_t, bool) = nullptr;
    api::Storage storage;
};

// Owns custom policy and input copies; executes through the recovered controller.
// Construction has no I/O. Bind before Core setup, then begin after setup/store.
class Adapter {
public:
    Adapter(oem::CoreApplication&, oem::HeadApplication&) noexcept;
    api::DeviceServices services() noexcept;
    bool bind(Ports, api::Engine&);
    bool begin();
    void poll();
    api::Result execute(const api::Command&);
    api::Result snapshot(api::Snapshot&);
    api::Result validate_recipe(const api::Recipe&);
    api::Result load_recipe(const api::Recipe&, bool start);
    bool idle() const noexcept;
    void wireless_capable(bool enabled) noexcept { wireless_ = enabled; }
    bool stopped() const noexcept { return inhibited_ != 0; }
    bool stop(const char* reason);
    bool show_home();
    bool show_recipes();
    bool tick_wifi(std::uint32_t now);
    bool service_stage(oem::CoreTickStage, std::uint32_t);
    bool service_effect(const oem::CoreAction&, std::uint32_t&);
private:
    bool wireless_ = false;
    enum class Page { Home, Recipes, Original, Scanning, Networks, Pairing };
    oem::CoreApplication& core_;
    oem::HeadApplication& head_;
    Ports ports_;
    api::Engine* engine_ = nullptr;
    oem::CoreFlowServices original_core_;
    oem::HeadFlowServices original_head_;
    oem::HeadCoreServices original_head_core_;
    oem::HeadUiServices original_ui_;
    oem::HeaterIo original_gpio_{};
    z_msp_transport original_msp_{};
    oem::I2cTransport original_sensor_{};
    bool sensor_failed_ = false;
    volatile std::uint32_t inhibited_ = 1;
    const char* stop_reason_ = "initializing";
    Page page_ = Page::Original;
    bool bound_ = false, begun_ = false, queued_ = false, encoder_click_ = false;
    bool care_recorded_ = false;
    void observe_care();
    bool native_prepared_ = false, owned_recipe_ = false;
    BrewRecovery recovery_;
    bool recovery_active_ = false, resuming_ = false;
    std::uint32_t recovery_date_ = 0, recovery_instruction_ = 0, elapsed_before_resume_ = 0;
    api::Command queued_command_;
    api::Recipe queued_recipe_, selected_recipe_;
    std::array<std::array<char,21>,api::RecipeCapacity> recipe_ids_{};
    std::array<std::uint8_t,oem::DisplayBufferBytes> frozen_display_{};
    std::uint32_t display_revision_ = 0, session_token_ = 0;
    bool display_valid_ = false;
    api::Result read_settings(api::Settings&);
    api::Result validate_settings(const api::Settings&, std::uint32_t);
    api::Result apply_settings(const api::Settings&, std::uint32_t);
    api::Result display(api::Display&);
    api::Result read_display(std::uint32_t, std::size_t, std::uint8_t*, std::size_t);
    bool handle_head(oem::HeadEventKind);
    bool handle_ui(const oem::HeadUiAction&);
    bool start_effect(const oem::CoreAction&, std::uint32_t&, std::uint32_t program_id);
    bool offline_policy(std::uint32_t);
    bool queue_program(std::uint32_t);
    bool wake_standby();
    bool clear_stop();
    bool show_settings();
    bool show_about();
    bool show_pairing();
    std::uint32_t pairing_ticket_ = 0;
    bool show_network();
    bool show_ethernet();
    bool select_interface(bool wired);
    bool start_wifi_scan();
    bool show_wifi_results(std::int32_t count);
    bool select_wifi(const oem::HeadUiEvent&);
    bool network_message(const char* title);
    std::array<oem::HeadWifiNetwork,31> scanned_{};
    std::size_t scanned_count_ = 0;
    std::uint32_t scan_started_ = 0;
    bool show_recovery(bool discard);
    api::Result resume_interrupted(bool confirmed);
    api::Result discard_interrupted();
    void poll_recovery();
    void sensor_failure();
    const char* text(const char*, const char*, const char*) const noexcept;
    static void ZSERIES_HEATER_IRAM guarded_write(void*, std::uint8_t, bool);
    static void guarded_transmit(void*, std::uint8_t, const std::uint8_t*, std::size_t);
};
}} // namespace zseries::standalone
