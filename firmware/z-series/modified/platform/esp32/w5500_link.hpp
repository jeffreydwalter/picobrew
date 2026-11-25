// WIZnet W5500 datasheet v1.1.0, socket 0 MACRAW / variable length SPI.
// All SPI operations run on the same task as the display. No waits for a
// command, link, address, free space or frame completion in the control loop.
#pragma once
#include <cstdint>
#include <cstddef>

namespace zseries { namespace standalone { namespace esp32 {
template<class Bus> class W5500Link {
public:
    static constexpr unsigned MaxFrame = 1518;
    explicit W5500Link(Bus& bus) : bus_(bus) {}
    bool online() const { return linked_; }
    void poll(bool enabled, const std::uint8_t* mac, std::uint32_t now) {
        if(!enabled){if(state_!=Off)retry(now);state_=Off;return;}
        switch(state_){
        case Off: case Retry:
            if(state_==Retry && now-since_<1000)return;
            if(byte(Common,Version)!=4){retry(now);return;}
            put(Common,Mode,0x80);transition(Reset,now);return;
        case Reset:
            if(now-since_<5)return;
            if(byte(Common,Mode)&0x80){if(now-since_>=100)retry(now);return;}
            bus_.write(Common,Mac,mac,6);
            // Each bank has 16 KiB shared by eight sockets. Release the seven
            // unused sockets before assigning the whole bank to socket zero.
            for(unsigned i=1;i<8;i++){put(1+i*4,RxSize,0);put(1+i*4,TxSize,0);}
            put(Socket,RxSize,16);put(Socket,TxSize,16);
            put(Socket,Mode,0x84); // MAC address filtering and MACRAW
            put(Socket,Command,Open);transition(Opening,now);return;
        case Opening:
            if(byte(Socket,Command)||byte(Socket,Status)!=0x42){if(now-since_>=100)retry(now);return;}
            transition(Ready,now);last_phy_=now-250;break;
        case Ready:break;
        }
        if(now-last_phy_>=250){
            last_phy_=now;
            if(byte(Common,Version)!=4||byte(Socket,Status)!=0x42){retry(now);return;}
            const bool up=(byte(Common,Phy)&1)!=0;
            if(linked_&&!up){retry(now);return;}
            linked_=up;
        }
        if(sending_){
            const auto irq=byte(Socket,Interrupt);
            if(irq&0x10){put(Socket,Interrupt,0x10);sending_=false;}
            else if((irq&0x08)||now-sent_at_>=250){retry(now);return;}
        }
        if(await_command_){
            if(!byte(Socket,Command))await_command_=false;
            else if(now-command_at_>=100)retry(now);
        }
    }
    unsigned receive(std::uint8_t* frame, std::uint32_t now){
        if(!linked_||await_command_||byte(Socket,Command))return 0;
        const auto available=stable(RxReceived);
        if(!available)return 0;
        if(available<2){incomplete(now);return 0;}
        const auto cursor=word(Socket,RxRead);
        const unsigned length=word(RxBuffer,cursor); // includes the two length bytes
        if(length<2||length>16384){retry(now);return 0;}
        if(length>available){incomplete(now);return 0;}
        rx_partial_=false;
        // A whole runt/jumbo frame is skippable even if lwIP cannot use it.
        const bool usable=length>=16&&length<=MaxFrame+2;
        if(usable)bus_.read(RxBuffer,static_cast<std::uint16_t>(cursor+2),frame,length-2);
        set_word(RxRead,cursor+length);command(Recv,now);
        return usable?length-2:0;
    }
    bool send(const std::uint8_t* frame,unsigned size,std::uint32_t now){
        if(!linked_||sending_||await_command_||size<14||size>MaxFrame||
           byte(Socket,Command)||stable(TxFree)<size)return false;
        const auto cursor=word(Socket,TxWrite);
        bus_.write(TxBuffer,cursor,frame,size);set_word(TxWrite,cursor+size);
        put(Socket,Interrupt,0x18);command(Send,now);sending_=true;sent_at_=now;return true;
    }
private:
    enum State{Off,Retry,Reset,Opening,Ready}state_=Off;
    enum {Common=0,Socket=1,TxBuffer=2,RxBuffer=3,Mode=0,Mac=9,Phy=0x2e,Version=0x39,
          Command=1,Interrupt=2,Status=3,RxSize=0x1e,TxSize=0x1f,TxFree=0x20,TxWrite=0x24,
          RxReceived=0x26,RxRead=0x28,Open=1,Send=0x20,Recv=0x40};
    Bus& bus_;
    bool linked_=false,sending_=false,await_command_=false,rx_partial_=false;
    std::uint32_t since_=0,last_phy_=0,sent_at_=0,command_at_=0,partial_at_=0;
    void transition(State state,std::uint32_t now){state_=state;since_=now;}
    void retry(std::uint32_t now){linked_=sending_=await_command_=rx_partial_=false;transition(Retry,now);}
    void incomplete(std::uint32_t now){
        if(!rx_partial_){rx_partial_=true;partial_at_=now;}
        else if(now-partial_at_>=100)retry(now);
    }
    std::uint8_t byte(unsigned block,unsigned reg){std::uint8_t out=0;bus_.read(block,reg,&out,1);return out;}
    std::uint16_t word(unsigned block,unsigned reg){std::uint8_t bytes[2];bus_.read(block,reg,bytes,2);return (bytes[0]<<8)|bytes[1];}
    void put(unsigned block,unsigned reg,std::uint8_t value){bus_.write(block,reg,&value,1);}
    void set_word(unsigned reg,std::uint16_t value){std::uint8_t bytes[]={static_cast<std::uint8_t>(value>>8),static_cast<std::uint8_t>(value)};bus_.write(Socket,reg,bytes,2);}
    unsigned stable(unsigned reg){const auto a=word(Socket,reg);return a==word(Socket,reg)?a:0;}
    void command(std::uint8_t code,std::uint32_t now){put(Socket,Command,code);command_at_=now;await_command_=true;}
};
}}}
