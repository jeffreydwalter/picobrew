// Drive the actual common Engine through the actual pinned SPIFFS implementation.
// Reuse the independent fixture's physical flash boundaries and engine device
// ports; production storage replaces the engine fixture's in-memory file map.
#define PICOBREW_SPIFFS_TEST_FLASH_SIZE 0xFB000
#define main storage_fixture_entry
#include "esp8266_storage_test.cpp"
#undef main
#include "engine_bridge.cpp"

namespace {
unsigned read_number(const std::string& json,const char* name) {
    const std::string token=std::string("\"")+name+"\":";
    const std::size_t found=json.find(token);require(found!=std::string::npos,"capacity API numeric field exists");
    return static_cast<unsigned>(std::strtoul(json.c_str()+found+token.size(),nullptr,10));
}
bool bind(World& world,SpiffsStorage& storage) {
    world.engine.reset();
    Clock clock;clock.context=&world;clock.millis=[](void* p){return World::self(p).time;};
    auto device = world.device();
    if (world.model == 0) device.capabilities = [](void* context) {
        auto& w = World::self(context);
        Capabilities caps = w.device().capabilities(context);
        caps.z_program_maker = caps.z_cloud_program_v1 = caps.z_picopak_v1 = true;
        return caps;
    };
    world.engine.reset(new Engine(device,storage.binding(),clock));
    const Result begun=world.engine->begin();
    if(!begun.ok())std::fprintf(stderr,"capacity model=%u clock=%u begin=%u error=%s message=%s spiffs=%d\n",world.model,world.time,begun.status,begun.error?begun.error:"",begun.message?begun.message:"",storage.last_error());
    return begun.ok();
}
struct CapacityResult {
    unsigned model=0,points=0,total=0,used=0,files=0,payload=0,free_blocks=0;
    unsigned reads=0,writes=0,erases=0,next_session=0;
    unsigned partition=0,block=0,recipes=0,steps=0;
    bool full=false,restarted=false,last_page=false,replaced=false;
};
// Exercise the Z recipe store alongside the same channel counts used by its
// production snapshot. Each recipe is replaced to fill both durable generations.
void z_channels(Snapshot& sample) {
    const char* ids[] = {"thermoblock", "wort", "drain", "ambient", "ambient-pressure"};
    const char* pumps[] = {"keg-pump", "drain-pump", "hex-pump", "heater"};
    for (unsigned i=0; i<5; ++i) {
        auto& sensor=sample.sensors[sample.sensor_count++];
        sensor.id=sensor.label=ids[i];
        sensor.reading.valid=true; sensor.reading.value=70;
        sensor.kind=i==4?SensorKind::Pressure:SensorKind::Temperature;
        sensor.unit=i==4?Unit::Kilopascal:Unit::Fahrenheit;
    }
    for (unsigned i=0; i<4; ++i) {
        auto& actuator=sample.actuators[sample.actuator_count++];
        actuator.id=actuator.label=pumps[i]; actuator.valid=true;
        actuator.kind=i==3?ActuatorKind::Heater:ActuatorKind::Pump;
        if (i==3) actuator.supports_level=actuator.level.valid=true;
    }
}
Recipe z_recipe(unsigned slot, unsigned revision) {
    Recipe recipe;
    std::snprintf(recipe.id,sizeof recipe.id,"z-%u",slot);
    std::snprintf(recipe.name,sizeof recipe.name,"Recipe %u rev %u",slot,revision);
    recipe.step_count=revision>=2 ? ZProgramStepCapacity : RecipeStepCapacity;
    // Native cloud, kit, then two generations of the largest persistent format.
    recipe.z_program=revision>=2;
    if (revision==0) recipe.z_cloud_water_ml=16163;
    if (revision==1) {
        recipe.pak.recipe_id=UINT32_MAX; recipe.pak.beer_id=UINT32_MAX;
        recipe.pak.abv=6; recipe.pak.ibu=7; recipe.pak.selected_abv=10; recipe.pak.selected_ibu=10;
    }
    for (unsigned i=0;i<recipe.step_count;++i) {
        auto& step=recipe.steps[i];
        std::snprintf(step.name,sizeof step.name,"Full length step %02u",i);
        step.location=2+i%4;
        if (recipe.z_program) {
            step.z.has_time=true; step.z.target_c=65; step.z.seconds=3600; step.z.drain_seconds=120;
        } else {
            step.target_f=152; step.minutes=60; step.drain_minutes=2;
            if (recipe.pak.recipe_id) {
                step.pak.minimum=0; step.pak.maximum=599; step.pak.action=i%4==3;
            }
        }
    }
    return recipe;
}
void z_library(Engine& engine,bool populate) {
    for (unsigned slot=0;slot<RecipeCapacity;++slot) {
        for (unsigned generation=populate?0:3;generation<4;++generation) {
            const Recipe expected=z_recipe(slot,generation);
            if (populate) require(engine.recipe_store(expected).ok(),"store full Z recipe generation");
            Recipe actual;
            require(engine.recipe_get(expected.id,actual).ok() && actual.step_count==expected.step_count &&
                    !std::strcmp(actual.name,expected.name) && actual.z_cloud_water_ml==expected.z_cloud_water_ml &&
                    actual.z_program==expected.z_program && actual.pak.recipe_id==expected.pak.recipe_id,
                    "full Z recipe catalog readback");
            if (!expected.pak.recipe_id) continue;
            require(actual.pak.beer_id==UINT32_MAX && actual.pak.selected_abv==10 && actual.pak.selected_ibu==10,
                    "complete kit strength controls read back");
            for (unsigned i=0;i<actual.step_count;++i)
                require(actual.steps[i].pak.minimum==0 && actual.steps[i].pak.maximum==599 &&
                        actual.steps[i].pak.action==expected.steps[i].pak.action,"complete kit routing options read back");
        }
    }
}
CapacityResult exercise(unsigned model,unsigned region,unsigned logical_block) {
    flash_size=region;block_size=logical_block;
    flash.assign(flash_size,0xff);read_calls=write_calls=erase_calls=0;
    {Raw raw;raw.format_fixture();}
    CapacityResult result;result.model=model;result.partition=region;result.block=logical_block;
    {
        SpiffsStorage storage(geometry());require(storage.begin(),"selected SDK partition geometry mounts");
        World world(model);
        if (!model) z_channels(world.snapshot);
        require(bind(world,storage),"actual engine begin on SPIFFS");
        if (!model) {
            z_library(*world.engine,true);
            result.recipes=world.engine->recipe_count();result.steps=ZProgramStepCapacity;
        }
        const char* start=model?"{\"command\":\"start\"}":"{\"command\":\"brew\",\"recipe_id\":\"z-0\"}";
        world_request(&world,PICO_HTTP_POST,"/api/v1/commands",start,3);
        require(world.status==202,"actual engine session starts");
        for(unsigned sample=0;sample<4224&&world.engine->persistence_ready();++sample)
            world_tick(&world,15000,1);
        world_request(&world,PICO_HTTP_GET,"/api/v1/history",nullptr,3);
        require(world.status==200,"history API remains readable");
        result.points=read_number(world.output,"record_count");
        result.full=result.points==4224&&world.engine->persistence_ready();
        char path[80];std::snprintf(path,sizeof(path),"/api/v1/history/samples/%u?id=1",result.points>1?result.points-2:0);
        world_request(&world,PICO_HTTP_GET,path,nullptr,3);result.last_page=world.status==200;
        world.engine.reset();storage.end();
        {Raw raw;require(raw.mount()==SPIFFS_OK,"capacity accounting mount");u32_t total=0,used=0;
         require(SPIFFS_info(&raw.fs,&total,&used)==SPIFFS_OK,"actual SPIFFS accounting");
         result.total=total;result.used=used;result.free_blocks=raw.fs.free_blocks;
         spiffs_DIR directory;spiffs_dirent entry;require(SPIFFS_opendir(&raw.fs,"/",&directory)!=nullptr,"capacity directory");
         while(SPIFFS_readdir(&directory,&entry)){++result.files;result.payload+=entry.size;}
         require(SPIFFS_closedir(&directory)==SPIFFS_OK,"capacity directory close");}
        require(storage.begin(),"capacity remount");
        world.snapshot.recording=world.snapshot.busy=false;world.command.clear();world.state="idle";
        const bool began=bind(world,storage);world_request(&world,PICO_HTTP_GET,"/api/v1/history",nullptr,3);
        if (!model && began) z_library(*world.engine,false);
        result.restarted=began&&world.status==200&&read_number(world.output,"record_count")==result.points;
        world_request(&world,PICO_HTTP_GET,path,nullptr,3);result.last_page=result.last_page&&world.status==200;
        if(result.full) {
            // A full retained history must still leave enough space to replace
            // catalogue/schema/chunk banks and start the next session.
            world_request(&world,PICO_HTTP_POST,"/api/v1/commands",start,3);
            require(world.status==202,"new session after full history");
            world_tick(&world,15000,25);
            world_request(&world,PICO_HTTP_GET,"/api/v1/history",nullptr,3);
            result.next_session=read_number(world.output,"id");
            result.replaced=world.status==200&&world.engine->persistence_ready()&&result.next_session==2;
        }
        world.engine.reset();storage.end();
    }
    result.reads=read_calls;result.writes=write_calls;result.erases=erase_calls;
    return result;
}
void emit(const CapacityResult& r) {
    std::printf("{\"model\":\"%s\",\"recipe_slots\":%u,\"steps_per_recipe\":%u,\"partition_bytes\":%u,\"page_bytes\":%u,\"block_bytes\":%u,\"points\":%u,\"spiffs_total_bytes\":%u,\"spiffs_used_bytes\":%u,\"spiffs_free_bytes\":%u,\"free_blocks\":%u,\"files\":%u,\"payload_bytes\":%u,\"full_history\":%s,\"restart_preserved\":%s,\"last_page_valid\":%s,\"next_session\":%u,\"replacement_reserve\":%s,\"hal_reads\":%u,\"hal_writes\":%u,\"hal_erases\":%u}",
        r.model==1?"picoferm":r.model==2?"picostill":"z-series",r.recipes,r.steps,r.partition,page_size,r.block,r.points,r.total,r.used,r.total-r.used,r.free_blocks,r.files,r.payload,
        r.full?"true":"false",r.restarted?"true":"false",r.last_page?"true":"false",r.next_session,r.replaced?"true":"false",r.reads,r.writes,r.erases);
}
}
int main() {
#ifdef PICOBREW_ESP32_SPIFFS_SIZING
    const CapacityResult z=exercise(0,0x16F000,4096);
    const CapacityResult still=exercise(2,0x16F000,4096);
    std::fprintf(stderr,"Still ESP32-configuration sizing run complete: %u records, %u used bytes\n",still.points,still.used);
    const CapacityResult reference=exercise(2,0x200000,4096);
    std::fprintf(stderr,"Still 2MiB reference sizing run complete: %u records, %u used bytes\n",reference.points,reference.used);
    std::printf("{\"profiles\":[");emit(still);std::printf(",");emit(reference);std::printf(",");emit(z);
    const bool passed=still.full&&still.restarted&&still.last_page&&still.replaced&&
                      reference.full&&reference.restarted&&reference.last_page&&reference.replaced&&
                      z.full&&z.restarted&&z.last_page&&z.replaced&&z.recipes==RecipeCapacity;
    std::printf("],\"checks\":%u,\"pointer_bytes\":%zu,\"gc_max_runs\":%u,\"metadata_bytes\":%u,\"object_index_alignment\":%u,\"passed\":%s}\n",checks,sizeof(void*),SPIFFS_GC_MAX_RUNS,SPIFFS_OBJ_META_LEN,SPIFFS_ALIGNED_OBJECT_INDEX_TABLES,passed?"true":"false");
#else
    const CapacityResult ferm=exercise(1,0xFB000,8192);
    std::fprintf(stderr,"Ferm capacity run complete: %u records, %u used bytes\n",ferm.points,ferm.used);
    const CapacityResult too_small=exercise(2,0xFB000,8192);
    std::fprintf(stderr,"Still undersized stress run complete: %u records, %u used bytes\n",too_small.points,too_small.used);
    std::printf("{\"profiles\":[");emit(ferm);std::printf(",");emit(too_small);
    std::printf("],\"checks\":%u,\"pointer_bytes\":%zu,\"gc_max_runs\":%u,\"metadata_bytes\":%u,\"passed\":%s}\n",checks,sizeof(void*),SPIFFS_GC_MAX_RUNS,SPIFFS_OBJ_META_LEN,ferm.full&&ferm.restarted&&ferm.last_page&&ferm.replaced&&!too_small.full&&too_small.last_page?"true":"false");
#endif
}
