// The factory binary has no brewer controller, pairing endpoint or recipe engine.
#include <algorithm> // Parse the standard library before the pinned Arduino abs macro.
#include "factory_flash.hpp"
#include "output_cutoff.hpp"
#include "zseries/board_settings.hpp"
#include "wired_network.hpp"
#include "picobrew/provisioning.hpp"
#include "picobrew/verified_storage.hpp"
#include "network_ports.hpp"
#include "storage_ports.hpp"
#include "startup.hpp"
#include "station.hpp"
#include <WiFi.h>
#include <esp_system.h>
#include <cstdio>

extern void tcpipInit();
struct RecoveryIdentity { char identity[64]; char wired[sizeof(PB_Z_WIRED_RECOVERY)]; };
extern "C" const RecoveryIdentity z_recovery_identity __attribute__((section(".rodata_custom_desc"),used)) = {PB_RECOVERY_IDENTITY,PB_Z_WIRED_RECOVERY};
namespace {
namespace sdk = picobrew::standalone::esp32;
namespace api = picobrew::standalone;
namespace ota = picobrew::esp32_ota;

class Recovery {
    ota::FactoryFlash flash_;
    ota::Service update_{flash_};
    sdk::SpiffsFiles files_;
    api::VerifiedStorage storage_{files_.io()};
    api::Provisioning config_{storage_.binding(), nullptr, nullptr};
    zseries::standalone::BoardSettings board_{storage_.binding()};
    SPIClass spi_{2};
    zseries::standalone::esp32::WiredNetwork ethernet_{spi_};
    bool wired_ = false;
    sdk::HttpSocket socket_;
    PicoHttpIo io_{};
    PicoHttpServer http_{};
    char uid_[13]{};
    bool listening_ = false;
    std::uint32_t restart_ms_ = 0;

    static bool route(const PicoHttpRequest* req, PicoHttpResponse* res, char* arena, std::size_t cap, void* ctx) {
        auto& self = *static_cast<Recovery*>(ctx);
        const bool pending = self.update_.reboot_pending();
        const bool handled = self.update_.handle(*req, *res, arena, cap, self.uid_, false);
        if (!pending && self.update_.reboot_pending()) self.restart_ms_ = ::millis();
        return handled;
    }
public:
    bool start() {
        flash_.inspect();
        if (flash_.role() != ota::Role::Recovery || !files_.mount() || !config_.load().ok() ||
            !config_.key_configured()) return false;
        std::uint8_t mac[6] = {};
        if (esp_efuse_mac_get_default(mac) != ESP_OK) return false;
        ::snprintf(uid_, sizeof(uid_), "%02x%02x%02x%02x%02x%02x", mac[0],mac[1],mac[2],mac[3],mac[4],mac[5]);
        io_ = socket_.io();
        pico_http_server_initialize(&http_, &io_, &socket_, route, this, nullptr, 0);
        if (!pico_http_server_set_key(&http_, config_.key(), 64)) return false;
        WiFi.persistent(false);
        wired_ = board_.load() && board_.wired();
        if (wired_) {
            WiFi.mode(WIFI_OFF);
            tcpipInit();
            pinMode(0,OUTPUT);digitalWrite(0,HIGH); // Deselect the OLED on the shared bus.
            spi_.begin(17,5,16,-1);
            mac[5] += 3; // Same Ethernet MAC used by the field application.
            ethernet_.prepare(mac);
            return true;
        }
        if(!config_.network_configured())return false;
        const auto& net = config_.network();
        return sdk::connect_station(net.ssid, net.password) == 0;
    }
    void poll() {
        if(wired_)ethernet_.poll(true,::millis());
        if (!listening_ && (wired_ ? ethernet_.address()!=0 : WiFi.status()==WL_CONNECTED)) listening_ = socket_.begin();
        if (listening_) pico_http_server_poll(&http_, ::millis());
        if (update_.reboot_pending() && (http_.client < 0 || ::millis() - restart_ms_ >= 2000u)) esp_restart();
    }
};
void task(void*) {
    static Recovery recovery;
    if (!recovery.start()) { vTaskDelete(nullptr); return; }
    for (;;) { recovery.poll(); ::delay(1); }
}
}
// The recovery linker selects this entry as app_main. The field linker discards it.
extern "C" void picobrew_z_recovery_main() {
    const int initialized = sdk::initialize_arduino();
    const bool off = zseries::standalone::esp32::force_outputs_off();
    if (initialized != 0 || !off) return;
    xTaskCreatePinnedToCore(task, "picoZRecovery", 12288, nullptr, 1, nullptr, 1);
}
