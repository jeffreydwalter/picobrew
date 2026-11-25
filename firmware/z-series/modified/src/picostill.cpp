#include "zseries/standalone.hpp"
#include "still_link.hpp"
#include "engine_internal.hpp"
#include <cstring>
#include <algorithm>

namespace zseries {
namespace standalone {
using namespace oem;
namespace link = api::still_link;
using api::detail::Json;
using api::detail::Writer;
namespace {
constexpr char RecordName[] = "stillpair";
constexpr unsigned RecordSize = 1 + 64 + 13 + 65 + 2;
constexpr char StockStopUnconfirmed[] = "PicoStill stop unconfirmed. If running, press its button.";
bool still_program(unsigned id) {
    return id == unsigned(ZProgramId::PicoStillManual) || id == unsigned(ZProgramId::PicoStillBoilPoint);
}
} // namespace

void Adapter::load_still_address() {
    using namespace api::detail;
    std::uint8_t data[RecordSize];
    std::size_t size = 0;
    auto result =
        Records(ports_.storage).load(RecordName, data, sizeof data, size, still_generation_, 0, true);
    if (result == RecordResult::Missing)
        return;
    StillAddress address;
    if (result == RecordResult::Ok && size == sizeof data && (data[0] == 1 || data[0] == 2)) {
        address.stock = data[0] == 2;
        std::memcpy(address.host, data + 1, 64);
        std::memcpy(address.uid, data + 65, 13);
        std::memcpy(address.key, data + 78, 65);
        address.port = data[143] | (std::uint16_t(data[144]) << 8);
        if (!address.uid[0] || address.valid()) {
            still_address_ = address;
            return;
        }
    }
    still_error_ = "Pairing storage could not be verified";
}
bool Adapter::save_still_address(const StillAddress &address) {
    using namespace api::detail;
    std::uint8_t data[RecordSize] = {};
    data[0] = address.stock ? 2 : 1;
    std::memcpy(data + 1, address.host, 64);
    std::memcpy(data + 65, address.uid, 13);
    std::memcpy(data + 78, address.key, 65);
    data[143] = std::uint8_t(address.port);
    data[144] = std::uint8_t(address.port >> 8);
    std::uint32_t generation = 0;
    if (Records(ports_.storage).save(RecordName, data, sizeof data, still_generation_, generation) !=
        RecordResult::Ok)
        return false;
    still_generation_ = generation;
    still_address_ = address;
    return true;
}
bool Adapter::set_still_logging(bool enabled) {
    const auto prior = core_.eeprom.data.still_session_mode;
    core_.eeprom.data.still_session_mode = enabled ? SessionMode::Online : SessionMode::Offline;
    if (ports_.save_configuration(ports_.context))
        return true;
    core_.eeprom.data.still_session_mode = prior;
    return false;
}
bool Adapter::picostill_http(const PicoHttpRequest &req, PicoHttpResponse &res, char *arena,
                             std::size_t capacity) {
    if (std::strcmp(req.path, "/api/v1/picostill"))
        return false;
    api::Result result;
    if (!req.auth_configured || !req.authenticated)
        result = {401, "unauthorized", "A device key is required"};
    else if (req.method == PICO_HTTP_GET) {
    } else if (req.method != PICO_HTTP_PUT && req.method != PICO_HTTP_DELETE && req.method != PICO_HTTP_PATCH)
        result = {405, "method_not_allowed", "Use GET, PUT, PATCH or DELETE"};
    else if (!idle() || still_session_ || still_peer_.op != StillPeer::None)
        result = {409, "busy", "Return the brewer to the idle home screen"};
    else if (req.method == PICO_HTTP_DELETE) {
        // Remove the key from both banks, including a stale/damaged copy.
        std::uint8_t data[RecordSize];
        std::size_t size = 0;
        std::uint32_t prior = 0;
        using namespace api::detail;
        const auto saved = Records(ports_.storage).load(RecordName, data, sizeof data, size, prior);
        if (saved != RecordResult::Io)
            still_generation_ = saved == RecordResult::Ok ? prior : 0;
        if (saved == RecordResult::Io || !save_still_address(StillAddress{}))
            result = {503, "storage_io", "PicoStill pairing could not be cleared"};
        else {
            still_verified_ = false;
            still_error_ = "";
            if (!save_still_address(StillAddress{}))
                result = {503, "storage_io", "Repeat removal to erase the other pairing record"};
        }
    } else {
        char key[40];
        StillAddress candidate;
        bool enabled = false;
        unsigned fields = 0;
        Json j(req.body, req.body ? req.body_length : 0);
        bool valid = req.body && req.body_length < 420 && j.token('{');
        while (valid && !j.peek('}')) {
            if (!j.key(key)) {
                valid = false;
                break;
            }
            unsigned field = 0;
            std::int64_t port = 0;
            if (!std::strcmp(key, "host")) {
                field = 1;
                valid = j.string(candidate.host, sizeof candidate.host);
            } else if (!std::strcmp(key, "uid")) {
                field = 2;
                valid = j.string(candidate.uid, sizeof candidate.uid);
            } else if (!std::strcmp(key, "key")) {
                field = 4;
                valid = j.string(candidate.key, sizeof candidate.key);
            } else if (!std::strcmp(key, "port")) {
                field = 8;
                valid = j.integer(1, 65535, port);
                candidate.port = std::uint16_t(port);
            } else if (!std::strcmp(key, "protocol")) {
                char protocol[16];
                field = 32;
                valid = j.string(protocol, sizeof protocol) && !std::strcmp(protocol, "stock") && ports_.stock_still_neighbor;
                candidate.stock = valid;
            } else if (!std::strcmp(key, "session_logging")) {
                field = 16;
                valid = j.boolean(enabled);
            }
            if (!field || (fields & field))
                valid = false;
            fields |= field;
            if (!valid || !j.next('}'))
                break;
            if (j.peek('}'))
                valid = false;
        }
        if (!valid || !j.token('}') || !j.done() ||
            (req.method == PICO_HTTP_PATCH ? fields != 16 : fields != (candidate.stock ? 43u : 15u) || !candidate.valid()))
            result = {400, "invalid_pairing", "Invalid PicoStill address, identity, key or preference"};
        else if (req.method == PICO_HTTP_PATCH) {
            if (!set_still_logging(enabled))
                result = {503, "storage_io", "Could not save the session preference"};
        } else {
            still_candidate_ = candidate;
            if (!start_still_request(StillPeer::Probe, ports_.millis(ports_.context))) {
                still_candidate_ = {};
                still_error_ = "Could not contact the PicoStill";
                result = {503, "link_unavailable", still_error_};
            } else {
                still_checking_ = true;
                still_error_ = "";
                result = {202};
            }
        }
    }
    Writer w(arena, capacity);
    if (!result.ok())
        api::detail::error(w, result);
    else {
        w.text("{\"paired\":");
        w.boolean(still_address_.uid[0]);
        w.text(",\"stock_supported\":");
        w.boolean(ports_.stock_still_neighbor != nullptr);
        w.text(",\"protocol\":");
        w.quote(still_address_.stock ? "stock" : "standalone");
        w.text(",\"verified\":");
        w.boolean(still_verified_);
        w.text(",\"checking\":");
        w.boolean(still_checking_);
        w.text(",\"host\":");
        w.quote(still_address_.host);
        w.format(",\"port\":%u,\"uid\":", unsigned(still_address_.port));
        w.quote(still_address_.uid);
        w.text(",\"session_logging\":");
        w.boolean(core_.eeprom.data.still_session_mode != SessionMode::Offline);
        w.text(",\"error\":");
        w.quote(still_error_);
        w.text(",\"sample\":");
        if (!still_session_ || !still_claimed() || (still_address_.stock && !still_active_))
            w.text("null");
        else {
            const auto &sample = core_.still_samples.current;
            w.format("{\"received_age_ms\":%lu,\"temperature_c\":[",
                     static_cast<unsigned long>(ports_.millis(ports_.context) - last_still_sample_));
            for (unsigned i = 0; i < 4; ++i) {
                if (i)
                    w.text(",");
                w.number(sample.temperature[i]);
            }
            w.text("],\"pressure_psi\":");
            w.number(sample.pressure);
            w.text(",\"pump_running\":");
            w.boolean(sample.pump_running);
            w.text(",\"fan_2_running\":");
            w.boolean(sample.fan_running);
            w.text(",\"program_running\":");
            w.boolean(sample.program_running);
            w.text("}");
        }
        w.text("}");
    }
    res.status = w.ok ? result.status : 503;
    res.body = reinterpret_cast<const std::uint8_t *>(arena);
    res.body_length = w.ok ? w.length : 0;
    res.content_type = "application/json";
    res.content_encoding = nullptr;
    return true;
}
bool Adapter::start_still_request(StillPeer::Op op, std::uint32_t now) {
    return still_peer_.begin(op, op == StillPeer::Probe ? still_candidate_ : still_address_,
                             core_.identifier.data(), still_lease_, now);
}
void Adapter::still_state(PicoStillStateId id) {
    auto &state = core_.still.state;
    if (state.id == id)
        return;
    state.previous = state.id;
    state.id = id;
    state.polling_stopped = id == PicoStillStateId::Error;
    if (!state.polling_stopped)
        state.error[0] = 0;
    CoreStillEvent event;
    event.type = StillEventId::StateTransition;
    event.state = id;
    core_.core.still_events.put(event);
    core_.refresh_observed();
}
void Adapter::close_still(bool fault) {
    if (!still_session_)
        return;
    hold_still_outputs();
    still_session_ = still_active_ = false;
    still_peer_.close();
    bool stop_unconfirmed = false;
    if (fault)
        still_error_ = "PicoStill connection or sensor interlock failed";
    if (still_lease_[0] || still_peer_.stock_start_sent) {
        if (!start_still_request(StillPeer::Stop, ports_.millis(ports_.context)) && still_address_.stock) {
            stop_unconfirmed = true;
            still_error_ = StockStopUnconfirmed;
        }
    }
    stock_claimed_ = false;
    still_peer_.stock_active = false;
    still_peer_.stock_start_sent = false;
    still_lease_[0] = 0;
    core_.still.commands.pending = false;
    if (fault || stop_unconfirmed) {
        api::detail::copy(core_.still.state.error.data(), core_.still.state.error.size(), still_error_);
        still_state(PicoStillStateId::Error);
        if (fault)
            stop("picostill-link");
    } else
        still_state(PicoStillStateId::Idle);
}
void Adapter::tick_still(std::uint32_t now) {
    core_.still.wifi_events.pending = false;
    if (!still_session_ && still_output_hold_ && core_.runtime.state.id == RuntimeStateId::Standby)
        release_still_outputs(false);
    if (still_session_ && (core_.core.state.id == CoreStateId::Error || core_.system_error ||
                           core_.msp_state.errors || core_.msp_state.id != Z_MSP_APP_RUNNING)) {
        close_still(true);
        return;
    }
    if (still_session_ && (stopped() || !still_program(core_.core.state.program_id) ||
                           core_.core.state.id == CoreStateId::Idle ||
                           core_.core.state.id == CoreStateId::Stopping || core_.runtime.stopping)) {
        close_still(false);
        return;
    }
    if (still_session_ && still_claimed() && now - last_still_sample_ >= link::LeaseMs) {
        close_still(true);
        return;
    }
    const auto op = still_peer_.op;
    if (op != StillPeer::None) {
        const char *body = nullptr;
        std::size_t length = 0;
        link::Sample sample;
        const auto &target = op == StillPeer::Probe ? still_candidate_ : still_address_;
        const int read = target.stock ? still_peer_.stock_result(now, target, sample) :
                                        still_peer_.poll(now, body, length);
        if (!read)
            return;
        const bool ok = read > 0 && (target.stock ||
                         (sample.parse(body, length) && !std::strcmp(sample.uid, target.uid)));
        still_peer_.close();
        if (op == StillPeer::Probe) {
            still_checking_ = false;
            if (!ok)
                still_error_ = target.stock ? "Check the stock PicoStill UID, LAN address, firmware 0.0.35 and idle state" :
                                               "Could not verify the PicoStill identity and key";
            else if (!save_still_address(still_candidate_))
                still_error_ = "Could not save PicoStill pairing";
            else {
                still_verified_ = true;
                still_error_ = "";
            }
            still_candidate_ = {};
            return;
        }
        if (op == StillPeer::Stop) {
            if (target.stock && !ok) {
                still_error_ = StockStopUnconfirmed;
                api::detail::copy(core_.still.state.error.data(), core_.still.state.error.size(), still_error_);
                still_state(PicoStillStateId::Error);
            }
            return;
        }
        const bool needs_sample = still_active_ || op == StillPeer::Start;
        if (!ok || ((!target.stock || needs_sample) && !sample.healthy) ||
            (!target.stock && (!sample.lease[0] || (op != StillPeer::Claim && std::strcmp(sample.lease, still_lease_)))) ||
            sample.active != needs_sample) {
            close_still(true);
            return;
        }
        if (op == StillPeer::Claim) {
            stock_claimed_ = target.stock;
            std::strcpy(still_lease_, sample.lease);
            still_verified_ = true;
            core_.still_samples = {};
            still_state(sample.clean ? PicoStillStateId::PendingStart : PicoStillStateId::PendingCleanedAck);
        } else if (op == StillPeer::Cleaned) {
            if (!target.stock && !sample.clean) {
                close_still(true);
                return;
            }
            still_state(PicoStillStateId::PendingStart);
        } else if (op == StillPeer::Start) {
            still_active_ = true;
            still_peer_.stock_active = target.stock;
            release_still_outputs(true);
            still_state(PicoStillStateId::Running);
        }
        if (target.stock && !sample.healthy) {
            // The stock accessory first publishes sensor data when it starts.
            last_still_sample_ = now;
            core_.refresh_observed();
            return;
        }
        auto &samples = core_.still_samples;
        auto previous = samples.current;
        samples.current.timestamp = now ? now : 1;
        std::copy_n(sample.temperatures, 4, samples.current.temperature.begin());
        samples.current.pressure = sample.pressure;
        samples.current.pump_running = sample.pump;
        samples.current.fan_running = sample.fan;
        samples.current.program_running = sample.active;
        samples.current.update_speeds(previous);
        samples.update_statistics(samples.current.timestamp);
        core_.still.state.sample_date = now;
        core_.still.state.sample_delta_ms = now - last_still_sample_;
        last_still_sample_ = now;
        core_.refresh_observed();
    }
    PicoStillCommand command;
    core_.still.commands.take(command);
    if (!still_session_)
        return;
    StillPeer::Op next = StillPeer::None;
    switch (command.id) {
    case StillCommandId::None:
        break;
    case StillCommandId::Scan:
        if (!still_claimed()) {
            next = StillPeer::Claim;
            still_state(PicoStillStateId::Scanning);
        }
        break;
    case StillCommandId::CleanedAck:
        if (core_.still.state.id == PicoStillStateId::PendingCleanedAck)
            next = StillPeer::Cleaned;
        break;
    case StillCommandId::Start:
        if (core_.still.state.id == PicoStillStateId::PendingStart) {
            next = StillPeer::Start;
            still_state(PicoStillStateId::Starting);
        }
        break;
    case StillCommandId::FanOn:
    case StillCommandId::FanOff:
        if (still_active_)
            next = command.id == StillCommandId::FanOn ? StillPeer::FanOn : StillPeer::FanOff;
        break;
    case StillCommandId::Stop:
        // Waking the native runtime from standby passes through Idle and
        // emits a generic still cancel before it begins this program.
        if (still_claimed() || core_.still.state.id != PicoStillStateId::Idle)
            close_still(false);
        return;
    case StillCommandId::Reconnect:
        close_still(true);
        return;
    default:
        break;
    }
    if (next == StillPeer::None && still_claimed() && now - last_still_sample_ >= link::PollMs)
        next = StillPeer::Poll;
    if (next != StillPeer::None && !start_still_request(next, now))
        close_still(true);
}
} // namespace standalone
} // namespace zseries
