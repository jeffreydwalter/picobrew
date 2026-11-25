#pragma once
#include "picobrew/standalone_engine.hpp"

namespace zseries {
namespace standalone {
struct StillAddress {
    char host[64] = {}, uid[13] = {}, key[65] = {};
    std::uint16_t port = 80;
    bool valid() const;
};
// One outstanding, bounded HTTP request on a socket independent of Hub telemetry.
class StillPeer {
  public:
    enum Op { None, Probe, Claim, Poll, Cleaned, Start, Stop, FanOn, FanOff };
    picobrew::standalone::TelemetryIo io;
    bool begin(Op, const StillAddress &, const char *brewer, const char *lease, std::uint32_t now);
    int poll(std::uint32_t now, const char *&body, std::size_t &size);
    void close();
    Op op = None;

  private:
    char tx_[512] = {}, rx_[1024] = {};
    std::size_t tx_size_ = 0, sent_ = 0, received_ = 0;
    std::uint32_t began_ = 0;
    bool connected_ = false;
};
} // namespace standalone
} // namespace zseries
