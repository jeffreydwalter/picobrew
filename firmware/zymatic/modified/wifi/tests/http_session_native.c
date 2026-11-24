/* Exercise the HTTP session directly and through the CC3000 socket state machine. */
#include "wifi/server.h"
#include "zymatic_pairing.h"

#include <assert.h>
#include <stdio.h>
#include <string.h>

#define PROGMEM
#define ZYMATIC_SITE_SECTION
#include "site/zymatic_site.h"
#include "site/zymatic_pair_js.h"
#include "site/zymatic_favicon.h"

extern void zh_test_seed(void);
extern uint8_t zh_test_sram[0x4100];
_Alignas(int16_t) uint8_t zymatic_test_encoder[24];
uint8_t zymatic_test_fault, zymatic_test_frame[1024];
uint32_t zymatic_test_temperature_bits(uint8_t heat)
{
    uint32_t bits;
    memcpy(&bits, zh_test_sram + (heat ? 0x23e0 : 0x23dc), sizeof(bits));
    return bits;
}
uint8_t zymatic_test_read_oem_serial(char *serial)
{
    (void)serial;
    return 0;
}

static const char uid[] = "test-zymatic-wlan";
static const char authorization[] =
    "Authorization: Bearer abababababababababababababababababababababababababababababababab\r\n";
static uint8_t key[32];

static void feed(ZymaticNetwork *session, const char *request, uint8_t fragment)
{
    size_t remaining = strlen(request);
    while (remaining) {
        uint8_t count = remaining < fragment ? (uint8_t)remaining : fragment;
        zymatic_wifi_receive(session, (const uint8_t *)request, count, key);
        request += count;
        remaining -= count;
    }
}

/* Poll without an acknowledgment, then acknowledge a short write. Compare
 * the retried bytes before copying only the acknowledged prefix to our peer. */
static size_t read_reply(ZymaticNetwork *session, uint32_t now, char *reply, size_t capacity)
{
    size_t used = 0;
    for (unsigned polls = 0; polls < 4096; ++polls) {
        if (zymatic_wifi_closing(session, now)) {
            reply[used] = 0;
            return used;
        }
        uint8_t chunk[16], retried[16];
        uint8_t count = zymatic_wifi_output(session, chunk, key, uid, now, ZYMATIC_MENU);
        assert(count <= sizeof(chunk));
        if (!count) continue;
        assert(zymatic_wifi_output(session, retried, key, uid, now, ZYMATIC_MENU) == count);
        assert(!memcmp(chunk, retried, count));
        uint8_t acknowledged = count > 7 ? 7 : count;
        assert(used + acknowledged < capacity);
        memcpy(reply + used, chunk, acknowledged);
        used += acknowledged;
        zymatic_wifi_sent(session, acknowledged);
    }
    assert(0 && "HTTP response failed to close");
    return 0;
}

static void website(void)
{
    const struct { const char *path, *type; const uint8_t *bytes; size_t size; } assets[] = {
        {"/", "text/html", zymatic_site, sizeof(zymatic_site)},
        {"/pair.js", "application/javascript", zymatic_pair_js, sizeof(zymatic_pair_js)},
        {"/favicon.ico", "image/x-icon", zymatic_favicon, sizeof(zymatic_favicon)},
    };
    ZymaticNetwork session = {0};
    for (size_t i = 0; i < sizeof(assets) / sizeof(assets[0]); ++i) {
        char request[96], reply[4096];
        snprintf(request, sizeof(request), "GET %s HTTP/1.1\r\nHost: brewer\r\n\r\n", assets[i].path);
        zymatic_wifi_connect(&session, 100);
        feed(&session, request, 1);
        size_t length = read_reply(&session, 101, reply, sizeof(reply));
        const char *body = strstr(reply, "\r\n\r\n");
        assert(body && strstr(reply, "200 OK") && strstr(reply, "Content-Encoding: gzip"));
        assert(strstr(reply, assets[i].type));
        body += 4;
        assert(length == (size_t)(body - reply) + assets[i].size);
        assert(!memcmp(body, assets[i].bytes, assets[i].size));
    }
}

static void authenticated_routes(void)
{
    ZymaticNetwork session = {0};
    char request[256], reply[2048];
    snprintf(request, sizeof(request), "GET /api/v1/status HTTP/1.1\r\nHost: brewer\r\n%s\r\n", authorization);
    zymatic_wifi_connect(&session, 50);
    feed(&session, request, 5);
    read_reply(&session, 60, reply, sizeof(reply));
    assert(strstr(reply, "200 OK") && strstr(reply, uid) && strstr(reply, "\"model\":\"zymatic\""));

    char *token = strstr(request, "Bearer ") + strlen("Bearer ");
    token[63] = 'c';
    zymatic_wifi_connect(&session, 75);
    feed(&session, request, 1);
    read_reply(&session, 85, reply, sizeof(reply));
    assert(strstr(reply, "401 Unauthorized") && !strstr(reply, uid));

    snprintf(request, sizeof(request), "GET /api/v1/history HTTP/1.0\r\n%s\r\n", authorization);
    zymatic_wifi_connect(&session, 100);
    feed(&session, request, 13);
    read_reply(&session, 101, reply, sizeof(reply));
    assert(strstr(reply, "\"sessions\":[{\"id\":1,"));
    assert(strstr(reply, "\"state\":\"completed\""));
    assert(strlen(reply) >= 3 && !strcmp(reply + strlen(reply) - 3, "}]}"));
}

static void pairing_claim(void)
{
    const char nonce[] = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
    ZymaticNetwork session = {0};
    char request[384], body[96], reply[512];
    snprintf(body, sizeof(body), "{\"nonce\":\"%s\"}", nonce);
    snprintf(request, sizeof(request), "POST /api/v1/pairing/request HTTP/1.0\r\nContent-Length: %zu\r\n\r\n%s", strlen(body), body);
    zymatic_pair_reset();
    zymatic_wifi_connect(&session, 101);
    feed(&session, request, 3);
    read_reply(&session, 102, reply, sizeof(reply));
    assert(strstr(reply, "202 Accepted") && strstr(reply, "\"code\":\"01234567\""));
    char code[9];
    uint32_t ticket;
    assert(zymatic_pair_prompt(103, code, &ticket) && !strcmp(code, "01234567"));
    assert(zymatic_pair_decide(104, ticket, 1));
    snprintf(request, sizeof(request), "POST /api/v1/pairing/claim HTTP/1.0\r\nContent-Length: %zu\r\n\r\n%s", strlen(body), body);
    zymatic_wifi_connect(&session, 105);
    feed(&session, request, 9);
    read_reply(&session, 106, reply, sizeof(reply));
    assert(strstr(reply, "200 OK") && strstr(reply, "\"api_key\":\"abababab"));
    zymatic_wifi_connect(&session, 107);
    feed(&session, request, 9);
    read_reply(&session, 108, reply, sizeof(reply));
    assert(strstr(reply, "404 Not Found") && !strstr(reply, "api_key"));
}

static void reconnect(void)
{
    ZymaticNetwork session = {0};
    uint8_t chunk[16];
    char reply[2048];
    zymatic_wifi_connect(&session, 100);
    feed(&session, "GET /api/v1/status HTTP/1.0\r\n", 4);
    feed(&session, authorization, 13);
    assert(!zymatic_wifi_output(&session, chunk, key, uid, 101, ZYMATIC_MENU));
    /* A different socket replaces an authenticated, incomplete request. */
    zymatic_wifi_connect(&session, 110);
    feed(&session, "GET /api/v1/status HTTP/1.0\r\n\r\n", 7);
    read_reply(&session, 120, reply, sizeof(reply));
    assert(strstr(reply, "401 Unauthorized") && !strstr(reply, uid));

    /* Drop the peer before the first response finishes, then reuse the slot. */
    zymatic_wifi_connect(&session, 200);
    feed(&session, "GET /pair.js HTTP/1.0\r\n\r\n", 10);
    uint8_t sent = zymatic_wifi_output(&session, chunk, key, uid, 201, ZYMATIC_MENU);
    assert(sent > 0);
    zymatic_wifi_sent(&session, sent);
    zymatic_wifi_connect(&session, 210);
    feed(&session, "GET /api/v1/health HTTP/1.0\r\n\r\n", 2);
    read_reply(&session, 211, reply, sizeof(reply));
    assert(!strncmp(reply, "HTTP/1.0 200 OK\r\n", strlen("HTTP/1.0 200 OK\r\n")));
    const char *body = strstr(reply, "\r\n\r\n");
    assert(body && !strcmp(body + 4, "{\"ok\":true}"));
}

static void deadlines(void)
{
    ZymaticNetwork session = {0};
    const uint32_t start = UINT32_MAX - 99;
    uint8_t chunk[16];
    zymatic_wifi_connect(&session, start);
    feed(&session, "GET /", 1);
    assert(!zymatic_wifi_closing(&session, start + 4999));
    assert(zymatic_wifi_closing(&session, start + 5000));
    zymatic_wifi_connect(&session, start);
    feed(&session, "GET / HTTP/1.0\r\n\r\n", 10);
    assert(zymatic_wifi_output(&session, chunk, key, uid, start + 1, ZYMATIC_MENU));
    assert(!zymatic_wifi_closing(&session, start + 29999));
    assert(zymatic_wifi_closing(&session, start + 30000));
}

static void invalid_fragment(void)
{
    ZymaticNetwork session = {0};
    uint8_t chunk[17] = {0};
    zymatic_wifi_connect(&session, 100);
    zymatic_wifi_receive(&session, chunk, sizeof(chunk), key);
    assert(zymatic_wifi_closing(&session, 101));
    zymatic_wifi_connect(&session, 102);
    feed(&session, "GET / HTTP/1.0\r\n\r\n", 16);
    assert(zymatic_wifi_output(&session, chunk, key, uid, 103, ZYMATIC_MENU) == 16);
    zymatic_wifi_sent(&session, 17);
    assert(zymatic_wifi_closing(&session, 104));
}

/* The foreground accepts a connection on port 80, then reads and writes the
 * same status response the Hub consumes. Inject HCI replies after submission,
 * never recursively from inside the transmit callback. */
struct radio_fixture {
    uint8_t frame[ZYM_CC3000_MAX_TX_FRAME];
    uint8_t size;
};

static enum zym_cc3000_submit_result stage_frame(void *context, const uint8_t *frame, uint8_t size)
{
    struct radio_fixture *f = context;
    assert(!f->size && size <= sizeof(f->frame));
    memcpy(f->frame, frame, size);
    f->size = size;
    return ZYM_WIFI_SENT;
}

static void radio_http(void)
{
    struct zym_cc3000 radio = {0};
    ZymaticNetwork http = {0};
    struct radio_fixture f = {0};
    struct zym_cc3000_broker broker;
    zym_broker_begin(&broker, stage_frame, &f);
    char request[220], response[2048] = {0};
    snprintf(request, sizeof(request), "GET /api/v1/status HTTP/1.1\r\nHost: brewer\r\n%s\r\n", authorization);
    const char *unread = request;
    size_t written = 0;
    uint8_t frame[48] = {0}, length = 0;
    uint32_t now = 0;
    zym_cc3000_begin(&radio, 80);
    for (; now < 4000; ++now) {
        if (length) assert(zym_broker_irq(&broker, frame, length) == ZYM_RADIO_HOLD_SPI);
        bool consumed = zymatic_wifi_server_poll(&broker, &radio, &http, key, uid, now, ZYMATIC_MENU);
        assert(consumed == (length != 0));
        if (length && frame[0] == 4 && frame[1] == 4) {
            uint8_t count = frame[9];
            memset(frame, 0, sizeof(frame));
            frame[0] = 2; frame[1] = 0x85; frame[3] = count;
            memcpy(frame + 5, unread, count);
            length = 5 + count;
            unread += count;
            continue;
        }
        length = 0;
        if (!f.size) continue;
        uint8_t op = f.frame[0] == 2 ? 3 : f.frame[1];
        memset(frame, 0, sizeof(frame));
        frame[0] = 4; frame[1] = op; frame[2] = 0x10;
        length = 9;
        switch (op) {
        case 1: frame[5] = 1; break; /* listener */
        case 2: assert(f.frame[18] == 0 && f.frame[19] == 80); break;
        case 6: case 9: break;
        case 5: length = 29; frame[5] = 1; frame[9] = 3; break;
        case 8: length = 21; frame[5] = 1; frame[9] = 8; break;
        case 4: {
            size_t count = strlen(unread);
            assert(count > 0);
            length = 17; frame[5] = 3; frame[9] = count > 7 ? 7 : (uint8_t)count;
            break;
        }
        case 3: {
            uint8_t acked = f.frame[13] > 9 ? 9 : f.frame[13];
            assert(written + acked < sizeof(response));
            memcpy(response + written, f.frame + 21, acked);
            written += acked;
            length = 13; frame[5] = 3; frame[9] = acked;
            break;
        }
        case 11:
            assert(f.frame[4] == 3 && !*unread);
            assert(strstr(response, "200 OK") && strstr(response, uid));
            frame[3] = length - 4;
            assert(zym_broker_irq(&broker, frame, length) == ZYM_RADIO_HOLD_SPI);
            assert(zymatic_wifi_server_poll(&broker, &radio, &http, key, uid, now, ZYMATIC_MENU));
            assert(!radio.connected && !memcmp(&http, &(ZymaticNetwork){0}, sizeof(http)));
            return;
        default: assert(0 && "unexpected HCI opcode");
        }
        frame[3] = length - 4;
        f.size = 0;
    }
    assert(0 && "Wi-Fi HTTP exchange failed to finish");
}

int main(void)
{
    memset(key, 0xab, sizeof(key));
    zh_test_seed();
    radio_http();
    website();
    authenticated_routes();
    pairing_claim();
    reconnect();
    deadlines();
    invalid_fragment();
    return 0;
}
