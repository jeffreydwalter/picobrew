// Local Z/PicoStill coordination. Temperatures are native Celsius, pressure is
// reported in psi, and fan indicates fan 2 as in the original accessory report.
#pragma once
#include "json.hpp"
namespace picobrew {
namespace standalone {
namespace still_link {
constexpr std::uint32_t LeaseMs = 6500;
constexpr std::uint32_t PollMs = 1500;
inline bool hex(const char *s, std::size_t length) {
    return std::strlen(s) == length && std::strspn(s, "0123456789abcdef") == length;
}
struct Sample {
    char uid[13] = {}, lease[17] = {};
    bool healthy = false, clean = false, active = false, pump = false, fan = false;
    double temperatures[4] = {}, pressure = 0;
    void write(detail::Writer &w) const {
        w.text("{\"uid\":");
        w.quote(uid);
        w.text(",\"lease\":");
        w.quote(lease);
        w.text(",\"healthy\":");
        w.boolean(healthy);
        w.text(",\"clean\":");
        w.boolean(clean);
        w.text(",\"active\":");
        w.boolean(active);
        w.text(",\"pump\":");
        w.boolean(pump);
        w.text(",\"fan\":");
        w.boolean(fan);
        w.text(",\"celsius\":[");
        for (unsigned i = 0; i < 4; ++i) {
            if (i)
                w.text(",");
            w.format("%.9g", temperatures[i]);
        }
        w.text("],\"pressure_psi\":");
        w.format("%.9g", pressure);
        w.text("}");
    }
    bool parse(const char *data, std::size_t size) {
        detail::Json j(data, size);
        unsigned seen = 0;
        char k[40];
        bool valid = j.token('{');
        while (valid && !j.peek('}')) {
            if (!j.key(k))
                return false;
            unsigned f = 0;
            if (!std::strcmp(k, "uid")) {
                f = 1;
                valid = j.string(uid, sizeof uid) && hex(uid, 12);
            } else if (!std::strcmp(k, "lease")) {
                f = 2;
                valid = j.string(lease, sizeof lease) && (!lease[0] || hex(lease, 16));
            } else if (!std::strcmp(k, "healthy")) {
                f = 4;
                valid = j.boolean(healthy);
            } else if (!std::strcmp(k, "clean")) {
                f = 8;
                valid = j.boolean(clean);
            } else if (!std::strcmp(k, "active")) {
                f = 16;
                valid = j.boolean(active);
            } else if (!std::strcmp(k, "pump")) {
                f = 32;
                valid = j.boolean(pump);
            } else if (!std::strcmp(k, "fan")) {
                f = 64;
                valid = j.boolean(fan);
            } else if (!std::strcmp(k, "celsius")) {
                f = 128;
                valid = j.token('[');
                for (unsigned i = 0; i < 4 && valid; ++i)
                    valid = (!i || j.token(',')) && j.number(temperatures[i]);
                valid = valid && j.token(']');
            } else if (!std::strcmp(k, "pressure_psi")) {
                f = 256;
                valid = j.number(pressure);
            }
            if (!f || (seen & f))
                return false;
            seen |= f;
            if (!valid || !j.next('}'))
                break;
            if (j.peek('}'))
                return false;
        }
        return valid && j.token('}') && j.done() && seen == 511;
    }
};
} // namespace still_link
} // namespace standalone
} // namespace picobrew
