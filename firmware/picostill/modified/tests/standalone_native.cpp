#include "picostill/standalone.hpp"
#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <map>
#include <memory>
#include <string>
#include <vector>
namespace api = picobrew::standalone;
namespace oem = picostill::oem;
namespace {
unsigned checks = 0;
#define CHECK(x) do { ++checks; if (!(x)) { std::fprintf(stderr,"check failed at %u: %s\n",__LINE__,#x); std::abort(); } } while (0)
struct Ports final : oem::HardwarePorts, oem::I2cPorts, oem::DiagnosticPorts {
    std::uint32_t time = 0;
    bool button = true, tach = true, pressure_ok = true, adc_ok = true;
    bool pins[40] = {}; unsigned pwm[4] = {}, writes = 0, high_pump_writes = 0, assertions = 0;
    unsigned adc_raw = 2048, pressure_raw = 8120172, requests = 0;
    std::vector<unsigned> input; std::size_t at = 0;
    std::uint32_t millis() override { return time; }
    void pin_mode(std::uint8_t, unsigned) override {}
    bool digital_read(std::uint8_t pin) override { return pin == 13 ? button : tach && ((time / 5) & 1); }
    void digital_write(std::uint8_t pin, bool value) override { CHECK(pin < 40); pins[pin] = value; ++writes; if (pin == 19 && value) ++high_pump_writes; }
    void pwm_setup(std::uint8_t, double, unsigned) override {}
    void pwm_attach(std::uint8_t, std::uint8_t) override {}
    void pwm_write(std::uint8_t channel, std::uint8_t duty) override { CHECK(channel < 4); pwm[channel] = duty; ++writes; }
    void variable(const char*, const char*, const char*) override {}
    void begin_transmission(std::uint8_t) override {}
    void write_byte(std::uint8_t) override {}
    void end_transmission() override {}
    unsigned request(std::uint8_t address, unsigned count) override {
        ++requests; input.clear(); at = 0;
        if (address == 0x18 && pressure_ok) input = {0x40, pressure_raw >> 16, (pressure_raw >> 8) & 255, pressure_raw & 255};
        if (address == 0x34 && adc_ok) for (unsigned i = 0; i < 4; ++i) { input.push_back(adc_raw >> 8); input.push_back(adc_raw & 255); }
        CHECK(count == 4 || count == 8); return input.size();
    }
    int available() override { return input.size() - at; }
    int read_byte() override { return at < input.size() ? input[at++] : -1; }
    void delay_ms(std::uint32_t) override {}
    void assertion(const char*, unsigned, const char*) override { ++assertions; }
    void message(const char*) override {}
};
struct Files {
    std::map<std::string,std::vector<std::uint8_t>> data;
    unsigned writes = 0, fail_write = 0;
    api::Storage binding() {
        api::Storage s; s.context = this;
        s.read=[](void* p,const char* path,std::size_t offset,std::uint8_t* bytes,std::size_t capacity,std::size_t& copied,std::size_t& total) {
            auto& files=*static_cast<Files*>(p); copied=total=0; const auto it=files.data.find(path);
            if(it==files.data.end())return api::StorageResult::Missing;
            total=it->second.size(); if(offset>total)return api::StorageResult::Error;
            copied=std::min(capacity,total-offset); if(copied)std::memcpy(bytes,it->second.data()+offset,copied); return api::StorageResult::Ok;
        };
        s.write_atomic=[](void* p,const char* path,const std::uint8_t* bytes,std::size_t size) {
            auto& files=*static_cast<Files*>(p); ++files.writes;
            if(files.fail_write && !--files.fail_write)return api::StorageResult::Error;
            files.data[path]=std::vector<std::uint8_t>(bytes,bytes+size);return api::StorageResult::Ok;
        };
        s.remove=[](void* p,const char* path){return static_cast<Files*>(p)->data.erase(path)?api::StorageResult::Ok:api::StorageResult::Missing;};return s;
    }
};
struct World {
    Ports ports; Files files; picostill::standalone::Device device; api::Engine engine;
    unsigned status = 0;
    static api::Clock clock(Ports& ports) { api::Clock c; c.context=&ports; c.millis=[](void*p){return static_cast<Ports*>(p)->time;};return c; }
    explicit World(bool ready=true,const Files* restored=nullptr,bool wireless=false):device(ports,ports,ports),engine(device.services(),files.binding(),clock(ports)) {
        CHECK(ports.writes==0); if(restored)files.data=restored->data;
        device.bind_engine(engine); device.begin(ready,"000011112222"); CHECK(!ports.pins[18]&&!ports.pins[19]);
        device.wireless_capable(wireless);CHECK(engine.begin().ok());device.storage_ready(true);
    }
    void poll(){device.poll();engine.poll();}
    void tick(unsigned amount) { for(unsigned n=0;n<amount;n+=5){ports.time+=std::min(5u,amount-n);poll();} }
    void warm(){poll();tick(2100);CHECK(device.sensors_healthy());}
    std::string request(const char* path,const char* body=nullptr,PicoHttpMethod method=PICO_HTTP_GET) {
        PicoHttpRequest request={};PicoHttpResponse response={};char arena[PICO_HTTP_ARENA_CAPACITY]={};
        request.auth_configured=request.authenticated=true;request.method=method;std::snprintf(request.path,sizeof(request.path),"%s",path);
        if(body){request.body_length=std::strlen(body);CHECK(request.body_length<sizeof(arena));std::memcpy(arena,body,request.body_length);}
        request.body=arena;CHECK(device.brewer_link(request,response,arena,sizeof arena)||engine.handle(request,response,arena,sizeof(arena)));status=response.status;
        CHECK(response.body_length<sizeof(arena));return std::string(reinterpret_cast<const char*>(response.body),response.body_length);
    }
    std::string command(const char* body){return request("/api/v1/commands",body,PICO_HTTP_POST);}
    bool off()const{return !ports.pins[19]&&!ports.pins[18]&&!ports.pwm[1]&&!ports.pwm[2];}
    api::Snapshot snapshot(){api::Snapshot s;CHECK(device.snapshot(s).ok());return s;}
};
void discovery_and_start_guard(){
    World w;w.command("{\"command\":\"start\"}");CHECK(w.status==409);CHECK(w.off());
    const auto acknowledgment = w.command("{\"command\":\"ack-clean-condenser\"}");
    CHECK(w.status == 202 && acknowledgment.find("\"accepted\":true") != std::string::npos);
    CHECK(acknowledgment.find("\"command\":\"ack-clean-condenser\"") != std::string::npos);
    const auto info=w.request("/api/v1");CHECK(info.find("\"model\":\"picostill\"")!=std::string::npos);
    CHECK(info.find("\"ota_protocol\"")==std::string::npos);
    World provisioned(true,nullptr,true);
    auto qualified=provisioned.request("/api/v1");
    CHECK(qualified.find("\"ota_protocol\":\"esp32-factory-v1\"")!=std::string::npos);
    CHECK(qualified.find("\"firmware_version\":\"1.0.0\"")!=std::string::npos);
    CHECK(info.find("\"heating\":false")!=std::string::npos);CHECK(info.find("\"recipes\":false")!=std::string::npos);
    w.request("/api/v1/recipes");CHECK(w.status==404);w.request("/api/v1/display");CHECK(w.status==404);
    w.command("{\"command\":\"brew\",\"recipe_id\":\"bad\"}");CHECK(w.status==400);
    w.warm();auto s=w.snapshot();CHECK(s.sensor_count==5&&s.actuator_count==3);
    for(unsigned i=0;i<4;++i){CHECK(s.sensors[i].reading.valid);CHECK(std::fabs(s.sensors[i].reading.value-(w.device.hardware.sensors.values[i]*1.8+32))<1e-5);}
    CHECK(std::fabs(s.pressure_kpa.value-12*6.894757293168)<1e-4);CHECK(s.temperature_f.value==s.sensors[2].reading.value);
    CHECK(!s.actuators[0].supports_level&&!s.actuators[0].supports_rpm);CHECK(s.actuators[1].supports_level&&s.actuators[1].supports_rpm);
    w.ports.adc_ok=false;w.tick(2100);CHECK(!w.snapshot().sensors[0].reading.valid);
    w.command("{\"command\":\"start\"}");CHECK(w.status==409);CHECK(w.off());
    World no_board(false);no_board.command("{\"command\":\"start\"}");CHECK(no_board.status==503);CHECK(no_board.off());
}
void reservation_and_manual_control(){
    World bad;bad.warm();bad.files.fail_write=1;bad.command("{\"command\":\"start\"}");CHECK(bad.status==503);CHECK(bad.off());CHECK(bad.ports.high_pump_writes==0);
    bad.command("{\"command\":\"stop\"}");CHECK(bad.status==202&&bad.off());
    World w;w.warm();w.command("{\"command\":\"start\"}");CHECK(w.status==202);w.poll();CHECK(w.ports.pins[19]&&w.ports.pins[18]&&w.ports.pwm[2]==255);
    CHECK(w.request("/api/v1/history").find("\"id\":1")!=std::string::npos);
    w.command("{\"command\":\"set-fan\",\"channel\":2,\"level\":60}");CHECK(w.status==409);CHECK(w.ports.pwm[2]==255);
    w.command("{\"command\":\"set-fan\",\"channel\":3,\"level\":90}");CHECK(w.status==400);
    w.command("{\"command\":\"set-fan\",\"channel\":1,\"level\":50}");CHECK(w.status==202);CHECK(w.ports.pwm[1]==128);
    w.command("{\"command\":\"set-pump\",\"enabled\":false}");CHECK(w.status==202&&!w.ports.pins[19]);
    const auto high=w.ports.high_pump_writes;w.device.controller.set_state(oem::HWState::PumpOff);w.device.controller.set_state(oem::HWState::On);
    CHECK(w.ports.high_pump_writes==high&&!w.ports.pins[19]);w.poll();CHECK(!w.snapshot().actuators[0].enabled);
    w.command("{\"command\":\"set-pump\",\"enabled\":true}");CHECK(w.status==202&&w.ports.pins[19]);
    w.command("{\"command\":\"stop\"}");CHECK(w.status==202&&w.off());w.poll();CHECK(!w.snapshot().recording);
    CHECK(w.request("/api/v1/history").find("\"state\":\"completed\"")!=std::string::npos);
    w.request("/api/v1/settings","{\"temperature_unit\":\"C\"}",PICO_HTTP_PATCH);CHECK(w.status==200);
    World restored(true,&w.files);CHECK(restored.off()&&!restored.snapshot().recording);
    CHECK(restored.request("/api/v1/settings").find("\"temperature_unit\":\"C\"")!=std::string::npos);
}
void native_programs_and_faults(){
    World w;w.warm();w.command("{\"command\":\"clean\"}");CHECK(w.status==202);CHECK(w.ports.pins[19]&&w.ports.pwm[2]==255);
    w.tick(19995);CHECK(w.snapshot().recording);w.tick(5);CHECK(w.off()&&!w.snapshot().recording);
    World failed;failed.warm();failed.command("{\"command\":\"essential-oils\"}");CHECK(failed.status==202);
    failed.ports.pressure_ok=false;failed.tick(6100);CHECK(failed.off()&&!failed.snapshot().recording);CHECK(failed.snapshot().stopped);
    CHECK(std::string(failed.snapshot().stop_reason)=="hardware-fault");
    CHECK(failed.request("/api/v1/history").find("\"state\":\"completed\"")!=std::string::npos);
    World fan;fan.warm();fan.command("{\"command\":\"start\"}");fan.ports.tach=false;fan.tick(5000);
    CHECK(fan.off()&&!fan.snapshot().recording);CHECK(fan.device.controller.error==oem::StillError::FanFailure);
}
void physical_button_and_wrap(){
    World w;w.warm();w.ports.button=false;w.poll();w.tick(5000);CHECK(w.off());w.ports.button=true;w.tick(250);CHECK(w.off());
    for(unsigned i=0;i<2;++i){w.ports.button=false;w.tick(20);w.ports.button=true;w.tick(50);}w.tick(250);
    CHECK(w.snapshot().recording&&w.ports.pins[19]);w.ports.button=false;w.poll();CHECK(w.off()&&!w.snapshot().recording);
    CHECK(std::string(w.snapshot().stop_reason)=="physical-button");
    World wrapped;wrapped.ports.time=0xfffff000u;wrapped.device.hardware.next_sample=wrapped.ports.time;wrapped.warm();
    wrapped.command("{\"command\":\"start\"}");CHECK(wrapped.status==202);
    wrapped.device.hardware.sensors.values[1]=40;wrapped.device.controller.set_state(oem::HWState::CondenserOutputOverTemp);
    // Keep the native sensor read from replacing this explicit overtemperature
    // test vector, while tach input and actual controller transitions execute.
    wrapped.device.hardware.next_sample=wrapped.ports.time+200000;
    wrapped.tick(2000);CHECK(wrapped.ports.pins[19]);CHECK(wrapped.device.controller.state==oem::HWState::CondenserOutputOverTemp);
    wrapped.tick(118000);CHECK(!wrapped.ports.pins[19]);CHECK(wrapped.device.controller.state==oem::HWState::PumpOff);
    CHECK(wrapped.device.hardware.fans.second.metrics.samples>4);CHECK(wrapped.device.hardware.fans.second.metrics.rpm_average>=1600);
    wrapped.device.hardware.next_sample=wrapped.ports.time;wrapped.tick(5);CHECK(wrapped.device.hardware.sensors.values[1]<35);
    CHECK(wrapped.device.controller.state==oem::HWState::On&&wrapped.ports.pins[19]);
    wrapped.device.stop();wrapped.poll();CHECK(wrapped.off());
    World zero;zero.ports.time=0xfffff830u;zero.device.hardware.next_sample=zero.ports.time;zero.poll();
    CHECK(zero.device.hardware.next_sample==0);const unsigned requests=zero.ports.requests;
    zero.tick(1000);CHECK(zero.ports.requests==requests);zero.tick(1000);CHECK(zero.ports.requests==requests+2);
}
}
void paired_brewer(){
    World w;w.warm();w.device.link_nonce(0x11335577);
    auto request=[&](const char* op,const char* token,unsigned expected,const char* brewer="010203040506"){
        std::string body=std::string("{\"op\":\"")+op+"\",\"brewer\":\""+brewer+"\",\"uid\":\"000011112222\"";
        if(token)body+=std::string(",\"lease\":\"")+token+"\"";
        body+="}";auto res=w.request("/api/v1/brewer-link",body.c_str(),PICO_HTTP_POST);
        if(w.status!=expected)std::fprintf(stderr,"%s t=%u idle=%d healthy=%d expected %u got %u %s\n",op,w.ports.time,w.device.idle(),w.device.sensors_healthy(),expected,w.status,res.c_str());
        CHECK(w.status==expected);return res;
    };
    const char* token="1133557700000001";
    CHECK(request("probe",nullptr,200).find("\"lease\":\"\"")!=std::string::npos);
    CHECK(w.engine.record_care(api::CareEvent::StillUsed).ok());
    CHECK(request("claim",nullptr,200).find(token)!=std::string::npos);
    CHECK(!w.device.idle()&&w.off());
    request("claim",nullptr,409,"999999999999");
    request("start",token,409,"999999999999");request("start",token,409);
    w.command("{\"command\":\"clean\"}");CHECK(w.status==409&&w.off());
    request("cleaned",token,200);CHECK(w.off());
    CHECK(request("start",token,200).find("\"active\":true")!=std::string::npos);
    CHECK(w.device.controller.running()&&w.device.controller.program==oem::ProgramId::PicoManual);
    request("start",token,200);request("fan-on",token,200);CHECK(w.device.hardware.fans.first.enabled);
    request("fan-off",token,200);CHECK(!w.device.hardware.fans.first.enabled);
    w.tick(5000);request("poll",token,200);w.tick(5000);
    request("probe",nullptr,200); // Discovery cannot renew another brewer's session.
    w.tick(1499);CHECK(w.device.controller.running());w.tick(1);
    CHECK(w.off()&&w.device.idle()&&std::string(w.snapshot().stop_reason)=="brewer-link-timeout");
    request("start",token,409);w.tick(2200);request("claim",nullptr,200);
    w.ports.button=false;w.poll();CHECK(w.off()&&w.device.idle());w.ports.button=true;w.poll();
    request("start","1133557700000002",409);
    request("claim",nullptr,200);request("cleaned","1133557700000003",200);request("start","1133557700000003",200);
    w.ports.adc_ok=false;w.tick(3000);CHECK(w.off()&&w.device.idle());
    PicoHttpRequest req{};std::strcpy(req.path,"/api/v1/brewer-link");req.method=PICO_HTTP_POST;req.auth_configured=true;
    PicoHttpResponse res{};char out[300];CHECK(w.device.brewer_link(req,res,out,sizeof out)&&res.status==401);
}
int main(){paired_brewer();discovery_and_start_guard();reservation_and_manual_control();native_programs_and_faults();physical_button_and_wrap();
    std::printf("PicoStill standalone: %u checks passed; native controller, sensor conversions, guarded outputs, physical controls, history and rollover\n",checks);}
