/* Cooperative TI CC3000 TCP listener. The shared radio owner supplies HCI
 * frames, arbitrates commands, and accounts for the radio's TX credits. */
#ifndef ZYMATIC_CC3000_H
#define ZYMATIC_CC3000_H

#include <stdbool.h>
#include <stdint.h>

#define ZYM_CC3000_CHUNK 16
#define ZYM_CC3000_MAX_TX_FRAME 48

enum zym_cc3000_notice { ZYM_WIFI_NONE, ZYM_WIFI_OPEN, ZYM_WIFI_RX,
    ZYM_WIFI_TX_ACK, ZYM_WIFI_CLOSED, ZYM_WIFI_LINK_FAILED };

struct zym_cc3000_event {
    enum zym_cc3000_notice kind;
    uint32_t peer;
    uint8_t size;
    uint8_t data[ZYM_CC3000_CHUNK];
};

/* BUSY retains ownership at the caller and makes no change to radio credits.
 * SENT means the broker copied this frame, accepted sole responsibility for
 * it, and atomically reserved a CC3000 TX credit for a DATA send. It must
 * hold the transaction through both the RECV event and following DATA frame.
 * Input/output frames omit the chip's SPI framing and alignment padding. */
enum zym_cc3000_submit_result { ZYM_WIFI_BUSY, ZYM_WIFI_SENT, ZYM_WIFI_BAD_LINK };
typedef enum zym_cc3000_submit_result (*zym_cc3000_submit)(
    void *context, const uint8_t *frame, uint8_t size);

struct zym_cc3000 {
    uint32_t token, since, next_accept;
    uint16_t port;
    uint8_t state, listener, client, size;
    bool waiting, want_read, want_close, connected, accept_backoff;
    uint8_t pending[ZYM_CC3000_CHUNK];
};

/* Zero-initialize s before first use. Begin only after a fresh radio
 * generation with IP acquired and all old HCI transactions drained. */
void zym_cc3000_begin(struct zym_cc3000 *s, uint16_t port);
void zym_cc3000_link_lost(struct zym_cc3000 *s, struct zym_cc3000_event *out);
/* Stop submitting application traffic after Wi-Fi loss. Observe any prior
 * completion/data before explicitly closing each allocated descriptor. */
void zym_cc3000_retire(struct zym_cc3000 *s, uint8_t owned_sockets);
/* Each call attempts at most one command or consumes one supplied frame.
 * Returns true iff the input frame belonged to this owner. The broker must
 * deliver unsolicited events, radio resets and other clients' events itself. */
bool zym_cc3000_poll(struct zym_cc3000 *s, uint32_t now, zym_cc3000_submit submit,
                     void *context, const uint8_t *frame, uint16_t size,
                     struct zym_cc3000_event *out);
bool zym_cc3000_read(struct zym_cc3000 *s, uint32_t peer);
bool zym_cc3000_send(struct zym_cc3000 *s, uint32_t peer, const uint8_t *data,
                     uint8_t size);
bool zym_cc3000_close(struct zym_cc3000 *s, uint32_t peer);
#endif
