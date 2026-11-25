#include "picostill/standalone.hpp"
#include "still_link.hpp"
#include "engine_internal.hpp"
namespace picostill {
namespace standalone {
namespace api = picobrew::standalone;
namespace link = api::still_link;
void Device::expire_link() {
    if (lease_[0] && output.millis() - lease_date_ >= link::LeaseMs)
        stop("brewer-link-timeout");
}
bool Device::brewer_link(const PicoHttpRequest &req, PicoHttpResponse &res, char *arena,
                         std::size_t capacity) {
    if (std::strcmp(req.path, "/api/v1/brewer-link"))
        return false;
    api::Result result;
    char op[10] = {}, uid[13] = {}, brewer[13] = {}, token[17] = {}, k[40];
    if (!req.auth_configured || !req.authenticated)
        result = {401, "unauthorized", "A device key is required"};
    else if (req.method != PICO_HTTP_POST)
        result = {405, "method_not_allowed", "Use POST"};
    else {
        expire_link();
        api::detail::Json j(req.body, req.body ? req.body_length : 0);
        unsigned seen = 0;
        bool valid = req.body && req.body_length < 256 && j.token('{');
        while (valid && !j.peek('}')) {
            if (!j.key(k)) {
                valid = false;
                break;
            }
            unsigned field = 0;
            if (!std::strcmp(k, "op")) {
                field = 1;
                valid = j.string(op, sizeof op);
            } else if (!std::strcmp(k, "uid")) {
                field = 2;
                valid = j.string(uid, sizeof uid) && link::hex(uid, 12);
            } else if (!std::strcmp(k, "brewer")) {
                field = 4;
                valid = j.string(brewer, sizeof brewer) && link::hex(brewer, 12);
            } else if (!std::strcmp(k, "lease")) {
                field = 8;
                valid = j.string(token, sizeof token) && link::hex(token, 16);
            }
            if (!field || (seen & field))
                valid = false;
            seen |= field;
            if (!valid || !j.next('}'))
                break;
            if (j.peek('}'))
                valid = false;
        }
        const bool probe = !std::strcmp(op, "probe"), claim = !std::strcmp(op, "claim");
        if (!valid || !j.token('}') || !j.done() || seen != unsigned(probe || claim ? 7 : 15))
            result = {400, "invalid_link", "Invalid brewer link request"};
        else if (std::strcmp(uid, uid_))
            result = {409, "identity_mismatch", "This is a different PicoStill"};
        else if (probe) {
        } // Read-only identity and capability check; never refresh a lease.
        else if (claim) {
            if (!idle() || menu_ || !board_ready_ || !storage_ready_ || !sensors_healthy())
                result = {409, "unavailable", "PicoStill must be idle with healthy sensors"};
            else {
                if (++lease_sequence_ == 0)
                    ++lease_sequence_;
                ::snprintf(lease_, sizeof lease_, "%08lx%08lx", (unsigned long)boot_nonce_,
                           (unsigned long)lease_sequence_);
                std::strcpy(owner_, brewer);
                lease_date_ = output.millis();
            }
        } else if (!lease_[0] || std::strcmp(brewer, owner_) || std::strcmp(token, lease_))
            result = {409, "lease_mismatch", "The brewer session has expired"};
        else if (!std::strcmp(op, "stop"))
            stop("paired-brewer");
        else if (!std::strcmp(op, "cleaned")) {
            if (controller.running() || !engine_)
                result = {409, "busy", "Stop the accessory before confirming cleaning"};
            else
                result = engine_->record_care(api::CareEvent::CondenserCleaned);
        } else if (!sensors_healthy()) {
            stop("brewer-link-interlock");
            result = {409, "sensor_interlock", "Healthy PicoStill sensors are required"};
        } else if (!std::strcmp(op, "start")) {
            if (!link_running_) {
                if (!engine_ || (engine_->care_due() & api::CondenserDue))
                    result = {409, "cleaning_due", "Confirm that the condenser was cleaned"};
                else {
                    result = start(oem::ProgramId::PicoManual, "start");
                    link_running_ = result.ok();
                }
            }
        } else if (!std::strcmp(op, "fan-on") || !std::strcmp(op, "fan-off")) {
            if (!link_running_ || !controller.running())
                result = {409, "not_running", "The paired program is idle"};
            else
                hardware.fans.set_first(!std::strcmp(op, "fan-on"));
        } else if (std::strcmp(op, "poll"))
            result = {400, "invalid_link", "Unknown brewer link operation"};
        if (result.ok() && !probe && lease_[0])
            lease_date_ = output.millis();
    }
    api::detail::Writer w(arena, capacity);
    if (!result.ok())
        api::detail::error(w, result);
    else {
        link::Sample sample;
        std::memcpy(sample.uid, uid_, 12);
        if (!std::strcmp(op, "claim") || token[0])
            api::detail::copy(sample.lease, sizeof sample.lease, lease_);
        sample.healthy = sensors_healthy();
        sample.clean = engine_ && !(engine_->care_due() & api::CondenserDue);
        sample.active = link_running_ && controller.running();
        sample.pump = hardware.pump.enabled;
        sample.fan = hardware.fans.second_enabled();
        if (sample.healthy) {
            for (unsigned i = 0; i < 4; ++i)
                sample.temperatures[i] = hardware.sensors.values[i];
            sample.pressure = hardware.sensors.values[4];
        }
        sample.write(w);
    }
    res.status = w.ok ? (result.ok() ? 200 : result.status) : 503;
    res.body = reinterpret_cast<const std::uint8_t *>(arena);
    res.body_length = w.ok ? w.length : 0;
    res.content_type = "application/json";
    res.content_encoding = nullptr;
    return true;
}
} // namespace standalone
} // namespace picostill
