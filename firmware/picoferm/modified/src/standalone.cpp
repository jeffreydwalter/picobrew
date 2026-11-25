#include "picoferm/version.hpp"
#include "picoferm/standalone.hpp"
#include <cmath>
#include <cstring>

namespace picoferm { namespace standalone {
namespace {
namespace api=picobrew::standalone;
const api::CommandDescriptor commands[]={
    {"ack-sanitize-seal",0,0},{"start",0,0},{"stop",0,0},{"calibrate",api::Confirm,api::Confirm},
    {"sample",0,0},{"set-sampling-interval",api::Value,api::Value}
};
bool due(std::uint32_t now,std::uint32_t deadline){return now-deadline<0x80000000u;}
api::Result busy(){return {409,"busy","Wait for the current acquisition or stop the fermentation session first"};}
api::Result unavailable(){return {503,"sensor_unavailable","A verified sensor measurement is required"};}
}
Controller::Controller(SensorIo sensors,api::Storage storage,api::Clock clock) noexcept
    :acquisition_(sensors),configuration_(storage),clock_(clock){}
bool Controller::identity(const char* value) noexcept {
    if(begun_||!value||!*value||std::strlen(value)>=sizeof(uid_))return false;
    for(const char* p=value;*p;++p)if(static_cast<unsigned char>(*p)<33||static_cast<unsigned char>(*p)>126)return false;
    std::strcpy(uid_,value);return true;
}
std::uint32_t Controller::now()const noexcept{return clock_.millis?clock_.millis(clock_.context):0;}
void Controller::event(const char* kind,const char* detail){if(engine_)engine_->event(kind,detail);}
api::DeviceServices Controller::services() noexcept {
    api::DeviceServices out;out.context=this;
    out.identity=[](void* p){auto& self=*static_cast<Controller*>(p);api::Identity id;
        id.model="picoferm";id.model_name="PicoFerm";id.model_family="picoferm";id.firmware_version=PICOFERM_VERSION;id.device_uid=self.uid_;return id;};
    out.capabilities=[](void*){api::Capabilities caps;caps.flags=api::Temperature|api::Pressure|api::Battery|api::Calibration|api::Fermentation|api::IntermittentWifi;
        caps.settings=api::TemperatureUnit;caps.commands=commands;caps.command_count=sizeof(commands)/sizeof(*commands);return caps;};
    out.snapshot=[](void* p,api::Snapshot& value){return static_cast<Controller*>(p)->snapshot(value);};
    out.execute=[](void* p,const api::Command& value){return static_cast<Controller*>(p)->execute(value);};
    out.idle=[](void* p){return static_cast<Controller*>(p)->idle();};
    out.read_settings=[](void* p,api::Settings& value){value.temperature_unit=static_cast<Controller*>(p)->temperature_unit_;return api::Result();};
    out.validate_settings=[](void* p,const api::Settings& value,std::uint32_t changed){return static_cast<Controller*>(p)->validate_settings(value,changed);};
    out.apply_settings=[](void* p,const api::Settings& value,std::uint32_t changed){return static_cast<Controller*>(p)->apply_settings(value,changed);};
    out.history_interval_ms=[](void* p){return static_cast<Controller*>(p)->configuration_.value().sampling_seconds*1000u;};
    return out;
}
api::Result Controller::begin(const std::uint8_t* original,std::size_t size){
    if(begun_||!clock_.millis||!uid_[0]||!engine_)return {503,"missing_binding","The standalone controller is not fully bound"};
    const auto loaded=configuration_.load(original,size);
    recording_=calibrating_=false;session_token_=seen_sequence_=0;seen_fault_=SensorFault::None;
    begun_=true;acquisition_.begin(now());request_sample(false);
    next_sample_=now()+configuration_.value().sampling_seconds*1000u;
    return loaded;
}
bool Controller::measured()const noexcept{
    const auto& value=acquisition_.sample();
    // Keep readings across an in-flight acquisition, but do not present stale
    // last-good values after a fault or a missed full sampling interval.
    return value.valid&&acquisition_.state()!=SensorState::Fault&&
        now()-value.measured_at<=configuration_.value().sampling_seconds*1000u+5000u;
}
bool Controller::request_sample(bool recover){
    if(recover&&acquisition_.state()==SensorState::Fault)acquisition_.begin(now());
    return acquisition_.request(now());
}
void Controller::poll(){
    if(!begun_)return;
    const auto clock=now();acquisition_.poll(clock);
    if(acquisition_.fault()!=seen_fault_){
        seen_fault_=acquisition_.fault();
        if(seen_fault_!=SensorFault::None){calibrating_=false;next_sample_=clock+configuration_.value().sampling_seconds*1000u;
            event("sensor-error",BoundedAcquisition::fault_name(seen_fault_));}
    }
    const auto& sample=acquisition_.sample();
    if(sample.valid&&sample.sequence!=seen_sequence_){
        seen_sequence_=sample.sequence;next_sample_=clock+configuration_.value().sampling_seconds*1000u;
        if(calibrating_){
            calibrating_=false;
            if(!CalibrationStore::valid_offset(static_cast<std::uint32_t>(sample.pressure_raw)))event("calibration-error","Pressure offset is outside the recovered valid range");
            else{
                auto next=configuration_.value();next.offset=static_cast<std::uint32_t>(sample.pressure_raw);next.calibrated=true;
                const auto saved=configuration_.save(next);
                event(saved.ok()?"calibration":"calibration-error",saved.ok()?"Verified offset stored":saved.error);
            }
        }
        event("sample","Sensor measurement updated");
    }
    if(!acquisition_.busy()&&due(clock,next_sample_)){
        next_sample_=clock+configuration_.value().sampling_seconds*1000u;
        request_sample(true);
    }
}
api::Result Controller::execute(const api::Command& command){
    const api::CommandDescriptor* descriptor=nullptr;
    for(const auto& candidate:commands)if(!std::strcmp(command.name,candidate.name))descriptor=&candidate;
    if(!descriptor||(command.fields&~descriptor->allowed_fields)||(command.fields&descriptor->required_fields)!=descriptor->required_fields)
        return {400,"invalid_command","Command arguments do not match this PicoFerm"};
    if(!std::strcmp(command.name,"ack-sanitize-seal")){
        if(recording_||calibrating_)return busy();
        if (!engine_) return {503, "not_ready", "Care record unavailable"};
        const auto saved = engine_->record_care(api::CareEvent::SealSanitized);
        return saved.ok() ? api::Result(202) : saved;
    }
    if(!std::strcmp(command.name,"stop")){
        recording_=false;calibrating_=false;if(engine_)engine_->cancel_prepared_session();
        event("stop","Local recording stopped; no session will resume automatically");return {202};
    }
    if(!begun_)return unavailable();
    if(!configuration_.healthy())return {503,"calibration_storage","Repair the modified calibration/settings record before changing operation"};
    if(!std::strcmp(command.name,"sample")){
        if(!request_sample(true))return busy();
        return {202};
    }
    if(!std::strcmp(command.name,"set-sampling-interval")){
        if(!idle())return busy();
        if(!std::isfinite(command.value)||command.value!=std::floor(command.value)||command.value<60||command.value>86400)
            return {400,"invalid_interval","Sampling interval must be an integer from 60 to 86400 seconds"};
        auto next=configuration_.value();next.sampling_seconds=static_cast<std::uint32_t>(command.value);
        const auto saved=configuration_.save(next);if(!saved.ok())return saved;
        next_sample_=now()+next.sampling_seconds*1000u;event("sampling-interval","Verified interval stored");return {202};
    }
    if(!idle())return busy();
    if(!measured())return unavailable();
    if(!std::strcmp(command.name,"calibrate")){
        if(!command.confirm)return {400,"confirmation_required","Remove PicoFerm and its GRAY seal, fully depressurize, then confirm calibration"};
        // Always acquire a fresh pair after the explicit confirmation.
        if(!request_sample(false))return busy();
        calibrating_=true;return {202};
    }
    if(!configuration_.value().calibrated)return {409,"calibration_required","Pressure calibration must be explicitly completed before starting a session"};
    std::uint32_t token=session_token_+1;if(!token)++token;
    const auto reserved=engine_->prepare_session("start","",token);if(!reserved.ok())return reserved;
    if(!request_sample(false)){engine_->cancel_prepared_session();return busy();}
    session_token_=token;recording_=true;engine_->record_care(api::CareEvent::FermUsed);event("fermentation","Local session started");return {202};
}
api::Result Controller::snapshot(api::Snapshot& out){
    out=api::Snapshot();out.uptime_ms=now();const bool available=begun_&&measured();
    out.ready=available&&configuration_.healthy();out.busy=recording_||calibrating_||acquisition_.busy();
    out.recording=recording_;out.session_token=session_token_;out.command=recording_?"start":"";
    if(!begun_||acquisition_.state()==SensorState::Initializing){out.state="initializing";out.device_state=0;}
    else if(!configuration_.healthy()){out.state="storage-fault";out.device_state=6;}
    else if(acquisition_.state()==SensorState::Fault){out.state=BoundedAcquisition::fault_name(acquisition_.fault());out.device_state=5;}
    else if(calibrating_){out.state="calibrating";out.device_state=3;}
    else if(recording_){out.state="fermenting";out.device_state=2;}
    else if(!configuration_.value().calibrated){out.state="calibration-required";out.device_state=4;}
    else {out.state=acquisition_.busy()?"sampling":"idle";out.device_state=1;}
    const auto& sample=acquisition_.sample();
    out.temperature_f.value=sample.temperature_f;out.temperature_f.valid=available;
    out.battery_v.value=sample.battery_v;out.battery_v.valid=available;
    auto& temperature=out.sensors[out.sensor_count++];temperature.id="temperature";temperature.label="Temperature";
    temperature.kind=api::SensorKind::Temperature;temperature.unit=api::Unit::Fahrenheit;temperature.reading=out.temperature_f;
    auto& pressure=out.sensors[out.sensor_count++];pressure.id="pressure_native";pressure.label="Native pressure";
    pressure.kind=api::SensorKind::Pressure;pressure.unit=api::Unit::Raw;
    if(available&&configuration_.value().calibrated){
        const auto& settings=configuration_.value();
        const float difference=sample.pressure_raw>=static_cast<std::int32_t>(settings.offset)?static_cast<float>(sample.pressure_raw-settings.offset):0.0f;
        pressure.reading.value=(difference*10.0f)/static_cast<float>(settings.slope);pressure.reading.valid=std::isfinite(pressure.reading.value);
    }
    auto& battery=out.sensors[out.sensor_count++];battery.id="battery";battery.label="Battery";
    battery.kind=api::SensorKind::Voltage;battery.unit=api::Unit::Volt;battery.reading=out.battery_v;
    // No verified physical unit exists for the OEM native pressure scale.
    // pressure_kpa stays invalid, and no heater/pump/recipe fields are invented.
    return {};
}
api::Result Controller::validate_settings(const api::Settings& value,std::uint32_t changed)const{
    if(!changed||(changed&~std::uint32_t(api::TemperatureUnit))||(value.temperature_unit!='F'&&value.temperature_unit!='C'))
        return {400,"invalid_settings","PicoFerm supports temperature_unit F or C"};
    return {};
}
api::Result Controller::apply_settings(const api::Settings& value,std::uint32_t changed){
    const auto valid=validate_settings(value,changed);if(!valid.ok())return valid;
    temperature_unit_=value.temperature_unit;return {};
}
void Controller::long_press(){
    api::Command command;std::strcpy(command.name,recording_||calibrating_?"stop":"start");
    const auto result=execute(command);if(!result.ok())event("button-error",result.error?result.error:"command_failed");
}
oem::Animation Controller::animation()const noexcept{
    if(!configuration_.healthy()||acquisition_.state()==SensorState::Fault)return oem::Animation::GenericError;
    if(!configuration_.value().calibrated&&!acquisition_.busy())return oem::Animation::FermentationError;
    if(recording_)return oem::Animation::FermentationInProgress;
    if(acquisition_.busy()||calibrating_)return oem::Animation::Busy;
    return oem::Animation::Off;
}
}}
