#pragma once
#include "records.hpp"
#include "zseries/eeprom.hpp"

namespace zseries { namespace standalone {

// The protected partition layout has no Arduino EEPROM partition. The Head and
// Core still use the original 120-byte representation, including boiler type.
class BoardSettings {
public:
    explicit BoardSettings(picobrew::standalone::Storage storage):storage_(storage), records_(storage){}
    bool load() {
        using namespace picobrew::standalone;
        ready_=false;
        generation_=0;
        std::size_t length=0;
        const auto result=records_.load("zboard",bytes_.data(),bytes_.size(),length,generation_);
        if(result==detail::RecordResult::Missing){
            std::size_t total=0;
            const auto seed=storage_.read(storage_.context,"zboard.seed",0,bytes_.data(),bytes_.size(),length,total);
            if(seed==StorageResult::Missing){bytes_=oem::encode_eeprom(oem::EepromData{});length=bytes_.size();}
            else if(seed!=StorageResult::Ok||total!=bytes_.size())return false;
        }else if(result!=detail::RecordResult::Ok)return false;
        ready_=length==bytes_.size()&&valid(bytes_);
        return ready_;
    }
    bool wired() const {return ready_&&oem::decode_eeprom(bytes_).use_ethernet;}
    oem::EepromBackend backend(){
        oem::EepromBackend out;
        out.context=this;
        out.begin=[](void* p,std::uint32_t n){return static_cast<BoardSettings*>(p)->ready_&&n>=oem::EepromDataSize;};
        out.read_data=[](void* p,oem::EepromBytes& bytes){bytes=static_cast<BoardSettings*>(p)->bytes_;};
        out.write_data=[](void* p,const oem::EepromBytes& bytes){static_cast<BoardSettings*>(p)->pending_=bytes;};
        out.commit=[](void* p){
            auto& self=*static_cast<BoardSettings*>(p);
            if(!self.ready_||!valid(self.pending_))return false;
            if(self.pending_==self.bytes_)return true;
            std::uint32_t next=0;
            if(self.records_.save("zboard",self.pending_.data(),self.pending_.size(),self.generation_,next)!=picobrew::standalone::detail::RecordResult::Ok)return false;
            self.bytes_=self.pending_;self.generation_=next;return true;
        };
        return out;
    }
private:
    static bool valid(const oem::EepromBytes& bytes){
        auto settings=oem::decode_eeprom(bytes);
        return oem::eeprom_data_valid(settings)&&!oem::eeprom_enforce_valid_enums(settings)&&
               settings.use_ethernet<=1&&settings.use_imperial<=1&&settings.show_detail<=1&&settings.slideshow_cycle_static<=1;
    }
    picobrew::standalone::Storage storage_;
    picobrew::standalone::detail::Records records_;
    oem::EepromBytes bytes_{},pending_{};
    std::uint32_t generation_=0;
    bool ready_=false;
};

}}
