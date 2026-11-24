#include "cc3000_broker.h"
#include <string.h>

void zym_broker_begin(struct zym_cc3000_broker *b, zym_cc3000_submit bounded_spi,
                      void *hardware)
{
    memset(b, 0, sizeof(*b));
    b->try_write = bounded_spi;
    b->hardware = hardware;
}

bool zym_broker_acquire_oem(struct zym_cc3000_broker *b)
{
    if (b->draining && b->http_sockets) return false;
    if (b->owner != ZYM_RADIO_FREE) {
        if (b->owner == ZYM_RADIO_HTTP) b->oem_queued = true;
        return false;
    }
    b->oem_queued = false;
    b->owner = ZYM_RADIO_OEM;
    return true;
}

void zym_broker_release_oem(struct zym_cc3000_broker *b)
{
    if (b->owner == ZYM_RADIO_OEM) b->owner = ZYM_RADIO_FREE;
}

void zym_broker_link_lost(struct zym_cc3000_broker *b)
{
    b->owner = ZYM_RADIO_LOST;
    /* A borrowed frame, if any, must still be released by the foreground. */
}

static enum zym_cc3000_submit_result submit(void *context, const uint8_t *frame, uint8_t size)
{
    struct zym_cc3000_broker *b = context;
    if (b->owner == ZYM_RADIO_LOST || !b->try_write) return ZYM_WIFI_BAD_LINK;
    if (b->owner != ZYM_RADIO_FREE || (b->oem_queued && !b->draining)) return ZYM_WIFI_BUSY;
    b->owner = ZYM_RADIO_HTTP;
    b->expected = frame[0] == 2 ? 0x1003 : (uint16_t)frame[1] | (uint16_t)frame[2] << 8;
    b->submitted_socket = frame[0] == 2 ? frame[5] : frame[4];
    b->await_data = false;
    enum zym_cc3000_submit_result r = b->try_write(b->hardware, frame, size);
    if (b->owner == ZYM_RADIO_LOST) return ZYM_WIFI_BAD_LINK;
    if (r == ZYM_WIFI_BUSY) b->owner = ZYM_RADIO_FREE;
    else if (r == ZYM_WIFI_BAD_LINK) zym_broker_link_lost(b);
    return r;
}

enum zym_radio_rx zym_broker_irq(struct zym_cc3000_broker *b,
                                 const uint8_t *frame, uint16_t size)
{
    if (!frame || size < 2) {
        zym_broker_link_lost(b);
        return ZYM_RADIO_DISCARD;
    }
    uint16_t op = 0;
    if (frame[0] == 4 && size >= 3) {
        op = (uint16_t)frame[1] | (uint16_t)frame[2] << 8;
        if (op == 0x8002) b->draining = true; /* leave the HCI engine alive */
        if (op == 0x1000) zym_broker_link_lost(b); /* chip requests startup patches */
        if ((op & 0xc000) || op == 0x1000) return ZYM_RADIO_PASS_TO_OEM;
        /* SEND failure -57 is also an unsolicited chip report. The OEM
         * records it in its own bookkeeping even if HTTP owns a command. */
        if ((op == 0x1003 || op == 0x100e || op == 0x100f) &&
            size >= 13 && frame[9] == 0xc7 &&
            frame[10] == 0xff && frame[11] == 0xff && frame[12] == 0xff &&
            frame[5] < 8 && !frame[6] && !frame[7] && !frame[8]) {
            if (!(b->http_sockets & (1u << frame[5]))) return ZYM_RADIO_PASS_TO_OEM;
            if (b->owner != ZYM_RADIO_HTTP || b->expected != op || b->submitted_socket != frame[5]) {
                zym_broker_link_lost(b);
                return ZYM_RADIO_DISCARD;
            }
        }
    }
    if (b->owner == ZYM_RADIO_LOST) return ZYM_RADIO_DISCARD;
    if (b->owner == ZYM_RADIO_OEM) return ZYM_RADIO_PASS_TO_OEM;
    if (b->owner == ZYM_RADIO_FREE) {
        zym_broker_link_lost(b);
        return ZYM_RADIO_DISCARD;
    }
    bool match = b->await_data ? frame[0] == 2 && frame[1] == 0x85
                              : op == b->expected;
    if (!match || b->rx) {
        /* Solicited traffic without a matching owner is ambiguous. Keep it
         * out of both callers' result buffers until the next radio boot. */
        zym_broker_link_lost(b);
        return ZYM_RADIO_DISCARD;
    }
    b->rx = frame;
    b->rx_size = size;
    return ZYM_RADIO_HOLD_SPI;
}

bool zym_broker_poll(struct zym_cc3000_broker *b, struct zym_cc3000 *s,
                     uint32_t now, struct zym_cc3000_event *out)
{
    bool borrowed = b->rx != NULL;
    if (b->owner == ZYM_RADIO_LOST) {
        zym_cc3000_link_lost(s, out);
    } else {
        bool consumed = zym_cc3000_poll(s, now, submit, b, b->rx, b->rx_size, out);
        if (out->kind == ZYM_WIFI_LINK_FAILED || (borrowed && !consumed)) {
            zym_broker_link_lost(b);
            if (out->kind != ZYM_WIFI_LINK_FAILED) zym_cc3000_link_lost(s, out);
        } else if (borrowed) {
            if (b->expected == 0x1001 && b->rx[5] < 8 && !b->rx[6] && !b->rx[7] && !b->rx[8])
                b->http_sockets |= (uint8_t)(1u << s->listener);
            if (out->kind == ZYM_WIFI_OPEN) b->http_sockets |= (uint8_t)(1u << s->client);
            if (b->expected == 0x100b) b->http_sockets &= (uint8_t)~(1u << b->submitted_socket);
            /* A positive RECV event is followed by a separate data frame that
             * carries no transaction ID. Keep the lease through both frames. */
            if (s->waiting) b->await_data = true;
            else b->owner = ZYM_RADIO_FREE;
        }
    }
    if (borrowed) { b->rx = NULL; b->rx_size = 0; }
    return borrowed;
}
