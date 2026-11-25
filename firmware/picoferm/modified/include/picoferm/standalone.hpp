#pragma once
#include "picoferm/bounded_acquisition.hpp"
#include "picoferm/calibration_store.hpp"
#include "picoferm/hardware.hpp"
#include "picobrew/standalone_engine.hpp"

namespace picoferm { namespace standalone {
class Controller {
public:
    Controller(SensorIo sensors, picobrew::standalone::Storage storage,
               picobrew::standalone::Clock clock) noexcept;
    // Call before Engine.begin. Constructor performs no platform I/O.
    bool identity(const char* uid) noexcept;
    void bind(picobrew::standalone::Engine& engine) noexcept { engine_=&engine; }
    picobrew::standalone::Result begin(const std::uint8_t* original, std::size_t size);
    void poll();
    picobrew::standalone::DeviceServices services() noexcept;
    picobrew::standalone::Result execute(const picobrew::standalone::Command&);
    picobrew::standalone::Result snapshot(picobrew::standalone::Snapshot&);
    bool idle() const noexcept { return begun_ && !recording_ && !calibrating_ && !acquisition_.busy(); }
    std::uint32_t next_sample_at() const { return next_sample_; }
    void long_press();
    oem::Animation animation() const noexcept;
    const BoundedAcquisition& acquisition() const noexcept { return acquisition_; }
    const CalibrationStore& configuration() const noexcept { return configuration_; }
private:
    BoundedAcquisition acquisition_;
    CalibrationStore configuration_;
    picobrew::standalone::Clock clock_;
    picobrew::standalone::Engine* engine_=nullptr;
    char uid_[33]={};
    char temperature_unit_='F';
    bool begun_=false, recording_=false, calibrating_=false;
    std::uint32_t session_token_=0, seen_sequence_=0, next_sample_=0;
    SensorFault seen_fault_=SensorFault::None;
    std::uint32_t now() const noexcept;
    bool measured() const noexcept;
    bool request_sample(bool recover);
    void event(const char* kind,const char* detail);
    picobrew::standalone::Result validate_settings(const picobrew::standalone::Settings&,std::uint32_t changed) const;
    picobrew::standalone::Result apply_settings(const picobrew::standalone::Settings&,std::uint32_t changed);
};
}}
