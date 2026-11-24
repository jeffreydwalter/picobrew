#include "server.h"
#include <string.h>

_Static_assert(ZYM_CC3000_CHUNK == ZYMATIC_WIFI_CHUNK, "Radio and HTTP chunk sizes must match");

bool zymatic_wifi_server_poll(struct zym_cc3000_broker *broker, struct zym_cc3000 *radio,
                              ZymaticNetwork *http, const uint8_t key[32],
                              const char *uid, uint32_t now, uint8_t mode)
{
    struct zym_cc3000_event event;
    if (broker->draining) zym_cc3000_retire(radio, broker->http_sockets);
    bool release_rx = zym_broker_poll(broker, radio, now, &event);
    if (broker->draining) return release_rx;
    if (event.kind == ZYM_WIFI_CLOSED || event.kind == ZYM_WIFI_LINK_FAILED) {
        memset(http, 0, sizeof(*http));
        return release_rx;
    }
    if (!radio->connected) return release_rx;
    if (event.kind == ZYM_WIFI_OPEN) zymatic_wifi_connect(http, now);
    if (zymatic_wifi_closing(http, now)) {
        zym_cc3000_close(radio, radio->token);
        return release_rx;
    }
    if (event.kind == ZYM_WIFI_RX) zymatic_wifi_receive(http, event.data, event.size, key);
    if (event.kind == ZYM_WIFI_TX_ACK) zymatic_wifi_sent(http, event.size);
    if (zymatic_wifi_reading(http)) {
        zym_cc3000_read(radio, radio->token);
    } else if (!radio->waiting && !radio->size && !radio->want_close) {
        uint8_t bytes[ZYMATIC_WIFI_CHUNK];
        uint8_t count = zymatic_wifi_output(http, bytes, key, uid, now, mode);
        if (count) zym_cc3000_send(radio, radio->token, bytes, count);
        else if (zymatic_wifi_closing(http, now)) zym_cc3000_close(radio, radio->token);
    }
    return release_rx;
}
