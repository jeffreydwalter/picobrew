#include "zseries/brew_recovery.hpp"
#include "records.hpp"
#include "recipe_record.hpp"
#include "zseries/archive_program.hpp"
#include <algorithm>
#include <cmath>
#include <cstring>
#include <limits>

namespace zseries { namespace standalone {
using namespace oem;
namespace detail = api::detail;
namespace {
constexpr std::size_t HeaderBytes=96;
constexpr std::size_t MaximumBytes=HeaderBytes+detail::ZRecipeBytes+InstructionStack::capacity*8;
constexpr std::uint32_t Magic=0x31525a50u;
api::Result error(){return {503,"recovery_storage","The interrupted brew checkpoint could not be verified; discard it explicitly before starting another brew"};}
api::Result invalid(){return {503,"recovery_invalid","The native brew continuation cannot be represented safely"};}
bool label(const char* text,std::size_t capacity,bool empty=false){
    if(!empty&&!text[0])return false;
    for(std::size_t i=0;i<capacity;++i){const auto c=static_cast<unsigned char>(text[i]);if(!c)return true;if(c<32||c>126)return false;}
    return false;
}
bool code_valid(const Instruction* code,std::size_t count,const api::Recipe& recipe){
    if(!count||count>InstructionStack::capacity)return false;
    for(std::size_t i=0;i<count;++i){const auto& op=code[i];if(!instruction_valid(op))return false;
        if((op.opcode==OpCode::StepName||op.opcode==OpCode::SubStepName)&&!is_common_string_reference(op.argument)&&op.argument>recipe.step_count)return false;
        // Recovery is exclusively a local recipe program. Never accept unrelated
        // Still, manual-input, test, or indefinite-wait instruction families.
        switch(op.opcode){
        case OpCode::StillScan:case OpCode::StillStart:case OpCode::StSetTargetHL:case OpCode::StFindHeatLoss:
        case OpCode::StFindBoilPoint:case OpCode::StStabilize:case OpCode::StDistille:case OpCode::WaitStillReady:
        case OpCode::WaitSealed:case OpCode::WaitVacuum:case OpCode::WaitT3Stable:case OpCode::WaitT1Rise:
        case OpCode::WaitT3Drop:case OpCode::WaitT2Safety:case OpCode::WaitPumpOk:case OpCode::RinseCycle:
        case OpCode::GetStepParams:case OpCode::GetTemp:case OpCode::GetTime:return false;
        default:break;
        }
    }
    return true;
}
std::uint32_t saturating_add(std::uint32_t a,std::uint32_t b){return b>0xffffffffu-a?0xffffffffu:a+b;}
}
api::Result BrewRecovery::load(api::Storage storage){
    storage_=storage;ready_=present_=false;generation_=0;saved_=Checkpoint{};
    std::uint8_t bytes[MaximumBytes]{};std::size_t length=0;detail::Records records(storage_);
    const auto result=records.load("z-recovery",bytes,sizeof bytes,length,generation_,0,true);
    if(result==detail::RecordResult::Missing){ready_=true;return {};}
    if(result!=detail::RecordResult::Ok)return error();
    if(length<HeaderBytes||detail::get32(bytes)!=Magic||bytes[4]>1)return error();
    if(!bytes[4]){if(length!=HeaderBytes)return error();ready_=true;return {};}
    const auto count=detail::get32(bytes+8);
    if(count>InstructionStack::capacity||length<HeaderBytes+count*8)return error();
    const auto recipe_bytes=length-HeaderBytes-count*8;
    if(!detail::read_recipe_record(saved_.recipe,bytes+HeaderBytes,recipe_bytes))return error();
    saved_.count=count;saved_.elapsed=detail::get32(bytes+12);saved_.seconds=detail::get32(bytes+16);
    saved_.step=static_cast<std::int32_t>(detail::get32(bytes+20));std::memcpy(saved_.step_name,bytes+24,sizeof saved_.step_name);
    if(!detail::recipe_valid(saved_.recipe,true,true,true)||!label(saved_.step_name,sizeof saved_.step_name,true)||saved_.step<-1||saved_.step>=saved_.recipe.step_count||saved_.seconds>0x7fffffffu)return error();
    for(std::size_t i=0;i<count;++i){const auto* p=bytes+HeaderBytes+recipe_bytes+i*8;saved_.code[i]={static_cast<OpCode>(detail::get32(p)),detail::get32(p+4)};}
    if(!code_valid(saved_.code.data(),saved_.count,saved_.recipe))return error();
    ready_=present_=true;return {};
}
api::Result BrewRecovery::save(const Checkpoint& value,bool present){
    std::uint8_t bytes[MaximumBytes]{};detail::put32(bytes,Magic);bytes[4]=present;
    std::size_t length=HeaderBytes;
    if(present){
        if(!detail::recipe_valid(value.recipe,true,true,true)||!code_valid(value.code.data(),value.count,value.recipe))return invalid();
        detail::put32(bytes+8,value.count);detail::put32(bytes+12,value.elapsed);detail::put32(bytes+16,value.seconds);
        detail::put32(bytes+20,static_cast<std::uint32_t>(value.step));std::memcpy(bytes+24,value.step_name,sizeof value.step_name);
        length+=detail::write_recipe_record(bytes+HeaderBytes,value.recipe);
        for(std::size_t i=0;i<value.count;++i){detail::put32(bytes+length,static_cast<std::uint32_t>(value.code[i].opcode));detail::put32(bytes+length+4,value.code[i].argument);length+=8;}
    }
    detail::Records records(storage_);std::uint32_t next=0;
    if(records.save("z-recovery",bytes,length,generation_,next)!=detail::RecordResult::Ok){ready_=false;return error();}
    generation_=next;saved_=value;present_=present;ready_=true;return {};
}
api::Result BrewRecovery::prepare(const api::Recipe& recipe,const ProgramBuffer& program){
    if(!ready_)return error();
    Checkpoint value;value.recipe=recipe;value.count=program.record().view.code_size;
    if(value.count>value.code.size())return invalid();
    std::copy(program.record().view.code,program.record().view.code+value.count,value.code.begin());
    return save(value,true);
}
api::Result BrewRecovery::capture(const ProgramRuntime& runtime,double target,std::uint32_t now,std::uint32_t previous_elapsed){
    if(!ready_||!present_)return error();
    if(runtime.state.id!=RuntimeStateId::Running||runtime.stopping||!std::isfinite(target)||target<0||target>98)return invalid();
    Checkpoint value;value.recipe=saved_.recipe;value.elapsed=saturating_add(previous_elapsed,runtime.state.elapsed_seconds);
    value.step=runtime.saved_step_reference>=1&&runtime.saved_step_reference<=value.recipe.step_count?static_cast<std::int32_t>(runtime.saved_step_reference-1):-1;
    if(runtime.state.step_name)std::strncpy(value.step_name,runtime.state.step_name,sizeof value.step_name-1);
    ProgramRuntime copy=runtime; // Only stack operations below; never tick or dispatch this copy.
    const auto pending=runtime.state.instruction;
    const auto elapsed=(now-runtime.state.instruction_date)/1000;
    const bool complete=pending.opcode==OpCode::WaitFor&&elapsed>=pending.argument;
    if(pending.opcode==OpCode::WaitFor&&!complete)value.seconds=pending.argument-elapsed;
    const auto reference_ok=[&](std::uint32_t reference){return is_common_string_reference(reference)||reference<=value.recipe.step_count;};
    const std::size_t extra=runtime.state.paused?1+(runtime.saved_step_reference?1:0)+(runtime.saved_substep_reference?1:0):
        (complete?0:1)+(runtime.state.screen?1:0)+(runtime.state.pid_active?1:0)+1+(runtime.requested_valve?2:0)+
        (runtime.saved_step_reference?1:0)+(runtime.saved_substep_reference?1:0);
    // Complete OEM diagnostics return from assertions and can drop full-stack
    // pushes. Validate ahead of the pure continuation helper so checkpointing
    // neither runs diagnostics nor loses a resume instruction silently.
    if(copy.stack.size()>InstructionStack::capacity-extra||(!complete&&!instruction_valid(pending))||
       !reference_ok(runtime.saved_step_reference)||!reference_ok(runtime.saved_substep_reference)||
       runtime.requested_valve>9||(runtime.requested_pumps&~0x70u)||runtime.state.screen>102)return invalid();
    if(runtime.state.paused){
        // The original pause already saved its resume sequence in the stack.
        // Preserve the additional acknowledgement before restoring outputs.
        if(copy.stack.push_checked({OpCode::Pause,runtime.state.pause_reason})!=RuntimeResult::Ok)return invalid();
        if(runtime.saved_substep_reference&&copy.stack.push_checked({OpCode::SubStepName,runtime.saved_substep_reference})!=RuntimeResult::Ok)return invalid();
        if(runtime.saved_step_reference&&copy.stack.push_checked({OpCode::StepName,runtime.saved_step_reference})!=RuntimeResult::Ok)return invalid();
    }else if(copy.push_resume(now,complete)!=RuntimeResult::Ok)return invalid();
    const auto append=[&](OpCode opcode,std::uint32_t argument){if(value.count==value.code.size())return false;value.code[value.count++]={opcode,argument};return true;};
    if(!append(OpCode::StopPidKD,0)||!append(OpCode::Setup,(runtime.state.flags&0xfffu)|0x400u)||
       !append(OpCode::RequireFilter,1)||!append(OpCode::SetTime,runtime.state.time_seconds)||
       !append(OpCode::SetTarget,static_cast<std::uint32_t>(target*256)))return invalid();
    if((runtime.state.flags&~0xfffu)&&!append(OpCode::FlagSet,runtime.state.flags&~0xfffu))return invalid();
    bool warmup=runtime.state.pid_active;
    for(std::size_t i=copy.stack.size();i;--i){const auto instruction=copy.stack.at(i-1);
        if(warmup&&instruction.opcode==OpCode::StartPid){
            // Reheat/cool before restarting a saved timed hold. The original
            // in-RAM pause continuation alone cannot account for an outage.
            if(!append(OpCode::ReachTarget,static_cast<std::uint32_t>(target*256)))return invalid();
            if(runtime.saved_substep_reference&&!append(OpCode::SubStepName,runtime.saved_substep_reference))return invalid();
            warmup=false;
        }else if(!append(instruction.opcode,instruction.argument))return invalid();
    }
    return save(value,true);
}
api::Result BrewRecovery::restore(ProgramBuffer& buffer,double pressure) const {
    if(!ready_)return error();
    if(!present_)return {409,"no_recovery","No interrupted brew is retained"};
    if(buffer.start(SessionType::LocalBrew,0,saved_.recipe.name)!=ProgramBuildResult::Ok)return invalid();
    for(std::size_t i=0;i<saved_.recipe.step_count;++i)if(buffer.append_string(saved_.recipe.steps[i].name)!=ProgramBuildResult::Ok)return invalid();
    if(buffer.set_code(saved_.code.data(),saved_.count)!=ProgramBuildResult::Ok||program_validation_error(buffer.record().view))return invalid();
    ProgramParameters parameters;parameters.water={saved_.recipe.z_cloud_water_ml?archive_water_liters(saved_.recipe.z_cloud_water_ml):11.36,Unit::Liter};parameters.resumed_session=true;
    return finish_program(buffer,parameters,pressure,98,0)==ProgramBuildResult::Ok?api::Result():invalid();
}
api::Result BrewRecovery::clear(){
    // Explicit discard repairs both banks, including an invalid previous
    // generation. A verified tombstone prevents resurrection of an old brew.
    if(!ready_){std::uint8_t bytes[MaximumBytes];std::size_t length=0;std::uint32_t previous=0;detail::Records records(storage_);
        const auto result=records.load("z-recovery",bytes,sizeof bytes,length,previous);
        if(result==detail::RecordResult::Io)return error();
        generation_=result==detail::RecordResult::Ok?previous:0;}
    Checkpoint empty;auto result=save(empty,false);if(!result.ok())return result;
    result=save(empty,false);return result;
}
void BrewRecovery::describe(api::Recovery& out,bool available) const noexcept {
    out={};out.supported=true;out.valid=ready_;out.available=ready_&&present_&&available;
    if(!out.available)return;
    std::memcpy(out.recipe_name,saved_.recipe.name,sizeof out.recipe_name);std::memcpy(out.step_name,saved_.step_name,sizeof out.step_name);
    out.current_step=saved_.step;out.step_seconds=static_cast<std::int32_t>(saved_.seconds);out.elapsed_seconds=saved_.elapsed;
}
}}
