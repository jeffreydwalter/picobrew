#include "../platform/esp32/w5500_link.hpp"
#include <array>
#include <cassert>
#include <cstring>
#include <vector>

using namespace zseries::standalone::esp32;
struct Chip {
    std::array<std::array<std::uint8_t,64>,32> regs{};
    std::array<std::uint8_t,16384> rx{},tx{};
    unsigned operations=0,commands=0,sends=0;
    bool command_stuck=false,unstable=false;
    std::uint16_t receive_cursor=0;
    Chip(){regs[0][0x39]=4;regs[0][0x2e]=1;word(1,0x20,16384);}
    void word(unsigned b,unsigned r,unsigned v){regs[b][r]=v>>8;regs[b][r+1]=v;}
    unsigned word(unsigned b,unsigned r){return (regs[b][r]<<8)|regs[b][r+1];}
    void read(unsigned block,unsigned offset,std::uint8_t* bytes,unsigned size){
        ++operations;
        for(unsigned i=0;i<size;i++)bytes[i]=block==3?rx[(offset+i)%rx.size()]:regs.at(block).at(offset+i);
        if(block==1&&offset==0x26&&unstable)++regs[1][0x27];
    }
    void write(unsigned block,unsigned offset,const std::uint8_t* bytes,unsigned size){
        ++operations;
        for(unsigned i=0;i<size;i++){
            if(block==2)tx[(offset+i)%tx.size()]=bytes[i];
            else if(block==1&&offset==2)regs[1][2]&=~bytes[i];
            else regs.at(block).at(offset+i)=bytes[i];
        }
        if(block==0&&offset==0&&bytes[0]==0x80)regs[0][0]=0;
        if(block!=1||offset!=1)return;
        ++commands;
        if(bytes[0]==1)regs[1][3]=0x42;
        if(bytes[0]==0x20)++sends;
        if(bytes[0]==0x40){const auto cursor=word(1,0x28);const std::uint16_t delta=cursor-receive_cursor;word(1,0x26,word(1,0x26)-delta);receive_cursor=cursor;}
        if(!command_stuck)regs[1][1]=0;
    }
    void frame(std::uint16_t start,unsigned length){
        receive_cursor=start;word(1,0x28,start);word(1,0x26,length);
        rx[start%rx.size()]=length>>8;rx[(start+1)%rx.size()]=length;
        for(unsigned i=2;i<length;i++)rx[(start+i)%rx.size()]=i;
    }
};
int main(){
    const std::uint8_t mac[]={2,4,5,6,7,8};
    Chip chip;W5500Link<Chip> link(chip);
    auto poll=[&](std::uint32_t t,bool enabled=true){chip.operations=0;link.poll(enabled,mac,t);assert(chip.operations<=23);};
    poll(0,false);assert(chip.operations==0);
    poll(1);assert(!link.online());
    poll(5);assert(chip.operations==0);
    poll(6);poll(7);assert(link.online()&&!std::memcmp(chip.regs[0].data()+9,mac,6));
    assert(chip.regs[1][0]==0x84&&chip.regs[1][0x1e]==16&&chip.regs[1][0x1f]==16);
    std::uint8_t bytes[1518]{};
    chip.frame(65530,80);assert(link.receive(bytes,8)==78);
    for(unsigned i=0;i<78;++i)assert(bytes[i]==i+2);
    assert(chip.word(1,0x28)==74&&chip.word(1,0x26)==0);
    assert(!link.send(bytes,78,8));poll(9);
    chip.word(1,0x24,65520);assert(link.send(bytes,78,9));assert(chip.word(1,0x24)==62&&chip.sends==1);
    assert(chip.tx[16368]==2&&chip.tx[0]==18);
    poll(10);assert(!link.send(bytes,78,10));
    chip.regs[1][2]|=0x10;poll(11);
    assert(!link.send(bytes,1519,12)&&!link.send(bytes,13,12));
    chip.word(1,0x20,0);assert(!link.send(bytes,78,12));
    chip.frame(0,20);chip.unstable=true;assert(!link.receive(bytes,12));chip.unstable=false;
    chip.frame(0,1600);assert(!link.receive(bytes,12)&&link.online()&&chip.word(1,0x26)==0);
    poll(13);chip.frame(0,18);chip.word(1,0x26,8);assert(!link.receive(bytes,13)&&link.online());
    assert(!link.receive(bytes,113)&&!link.online());
    poll(1000);assert(chip.operations==0);
    poll(1113);poll(1118);poll(1119);assert(link.online());
    chip.frame(30,32);chip.command_stuck=true;assert(link.receive(bytes,1120)==30);
    for(unsigned i=1;i<100;i++){poll(1120+i);assert(link.online());}
    poll(1220);assert(!link.online());
    chip.command_stuck=false;poll(2220);poll(2225);poll(2226);assert(link.online());
    chip.regs[0][0x2e]=0;poll(2476);assert(!link.online());
    chip.regs[0][0x39]=0xff;poll(3476);assert(!link.online()&&chip.operations==1);
}
