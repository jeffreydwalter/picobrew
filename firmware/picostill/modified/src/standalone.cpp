#include "picostill/wireless_update.hpp"
#include "picostill/standalone.hpp"
#include <cmath>
#include <cstdio>
#include <cstring>

namespace picostill { namespace standalone {
namespace api = picobrew::standalone;
namespace {
api::Result invalid() { return api::Result(400, "invalid_command", "Invalid PicoStill command arguments"); }
const api::CommandDescriptor commands[] = {
    {"ack-clean-condenser", 0, 0}, {"start", 0, 0}, {"essential-oils", 0, 0}, {"clean", 0, 0}, {"stop", 0, 0},
    {"set-pump", api::Enabled, api::Enabled},
    {"set-fan", api::Channel | api::Level | api::Enabled, api::Channel | api::Level}
};
bool finite(float value) { return std::isfinite(static_cast<double>(value)); }
}
void OutputGuard::digital_write(std::uint8_t pin, bool high) {
    if ((pin == 18 && inhibited) || (pin == 19 && (inhibited || pump_inhibited))) high = false;
    ports_.digital_write(pin, high);
}
void OutputGuard::pwm_write(std::uint8_t channel, std::uint8_t duty) {
    ports_.pwm_write(channel, inhibited && (channel == 1 || channel == 2) ? 0 : duty);
}
void OutputGuard::inhibit() {
    inhibited = pump_inhibited = true;
    // Force physical lows even if a stale logical flag already says 'off'.
    ports_.digital_write(19, false); ports_.pwm_write(1, 0); ports_.pwm_write(2, 0); ports_.digital_write(18, false);
}
Device::Device(oem::HardwarePorts& ports, oem::I2cPorts& i2c, oem::DiagnosticPorts& diagnostic_ports) noexcept
    : output(ports), diagnostics(diagnostic_ports), hardware(output, i2c, diagnostics, *this),
      controller(hardware, output, diagnostics, *this), leds(output, diagnostics) {}
void Device::begin(bool hardware_ready, const char* uid) {
    board_ready_ = hardware_ready; begun_ = true; ::snprintf(uid_, sizeof(uid_), "%s", uid ? uid : "unknown");
    output.inhibit(); hardware.pump.enabled = false;
    hardware.fans.power.enabled = hardware.fans.first.enabled = hardware.fans.second.enabled = false;
    output.pin_mode(13, 1); leds.setup(); leds.start(hardware_ready ? oem::Animation::Ready : oem::Animation::HardwareNotConnected);
    if (hardware_ready) hardware.enable_sensors(true);
}
bool Device::sensors_healthy() {
    if (!begun_ || !board_ready_ || !hardware.sensors_ready() || !hardware.sensors.pressure.valid || !finite(hardware.sensors.values[4])) return false;
    for (unsigned i = 0; i < 4; ++i) if (!hardware.sensors.adc.valid[i] || !finite(hardware.sensors.values[i])) return false;
    return hardware.sensors.metrics.read_success_rates_good(diagnostics) && hardware.sensors_in_range();
}
void Device::off() {
    output.inhibit(); hardware.pump.enabled = false;
    if (controller.running()) controller.stop();
    hardware.pump.enabled = false;
    hardware.fans.power.enabled = hardware.fans.first.enabled = hardware.fans.second.enabled = false;
    // Read-only local monitoring remains active after outputs are inhibited.
    if (board_ready_ && !hardware.sensors_enabled) hardware.enable_sensors(true);
}
void Device::stop(const char* reason) {
    lease_[0]=owner_[0]=0;link_running_=false;
    stopped_ = true; stop_reason_ = reason; off(); was_running_ = false; menu_ = false;
    if (engine_) engine_->cancel_prepared_session();
    leds.start(oem::Animation::SteadyYellow);
    if (engine_) engine_->event("stop", reason);
}
api::Result Device::start(oem::ProgramId program, const char* command) {
    if (controller.running()) return api::Result(409, "busy", "PicoStill is already running");
    if (!begun_ || !board_ready_) return api::Result(503, "not_ready", "PicoStill hardware initialization failed");
    if (!storage_ready_ || !engine_) return api::Result(503, "storage_io", "A verified history filesystem is required");
    if (!sensors_healthy()) return api::Result(409, "sensor_interlock", "Wait for valid, in-range PicoStill sensor readings");
    const std::uint32_t next_token = (token_ + 1) ? token_ + 1 : 1;
    const api::Result reserved = engine_->prepare_session(command, "", next_token);
    if (!reserved.ok()) return reserved;
    token_ = next_token; ::snprintf(command_, sizeof(command_), "%s", command);
    hardware.error = oem::StillError::None; controller.error = oem::StillError::None;
    stopped_ = false; stop_reason_ = "none"; menu_ = false;
    output.inhibited = output.pump_inhibited = false;
    controller.start(program);
    if (!controller.running()) {
        off(); engine_->cancel_prepared_session(); return api::Result(503, "start_failed", "The native PicoStill controller did not start");
    }
    // Native cleaning starts the pump before the foreground fan2 guard. Apply
    // that same required fan2 condition before returning command acceptance.
    if (hardware.pump.enabled && !hardware.fans.second_enabled()) {
        hardware.fans.power.set(true); hardware.fans.second.set(true, 255);
    }
    if (program != oem::ProgramId::RequestedCleaning) engine_->record_care(api::CareEvent::StillUsed);
    was_running_ = true; leds.start(oem::Animation::ProgramRunning); return api::Result(202);
}
api::Result Device::execute(const api::Command& command) {
    const api::CommandDescriptor* descriptor = nullptr;
    for (const auto& candidate : commands) if (!std::strcmp(candidate.name, command.name)) descriptor = &candidate;
    if (!descriptor || (command.fields & ~descriptor->allowed_fields) ||
        (command.fields & descriptor->required_fields) != descriptor->required_fields) return invalid();
    if (lease_[0] && std::strcmp(command.name,"stop")) return {409,"brewer_link_active","Stop the paired brewer session before operating PicoStill separately"};
    if (!std::strcmp(command.name, "ack-clean-condenser")) {
        if (!idle()) return {409, "busy", "Wait for PicoStill to stop before cleaning"};
        if (!engine_) return {503, "not_ready", "Care record unavailable"};
        const auto saved = engine_->record_care(api::CareEvent::CondenserCleaned);
        return saved.ok() ? api::Result(202) : saved;
    }
    if (!std::strcmp(command.name, "stop")) { stop(); return api::Result(202); }
    if (!std::strcmp(command.name, "start")) return start(oem::ProgramId::PicoManual, command.name);
    if (!std::strcmp(command.name, "essential-oils")) return start(oem::ProgramId::PicoEssentialOils, command.name);
    if (!std::strcmp(command.name, "clean")) return start(oem::ProgramId::RequestedCleaning, command.name);
    if (!controller.running() || stopped_ || output.inhibited)
        return api::Result(409, "not_running", "Start a PicoStill program before changing its actuators");
    if (!std::strcmp(command.name, "set-pump")) {
        if (!command.enabled) {
            output.pump_inhibited = true; output.digital_write(19, false); hardware.pump.enabled = false;
            return api::Result(202);
        }
        if (controller.state != oem::HWState::On || !sensors_healthy() || controller.error != oem::StillError::None || hardware.error != oem::StillError::None)
            return api::Result(409, "pump_interlock", "The native temperature, pressure or hardware interlock inhibits the pump");
        hardware.fans.power.set(true); hardware.fans.second.set(true, 255);
        output.pump_inhibited = false; hardware.pump.set_pump(true); return api::Result(202);
    }
    if (!std::strcmp(command.name, "set-fan")) {
        if (command.channel < 1 || command.channel > 2 || !std::isfinite(command.level) || command.level < 0 || command.level > 100) return invalid();
        const bool enabled = (command.fields & api::Enabled) ? command.enabled : command.level > 0;
        if (command.channel == 2 && hardware.pump.enabled && (!enabled || command.level < 61))
            return api::Result(409, "pump_requires_fan", "Fan 2 must retain cooling and tach monitoring while the pump runs");
        const auto duty = static_cast<std::uint8_t>(command.level * 255.0 / 100.0 + 0.5);
        oem::Fan& fan = command.channel == 1 ? hardware.fans.first : hardware.fans.second;
        if (enabled) hardware.fans.power.set(true);
        fan.set(enabled, duty);
        if (!hardware.fans.first.enabled && !hardware.fans.second.enabled) hardware.fans.power.set(false);
        return api::Result(202);
    }
    return invalid();
}
void Device::buttons() {
    const auto now = output.millis(); const bool pressed = !output.digital_read(13);
    if (pressed && !pressed_) {
        pressed_ = true; held_ = false; button_changed_ = now; if (clicks_ < 3) ++clicks_;
        if (lease_[0] || controller.running()) { stop("physical-button"); clicks_ = 0; held_ = true; }
    } else if (!pressed && pressed_) { pressed_ = false; button_released_ = now; }
    if (pressed_ && !held_ && now - button_changed_ >= 5000) {
        held_ = true; button_event(true, clicks_); clicks_ = 0;
    } else if (!pressed_ && clicks_ && now - button_released_ >= 200) {
        const unsigned count = clicks_; clicks_ = 0; button_event(false, count);
    }
}
void Device::button_event(bool held, unsigned count) {
    if (held) {
        menu_ = !menu_; menu_index_ = 0; leds.start(menu_ ? oem::Animation::FastGreen : oem::Animation::Ready); return;
    }
    if (!menu_) return;
    if (count == 1) { menu_index_ = (menu_index_ + 1) % 3; leds.start(static_cast<oem::Animation>(5 + menu_index_)); }
    else if (count == 2) {
        const char* names[] = {"start", "essential-oils", "clean"}; api::Command command;
        ::snprintf(command.name, sizeof(command.name), "%s", names[menu_index_]); const api::Result result = execute(command);
        if (!result.ok()) { leds.start(oem::Animation::SlowRed); if (engine_) engine_->event("command-rejected", result.error); }
    }
}
void Device::poll() {
    if (!begun_) return;
    expire_link();
    buttons();
    if (board_ready_) controller.poll();
    // A manual pump-off request is latched at the GPIO port, so a native state
    // transition cannot briefly reenergize it before foreground bookkeeping.
    if (output.pump_inhibited) hardware.pump.enabled = false;
    if (was_running_ && !controller.running()) {
        const bool failed = controller.error != oem::StillError::None || hardware.error != oem::StillError::None;
        if (failed) {
            stopped_ = true; stop_reason_ = "hardware-fault"; off();
            const auto error = controller.error != oem::StillError::None ? controller.error : hardware.error;
            leds.start(oem::error_animation(error, diagnostics));
            if (engine_) engine_->event("controller-error", oem::to_string(error));
        } else { off(); leds.start(oem::Animation::SteadyGreen); }
        was_running_ = false;
    }
    if (link_running_ && (!controller.running() || !sensors_healthy())) stop("brewer-link-interlock");
    leds.poll();
}
api::Result Device::snapshot(api::Snapshot& result) {
    result = api::Snapshot(); result.uptime_ms = output.millis(); result.ready = begun_ && board_ready_ && storage_ready_;
    result.busy = lease_[0] || controller.running(); result.stopped = stopped_; result.stop_reason = stop_reason_; result.command = command_;
    result.state = !begun_ ? "initializing" : !board_ready_ ? "hardware-unavailable" : !storage_ready_ ? "storage-unavailable" :
        stopped_ ? "stopped" : controller.running() ? oem::to_string(controller.state) : menu_ ? "selecting-program" : "idle";
    result.device_state = static_cast<unsigned>(controller.program); result.phase = static_cast<unsigned>(controller.state);
    result.controller_status = static_cast<unsigned>(controller.error != oem::StillError::None ? controller.error : hardware.error);
    result.recording = controller.running(); result.session_token = token_;
    const char* ids[] = {"inlet", "outlet", "pot", "ambient", "pressure"};
    const char* labels[] = {"Inlet", "Outlet", "Pot", "Ambient", "Pressure"}; result.sensor_count = 5;
    for (unsigned i = 0; i < 5; ++i) {
        api::Sensor& sensor = result.sensors[i]; sensor.id = ids[i]; sensor.label = labels[i];
        sensor.kind = i < 4 ? api::SensorKind::Temperature : api::SensorKind::Pressure;
        sensor.unit = i < 4 ? api::Unit::Fahrenheit : api::Unit::Kilopascal;
        const bool valid = i < 4 ? hardware.sensors.adc.valid[i] : hardware.sensors.pressure.valid;
        sensor.reading.valid = board_ready_ && valid && finite(hardware.sensors.values[i]);
        // Native controller temperatures are Celsius; its original browser
        // explicitly labels pressure psi. Common wire units are F and kPa.
        sensor.reading.value = i < 4 ? double(hardware.sensors.values[i]) * 1.8 + 32.0 : double(hardware.sensors.values[i]) * 6.894757293168;
    }
    result.temperature_f = result.sensors[2].reading; result.pressure_kpa = result.sensors[4].reading;
    result.actuator_count = 3;
    api::Actuator& pump = result.actuators[0]; pump.id = "pump"; pump.label = "Pump"; pump.kind = api::ActuatorKind::Pump;
    pump.valid = begun_ && board_ready_; pump.enabled = hardware.pump.enabled && !output.inhibited && !output.pump_inhibited;
    const oem::Fan* fans[] = {&hardware.fans.first, &hardware.fans.second};
    for (unsigned i = 0; i < 2; ++i) {
        const oem::Fan& fan = *fans[i]; api::Actuator& actuator = result.actuators[i + 1];
        actuator.id = i ? "fan2" : "fan1"; actuator.label = i ? "Fan 2" : "Fan 1"; actuator.kind = api::ActuatorKind::Fan;
        actuator.valid = begun_ && board_ready_; actuator.enabled = fan.enabled && hardware.fans.power.enabled && !output.inhibited;
        actuator.supports_level = actuator.supports_rpm = true; actuator.level.valid = actuator.valid;
        actuator.level.value = actuator.enabled ? double(fan.speed) * 100.0 / 255.0 : 0;
        actuator.rpm.valid = fan.metrics.samples && finite(fan.metrics.rpm_average); actuator.rpm.value = fan.metrics.rpm_average;
    }
    return api::Result();
}
api::DeviceServices Device::services() noexcept {
    api::DeviceServices ports; ports.context = this;
    ports.identity = [](void* p) { api::Identity id; Device& device = *static_cast<Device*>(p);
        id.model = id.model_family = "picostill"; id.model_name = "PicoStill"; id.firmware_version = update::Version; id.device_uid = device.uid_; return id; };
    ports.capabilities = [](void* p) { api::Capabilities c;
        if (static_cast<Device*>(p)->wireless_) c.ota_protocol = update::Protocol;
        c.picostill_brewer_link_v1 = true;
        c.flags = api::Pump | api::Fans | api::Temperature | api::Pressure;
        c.settings = api::TemperatureUnit; c.commands = commands; c.command_count = sizeof(commands) / sizeof(commands[0]); return c; };
    ports.snapshot = [](void* p, api::Snapshot& result) { return static_cast<Device*>(p)->snapshot(result); };
    ports.execute = [](void* p, const api::Command& command) { return static_cast<Device*>(p)->execute(command); };
    ports.idle = [](void* p) { return static_cast<Device*>(p)->idle(); };
    ports.read_settings = [](void* p, api::Settings& settings) { settings = static_cast<Device*>(p)->settings_; return api::Result(); };
    ports.validate_settings = [](void* p, const api::Settings& settings, std::uint32_t changed) {
        if (!static_cast<Device*>(p)->idle()) return api::Result(409, "busy", "Change preferences while PicoStill is idle");
        return changed & ~api::TemperatureUnit || (settings.temperature_unit != 'F' && settings.temperature_unit != 'C') ?
            api::Result(400, "invalid_settings", "PicoStill supports the display temperature unit") : api::Result(); };
    ports.apply_settings = [](void* p, const api::Settings& settings, std::uint32_t) { static_cast<Device*>(p)->settings_ = settings; return api::Result(); };
    return ports;
}
void Device::sensors_sampled() {}
void Device::snapshot_report(const oem::HardwareSnapshot&) { if (engine_) engine_->event("diagnostic", "native hardware snapshot"); }
void Device::full_test_report(const oem::FullTestResult&) { if (engine_) engine_->event("diagnostic", "native full hardware report"); }
void Device::controller_changed() { if (engine_) engine_->event("controller", oem::to_string(controller.state)); }
void Device::console_message(const char* message) { if (engine_) engine_->event("controller", message); }
}}
