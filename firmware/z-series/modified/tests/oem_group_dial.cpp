// Z119 group receive, in-memory HTTP streams and Head encoder events.
#include "zseries/core_application.hpp"
#include "zseries/head_application.hpp"
#include <algorithm>
#include <cassert>
#include <cstring>
#include <string>
#ifdef Z119_GROUP_LOOPBACK_TLS
#include "oem_group_tls.hpp"
#endif

using namespace zseries::oem;

namespace {
const char previous[] = R"({"Name":"Previous local","IsBeer":true,"Steps":[{"Name":"Prior step","StepType":0,"Location":2,"Temp":60,"Time":456,"Drain":0}]})";
struct Recipe {
    unsigned id;
    const char* name;
    const char* step;
    unsigned minutes;

    std::string offer() const {
        return R"({"FirmwareVersion":"0.0.119","SessionType":6,"ZProgramId":24,"Name":"Fixture offer","RecipeID":)" +
            std::to_string(id) + "}";
    }
    std::string body() const {
        return R"({"ID":)" + std::to_string(id) + R"(,"Name":")" + name +
            R"(","TypeCode":"Beer","StartWater":4,"Steps":[{"Name":")" + step +
            R"(","Action":0,"Location":1,"Temp":150,"Drain":0,"Time":)" + std::to_string(minutes) + "}]}";
    }
};
const Recipe first{420, "Fetched recipe", "Offered step", 3};
const Recipe second{421, "Second recipe", "Second step", 7};

struct MemoryHttp {
    std::string request, response;
    std::size_t offset = 0;

    void set_body(const std::string& body) {
        offset = 0;
        request.clear();
        response = "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " +
            std::to_string(body.size()) + "\r\n\r\n" + body;
    }

    static bool perform(void* context, const HttpAction& action, std::uint32_t& result) {
        auto& wire = *static_cast<MemoryHttp*>(context);
        const auto available = wire.response.size() - wire.offset;
        switch (action.effect) {
        case HttpEffect::AcquireClient:
            *action.acquired_class = HttpClientClass::WifiSecure;
            result = 1;
            return true;
        case HttpEffect::ConnectHost: result = 1; return true;
        case HttpEffect::FreeHeap: result = 100000; return true;
        case HttpEffect::Write:
            wire.request.append(reinterpret_cast<const char*>(action.data), action.size);
            result = action.size;
            return true;
        case HttpEffect::Available: result = available; return true;
        case HttpEffect::Connected: result = available > 0; return true;
        case HttpEffect::ReadByte:
            assert(available > 0);
            result = static_cast<unsigned char>(wire.response[wire.offset++]);
            return true;
        case HttpEffect::ReadBytes:
            result = std::min(available, action.size);
            std::memcpy(action.destination, wire.response.data() + wire.offset, result);
            wire.offset += result;
            return true;
        case HttpEffect::Flush: case HttpEffect::Stop: return true;
        default: return false;
        }
    }

    void bind(HttpClient& client) {
        client.services.context = this;
        client.services.perform = perform;
        client.services.millis = [](void* p) {return ++static_cast<MemoryHttp*>(p)->now;};
    }

    std::uint32_t now = 10000;
};

struct Brewer {
    HeadApplication head;
    CoreApplication app{head.core_events};
    MemoryHttp download, session;
    std::uint32_t now = 10000;

    Brewer() {
        head.bind_settings(app.eeprom.data);
        app.ports.context = this;
        app.ports.millis = [](void* p) {return static_cast<Brewer*>(p)->now;};
        app.ports.remote_session = [](void* p, const AzureCommand& cmd, std::uint32_t last_activity) {
            return static_cast<Brewer*>(p)->head.handle_remote_session(cmd, last_activity) == HeadApplicationResult::Ok;
        };
        app.ports.azure_window_closed = [](void* p, bool) {
            HeadAzureEvent event;
            event.type = AzureEventType::TimeoutStartProgram;
            return static_cast<Brewer*>(p)->head.receive_azure(event);
        };
        app.ports.diagnostics.log = [](void*, ConsoleLevel, const char*, const char*, std::uint32_t, const char*) {return true;};
        head.services.context = this;
        head.services.millis = app.ports.millis;
        head.services.post_core = [](void* p, const HeadUiCoreEnvelope& cmd) {return static_cast<Brewer*>(p)->app.post(cmd);};
        head.services.discard_remote_session = [](void* p) {return static_cast<Brewer*>(p)->app.azure.session_discard() == BackendResult::Ok;};
        head.services.alarm = [](void*, bool) {return true;};
        head.services.display_io.digital_write = [](void*, std::uint8_t, std::uint8_t) {};
        head.services.display_io.spi_byte = [](void*, std::uint8_t) {};
        head.services.display_io.spi_bytes = [](void*, const std::uint8_t*, std::size_t) {};

        app.network = 2;
        app.core.state.id = CoreStateId::Idle;
        app.runtime.state.id = RuntimeStateId::Idle;
        app.azure.enabled = true;
        app.azure.state = AzureState::Listening;
        app.azure.services.context = this;
        app.azure.services.millis = app.ports.millis;
        app.azure.services.perform = [](void*, const AzureAction& action, std::uint32_t& result) {
            result = 1;
            return action.effect == AzureEffect::Connected || action.effect == AzureEffect::Loop ||
                (action.effect == AzureEffect::Diagnostic && action.argument == 0x289); // Loaded window expired.
        };
        app.backend.token = app.sessions.token = "SYNTHETIC";
        app.backend.authorization = app.sessions.authorization = "";
        download.bind(app.backend_http);
        session.bind(app.session_http);
        app.auxiliary.pressure_pa = 101325;
        app.temperatures.estimated_boil_celsius = 100;
        app.refresh_observed();

        char local[sizeof previous];
        std::memcpy(local, previous, sizeof local);
        assert(parse_local_recipe(app.program, local, sizeof local - 1, app.backend.environment).ready);
        check_previous();
    }

    void check_buffer(SessionType type, unsigned id, const char* name, const char* step, unsigned hold) {
        const auto& record = *app.programs.find(static_cast<unsigned>(ZProgramId::Buffered));
        const auto& view = record.view;
        assert(record.type == type && record.unique_id == id && std::strcmp(view.name, name) == 0);
        assert(std::any_of(view.strings, view.strings + view.string_count,
            [step](const char* text) {return std::strcmp(text, step) == 0;}));
        assert(std::any_of(view.code, view.code + view.code_size, [hold](Instruction i) {
            return i.opcode == OpCode::HoldFor && i.argument == hold;
        }));
    }

    void check_previous() {check_buffer(SessionType::LocalBrew, 0, "Previous local", "Prior step", 456);}
    void check_offered(const Recipe& r) {check_buffer(SessionType::BrewZymatic, r.id, r.name, r.step, r.minutes * 60);}

    void head_tick() {
        // The ESP32 observe_head binding copies these fields before calling Head.
        head.observed.core = app.core.state;
        head.observed.runtime = app.runtime.state;
        const auto& record = app.program.record();
        head.observed.loaded_program_status = static_cast<unsigned>(app.azure.state);
        head.observed.loaded_session_type = record.type;
        head.observed.loaded_recipe.id = record.unique_id;
        std::strcpy(head.observed.loaded_recipe.name.data(), record.view.name);
        head.observed.loaded_duration = app.program.stats.duration_seconds;
        assert(head.tick(++now) == HeadApplicationResult::Ok);
    }

    void dispatch(CoreCommandId expected) {
        CoreCommand cmd;
        assert(app.core.commands.take(cmd) && cmd.id == expected);
        CoreAction action(CoreEffect::DispatchCoreCommand);
        action.command = &cmd;
        std::uint32_t ignored = 0;
        assert(app.perform(action, ignored));
    }

    void receive(const Recipe& r) {
        auto incoming = r.offer();
        assert(app.azure.receive("synthetic/receiver", &incoming[0], incoming.size()) == BackendResult::Ok);
        assert(app.azure.state == AzureState::SessionLoaded && app.azure.deadline == now + 30000);
        assert(app.tick_stage(CoreTickStage::Azure, now));
        dispatch(CoreCommandId::AzureSessionLoad);
    }

    void fetch(unsigned id, const std::string& body) {
        download.set_body(body);
        dispatch(CoreCommandId::ZymaticRecipeLoad);
        assert(app.backend.pending_recipe.id == id);
        assert(head.core_events.pending && head.core_events.value.type == CoreEventType::Loading);
        head_tick();
        for (unsigned i = 0; i < 32 && app.backend.state != 0; ++i) {
            assert(app.tick_stage(CoreTickStage::HttpBackend, ++now));
        }
        assert(app.backend.state == 0 && download.offset == download.response.size());
        assert(download.request.find("GET /Vendors/input.cshtml?type=Recipe&token=SYNTHETIC&id=" + std::to_string(id) + " HTTP/") == 0);
        assert(!app.core.commands.pending && !app.runtime_commands.pending && session.request.empty());
        assert(app.core.state.id == CoreStateId::Idle && head.core_events.pending);
    }

    void ready_menu(const Recipe& r) {
        assert(head.core_events.value.type == CoreEventType::ProgramLoaded);
        assert(head.core_events.value.source == CoreCommandId::ZymaticRecipeLoad);
        check_offered(r);
        head_tick();
        const auto& menu = head.screens.menu_data;
        assert(menu.count == 2 && head.encoder.state.menu.enabled);
        assert(menu.items[0].event.command == UiCommand::StartLoadedProgram);
        assert(menu.items[1].event.command == UiCommand::GroupSessionExit);
        assert(std::strcmp(menu.items[0].text.data(), "Start") == 0);
        assert(std::strcmp(menu.items[1].text.data(), "Exit") == 0);
        assert(!app.core.commands.pending && session.request.empty());
    }

    void dial(const Recipe& r, bool start) {
        ready_menu(r);
        if (!start) {
            assert(head.receive_encoder(-1));
            head_tick();
        }
        assert(head.encoder.state.menu.selected == (start ? 0u : 1u));
        assert(head.receive_button(CoreButtonEvent{}));
        head_tick();
        assert(head.ui_events.pending && !app.core.commands.pending);
        head_tick();
        if (start) {
            assert(app.core.commands.value.program_id == static_cast<unsigned>(ZProgramId::Buffered));
            dispatch(CoreCommandId::StartProgram);
            assert(app.core.state.id == CoreStateId::Starting && app.azure.state == AzureState::SessionRunning);
            assert(app.commands.session.unique_program_id == r.id);
            assert(session.request.find("POST /Vendors/input.cshtml?type=ZSession&token=SYNTHETIC HTTP/") == 0);
        } else {
            dispatch(CoreCommandId::AzureSessionDiscard);
            assert(app.azure.state == AzureState::Listening && app.core.state.id == CoreStateId::Idle);
            assert(session.request.empty() && !app.runtime_commands.pending);
        }
        check_offered(r);
    }
};
} // namespace

int main(int argc, char** argv) {
    assert(argc >= 2);
    const std::string scenario = argv[1];
    Brewer brewer;
#ifdef Z119_GROUP_LOOPBACK_TLS
    if (scenario.compare(0, 5, "mqtt-") == 0) {
        assert(argc == 4);
        const auto port = std::stoul(argv[2]);
        assert(port > 0 && port <= 65535);
        LoopbackMqtt wire(brewer.now, static_cast<std::uint16_t>(port), argv[3]);
        auto& azure = brewer.app.azure;
        wire.attach(azure);
        if (scenario == "mqtt-wrong-name" || scenario == "mqtt-untrusted-ca") {
            const long expected = scenario == "mqtt-wrong-name" ? X509_V_ERR_HOSTNAME_MISMATCH :
                X509_V_ERR_UNABLE_TO_GET_ISSUER_CERT_LOCALLY;
            assert(wire.verify_result == expected && !wire.connected);
            assert(azure.state == AzureState::Connecting && !brewer.app.core.commands.pending);
            assert(brewer.download.request.empty() && brewer.session.request.empty());
            return 0;
        }
        assert(scenario == "mqtt-start" || scenario == "mqtt-exit");
        assert(wire.connected && wire.verify_result == X509_V_OK);
        const auto until = std::chrono::steady_clock::now() + std::chrono::seconds(4);
        while (!brewer.app.core.commands.pending && std::chrono::steady_clock::now() < until) {
            assert(brewer.app.tick_stage(CoreTickStage::Azure, brewer.now));
        }
        assert(azure.state == AzureState::SessionLoaded && azure.deadline == brewer.now + 30000);
        brewer.dispatch(CoreCommandId::AzureSessionLoad);
        brewer.check_previous();
        brewer.fetch(first.id, first.body());
        brewer.dial(first, scenario == "mqtt-start");
        azure.disconnect();
        assert(brewer.app.tick_stage(CoreTickStage::Azure, brewer.now));
        assert(azure.state == AzureState::Disabled && !wire.connected);
        return 0;
    }
#endif
    assert(argc == 2);
    if (scenario == "recent-input") {
        brewer.app.encoder.activity_date = 1; // 9,999 ms of quiet, one short.
        brewer.receive(first);
        brewer.check_previous();
        assert(brewer.app.azure.state == AzureState::Listening && !brewer.app.core.commands.pending);
        assert(brewer.download.request.empty() && brewer.session.request.empty());
    } else if (scenario == "invalid-download") {
        brewer.receive(first);
        brewer.fetch(first.id, "{");
        assert(brewer.head.core_events.value.type == CoreEventType::LoadingError);
        brewer.check_previous();
    } else if (scenario == "timeout") {
        brewer.receive(first);
        brewer.fetch(first.id, first.body());
        brewer.ready_menu(first);
        brewer.now = brewer.app.azure.deadline - 1;
        assert(brewer.app.tick_stage(CoreTickStage::Azure, brewer.now));
        assert(!brewer.app.core.commands.pending);
        assert(brewer.app.tick_stage(CoreTickStage::Azure, ++brewer.now));
        brewer.dispatch(CoreCommandId::AzureWindowTimeout);
        assert(brewer.head.azure_events.pending);
        brewer.head_tick();
        assert(brewer.app.azure.state == AzureState::Listening && brewer.app.core.state.id == CoreStateId::Idle);
        assert(!brewer.app.core.commands.pending && !brewer.app.runtime_commands.pending && brewer.session.request.empty());
        brewer.check_offered(first);
    } else {
        assert(scenario == "exit" || scenario == "start" || scenario == "exit-then-start");
        brewer.receive(first);
        brewer.check_previous();
        brewer.fetch(first.id, first.body());
        brewer.dial(first, scenario == "start");
        if (scenario == "exit-then-start") {
            // The physical-input owner normally records the last dial movement.
            // Simulate the quiet interval before another invitation arrives.
            brewer.app.encoder.activity_date = brewer.now;
            brewer.now += brewer.head.remote_input_quiet_ms;
            brewer.receive(second);
            brewer.check_offered(first);
            brewer.fetch(second.id, second.body());
            brewer.dial(second, true);
        }
    }
}
