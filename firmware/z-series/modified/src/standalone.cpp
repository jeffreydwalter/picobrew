#include "esp32_ota_target.hpp"
#include "zseries/standalone.hpp"
#include "recipe_record.hpp"
#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>

namespace zseries { namespace standalone {
using namespace oem;
namespace {
class ControlGuard {
public:
    explicit ControlGuard(const HeaterSynchronization& io):io_(io){if(io_.enter)io_.enter(io_.context);}
    ~ControlGuard(){if(io_.leave)io_.leave(io_.context);}
private:
    const HeaterSynchronization& io_;
};
constexpr std::uint32_t LocalRecipes = 0x10000, SelectLocalRecipe = 0x10001,
                        LocalHome = 0x10002, LocalClearStop = 0x10003,
                        LocalRecovery = 0x10004, LocalResume = 0x10005,
                        LocalDiscard = 0x10006, LocalDiscardConfirmed = 0x10007,
                        LocalAbout = 0x10008, LocalPairing = 0x10009,
                        LocalPairCancel = 0x1000a, LocalPairApprove = 0x1000b;
const api::CommandDescriptor commands[] = {
    {"brew",api::RecipeId,api::RecipeId},{"sous-vide",0,0},
    {"cold-brew",0,0},{"rinse",0,0},{"clean",0,0},{"circulate",0,0},
    {"drain",0,0},{"rack-beer",0,0},{"chill",0,0},{"utilities",0,0},
    {"settings",0,0},{"stop",0,0},{"clear-stop",0,0},
    {"pause",0,0},{"resume",0,0},{"encoder",api::Delta|api::Click,0},
    {"resume-interrupted",api::Confirm,api::Confirm},{"discard-interrupted",0,0}
};
api::Result busy(){return {409,"busy","Return to the idle home screen first"};}
api::Result unavailable(){return {503,"controller_unavailable","Controller initialization or a device fault prevents this operation"};}
template<std::size_t N>void copy(char(&out)[N],const char* input){
    std::memset(out,0,N);if(input)std::memcpy(out,input,std::min(N-1,std::strlen(input)));
}
bool step(void* context,std::size_t index,LocalRecipeStep& out){
    const auto& recipe=*static_cast<const api::Recipe*>(context);
    if(index>=recipe.step_count)return false;
    const auto& in=recipe.steps[index];out.name=in.name;out.location=in.location;
    if(recipe.z_program) {
        out.type=in.z.pause?2:1;
        out.temperature_celsius=in.z.target_c;
        out.seconds=in.z.seconds;
        out.drain_seconds=in.z.drain_seconds;
        return true;
    }
    out.type=0;
    out.temperature_celsius=in.target_f?(static_cast<double>(in.target_f)-32.0)/1.8:0;
    out.seconds=static_cast<std::int32_t>(in.minutes)*60;out.drain_seconds=static_cast<std::int32_t>(in.drain_minutes)*60;
    return true;
}
ProgramBuildResult build(ProgramBuffer& buffer,const api::Recipe& recipe,double pressure,double boil){
    oem::LocalRecipe input;input.name=recipe.name;input.is_beer=!recipe.z_program||recipe.is_beer;
    input.use_pico_pak_mode=recipe.z_program&&recipe.use_pico_pak;input.step_count=recipe.step_count;
    input.context=const_cast<api::Recipe*>(&recipe);input.step=step;
    const auto result=build_local_recipe(buffer,input,RecipeEnvironment(pressure,boil));
    if(result!=ProgramBuildResult::Ok)return result;
    // API target=0 means unheated. The OEM local recipe builder merely omits
    // ReachTarget, which can retain a previous step's active PID. Explicitly
    // stop that PID before MoveValve can preserve/restart it in the next step.
    std::array<Instruction,ProgramBuffer::instruction_capacity> code{};std::size_t count=0;
    const auto& view=buffer.record().view;
    for(std::size_t i=0;i<view.code_size;++i){
        if(count==code.size())return ProgramBuildResult::CodeFull;
        code[count++]=view.code[i];
        if(view.code[i].opcode==OpCode::StepName&&view.code[i].argument>=1&&view.code[i].argument<=recipe.step_count){
            const auto& step=recipe.steps[view.code[i].argument-1];
            const bool unheated=recipe.z_program?(!step.z.pause&&step.z.target_c<=0):step.target_f==0;
            if(!unheated)continue;
            if(count==code.size())return ProgramBuildResult::CodeFull;
            code[count++]={OpCode::NoTarget,0};
        }
    }
    return buffer.set_code(code.data(),count);
}
bool recordable(std::uint32_t id){return id==24||id==28||id==5||id==6||(id>=20&&id<=23);}
}
Adapter::Adapter(CoreApplication& core,HeadApplication& head) noexcept:core_(core),head_(head){}
const char* Adapter::text(const char* en,const char* es,const char* fr) const noexcept {
    switch(core_.eeprom.data.language){case LanguageType::Spanish:return es;case LanguageType::French:return fr;default:return en;}
}
api::DeviceServices Adapter::services() noexcept {
    api::DeviceServices out;out.context=this;
    out.identity=[](void* p){api::Identity id;id.model="z-series";id.model_name="Pico Z";id.model_family="z-series";id.firmware_version=picobrew::esp32_ota::Version;id.device_uid=static_cast<Adapter*>(p)->core_.identifier.data();return id;};
    out.capabilities=[](void* p){api::Capabilities caps;
        if (static_cast<Adapter*>(p)->wireless_) caps.ota_protocol = "esp32-factory-v1";
        caps.flags=api::Recipes|api::Brewing|api::Heating|api::Pump|api::Temperature|api::Pressure|api::FrontPanel;
        caps.settings=api::TemperatureUnit|api::Language|api::DetailedStatus|api::AutoCycle;caps.commands=commands;caps.command_count=sizeof(commands)/sizeof(*commands);caps.zero_minute_steps=true;caps.z_program_maker=true;caps.dedicated_pause_resume=true;return caps;};
    out.snapshot=[](void* p,api::Snapshot& value){return static_cast<Adapter*>(p)->snapshot(value);};
    out.execute=[](void* p,const api::Command& value){return static_cast<Adapter*>(p)->execute(value);};
    out.idle=[](void* p){return static_cast<Adapter*>(p)->idle();};
    out.read_settings=[](void* p,api::Settings& value){return static_cast<Adapter*>(p)->read_settings(value);};
    out.validate_settings=[](void* p,const api::Settings& value,std::uint32_t changed){return static_cast<Adapter*>(p)->validate_settings(value,changed);};
    out.apply_settings=[](void* p,const api::Settings& value,std::uint32_t changed){return static_cast<Adapter*>(p)->apply_settings(value,changed);};
    out.validate_recipe=[](void* p,const api::Recipe& value){return static_cast<Adapter*>(p)->validate_recipe(value);};
    out.display=[](void* p,api::Display& value){return static_cast<Adapter*>(p)->display(value);};
    out.read_display=[](void* p,std::uint32_t revision,std::size_t offset,std::uint8_t* data,std::size_t size){return static_cast<Adapter*>(p)->read_display(revision,offset,data,size);};
    return out;
}
bool Adapter::bind(Ports ports,api::Engine& engine){
    if(bound_||!ports.millis||!ports.http_poll||!ports.serial_poll||!ports.lan_ready||!ports.save_network||!ports.reconnect_network||!ports.save_configuration||!ports.storage.read||!ports.storage.write_atomic)return false;
    if(!core_.core.services.tick||!core_.core.services.perform||!core_.ports.heater_gpio.configure_output||!core_.ports.heater_gpio.write_pin||
       !head_.flow.services.pending||!head_.flow.services.handle_event||!head_.flow.services.perform||
       !head_.core.services.assertion||!head_.core.services.perform||!head_.ui.services.assertion||!head_.ui.services.perform||
       !core_.msp.transport.transmit||!core_.msp.transport.receive||!core_.msp.transport.delay_ms)return false;
    const auto& sensor=core_.ports.sensor_bus;
    if(!sensor.begin_transmission||!sensor.write||!sensor.end_transmission||!sensor.request_from||!sensor.read||!sensor.delay_ms)return false;
    ports_=ports;engine_=&engine;original_core_=core_.core.services;original_head_=head_.flow.services;original_head_core_=head_.core.services;original_ui_=head_.ui.services;
    original_gpio_=core_.ports.heater_gpio;original_msp_=core_.msp.transport;original_sensor_=sensor;
    auto& bus=core_.ports.sensor_bus;bus.context=this;
    bus.begin_transmission=[](void* p,std::uint8_t address){auto& s=*static_cast<Adapter*>(p);if(!s.sensor_failed_)s.original_sensor_.begin_transmission(s.original_sensor_.context,address);};
    bus.write=[](void* p,std::uint8_t byte)->std::size_t{auto& s=*static_cast<Adapter*>(p);if(s.sensor_failed_)return 0;
        const auto count=s.original_sensor_.write(s.original_sensor_.context,byte);if(count!=1)s.sensor_failure();return count;};
    bus.end_transmission=[](void* p,bool stop)->std::uint8_t{auto& s=*static_cast<Adapter*>(p);if(s.sensor_failed_)return 4;
        const auto result=s.original_sensor_.end_transmission(s.original_sensor_.context,stop);if(result)s.sensor_failure();return result;};
    bus.request_from=[](void* p,std::uint8_t address,std::size_t size,bool stop)->std::size_t{auto& s=*static_cast<Adapter*>(p);if(s.sensor_failed_)return 0;
        const auto count=s.original_sensor_.request_from(s.original_sensor_.context,address,size,stop);if(count!=size)s.sensor_failure();return count;};
    bus.read=[](void* p){auto& s=*static_cast<Adapter*>(p);if(s.sensor_failed_)return -1;
        const int value=s.original_sensor_.read(s.original_sensor_.context);if(value<0||value>255)s.sensor_failure();return value;};
    bus.delay_ms=[](void* p,std::uint32_t ms){auto& s=*static_cast<Adapter*>(p);if(!s.sensor_failed_)s.original_sensor_.delay_ms(s.original_sensor_.context,ms);};
    core_.ports.heater_gpio.context=this;
    core_.ports.heater_gpio.configure_output=[](void* p,std::uint8_t pin){auto& s=*static_cast<Adapter*>(p);s.original_gpio_.configure_output(s.original_gpio_.context,pin);};
    core_.ports.heater_gpio.write_pin=guarded_write;
    core_.msp.transport.context=this;core_.msp.transport.transmit=guarded_transmit;
    core_.msp.transport.receive=[](void* p,std::uint8_t address,std::uint8_t* data,std::size_t size){auto& s=*static_cast<Adapter*>(p);s.original_msp_.receive(s.original_msp_.context,address,data,size);};
    core_.msp.transport.delay_ms=[](void* p,std::uint32_t ms){auto& s=*static_cast<Adapter*>(p);s.original_msp_.delay_ms(s.original_msp_.context,ms);};
    core_.core.services.context=this;
    core_.core.services.millis=[](void* p){auto& s=*static_cast<Adapter*>(p);return s.ports_.millis(s.ports_.context);};
    core_.core.services.tick=[](void* p,CoreTickStage stage,std::uint32_t now){return static_cast<Adapter*>(p)->service_stage(stage,now);};
    core_.core.services.perform=[](void* p,const CoreAction& action,std::uint32_t& value){return static_cast<Adapter*>(p)->service_effect(action,value);};
    head_.flow.services.context=this;
    head_.flow.services.millis=core_.core.services.millis;
    head_.flow.services.pending=[](void* p,HeadEventKind kind){auto& s=*static_cast<Adapter*>(p);return s.original_head_.pending(s.original_head_.context,kind);};
    head_.flow.services.handle_event=[](void* p,HeadEventKind kind){return static_cast<Adapter*>(p)->handle_head(kind);};
    head_.flow.services.perform=[](void* p,const HeadAction& action){auto& s=*static_cast<Adapter*>(p);
        if(action.effect==HeadEffect::HomeScreen)return s.show_home();
        if(action.effect==HeadEffect::NetworkRequired)return s.show_network();
        return s.original_head_.perform(s.original_head_.context,action);};
    head_.core.services.context=this;head_.core.services.millis=core_.core.services.millis;
    head_.core.services.assertion=[](void* p,std::uint32_t line){auto& s=*static_cast<Adapter*>(p);return s.original_head_core_.assertion(s.original_head_core_.context,line);};
    head_.core.services.perform=[](void* p,const HeadCoreAction& action,std::uint32_t& result){auto& s=*static_cast<Adapter*>(p);
        if(action.effect==HeadCoreEffect::HomeScreen){result=0;return s.show_home();}
        if(action.effect==HeadCoreEffect::NetworkConfiguration){result=0;return s.show_network();}
        return s.original_head_core_.perform(s.original_head_core_.context,action,result);};
    head_.ui.services.context=this;head_.ui.services.millis=core_.core.services.millis;
    head_.ui.services.assertion=[](void* p,std::uint32_t line){auto& s=*static_cast<Adapter*>(p);return s.original_ui_.assertion(s.original_ui_.context,line);};
    head_.ui.services.perform=[](void* p,const HeadUiAction& action){return static_cast<Adapter*>(p)->handle_ui(action);};
    bound_=true;return true;
}
bool Adapter::begin(){
    if(!bound_||!head_.initialized)return false;
    // A reboot of the ESP32 does not prove the independent MSP stopped its
    // pumps. Establish inactive outputs before releasing startup inhibition.
    if(!stop("initializing")||core_.system_error||core_.msp_state.id!=Z_MSP_APP_RUNNING||core_.msp_state.errors)return false;
    core_.eeprom.data.azure_enabled=BoolOption::False;core_.core.state.offline=true;core_.core.state.azure_enabled=false;
    core_.azure.enabled=false;core_.registration.enabled=false;core_.session_logger.stop_requested=false;
    core_.local_server.enable(false);
    const auto recovered=recovery_.load(ports_.storage);
    if(!recovered.ok()&&engine_)engine_->event("recovery-error",recovered.error);
    // The composition selects verified modified network credentials. Original
    // EEPROM credentials are only an explicit, read-only migration source.
    {ControlGuard guard(core_.ports.control);inhibited_=0;stop_reason_="none";}
    begun_=true;return true;
}
void Adapter::sensor_failure(){
    if(sensor_failed_)return;
    sensor_failed_=true;stop("sensor-io");core_.system_error=true;
    std::strncpy(core_.system_error_text.data(),"Sensor I/O failed",core_.system_error_text.size()-1);
}
void ZSERIES_HEATER_IRAM Adapter::guarded_write(void* p,std::uint8_t pin,bool enabled){
    auto& self=*static_cast<Adapter*>(p);
    if(self.inhibited_&&(pin==OriginalHeaterEnablePin||pin==self.core_.heater.output_pin))enabled=false;
    self.original_gpio_.write_pin(self.original_gpio_.context,pin,enabled);
}
void Adapter::guarded_transmit(void* p,std::uint8_t address,const std::uint8_t* bytes,std::size_t size){
    auto& self=*static_cast<Adapter*>(p);
    if(self.inhibited_&&size>=5&&bytes[0]==8&&
       (bytes[2]==Z_MSP_PUMPS_SET||bytes[2]==Z_MSP_KEG_ON||bytes[2]==Z_MSP_DRAIN_ON||bytes[2]==Z_MSP_HEX_ON)){
        std::uint8_t stopped[6];const auto length=z_msp_app_frame(stopped,sizeof stopped,Z_MSP_PUMPS_SET,0,1);
        self.original_msp_.transmit(self.original_msp_.context,address,stopped,length);return;
    }
    self.original_msp_.transmit(self.original_msp_.context,address,bytes,size);
}
bool Adapter::stop(const char* reason){
    if(!bound_)return false;
    {ControlGuard guard(core_.ports.control);inhibited_=1;stop_reason_=reason;
        core_.heater.state.enabled=false;core_.heater.state.started=false;core_.heater.state.output_ticks=0;core_.heater.countdown=0;
        core_.heater.pid.automatic=false;
        original_gpio_.write_pin(original_gpio_.context,OriginalHeaterEnablePin,false);
        original_gpio_.write_pin(original_gpio_.context,core_.heater.output_pin,false);}
    queued_=false;encoder_click_=false;core_.core.commands.pending=false;core_.runtime_commands.pending=false;
    core_.core.state.runtime_start_pending=false;core_.core.state.rinse_pending=false;
    if(begun_){RuntimeCommand stop;stop.id=RuntimeCommandId::Stop;core_.post_runtime(stop);}
    // The transport guard also covers future console/Runtime pump commands.
    // Send directly so an OEM moving-valve assertion cannot postpone cutoff.
    if(core_.msp_state.id==Z_MSP_APP_RUNNING){std::uint8_t frame[6],response[9];
        const auto size=z_msp_app_frame(frame,sizeof frame,Z_MSP_PUMPS_SET,0,1);
        original_msp_.transmit(original_msp_.context,core_.msp.address,frame,size);
        original_msp_.delay_ms(original_msp_.context,core_.msp.response_delay);
        original_msp_.receive(original_msp_.context,core_.msp.address,response,sizeof response);
        z_msp_apply_response(&core_.msp_state,response);}
    if(core_.core.state.id==CoreStateId::Starting)core_.core.transition(CoreStateId::Idle);
    if(engine_&&begun_){engine_->cancel_prepared_session();engine_->event("stop",reason);}native_prepared_=false;
    if(begun_){
        recovery_active_=false;resuming_=false;
        if(!std::strcmp(reason,"requested")&&(recovery_.present()||!recovery_.ready())){
            const auto cleared=recovery_.clear();if(!cleared.ok()&&engine_)engine_->event("recovery-error",cleared.error);
        }
    }
    return true;
}
bool Adapter::idle() const noexcept {
    // Native standby clears the screen and is an idle controller state. A
    // canonical remote start may wake it without a synthetic encoder action.
    const bool home=(core_.core.state.id==CoreStateId::Idle&&page_==Page::Home)||core_.core.state.id==CoreStateId::StandBy;
    return begun_&&home&&core_.runtime.state.id==RuntimeStateId::Standby&&
        !queued_&&!encoder_click_&&!core_.core.commands.pending&&!core_.runtime_commands.pending;
}
bool Adapter::wake_standby(){
    if(core_.core.state.id!=CoreStateId::StandBy)return core_.core.state.id==CoreStateId::Idle;
    if(!core_.ports.display_power||!core_.ports.display_power(core_.ports.context,true)||
       head_.display_power(true)!=HeadApplicationResult::Ok||
       core_.core.transition(CoreStateId::Idle)!=CoreFlowResult::Ok)return false;
    core_.encoder.activity_date=ports_.millis(ports_.context);page_=Page::Home;return true;
}
bool Adapter::clear_stop(){
    if(!idle()||core_.system_error||core_.msp_state.errors||core_.msp_state.id!=Z_MSP_APP_RUNNING||
       (!std::strcmp(stop_reason_,"filter-removed")&&core_.safeties.filter_missing))return false;
    {ControlGuard guard(core_.ports.control);inhibited_=0;stop_reason_="none";}
    if(engine_)engine_->event("clear-stop","Output inhibition cleared; no session resumed");
    return true;
}
api::Result Adapter::validate_recipe(const api::Recipe& recipe){
    if(!api::detail::recipe_valid(recipe,true,true))return {400,"invalid_recipe","Recipe exceeds supported Z program bounds"};
    ProgramBuffer temporary;
    if(build(temporary,recipe,101325,100)!=ProgramBuildResult::Ok)return {400,"invalid_recipe","Recipe cannot be represented by the native Z program"};
    return {};
}
api::Result Adapter::load_recipe(const api::Recipe& recipe,bool start){
    if(core_.core.state.id!=CoreStateId::Idle||core_.runtime.state.id!=RuntimeStateId::Standby||stopped())return busy();
    const auto valid=validate_recipe(recipe);if(!valid.ok())return valid;
    if(core_.system_error||core_.msp_state.id!=Z_MSP_APP_RUNNING||core_.msp_state.errors)return unavailable();
    if(!recovery_.ready())return {503,"recovery_storage","Discard the invalid interrupted brew checkpoint before starting"};
    if(recovery_.present()&&!recovery_active_)return {409,"recovery_pending","Resume or discard the interrupted brew first"};
    if(build(core_.program,recipe,core_.auxiliary.pressure_pa,core_.temperatures.estimated_boil_celsius)!=ProgramBuildResult::Ok)
        return {400,"invalid_recipe","The current native recipe environment rejected this recipe"};
    selected_recipe_=recipe;owned_recipe_=true;page_=Page::Original;
    if(start){if(!queue_program(static_cast<std::uint32_t>(ZProgramId::Buffered)))return busy();}
    else core_.publication.ready();
    return {202};
}
bool Adapter::queue_program(std::uint32_t id){
    CoreCommand command;command.id=CoreCommandId::StartProgram;command.program_id=id;
    return core_.post(command);
}
api::Result Adapter::execute(const api::Command& input){
    if(!std::strcmp(input.name,"stop"))return stop("requested")?api::Result(202):unavailable();
    if(!std::strcmp(input.name,"clear-stop"))return clear_stop()?api::Result(202):busy();
    if(!begun_)return unavailable();
    if(!std::strcmp(input.name,"discard-interrupted"))return discard_interrupted();
    if(!std::strcmp(input.name,"resume-interrupted"))return resume_interrupted(input.confirm);
    if(!std::strcmp(input.name,"encoder")){
        if(input.delta<-100||input.delta>100||(!input.delta&&!input.click))return {400,"invalid_command","Provide encoder movement or a click"};
        if(page_==Page::Pairing)return {409,"physical_confirmation","Use the physical dial to confirm pairing"};
        if(encoder_click_||head_.encoder_events.pending||head_.button_events.pending)return busy();
        if(core_.core.state.id==CoreStateId::StandBy){
            if(!wake_standby())return unavailable();
        }
        if(input.delta)head_.receive_encoder(-input.delta);
        if(input.click){
            if(input.delta)encoder_click_=true;
            else {CoreButtonEvent button;button.id=1;button.type=0;button.argument=1;head_.receive_button(button);}}
        core_.encoder.activity_date=ports_.millis(ports_.context);return {202};
    }
    if(stopped())return {409,"stopped","Clear the latched stop at the idle home screen first"};
    if(!std::strcmp(input.name,"pause")||!std::strcmp(input.name,"resume")){
        if(core_.core.state.id!=CoreStateId::Running)return busy();
        CoreCommand command;command.id=!std::strcmp(input.name,"pause")?CoreCommandId::Pause:CoreCommandId::Resume;
        return core_.post(command)?api::Result(202):busy();
    }
    if(!idle())return busy();
    if(core_.system_error||core_.msp_state.errors||core_.msp_state.id!=Z_MSP_APP_RUNNING)return unavailable();
    bool known=false;for(const auto& descriptor:commands)if(!std::strcmp(input.name,descriptor.name))known=true;
    if(!known)return {400,"invalid_command","This Z command is not supported"};
    queued_command_=input;queued_command_.recipe=nullptr;
    if(!std::strcmp(input.name,"brew")){
        if(!input.recipe)return {400,"invalid_recipe","An existing recipe is required"};
        auto valid=validate_recipe(*input.recipe);if(!valid.ok())return valid;queued_recipe_=*input.recipe;
    }
    if(!wake_standby())return unavailable();
    queued_=true;return {202};
}
void Adapter::poll(){
    if(begun_)poll_recovery();
    if(page_==Page::Pairing){
        auto& menu=head_.screens.menu_data;
        if(!menu.count||static_cast<std::uint32_t>(menu.items[0].event.command)!=LocalPairCancel||
           std::strcmp(head_.screens.state.title.data(),text("Pair with Hub","Enlazar con Hub","Jumeler au Hub"))){
            // A stock alarm or instruction took over the display.
            page_=Page::Original;pairing_ticket_=0;
        }else{
            char code[9];
            if(ports_.pair_prompt(ports_.context,ports_.millis(ports_.context),code)!=pairing_ticket_)
                show_pairing();
        }
    }
    if(native_prepared_&&core_.core.state.id!=CoreStateId::Starting&&core_.core.state.id!=CoreStateId::Running&&core_.core.state.id!=CoreStateId::Stopping){
        engine_->cancel_prepared_session();native_prepared_=false;
    }
    // Head dispatches Button before Encoder. A combined request must complete
    // its movement before its click can select an item on the resulting screen.
    if(begun_&&encoder_click_&&!head_.encoder_events.pending&&!head_.button_events.pending){
        CoreButtonEvent button;button.id=1;button.type=0;button.argument=1;
        if(head_.receive_button(button))encoder_click_=false;
    }
    if(!begun_||!queued_)return;
    queued_=false;const auto& name=queued_command_.name;
    api::Result result;
    if(!std::strcmp(name,"brew"))result=load_recipe(queued_recipe_,true);
    else {
        UiCommand command=UiCommand::None;
        if(!std::strcmp(name,"sous-vide"))command=UiCommand::StartSousVideWithKeg;
        else if(!std::strcmp(name,"cold-brew"))command=UiCommand::CoffeeColdBrewTweak;
        else if(!std::strcmp(name,"rinse"))command=UiCommand::StartRinse;
        else if(!std::strcmp(name,"clean"))command=UiCommand::StartClean;
        else if(!std::strcmp(name,"circulate"))command=UiCommand::StartCirculate;
        else if(!std::strcmp(name,"drain"))command=UiCommand::StartDrain;
        else if(!std::strcmp(name,"rack-beer"))command=UiCommand::StartRack;
        else if(!std::strcmp(name,"chill"))command=UiCommand::StartChill;
        else if(!std::strcmp(name,"utilities"))command=UiCommand::Utilities;
        else if(!std::strcmp(name,"settings"))command=UiCommand::Settings;
        HeadUiEvent event;event.command=command;
        if(command==UiCommand::None||!head_.receive_ui(event))result=busy();else page_=Page::Original;
    }
    if(!result.ok()&&engine_)engine_->event("command-error",result.error?result.error:"controller_error");
}
bool Adapter::offline_policy(std::uint32_t now){
    auto& state=core_.core.state;state.offline=true;state.azure_enabled=false;core_.eeprom.data.azure_enabled=BoolOption::False;
    if(state.id==CoreStateId::Booting){
        bool sensors=true;for(const auto& value:core_.temperatures.temperatures)sensors=sensors&&value.updated_ms!=0;
        if(sensors&&!core_.system_error&&core_.msp_state.id==Z_MSP_APP_RUNNING&&!core_.msp_state.errors&&now-state.state_date>=core_.core.boot_delay_ms)
            return core_.core.transition(CoreStateId::Idle)==CoreFlowResult::Ok;
    }
    if(state.id==CoreStateId::NeedsNetwork||state.id==CoreStateId::SyncPending||state.id==CoreStateId::Syncing||state.id==CoreStateId::Registering)
        return core_.core.transition(CoreStateId::Idle)==CoreFlowResult::Ok;
    return true;
}
bool Adapter::tick_wifi(std::uint32_t now){
    // An intervening native instruction or alarm owns its new screen.
    if(page_==Page::Scanning&&head_.screens.state.reset_time!=scan_started_)page_=Page::Original;
    if(page_==Page::Scanning){
        auto& io=core_.wifi.services;
        std::uint32_t count=0;
        const bool ok=io.perform(io.context,WifiAction(WifiEffect::ScanComplete),count);
        if(!ok||now-scan_started_>=15000)
            show_wifi_results(-2);
        else if(static_cast<std::int32_t>(count)!=-1)
            show_wifi_results(static_cast<std::int32_t>(count));
    }
    auto& wifi=core_.wifi;
    // The OEM Checking state requires a vendor hostname to resolve. This
    // profile supplies a LAN service and qualifies the active station lease.
    if(wifi.state.id==WiFiStateId::Checking&&!wifi.state.requested.internet_expected){
        if(wifi.target!=WiFiStateId::Connected)
            return wifi.transition(WiFiStateId::Disconnecting)==WifiResult::Ok;
        if(ports_.lan_ready(ports_.context,1)){
            auto& io=wifi.services;
            if(!io.perform(io.context,WifiAction(WifiEffect::LocalAddress),wifi.state.local_address))return false;
            if(wifi.state.local_address){
                wifi.state.local_address_present=true;
                if(!io.perform(io.context,WifiAction(WifiEffect::GatewayAddress),wifi.state.gateway_address))return false;
                return wifi.transition(WiFiStateId::Connected)==WifiResult::Ok;
            }
        }
        if(now-wifi.state_date>=wifi.connecting_timeout_ms)
            return wifi.fail(now,WifiError::WifiConnectingTimeout)==WifiResult::Ok;
        return true;
    }
    return wifi.tick(now)==WifiResult::Ok;
}
bool Adapter::service_stage(CoreTickStage stage,std::uint32_t now){
    switch(stage){
    // These remote services are intentionally replaced/disabled in this profile.
    case CoreTickStage::FirmwareUpdate:case CoreTickStage::BinaryDownload:
    case CoreTickStage::ZBackend:case CoreTickStage::HttpBackend:
    case CoreTickStage::Registration:case CoreTickStage::ProgramDownload:case CoreTickStage::SessionLogger:return true;
    case CoreTickStage::Wifi:{
        const bool ok=tick_wifi(now);
        core_.refresh_observed();
        return ok;
    }
    case CoreTickStage::Azure:return offline_policy(now);
    case CoreTickStage::HttpServer:ports_.http_poll(ports_.context,now);return true;
    case CoreTickStage::SerialCommand:ports_.serial_poll(ports_.context);return true;
    default:break;
    }
    const bool ok=original_core_.tick(original_core_.context,stage,now);
    if(ok&&stage==CoreTickStage::Runtime)observe_care();
    if(stage==CoreTickStage::Safeties&&core_.runtime.filter_required&&core_.safeties.filter_missing&&!stopped())stop("filter-removed");
    return ok;
}
void Adapter::observe_care(){
    if(!engine_||stopped()||care_recorded_||core_.runtime.state.id!=RuntimeStateId::Running)return;
    const auto type=core_.core.state.session_type;
    const auto screen=core_.runtime.state.screen;
    api::CareEvent event;
    // These are the final native screens, after all programmed rinse cycles.
    // A stop or reboot before reaching them leaves the reminder outstanding.
    if(type==SessionType::Rinse&&screen==30)event=api::CareEvent::RinseFinished;
    else if(type==SessionType::Clean&&screen==40)event=api::CareEvent::CleanFinished;
    else{
        if(!(core_.msp_state.pumps&0x30))return;
        switch(type){
        case SessionType::LocalBrew:case SessionType::BrewZymatic:case SessionType::BrewPicoPak:
        case SessionType::BrewZRecipe:case SessionType::BrewDemo:case SessionType::ManualBrew:
            event=api::CareEvent::BrewUsed;break;
        case SessionType::Coffee:case SessionType::Chill:event=api::CareEvent::RinseNeeded;break;
        default:return;
        }
    }
    // A persistent storage failure must not issue repeated writes in the control loop.
    care_recorded_=true;
    engine_->record_care(event);
}
bool Adapter::service_effect(const CoreAction& action,std::uint32_t& result){
    result=0;
    switch(action.effect){
    case CoreEffect::SetNetwork:core_.network=action.argument&&ports_.lan_ready(ports_.context,action.argument)?action.argument:0;result=action.argument?core_.network!=0:1;return true;
    case CoreEffect::WifiConnect:{
        if(!action.credentials)return false;
        if(!ports_.save_network(ports_.context,action.credentials->ssid.data(),action.credentials->password.data())){
            if(engine_)engine_->event("network-error","Wi-Fi settings could not be saved");
            return network_message(text("Wi-Fi settings rejected","Wi-Fi no guardado","Wi-Fi non enregistre"));
        }
        WifiCredentials credentials;credentials.ssid=action.credentials->ssid;credentials.password=action.credentials->password;
        credentials.internet_expected=false;core_.wifi.connect(credentials);return true;
    }
    case CoreEffect::HttpServerEnable:return true; // shared authenticated server owns port 80
    case CoreEffect::AzureRecover:case CoreEffect::AzureBeginSession:case CoreEffect::AzureEndSession:case CoreEffect::AzureDiscard:
        core_.azure.enabled=false;core_.core.state.azure_enabled=false;return true;
    case CoreEffect::StartSync:case CoreEffect::RegistrationRequest:core_.registration.enabled=false;return true;
    case CoreEffect::BeginSession:core_.core.state.session_mode=SessionMode::Offline;return true;
    case CoreEffect::PrepareFirmwareUpdate:case CoreEffect::StartFirmwareUpdate:case CoreEffect::AzureSessionLoad:return false;
    case CoreEffect::RequestBackendCommand:return show_recipes();
    case CoreEffect::StartProgram:
        return start_effect(action,result,action.argument);
    case CoreEffect::RuntimeStart:
        if(stopped()){core_.core.state.runtime_start_pending=false;return true;}break;
    case CoreEffect::DispatchCoreCommand:
        // Coffee and local recipes share the original mutable ProgramBuffer.
        // Its native id24 does not establish that it contains our saved beer.
        if(action.command&&action.command->id==CoreCommandId::CoffeeProgramLoad&&core_.core.state.id==CoreStateId::Idle)
            owned_recipe_=false;
        if(action.command&&action.command->id==CoreCommandId::StartProgram){
            // The recovered dispatcher ignores StartProgram outside Idle.
            // Preserve that boundary before touching persistent history.
            if(core_.core.state.id!=CoreStateId::Idle)break;
            return start_effect(action,result,action.command->program_id);
        }
        break;
    default:break;
    }
    return original_core_.perform(original_core_.context,action,result);
}
bool Adapter::start_effect(const CoreAction& action,std::uint32_t& result,std::uint32_t id){
    // A queued resume is consumed by this attempt, including storage rejection.
    // Keep the retained checkpoint reviewable if no native session starts.
    const bool requested_resume=resuming_;resuming_=false;
    if(stopped()||id==static_cast<std::uint32_t>(ZProgramId::ManualBrew))return show_home();
    const auto* program=core_.programs.find(id);
    if(!program)return false;
    const bool local_brew=id==24&&program->type==SessionType::LocalBrew;
    if(local_brew&&!owned_recipe_){engine_->event("session-error","unowned_local_recipe");return show_home();}
    if(recordable(id)){
        if(!recovery_.ready()||(recovery_.present()&&!recovery_active_&&!(local_brew&&requested_resume))){
            engine_->event("session-error",recovery_.ready()?"recovery_pending":"recovery_storage");return show_home();}
        std::uint32_t token=session_token_+1;if(!token)++token;
        const auto reserved=engine_->prepare_session(local_brew?"brew":"program",program->view.name,token);
        if(!reserved.ok()){engine_->event("session-error",reserved.error?reserved.error:"storage_error");return show_home();}
        session_token_=token;
        native_prepared_=true;
        if(local_brew&&!requested_resume){
            const auto saved=recovery_.prepare(selected_recipe_,core_.program);
            if(!saved.ok()){engine_->cancel_prepared_session();native_prepared_=false;engine_->event("recovery-error",saved.error);return show_home();}
            elapsed_before_resume_=0;
        }
    }
    // The legacy builder uses 11.36 L to estimate heating time. Local API
    // recipes contain no starting water volume. Leave the session quantity
    // unspecified so the native loading screen gives its generic water prompt.
    if(local_brew)core_.program.parameters.water={0,Unit::Liter};
    const bool ok=original_core_.perform(original_core_.context,action,result);
    if(ok&&core_.core.state.id==CoreStateId::Starting&&core_.core.state.program_id==id)care_recorded_=requested_resume;
    if(recordable(id)&&(!ok||core_.core.state.id!=CoreStateId::Starting||core_.core.state.program_id!=id)){engine_->cancel_prepared_session();native_prepared_=false;}
    if(local_brew&&ok&&core_.core.state.id==CoreStateId::Starting){recovery_active_=true;recovery_date_=ports_.millis(ports_.context);recovery_instruction_=0;}
    return ok;
}
bool Adapter::handle_ui(const HeadUiAction& action){
    if(action.effect==HeadUiEffect::NetworkConfiguration)return show_network();
    if(action.effect==HeadUiEffect::DispatchCoreCommand&&action.first==static_cast<std::uint32_t>(CoreCommandId::WiFiScan))
        return start_wifi_scan();
    if(action.effect==HeadUiEffect::WifiKey)return action.event&&select_wifi(*action.event);
    if(action.effect==HeadUiEffect::WifiDetails&&(!ports_.lan_ready(ports_.context,1)||core_.wifi.state.id!=WiFiStateId::Connected))
        return network_message(text("Wi-Fi offline","Wi-Fi desconectado","Wi-Fi deconnecte"));
    if(action.effect==HeadUiEffect::ConnectWifi)return ports_.reconnect_network(ports_.context)&&show_network();
    if(action.effect==HeadUiEffect::SetImperial||action.effect==HeadUiEffect::SetLanguage||action.effect==HeadUiEffect::SetSlideshowDetail||action.effect==HeadUiEffect::SetStaticCycle){
        if(!engine_)return false;
        api::Settings settings;read_settings(settings);std::uint32_t changed=0;
        if(action.effect==HeadUiEffect::SetImperial){settings.temperature_unit=action.first?'F':'C';changed=api::TemperatureUnit;}
        else if(action.effect==HeadUiEffect::SetSlideshowDetail){settings.detailed_status=action.first!=0;changed=api::DetailedStatus;}
        else if(action.effect==HeadUiEffect::SetStaticCycle){settings.auto_cycle=action.first==0;changed=api::AutoCycle;}
        else {if(action.first>=LanguageTypeCount)return false;copy(settings.language,action.first==1?"fr":action.first==2?"es":"en");changed=api::Language;}
        const auto saved=engine_->preferences_store(settings,changed);
        if(!saved.ok()){engine_->event("settings-error",saved.error?saved.error:"storage_error");return false;}
        return head_.synchronize()==HeadApplicationResult::Ok;
    }
    return original_ui_.perform(original_ui_.context,action);
}
bool Adapter::show_home(){
    auto& screen=head_.screens;screen.clear_data();HeadUiEvent recipes;recipes.command=static_cast<UiCommand>(LocalRecipes);
    screen.menu_data.append(recipes,text("Brew Recipe","Preparar receta","Brasser une recette"));
    if(recovery_.present()&&!recovery_active_){HeadUiEvent resume;resume.command=static_cast<UiCommand>(LocalRecovery);
        screen.menu_data.append(resume,text("Resume interrupted","Reanudar interrumpido","Reprendre brassage"));}
    if((recovery_.present()&&!recovery_active_)||!recovery_.ready()){HeadUiEvent discard;discard.command=static_cast<UiCommand>(LocalDiscard);
        screen.menu_data.append(discard,text("Discard interrupted","Descartar pendiente","Abandonner reprise"));}
    for(auto command:{UiCommand::StartSousVideWithKeg,UiCommand::CoffeeMenu,UiCommand::Utilities,UiCommand::Settings})screen.menu_data.append(command);
    if(stopped()){HeadUiEvent clear;clear.command=static_cast<UiCommand>(LocalClearStop);screen.menu_data.append(clear,text("Clear stop","Quitar parada","Effacer arret"));}
    const unsigned due=engine_?engine_->care_due():0;
    if(!stopped()&&(due&api::CleanDue))screen.menu_data.append(UiCommand::StartClean);
    else if(!stopped()&&(due&api::RinseDue))screen.menu_data.append(UiCommand::StartRinse);
    const char* title=text("Pico Z - local","Pico Z - local","Pico Z - local");
    if(due&api::CleanDue)title=text("Cleaning due","Toca limpieza","Nettoyage requis");
    else if(due&api::RinseDue)title=text("Rinse due","Toca enjuague","Rincage requis");
    page_=Page::Home;return screen.menu_screen(title)==HeadScreenResult::Ok;
}
bool Adapter::show_settings(){
    auto& screen=head_.screens;screen.clear_data();
    for(auto command:{UiCommand::ConfigureNetwork,UiCommand::ConfigureUnits,UiCommand::ConfigureLanguage,UiCommand::ConfigureSlideshowDetail,UiCommand::ConfigureSlideshowCycle})
        if(!screen.menu_data.append(command))return false;
    HeadUiEvent about;
    about.command=static_cast<UiCommand>(LocalAbout);
    if(!screen.menu_data.append(about,text("About","Acerca de","A propos"))||
       !screen.menu_data.append(UiCommand::Exit))return false;
    page_=Page::Original;return screen.menu_screen(text("Settings","Ajustes","Parametres"))==HeadScreenResult::Ok;
}
bool Adapter::show_about(){
    auto& screen=head_.screens;
    screen.clear_data();
    HeadUiEvent details, pairing, back;
    details.command=UiCommand::About;
    pairing.command=static_cast<UiCommand>(LocalPairing);
    back.command=UiCommand::Settings;
    if(!screen.menu_data.append(details,text("Machine details","Datos de maquina","Infos machine"))||
       (ports_.pair_prompt&&ports_.pair_decide&&!screen.menu_data.append(pairing,text("Pair with Hub","Enlazar con Hub","Jumeler au Hub")))||
       !screen.menu_data.append(back,text("Back","Atras","Retour")))return false;
    page_=Page::Original;
    return screen.menu_screen(text("About","Acerca de","A propos"))==HeadScreenResult::Ok;
}
bool Adapter::show_pairing(){
    if(!ports_.pair_prompt||!ports_.pair_decide)return show_about();
    auto& screen=head_.screens;
    screen.clear_data();
    char code[9]={};
    pairing_ticket_=ports_.pair_prompt(ports_.context,ports_.millis(ports_.context),code);
    HeadUiEvent cancel, approve;
    cancel.command=static_cast<UiCommand>(LocalPairCancel);
    approve.command=static_cast<UiCommand>(LocalPairApprove);
    cancel.index=approve.index=pairing_ticket_;
    if(!screen.menu_data.append(cancel,pairing_ticket_?text("Cancel","Cancelar","Annuler"):text("Back","Atras","Retour"))||
       (pairing_ticket_&&!screen.menu_data.append(approve,text("Approve","Aprobar","Autoriser"))))return false;
    // Discard both portions of an earlier queued REST turn-and-click before
    // showing the code. Only new physical events can operate this screen.
    encoder_click_=false;
    head_.button_events.pending=false;
    head_.encoder_events.pending=false;
    page_=Page::Pairing;
    const char* lines[]={pairing_ticket_?text("Compare Hub code","Compare codigo","Comparer le code"):
        text("Request from Hub","Iniciar en el Hub","Demander du Hub"),code};
    HeadScreenRequest request;
    request.title=text("Pair with Hub","Enlazar con Hub","Jumeler au Hub");
    request.screen.layout=17;request.has_body=true;
    request.body.lines=lines;request.body.count=pairing_ticket_?2:1;
    request.menu_rows=screen.menu_data.count;request.menu_vertical=2;
    return screen.show(request)==HeadScreenResult::Ok;
}
bool Adapter::show_network(){
    auto& screen=head_.screens;screen.clear_data();
    for(auto command:{UiCommand::WiFiScan,UiCommand::WiFiReconnect,UiCommand::WiFiDetails,UiCommand::Exit})
        if(!screen.menu_data.append(command))return false;
    page_=Page::Original;return screen.menu_screen("Wi-Fi")==HeadScreenResult::Ok;
}
bool Adapter::network_message(const char* title){
    auto& screen=head_.screens;
    screen.clear_data();
    return screen.menu_data.append(UiCommand::ConfigureNetworkAsBack,text("Back","Atras","Retour"))&&
           screen.menu_screen(title)==HeadScreenResult::Ok;
}
bool Adapter::start_wifi_scan(){
    // A foreground scan must not change the manager's connection target.
    auto& io=core_.wifi.services;
    WifiAction action(WifiEffect::Scan);action.argument=1;action.fourth=300;
    std::uint32_t count=0;
    scanned_count_=0;
    if(!io.perform||!io.perform(io.context,action,count))return show_wifi_results(-2);
    if(static_cast<std::int32_t>(count)!=-1)return show_wifi_results(static_cast<std::int32_t>(count));
    if(!network_message(text("Searching...","Buscando...","Recherche...")))return false;
    scan_started_=head_.screens.state.reset_time;
    page_=Page::Scanning;
    return true;
}
bool Adapter::show_wifi_results(std::int32_t count){
    scanned_count_=0;
    page_=Page::Networks;
    auto& screen=head_.screens;
    screen.clear_data();
    for(std::int32_t i=0;i<count&&i<256;++i){
        HeadWifiNetwork ap;
        if(!head_.services.scan_network||!head_.services.scan_network(head_.services.context,i,ap))
            return network_message(text("Scan failed","Error de escaneo","Echec recherche"));
        if(ap.rssi < -84 || !ap.ssid[0] || ap.ssid.back())continue;
        // UART and OLED share the same provisioning grammar.
        const auto end=std::find(ap.ssid.begin(),ap.ssid.end(),'\0');
        if(std::any_of(ap.ssid.begin(),end,[](unsigned char byte){return byte<32||byte==127;}))continue;
        std::size_t row=0;
        while(row<scanned_count_ && std::strcmp(scanned_[row].ssid.data(),ap.ssid.data()))++row;
        if(row==scanned_count_){
            if(scanned_count_==scanned_.size())continue;
            ++scanned_count_;
        } else if((scanned_[row].encrypted&&!ap.encrypted)||
                  (scanned_[row].encrypted==ap.encrypted&&scanned_[row].rssi>=ap.rssi))continue;
        scanned_[row]=ap;
    }
    for(std::size_t i=0;i<scanned_count_;++i){
        const auto& ap=scanned_[i];
        HeadUiEvent event;event.command=UiCommand::WiFiSsidGetKey;event.flags=1;event.index=i;
        char label[52];
        ::snprintf(label,sizeof label,"%s%s%s",ap.encrypted?"\xc2\x82 ":"",ap.rssi<-75?"[weak] ":"",ap.ssid.data());
        if(!screen.menu_data.append(event,label))return false;
    }
    if(!scanned_count_)return network_message(count<0?text("Scan failed","Error de escaneo","Echec recherche"):
                                                text("No networks","Sin redes","Aucun reseau"));
    return screen.menu_data.append(UiCommand::ConfigureNetworkAsBack,text("Back","Atras","Retour"))&&
           screen.menu_screen(ui_localized_text(UiTextCategory::TextTitle,57,screen.language))==HeadScreenResult::Ok;
}
bool Adapter::select_wifi(const HeadUiEvent& event){
    if(page_!=Page::Networks||!(event.flags&1)||event.index>=scanned_count_)return true;
    page_=Page::Original;
    const auto& ap=scanned_[event.index];
    auto& editor=head_.text.state;
    editor.credentials.ssid=ap.ssid;editor.credentials.password.fill(0);
    editor.awaiting_wifi_password=ap.encrypted;
    if(!ap.encrypted){
        CoreCommand command;command.id=CoreCommandId::WiFiConnect;command.credentials=editor.credentials;
        return head_.services.post_core&&head_.services.post_core(head_.services.context,HeadUiCoreEnvelope(command,nullptr));
    }
    HeadScreenRequest request;request.screen.layout=8;request.screen.input_kind=2;
    request.title=ui_localized_text(UiTextCategory::TextTitle,28,head_.screens.language);
    return head_.screens.show(request)==HeadScreenResult::Ok;
}
bool Adapter::show_recovery(bool discard){
    auto& screen=head_.screens;screen.clear_data();HeadUiEvent confirm,back;
    confirm.command=static_cast<UiCommand>(discard?LocalDiscardConfirmed:LocalResume);
    back.command=static_cast<UiCommand>(LocalHome);
    if(!screen.menu_data.append(confirm,discard?text("Discard saved brew","Descartar infusion","Abandonner brassage"):
        text("Water/filter checked","Agua/filtro revisado","Eau/filtre verifies"))||
       !screen.menu_data.append(back,text("Back","Atras","Retour")))return false;
    page_=Page::Original;
    return screen.menu_screen(discard?text("Discard interrupted?","Descartar pendiente?","Abandonner reprise?"):
        text("Check water / filter","Revisar agua/filtro","Verifier eau/filtre"))==HeadScreenResult::Ok;
}
api::Result Adapter::resume_interrupted(bool confirmed){
    if(!confirmed)return {400,"confirmation_required","Check the vessel, water and filter, then explicitly confirm resumption"};
    if(!idle()||stopped())return busy();
    if(core_.system_error||core_.msp_state.errors||core_.msp_state.id!=Z_MSP_APP_RUNNING)return unavailable();
    if(core_.safeties.filter_missing)return {409,"filter_missing","Insert the filter before resuming"};
    const auto result=recovery_.restore(core_.program,core_.auxiliary.pressure_pa);if(!result.ok())return result;
    if(!wake_standby())return unavailable();
    selected_recipe_=recovery_.recipe();owned_recipe_=true;elapsed_before_resume_=recovery_.elapsed();
    if(!queue_program(24))return busy();
    resuming_=true;page_=Page::Original;engine_->event("recovery-resume","Explicitly queued saved native brew continuation");return {202};
}
api::Result Adapter::discard_interrupted(){
    if(!idle()||recovery_active_||resuming_)return busy();
    const auto result=recovery_.clear();if(!result.ok())return result;
    engine_->event("recovery-discard","Interrupted brew checkpoint discarded");show_home();return {202};
}
void Adapter::poll_recovery(){
    if(!recovery_active_)return;
    auto& runtime=core_.runtime;
    const bool finished=runtime.stopping||runtime.state.screen==14||runtime.state.screen==24||
        ((core_.core.state.id==CoreStateId::Idle||core_.core.state.id==CoreStateId::StandBy)&&
         (runtime.state.id==RuntimeStateId::Idle||runtime.state.id==RuntimeStateId::Standby));
    if(finished){
        recovery_active_=false;const auto cleared=recovery_.clear();
        if(!cleared.ok())engine_->event("recovery-error",cleared.error);
        return;
    }
    if(stopped()||core_.system_error||core_.core.state.id==CoreStateId::Error){recovery_active_=false;return;}
    if(runtime.state.id!=RuntimeStateId::Running||!runtime.state.instruction_count)return;
    const auto now=ports_.millis(ports_.context);
    if(runtime.state.instruction_count==recovery_instruction_&&now-recovery_date_<30000)return;
    const auto saved=recovery_.capture(runtime,core_.heater.state.target_celsius,now,elapsed_before_resume_);
    if(!saved.ok()){
        stop("recovery-storage");engine_->event("recovery-error",saved.error);return;
    }
    recovery_date_=now;recovery_instruction_=runtime.state.instruction_count;
}
bool Adapter::show_recipes(){
    if(!engine_)return false;
    auto& screen=head_.screens;screen.clear_data();recipe_ids_={};
    const auto count=std::min(api::RecipeCapacity,engine_->recipe_count());
    for(std::size_t i=0;i<count;++i){api::Recipe recipe;const auto result=engine_->recipe_at(i,recipe);if(!result.ok())return false;
        std::memcpy(recipe_ids_[i].data(),recipe.id,sizeof recipe.id);HeadUiEvent item;item.command=static_cast<UiCommand>(SelectLocalRecipe);item.index=i;
        if(!screen.menu_data.append(item,recipe.name))return false;}
    HeadUiEvent back;back.command=static_cast<UiCommand>(LocalHome);screen.menu_data.append(back,text("Back","Atras","Retour"));
    page_=Page::Recipes;return screen.menu_screen(count?text("Local recipes","Recetas locales","Recettes locales"):text("No local recipes","Sin recetas","Aucune recette"))==HeadScreenResult::Ok;
}
bool Adapter::handle_head(HeadEventKind kind){
    // Background station scans are independent from the explicit picker.
    if(kind==HeadEventKind::Wifi&&head_.wifi_events.pending&&
       (head_.wifi_events.value.type==WifiEventType::ScanStarted||head_.wifi_events.value.type==WifiEventType::ScanCompleted)){
        CoreWifiEvent event;head_.wifi_events.take(event);return true;
    }
    if(kind==HeadEventKind::UiCommand&&head_.ui_events.pending){const auto event=head_.ui_events.value;const auto id=static_cast<std::uint32_t>(event.command);
        if(id>=LocalRecipes&&id<=LocalPairApprove){HeadUiEvent consumed;head_.ui_events.take(consumed);
            // Match HeadUiCommands::handle's shared menu-command prologue.
            if(!head_.services.alarm||!head_.services.alarm(head_.services.context,false))return false;
            head_.ui.transient.active=false;head_.ui.transient.deadline=0;
            if(id==LocalAbout)return show_about();
            if(id==LocalPairing)return show_pairing();
            if(id==LocalPairApprove||id==LocalPairCancel){
                if(page_!=Page::Pairing||event.index!=pairing_ticket_)return true;
                if(pairing_ticket_&&ports_.pair_decide)
                    ports_.pair_decide(ports_.context,ports_.millis(ports_.context),pairing_ticket_,id==LocalPairApprove);
                pairing_ticket_=0;
                return show_about();
            }
            if(id==LocalRecipes)return show_recipes();
            if(id==LocalHome)return show_home();
            if(id==LocalClearStop){page_=Page::Home;clear_stop();return show_home();}
            if(id==LocalRecovery||id==LocalDiscard)return show_recovery(id==LocalDiscard);
            if(id==LocalResume||id==LocalDiscardConfirmed){page_=Page::Home;
                const auto result=id==LocalResume?resume_interrupted(true):discard_interrupted();
                if(!result.ok()){engine_->event("recovery-error",result.error);return show_home();}return true;}
            if(event.index>=recipe_ids_.size()||!engine_)return false;
            api::Recipe recipe;auto result=engine_->recipe_get(recipe_ids_[event.index].data(),recipe);
            if(result.ok())result=load_recipe(recipe,false);
            if(!result.ok()){engine_->event("recipe-error",result.error?result.error:"load_failed");return show_recipes();}return true;
        }
        if(event.command!=UiCommand::WiFiSsidGetKey)page_=Page::Original;
        if(event.command==UiCommand::Settings||event.command==UiCommand::ConfigureAzure||event.command==UiCommand::SetAzureEnabled||event.command==UiCommand::SetAzureDisabled||
           event.command==UiCommand::ConfigureEthernet||event.command==UiCommand::EthernetConnect){
            HeadUiEvent consumed;head_.ui_events.take(consumed);
            if(!head_.services.alarm||!head_.services.alarm(head_.services.context,false))return false;
            head_.ui.transient.active=false;head_.ui.transient.deadline=0;return show_settings();}
    }
    return original_head_.handle_event(original_head_.context,kind);
}
api::Result Adapter::snapshot(api::Snapshot& out){
    out={};out.uptime_ms=ports_.millis?ports_.millis(ports_.context):0;
    out.ready=begun_&&!core_.system_error&&core_.msp_state.id==Z_MSP_APP_RUNNING&&!core_.msp_state.errors&&core_.core.state.id!=CoreStateId::Off&&core_.core.state.id!=CoreStateId::Booting&&core_.core.state.id!=CoreStateId::Error;
    out.busy=!idle();out.stopped=stopped();out.stop_reason=stop_reason_;out.state=to_string(core_.core.state.id);
    out.device_state=static_cast<std::uint32_t>(core_.core.state.id);out.phase=static_cast<std::uint32_t>(core_.runtime.state.instruction.opcode);
    out.controller_status=static_cast<std::uint32_t>(core_.runtime.state.id);out.compartment=core_.runtime.requested_valve;
    HeaterSensors sensors;HeaterState heater;SafetyState safety;
    {ControlGuard guard(core_.ports.control);sensors=core_.temperatures;heater=core_.heater.state;safety=core_.safeties;}
    auto temperature=[&](unsigned i){api::Reading reading;reading.value=sensors.temperatures[i].celsius*1.8+32;reading.valid=out.ready&&sensors.temperatures[i].updated_ms!=0&&std::isfinite(reading.value)&&out.uptime_ms-sensors.temperatures[i].updated_ms<=5000;return reading;};
    out.wort_f=temperature(1);out.thermoblock_f=temperature(0);out.temperature_f=out.wort_f;
    // The native PID retains its last setpoint after stopping. The v1 API
    // uses zero for an unheated phase, including before the first brew.
    out.target_f.value=heater.started ? heater.target_celsius*1.8+32 : 0;
    out.target_f.valid=out.ready&&std::isfinite(out.target_f.value);
    out.pressure_kpa.value=core_.auxiliary.pressure_pa/1000;out.pressure_kpa.valid=out.ready&&std::isfinite(out.pressure_kpa.value)&&core_.auxiliary.pressure_pa>0&&core_.auxiliary.pressure_ms!=0&&out.uptime_ms-core_.auxiliary.pressure_ms<=5000;
    static const char* const ids[]={"thermoblock","wort","drain","ambient"};
    static const char* const labels[]={"Thermoblock","Wort","Drain","Ambient"};
    for(unsigned i=0;i<4;++i){auto& sensor=out.sensors[out.sensor_count++];sensor.id=ids[i];sensor.label=labels[i];sensor.kind=api::SensorKind::Temperature;sensor.unit=api::Unit::Fahrenheit;sensor.reading=temperature(i);}
    auto& pressure=out.sensors[out.sensor_count++];pressure.id="ambient-pressure";pressure.label="Ambient pressure";pressure.kind=api::SensorKind::Pressure;pressure.unit=api::Unit::Kilopascal;pressure.reading=out.pressure_kpa;
    out.step_seconds=static_cast<std::int32_t>(core_.runtime.state.instruction_left_seconds);
    const bool local_brew=owned_recipe_&&core_.core.state.program_id==24&&core_.core.state.session_type==SessionType::LocalBrew;
    out.step_count=local_brew?selected_recipe_.step_count:0;
    // The OEM counter includes preparation, completion, and repeated resume
    // labels. Only the saved recipe string reference identifies an API step.
    const auto reference=core_.runtime.saved_step_reference;
    out.current_step=local_brew&&reference>=1&&reference<=out.step_count?static_cast<std::int32_t>(reference-1):-1;
    copy(out.recipe_name,core_.commands.session.name.data());copy(out.step_name,core_.runtime.state.step_name);
    out.filter_known=out.ready;out.filter_inserted=!safety.filter_missing;
    const auto& menu=head_.encoder.state.menu;out.encoder_position=static_cast<std::int32_t>(menu.selected);out.encoder_max=menu.count?static_cast<std::int32_t>(menu.count-1):0;
    out.recording=recordable(core_.core.state.program_id)&&(core_.core.state.id==CoreStateId::Starting||core_.core.state.id==CoreStateId::Running||core_.core.state.id==CoreStateId::Stopping);
    if(out.recording)native_prepared_=false;
    out.session_token=session_token_;out.command=out.recording?(core_.core.state.session_type==SessionType::LocalBrew?"brew":"program"):"";
    recovery_.describe(out.recovery,!recovery_active_&&!resuming_);
    static const char* const pump_ids[]={"keg-pump","drain-pump","hex-pump"};
    static const char* const pump_labels[]={"Keg pump","Drain pump","HEX pump"};
    for(unsigned i=0;i<3;++i){auto& actuator=out.actuators[out.actuator_count++];actuator.id=pump_ids[i];actuator.label=pump_labels[i];actuator.kind=api::ActuatorKind::Pump;actuator.valid=out.ready&&core_.msp_state.id==Z_MSP_APP_RUNNING;actuator.enabled=(core_.msp_state.pumps&(0x10u<<i))!=0;}
    auto& heat=out.actuators[out.actuator_count++];heat.id="heater";heat.label="Heater";heat.kind=api::ActuatorKind::Heater;heat.valid=out.ready;heat.enabled=heater.enabled&&!stopped();heat.supports_level=true;heat.level.valid=heat.valid;heat.level.value=stopped()?0:std::max(0.0,std::min(100.0,heater.output_ticks));
    return {};
}
api::Result Adapter::read_settings(api::Settings& out){
    out.temperature_unit=core_.eeprom.data.use_imperial?'F':'C';out.detailed_status=core_.eeprom.data.show_detail!=0;
    out.auto_cycle=!core_.eeprom.data.slideshow_cycle_static;
    copy(out.language,text("en","es","fr"));return {};
}
api::Result Adapter::validate_settings(const api::Settings& value,std::uint32_t changed){
    if(changed&~std::uint32_t(api::TemperatureUnit|api::Language|api::DetailedStatus|api::AutoCycle))return {400,"invalid_settings","Unsupported setting"};
    if((value.temperature_unit!='F'&&value.temperature_unit!='C')||(std::strcmp(value.language,"en")&&std::strcmp(value.language,"es")&&std::strcmp(value.language,"fr")))return {400,"invalid_settings","Unsupported preference value"};
    return {};
}
api::Result Adapter::apply_settings(const api::Settings& value,std::uint32_t changed){
    auto valid=validate_settings(value,changed);if(!valid.ok())return valid;
    auto& settings=core_.eeprom.data;
    if(changed&api::TemperatureUnit)settings.use_imperial=value.temperature_unit=='F';
    if(changed&api::DetailedStatus)settings.show_detail=value.detailed_status;
    if(changed&api::AutoCycle)settings.slideshow_cycle_static=!value.auto_cycle;
    if(changed&api::Language)settings.language=!std::strcmp(value.language,"es")?LanguageType::Spanish:!std::strcmp(value.language,"fr")?LanguageType::French:LanguageType::English;
    head_.refresh_observed();if(page_==Page::Home&&!show_home())return unavailable();return {};
}
api::Result Adapter::display(api::Display& out){
    if(!head_.initialized)return unavailable();
    if(!display_valid_||std::memcmp(frozen_display_.data(),head_.display.pixels.data(),frozen_display_.size())){
        frozen_display_=head_.display.pixels;++display_revision_;if(!display_revision_)++display_revision_;display_valid_=true;}
    out.width=DisplayWidth;out.height=DisplayHeight;out.format=api::DisplayFormat::RowGray4Msb;out.rotation=0;out.total_bytes=frozen_display_.size();out.revision=display_revision_;return {};
}
api::Result Adapter::read_display(std::uint32_t revision,std::size_t offset,std::uint8_t* data,std::size_t size){
    if(!display_valid_||revision!=display_revision_)return {409,"display_changed","Refresh the display"};
    if(!data||offset>frozen_display_.size()||size>frozen_display_.size()-offset)return {400,"invalid_display_page","Invalid display byte range"};
    std::memcpy(data,frozen_display_.data()+offset,size);return {};
}
}} // namespace zseries::standalone
