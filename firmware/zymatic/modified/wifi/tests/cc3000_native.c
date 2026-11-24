#include "../cc3000.h"
#include <assert.h>
#include <stdio.h>
#include <string.h>

static struct zym_cc3000 server;
static struct zym_cc3000_event event;
static enum zym_cc3000_submit_result acceptance = ZYM_WIFI_SENT;
static uint32_t now;
static uint8_t tx[48], tx_size;
static unsigned submissions;
static bool trace;

static void vector(char direction, const uint8_t *p, uint16_t size)
{
    if (!trace) return;
    printf("%c ", direction);
    for (uint16_t i = 0; i < size; ++i) printf("%02x", p[i]);
    putchar('\n');
}

static enum zym_cc3000_submit_result submit(void *context, const uint8_t *p, uint8_t n)
{
    (void)context;
    assert(n <= sizeof(tx));
    memcpy(tx, p, n); tx_size = n; submissions++;
    vector('>', p, n);
    return acceptance;
}

static void tick(void)
{
    assert(!zym_cc3000_poll(&server, now, submit, NULL, NULL, 0, &event));
}

static void expect_command(uint8_t op, const uint8_t *args, uint8_t size)
{
    unsigned previous = submissions;
    tick();
    assert(submissions == previous + 1);
    assert(tx_size == 4 + size && tx[0] == 1 && tx[1] == op &&
           tx[2] == 0x10 && tx[3] == size && !memcmp(tx + 4, args, size));
}

static bool feed(const uint8_t *p, uint16_t n)
{
    return zym_cc3000_poll(&server, now, submit, NULL, p, n, &event);
}

static void reply(uint8_t op, const uint8_t *args, uint8_t n)
{
    uint8_t frame[48] = {4, op, 0x10, (uint8_t)(1 + n), 0};
    assert(5 + n <= (int)sizeof(frame));
    memcpy(frame + 5, args, n);
    vector('<', frame, 5 + n);
    assert(feed(frame, 5 + n));
}

static uint32_t connect_client(void)
{
    uint8_t socket_args[12] = {2, [4]=1, [8]=6};
    uint8_t option[21] = {1, [4]=255, [5]=255, [8]=2, [12]=8, [16]=1};
    uint8_t bind_args[20] = {1, [4]=8, [8]=8, [12]=2, [14]=0x1f, [15]=0x90};
    uint8_t descriptor[8] = {1};
    uint8_t zero[4] = {0};
    uint8_t accepted[24] = {1, [4]=3};
    zym_cc3000_begin(&server, 8080);
    expect_command(1, socket_args, 12);
    unsigned previous = submissions;
    tick(); assert(submissions == previous && !event.kind);
    assert(!feed((uint8_t[]){4,0,0x41,1,0}, 5)); /* broker owns flow-control events */
    reply(1, descriptor, 4);
    expect_command(9, option, 21); reply(9, zero, 4);
    expect_command(2, bind_args, 20); reply(2, zero, 4);
    expect_command(6, descriptor, 8); reply(6, zero, 4);
    expect_command(5, descriptor, 4);
    reply(5, (uint8_t[24]){1,[4]=0xfe,255,255,255}, 24);
    previous = submissions;
    tick(); assert(submissions == previous);
    now += 40;
    expect_command(5, descriptor, 4); reply(5, accepted, 24);
    assert(event.kind == ZYM_WIFI_OPEN && event.peer);
    return event.peer;
}

static void receive_event(uint32_t peer)
{
    uint8_t select_args[44] = {4, [4]=20, [8]=20, [12]=20, [16]=20,
                               [24]=8, [40]=0x88, [41]=0x13};
    assert(zym_cc3000_read(&server, peer));
    expect_command(8, select_args, sizeof(select_args)); reply(8, (uint8_t[16]){0}, 16);
    expect_command(8, select_args, sizeof(select_args));
    reply(8, (uint8_t[16]){1,[4]=8}, 16);
    expect_command(4, (uint8_t[12]){3,[4]=16}, 12);
    reply(4, (uint8_t[12]){3,[4]=3}, 12);
    assert(event.kind == ZYM_WIFI_NONE);
}

int main(int argc, char **argv)
{
    trace = argc == 2 && !strcmp(argv[1], "--vectors");
    now = UINT32_MAX - 45;
    uint32_t peer = connect_client();
    receive_event(peer);
    assert(!feed((uint8_t[]){4,0,0x82,1,0}, 5));
    assert(feed((uint8_t[]){2,0x85,4,7,0,0,0,0,0,'G','E','T'}, 12));
    assert(event.kind == ZYM_WIFI_RX && event.peer == peer && event.size == 3 &&
           !memcmp(event.data, "GET", 3));
    assert(!zym_cc3000_send(&server, peer + 1, (uint8_t *)"abc", 3));
    assert(!zym_cc3000_send(&server, peer, (uint8_t *)"abc", 17));
    uint8_t data[] = "abc";
    assert(zym_cc3000_send(&server, peer, data, 3)); data[0] = 'X';
    acceptance = ZYM_WIFI_BUSY; tick();
    const uint8_t send_frame[24] = {2,0x81,16,19,0,3,[9]=12,[13]=3,[21]='a','b','c'};
    assert(!event.kind && tx_size == sizeof(send_frame) && !memcmp(tx, send_frame, tx_size));
    acceptance = ZYM_WIFI_SENT; tick();
    assert(!event.kind && !memcmp(tx, send_frame, tx_size));
    reply(3, (uint8_t[8]){3,[4]=2}, 8);
    assert(event.kind == ZYM_WIFI_TX_ACK && event.peer == peer && event.size == 2);
    assert(zym_cc3000_send(&server, peer, (uint8_t *)"c", 1)); tick();
    assert(zym_cc3000_close(&server, peer));
    reply(3, (uint8_t[8]){3,[4]=1}, 8); assert(event.kind == ZYM_WIFI_TX_ACK);
    expect_command(11, (uint8_t[4]){3}, 4); reply(11, (uint8_t[4]){0}, 4);
    assert(event.kind == ZYM_WIFI_CLOSED && event.peer == peer);
    assert(!zym_cc3000_read(&server, peer));
    trace = false; /* the remaining cases deliberately simulate radio faults */

    uint32_t peer2 = connect_client();
    assert(peer2 != peer && !zym_cc3000_close(&server, peer));
    receive_event(peer2);
    assert(feed((uint8_t[]){2,0x85,0,2,0,'G','E'}, 7));
    assert(event.kind == ZYM_WIFI_LINK_FAILED && event.peer == peer2);
    peer = connect_client();
    assert(zym_cc3000_read(&server, peer)); tick();
    now += 3000; tick();
    assert(event.kind == ZYM_WIFI_LINK_FAILED && event.peer == peer);
    assert(!feed((uint8_t[21]){4,8,0x10,17}, 21)); /* late completion cannot resurrect a session */
    peer = connect_client();
    assert(zym_cc3000_send(&server, peer, (uint8_t *)"a", 1)); tick();
    reply(3, (uint8_t[8]){2,[4]=1}, 8);
    assert(event.kind == ZYM_WIFI_LINK_FAILED);
    peer = connect_client();
    zym_cc3000_link_lost(&server, &event);
    assert(event.kind == ZYM_WIFI_LINK_FAILED && event.peer == peer);
    puts("CC3000 listener, receive, acknowledged send, close and ownership checks passed");
}
