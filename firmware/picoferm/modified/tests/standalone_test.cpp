#include "picoferm/standalone.hpp"
#include "picoferm/local_controls.hpp"
#include "picobrew/common_web.h"
#include <algorithm>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <map>
#include <memory>
#include <string>
#include <vector>

namespace api=picobrew::standalone;
namespace ferm=picoferm::standalone;
namespace {
unsigned checks=0;
void require(bool ok,const char* message){++checks;if(!ok){std::fprintf(stderr,"PicoFerm standalone: %s\n",message);std::exit(1);}}
unsigned field(const std::string& document,const char* name){
    const auto key=std::string("\"")+name+"\":";const auto start=document.find(key);
    require(start!=std::string::npos,"JSON field exists");
    return static_cast<unsigned>(std::strtoul(document.c_str()+start+key.size(),nullptr,10));
}
struct Bus {
    std::uint8_t registers[32]={};
    std::vector<std::pair<unsigned,unsigned>> writes;
    bool power_ready=true,calibration_done=true,conversion_ready=true;
    unsigned calls=0,fail_at=0,begins=0,pressure_reads=0,temperature_reads=0;
    std::int32_t pressure=100000,temperature=0;
    float battery=3.14f;
    bool good(){return ++calls!=fail_at;}
    ferm::SensorIo binding(){ferm::SensorIo io;io.context=this;
        io.begin=[](void* p,unsigned sda,unsigned scl,unsigned address){auto& b=*static_cast<Bus*>(p);++b.begins;require(sda==2&&scl==14&&address==42,"original I2C pins and address");return b.good();};
        io.read=[](void* p,std::uint8_t reg,std::uint8_t& value){auto& b=*static_cast<Bus*>(p);if(!b.good()||reg>=32)return false;value=b.registers[reg];
            if(reg==0){value=static_cast<std::uint8_t>((value&~40u)|(b.power_ready?8u:0u)|(b.conversion_ready?32u:0u));}
            if(reg==2&&b.calibration_done)value&=static_cast<std::uint8_t>(~4u);
            return true;};
        io.write=[](void* p,std::uint8_t reg,std::uint8_t value){auto& b=*static_cast<Bus*>(p);if(!b.good()||reg>=32)return false;b.registers[reg]=value;b.writes.push_back({reg,value});return true;};
        io.read24=[](void* p,std::uint8_t reg,std::uint32_t& value){auto& b=*static_cast<Bus*>(p);if(!b.good()||reg!=0x12)return false;
            const bool temp=(b.registers[2]&128u)!=0;const unsigned index=temp?++b.temperature_reads:++b.pressure_reads;
            const auto measured=temp?b.temperature:b.pressure;value=static_cast<std::uint32_t>(index%5?123:measured)&0xffffffu;return true;};
        io.battery=[](void* p,float& value){auto& b=*static_cast<Bus*>(p);value=b.battery;return b.good();};return io;
    }
};
struct Memory {
    std::map<std::string,std::vector<std::uint8_t>> files;
    unsigned writes=0,fail_write=0;bool fail_read=false,corrupt_write=false;
    api::Storage binding(){api::Storage io;io.context=this;
        io.read=[](void* p,const char* name,std::size_t offset,std::uint8_t* out,std::size_t cap,std::size_t& count,std::size_t& total){auto& m=*static_cast<Memory*>(p);count=total=0;
            if(m.fail_read)return api::StorageResult::Error;
            const auto it=m.files.find(name);if(it==m.files.end())return api::StorageResult::Missing;
            total=it->second.size();if(offset>total)return api::StorageResult::Error;
            count=std::min(cap,total-offset);if(count)std::memcpy(out,it->second.data()+offset,count);return api::StorageResult::Ok;};
        io.write_atomic=[](void* p,const char* name,const std::uint8_t* value,std::size_t size){auto& m=*static_cast<Memory*>(p);if(++m.writes==m.fail_write)return api::StorageResult::Error;
            m.files[name]=std::vector<std::uint8_t>(value,value+size);if(m.corrupt_write&&!m.files[name].empty())m.files[name][0]^=1;return api::StorageResult::Ok;};
        io.remove=[](void* p,const char* name){static_cast<Memory*>(p)->files.erase(name);return api::StorageResult::Ok;};return io;
    }
};
picoferm::oem::PersistentData original(bool calibrated=true){picoferm::oem::PersistentData result;
    result.word(result.cookie_position,0xdeadbeefu);result.word(result.version_position,1);result.word(result.slope_position,110840);
    result.word(result.pressure_offset_position,calibrated?100000:0);result.bytes[result.has_offset_position]=calibrated?1:0;return result;
}
void run(ferm::BoundedAcquisition& acquisition,std::uint32_t& time,unsigned count){for(unsigned i=0;i<count;++i){acquisition.poll(time);time+=5;}}
void acquisition_cases(){
    Bus bus;ferm::BoundedAcquisition acquisition(bus.binding());std::uint32_t clock=0;
    acquisition.begin(clock);require(acquisition.request(clock),"initial sample can queue during setup");run(acquisition,clock,500);
    require(acquisition.state()==ferm::SensorState::Idle&&acquisition.sample().valid,"bounded boot/acquisition completes");
    require(bus.pressure_reads==5&&bus.temperature_reads==5,"four discarded and one retained sample per mux");
    require(acquisition.sample().pressure_raw==100000&&acquisition.sample().temperature_raw==0,"fifth samples are retained");
    require(acquisition.sample().temperature_f==picoferm::oem::Acquisition::temperature_fahrenheit(0),"original temperature conversion retained");
    require(acquisition.sample().battery_v==3.14f,"battery observation retained");
    require(bus.writes[0]==std::make_pair(0u,41u)&&bus.writes[1]==std::make_pair(0u,40u)&&bus.writes[2]==std::make_pair(0u,42u),"original reset and power sequence");
    require((bus.registers[0]&0x84u)==0x84u&&(bus.registers[1]&0x3fu)==0x28u&&(bus.registers[2]&0x70u)==0x20u,"original reference gain and rate");
    require(bus.registers[0x15]==0x30&&bus.registers[0x1b]==1&&bus.registers[0x11]==0x20,"original internal ADC setup");
    bus.temperature=-100000;require(acquisition.request(clock),"second acquisition");run(acquisition,clock,450);
    require(acquisition.sample().temperature_raw==-100000&&acquisition.sample().sequence==2,"signed24 conversion and sequence");
    bus.conversion_ready=false;require(acquisition.request(clock),"readiness fault attempt");run(acquisition,clock,350);
    require(acquisition.fault()==ferm::SensorFault::ReadyTimeout&&!acquisition.sample().valid,"stalled conversion times out and invalidates old reading");
    const auto calls=bus.calls;run(acquisition,clock,100);require(calls==bus.calls,"fault does not spin on bus");
    bus.conversion_ready=true;bus.pressure=0;acquisition.begin(clock);acquisition.request(clock);run(acquisition,clock,500);
    require(acquisition.fault()==ferm::SensorFault::InvalidPressure,"invalid retained pressure fails explicitly");
    for(bool power:{false,true}){
        Bus blocked;blocked.power_ready=power;blocked.calibration_done=false;ferm::BoundedAcquisition state(blocked.binding());std::uint32_t now=0xffffff00u;
        state.begin(now);run(state,now,650);
        require(state.fault()==(power?ferm::SensorFault::CalibrationTimeout:ferm::SensorFault::PowerTimeout),"setup wait bounded across millis wrap");
        require(blocked.calls<500,"setup timeout call count bounded");
    }
    for(unsigned failure=1;failure<=48;++failure){Bus failing;failing.fail_at=failure;ferm::BoundedAcquisition state(failing.binding());std::uint32_t now=0;
        state.begin(now);state.request(now);run(state,now,500);require(state.fault()==ferm::SensorFault::Bus,"each initialization/register failure is explicit");}
}
struct PanelPorts: picoferm::oem::Ports,picoferm::oem::HardwarePorts,picoferm::oem::LedEvents,picoferm::oem::ButtonEvents {
    std::uint32_t time=0,level=1;unsigned long_presses=0,persists=0;
    std::vector<std::pair<unsigned,unsigned>> writes,modes;
    std::vector<std::string> logs;
    void yield()override{}
    void text(const char* scope,const char* key,const char* value)override{logs.push_back(std::string(scope)+"/"+key+"/"+value);}
    void integer(const char*,const char*,std::uint32_t)override{}
    void number(const char*,const char*,float)override{}
    void boolean(const char*,const char*,bool)override{}
    void digital_write(unsigned pin,bool value)override{writes.push_back({pin,value?1u:0u});}
    std::uint32_t digital_read(unsigned)override{return level;}
    void persist(const std::uint8_t*,std::size_t)override{++persists;}
    std::uint32_t millis()override{return time;}
    void pin_mode(unsigned pin,unsigned mode)override{modes.push_back({pin,mode});}
    std::int32_t analog_read(unsigned)override{return 0;}
    void wire_begin(unsigned,unsigned)override{}
    void wire_begin_transmission(unsigned)override{}
    void wire_write(std::uint8_t)override{}
    void wire_end_transmission()override{}
    void wire_request(unsigned,unsigned)override{}
    std::uint32_t wire_read()override{return 0;}
    void animation_changed(picoferm::oem::Animation)override{}
    void long_press()override{++long_presses;}
};
void control_cases(){
    PanelPorts original_ports,modified_ports;
    picoferm::oem::Leds original_leds(original_ports,original_ports,original_ports);
    picoferm::oem::Button original_button(original_ports,original_ports,original_ports);
    ferm::LocalControls controls(modified_ports,modified_ports,modified_ports,modified_ports);
    original_button.initialize(0);original_leds.initialize();controls.initialize(0);
    require(original_ports.modes==modified_ports.modes&&original_ports.writes==modified_ports.writes,"original button/latch and LED pin setup retained");
    for(unsigned tick=0;tick<3500;tick+=100){
        original_ports.time=modified_ports.time=tick;
        original_ports.level=modified_ports.level=tick<3200?0:1;
        original_button.poll();controls.poll_button();
        original_leds.set(picoferm::oem::Animation::Busy);original_leds.poll();controls.poll_leds(picoferm::oem::Animation::Busy);
    }
    require(original_ports.writes==modified_ports.writes&&original_ports.logs==modified_ports.logs,"normal button and LED sequence matches reconstructed OEM");
    require(modified_ports.long_presses==1&&!controls.button.pressed&&!modified_ports.persists,"long press occurs once without OEM EEPROM writes");
    PanelPorts wrapped;ferm::LocalControls state(wrapped,wrapped,wrapped,wrapped);state.initialize(0);
    wrapped.time=0xffffff00u;wrapped.level=0;state.poll_button();state.poll_leds(picoferm::oem::Animation::Busy);
    const auto writes=wrapped.writes.size();wrapped.time+=100;state.poll_button();state.poll_leds(picoferm::oem::Animation::Busy);
    require(!wrapped.long_presses&&wrapped.writes.size()==writes,"no early LED or button action before clock rollover");
    wrapped.time=0xffffff00u+250u;state.poll_leds(picoferm::oem::Animation::Busy);
    require(wrapped.writes.size()==writes+3,"LED toggles at original250ms period near rollover");
    wrapped.time=0xffffff00u+500u;state.poll_leds(picoferm::oem::Animation::Busy);
    require(wrapped.writes.size()==writes+6,"LED continues across rollover");
    wrapped.time=0xffffff00u+2000u;state.poll_button();require(!wrapped.long_presses,"strictly more than two seconds required across rollover");
    ++wrapped.time;state.poll_button();state.poll_button();require(wrapped.long_presses==1,"one long press after two seconds across rollover");
    const auto events=std::count(wrapped.logs.begin(),wrapped.logs.end(),"BTN/EVENT/LONGPRESS");
    require(events==1,"rollover produces no premature repeated long-press diagnostics");
}
void storage_cases(){
    Memory storage;auto data=original();const auto old=data.bytes;
    ferm::CalibrationStore store(storage.binding());require(store.load(data.bytes.data(),data.bytes.size()).ok(),"import valid OEM record");
    require(store.value().calibrated&&store.value().offset==100000&&store.value().slope==110840,"OEM calibration imported exactly");
    require(data.bytes==old&&storage.files.size()==1&&storage.files.count("ferm-cal"),"import never writes OEM bytes");
    data.word(data.pressure_offset_position,120000);ferm::CalibrationStore reboot(storage.binding());const unsigned before=storage.writes;
    require(reboot.load(data.bytes.data(),data.bytes.size()).ok()&&reboot.value().offset==100000&&storage.writes==before,"existing modified calibration wins without rewrite");
    auto next=reboot.value();next.sampling_seconds=900;require(reboot.save(next).ok(),"sampling interval verified");
    next.offset=34469;require(!reboot.save(next).ok()&&reboot.value().offset==100000,"invalid calibration preserves previous data");
    storage.files["ferm-cal"][8]^=1;ferm::CalibrationStore corrupt(storage.binding());const auto bytes=storage.files["ferm-cal"];const auto count=storage.writes;
    require(!corrupt.load(data.bytes.data(),data.bytes.size()).ok()&&!corrupt.healthy(),"corrupt modified record rejected");
    require(storage.files["ferm-cal"]==bytes&&storage.writes==count,"corruption never triggers OEM fallback overwrite");
    Memory blank;ferm::CalibrationStore defaults(blank.binding());require(defaults.load(nullptr,0).ok()&&!defaults.value().calibrated,"missing source requires explicit calibration");
    next=defaults.value();next.offset=110000;next.calibrated=true;blank.corrupt_write=true;
    require(!defaults.save(next).ok()&&!defaults.healthy()&&!defaults.value().calibrated,"failed readback never activates a new offset");
    Memory unreadable;unreadable.fail_read=true;ferm::CalibrationStore blocked(unreadable.binding());require(!blocked.load(old.data(),old.size()).ok()&&unreadable.writes==0,"I/O failure is not missing state");
}
struct World {
    Memory memory;Bus bus;std::uint32_t time=0;std::unique_ptr<ferm::Controller> controller;std::unique_ptr<api::Engine> engine;
    explicit World(bool calibrated=true){reboot(calibrated);}
    api::Clock clock(){api::Clock result;result.context=this;result.millis=[](void* p){return static_cast<World*>(p)->time;};return result;}
    void reboot(bool calibrated=true){engine.reset();controller.reset();controller.reset(new ferm::Controller(bus.binding(),memory.binding(),clock()));
        require(controller->identity("picoferm-local-test"),"stable device UID configured");engine.reset(new api::Engine(controller->services(),memory.binding(),clock()));controller->bind(*engine);
        const auto source=original(calibrated);require(controller->begin(source.bytes.data(),source.bytes.size()).ok(),"controller begins");require(engine->begin().ok(),"real Engine loads");advance(2500);
    }
    void advance(unsigned milliseconds){for(unsigned n=0;n<milliseconds;n+=5){controller->poll();engine->poll();time+=5;}}
    api::Snapshot snapshot(){api::Snapshot result;require(controller->snapshot(result).ok(),"snapshot succeeds");return result;}
    std::pair<unsigned,std::string> api_call(PicoHttpMethod method,const char* path,const char* body=""){
        PicoHttpRequest request={};request.method=method;std::strncpy(request.path,path,sizeof(request.path)-1);request.body=body;request.body_length=std::strlen(body);request.authenticated=request.auth_configured=true;
        PicoHttpResponse response={};char buffer[PICO_HTTP_ARENA_CAPACITY]={};require(engine->handle(request,response,buffer,sizeof(buffer)),"actual API route handled");
        return {response.status,std::string(reinterpret_cast<const char*>(response.body),response.body_length)};
    }
};
// Capture the TCP destination and bytes that the PicoFerm would send.
struct HubReceiver {
    std::string host, frame;
    unsigned port = 0;
    api::TelemetryIo binding() {
        api::TelemetryIo io;
        io.context = this;
        io.begin = [](void* ctx, const char* hostname, std::uint16_t port) {
            auto& self = *static_cast<HubReceiver*>(ctx);
            self.host = hostname; self.port = port; self.frame.clear();
            return true;
        };
        io.poll_connect = [](void*) { return 1; };
        io.send = [](void* ctx, const std::uint8_t* data, std::size_t size) {
            static_cast<HubReceiver*>(ctx)->frame.append(reinterpret_cast<const char*>(data), size);
            return static_cast<int>(size);
        };
        io.receive = [](void*, std::uint8_t* out, std::size_t capacity) {
            const char response[] = "HTTP/1.1 202 Accepted\r\n";
            const auto n = std::min(capacity, sizeof(response)-1);
            std::memcpy(out, response, n);
            return static_cast<int>(n);
        };
        io.close = [](void*) {};
        return io;
    }
};

void paired_receiver_cases() {
    for (const std::string host : {"brewery.example", "192.0.2.17"}) {
        HubReceiver receiver;
        World world;
        const auto settings = std::string("{\"enabled\":true,\"host\":\"") + host + "\",\"port\":8181}";
        require(world.api_call(PICO_HTTP_PUT, "/api/v1/telemetry", settings.c_str()).first == 200,
                "the Hub can provision a receiver through the PicoFerm API");
        world.reboot();
        world.engine->set_telemetry_io(receiver.binding());
        world.advance(1000);
        require(receiver.host == host && receiver.port == 8181, "saved receiver is used after restart");
        require(receiver.frame.find("POST /api/v1/telemetry HTTP/1.0\r\nHost: " + host + ":8181\r\n") == 0,
                "sensor report goes directly to the paired Hub");
        for (const char* id : {"temperature", "pressure_native", "battery"}) {
            require(receiver.frame.find(std::string("\"id\":\"") + id + "\"") != std::string::npos,
                    "sensor report contains the PicoFerm measurement");
        }
        const auto status = world.api_call(PICO_HTTP_GET, "/api/v1/telemetry");
        require(status.first == 200 && field(status.second, "last_http_status") == 202,
                "the local website can read the receiver acknowledgment");
    }
}
void controller_cases(){
    World world;auto current=world.snapshot();require(current.ready&&!current.busy&&!current.recording,"standalone ready without cloud or session resume");
    const auto care = world.api_call(PICO_HTTP_POST, "/api/v1/commands", "{\"command\":\"ack-sanitize-seal\"}");
    require(care.first == 202 && care.second.find("\"accepted\":true") != std::string::npos
            && care.second.find("\"command\":\"ack-sanitize-seal\"") != std::string::npos,
            "Hub receives a verified seal acknowledgment");
    require(current.sensor_count==3&&!current.actuator_count&&!current.pressure_kpa.valid&&!current.wort_f.valid,"only actual accessory measurements");
    const auto discovery=world.api_call(PICO_HTTP_GET,"/api/v1");require(discovery.first==200&&discovery.second.find("\"model\":\"picoferm\"")!=std::string::npos,"real Engine advertises PicoFerm identity");
    require(discovery.second.find("ota_protocol")==std::string::npos,"single-bank firmware omits OTA");
    world.engine->set_ota_protocol("esp8266-ab-v1");
    require(world.api_call(PICO_HTTP_GET,"/api/v1").second.find("\"ota_protocol\":\"esp8266-ab-v1\"")!=std::string::npos,"qualified live bootstrap is discoverable");
    world.engine->set_ota_protocol(nullptr);
    require(discovery.second.find("\"recipes\":false")!=std::string::npos&&discovery.second.find("\"front_panel\":false")!=std::string::npos,"no fictitious capabilities");
    require(world.api_call(PICO_HTTP_GET,"/api/v1/display").first==404&&world.api_call(PICO_HTTP_GET,"/api/v1/recipes").first==404,"unsupported resources absent");
    require(world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"start\"}").first==202,"local start accepted with reserved history");
    current=world.snapshot();require(current.recording&&current.session_token!=0,"native recording uses reserved token");
    require(world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"set-sampling-interval\",\"value\":900}").first==409,"active session cadence cannot change");
    world.advance(2500);const auto history=world.api_call(PICO_HTTP_GET,"/api/v1/history");
    require(history.second.find("sensor_pressure_native")!=std::string::npos&&history.second.find("\"sessions\":[{")!=std::string::npos,"history includes actual named pressure");
    require(field(history.second,"sample_interval_ms")==300000,"session captures configured default cadence");
    const auto retained=field(history.second,"record_count");world.advance(45000);
    require(field(world.api_call(PICO_HTTP_GET,"/api/v1/history").second,"record_count")==retained,"300-second measurements are not copied into 15-second rows");
    require(world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"stop\"}").first==202&&!world.snapshot().recording,"stop ends recording immediately");
    world.reboot();require(!world.snapshot().recording,"reboot never resumes saved session");
    require(field(world.api_call(PICO_HTTP_GET,"/api/v1/history").second,"sample_interval_ms")==300000,"saved cadence survives stop and reboot");
    for(const char* invalid:{"59","86401","60.5","null"}){const std::string command=std::string("{\"command\":\"set-sampling-interval\",\"value\":")+invalid+"}";
        require(world.api_call(PICO_HTTP_POST,"/api/v1/commands",command.c_str()).first==400,"sampling range/type validated");}
    require(world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"set-sampling-interval\",\"value\":60}").first==202,"minimum interval accepted");
    const auto sequence=world.controller->acquisition().sample().sequence;world.advance(59000);require(world.controller->acquisition().sample().sequence==sequence,"no early periodic sample");
    world.advance(4000);require(world.controller->acquisition().sample().sequence==sequence+1,"periodic bounded sample at configured interval");
    require(world.api_call(PICO_HTTP_PATCH,"/api/v1/settings","{\"temperature_unit\":\"C\"}").first==200,"supported display unit persists");
    require(world.api_call(PICO_HTTP_PATCH,"/api/v1/settings","{\"language\":\"fr\"}").first==400,"unsupported preference rejected");
    world.reboot();require(world.controller->configuration().value().sampling_seconds==60,"sampling interval survives restart");
    require(world.api_call(PICO_HTTP_GET,"/api/v1/settings").second.find("\"C\"")!=std::string::npos,"shared display setting survives restart");
    require(world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"start\"}").first==202,"new session starts after interval update");
    world.advance(2500);const auto changed=world.api_call(PICO_HTTP_GET,"/api/v1/history");
    require(field(changed.second,"sample_interval_ms")==60000,"new session captures new cadence");
    require(changed.second.find("\"sample_interval_ms\":300000")!=std::string::npos,"older session keeps original cadence");
    world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"stop\"}");
}
void calibration_cases(){
    World world(false);require(world.snapshot().ready&&!world.snapshot().sensors[1].reading.valid,"uncalibrated unit still permits explicit calibration");
    require(world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"start\"}").first==409,"uncalibrated session cannot start");
    const auto writes=world.memory.writes;const auto sequence=world.controller->acquisition().sample().sequence;
    require(world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"calibrate\",\"confirm\":false}").first==400,"false calibration confirmation rejected");
    require(world.memory.writes==writes&&world.controller->acquisition().sample().sequence==sequence,"unconfirmed calibration performs no work");
    world.bus.pressure=120000;
    require(world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"calibrate\",\"confirm\":true}").first==202,"confirmed calibration queues fresh measurement");
    require(!world.controller->configuration().value().calibrated,"old sample never used as calibration");world.advance(2500);
    require(world.controller->configuration().value().offset==120000&&world.snapshot().sensors[1].reading.value==0,"verified fresh offset takes effect");
    world.bus.pressure=34000;require(world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"calibrate\",\"confirm\":true}").first==202,"out-of-range offset acquired for validation");world.advance(2500);
    require(world.controller->configuration().value().offset==120000,"invalid offset does not overwrite calibration");
    world.bus.pressure=130000;world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"calibrate\",\"confirm\":true}");
    world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"stop\"}");world.advance(2500);
    require(world.controller->configuration().value().offset==120000,"stop cancels pending calibration commit");
    world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"calibrate\",\"confirm\":true}");
    world.memory.fail_write=world.memory.writes+1;world.advance(2500);
    require(!world.controller->configuration().healthy()&&world.controller->configuration().value().offset==120000,"storage failure cannot activate new calibration");
    require(world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"stop\"}").first==202,"stop remains available on storage fault");
}
void session_failure_and_fault_cases(){
    World world;world.memory.fail_write=world.memory.writes+1;
    require(world.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"start\"}").first==503&&!world.snapshot().recording,"failed identity reservation prevents recording");
    World failed;failed.bus.conversion_ready=false;
    require(failed.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"sample\"}").first==202,"faulting sample accepted asynchronously");
    failed.advance(1700);require(failed.controller->acquisition().fault()==ferm::SensorFault::ReadyTimeout&&!failed.snapshot().temperature_f.valid,"sensor fault invalidates readings");
    require(failed.api_call(PICO_HTTP_GET,"/api/v1/status").first==200&&failed.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"stop\"}").first==202,"status and stop available after ADC timeout");
    failed.bus.conversion_ready=true;require(failed.api_call(PICO_HTTP_POST,"/api/v1/commands","{\"command\":\"sample\"}").first==202,"explicit bounded recovery accepted");failed.advance(2500);require(failed.snapshot().ready,"sensor recovers without reboot");
    failed.controller->long_press();require(failed.snapshot().recording,"physical long press starts explicit local session");failed.controller->long_press();require(!failed.snapshot().recording,"physical long press stops session");
}
}
int main(){acquisition_cases();control_cases();storage_cases();paired_receiver_cases();controller_cases();calibration_cases();session_failure_and_fault_cases();std::printf("{\"checks\":%u,\"complete\":true}\n",checks);}
