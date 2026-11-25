// Complete PicoStill standalone 1.0.0 owner. The recovered cloud/update/main
// application is not instantiated. The modified task preserves local control
// independently of Wi-Fi and the optional Hub.
#include "picostill/standalone.hpp"
#include "arduino_ports.hpp"
#include "factory_flash.hpp"
#include "picobrew/common_web.h"
#include "picobrew/provisioning.hpp"
#include "network_ports.hpp"
#include "storage_ports.hpp"
#include "startup.hpp"
#include "station.hpp"
#include <WiFi.h>
#include <esp_system.h>
#include <nvs.h>
#include <freertos/FreeRTOS.h>
#include <freertos/task.h>
#include <cstdio>
#include <cstring>

namespace {
namespace ota = picobrew::esp32_ota;
namespace api = picobrew::standalone;
namespace sdk = picobrew::standalone::esp32;
class Application {
public:
    Application() : device_(board_, board_, board_), storage_(files_.io()),
        provisioning_(storage_.binding(), &device_, [](void* p) { return static_cast<picostill::standalone::Device*>(p)->idle(); }),
        engine_(device_.services(), storage_.binding(), clock()), socket_io_(listener_.io()) { device_.bind_engine(engine_); }
    void begin() {
        Serial.begin(115200, SERIAL_8N1, -1, -1, false, 20000);
        Serial.println("PicoStill standalone 1.0.0");
        // NVS failure cannot reach controller initialization. Establish the
        // known output lows even when the rest of startup is unavailable.
        board_.pin_mode(18, 2); board_.digital_write(18, false);
        board_.pin_mode(19, 2); board_.digital_write(19, false);
        const bool startup = sdk::nvs_initialization_status() == 0;
        flash_.inspect();
        const auto role = flash_.role();
        recovery_ = role == ota::Role::Recovery || role == ota::Role::InvalidRecovery;
        const bool hardware = !recovery_ && startup && board_.initialize_hardware(device_.hardware.fans, device_.hardware.pump, device_.hardware.sensors);
        Wire.setTimeOut(50); // Pinned SDK's finite transaction timeout.
        std::uint8_t mac[6] = {};
        const bool identity = esp_efuse_mac_get_default(mac) == ESP_OK;
        if (identity) ::snprintf(uid_, sizeof(uid_), "%02x%02x%02x%02x%02x%02x", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);
        if (!recovery_) {device_.link_nonce(esp_random());device_.begin(hardware && identity, uid_);}
        const bool mounted = files_.mount(); std::size_t total = 0, used = 0;
        // The selected clean-install partition is 2 MiB. The independently
        // exercised historical 0x16f000 sizing profile also retains 4,224
        // Still records. Smaller filesystems cannot claim this capacity.
        const bool capacity = mounted && files_.capacity(total, used) && total >= 1300000u;
        if (!startup) Serial.println("ERROR: NVS initialization failed; controller inhibited; no erase attempted");
        if (!recovery_ && (!hardware || !identity)) Serial.println("ERROR: PicoStill hardware initialization is unavailable");
        if (!capacity) Serial.println("ERROR: verified history filesystem is unavailable or too small; no formatting attempted");
        report(provisioning_.load());
        if (!recovery_) {
            if (startup) import_original_network();
            device_.wireless_capable(role == ota::Role::Application && capacity && identity && startup);
            engine_.set_telemetry_io(telemetry_.services());
            const api::Result initialized = engine_.begin(); report(initialized);
            device_.storage_ready(capacity && initialized.ok());
        }
        pico_http_server_initialize(&http_, &socket_io_, &listener_, route, this,
                                    recovery_ ? nullptr : pico_common_web_assets, recovery_ ? 0 : pico_common_web_assets_count);
        if (!recovery_) pico_http_server_enable_pairing(&http_, false);
        pico_http_server_set_asset_io(&http_, &pico_common_web_asset_io, nullptr);
        apply_key();
        if (startup) { WiFi.persistent(false); apply_network(); }
        provisioning_.take_key_changed(); provisioning_.take_network_changed();
        Serial.println("UART: status | key <64 lowercase hex> | wifi <SSID><TAB><password>");
    }
    void poll() {
        if (!recovery_ && !update_.reboot_pending()) device_.poll();
        for (unsigned n = 0; n < 32 && !recovery_ && !update_.reboot_pending() && Serial.available() > 0; ++n) {
            const int byte = Serial.read(); if (byte < 0) break;
            api::Result result; if (provisioning_.feed(static_cast<char>(byte), result)) report(result);
        }
        if (provisioning_.take_key_changed()) { pico_http_server_disconnect(&http_); apply_key(); }
        if (provisioning_.take_network_changed()) { pico_http_server_disconnect(&http_); apply_network(); }
        const bool connected = WiFi.status() == WL_CONNECTED;
        if (connected && !connected_) {
            char text[64]; const IPAddress address = WiFi.localIP();
            ::snprintf(text, sizeof(text), "Local browser: http://%u.%u.%u.%u", address[0], address[1], address[2], address[3]); Serial.println(text);
        }
        if (!connected && connected_) pico_http_server_disconnect(&http_);
        connected_ = connected;
        if (connected && !listening_) listening_ = listener_.begin(80);
        pico_http_server_poll(&http_, ::millis());
        if (update_.reboot_pending()) {
            if (http_.client < 0 || static_cast<std::uint32_t>(::millis() - restart_ms_) >= 2000u) esp_restart();
        } else if (!recovery_) engine_.poll();
        ::yield();
    }
private:
    picostill::oem::ArduinoPorts board_;
    picostill::standalone::Device device_;
    sdk::SpiffsFiles files_;
    api::VerifiedStorage storage_;
    api::Provisioning provisioning_;
    api::Engine engine_;
    sdk::HttpSocket listener_;
    sdk::TelemetrySocket telemetry_;
    PicoHttpIo socket_io_;
    PicoHttpServer http_{};
    ota::FactoryFlash flash_;
    ota::Service update_{flash_};
    bool connected_ = false, listening_ = false, recovery_ = false;
    char uid_[32] = {};
    std::uint32_t restart_ms_ = 0;
    static bool route(const PicoHttpRequest* req, PicoHttpResponse* res, char* arena, std::size_t capacity, void* context) {
        auto& self = *static_cast<Application*>(context);
        bool already_pending = self.update_.reboot_pending();
        if (self.update_.handle(*req, *res, arena, capacity, self.uid_, self.device_.idle())) {
            if (!already_pending && self.update_.reboot_pending()) {
                if (!self.recovery_) self.device_.output.inhibit();
                self.restart_ms_ = ::millis();
            }
            return true;
        }
        if (!self.recovery_ && self.device_.brewer_link(*req,*res,arena,capacity)) return true;
        return self.engine_.handle(*req, *res, arena, capacity);
    }
    static api::Clock clock() { api::Clock c; c.millis = [](void*) { return static_cast<std::uint32_t>(::millis()); }; return c; }
    static void report(const api::Result& result) {
        if (result.message) Serial.println(result.message);
        else if (!result.ok()) Serial.println("ERROR: standalone operation did not complete");
    }
    void apply_key() {
        if (!pico_http_server_set_key(&http_, provisioning_.key(), provisioning_.key_configured() ? 64 : 0))
            Serial.println("ERROR: API key is unavailable; authenticated controls remain disabled");
    }
    void apply_network() {
        if (sdk::nvs_initialization_status() != 0 || !provisioning_.network_configured()) return;
        const auto& credentials = provisioning_.network();
        if (sdk::connect_station(credentials.ssid, credentials.password) != 0) Serial.println("ERROR: Wi-Fi association request failed");
        else WiFi.setHostname("picostill");
    }
    void import_original_network() {
        // The OEM credential object begins with a version word, then fixed
        // SSID[32]/password[64], inside the NVS eeprom/eeprom blob. Read only;
        // never call CredentialStorage::begin(), which probes/rewrites NVS.
        nvs_handle handle = 0;
        if (nvs_open("eeprom", NVS_READONLY, &handle) != ESP_OK) return;
        std::uint8_t bytes[512] = {}; std::size_t size = sizeof(bytes);
        const int result = nvs_get_blob(handle, "eeprom", bytes, &size); nvs_close(handle);
        if (result == ESP_OK && size >= 100 && size <= sizeof(bytes) && !bytes[0] && !bytes[1] && !bytes[2] && !bytes[3]) {
            api::NetworkCredentials imported; std::memcpy(imported.ssid, bytes + 4, 32); std::memcpy(imported.password, bytes + 36, 64);
            if (imported.ssid[0]) { const api::Result saved = provisioning_.import_network(imported); if (saved.status != 409) report(saved); }
            volatile char* clear = reinterpret_cast<volatile char*>(&imported);
            for (std::size_t i = 0; i < sizeof(imported); ++i) clear[i] = 0;
        }
        volatile std::uint8_t* clear = bytes; for (std::size_t i = 0; i < sizeof(bytes); ++i) clear[i] = 0;
    }
};
Application application;
void appliance_task(void*) { application.begin(); for (;;) application.poll(); }
}
extern "C" void app_main() {
    // A checked, preservation-first replacement for the SDK's erase-on-error
    // initArduino. The application task starts status/UART even on failure,
    // but its hardware controller remains inhibited.
    picobrew::standalone::esp32::initialize_arduino();
    if (xTaskCreatePinnedToCore(appliance_task, "picoStillLocal", 8192, nullptr, 1, nullptr, 1) != pdPASS) {
        ::pinMode(18, OUTPUT); ::digitalWrite(18, LOW); ::pinMode(19, OUTPUT); ::digitalWrite(19, LOW);
    }
}
