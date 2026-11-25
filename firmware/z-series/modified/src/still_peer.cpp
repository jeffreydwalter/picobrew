#include "zseries/still_peer.hpp"
#include "still_link.hpp"
#include "engine_internal.hpp"
#include <cstdio>
#include <cmath>
#include <cstdlib>
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
    if (!api::detail::telemetry_valid(address) || !api::still_link::hex(uid, 12))
        return false;
    if (!stock)
        return api::still_link::hex(key, 64);
    // The stock API identifies a device by its Wi-Fi MAC on the same subnet.
    // Accept a canonical IPv4 literal so the identity check and socket agree.
    if (port != 80 || key[0])
        return false;
    const char* cursor = host;
    for (unsigned i = 0; i < 4; ++i) {
        const char* start = cursor;
        unsigned value = 0;
        while (*cursor >= '0' && *cursor <= '9') {
            value = value * 10 + unsigned(*cursor++ - '0');
            if (cursor - start > 3 || value > 255)
                return false;
        }
        if (cursor == start || (cursor - start > 1 && *start == '0') ||
            (i == 0 && (!value || value == 127 || value >= 224)) ||
            *cursor != (i == 3 ? '\0' : '.'))
            return false;
        if (i < 3)
            ++cursor;
    }
    return true;
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
    stock_ = address.stock;
    if (stock_) {
        if (!check_neighbor)
            return false;
        op = command;
        began_ = now;
        StockRead first = Version;
        switch (command) {
        case Probe: case Claim: first = Version; break;
        case Cleaned: case Start: first = Program; break;
        case Poll: first = Program; break;
        case FanOn: case FanOff: first = Program; break;
        case Stop: first = Program; break;
        default: close(); return false;
        }
        if (open_stock(first, address))
            return true;
        close();
        return false;
    }
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
    return receive(now, body, size);
}
int StillPeer::receive(std::uint32_t now, const char *&body, std::size_t &size) {
    if (op == None || now - began_ >= 3000)
        return -1;
    if (!connected_) {
        const int ready = io.poll_connect(io.context);
        if (ready != 1)
            return ready == 0 ? 0 : -1;
        connected_ = true;
    }
    if (stock_ && !sent_) {
        const int identity = check_neighbor(identity_context, stock_host_, stock_uid_, connection_);
        if (identity != 1)
            return identity == 0 ? 0 : -1;
    }
    if (sent_ < tx_size_) {
        const auto remaining = tx_size_ - sent_;
        const int sent = io.send(io.context, reinterpret_cast<const std::uint8_t *>(tx_) + sent_, remaining);
        if (sent == PICO_HTTP_IO_AGAIN)
            return 0;
        if (sent <= 0 || std::size_t(sent) > remaining)
            return -1;
        sent_ += sent;
        if (stock_ && stock_read_ == StartProgram && sent_ == tx_size_)
            stock_start_sent = true;
        if (sent_ < tx_size_)
            return 0;
    }
    const auto remaining = sizeof rx_ - received_ - 1;
    if (!remaining)
        return -1;
    int n = io.receive(io.context, reinterpret_cast<std::uint8_t *>(rx_) + received_, remaining);
    if (n == PICO_HTTP_IO_AGAIN)
        return 0;
    if (n < 0 || (!n && !stock_) || std::size_t(n) > remaining || std::memchr(rx_ + received_, 0, n))
        return -1;
    received_ += n;
    rx_[received_] = 0;
    const char *end = std::strstr(rx_, "\r\n\r\n");
    if (!end)
        return n ? 0 : -1;
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
    body = end + 4;
    size = std::size_t(rx_ + received_ - body);
    // Stock sends HTTP/1.0, no Content-Length, and closes after the response.
    if (stock_ && !length_seen)
        return n ? 0 : size ? 1 : -1;
    if (!length_seen || !expected || expected >= sizeof rx_ || (!n && size != expected))
        return -1;
    return size == expected ? 1 : size < expected ? 0 : -1;
}
bool StillPeer::open_stock(StockRead request, const StillAddress& address) {
    static const char* const paths[] = {"/version", "/runningprogram", "/hardwareError", "/start", "/data", "/end"};
    const char* path = request == SetFan ? (op == FanOn ? "/fanon" : "/fanoff") : paths[request];
    ++connection_;
    stock_read_ = request;
    std::strcpy(stock_host_, address.host);
    std::strcpy(stock_uid_, address.uid);
    tx_size_ = ::snprintf(tx_, sizeof tx_, "GET %s HTTP/1.0\r\nHost: %s\r\nConnection: close\r\n\r\n", path, address.host);
    connected_ = false;
    sent_ = received_ = 0;
    return io.begin(io.context, address.host, address.port);
}

namespace {
// Stock 0.0.35 reports four Fahrenheit readings, absolute pressure in psi,
// three 0/1 flags, then the controller error. Convert once, at the wire boundary.
bool stock_sample(const char* body, api::still_link::Sample& out) {
    if (*body++ != '#')
        return false;
    for (unsigned i = 0; i < 5; ++i) {
        const char* comma = std::strchr(body, ',');
        if (!comma || comma == body || comma - body > 15)
            return false;
        // Accept the space padding emitted by the stock decimal formatter.
        char* end;
        const double value = std::strtod(body, &end);
        if (end != comma || !std::isfinite(value))
            return false;
        if (i < 4) {
            out.temperatures[i] = (value - 32.0) / 1.8;
            if (out.temperatures[i] < 0 || out.temperatures[i] > 80)
                return false;
        } else {
            if (value < 0 || value > 16)
                return false;
            out.pressure = value;
        }
        body = comma + 1;
    }
    bool* flags[] = {&out.pump, &out.fan, &out.active};
    for (auto flag : flags) {
        if ((*body != '0' && *body != '1') || body[1] != ',')
            return false;
        *flag = *body == '1';
        body += 2;
    }
    return !std::strcmp(body, "None#");
}
}
int StillPeer::stock_result(std::uint32_t now, const StillAddress& address, api::still_link::Sample& sample) {
    const char* body = nullptr;
    std::size_t length = 0;
    const int status = receive(now, body, length);
    if (status != 1)
        return status;
    StockRead next;
    switch (stock_read_) {
    case Version:
        if (std::strcmp(body, "#0.0.35#"))
            return -1;
        next = Program;
        break;
    case Program:
        if (op == Stop && !std::strcmp(body, "#None#"))
            return 1;
        if (op == Stop || (op == Poll && stock_active) || op == FanOn || op == FanOff) {
            if (std::strcmp(body, "#PicoManual#"))
                return -1;
            if (op == Stop) next = EndProgram;
            else if (op == Poll) next = ReadData;
            else next = SetFan;
        } else {
            if (std::strcmp(body, "#None#"))
                return -1;
            next = Health;
        }
        break;
    case Health:
        if (std::strcmp(body, "#None#"))
            return -1;
        if (op != Start)
            return 1;
        next = StartProgram;
        break;
    case StartProgram:
        if (!std::strcmp(body, "#F#"))
            stock_start_sent = false; // Stock refused because another program already owns the accessory.
        if (std::strcmp(body, "#T#"))
            return -1;
        next = ReadData;
        break;
    case SetFan:
        if (std::strcmp(body, "#T#"))
            return -1;
        next = ReadData;
        break;
    case EndProgram:
        if (!std::strcmp(body, "#F#"))
            return 1;
        if (std::strcmp(body, "#T#"))
            return -1;
        next = ReadData;
        break;
    case ReadData:
        if (!stock_sample(body, sample))
            return -1;
        if (op == Stop && !sample.active)
            return 1;
        if (op != Stop && sample.active) {
            sample.healthy = true;
            return 1;
        }
        // The stock HTTP handler acknowledges /start before the foreground
        // controller publishes its first active sample. Keep outputs inhibited.
        if (op != Start && op != Stop)
            return -1;
        next = ReadData;
        break;
    default: return -1;
    }
    io.close(io.context);
    return open_stock(next, address) ? 0 : -1;
}

} // namespace standalone
} // namespace zseries
