#pragma once
#include "picobrew/standalone_engine.hpp"

namespace picobrew { namespace standalone { namespace still_link { struct Sample; } } }

namespace zseries {
namespace standalone {
struct StillAddress {
    char host[64] = {}, uid[13] = {}, key[65] = {};
    std::uint16_t port = 80;
    bool stock = false;
    bool valid() const;
};
// One outstanding, bounded HTTP request on a socket independent of Hub telemetry.
class StillPeer {
  public:
    enum Op { None, Probe, Claim, Poll, Cleaned, Start, Stop, FanOn, FanOff };
    picobrew::standalone::TelemetryIo io;
    bool begin(Op, const StillAddress &, const char *brewer, const char *lease, std::uint32_t now);
    int poll(std::uint32_t now, const char *&body, std::size_t &size);
    // Check the neighbor on the connected LAN before sending a stock request.
    void *identity_context = nullptr;
    int (*check_neighbor)(void*, const char *host, const char *uid, std::uint32_t connection) = nullptr;
    int stock_result(std::uint32_t now, const StillAddress&, picobrew::standalone::still_link::Sample&);
    bool stock_active = false;
    bool stock_start_sent = false;
    void close();
    Op op = None;

  private:
    enum StockRead { Version, Program, Health, StartProgram, ReadData, EndProgram, FanState, SetFan };
    StockRead stock_read_ = Version;
    bool open_stock(StockRead, const StillAddress&);
    int receive(std::uint32_t now, const char *&body, std::size_t &size);
    std::uint32_t connection_ = 0;
    char stock_host_[16] = {}, stock_uid_[13] = {};
    char tx_[512] = {}, rx_[1024] = {};
    std::size_t tx_size_ = 0, sent_ = 0, received_ = 0;
    std::uint32_t began_ = 0;
    bool connected_ = false, stock_ = false;
};
} // namespace standalone
} // namespace zseries
