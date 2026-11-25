#include "wired_network.hpp"
#include <utility/w5500.h>
#include <lwip/dhcp.h>
#include <lwip/etharp.h>
#include <lwip/tcpip.h>
#include <cstring>

namespace zseries { namespace standalone { namespace esp32 {
namespace {
constexpr unsigned ChipSelect=21;
constexpr unsigned FrameSize=W5500Link<WiredSpi>::MaxFrame;
static_assert(ETH_PAD_SIZE==0,"W5500 frame bridge requires unpadded Ethernet headers");
// The recovered display and W5500 share System::spi. Only the firmware task
// touches the bus; the TCP/IP task enqueues independent packet copies.
}
void WiredSpi::prepare(){
    pinMode(ChipSelect,OUTPUT);digitalWrite(ChipSelect,HIGH);
    // Recovered Head observations also query this chip via its original driver.
    w5500.setSPI(spi_);SPI_CS=ChipSelect;
}
void WiredSpi::start(unsigned block,unsigned offset,bool writing){
    spi_.beginTransaction(SPISettings(4000000,MSBFIRST,SPI_MODE0));
    digitalWrite(ChipSelect,LOW);
    spi_.transfer(static_cast<std::uint8_t>(offset>>8));
    spi_.transfer(static_cast<std::uint8_t>(offset));
    spi_.transfer(static_cast<std::uint8_t>((block<<3)|(writing?4:0)));
}
void WiredSpi::finish(){digitalWrite(ChipSelect,HIGH);spi_.endTransaction();}
void WiredSpi::read(unsigned block,unsigned offset,std::uint8_t* bytes,unsigned size){
    start(block,offset,false);
    // This pinned ESP32 SPI HAL reads/writes whole 32-bit words, including
    // the trailing partial word. Never pass a small register or pbuf directly.
    alignas(4) std::uint8_t chunk[64];
    while(size){const unsigned n=size>sizeof chunk?sizeof chunk:size;
        spi_.transferBytes(nullptr,chunk,n);std::memcpy(bytes,chunk,n);bytes+=n;size-=n;}
    finish();
}
void WiredSpi::write(unsigned block,unsigned offset,const std::uint8_t* bytes,unsigned size){
    start(block,offset,true);
    alignas(4) std::uint8_t chunk[64]={};
    while(size){const unsigned n=size>sizeof chunk?sizeof chunk:size;
        std::memcpy(chunk,bytes,n);spi_.writeBytes(chunk,n);bytes+=n;size-=n;}
    finish();
}

void WiredNetwork::prepare(const std::uint8_t* mac){std::memcpy(mac_,mac,sizeof mac_);bus_.prepare();}
err_t WiredNetwork::initialize(netif* interface){
    auto& self=*static_cast<WiredNetwork*>(interface->state);
    interface->name[0]='w';interface->name[1]='z';
    interface->mtu=1500;interface->hwaddr_len=6;
    std::memcpy(interface->hwaddr,self.mac_,6);
    interface->flags=NETIF_FLAG_BROADCAST|NETIF_FLAG_ETHARP|NETIF_FLAG_ETHERNET;
    interface->output=etharp_output;interface->linkoutput=enqueue;
    return ERR_OK;
}
err_t WiredNetwork::enqueue(netif* interface,pbuf* packet){
    auto& self=*static_cast<WiredNetwork*>(interface->state);
    if(!self.stack_up_||!packet||packet->tot_len<14||packet->tot_len>FrameSize)return ERR_IF;
    pbuf* copy=pbuf_alloc(PBUF_RAW,packet->tot_len,PBUF_RAM);
    if(!copy)return ERR_MEM;
    if(pbuf_copy(copy,packet)!=ERR_OK||xQueueSend(self.tx_,&copy,0)!=pdTRUE){pbuf_free(copy);return ERR_MEM;}
    return ERR_OK;
}
void WiredNetwork::synchronize(void* context){
    auto& self=*static_cast<WiredNetwork*>(context);
    portENTER_CRITICAL(&self.lock_);const bool up=self.wanted_,drop=self.drop_;self.drop_=false;portEXIT_CRITICAL(&self.lock_);
    auto& interface=self.net_;
    if((drop||!up)&&self.stack_up_){
        self.stack_up_=false;dhcp_stop(&interface);netif_set_down(&interface);netif_set_link_down(&interface);
        ip4_addr_t zero{};netif_set_addr(&interface,&zero,&zero,&zero);
        if(netif_default==&interface){
            netif* previous=nullptr;
            for(auto* candidate=netif_list;candidate;candidate=candidate->next)
                if(candidate==self.previous_default_)previous=candidate;
            netif_set_default(previous);
        }
    }
    if(up&&!self.registered_){
        ip4_addr_t zero{};
        self.registered_=netif_add(&interface,&zero,&zero,&zero,&self,initialize,tcpip_input)!=nullptr;
    }
    if(up&&self.registered_){
        if(!self.stack_up_){
            if(netif_default!=&interface)self.previous_default_=netif_default;
            self.stack_up_=true;
            netif_set_link_up(&interface);netif_set_up(&interface);
            if(dhcp_start(&interface)!=ERR_OK){self.stack_up_=false;netif_set_down(&interface);netif_set_link_down(&interface);}
        }
        if(self.stack_up_&&netif_default!=&interface)netif_set_default(&interface);
    }
    const auto ip=self.stack_up_?netif_ip4_addr(&interface)->addr:0u;
    portENTER_CRITICAL(&self.lock_);self.stack_address_=ip;self.stack_live_=self.stack_up_;self.callback_pending_=false;portEXIT_CRITICAL(&self.lock_);
}
void WiredNetwork::discard(){
    if(outgoing_){pbuf_free(outgoing_);outgoing_=nullptr;}
    pbuf* packet;
    for(unsigned i=0;i<4&&tx_&&xQueueReceive(tx_,&packet,0)==pdTRUE;++i)pbuf_free(packet);
}
void WiredNetwork::receive(std::uint32_t now){
    // No per-poll receive allocation when the cable is quiet.
    std::uint8_t frame[FrameSize];
    const auto count=link_.receive(frame,now);
    if(!count)return;
    pbuf* packet=pbuf_alloc(PBUF_RAW,count,PBUF_RAM);
    if(packet&&(pbuf_take(packet,frame,count)!=ERR_OK||tcpip_input(packet,&net_)!=ERR_OK))pbuf_free(packet);
}
void WiredNetwork::transmit(std::uint32_t now){
    if(!outgoing_&&xQueueReceive(tx_,&outgoing_,0)!=pdTRUE)return;
    if(link_.send(static_cast<const std::uint8_t*>(outgoing_->payload),outgoing_->len,now)){pbuf_free(outgoing_);outgoing_=nullptr;}
}
void WiredNetwork::poll(bool enabled,std::uint32_t now){
    if(!enabled&&!tx_)return;
    if(!tx_){
        if(now-last_sync_<1000)return;
        last_sync_=now;tx_=xQueueCreate(4,sizeof(pbuf*));if(!tx_)return;
    }
    link_.poll(enabled,mac_,now);
    const bool online=link_.online();
    bool schedule=false;
    portENTER_CRITICAL(&lock_);
    const bool changed=wanted_!=online;if(changed&&!online)drop_=true;wanted_=online;
    if(!callback_pending_&&(changed||now-last_sync_>=250)){
        callback_pending_=true;schedule=true;last_sync_=now;
    }
    const auto lease=online&&!drop_?stack_address_:0;
    const bool ready=online&&stack_live_&&!drop_&&!callback_pending_;
    portEXIT_CRITICAL(&lock_);
    if(schedule&&tcpip_callback_with_block(synchronize,this,0)!=ERR_OK){
        portENTER_CRITICAL(&lock_);callback_pending_=false;portEXIT_CRITICAL(&lock_);
    }
    published_address_=lease;
    if(!online){discard();return;}
    if(!ready)return;
    // One frame per iteration; alternate direction to bound latency under flood.
    if(offer_tx_)transmit(now);else receive(now);offer_tx_=!offer_tx_;
}
}}}
