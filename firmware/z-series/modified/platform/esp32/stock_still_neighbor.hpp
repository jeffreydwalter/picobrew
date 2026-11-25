// A stock PicoStill's PicoStill_<UID> network name contains its factory
// station MAC. The stock HTTP application does not return an ID or issue a key.
#pragma once
#include <freertos/FreeRTOS.h>
#include <freertos/portmacro.h>
#include <lwip/etharp.h>
#include <lwip/ip4.h>
#include <lwip/tcpip.h>
#include <cstring>
#include <cstdint>
#include <cstdio>

namespace zseries { namespace standalone { namespace esp32 {
class StockStillNeighbor {
  public:
    int check(const char* host, const char* uid, std::uint32_t connection) {
        portENTER_CRITICAL(&lock_);
        if (pending_) {
            portEXIT_CRITICAL(&lock_);
            return 0;
        }
        if (ready_ && connection == connection_) {
            const int result = result_;
            portEXIT_CRITICAL(&lock_);
            return result;
        }
        pending_ = true;
        ready_ = false;
        connection_ = connection;
        portEXIT_CRITICAL(&lock_);
        ip4addr_aton(host, &address_); // StillAddress::valid requires a canonical literal.
        std::memcpy(uid_, uid, sizeof uid_);
        if (tcpip_callback_with_block(lookup, this, 0) == ERR_OK)
            return 0;
        portENTER_CRITICAL(&lock_);
        pending_ = false;
        portEXIT_CRITICAL(&lock_);
        return -1;
    }
  private:
    static void lookup(void* context) {
        auto& self = *static_cast<StockStillNeighbor*>(context);
        auto* interface = ip4_route(&self.address_);
        struct eth_addr* peer = nullptr;
        const ip4_addr_t* resolved = nullptr;
        int result = -1;
        if (interface && ip4_addr_netcmp(&self.address_, netif_ip4_addr(interface), netif_ip4_netmask(interface)) &&
            etharp_find_addr(interface, &self.address_, &peer, &resolved) >= 0 && peer && resolved) {
            char uid[13];
            ::snprintf(uid, sizeof uid, "%02x%02x%02x%02x%02x%02x", unsigned(peer->addr[0]),
                       unsigned(peer->addr[1]), unsigned(peer->addr[2]), unsigned(peer->addr[3]),
                       unsigned(peer->addr[4]), unsigned(peer->addr[5]));
            result = std::strcmp(uid, self.uid_) == 0 ? 1 : -1;
        }
        portENTER_CRITICAL(&self.lock_);
        self.result_ = result;
        self.pending_ = false;
        self.ready_ = true;
        portEXIT_CRITICAL(&self.lock_);
    }
    portMUX_TYPE lock_ = portMUX_INITIALIZER_UNLOCKED;
    bool pending_ = false, ready_ = false;
    int result_ = -1;
    std::uint32_t connection_ = 0;
    ip4_addr_t address_{};
    char uid_[13]{};
};
}}}
