#include "zseries/still_peer.hpp"
#include "still_link.hpp"
#include "engine_internal.hpp"
#include <cstdio>
#include <cstring>
#include <strings.h>

namespace zseries {
namespace standalone {
namespace api = picobrew::standalone;
bool StillAddress::valid() const {
    api::detail::TelemetryConfig address;
    if (!std::memchr(host, 0, sizeof host) || !std::memchr(uid, 0, sizeof uid) ||
        !std::memchr(key, 0, sizeof key))
        return false;
    api::detail::copy(address.host, sizeof address.host, host);
    address.port = port;
    address.enabled = true;
    return api::detail::telemetry_valid(address) && api::still_link::hex(uid, 12) &&
           api::still_link::hex(key, 64);
}
void StillPeer::close() {
    if (op != None && io.close)
        io.close(io.context);
    std::memset(tx_, 0, sizeof tx_);
    op = None;
    connected_ = false;
    tx_size_ = sent_ = received_ = 0;
}
bool StillPeer::begin(Op command, const StillAddress &address, const char *brewer, const char *lease,
                      std::uint32_t now) {
    static const char *const names[] = {"",      "probe", "claim",  "poll",   "cleaned",
                                        "start", "stop",  "fan-on", "fan-off"};
    if (op != None || command == None || !address.valid() || !api::still_link::hex(brewer, 12) || !io.begin ||
        !io.poll_connect || !io.send || !io.receive || !io.close)
        return false;
    char json[128];
    int length;
    if (command == Claim || command == Probe)
        length = ::snprintf(json, sizeof json, "{\"op\":\"%s\",\"uid\":\"%s\",\"brewer\":\"%s\"}",
                            names[command], address.uid, brewer);
    else {
        if (!api::still_link::hex(lease, 16))
            return false;
        length =
            ::snprintf(json, sizeof json, "{\"op\":\"%s\",\"uid\":\"%s\",\"brewer\":\"%s\",\"lease\":\"%s\"}",
                       names[command], address.uid, brewer, lease);
    }
    if (length <= 0 || std::size_t(length) >= sizeof json)
        return false;
    int frame = ::snprintf(
        tx_, sizeof tx_,
        "POST /api/v1/brewer-link HTTP/1.1\r\nHost: %s:%u\r\nAuthorization: Bearer %s\r\nContent-Type: "
        "application/json\r\nContent-Length: %u\r\nConnection: close\r\n\r\n%s",
        address.host, unsigned(address.port), address.key, unsigned(length), json);
    if (frame <= 0 || std::size_t(frame) >= sizeof tx_)
        return false;
    op = command;
    began_ = now;
    tx_size_ = std::size_t(frame);
    if (!io.begin(io.context, address.host, address.port)) {
        close();
        return false;
    }
    return true;
}
int StillPeer::poll(std::uint32_t now, const char *&body, std::size_t &size) {
    if (op == None || now - began_ >= 3000)
        return -1;
    if (!connected_) {
        const int ready = io.poll_connect(io.context);
        if (ready != 1)
            return ready == 0 ? 0 : -1;
        connected_ = true;
    }
    if (sent_ < tx_size_) {
        const auto remaining = tx_size_ - sent_;
        const int sent = io.send(io.context, reinterpret_cast<const std::uint8_t *>(tx_) + sent_, remaining);
        if (sent == PICO_HTTP_IO_AGAIN)
            return 0;
        if (sent <= 0 || std::size_t(sent) > remaining)
            return -1;
        sent_ += sent;
        if (sent_ < tx_size_)
            return 0;
    }
    const auto remaining = sizeof rx_ - received_ - 1;
    if (!remaining)
        return -1;
    int n = io.receive(io.context, reinterpret_cast<std::uint8_t *>(rx_) + received_, remaining);
    if (n == PICO_HTTP_IO_AGAIN)
        return 0;
    if (n <= 0 || std::size_t(n) > remaining || std::memchr(rx_ + received_, 0, n))
        return -1;
    received_ += n;
    rx_[received_] = 0;
    const char *end = std::strstr(rx_, "\r\n\r\n");
    if (!end)
        return 0;
    if (std::strncmp(rx_, "HTTP/1.1 200 ", 13) && std::strncmp(rx_, "HTTP/1.0 200 ", 13))
        return -1;
    // The paired firmware always emits fixed length JSON. Reject truncated,
    // oversized, duplicate-length, or chunked responses before decoding it.
    std::size_t expected = 0;
    bool length_seen = false;
    for (const char *line = std::strstr(rx_, "\r\n") + 2; line < end;) {
        const char *next = std::strstr(line, "\r\n");
        if (!next || next > end)
            return -1;
        if (!strncasecmp(line, "Content-Length:", 15)) {
            if (length_seen)
                return -1;
            length_seen = true;
            const char *p = line + 15;
            while (p < next && *p == ' ')
                ++p;
            if (p == next)
                return -1;
            for (; p < next; ++p) {
                if (*p < '0' || *p > '9' || expected > sizeof rx_)
                    return -1;
                expected = expected * 10 + unsigned(*p - '0');
            }
        } else if (!strncasecmp(line, "Transfer-Encoding:", 18))
            return -1;
        line = next + 2;
    }
    if (!length_seen || !expected || expected >= sizeof rx_)
        return -1;
    body = end + 4;
    size = std::size_t(rx_ + received_ - body);
    return size == expected ? 1 : size < expected ? 0 : -1;
}
} // namespace standalone
} // namespace zseries
