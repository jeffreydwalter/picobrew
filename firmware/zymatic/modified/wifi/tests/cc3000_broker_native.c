#include "../cc3000_broker.h"
#include <assert.h>
#include <stdio.h>
#include <string.h>

static struct zym_cc3000 server;
static struct zym_cc3000_broker radio;
static struct zym_cc3000_event event;
static uint32_t now;
static unsigned writes;
static uint8_t packet[32];
static bool answer_during_submit;
static const uint8_t socket_open[] = {4,1,0x10,5,0,2,0,0,0};

static enum zym_cc3000_submit_result hardware(void *unused, const uint8_t *frame, uint8_t n)
{
    (void)unused;
    assert(n >= 4 && frame[0] != 0);
    ++writes;
    if (answer_during_submit) {
        assert(zym_broker_irq(&radio, socket_open, sizeof(socket_open)) == ZYM_RADIO_HOLD_SPI);
        answer_during_submit = false;
    }
    return ZYM_WIFI_SENT;
}

static void poll(bool held)
{
    assert(zym_broker_poll(&radio, &server, now, &event) == held);
}

static void held_reply(uint8_t op, uint8_t n, uint8_t first, uint8_t second)
{
    memset(packet, 0, sizeof(packet));
    packet[0] = 4; packet[1] = op; packet[2] = 0x10; packet[3] = n + 1;
    packet[5] = first; packet[9] = second;
    assert(zym_broker_irq(&radio, packet, n + 5) == ZYM_RADIO_HOLD_SPI);
    poll(true);
}

static void command(uint8_t op, uint8_t n, uint8_t first, uint8_t second)
{
    unsigned before = writes;
    poll(false);
    assert(writes == before + 1 && radio.owner == ZYM_RADIO_HTTP);
    held_reply(op, n, first, second);
}

int main(void)
{
    const uint8_t credits[] = {4,0,0x41,7,0,1,0,0,0,1,0};
    const uint8_t old_send_failure[] = {4,3,0x10,9,1,7,0,0,0,0xc7,0xff,0xff,0xff};
    zym_cc3000_begin(&server, 80);
    zym_broker_begin(&radio, hardware, NULL);
    assert(zym_broker_acquire_oem(&radio));
    poll(false);
    assert(!writes);
    assert(zym_broker_irq(&radio, socket_open, sizeof(socket_open)) == ZYM_RADIO_PASS_TO_OEM);
    zym_broker_release_oem(&radio);
    answer_during_submit = true;
    poll(false);
    assert(radio.rx == socket_open);
    assert(!zym_broker_acquire_oem(&radio)); /* OEM requests the very same SOCKET opcode */
    poll(true); assert(radio.http_sockets == (1u << 2));
    poll(false); assert(writes == 1); /* next HTTP command yields to waiting OEM */
    assert(zym_broker_acquire_oem(&radio));
    zym_broker_release_oem(&radio);
    command(9, 4, 0, 0); command(2, 4, 0, 0); command(6, 4, 0, 0);
    command(5, 24, 2, 4);
    uint32_t peer = event.peer;
    assert(event.kind == ZYM_WIFI_OPEN && radio.http_sockets == 0x14);
    assert(zym_cc3000_read(&server, peer));
    command(8,16,1,16);
    poll(false);
    assert(zym_broker_irq(&radio, credits, sizeof(credits)) == ZYM_RADIO_PASS_TO_OEM);
    assert(zym_broker_irq(&radio, old_send_failure, sizeof(old_send_failure)) == ZYM_RADIO_PASS_TO_OEM);
    held_reply(4,12,4,1);
    assert(radio.owner == ZYM_RADIO_HTTP && radio.await_data);
    assert(!zym_broker_acquire_oem(&radio));
    assert(zym_broker_irq(&radio, (uint8_t[]){2,0x85,0,1,0,'x'}, 6) == ZYM_RADIO_HOLD_SPI);
    poll(true);
    assert(event.kind == ZYM_WIFI_RX && event.peer == peer && event.data[0] == 'x');
    assert(zym_broker_acquire_oem(&radio));
    zym_broker_release_oem(&radio);

    assert(zym_cc3000_close(&server, peer));
    command(11,4,0,0);
    assert(radio.http_sockets == 4 && event.kind == ZYM_WIFI_CLOSED);
    poll(false);
    now += 3000; poll(false);
    assert(radio.owner == ZYM_RADIO_LOST && event.kind == ZYM_WIFI_LINK_FAILED);
    assert(!zym_broker_acquire_oem(&radio));
    assert(zym_broker_irq(&radio, socket_open, sizeof(socket_open)) == ZYM_RADIO_DISCARD);
    assert(zym_broker_irq(&radio, credits, sizeof(credits)) == ZYM_RADIO_PASS_TO_OEM);
    zym_broker_begin(&radio, hardware, NULL); zym_cc3000_begin(&server, 80);
    poll(false);
    assert(zym_broker_irq(&radio, socket_open, sizeof(socket_open)) == ZYM_RADIO_HOLD_SPI);
    zym_broker_link_lost(&radio); poll(true);
    assert(event.kind == ZYM_WIFI_LINK_FAILED && !radio.rx);
    puts("CC3000 radio ownership, deferred OEM commands, event routing and generation loss passed");
}
