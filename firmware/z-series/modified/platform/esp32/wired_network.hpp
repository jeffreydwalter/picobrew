#pragma once
#include "w5500_link.hpp"
#include <SPI.h>
#include <freertos/FreeRTOS.h>
#include <freertos/queue.h>
#include <lwip/netif.h>

namespace zseries { namespace standalone { namespace esp32 {
class WiredSpi {
public:
    explicit WiredSpi(SPIClass& spi):spi_(spi){}
    void prepare();
    void read(unsigned block,unsigned offset,std::uint8_t* bytes,unsigned size);
    void write(unsigned block,unsigned offset,const std::uint8_t* bytes,unsigned size);
private:
    SPIClass& spi_;
    void start(unsigned block,unsigned offset,bool writing);
    void finish();
};
class WiredNetwork {
public:
    explicit WiredNetwork(SPIClass& spi):bus_(spi),link_(bus_){}
    void prepare(const std::uint8_t* mac);
    void poll(bool enabled,std::uint32_t now);
    std::uint32_t address() const {return published_address_;}
private:
    WiredSpi bus_;
    W5500Link<WiredSpi> link_;
    netif net_{};
    std::uint8_t mac_[6]{};
    QueueHandle_t tx_=nullptr;
    pbuf* outgoing_=nullptr;
    bool offer_tx_=true;
    bool registered_=false, stack_up_=false; // tcpip thread only
    netif* previous_default_=nullptr;
    portMUX_TYPE lock_=portMUX_INITIALIZER_UNLOCKED;
    bool wanted_=false,callback_pending_=false,drop_=false,stack_live_=false; // shared under lock
    std::uint32_t stack_address_=0;
    std::uint32_t published_address_=0,last_sync_=0;
    static void synchronize(void* context);
    static err_t initialize(netif*);
    static err_t enqueue(netif*,pbuf*);
    void receive(std::uint32_t now);
    void transmit(std::uint32_t now);
    void discard();
};
}}}
