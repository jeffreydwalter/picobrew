// Standalone Z composition. Uses the recovered hardware/Head/controller with
// explicitly owned local API, storage, offline policy and output inhibition.
#include "application_platform.hpp"
#include "zseries/standalone.hpp"
#include "picobrew/common_web.h"
#include "picobrew/verified_storage.hpp"
#include "picobrew/provisioning.hpp"
#include "network_ports.hpp"
#include "storage_ports.hpp"
#include "msp_bus.hpp"
#include "wired_network.hpp"
#include "factory_flash.hpp"
#include "output_cutoff.hpp"
#include <esp_system.h>
#include "startup.hpp"
#include "station.hpp"
#include <WiFi.h>
#include <esp_heap_caps.h>
#include <esp_task_wdt.h>
#include <cstring>
#include <cstdio>
#include <new>

// Same TCP/IP initialization used by ETH.cpp in the pinned Arduino core.
extern void tcpipInit();

extern "C" const char z_field_identity[64] __attribute__((section(".rodata_custom_desc"),used)) = PB_FIELD_IDENTITY;

namespace zseries { namespace standalone { namespace esp32 {
namespace {
namespace platform=picobrew::standalone::esp32;
constexpr std::uint32_t TaskStackBytes=32768;
api::Clock clock(){api::Clock result;result.millis=[](void*)->std::uint32_t{return ::millis();};return result;}
struct Application {
    oem::esp32::FirmwareApplication firmware;
    platform::SpiffsFiles files;
    api::VerifiedStorage storage;
    Adapter adapter;
    api::Engine engine;
    api::Provisioning provisioning;
    picobrew::esp32_ota::FactoryFlash flash;
    picobrew::esp32_ota::Service update{flash};
    bool update_ready = false, update_store_ready = false;
    std::uint32_t restart_ms = 0;
    platform::HttpSocket listener;
    platform::TelemetrySocket telemetry;
    WiredNetwork ethernet{firmware.system.spi};
    PicoHttpIo http_io{};
    PicoHttpServer http{};
    MspBus bus;
    z_msp_diagnostics diagnostics{};
    oem::WifiServices original_wifi;
    bool controller_ready=false,http_ready=false,fault_reported=false,initializing=true;
    std::uint32_t listen_attempt=0;

    Application():storage(files.io()),adapter(firmware.core,firmware.head),
        engine(adapter.services(),storage.binding(),clock()),provisioning(storage.binding(),this,[](void* p){auto& a=*static_cast<Application*>(p);
            return a.initializing||!a.controller_ready||a.fault_reported||(a.firmware.core.runtime.state.id==oem::RuntimeStateId::Standby&&
                a.firmware.core.core.state.id!=oem::CoreStateId::Starting&&a.firmware.core.core.state.id!=oem::CoreStateId::Running&&a.firmware.core.core.state.id!=oem::CoreStateId::Stopping);}),
        bus(firmware.system.controller_bus){}
    bool bind(){
        ethernet.prepare(firmware.core.ethernet_mac.data());
        original_wifi=firmware.core.wifi.services;
        firmware.core.wifi.services.context=this;
        firmware.core.wifi.services.millis=[](void*)->std::uint32_t{return ::millis();};
        firmware.core.wifi.services.perform=[](void* p,const oem::WifiAction& action,std::uint32_t& result){auto& a=*static_cast<Application*>(p);
            if(action.effect==oem::WifiEffect::BeginConnection){
                if(!action.credentials)return false;
                const int error=platform::connect_station(action.credentials->ssid.data(),action.credentials->password.data());
                result=error?WL_CONNECT_FAILED:WL_IDLE_STATUS;
                if(error)a.engine.event("network-error","Wi-Fi connection request failed; local controller remains available");
                // Failed association/configuration follows the manager's bounded
                // timeout/backoff; it must not abort a running local brew.
                return true;
            }
            if(action.effect==oem::WifiEffect::ResolveHost){if(!action.address)return false;*action.address=0;result=0;return true;}
            return a.original_wifi.perform&&a.original_wifi.perform(a.original_wifi.context,action,result);
        };
        firmware.core.msp.transport=bus.transport();
        // A failed bus transaction must abort setup/update at its next
        // diagnostic boundary, rather than flash every remaining block.
        diagnostics.context=this;
        diagnostics.assertion=[](void* p,const char* file,std::uint32_t line,const char* expression){auto& a=*static_cast<Application*>(p);
            return !a.bus.failed()&&a.firmware.assertion(file,line,expression);};
        diagnostics.log=[](void* p,unsigned level,const char* tag,const char* file,std::uint32_t line,const char* message){auto& a=*static_cast<Application*>(p);
            return !a.bus.failed()&&a.firmware.log(static_cast<oem::ConsoleLevel>(level),tag,file,line,"%s",message);};
        diagnostics.trace=[](void* p,bool begin,const char* tag,const char* file,std::uint32_t line,const char* name){auto& a=*static_cast<Application*>(p);
            return !a.bus.failed()&&a.firmware.scope(begin,tag,file,line,name);};
        diagnostics.assertion_count=[](void* p){return static_cast<Application*>(p)->firmware.diagnostic_state.assertion_count;};
        diagnostics.watchdog_reset=[](void* p){auto& a=*static_cast<Application*>(p);if(a.bus.failed())return false;esp_task_wdt_reset();return true;};
        firmware.core.msp.diagnostics=&diagnostics;
        Ports ports;ports.context=this;
        ports.storage=storage.binding();
        ports.group_boot_nonce=esp_random();
        ports.millis=[](void*)->std::uint32_t{return ::millis();};
        ports.http_poll=[](void* p,std::uint32_t now){static_cast<Application*>(p)->http_poll(now);};
        ports.serial_poll=[](void* p){static_cast<Application*>(p)->serial_poll();};
        ports.lan_ready=[](void* p,std::uint32_t network){auto& app=*static_cast<Application*>(p);
            return network==2 ? app.ethernet.address()!=0 : network==1&&!app.firmware.core.eeprom.data.use_ethernet&&
                WiFi.status()==WL_CONNECTED&&static_cast<std::uint32_t>(WiFi.localIP())!=0;};
        ports.wired_address=[](void* p){return static_cast<Application*>(p)->ethernet.address();};
        ports.apply_interface=[](void* p){auto& app=*static_cast<Application*>(p);
            pico_http_server_disconnect(&app.http);app.telemetry.close();
            if(app.firmware.core.eeprom.data.use_ethernet){
                app.firmware.core.wifi.select_target(oem::WiFiStateId::Idle);
                WiFi.mode(WIFI_OFF);
            }else {WiFi.mode(WIFI_STA);app.apply_network();}
            app.update_capability();};
        ports.save_network=[](void* p,const char* ssid,const char* password){auto& a=*static_cast<Application*>(p);
            char line[104];const int length=::snprintf(line,sizeof line,"wifi %s\t%s",ssid,password);
            if(length<=0||static_cast<std::size_t>(length)>=sizeof line)return false;
            const auto result=a.provisioning.execute(line,static_cast<std::size_t>(length));std::memset(line,0,sizeof line);
            if(!result.ok())return false;
            a.provisioning.take_network_changed();a.sync_network();a.update_capability();return true;};
        ports.reconnect_network=[](void* p){auto& a=*static_cast<Application*>(p);a.apply_network();return true;};
        ports.save_configuration=[](void* p){return static_cast<Application*>(p)->firmware.save_configuration();};
        ports.pair_prompt=[](void*,std::uint32_t now,char code[9]){return pico_http_pairing_prompt(now,code);};
        ports.pair_decide=[](void*,std::uint32_t now,std::uint32_t serial,bool allow){return pico_http_pairing_decide(now,serial,allow);};
        if(!adapter.bind(ports,engine))return false;
        // Establish the known inactive outputs even if a later setup stage
        // fails before the original heater initializer is reached.
        auto& gpio=firmware.core.ports.heater_gpio;
        gpio.configure_output(gpio.context,oem::OriginalHeaterEnablePin);gpio.write_pin(gpio.context,oem::OriginalHeaterEnablePin,false);
        gpio.configure_output(gpio.context,firmware.core.heater.output_pin);gpio.write_pin(gpio.context,firmware.core.heater.output_pin,false);
        return true;
    }
    static bool route(const PicoHttpRequest* req,PicoHttpResponse* res,char* arena,std::size_t capacity,void* ctx){
        auto& self=*static_cast<Application*>(ctx);
        const bool pending=self.update.reboot_pending();
        if(self.update.handle(*req,*res,arena,capacity,self.update_ready?self.firmware.core.identifier.data():nullptr,
                self.update_ready && !self.fault_reported && self.adapter.idle())){
            if(!pending && self.update.reboot_pending()){
                self.adapter.stop("firmware-update");
                force_outputs_off();
                self.restart_ms=::millis();
            }
            return true;
        }
        if(self.adapter.group_http(*req,*res,arena,capacity))return true;
        return self.engine.handle(*req,*res,arena,capacity);
    }
    void configure_http(){
        http_io=listener.io();
        pico_http_server_initialize(&http,&http_io,&listener,route,this,
            pico_common_web_assets,pico_common_web_assets_count);
        pico_http_server_enable_pairing(&http, true);
        pico_http_server_set_asset_io(&http,&pico_common_web_asset_io,nullptr);
        pico_http_server_set_key(&http,provisioning.key(),std::strlen(provisioning.key()));
        engine.set_telemetry_io(telemetry.services());
        listen_attempt=::millis()-1000;
    }
    void http_poll(std::uint32_t now){
        if(!http_ready&&now-listen_attempt>=1000){listen_attempt=now;http_ready=listener.begin();}
        if(http_ready)pico_http_server_poll(&http,now);
    }
    void sync_network(){
        const auto& saved=provisioning.network();auto& data=firmware.core.eeprom.data;
        std::strncpy(data.wifi_ssid.data(),saved.ssid,data.wifi_ssid.size()-1);
        std::strncpy(data.wifi_password.data(),saved.password,data.wifi_password.size()-1);
    }
    void apply_network(){
        if(firmware.core.eeprom.data.use_ethernet||!provisioning.network_configured())return;
        sync_network();oem::WifiCredentials credentials;const auto& network=provisioning.network();
        std::memcpy(credentials.ssid.data(),network.ssid,sizeof network.ssid);
        std::memcpy(credentials.password.data(),network.password,sizeof network.password);credentials.internet_expected=false;
        firmware.core.wifi.connect(credentials);
    }
    void update_capability(){
        update_ready=controller_ready && !fault_reported && update_store_ready && provisioning.key_configured() &&
            !firmware.core.eeprom.data.use_ethernet && provisioning.network_configured() && flash.role()==picobrew::esp32_ota::Role::Application;
        adapter.wireless_capable(update_ready);
    }
    void serial_poll(){
        // This modified profile owns UART input. It never passes provisioning
        // text through the original command echo/logger.
        for(unsigned budget=0;budget<64&&Serial.available()>0;++budget){const int value=Serial.read();if(value<0)break;
            api::Result result;if(provisioning.feed(static_cast<char>(value),result)&&result.message)Serial.println(result.message);}
        if(provisioning.take_key_changed()){update_capability();pico_http_server_disconnect(&http);pico_http_server_set_key(&http,provisioning.key(),std::strlen(provisioning.key()));}
        if(provisioning.take_network_changed()){apply_network();update_capability();}
    }
    void fault(const char* reason){
        if(fault_reported)return;
        fault_reported=true;update_capability();firmware.core.system_error=true;
        std::strncpy(firmware.core.system_error_text.data(),reason,firmware.core.system_error_text.size()-1);
        adapter.stop(reason);engine.event("controller-fault",reason);
        firmware.core.core.transition(oem::CoreStateId::Error);
    }
    bool begin(){
        if(!bind()){Serial.println("Pico Z standalone bindings unavailable");return false;}
        // A misplaced field image must never run the brewer from the protected factory slot.
        flash.inspect();
        const auto* running=esp_ota_get_running_partition();
        if(!running || !std::strcmp(running->label,"pb_recovery"))return false;
        controller_ready=firmware.setup();
        files.mount(); // Failure remains visible; no automatic erase/format.
        const auto provisioned=provisioning.load();
        if(!provisioned.ok())Serial.println(provisioned.message);
        if(!provisioning.network_configured()&&firmware.core.eeprom.data.wifi_ssid[0]){
            api::NetworkCredentials imported;std::strncpy(imported.ssid,firmware.core.eeprom.data.wifi_ssid.data(),sizeof imported.ssid-1);
            std::strncpy(imported.password,firmware.core.eeprom.data.wifi_password.data(),sizeof imported.password-1);
            provisioning.import_network(imported); // Refuses an existing/corrupt modified record.
        }
        const auto stored=engine.begin();
        if(!stored.ok())Serial.println("Pico Z persistent store unavailable; starting sessions is disabled");
        controller_ready=controller_ready&&adapter.begin();
        WiFi.persistent(false);
        tcpipInit(); // Ethernet-only sites also need the SDK TCP/IP thread.
        WiFi.mode(firmware.core.eeprom.data.use_ethernet ? WIFI_OFF : WIFI_STA);
        std::size_t total=0,used=0;
        update_store_ready=stored.ok() && files.capacity(total,used) && total>=1300000u;
        update_capability();
        configure_http();
        provisioning.take_network_changed();apply_network();
        if(!controller_ready)fault(bus.failed()?"controller-io":"controller-setup");
        initializing=false;
        Serial.printf("Pico Z standalone owner %u bytes, task stack %u bytes\r\n",
            static_cast<unsigned>(sizeof(Application)),static_cast<unsigned>(TaskStackBytes));
        return true;
    }
    void poll(){
        const std::uint32_t now=::millis();
        ethernet.poll(firmware.core.eeprom.data.use_ethernet!=0,now);
        if(update.reboot_pending()){
            http_poll(now);
            if(http.client<0 || now-restart_ms>=2000u)esp_restart();
            return;
        }
        if(controller_ready&&!fault_reported){adapter.poll();if(!firmware.tick(now))fault(bus.failed()?"controller-io":"controller-service");}
        else {serial_poll();
            // Keep local diagnosis/provisioning reachable after a controller
            // failure without entering the broken acquisition/MSP stages.
            adapter.tick_wifi(now);
            http_poll(now);esp_task_wdt_reset();
            if(firmware.head.initialized){firmware.observe_head();firmware.head.tick(now);}}
        if(bus.failed())fault("controller-io");
        else if(firmware.core.system_error&&!fault_reported)fault("sensor-or-controller-error");
        engine.poll();
    }
};
// Read from the compiled target object by the build report; it is not a host
// sizeof estimate or a claim about runtime free-heap/stack high-water marks.
extern "C" const std::uint32_t picobrew_z_memory_layout[6]
    __attribute__((used,section(".rodata.picobrew_z_memory")))={
        sizeof(Application),TaskStackBytes,sizeof(oem::CoreApplication),sizeof(oem::HeadApplication),sizeof(Adapter),sizeof(api::Engine)};
void firmware_task(void*){
    void* memory=heap_caps_malloc(sizeof(Application),MALLOC_CAP_INTERNAL|MALLOC_CAP_8BIT);
    if(!memory){Serial.println("Pico Z standalone allocation failed");vTaskDelete(nullptr);return;}
    auto* application=new(memory)Application;
    if(!application->begin()){vTaskDelete(nullptr);return;}
    for(;;){application->poll();::delay(1);}
}
}
bool start_firmware(){
    const int initialized=platform::initialize_arduino();
    // An ESP32 reset can leave the independent MSP running. Establish inactive
    // outputs before allocation/task creation as well as on an NVS failure.
    force_outputs_off();
    const volatile char* marker=z_field_identity;
    if(*marker!='P' || esp_ota_get_app_description()->magic_word!=ESP_APP_DESC_MAGIC_WORD)return false;
    // The modified owner replaces the OEM task, including its console setup.
    // Open UART before any allocation/task failure and before provisioning.
    oem::esp32::initialize_console();
    if(initialized!=0){
        Serial.println("Pico Z NVS initialization failed; storage preserved and controller not started");return false;
    }
    return xTaskCreatePinnedToCore(firmware_task,"picoLocalTask",TaskStackBytes,nullptr,1,nullptr,1)==pdPASS;
}
}}}
extern "C" void app_main(){
    if(!zseries::standalone::esp32::start_firmware())Serial.println("Pico Z standalone startup failed");
}
