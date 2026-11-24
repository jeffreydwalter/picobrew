#include "http_session.h"
#include <string.h>

enum { DISCONNECTED, READING, PREPARING, WRITING };

void zymatic_wifi_connect(ZymaticNetwork *net, uint32_t now)
{
    memset(net, 0, sizeof(*net));
    net->phase = READING;
    net->started = now;
}

void zymatic_wifi_receive(ZymaticNetwork *net, const uint8_t *bytes, uint8_t length,
                          const uint8_t key[32])
{
    if (net->phase != READING) return;
    if (length > ZYMATIC_WIFI_CHUNK) {
        net->phase = DISCONNECTED;
        return;
    }
    for (uint8_t i = 0; i < length; ++i) {
        if (zymatic_api_feed(net, bytes[i], key)) {
            net->phase = PREPARING;
            break; /* One request per connection, including protocol errors. */
        }
    }
}

uint8_t zymatic_wifi_closing(const ZymaticNetwork *net, uint32_t now)
{
    uint32_t limit = net->phase == WRITING ? 30000UL : 5000UL;
    return net->phase == DISCONNECTED || (uint32_t)(now - net->started) >= limit;
}

uint8_t zymatic_wifi_reading(const ZymaticNetwork *net)
{
    return net->phase == READING;
}

uint8_t zymatic_wifi_output(ZymaticNetwork *net, uint8_t out[ZYMATIC_WIFI_CHUNK],
                            const uint8_t key[32], const char *uid,
                            uint32_t now, uint8_t program_active)
{
    if (zymatic_wifi_closing(net, now)) return 0;
    if (net->phase == PREPARING) {
        /* This may dispatch a dial command. Execute exactly once even if the
         * radio cannot accept the response until a later foreground poll. */
#ifdef ZYMATIC_WIFI_COMPACT
        (void)key;
        zymatic_api_reply(net, uid, now, program_active);
#else
        zymatic_api_reply(net, key, uid, now, program_active);
#endif
        net->phase = WRITING;
    }
    if (net->phase != WRITING) return 0;
    if (net->sent == net->response_bytes) {
#ifdef ZYMATIC_WIFI_COMPACT
        net->phase = DISCONNECTED;
        return 0;
#else
        int8_t more = zymatic_api_more(net);
        if (!more) net->phase = DISCONNECTED;
        if (more <= 0) return 0;
#endif
    }
    uint16_t remaining = net->response_bytes - net->sent;
    uint8_t length = remaining > ZYMATIC_WIFI_CHUNK ? ZYMATIC_WIFI_CHUNK : (uint8_t)remaining;
    for (uint8_t i = 0; i < length; ++i)
        out[i] = zymatic_api_reply_byte(net, net->sent + i);
    return length;
}

void zymatic_wifi_sent(ZymaticNetwork *net, uint8_t count)
{
    if (net->phase != WRITING) return;
    if (count > ZYMATIC_WIFI_CHUNK || count > net->response_bytes - net->sent) {
        net->phase = DISCONNECTED;
        return;
    }
    net->sent += count;
}
