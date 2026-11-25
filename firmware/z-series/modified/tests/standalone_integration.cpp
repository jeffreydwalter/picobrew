#include "zseries/board_settings.hpp"
#include "zseries/standalone.hpp"
#include "zseries/program_recipe_json.hpp"
#include "zseries/program_runtime_heater.hpp"
#include <algorithm>
#include <cassert>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <map>
#include <string>
#include <vector>
using namespace zseries::oem;
using zseries::standalone::Adapter;
namespace api=picobrew::standalone;
namespace {
struct World {
    HeadApplication head;
    CoreApplication core{head.core_events};
    Adapter adapter{core,head};
    std::map<std::string,std::vector<std::uint8_t>> files;
    std::vector<std::vector<std::uint8_t>> frames;
    std::map<unsigned,bool> pins;
    std::vector<HeadWifiNetwork> access_points;
    bool reject_network=false,reject_save=false;
    unsigned switches=0;std::uint32_t wired_ip=0;
    std::string still_request, still_response;
    std::size_t response_offset=0;
    unsigned still_chunk=53;
    api::TelemetryIo still_io(){
        api::TelemetryIo io;io.context=this;
        io.begin=[](void* p,const char* host,std::uint16_t port){auto& w=self(p);w.still_request.clear();w.response_offset=0;
            return !std::strcmp(host,"still.local")&&port==80;};
        io.poll_connect=[](void*){return 1;};
        io.send=[](void* p,const std::uint8_t* data,std::size_t size){auto& w=self(p);
            unsigned count=static_cast<unsigned>(std::min<std::size_t>(size,w.still_chunk));w.still_request.append(reinterpret_cast<const char*>(data),count);return int(count);};
        io.receive=[](void* p,std::uint8_t* data,std::size_t size){auto& w=self(p);
            const auto left=w.still_response.size()-w.response_offset;
            if(!left)return int(PICO_HTTP_IO_AGAIN);
            const auto count=std::min<std::size_t>(w.still_chunk,std::min(size,left));
            std::memcpy(data,w.still_response.data()+w.response_offset,count);w.response_offset+=count;return int(count);};
        io.close=[](void*){};return io;
    }
    std::uint32_t pending_pair=0;
    unsigned approved_pair=0, cancelled_pair=0;
    int scan_result=-1;
    unsigned scan_reads=0;
    std::uint32_t now=10000;
    unsigned writes=0,fail_write=0,alarms=0,locks=0,displays=0,http_polls=0,assertions=0;
    bool filter_missing=false,lan=false,sensor_short=false,sensor_bad_byte=false,sensor_bad_write=false;
    api::Engine engine{adapter.services(),storage(),clock()};
    static World& self(void* p){return *static_cast<World*>(p);}
    api::Clock clock(){api::Clock c;c.context=this;c.millis=[](void* p){return self(p).now;};return c;}
    api::Storage storage(){
        api::Storage io;io.context=this;
        io.read=[](void* p,const char* name,std::size_t offset,std::uint8_t* data,std::size_t size,std::size_t& copied,std::size_t& total){
            auto& w=self(p);copied=total=0;auto found=w.files.find(name);if(found==w.files.end())return api::StorageResult::Missing;
            total=found->second.size();if(offset>total)return api::StorageResult::Error;copied=std::min(size,total-offset);
            if(copied)std::memcpy(data,found->second.data()+offset,copied);
            return api::StorageResult::Ok;};
        io.write_atomic=[](void* p,const char* name,const std::uint8_t* data,std::size_t size){auto& w=self(p);++w.writes;
            if(w.fail_write&&!--w.fail_write)return api::StorageResult::Error;
            w.files[name]=std::vector<std::uint8_t>(data,data+size);return api::StorageResult::Ok;};
        io.remove=[](void* p,const char* name){return self(p).files.erase(name)?api::StorageResult::Ok:api::StorageResult::Missing;};return io;
    }
    explicit World(const std::map<std::string,std::vector<std::uint8_t>>* restored=nullptr,bool wired=false){
        if(restored)files=*restored;
        head.bind_settings(core.eeprom.data);std::strcpy(core.identifier.data(),"112233445566");
        core.ports.context=this;core.ports.millis=[](void* p){return self(p).now;};
        core.ports.control.context=this;core.ports.control.enter=[](void* p){assert(!self(p).locks++);};
        core.ports.control.leave=[](void* p){assert(self(p).locks--==1);};
        auto& d=core.ports.diagnostics;d.context=this;
        d.log=[](void*,ConsoleLevel,const char*,const char*,std::uint32_t,const char*){return true;};
        d.assertion=[](void* p,const char*,std::uint32_t,const char*){++self(p).assertions;self(p).now+=200;return true;};
        core.runtime.services.diagnostics=d;
        core.ports.heater_gpio.context=this;core.ports.heater_gpio.configure_output=[](void*,std::uint8_t){};
        core.ports.heater_gpio.write_pin=[](void* p,std::uint8_t pin,bool high){self(p).pins[pin]=high;};
        core.ports.safety_gpio.context=this;core.ports.safety_gpio.configure_input=[](void*,std::uint8_t){};
        core.ports.safety_gpio.read_pin=[](void* p,std::uint8_t){return self(p).filter_missing?1:0;};
        auto& sensor=core.ports.sensor_bus;sensor.context=this;
        sensor.begin_transmission=[](void*,std::uint8_t){};
        sensor.write=[](void* p,std::uint8_t)->std::size_t{return self(p).sensor_bad_write?0:1;};
        sensor.end_transmission=[](void*,bool)->std::uint8_t{return 0;};
        sensor.request_from=[](void* p,std::uint8_t,std::size_t count,bool)->std::size_t{return self(p).sensor_short?count-1:count;};
        sensor.read=[](void* p){return self(p).sensor_bad_byte?-1:128;};
        sensor.delay_ms=[](void* p,std::uint32_t value){self(p).now+=value;};
        core.ports.display_power=[](void*,bool){return true;};
        z_msp_transport bus{};bus.context=this;
        bus.transmit=[](void* p,std::uint8_t,const std::uint8_t* data,std::size_t size){auto& w=self(p);w.frames.emplace_back(data,data+size);
            if(size>=5&&data[2]==Z_MSP_PUMPS_SET)w.core.msp_state.pumps=data[3];};
        bus.receive=[](void* p,std::uint8_t,std::uint8_t* data,std::size_t size){auto& w=self(p);assert(!w.locks);std::memset(data,0,size);
            if(size==9){data[0]=2;data[1]=w.core.msp_state.pumps;data[2]=1;}};
        bus.delay_ms=[](void* p,std::uint32_t delay){assert(!self(p).locks);self(p).now+=delay;};
        assert(z_msp_init(&core.msp,&core.msp_state,bus,0x40,1000,5,5)==Z_MSP_OK);
        core.msp_state.id=Z_MSP_APP_RUNNING;core.heater.output_pin=23;
        core.runtime.state.id=RuntimeStateId::Standby;core.core.state.id=CoreStateId::Idle;core.safeties.thermal.enabled=true;
        core.auxiliary.pressure_pa=101325;core.auxiliary.pressure_ms=now;core.temperatures.estimated_boil_celsius=100;
        for(auto& t:core.temperatures.temperatures){t.celsius=20;t.updated_ms=now;}
        auto& h=head.services;h.context=this;h.millis=core.ports.millis;h.assertion=d.assertion;h.diagnostics=d;
        h.post_core=[](void* p,const HeadUiCoreEnvelope& command){return self(p).core.post(command);};
        h.alarm=[](void* p,bool){++self(p).alarms;return true;};
        h.check_rinse=[](void*,std::uint32_t,std::uint32_t& result){result=0;return true;};
        h.save_configuration=[](void*){return true;};h.apply_azure_configuration=h.save_configuration;h.discard_remote_session=h.save_configuration;
        h.scan_network=[](void* p,std::uint32_t index,HeadWifiNetwork& out){
            auto& w=self(p);++w.scan_reads;if(index>=w.access_points.size())return false;
            out=w.access_points[index];return true;};
        h.run_display_tests=[](void*){return true;};h.display_test=h.run_display_tests;
        h.random_below=[](void*,std::uint32_t){return std::uint32_t(0);};
        h.scope=[](void*,bool,const char*,const char*,std::uint32_t,const char*){return true;};
        h.register_commands=[](void*,HeadApplication&,const std::array<HeadConsoleDescriptor,6>&){return true;};
        h.display_io.context=this;h.display_io.pin_mode=[](void*,std::uint8_t,std::uint8_t){};
        h.display_io.digital_write=[](void*,std::uint8_t,std::uint8_t){};h.display_io.spi_byte=[](void*,std::uint8_t){};
        h.display_io.spi_bytes=[](void* p,const std::uint8_t*,std::size_t size){assert(size==4096);++self(p).displays;};
        h.display_setup.context=this;h.display_setup.report_information=[](void*){};h.display_setup.register_commands=[](void*){};
        h.display_delay.context=this;h.display_delay.milliseconds=[](void* p,std::uint32_t value){self(p).now+=value;};
        zseries::standalone::Ports ports;ports.context=this;ports.millis=core.ports.millis;ports.storage=storage();ports.group_boot_nonce=0x89ab1257u;ports.still_io=still_io();
        ports.http_poll=[](void* p,std::uint32_t){++self(p).http_polls;};
        ports.serial_poll=[](void*){};ports.save_network=[](void* p,const char*,const char*){return !self(p).reject_network;};ports.reconnect_network=[](void*){return true;};
        ports.lan_ready=[](void* p,std::uint32_t){return self(p).lan;};ports.save_configuration=[](void* p){return !self(p).reject_save;};
        if(wired){
            ports.wired_address=[](void* p){return self(p).wired_ip;};
            ports.apply_interface=[](void* p){++self(p).switches;};
        }
        ports.pair_prompt=[](void* p,std::uint32_t,char code[9]){
            std::strcpy(code,"f82f8f33");return self(p).pending_pair;};
        ports.pair_decide=[](void* p,std::uint32_t,std::uint32_t serial,bool allow){
            auto& w=self(p);
            if(!serial||w.pending_pair!=serial)return false;
            if(allow)++w.approved_pair;else ++w.cancelled_pair;
            w.pending_pair=0;return true;};
        assert(adapter.bind(ports,engine));
        assert(bind_runtime_heater(core.runtime,core.runtime_heater,core.heater,core.temperatures,core.ports.heater_gpio));
        core.heater.synchronization=core.ports.control;
        assert(head.setup()==HeadApplicationResult::Ok);assert(engine.begin().ok());assert(adapter.begin());
        observe();assert(adapter.show_home());
    }
    void observe(){head.observed.core=core.core.state;head.observed.runtime=core.runtime.state;
        head.observed.runtime_inputs=core.runtime.observed;head.observed.session=core.commands.session;
        head.observed.heater=core.heater.state;head.observed.heater_sensors=core.temperatures;
        head.observed.auxiliary=core.auxiliary;head.observed.msp=core.msp_state;
        head.observed.loaded_session_type=core.program.record().type;
        head.observed.loaded_duration=core.program.stats.duration_seconds;
        if(core.program.record().view.name)std::strncpy(head.observed.loaded_recipe.name.data(),core.program.record().view.name,head.observed.loaded_recipe.name.size()-1);
        core.refresh_observed();}
    void head_tick(){observe();const auto result=head.tick(now);if(result!=HeadApplicationResult::Ok){
        std::fprintf(stderr,"Head failure result=%u detail=%u event=%u\n",unsigned(result),head.status.detail,unsigned(head.status.event));std::abort();}}
    bool dispatch(){CoreCommand command;if(!core.core.commands.take(command))return false;
        CoreAction action(CoreEffect::DispatchCoreCommand);action.command=&command;std::uint32_t result=0;return adapter.service_effect(action,result);}
    api::Result command(const char* name){api::Command input;std::strcpy(input.name,name);return adapter.execute(input);}
    std::string request(PicoHttpMethod method,const char* path,const char* body,unsigned expected){
        PicoHttpRequest request{};PicoHttpResponse response{};char arena[4096]{};
        request.method=method;request.auth_configured=request.authenticated=true;std::strcpy(request.path,path);
        if(body){request.body_length=std::strlen(body);assert(request.body_length<sizeof arena);std::memcpy(arena,body,request.body_length);}
        request.body=arena;
        assert(adapter.picostill_http(request,response,arena,sizeof arena)||adapter.group_http(request,response,arena,sizeof arena)||engine.handle(request,response,arena,sizeof arena));
        if(response.status!=expected){std::fprintf(stderr,"HTTP %s got%u expected%u: %.*s\n",path,response.status,expected,int(response.body_length),response.body);std::abort();}
        return std::string(reinterpret_cast<const char*>(response.body),response.body_length);
    }
};
api::Recipe recipe(){api::Recipe r;std::strcpy(r.id,"native");std::strcpy(r.name,"Owned recipe");r.step_count=2;
    for(unsigned i=0;i<2;++i){std::strcpy(r.steps[i].name,i?"Unheated":"Mash");r.steps[i].location=i+1;r.steps[i].target_f=i?0:152;r.steps[i].minutes=1;}
    return r;
}
void owned_recipe(){World w;auto r=recipe();assert(w.engine.recipe_store(r).ok());
    api::Command input;std::strcpy(input.name,"brew");input.recipe=&r;assert(w.adapter.execute(input).status==202);
    r=api::Recipe{};w.adapter.poll();assert(w.core.core.commands.pending);
    const auto& view=w.core.program.record().view;assert(std::strcmp(view.name,"Owned recipe")==0);
    bool unheated=false;for(std::size_t i=0;i<view.code_size;++i)if(view.code[i].opcode==OpCode::StepName&&view.code[i].argument==2){
        assert(i+1<view.code_size&&view.code[i+1].opcode==OpCode::NoTarget);unheated=true;}
    assert(unheated);const auto before=w.writes;assert(w.dispatch());assert(w.writes>before);
    assert(w.core.core.state.id==CoreStateId::Starting&&w.core.core.state.program_id==24);
    assert(w.core.core.state.session_mode==SessionMode::Offline&&w.core.core.state.runtime_start_pending);
    assert(w.core.commands.session.parameters.water.value==0&&w.core.commands.session.stats.duration_seconds>0);
    w.engine.poll();auto sessions=w.request(PICO_HTTP_GET,"/api/v1/history",nullptr,200);assert(sessions.find("Owned recipe")!=std::string::npos);
}
void maker_program() {
    World world;
    const char* path = "/api/v1/recipes/maker";
    char source[] = R"({"Name":"Adjunct run","IsBeer":false,"UsePicoPakMode":true,"IsMetric":false,"Steps":[{"Name":"Adjunct four","StepType":1,"Time":86400,"Location":6,"Drain":47,"Temp":67.125},{"Name":"Wait for me","StepType":2,"Time":17}]})";
    const std::string put = std::string("{\"create_only\":true,\"z_program\":") + source + "}";
    assert(world.request(PICO_HTTP_GET, "/api/v1", nullptr, 200).find("\"z_program_maker\":true") != std::string::npos);
    const auto result = world.request(PICO_HTTP_PUT, path, put.c_str(), 201);
    assert(result.find("\"Temp\":67.125") != std::string::npos);
    assert(result.find("\"IsBeer\":false,\"UsePicoPakMode\":true,\"IsMetric\":false") != std::string::npos);
    assert(result.find("\"StepType\":2,\"Time\":17}") != std::string::npos);
    world.request(PICO_HTTP_PUT, path, put.c_str(), 409);
    const std::string incompatible = std::string("{\"steps\":[],\"z_program\":") + source + "}";
    world.request(PICO_HTTP_PUT, path, incompatible.c_str(), 400);
    const std::string invalid = R"({"z_program":{"Name":"X","IsBeer":true,"UsePicoPakMode":false,"Steps":[{"Name":"Pause","StepType":2,"Location":6}]}})";
    world.request(PICO_HTTP_PUT, path, invalid.c_str(), 400);
    auto stored = world.files;
    World reopened(&stored);
    assert(reopened.request(PICO_HTTP_GET, path, nullptr, 200) == result);
    // Compare actual recovered parser/compiler instructions with the program
    // chosen through the standard standalone recipe/start API.
    ProgramBuffer original;
    assert(parse_local_recipe(original, source, std::strlen(source), RecipeEnvironment(101325,100)).ready);
    reopened.request(PICO_HTTP_POST, "/api/v1/commands", R"({"command":"brew","recipe_id":"maker"})", 202);
    reopened.adapter.poll();
    const auto& native = reopened.core.program.record().view;
    const auto& expected = original.record().view;
    assert(native.code_size == expected.code_size && native.string_count == expected.string_count);
    for (std::size_t i = 0; i < native.code_size; ++i)
        assert(native.code[i].opcode == expected.code[i].opcode && native.code[i].argument == expected.code[i].argument);
    for (std::size_t i = 0; i < native.string_count; ++i)
        assert(std::strcmp(native.strings[i], expected.strings[i]) == 0);
    assert(reopened.dispatch());
    zseries::standalone::BrewRecovery restart;
    assert(restart.load(reopened.storage()).ok() && restart.present());
    const auto& checkpoint = restart.recipe();
    assert(checkpoint.z_program && !checkpoint.is_beer && checkpoint.use_pico_pak);
    assert(checkpoint.steps[0].z.target_c == 67.125 && checkpoint.steps[0].z.seconds == 86400 &&
           checkpoint.steps[0].z.drain_seconds == 47 && checkpoint.steps[1].z.pause);

    const char* nullable = R"({"z_program":{"Name":"Cold","IsBeer":true,"UsePicoPakMode":false,"Steps":[{"Name":"Cold","StepType":1,"Time":10,"Drain":0,"Location":1,"Temp":null},{"Name":"Wait","StepType":2}]}})";
    const auto cold = world.request(PICO_HTTP_PUT, path, nullable, 200);
    assert(cold.find("\"Temp\":null") != std::string::npos && cold.find("\"StepType\":2}") != std::string::npos);
    api::Recipe r;
    assert(world.engine.recipe_get("maker", r).ok() && world.adapter.load_recipe(r, false).ok());
    const auto& instructions = world.core.program.record().view;
    assert(instructions.code[2].opcode == OpCode::NoTarget);
}

void storage_preflight(){World w;auto r=recipe();assert(w.adapter.load_recipe(r,true).ok());const auto frames=w.frames.size();
    w.fail_write=1;assert(w.dispatch());assert(w.core.core.state.id==CoreStateId::Idle&&!w.core.core.state.runtime_start_pending);
    assert(w.frames.size()==frames);assert(w.adapter.show_home());w.core.runtime_commands.pending=false;
    assert(w.adapter.load_recipe(r,true).ok());assert(w.dispatch());assert(w.core.core.state.id==CoreStateId::Idle&&!w.engine.persistence_ready());
}
void no_target(){World w;w.core.runtime.state.pid_active=true;w.core.heater.state.started=true;w.core.heater.state.enabled=true;
    assert(w.core.runtime.dispatch({OpCode::NoTarget,0})==RuntimeResult::Ok);Instruction next;assert(w.core.runtime.stack.pop(next)==RuntimeResult::Ok&&next.opcode==OpCode::StopPid);
    assert(w.core.runtime.dispatch(next)==RuntimeResult::Ok);assert(!w.core.runtime.state.pid_active&&!w.core.heater.state.started);
}
void stop_guard(){World w;w.core.heater.state.enabled=w.core.heater.state.started=true;w.core.heater.pid.automatic=true;w.core.heater.countdown=100;
    w.core.msp_state.pumps=0x70;w.core.msp_state.moving_valve=1;
    assert(w.command("stop").status==202&&w.adapter.stopped());assert(!w.pins[19]&&!w.pins[23]);assert(w.core.msp_state.pumps==0);
    assert(!w.core.heater.state.enabled&&!w.core.heater.state.started&&!w.core.heater.pid.automatic&&!w.core.heater.countdown);
    w.core.ports.heater_gpio.write_pin(w.core.ports.heater_gpio.context,19,true);w.core.ports.heater_gpio.write_pin(w.core.ports.heater_gpio.context,23,true);
    assert(!w.pins[19]&&!w.pins[23]);std::uint8_t frame[6];auto size=z_msp_app_frame(frame,sizeof frame,Z_MSP_PUMPS_SET,0x70,1);
    w.core.msp.transport.transmit(w.core.msp.transport.context,0x40,frame,size);assert(w.frames.back()[2]==Z_MSP_PUMPS_SET&&w.frames.back()[3]==0);
    assert(w.command("sous-vide").status==409);w.core.runtime_commands.pending=false;w.core.runtime.state.id=RuntimeStateId::Standby;
    assert(w.command("clear-stop").status==202&&!w.adapter.stopped());assert(!w.pins[19]&&!w.pins[23]);
    w.core.runtime.filter_required=true;w.filter_missing=true;
    assert(w.adapter.service_stage(CoreTickStage::Safeties,w.now)&&w.adapter.stopped());
    w.core.runtime_commands.pending=false;assert(w.command("clear-stop").status==409);
    w.filter_missing=false;assert(w.adapter.service_stage(CoreTickStage::Safeties,w.now));assert(w.command("clear-stop").status==202);
}
void input_order(){World w;assert(w.head.encoder.state.menu.count==5&&w.head.encoder.state.menu.selected==0);
    api::Command input;std::strcpy(input.name,"encoder");input.delta=1;input.click=true;assert(w.adapter.execute(input).status==202);
    assert(w.head.encoder_events.pending&&!w.head.button_events.pending);assert(w.adapter.execute(input).status==409);
    w.adapter.poll();assert(!w.head.button_events.pending);w.head_tick();assert(w.head.encoder.state.menu.selected==1);
    w.adapter.poll();assert(w.head.button_events.pending);w.head_tick();assert(w.head.ui_events.pending);
    assert(w.head.ui_events.value.command==UiCommand::SousVideMenu);w.head_tick();
    const auto& options=w.head.screens.menu_data;
    assert(options.count==3&&options.items[0].event.command==UiCommand::StartSousVideWithKeg);
    assert(options.items[1].event.command==UiCommand::StartSousVideWithoutKeg&&options.items[2].event.command==UiCommand::Back);
    assert(!w.core.core.commands.pending);
    assert(w.adapter.show_home());w.head.ui.transient.active=true;w.head.ui.transient.deadline=w.now+1000;
    input.delta=0;input.click=true;assert(w.adapter.execute(input).status==202);w.head_tick();w.head_tick();
    assert(!w.head.ui.transient.active&&!w.head.ui.transient.deadline&&w.alarms>0);
    assert(w.head.screens.menu_data.count==1); // Empty catalogue retains Back.
}
void sous_vide_programs(){
    for(bool keg:{false,true}){
        World w;
        const auto name=keg?"sous-vide":"sous-vide-no-keg";
        assert(w.request(PICO_HTTP_GET,"/api/v1",nullptr,200).find(name)!=std::string::npos);
        assert(w.command(name).status==202);w.adapter.poll();
        assert(w.head.ui_events.pending&&w.head.ui_events.value.command==(keg?UiCommand::StartSousVideWithKeg:UiCommand::StartSousVideWithoutKeg));
        w.head_tick();assert(w.core.core.commands.pending);
        assert(w.core.core.commands.value.program_id==(keg?6:5));
        assert(w.dispatch());assert(w.core.core.state.id==CoreStateId::Starting);
        assert(w.core.core.state.program_id==(keg?6:5));
    }
}
void offline_and_reservation(){World w;w.core.core.state.id=CoreStateId::Booting;w.core.core.state.state_date=1;w.core.core.state.azure_enabled=true;
    assert(w.adapter.service_stage(CoreTickStage::Azure,w.now));assert(w.core.core.state.id==CoreStateId::Idle&&w.core.core.state.offline&&!w.core.core.state.azure_enabled);
    w.core.runtime_commands.pending=false;auto r=recipe();assert(w.adapter.load_recipe(r,true).ok());assert(w.dispatch());
    CoreCommand cancel;cancel.id=CoreCommandId::Stop;assert(w.core.post(cancel));assert(w.dispatch());w.adapter.poll();
    assert(w.core.core.state.id==CoreStateId::Idle);w.core.runtime_commands.pending=false;assert(w.adapter.show_home());
    assert(w.adapter.load_recipe(r,true).ok());assert(w.dispatch());w.engine.poll();
    const auto before=w.writes;w.core.core.state.id=CoreStateId::Running;CoreCommand duplicate;duplicate.id=CoreCommandId::StartProgram;duplicate.program_id=24;
    assert(w.core.post(duplicate));assert(w.dispatch());assert(w.writes==before&&w.core.core.state.id==CoreStateId::Running);
}
void settings_and_display(){World w;
    // Exercise the production board settings backend on the protected layout (no EEPROM partition).
    zseries::standalone::BoardSettings board(w.storage());
    assert(board.load()&&!board.wired());
    EepromData original;original.use_ethernet=1;original.boiler=BoilerType::Small;
    auto bytes=encode_eeprom(original);
    w.files["zboard.seed"]={bytes.begin(),bytes.end()};
    assert(board.load()&&board.wired());
    auto board_io=board.backend();
    original.use_ethernet=0;auto wireless=encode_eeprom(original);
    board_io.write_data(board_io.context,wireless);assert(board_io.commit(board_io.context));
    zseries::standalone::BoardSettings reread(w.storage());assert(reread.load()&&!reread.wired());
    auto io=reread.backend();EepromBytes actual;io.read_data(io.context,actual);assert(decode_eeprom(actual).boiler==BoilerType::Small);
    w.fail_write=1;io.write_data(io.context,bytes);assert(!io.commit(io.context));assert(!reread.wired());w.fail_write=0;
    World broken;broken.files["zboard.seed"]={0xff,0xff};zseries::standalone::BoardSettings bad(broken.storage());assert(!bad.load());
    auto output=w.request(PICO_HTTP_PATCH,"/api/v1/settings","{\"temperature_unit\":\"C\",\"language\":\"fr\",\"detailed_status\":true,\"auto_cycle\":false}",200);
    assert(!w.core.eeprom.data.use_imperial&&w.core.eeprom.data.language==LanguageType::French&&w.core.eeprom.data.show_detail);
    assert(w.core.eeprom.data.slideshow_cycle_static&&output.find("\"auto_cycle\":false")!=std::string::npos);
    HeadUiEvent setting;setting.command=UiCommand::Settings;assert(w.head.receive_ui(setting));w.head_tick();
    bool cycle=false;for(std::size_t i=0;i<w.head.screens.menu_data.count;++i)
        if(w.head.screens.menu_data.items[i].event.command==UiCommand::ConfigureSlideshowCycle)cycle=true;
    assert(cycle);
    setting.command=UiCommand::SetAutoCycle;assert(w.head.receive_ui(setting));w.head_tick();
    assert(!w.core.eeprom.data.slideshow_cycle_static);
    auto services=w.adapter.services();api::Display first;assert(services.display(services.context,first).ok());assert(first.total_bytes==4096&&first.width==128&&first.height==64);
    std::uint8_t page[1024];assert(services.read_display(services.context,first.revision,1024,page,sizeof page).ok());
    auto old=page[0];w.head.display.pixels[1024]^=0xff;assert(services.read_display(services.context,first.revision,1024,page,sizeof page).ok()&&page[0]==old);
    api::Display second;assert(services.display(services.context,second).ok()&&first.revision!=second.revision);
    assert(services.read_display(services.context,first.revision,0,page,sizeof page).status==409);
    assert(services.read_display(services.context,second.revision,1024,page,sizeof page).ok()&&page[0]==std::uint8_t(old^0xff));
    auto metadata=w.request(PICO_HTTP_GET,"/api/v1/display",nullptr,200);assert(metadata.find("row-gray4-msb-hex")!=std::string::npos);
}
void status_fields(){World w;api::Snapshot s;assert(w.adapter.snapshot(s).ok());assert(s.ready&&!s.busy&&s.sensor_count==5&&s.actuator_count==4);
    assert(std::fabs(s.temperature_f.value-68)<1e-9&&std::fabs(s.pressure_kpa.value-101.325)<1e-9);
    assert(s.target_f.valid&&s.target_f.value==0);
    w.core.heater.state.target_celsius=65;w.core.heater.state.started=true;
    assert(w.adapter.snapshot(s).ok()&&s.target_f.valid&&s.target_f.value==149);
    w.core.heater.state.started=false;
    assert(w.adapter.snapshot(s).ok()&&s.target_f.valid&&s.target_f.value==0);
    auto status=w.request(PICO_HTTP_GET,"/api/v1/status",nullptr,200);assert(status.find("z-series")!=std::string::npos&&status.find("ambient-pressure")!=std::string::npos);
    w.now+=6000;assert(w.adapter.snapshot(s).ok()&&!s.temperature_f.valid&&!s.pressure_kpa.valid);
    w.core.system_error=true;assert(w.adapter.snapshot(s).ok()&&!s.ready&&!s.target_f.valid);
}
void running_hold(World& w,bool heated=true){
    const auto r=recipe();assert(w.adapter.load_recipe(r,true).ok());assert(w.dispatch());
    auto& runtime=w.core.runtime;runtime.program=w.core.program.record().view;runtime.stack.clear();
    bool tail=false;
    std::vector<Instruction> remaining;
    for(std::size_t i=0;i<runtime.program.code_size;++i){const auto instruction=runtime.program.code[i];
        if(tail)remaining.push_back(instruction);
        if(instruction.opcode==OpCode::HoldFor&&!tail)tail=true;
    }
    assert(tail);for(auto i=remaining.rbegin();i!=remaining.rend();++i)assert(runtime.stack.push(*i)==RuntimeResult::Ok);
    w.now=50000;runtime.state.id=RuntimeStateId::Running;runtime.state.instruction={OpCode::WaitFor,60};
    runtime.state.instruction_date=w.now-17000;runtime.state.instruction_count=10;runtime.state.elapsed_seconds=123;
    runtime.state.flags=0x210;runtime.state.pid_active=heated;runtime.state.step_name=runtime.program.strings[1];
    runtime.saved_step_reference=1;runtime.saved_substep_reference=heated?0x800b:0x800e;
    runtime.requested_pumps=heated?0x70:0x60;runtime.requested_valve=2;runtime.filter_required=true;
    w.core.heater.state.target_celsius=heated?66.5:0;w.core.heater.state.started=heated;
    w.core.core.state.id=CoreStateId::Running;w.adapter.poll();
}
void dedicated_session_controls(){
    World w;auto& runtime=w.core.runtime;
    auto availability=[&](bool pause,bool resume){
        const auto json=w.request(PICO_HTTP_GET,"/api/v1/status",nullptr,200);
        assert(json.find(pause?"\"can_pause\":true":"\"can_pause\":false")!=std::string::npos);
        assert(json.find(resume?"\"can_resume\":true":"\"can_resume\":false")!=std::string::npos);
    };
    availability(false,false);
    running_hold(w);
    availability(true,false);
    assert(w.command("resume").status==409);
    assert(w.command("pause").status==202);
    assert(w.command("pause").status==409);
    // A fill/setup prompt can arrive after HTTP accepts but before Runtime acts.
    runtime.state.screen=9;
    assert(w.adapter.service_stage(CoreTickStage::Runtime,w.now));
    assert(!runtime.state.paused);
    availability(false,false);
    runtime.state.screen=0;
    assert(w.command("pause").status==202);
    assert(w.adapter.service_stage(CoreTickStage::Runtime,w.now));
    for(unsigned i=0;i<60&&runtime.state.instruction.opcode!=OpCode::WaitUser;++i)
        assert(w.adapter.service_stage(CoreTickStage::Runtime,++w.now));
    assert(runtime.state.instruction.opcode==OpCode::WaitUser&&runtime.state.pause_reason==unsigned(PauseReason::User));
    availability(false,true);
    assert(w.command("pause").status==409);
    for(auto reason:{PauseReason::StepFilter,PauseReason::PicoStillError,PauseReason::StillT2Overtemp}){
        runtime.state.pause_reason=unsigned(reason);
        availability(false,false);assert(w.command("resume").status==409);
    }
    runtime.state.pause_reason=unsigned(PauseReason::User);w.core.msp_state.errors=4;
    assert(w.command("resume").status==409&&w.core.msp_state.errors==4);
    w.core.msp_state.errors=0;
    RuntimeCommand other;other.id=RuntimeCommandId::Stop;
    assert(w.core.runtime_commands.put(other));
    assert(w.command("resume").status==409&&w.core.runtime_commands.value.id==RuntimeCommandId::Stop);
    w.core.runtime_commands.pending=false;
    assert(w.command("resume").status==202);
    assert(w.command("resume").status==409);
    assert(w.adapter.service_stage(CoreTickStage::Runtime,++w.now));
    assert(runtime.state.instruction.opcode!=OpCode::WaitUser);
}
void recovery_restart(){World before;running_hold(before);
    auto persisted=before.files;World after(&persisted);api::Snapshot snapshot;assert(after.adapter.snapshot(snapshot).ok());
    assert(snapshot.recovery.supported&&snapshot.recovery.valid&&snapshot.recovery.available);
    assert(snapshot.recovery.step_seconds==43&&snapshot.recovery.elapsed_seconds==123&&snapshot.recovery.current_step==0);
    assert(std::strcmp(snapshot.recovery.recipe_name,"Owned recipe")==0);
    assert(!after.pins[19]&&!after.pins[23]&&!after.core.msp_state.pumps);
    assert(after.adapter.load_recipe(recipe(),true).status==409);
    api::Command resume;std::strcpy(resume.name,"resume-interrupted");assert(after.adapter.execute(resume).status==400);
    resume.confirm=true;after.core.safeties.filter_missing=true;assert(after.adapter.execute(resume).status==409);
    after.core.safeties.filter_missing=false;assert(after.adapter.execute(resume).status==202);assert(after.dispatch());
    assert(after.core.core.state.id==CoreStateId::Starting&&after.core.program.parameters.resumed_session);
    assert(after.core.commands.session.parameters.water.value==0);
    const auto& program=after.core.program.record().view;bool heat=false,wait=false;
    for(std::size_t i=0;i<program.code_size;++i){if(program.code[i].opcode==OpCode::ReachTarget)heat=true;
        if(program.code[i].opcode==OpCode::WaitFor&&program.code[i].argument==43){assert(heat);wait=true;}}
    assert(wait);assert(std::strcmp(program.strings[1],"Mash")==0);
    // Execute the restored program's real expansion and heater callbacks. A
    // cooled vessel reaches a temperature wait before its saved hold can run.
    auto& runtime=after.core.runtime;runtime.program=program;runtime.stack.clear();assert(runtime.stack.load(program)==RuntimeResult::Ok);
    runtime.observed.wort_temperature=20;runtime.state.id=RuntimeStateId::Running;
    bool waiting_for_heat=false;
    for(unsigned budget=0;budget<80&&runtime.stack.size();++budget){Instruction next;assert(runtime.stack.pop(next)==RuntimeResult::Ok);
        const auto result=runtime.dispatch(next);
        if(result!=RuntimeResult::Ok){std::fprintf(stderr,"Restored dispatch op=%u arg=%u result=%u line=%u controller=%d\n",unsigned(next.opcode),next.argument,unsigned(result),runtime.assertion_line,runtime.controller_result);std::abort();}
        // The real foreground loop waits for MSP motion completion before the
        // next pump instruction. This test's explicit hardware boundary reports
        // that completion without simulating motor mechanics.
        if(next.opcode==OpCode::MspValve)after.core.msp_state.moving_valve=0;
        if(next.opcode==OpCode::WaitTempA){waiting_for_heat=true;break;}
        assert(next.opcode!=OpCode::WaitFor);
    }
    assert(waiting_for_heat&&after.core.heater.state.started&&std::fabs(after.core.heater.state.target_celsius-66.5)<0.01);
}
void recovery_unheated_and_pause(){World before;running_hold(before,false);World after(&before.files);
    api::Command resume;std::strcpy(resume.name,"resume-interrupted");resume.confirm=true;assert(after.adapter.execute(resume).status==202);
    bool wait=false;
    for(std::size_t i=0;i<after.core.program.record().view.code_size;++i){const auto op=after.core.program.record().view.code[i];
        if(op.opcode==OpCode::WaitFor&&op.argument==43){wait=true;break;}
        assert(op.opcode!=OpCode::StartPid&&op.opcode!=OpCode::ReachTarget);}
    assert(wait);
    World paused;running_hold(paused);auto& runtime=paused.core.runtime;
    assert(runtime.push_resume(paused.now,false)==RuntimeResult::Ok);assert(runtime.dispatch({OpCode::Pause,2})==RuntimeResult::Ok);
    Instruction next;assert(runtime.stack.pop(next)==RuntimeResult::Ok&&next.opcode==OpCode::WaitUser);assert(runtime.dispatch(next)==RuntimeResult::Ok);
    paused.adapter.poll();World rebooted(&paused.files);assert(rebooted.adapter.execute(resume).status==202);
    bool acknowledged=false;
    for(std::size_t i=0;i<rebooted.core.program.record().view.code_size;++i){const auto op=rebooted.core.program.record().view.code[i];
        if(op.opcode==OpCode::Pause&&op.argument==2)acknowledged=true;
        if(op.opcode==OpCode::StartPid||op.opcode==OpCode::ReachTarget){assert(acknowledged);break;}}
    assert(acknowledged);
}
void recovery_corruption_and_discard(){World before;running_hold(before);auto persisted=before.files;
    bool corrupted=false;for(auto& file:persisted)if(file.first.find("z-recovery.")==0){file.second.back()^=1;corrupted=true;break;}assert(corrupted);
    World after(&persisted);api::Snapshot snapshot;assert(after.adapter.snapshot(snapshot).ok()&&!snapshot.recovery.valid&&!snapshot.recovery.available);
    assert(after.adapter.load_recipe(recipe(),true).status==503);assert(after.command("discard-interrupted").status==202);
    World cleared(&after.files);assert(cleared.adapter.snapshot(snapshot).ok()&&snapshot.recovery.valid&&!snapshot.recovery.available);
    assert(cleared.adapter.load_recipe(recipe(),true).ok());
}
void recovery_failure_and_terminal_state(){World before;running_hold(before);before.fail_write=1;before.now+=30000;before.adapter.poll();
    assert(before.adapter.stopped()&&!before.pins[19]&&!before.pins[23]&&!before.core.msp_state.pumps);
    World recovered(&before.files);api::Snapshot snapshot;assert(recovered.adapter.snapshot(snapshot).ok()&&snapshot.recovery.available);
    assert(recovered.command("stop").status==202);World stopped(&recovered.files);assert(stopped.adapter.snapshot(snapshot).ok()&&!snapshot.recovery.available);
    World complete;running_hold(complete);complete.core.runtime.state.screen=14;complete.adapter.poll();
    World finished(&complete.files);assert(finished.adapter.snapshot(snapshot).ok()&&!snapshot.recovery.available);
}
void sensor_fault_cutoff(){
    for(unsigned kind=0;kind<3;++kind){World w;w.core.heater.state.enabled=w.core.heater.state.started=true;w.core.heater.countdown=100;
        w.core.msp_state.pumps=0x70;auto& sensor=w.core.ports.sensor_bus;
        if(kind==0){w.sensor_short=true;assert(sensor.request_from(sensor.context,0x34,10,true)==9);}
        else if(kind==1){w.sensor_bad_byte=true;assert(sensor.read(sensor.context)==-1);}
        else {w.sensor_bad_write=true;assert(sensor.write(sensor.context,0x80)==0);}
        assert(w.adapter.stopped()&&w.core.system_error&&!w.pins[19]&&!w.pins[23]&&!w.core.msp_state.pumps);
        assert(!w.core.heater.state.started&&!w.core.heater.countdown);
        assert(sensor.request_from(sensor.context,0x34,10,true)==0&&sensor.read(sensor.context)==-1);
        api::Snapshot snapshot;assert(w.adapter.snapshot(snapshot).ok()&&!snapshot.ready);
        assert(w.command("clear-stop").status==409);
    }
}
void buffered_coffee(){
    {World w;
        w.request(PICO_HTTP_POST,"/api/v1/commands",R"({"command":"coffee"})",202);
        w.adapter.poll();w.head_tick();
        const auto& menu=w.head.screens.menu_data;
        for(auto mode:{UiCommand::CoffeeColdBrewTweak,UiCommand::CoffeeHotTweak,UiCommand::CoffeeHybridTweak,UiCommand::CoffeeCustomTweak}){
            bool present=false;for(unsigned n=0;n<menu.count;++n)present|=menu.items[n].event.command==mode;
            assert(present);
        }
        assert(w.core.core.state.id==CoreStateId::Idle&&!w.core.core.commands.pending);
    }
    for(unsigned previous=0;previous<2;++previous){World w;
        if(previous)assert(w.adapter.load_recipe(recipe(),false).ok());
        w.head.core_events.pending=false;
        CoreCommand coffee;coffee.id=CoreCommandId::CoffeeProgramLoad;
        coffee.program_parameters.id=ZProgramId::CoffeeColdBrew;
        coffee.program_parameters.water={11.36,Unit::Liter};
        assert(w.core.post(coffee)&&w.dispatch());assert(w.core.program.record().type==SessionType::Coffee);
        w.head_tick();
        HeadUiEvent run;run.command=static_cast<UiCommand>(46);assert(w.head.receive_ui(run));w.head_tick();
        assert(w.core.core.commands.pending&&w.core.core.commands.value.program_id==24);
        assert(w.dispatch());assert(w.core.core.state.id==CoreStateId::Starting&&w.core.core.state.runtime_start_pending);
        w.engine.poll();const auto sessions=w.request(PICO_HTTP_GET,"/api/v1/history",nullptr,200);
        assert(sessions.find("Cold Brew")!=std::string::npos&&sessions.find("\"command\":\"program\"")!=std::string::npos);
        api::Snapshot snapshot;assert(w.adapter.snapshot(snapshot).ok()&&snapshot.recording&&snapshot.step_count==0);
        World rebooted(&w.files);assert(rebooted.adapter.snapshot(snapshot).ok()&&!snapshot.recovery.available);
    }
}
void standby_remote_start(){
    World w;assert(w.engine.recipe_store(recipe()).ok());
    assert(w.core.core.transition(CoreStateId::StandBy)==CoreFlowResult::Ok);
    assert(w.adapter.service_stage(CoreTickStage::Runtime,w.now));w.head_tick();
    api::Snapshot before;assert(w.adapter.snapshot(before).ok()&&before.ready&&!before.busy);
    w.request(PICO_HTTP_POST,"/api/v1/commands",R"({"command":"brew","recipe_id":"native"})",202);
    w.adapter.poll();
    for(unsigned i=0;i<4&&!w.core.core.commands.pending;++i)w.head_tick();
    assert(w.core.core.state.id==CoreStateId::Idle&&w.core.core.commands.pending);
    assert(w.dispatch());assert(w.core.core.state.id==CoreStateId::Starting);
}
void recipe_menu_and_retired_manual_command() {
    World w;
    const auto& menu = w.head.screens.menu_data;
    assert(menu.count && std::strcmp(menu.items[0].text.data(), "Brew Recipe") == 0);
    for (unsigned i = 0; i < menu.count; ++i) {
        assert(menu.items[i].event.command != UiCommand::StartManualBrew);
    }
    const auto caps = w.request(PICO_HTTP_GET, "/api/v1", nullptr, 200);
    assert(caps.find("\"brew\"") != std::string::npos);
    assert(caps.find("\"manual\"") == std::string::npos);
    const auto rejected = w.request(PICO_HTTP_POST, "/api/v1/commands", R"({"command":"manual"})", 400);
    assert(rejected.find("\"invalid_command\"") != std::string::npos);
    assert(!w.core.core.commands.pending && w.core.core.state.id == CoreStateId::Idle);

    // A queued native event can still carry the original program ID.
    CoreCommand stale;
    stale.id = CoreCommandId::StartProgram;
    stale.program_id = static_cast<std::uint32_t>(ZProgramId::ManualBrew);
    const auto writes = w.writes;
    assert(w.core.post(stale) && w.dispatch());
    assert(w.core.core.state.id == CoreStateId::Idle && !w.core.core.state.runtime_start_pending);
    assert(w.writes == writes);

    assert(w.engine.recipe_store(recipe()).ok());
    assert(w.head.receive_ui(menu.items[0].event));
    w.head_tick();
    assert(menu.count == 2 && std::strcmp(menu.items[0].text.data(), "Owned recipe") == 0);
}

void recipe_step_indices(){World w;assert(w.adapter.load_recipe(recipe(),true).ok()&&w.dispatch());
    auto& runtime=w.core.runtime;runtime.program=w.core.program.record().view;
    assert(runtime.dispatch({OpCode::StepName,0x8002})==RuntimeResult::Ok);
    assert(runtime.dispatch({OpCode::StepName,0x8003})==RuntimeResult::Ok);
    assert(runtime.dispatch({OpCode::StepName,1})==RuntimeResult::Ok);
    api::Snapshot snapshot;assert(w.adapter.snapshot(snapshot).ok()&&snapshot.current_step==0&&snapshot.step_count==2);
    // Resuming the same step repeats its label in the native instruction stream.
    assert(runtime.dispatch({OpCode::StepName,1})==RuntimeResult::Ok);
    assert(w.adapter.snapshot(snapshot).ok()&&snapshot.current_step==0);
    assert(runtime.dispatch({OpCode::StepName,2})==RuntimeResult::Ok);
    assert(w.adapter.snapshot(snapshot).ok()&&snapshot.current_step==1);
    assert(runtime.dispatch({OpCode::StepName,0x8007})==RuntimeResult::Ok);
    assert(w.adapter.snapshot(snapshot).ok()&&snapshot.current_step==-1);
}
void physical_pairing(){
    World w;
    auto& menu=w.head.screens.menu_data;
    auto select=[&](const char* label){
        for(unsigned i=0;i<menu.count;++i){
            if(std::strcmp(menu.items[i].text.data(),label))continue;
            assert(w.head.receive_ui(menu.items[i].event));w.head_tick();return;
        }
        assert(!"Menu action missing");
    };
    auto press=[&](){
        CoreButtonEvent button;button.id=1;button.argument=1;
        assert(w.head.receive_button(button));w.head_tick();w.head_tick();
    };
    HeadUiEvent settings;settings.command=UiCommand::Settings;
    assert(w.head.receive_ui(settings));w.head_tick();select("About");select("Pair with Hub");
    assert(menu.count==1);w.pending_pair=17;w.adapter.poll();
    assert(menu.count==2&&w.head.encoder.state.menu.selected==0);
    api::Command remote;std::strcpy(remote.name,"encoder");remote.delta=1;remote.click=true;
    assert(w.adapter.execute(remote).status==409);press();
    assert(!w.approved_pair&&w.cancelled_pair==1);
    w.pending_pair=18;select("Pair with Hub");
    assert(w.head.receive_encoder(-1));w.head_tick();
    assert(w.head.encoder.state.menu.selected==1&&w.adapter.execute(remote).status==409);
    press();assert(w.approved_pair==1&&w.cancelled_pair==1);
    w.pending_pair=19;select("Pair with Hub");
    const auto stale=menu.items[1].event;
    w.pending_pair=20;w.adapter.poll();
    assert(w.head.encoder.state.menu.selected==0);
    assert(w.head.receive_ui(stale));w.head_tick();
    assert(menu.count==2&&w.approved_pair==1&&w.pending_pair==20);
    w.pending_pair=0;w.adapter.poll();assert(menu.count==1);press();
    select("Machine details");  // Original About presentation still reachable.
}
void native_network_menus(){World w;
    const auto check=[&](){assert(w.head.screens.menu_data.count==4);
        const UiCommand expected[]={UiCommand::WiFiScan,UiCommand::WiFiReconnect,UiCommand::WiFiDetails,UiCommand::Exit};
        for(unsigned i=0;i<4;++i)assert(w.head.screens.menu_data.items[i].event.command==expected[i]);};
    assert(w.core.core.transition(CoreStateId::NeedsNetwork)==CoreFlowResult::Ok);w.head_tick();check();
    assert(w.head.synchronize()==HeadApplicationResult::Ok);check();
    assert(w.core.core.transition(CoreStateId::Idle)==CoreFlowResult::Ok);w.head_tick();
    HeadUiEvent event;event.command=UiCommand::ConfigureNetwork;assert(w.head.receive_ui(event));w.head_tick();check();
}
void stable_wifi_picker_and_local_lease(){
    World w;
    auto& wifi=w.core.wifi;
    wifi.services.context=&w;
    wifi.services.millis=[](void* p){return World::self(p).now;};
    wifi.services.perform=[](void* p,const WifiAction& action,std::uint32_t& value){
        auto& world=World::self(p);value=0;
        switch(action.effect){
        case WifiEffect::Scan:value=static_cast<std::uint32_t>(-1);return true;
        case WifiEffect::ScanComplete:value=static_cast<std::uint32_t>(world.scan_result);return true;
        case WifiEffect::Status:value=world.lan?3:6;return true;
        case WifiEffect::LocalAddress:value=world.lan?0x0200000a:0;return true;
        case WifiEffect::GatewayAddress:value=world.lan?0x0100000a:0;return true;
        case WifiEffect::Rssi:case WifiEffect::ResolveHost:case WifiEffect::EmitEvent:return true;
        default:assert(false);return false;
        }
    };
    wifi.state.id=WiFiStateId::Connecting;wifi.target=WiFiStateId::Connected;
    std::strcpy(wifi.state.requested.ssid.data(),"saved");
    w.lan=true;
    assert(w.adapter.service_stage(CoreTickStage::Wifi,w.now));
    assert(wifi.state.id==WiFiStateId::Checking&&!wifi.test_host_resolved);
    assert(w.adapter.service_stage(CoreTickStage::Wifi,w.now));
    assert(wifi.state.id==WiFiStateId::Connected&&wifi.state.local_address==0x0200000a);
    const auto select=[&](UiCommand command){HeadUiEvent event;event.command=command;assert(w.head.receive_ui(event));w.head_tick();};
    select(UiCommand::WiFiScan);
    assert(w.head.screens.menu_data.count==1);
    select(UiCommand::ConfigureNetworkAsBack);assert(w.adapter.service_stage(CoreTickStage::Wifi,w.now));
    assert(w.head.screens.menu_data.count==4&&wifi.target==WiFiStateId::Connected);
    select(UiCommand::WiFiScan);
    const auto add=[&](const char* name,bool encrypted,int signal){
        HeadWifiNetwork ap;std::strcpy(ap.ssid.data(),name);ap.rssi=signal;ap.encrypted=encrypted;w.access_points.push_back(ap);};
    add("mesh",false,-50);add("Other",true,-45);add("mesh",true,-60);add("",false,-30);
    add("MESH",false,-40);add("01234567890123456789012345678901",true,-77);
    add("invalid\tentry",true,-50);
    w.scan_result=static_cast<int>(w.access_points.size());assert(w.adapter.service_stage(CoreTickStage::Wifi,w.now));
    const auto& menu=w.head.screens.menu_data;
    assert(menu.count==5&&menu.items[4].event.command==UiCommand::ConfigureNetworkAsBack);
    assert(w.scan_reads==7&&std::strstr(menu.items[3].text.data(),"01234567890123456789012345678901"));
    const HeadUiEvent selected=menu.items[0].event;
    w.access_points.clear();
    assert(w.head.receive_ui(selected));w.head_tick();
    assert(w.scan_reads==7&&w.head.text.state.awaiting_wifi_password);
    assert(!std::strcmp(w.head.text.state.credentials.ssid.data(),"mesh"));
    assert(wifi.target==WiFiStateId::Connected&&!std::strcmp(wifi.state.connected.ssid.data(),"saved"));
    w.lan=false;select(UiCommand::WiFiDetails);assert(!std::strcmp(w.head.screens.state.title.data(),"Wi-Fi offline"));
    w.lan=true;select(UiCommand::WiFiScan);w.scan_result=0;assert(w.adapter.service_stage(CoreTickStage::Wifi,w.now));
    assert(menu.count==1&&menu.items[0].event.command==UiCommand::ConfigureNetworkAsBack);
    select(UiCommand::ConfigureNetworkAsBack);assert(menu.count==4);
    w.reject_network=true;CoreCommand submitted;submitted.id=CoreCommandId::WiFiConnect;
    std::strcpy(submitted.credentials.ssid.data(),"new");
    std::strcpy(submitted.credentials.password.data(),"short");
    CoreAction join(CoreEffect::WifiConnect);join.credentials=&submitted.credentials;std::uint32_t result=0;
    assert(w.adapter.service_effect(join,result));
    assert(!std::strcmp(wifi.state.requested.ssid.data(),"saved")&&!w.core.system_error);
    assert(menu.count==1&&menu.items[0].event.command==UiCommand::ConfigureNetworkAsBack);
}
void wired_interface_selection(){
    World w(nullptr,true);
    auto select=[&](UiCommand cmd){HeadUiEvent event;event.command=cmd;assert(w.head.receive_ui(event));w.head_tick();};
    auto title=[&](const char* expected){assert(!std::strcmp(w.head.screens.state.title.data(),expected));};
    auto listed=[&](UiCommand cmd){const auto& menu=w.head.screens.menu_data;
        return std::any_of(menu.items.begin(),menu.items.begin()+menu.count,[&](const decltype(menu.items)::value_type& item){return item.event.command==cmd;});};
    select(UiCommand::ConfigureNetwork);title("Network");assert(listed(UiCommand::ConfigureEthernet));
    select(UiCommand::ConfigureEthernet);title("Ethernet");assert(listed(UiCommand::EthernetConnect));
    std::strcpy(w.core.eeprom.data.wifi_ssid.data(),"saved-wifi");
    w.reject_save=true;select(UiCommand::EthernetConnect);title("Save failed");
    assert(!w.core.eeprom.data.use_ethernet&&!w.switches);
    w.reject_save=false;w.core.runtime.state.id=RuntimeStateId::Running;
    select(UiCommand::EthernetConnect);title("Machine busy");assert(!w.switches);
    w.core.runtime.state.id=RuntimeStateId::Standby;
    select(UiCommand::EthernetConnect);title("Waiting for Ethernet");
    assert(w.core.eeprom.data.use_ethernet&&w.switches==1&&!listed(UiCommand::EthernetConnect));
    w.wired_ip=0x2A00000a;assert(w.adapter.tick_wifi(w.now)&&w.core.network==2);
    select(UiCommand::ConfigureEthernet);title("10.0.0.42");
    w.wired_ip=0;assert(w.adapter.tick_wifi(w.now)&&w.core.network==0);
    select(UiCommand::ConfigureNetworkAsBack);title("Network");
    assert(listed(UiCommand::WiFiReconnect)&&!listed(UiCommand::WiFiScan));
    select(UiCommand::WiFiReconnect);assert(!w.core.eeprom.data.use_ethernet&&w.switches==2);
    assert(!std::strcmp(w.core.eeprom.data.wifi_ssid.data(),"saved-wifi"));
    assert(listed(UiCommand::WiFiScan)&&!w.core.system_error);
}
void original_native_state(){
    World w;PicoHttpRequest req{};PicoHttpResponse res{};char arena[1200]{};
    std::strcpy(req.path,"/api/v1/status");
    assert(!w.adapter.native_state_http(req,res,arena,sizeof arena));
    std::strcpy(req.path,"/api/v1/z/state");req.method=PICO_HTTP_GET;
    auto request=[&](unsigned cap){assert(w.adapter.native_state_http(req,res,arena,cap));return res.status;};
    assert(request(sizeof arena)==503);req.auth_configured=true;assert(request(sizeof arena)==401);req.authenticated=true;
    req.method=PICO_HTTP_POST;assert(request(sizeof arena)==405);req.method=PICO_HTTP_GET;
    w.core.network=1;w.core.wifi.state.rssi=-61;w.core.msp_state.valve_position=6;w.core.msp_state.pumps=0x30;
    w.core.runtime.state.flags=1;w.core.runtime.state.pause_reason=2;w.core.runtime.state.elapsed_seconds=67;
    w.core.runtime.state.step_name="Hop one";assert(request(sizeof arena)==200&&res.body_length==std::strlen(arena));
    std::string out(arena);
    for(const char* field:{"\"UsePicoPakMode\":1","\"Location\":3","\"ValvePosition\":6","\"KegPumpOn\":1","\"DrainPumpOn\":1",
                           "\"PauseReason\":2","\"rssi\":-61","\"SecondsEllapsed\":67","\"StepName\":\"Hop one\""})assert(out.find(field)!=std::string::npos);
    assert(request(30)==503);
    std::string oversized(128,'x');w.core.runtime.state.step_name=oversized.c_str();
    assert(request(sizeof arena)==503);w.core.runtime.state.step_name=nullptr;
    assert(request(sizeof arena)==200&&std::string(arena).find("\"StepName\":null")!=std::string::npos);
    w.core.runtime.state.pause_reason=18;assert(request(sizeof arena)==503);
    w.core.runtime.state.pause_reason=0;w.core.network=2;assert(request(sizeof arena)==200);
    assert(std::strstr(arena,"\"rssi\":0"));
}
void rejected_resume_retains_checkpoint(){World before;running_hold(before);World after(&before.files);
    api::Command resume;std::strcpy(resume.name,"resume-interrupted");resume.confirm=true;
    assert(after.adapter.execute(resume).status==202);after.fail_write=1;assert(after.dispatch());
    assert(after.core.core.state.id==CoreStateId::Idle&&!after.core.core.state.runtime_start_pending);
    api::Snapshot snapshot;assert(after.adapter.snapshot(snapshot).ok()&&snapshot.recovery.available&&snapshot.recovery.valid);
    assert(after.command("discard-interrupted").status==202);
}
void recovery_preflight_has_no_diagnostics(){World w;running_hold(w);
    zseries::standalone::BrewRecovery checkpoint;assert(checkpoint.load(w.storage()).ok());
    auto& runtime=w.core.runtime;while(runtime.stack.size()<InstructionStack::capacity)assert(runtime.stack.push({OpCode::Noop,0})==RuntimeResult::Ok);
    const auto assertions=w.assertions,now=w.now;assert(!checkpoint.capture(runtime,66.5,w.now,0).ok());
    assert(w.assertions==assertions&&w.now==now);
    runtime.stack.clear();runtime.state.instruction={static_cast<OpCode>(0xffffffffu),0};
    assert(!checkpoint.capture(runtime,66.5,w.now,0).ok());assert(w.assertions==assertions&&w.now==now);
}
constexpr const char* group_path = "/api/v1/group-brew";
const char* rinse_invite = R"({"op":"offer","id":"1000000000000001","from":"Pico Z","action":"rinse"})";
const char* accept_rinse = R"({"op":"accept","id":"1000000000000001"})";
std::string group_recipe_offer(std::uint32_t revision, const char* id = "1000000000000002") {
    return std::string(R"({"op":"offer","id":")") + id + R"(","from":"Kitchen","action":"brew","recipe_id":"native","revision":)" + std::to_string(revision) + "}";
}
void physical_group_button(World& w, unsigned row) {
    if (row) { assert(w.head.receive_encoder(-1)); w.head_tick(); }
    CoreButtonEvent click;click.id=1;click.argument=1;
    assert(w.head.receive_button(click)); w.head_tick(); w.head_tick();
}
void group_brew() {
    World origin, target;
    auto r = recipe();
    assert(origin.engine.recipe_store(r).ok() && target.engine.recipe_store(r).ok());
    assert(origin.request(PICO_HTTP_GET,"/api/v1",nullptr,200).find("\"group_brewing_v1\":true")!=std::string::npos);
    assert(origin.adapter.load_recipe(r,true).ok());
    assert(origin.request(PICO_HTTP_GET,group_path,nullptr,200)=="{\"source\":null,\"invite\":null}");
    assert(origin.dispatch());
    const auto offered = origin.request(PICO_HTTP_GET,group_path,nullptr,200);
    assert(offered.find("\"id\":\"89ab125700000001\"")!=std::string::npos &&
           offered.find("\"recipe_id\":\"native\"")!=std::string::npos &&
           offered.find("\"action\":\"brew\"")!=std::string::npos);
    const auto post = group_recipe_offer(target.engine.recipe_revision());
    const auto frames = target.frames.size();
    const auto first=target.request(PICO_HTTP_POST,group_path,post.c_str(),200);
    assert(first.find("\"state\":\"pending\"")!=std::string::npos);
    assert(!target.core.core.commands.pending && !target.core.core.state.runtime_start_pending && target.frames.size()==frames);
    assert(target.head.encoder.state.menu.selected==0 && !std::strcmp(target.head.screens.menu_data.items[0].text.data(),"Decline"));
    const auto stale_click=target.head.screens.menu_data.items[1].event;
    assert(target.request(PICO_HTTP_POST,group_path,post.c_str(),200)==first);
    physical_group_button(target,0);
    assert(target.request(PICO_HTTP_POST,group_path,post.c_str(),200).find("\"declined\"")!=std::string::npos);
    assert(!target.core.core.commands.pending);
    const auto another=group_recipe_offer(target.engine.recipe_revision(),"1000000000000003");
    target.request(PICO_HTTP_POST,group_path,another.c_str(),200);
    assert(target.head.receive_ui(stale_click));target.head_tick();
    assert(!target.core.core.commands.pending);
    physical_group_button(target,1);
    assert(target.core.core.commands.pending && target.dispatch() && target.core.core.state.id==CoreStateId::Starting);
    auto accepted=target.request(PICO_HTTP_GET,group_path,nullptr,200);
    assert(accepted.find("\"source\":null")!=std::string::npos && accepted.find("\"state\":\"accepted\"")!=std::string::npos);
    target.request(PICO_HTTP_POST,group_path,R"({"op":"accept","id":"1000000000000003"})",200);
    assert(!target.core.core.commands.pending);
    origin.now+=120000;
    assert(origin.request(PICO_HTTP_GET,group_path,nullptr,200).find("\"source\":null")!=std::string::npos);
}
constexpr const char* pairing_path="/api/v1/picostill";
const std::string still_key(64,'b');
std::string pairing(const std::string& uid="001122334455") {
    return "{\"host\":\"still.local\",\"port\":80,\"uid\":\""+uid+"\",\"key\":\""+still_key+"\"}";
}
void still_response(World& w,const char* lease,bool clean,bool active,const char* uid="001122334455"){
    std::string json=std::string("{\"uid\":\"")+uid+"\",\"lease\":\""+lease+"\",\"healthy\":true,\"clean\":"+
        (clean?"true":"false")+",\"active\":"+(active?"true":"false")+
        ",\"pump\":true,\"fan\":false,\"celsius\":[22.1,25.2,31.5,20.4],\"pressure_psi\":12.34}";
    w.still_response="HTTP/1.1 200 OK\r\nContent-Length: "+std::to_string(json.size())+"\r\nConnection: close\r\n\r\n"+json;
    for(unsigned i=0;i<40;++i)assert(w.adapter.service_stage(CoreTickStage::PicoStill,++w.now));
    w.still_response.clear();
}
void pair_still(World& w){
    assert(w.request(PICO_HTTP_PUT,pairing_path,pairing().c_str(),202).find("\"checking\":true")!=std::string::npos);
    still_response(w,"",true,false);
    assert(w.still_request.find("Authorization: Bearer "+still_key)!=std::string::npos);
    auto state=w.request(PICO_HTTP_GET,pairing_path,nullptr,200);
    assert(state.find("\"verified\":true")!=std::string::npos && state.find(still_key)==std::string::npos);
}
void issue_still(World& w,StillCommandId id){PicoStillCommand cmd;cmd.id=id;w.core.still.commands.put(cmd);assert(w.adapter.service_stage(CoreTickStage::PicoStill,++w.now));}
void still_pairing(){
    World w;assert(w.command("picostill-manual").status==409);
    w.request(PICO_HTTP_PUT,pairing_path,pairing("invalid").c_str(),400);
    pair_still(w);World reboot(&w.files);
    auto saved=reboot.request(PICO_HTTP_GET,pairing_path,nullptr,200);
    assert(saved.find("\"paired\":true")!=std::string::npos&&saved.find("\"verified\":false")!=std::string::npos);
    reboot.request(PICO_HTTP_PUT,pairing_path,pairing("123456123456").c_str(),202);
    still_response(reboot,"",true,false);
    saved=reboot.request(PICO_HTTP_GET,pairing_path,nullptr,200);
    assert(saved.find("\"uid\":\"001122334455\"")!=std::string::npos&&saved.find("Could not verify")!=std::string::npos);
    reboot.request(PICO_HTTP_PATCH,pairing_path,"{\"session_logging\":false}",200);
    assert(reboot.core.eeprom.data.still_session_mode==SessionMode::Offline);
    assert(reboot.request(PICO_HTTP_DELETE,pairing_path,nullptr,200).find("\"paired\":false")!=std::string::npos);
    World removed(&reboot.files);assert(removed.command("picostill-boil-point").status==409);
}
void still_session(){
    World w;pair_still(w);w.request(PICO_HTTP_PATCH,pairing_path,"{\"session_logging\":true}",200);
    CoreCommand start;start.id=CoreCommandId::StartProgram;start.program_id=unsigned(ZProgramId::PicoStillManual);
    assert(w.core.post(start)&&w.dispatch());
    assert(w.core.core.state.id==CoreStateId::Starting);
    w.engine.poll();assert(w.request(PICO_HTTP_GET,"/api/v1/history",nullptr,200).find("PICOSTILL")!=std::string::npos);
    issue_still(w,StillCommandId::Stop); // Native standby wake, before StillScan.
    issue_still(w,StillCommandId::Scan);const char* token="abcdef1200000001";
    still_response(w,token,false,false);
    assert(w.core.still.state.id==PicoStillStateId::PendingCleanedAck);
    assert(w.still_request.find("\"op\":\"claim\"")!=std::string::npos);
    issue_still(w,StillCommandId::Start);assert(w.core.still.state.id==PicoStillStateId::PendingCleanedAck);
    issue_still(w,StillCommandId::CleanedAck);still_response(w,token,true,false);
    assert(w.core.still.state.id==PicoStillStateId::PendingStart);
    issue_still(w,StillCommandId::Start);assert(w.core.still.state.id==PicoStillStateId::Starting);
    w.core.runtime.state.id=RuntimeStateId::Running;const auto index=w.core.runtime.stack.size();
    assert(w.adapter.service_stage(CoreTickStage::Runtime,w.now)&&w.core.runtime.stack.size()==index);
    still_response(w,token,false,true);assert(w.core.still.state.id==PicoStillStateId::Running);
    const auto& observed=w.core.runtime.observed;
    assert(observed.still_sample_present&&std::fabs(observed.still_t3-31.5)<1e-6);
    assert(w.core.still_samples.current.pump_running&&!w.core.still_samples.current.fan_running);
    assert(std::fabs(w.core.still_samples.current.pressure-12.34)<1e-5);
    w.request(PICO_HTTP_DELETE,pairing_path,nullptr,409);
    w.core.heater.state.enabled=w.core.heater.state.started=true;w.core.msp_state.pumps=0x70;
    issue_still(w,StillCommandId::FanOn);w.now+=3001;assert(w.adapter.service_stage(CoreTickStage::PicoStill,w.now));
    assert(w.adapter.stopped()&&w.core.still.state.id==PicoStillStateId::Error&&!w.core.heater.state.enabled&&!w.core.msp_state.pumps);
    still_response(w,"",false,false);
    assert(w.still_request.find("\"op\":\"stop\"")!=std::string::npos);
    assert(w.request(PICO_HTTP_GET,pairing_path,nullptr,200).find("\"sample\":null")!=std::string::npos);
}
void still_native_programs(){
    for(const auto id:{ZProgramId::PicoStillManual,ZProgramId::PicoStillBoilPoint}){
        World w;pair_still(w);
        w.request(PICO_HTTP_PATCH,pairing_path,R"({"session_logging":false})",200);
        const char* name=id==ZProgramId::PicoStillManual?"picostill-manual":"picostill-boil-point";
        assert(w.command(name).status==202);w.adapter.poll();
        for(unsigned i=0;i<10&&!w.core.core.commands.pending;++i)w.head_tick();
        assert(w.dispatch()&&w.core.core.state.program_id==unsigned(id));
        api::Snapshot snapshot;assert(w.adapter.snapshot(snapshot).ok()&&!snapshot.recording);
        RuntimeCommand start;start.id=RuntimeCommandId::Start;start.program_id=unsigned(id);
        assert(w.core.post_runtime(start));w.core.core.state.id=CoreStateId::Running;
        unsigned prompts=0;bool claimed=false,started=false;
        for(unsigned i=0;i<180&&!started;++i){
            const auto command=w.core.still.commands.pending?w.core.still.commands.value.id:StillCommandId::None;
            assert(w.adapter.service_stage(CoreTickStage::PicoStill,++w.now));
            if(command==StillCommandId::Scan){
                assert(prompts>=5&&!claimed);still_response(w,"0000111100002222",true,false);claimed=true;
            }else if(command==StillCommandId::Start){
                const auto stack=w.core.runtime.stack.size();
                assert(claimed&&w.core.still.state.id==PicoStillStateId::Starting);
                assert(w.adapter.service_stage(CoreTickStage::Runtime,w.now)&&stack==w.core.runtime.stack.size());
                still_response(w,"0000111100002222",true,true);
                assert(w.core.still.state.id==PicoStillStateId::Running);started=true;break;
            }
            const auto op=w.core.runtime.state.instruction.opcode;
            if(op==OpCode::WaitUser||op==OpCode::GetTemp){
                RuntimeCommand answer;answer.id=op==OpCode::GetTemp?RuntimeCommandId::SetTemperature:RuntimeCommandId::Resume;
                answer.temperature=52;assert(w.core.post_runtime(answer));++prompts;
            }
            assert(w.adapter.service_stage(CoreTickStage::Runtime,++w.now));
            assert(!w.core.heater.state.started&&!w.pins[OriginalHeaterEnablePin]&&!w.pins[23]);
            assert(!(w.core.msp_state.pumps&0x70u));
        }
        if(!started)std::fprintf(stderr,"id%u state%u op%u prompts%u claim%d assertion%u\n",unsigned(id),unsigned(w.core.runtime.state.id),unsigned(w.core.runtime.state.instruction.opcode),prompts,claimed,w.core.runtime.assertion_line);
        assert(started);
        assert(w.command("stop").ok()&&w.adapter.stopped());still_response(w,"",false,false);
        assert(w.still_request.find("\"op\":\"stop\"")!=std::string::npos);
    }
}
void group_routines() {
    for (const auto id : {ZProgramId::Rinse,ZProgramId::Clean}) {
        World source,target;
        CoreCommand command;command.id=CoreCommandId::StartProgram;command.program_id=static_cast<unsigned>(id);
        assert(source.core.post(command) && source.dispatch());
        const char* name=id==ZProgramId::Rinse?"rinse":"clean";
        assert(source.request(PICO_HTTP_GET,group_path,nullptr,200).find(std::string("\"action\":\"")+name+"\"")!=std::string::npos);
        std::string offer=rinse_invite;offer.replace(offer.find("rinse"),5,name);
        target.request(PICO_HTTP_POST,group_path,offer.c_str(),200);
        target.request(PICO_HTTP_POST,group_path,accept_rinse,200);
        assert(target.dispatch() && target.core.core.state.program_id==static_cast<unsigned>(id));
        assert(target.request(PICO_HTTP_GET,group_path,nullptr,200).find("\"source\":null")!=std::string::npos);
    }
    World z;
    auto r=recipe();r.z_program=true;r.steps[0].z.has_time=true;r.steps[0].z.target_c=65.5;r.steps[0].z.seconds=73;
    r.steps[1].z.pause=true;r.steps[1].location=0;
    assert(z.engine.recipe_store(r).ok());
    auto offer=group_recipe_offer(z.engine.recipe_revision());
    z.request(PICO_HTTP_POST,group_path,offer.c_str(),200);
    z.request(PICO_HTTP_POST,group_path,R"({"op":"accept","id":"1000000000000002"})",200);
    assert(z.dispatch() && z.core.core.state.program_id==24);
    zseries::standalone::BrewRecovery checkpoint;assert(checkpoint.load(z.storage()).ok() && checkpoint.present());
    assert(checkpoint.recipe().z_program && checkpoint.recipe().steps[0].z.seconds==73 && checkpoint.recipe().steps[1].z.pause);
}
void group_interlocks() {
    World w;
    assert(w.engine.recipe_store(recipe()).ok());
    // Source labels and commands cannot be supplied as executable programs.
    w.request(PICO_HTTP_POST,group_path,R"({"op":"offer","id":"1000000000000000","from":"Other","action":"stop"})",400);
    w.request(PICO_HTTP_POST,group_path,R"({"op":"offer","id":"1000000000000000","from":"Other","action":"rinse","action":"clean"})",400);
    const auto old=group_recipe_offer(w.engine.recipe_revision()+1);
    w.request(PICO_HTTP_POST,group_path,old.c_str(),409);
    w.adapter.stop("requested");w.request(PICO_HTTP_POST,group_path,rinse_invite,409);w.core.runtime_commands.pending=false;
    assert(w.command("clear-stop").ok());
    w.core.msp_state.errors=1;w.request(PICO_HTTP_POST,group_path,rinse_invite,409);w.core.msp_state.errors=0;
    auto staged=group_recipe_offer(w.engine.recipe_revision());
    w.request(PICO_HTTP_POST,group_path,staged.c_str(),200);
    auto replacement=recipe();std::strcpy(replacement.name,"Different");assert(w.engine.recipe_store(replacement).status==409);
    w.request(PICO_HTTP_POST,group_path,R"({"op":"withdraw","id":"1000000000000002"})",200);
    assert(w.engine.recipe_store(replacement).ok());
    w.request(PICO_HTTP_POST,group_path,group_recipe_offer(w.engine.recipe_revision()-1,"1000000000000004").c_str(),409);
    assert(!w.core.core.commands.pending);
    w.request(PICO_HTTP_POST,group_path,rinse_invite,200);
    w.core.safeties.filter_missing=true;w.request(PICO_HTTP_POST,group_path,accept_rinse,409);
    w.core.safeties.filter_missing=false;
    api::Command click;std::strcpy(click.name,"encoder");click.click=true;assert(w.adapter.execute(click).status==409);
    const auto first=w.now;w.now+=119000;w.request(PICO_HTTP_POST,group_path,rinse_invite,200);
    w.now=first+120000;w.adapter.poll();w.request(PICO_HTTP_POST,group_path,accept_rinse,409);
    assert(w.request(PICO_HTTP_POST,group_path,rinse_invite,200).find("\"expired\"")!=std::string::npos);
    assert(!w.core.core.commands.pending);
    World short_offer;
    short_offer.now=UINT32_MAX-20;
    short_offer.request(PICO_HTTP_POST,group_path,R"({"op":"offer","id":"1000000000000001","from":"Kitchen","action":"rinse","ttl_ms":30})",200);
    short_offer.now+=25;
    short_offer.request(PICO_HTTP_POST,group_path,R"({"op":"offer","id":"1000000000000001","from":"Kitchen","action":"rinse","ttl_ms":120000})",200);
    short_offer.now+=5;
    short_offer.request(PICO_HTTP_POST,group_path,accept_rinse,409);
    assert(short_offer.request(PICO_HTTP_GET,group_path,nullptr,200).find("\"expired\"")!=std::string::npos && !short_offer.core.core.commands.pending);
    World withdraw;
    withdraw.request(PICO_HTTP_POST,group_path,rinse_invite,200);
    withdraw.request(PICO_HTTP_POST,group_path,R"({"op":"withdraw","id":"1000000000000001"})",200);
    withdraw.request(PICO_HTTP_POST,group_path,accept_rinse,409);
    assert(!withdraw.core.core.commands.pending);
    World auth;
    PicoHttpRequest req{};req.method=PICO_HTTP_POST;std::strcpy(req.path,group_path);
    req.body=rinse_invite;req.body_length=std::strlen(rinse_invite);PicoHttpResponse res{};char out[512];
    assert(auth.adapter.group_http(req,res,out,sizeof out) && res.status==503);
    req.auth_configured=true;
    assert(auth.adapter.group_http(req,res,out,sizeof out) && res.status==401);
    assert(auth.request(PICO_HTTP_GET,group_path,nullptr,200)=="{\"source\":null,\"invite\":null}");
    World fault;fault.request(PICO_HTTP_POST,group_path,rinse_invite,200);fault.request(PICO_HTTP_POST,group_path,accept_rinse,200);
    fault.core.safeties.filter_missing=true;assert(fault.dispatch() && fault.core.core.state.id==CoreStateId::Idle);
    assert(fault.request(PICO_HTTP_GET,group_path,nullptr,200).find("\"unavailable\"")!=std::string::npos);
}
}
int main(int argc,char** argv){assert(argc==2);const std::string test=argv[1];
    if(test=="still-programs")still_native_programs();else if(test=="still-pairing")still_pairing();else if(test=="still-session")still_session();else if(test=="group-brew")group_brew();else if(test=="group-routines")group_routines();else if(test=="group-interlocks")group_interlocks();
    else if(test=="maker")maker_program();else if(test=="recipe")owned_recipe();else if(test=="storage")storage_preflight();else if(test=="unheated")no_target();
    else if(test=="stop")stop_guard();else if(test=="input")input_order();else if(test=="offline")offline_and_reservation();
    else if(test=="settings")settings_and_display();else if(test=="status")status_fields();
    else if(test=="session-controls")dedicated_session_controls();else if(test=="recovery")recovery_restart();else if(test=="recovery-pause")recovery_unheated_and_pause();
    else if(test=="recovery-corrupt")recovery_corruption_and_discard();else if(test=="recovery-end")recovery_failure_and_terminal_state();
    else if(test=="sensor-fault")sensor_fault_cutoff();else if(test=="coffee-buffer")buffered_coffee();
    else if(test=="recipe-menu")recipe_menu_and_retired_manual_command();else if(test=="standby-start")standby_remote_start();else if(test=="step-indices")recipe_step_indices();
    else if(test=="wired-network")wired_interface_selection();
    else if(test=="sous-vide")sous_vide_programs();else if(test=="native-state")original_native_state();else if(test=="wifi-picker")stable_wifi_picker_and_local_lease();
    else if(test=="pairing")physical_pairing();
    else if(test=="network-menus")native_network_menus();else if(test=="recovery-rejected")rejected_resume_retains_checkpoint();
    else if(test=="recovery-preflight")recovery_preflight_has_no_diagnostics();else return 2;
}
