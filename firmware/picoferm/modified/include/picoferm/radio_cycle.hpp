#pragma once
#include <cstdint>

namespace picoferm { namespace standalone {
// Foreground time, acquisition and recording continue while only the radio sleeps.
// Durations use elapsed unsigned milliseconds so a millis() rollover is harmless.
class RadioCycle {
public:
    enum class Action { None, Sleep, Wake };
    static constexpr std::uint32_t Management = 120000;
    void hold(std::uint32_t now, std::uint32_t duration = Management) {
        if (remaining(now) >= duration) return;
        start_ = now;
        duration_ = duration;
    }
    Action plan(std::uint32_t now, bool paired, bool protected_work,
                bool transfer_active, std::uint32_t until_sample) {
        if (!paired || protected_work) hold(now);
        if (sleeping_) {
            if (!remaining(now) && now - slept_at_ < sleep_ms_) return Action::None;
            sleeping_ = false;
            hold(now, 30000); // bounded association, delivery and history service
            return Action::Wake;
        }
        if (remaining(now) || transfer_active) return Action::None;
        // Stay reachable periodically even when local sampling is set to a day.
        sleep_ms_ = until_sample < 300000 ? until_sample : 300000;
        if (sleep_ms_ < 5000) {
            hold(now, 5000);
            return Action::None;
        }
        slept_at_ = now;
        sleeping_ = true;
        return Action::Sleep;
    }
    void sleep_failed(std::uint32_t now) { sleeping_ = false; hold(now, 30000); }
    bool sleeping() const { return sleeping_; }
private:
    std::uint32_t start_ = 0, duration_ = 0, slept_at_ = 0, sleep_ms_ = 0;
    bool sleeping_ = false;
    std::uint32_t remaining(std::uint32_t now) const {
        const auto elapsed = now - start_;
        return elapsed < duration_ ? duration_ - elapsed : 0;
    }
};
}}
