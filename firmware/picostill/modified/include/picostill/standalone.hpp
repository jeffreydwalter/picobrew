// PicoStill standalone owner. Reuses the recovered board/controller modules;
// local operation, output inhibition and API composition are modified behavior.
#pragma once
#include "picostill/controller.hpp"
#include "picobrew/standalone_engine.hpp"

namespace picostill { namespace standalone {
class OutputGuard final : public oem::HardwarePorts {
public:
    explicit OutputGuard(oem::HardwarePorts& ports) noexcept : ports_(ports) {}
    bool inhibited = true, pump_inhibited = true;
    void inhibit();
    std::uint32_t millis() override { return ports_.millis(); }
    void pin_mode(std::uint8_t pin, unsigned mode) override { ports_.pin_mode(pin, mode); }
    bool digital_read(std::uint8_t pin) override { return ports_.digital_read(pin); }
    void digital_write(std::uint8_t pin, bool high) override;
    void pwm_setup(std::uint8_t channel, double frequency, unsigned resolution) override { ports_.pwm_setup(channel, frequency, resolution); }
    void pwm_attach(std::uint8_t pin, std::uint8_t channel) override { ports_.pwm_attach(pin, channel); }
    void pwm_write(std::uint8_t channel, std::uint8_t duty) override;
    void variable(const char* scope, const char* name, const char* value) override { ports_.variable(scope, name, value); }
private:
    oem::HardwarePorts& ports_;
};
class Device final : public oem::ControllerPorts {
public:
    Device(oem::HardwarePorts& ports, oem::I2cPorts& i2c, oem::DiagnosticPorts& diagnostics) noexcept;
    OutputGuard output;
    oem::Diagnostics diagnostics;
    oem::Hardware hardware;
    oem::Controller controller;
    oem::Leds leds;
    void bind_engine(picobrew::standalone::Engine& engine) noexcept { engine_ = &engine; }
    // Call after the recovered board GPIO/I2C initialization. No constructor
    // starts an output. Missing hardware/storage leaves status/stop available.
    void begin(bool hardware_ready, const char* uid);
    void link_nonce(std::uint32_t nonce) { boot_nonce_ = nonce; }
    bool brewer_link(const PicoHttpRequest&, PicoHttpResponse&, char*, std::size_t);
    void storage_ready(bool value) noexcept { storage_ready_ = value; }
    void wireless_capable(bool available) { wireless_ = available; }
    void poll();
    bool idle() const noexcept { return !controller.running() && !lease_[0]; }
    picobrew::standalone::DeviceServices services() noexcept;
    picobrew::standalone::Result execute(const picobrew::standalone::Command& command);
    picobrew::standalone::Result snapshot(picobrew::standalone::Snapshot& snapshot);
    bool sensors_healthy();
    void stop(const char* reason = "operator");
    void sensors_sampled() override;
    void snapshot_report(const oem::HardwareSnapshot&) override;
    void full_test_report(const oem::FullTestResult&) override;
    void controller_changed() override;
    void console_message(const char*) override;
private:
    bool wireless_ = false;
    char lease_[17] = {}, owner_[13] = {};
    std::uint32_t lease_date_=0, lease_sequence_=0, boot_nonce_=0;
    bool link_running_=false;
    void expire_link();
    picobrew::standalone::Engine* engine_ = nullptr;
    picobrew::standalone::Settings settings_;
    char uid_[32] = {}, command_[32] = {};
    const char* stop_reason_ = "none";
    bool begun_ = false, board_ready_ = false, storage_ready_ = false, stopped_ = false, was_running_ = false;
    bool menu_ = false, pressed_ = false, held_ = false;
    unsigned menu_index_ = 0, clicks_ = 0;
    std::uint32_t token_ = 0, button_changed_ = 0, button_released_ = 0;
    void buttons();
    void button_event(bool held, unsigned count);
    picobrew::standalone::Result start(oem::ProgramId program, const char* command);
    void off();
};
}}
