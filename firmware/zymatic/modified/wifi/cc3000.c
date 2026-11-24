#include "cc3000.h"
#include <string.h>

/* CC3000 SimpleLink HCI fields are little-endian except for the address
 * fields in sockaddr. See the TI socket host driver in Adafruit's library. */
enum stage { OFFLINE, SOCKET, NONBLOCK_ACCEPT, BIND, LISTEN, ACCEPT, SERVING,
             SELECT_READ, RECV, RECV_DATA, SEND, CLOSE_CLIENT, CLOSE_LISTENER };

static uint32_t get32(const uint8_t *p)
{
    return (uint32_t)p[0] | (uint32_t)p[1] << 8 |
           (uint32_t)p[2] << 16 | (uint32_t)p[3] << 24;
}

static uint16_t opcode(uint8_t state)
{
    switch (state) {
    case SOCKET: return 0x1001;
    case BIND: return 0x1002;
    case SEND: return 0x1003; /* data command 0x81 has a separate event */
    case RECV: return 0x1004;
    case ACCEPT: return 0x1005;
    case LISTEN: return 0x1006;
    case SELECT_READ: return 0x1008;
    case NONBLOCK_ACCEPT: return 0x1009;
    case CLOSE_CLIENT: case CLOSE_LISTENER: return 0x100b;
    default: return 0;
    }
}

static uint8_t request(const struct zym_cc3000 *s, uint8_t *frame)
{
    uint8_t *args = frame + 4;
    uint8_t size = 0;
    memset(frame, 0, ZYM_CC3000_MAX_TX_FRAME);
    args[0] = s->listener;
    switch (s->state) {
    case SOCKET:
        args[0] = 2; args[4] = 1; args[8] = 6; /* IPv4, stream, TCP */
        size = 12;
        break;
    case NONBLOCK_ACCEPT:
        args[4] = args[5] = 0xff; /* SOL_SOCKET */
        args[8] = 2; args[12] = 8; args[16] = 1;
        /* one byte option value: SOCK_ON is zero in this host protocol */
        size = 21;
        break;
    case BIND:
        args[4] = args[8] = 8;
        args[12] = 2;
        args[14] = (uint8_t)(s->port >> 8); args[15] = (uint8_t)s->port;
        size = 20;
        break;
    case LISTEN:
        size = 8; /* CC3000 ignores backlog */
        break;
    case ACCEPT: case CLOSE_LISTENER:
        size = 4;
        break;
    case SELECT_READ:
        args[0] = s->client + 1;
        args[4] = args[8] = args[12] = args[16] = 20;
        args[24] = (uint8_t)(1u << s->client);
        args[40] = 0x88; args[41] = 0x13; /* TI minimum timeval: 5000 us */
        size = 44;
        break;
    case RECV:
        args[0] = s->client; args[4] = ZYM_CC3000_CHUNK;
        size = 12;
        break;
    case SEND:
        frame[0] = 2; frame[1] = 0x81; frame[2] = 16;
        frame[3] = 16 + s->size; frame[4] = 0;
        frame[5] = s->client; frame[9] = 12; frame[13] = s->size;
        memcpy(frame + 21, s->pending, s->size);
        return 21 + s->size;
    case CLOSE_CLIENT:
        args[0] = s->client;
        size = 4;
        break;
    default: return 0;
    }
    uint16_t op = opcode(s->state);
    frame[0] = 1; frame[1] = (uint8_t)op; frame[2] = (uint8_t)(op >> 8);
    frame[3] = size;
    return size + 4;
}

void zym_cc3000_begin(struct zym_cc3000 *s, uint16_t port)
{
    uint32_t token = s->token;
    memset(s, 0, sizeof(*s));
    s->token = token;
    s->port = port;
    s->state = port ? SOCKET : OFFLINE;
}

void zym_cc3000_link_lost(struct zym_cc3000 *s, struct zym_cc3000_event *out)
{
    out->kind = s->state == OFFLINE ? ZYM_WIFI_NONE : ZYM_WIFI_LINK_FAILED;
    out->peer = s->connected ? s->token : 0;
    out->size = 0;
    s->state = OFFLINE;
    s->connected = false;
    s->waiting = false;
}

void zym_cc3000_retire(struct zym_cc3000 *s, uint8_t owned_sockets)
{
    if (s->waiting) return;
    s->accept_backoff = false;
    if (s->connected) s->want_close = true;
    else s->state = owned_sockets ? CLOSE_LISTENER : OFFLINE;
}

bool zym_cc3000_read(struct zym_cc3000 *s, uint32_t peer)
{
    if (!s->connected || peer != s->token || s->state != SERVING ||
        s->want_read || s->size || s->want_close) return false;
    s->want_read = true;
    return true;
}

bool zym_cc3000_send(struct zym_cc3000 *s, uint32_t peer, const uint8_t *data,
                     uint8_t size)
{
    if (!size || size > ZYM_CC3000_CHUNK || !data || !s->connected ||
        peer != s->token || s->state != SERVING || s->want_read ||
        s->want_close || s->size) return false;
    memcpy(s->pending, data, size);
    s->size = size;
    return true;
}

bool zym_cc3000_close(struct zym_cc3000 *s, uint32_t peer)
{
    if (!s->connected || peer != s->token) return false;
    s->want_close = true;
    return true;
}

static void backoff(struct zym_cc3000 *s, uint32_t now, uint16_t ms)
{
    s->next_accept = now + ms;
    s->accept_backoff = true;
}

static void complete(struct zym_cc3000 *s, const uint8_t *args, uint8_t length,
                     uint32_t now, struct zym_cc3000_event *out)
{
    uint32_t result = get32(args);
    s->waiting = false;
    switch (s->state) {
    case SOCKET:
        if (result > 7) { backoff(s, now, 1000); break; }
        s->listener = (uint8_t)result;
        s->state = NONBLOCK_ACCEPT;
        break;
    case NONBLOCK_ACCEPT: s->state = result ? CLOSE_LISTENER : BIND; break;
    case BIND: s->state = result ? CLOSE_LISTENER : LISTEN; break;
    case LISTEN: s->state = result ? CLOSE_LISTENER : ACCEPT; break;
    case ACCEPT:
        if (length < 8 || result != s->listener) goto bad;
        result = get32(args + 4);
        if (result == UINT32_MAX - 1) { backoff(s, now, 40); break; }
        if (result > 7) { s->state = CLOSE_LISTENER; break; }
        if (result == s->listener) goto bad;
        s->client = (uint8_t)result;
        if (!++s->token) ++s->token;
        s->connected = true;
        s->state = SERVING;
        out->kind = ZYM_WIFI_OPEN;
        out->peer = s->token;
        break;
    case SELECT_READ:
        if (length < 16) goto bad;
        if ((int32_t)result < 0) s->state = CLOSE_CLIENT;
        else if (result && (get32(args + 4) & (1u << s->client))) s->state = RECV;
        else s->state = SERVING; /* keep pending read across a poll with no data */
        break;
    case RECV:
        if (length < 12 || result != s->client) goto bad;
        result = get32(args + 4);
        if ((int32_t)result <= 0) { s->state = CLOSE_CLIENT; break; }
        if (result > ZYM_CC3000_CHUNK) goto bad;
        s->size = (uint8_t)result;
        s->state = RECV_DATA;
        s->waiting = true;
        s->since = now;
        break;
    case SEND:
        if (length < 8 || result != s->client) goto bad;
        result = get32(args + 4);
        if ((int32_t)result <= 0) { s->state = CLOSE_CLIENT; break; }
        if (result > s->size) goto bad;
        out->peer = s->token;
        out->kind = ZYM_WIFI_TX_ACK;
        out->size = (uint8_t)result;
        s->size = 0;
        s->state = SERVING;
        break;
    case CLOSE_CLIENT:
        if (result) goto bad;
        out->kind = ZYM_WIFI_CLOSED;
        out->peer = s->token;
        s->size = 0;
        s->want_read = s->want_close = s->connected = false;
        s->state = ACCEPT;
        break;
    case CLOSE_LISTENER:
        if (result) goto bad;
        s->state = SOCKET;
        backoff(s, now, 1000);
        break;
    default: goto bad;
    }
    return;
bad:
    zym_cc3000_link_lost(s, out);
}

bool zym_cc3000_poll(struct zym_cc3000 *s, uint32_t now, zym_cc3000_submit submit,
                     void *context, const uint8_t *frame, uint16_t size,
                     struct zym_cc3000_event *out)
{
    out->kind = ZYM_WIFI_NONE;
    out->peer = 0;
    out->size = 0;
    if (s->state == OFFLINE) return false;
    if (s->waiting && now - s->since >= 3000) {
        /* A late response has no transaction ID. Only a fresh radio generation
         * can safely reuse this owner's tokens after a lost completion. */
        zym_cc3000_link_lost(s, out);
        return false;
    }
    if (frame) {
        if (!s->waiting || size < 2) return false;
        if (s->state == RECV_DATA) {
            if (frame[0] != 2 || frame[1] != 0x85) return false;
            if (size < 5 || ((uint16_t)frame[4] << 8 | frame[3]) != size - 5 ||
                (uint16_t)(5 + frame[2] + s->size) != size) {
                zym_cc3000_link_lost(s, out);
                return true;
            }
            out->kind = ZYM_WIFI_RX; out->peer = s->token; out->size = s->size;
            memcpy(out->data, frame + 5 + frame[2], s->size);
            s->want_read = s->waiting = false;
            s->size = 0;
            s->state = SERVING;
            return true;
        }
        if (frame[0] != 4 || size < 3 ||
            ((uint16_t)frame[2] << 8 | frame[1]) != opcode(s->state)) return false;
        if (size < 9 || size != (uint16_t)(4 + frame[3]) || frame[4]) {
            zym_cc3000_link_lost(s, out);
        } else {
            complete(s, frame + 5, (uint8_t)(size - 5), now, out);
        }
        return true;
    }
    if (s->waiting) return false;
    if (s->accept_backoff) {
        if ((int32_t)(now - s->next_accept) < 0) return false;
        s->accept_backoff = false;
    }
    if (s->want_close && s->connected) s->state = CLOSE_CLIENT;
    else if (s->state == SERVING) {
        if (s->size) s->state = SEND;
        else if (s->want_read) s->state = SELECT_READ;
        else return false;
    }
    uint8_t tx[ZYM_CC3000_MAX_TX_FRAME];
    uint8_t n = request(s, tx);
    if (n && submit) {
        enum zym_cc3000_submit_result result = submit(context, tx, n);
        if (result == ZYM_WIFI_SENT) { s->waiting = true; s->since = now; }
        if (result == ZYM_WIFI_BAD_LINK) zym_cc3000_link_lost(s, out);
    }
    return false;
}
