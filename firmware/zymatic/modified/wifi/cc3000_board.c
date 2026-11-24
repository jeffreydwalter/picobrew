#include "cc3000_board.h"
#include "cc3000_spi_bus.h"
#include <stddef.h>
#include <string.h>

#define WLAN_FLAGS (*(volatile uint8_t *)0x24ee)
#define WLAN_CALLBACK (*(volatile uint16_t *)0x264b)
#define STOCK_CALLBACK (0x11e48uL / 2)

/* Stock may see a disconnect and finish reconnecting *during* its own
 * synchronous operation. Preserve that boundary for the HTTP session. */
static void wlan_notify(uint32_t event, uint8_t *data, uint8_t size)
{
    if (event == 0x8002) zym_spi_station_events |= ZYM_STATION_DROPPED;
    if (event == 0x8010) {
        if (size == 21 && data && !data[20]) zym_spi_station_events &= (uint8_t)~ZYM_DHCP_FAILED;
        else zym_spi_station_events |= ZYM_STATION_DROPPED | ZYM_DHCP_FAILED;
    }
    void (*const original)(uint32_t, uint8_t *, uint8_t) =
        (void (*)(uint32_t, uint8_t *, uint8_t))STOCK_CALLBACK;
    original(event, data, size);
}

bool zym_cc3000_board_begin(struct zym_cc3000_broker *broker, struct zym_cc3000 *radio,
                            uint16_t port)
{
    /* The stock WLAN callback sets these bits on connect and DHCP, and
     * clears both on disconnect. Stock setup owns its callback pointer. */
    if (!port || (WLAN_FLAGS & 6) != 6 || WLAN_CALLBACK != STOCK_CALLBACK ||
        !zym_spi_bind()) return false;
    zym_spi_station_events = 0;
    WLAN_CALLBACK = (uint16_t)(uintptr_t)wlan_notify;
    zym_broker_begin(broker, zym_spi_submit, NULL);
    zym_cc3000_begin(radio, port);
    return true;
}

/* Call the actual 1.1.14 asynchronous decoder directly. Its usual caller,
 * 0x135ac, would reenable the stock foreground SPI reader at 0x129c6.
 * The bounded receiver already owns this packet. Verify variable fields
 * before the original decoder is allowed to read them. */
static bool stock_event(uint8_t *frame, uint8_t size)
{
    if (size < 5 || frame[0] != 4 || size != 4u + frame[3]) return false;
    uint16_t op = (uint16_t)frame[2] << 8 | frame[1];
    switch (op) {
    case 0x4100: { /* returned TX buffers: count, then four bytes per handle */
        if (size < 7 || frame[6] || 7u + 4u * frame[5] != size) return false;
        uint32_t outstanding = *(volatile uint32_t *)0x2661 - *(volatile uint32_t *)0x2665;
        for (uint8_t i = 9; i < size; i += 4) {
            uint16_t released = (uint16_t)frame[i+1] << 8 | frame[i];
            if (released > outstanding) return false;
            outstanding -= released;
        }
        break;
    }
    case 0x8010: case 0x8040: /* DHCP and ping contain five 32-bit fields */
        if (size < 25) return false;
        break;
    case 0x8800: /* TCP close-wait supplies a descriptor */
        if (size < 6 || frame[5] >= 8) return false;
        break;
    case 0x1003: case 0x100e: case 0x100f: /* failed SEND, WRITE or SENDTO */
        if (size < 13) return false;
        break;
    }
    int32_t (*const classify)(uint8_t *) = (int32_t (*)(uint8_t *))(0x13178uL / 2);
    return classify(frame) == 1;
}

void zym_cc3000_board_poll(struct zym_cc3000_broker *broker, struct zym_cc3000 *radio,
                           ZymaticNetwork *http, const uint8_t key[32], const char *uid,
                           uint32_t now, uint8_t mode)
{
    zym_spi_flush_pending();
    if (broker->owner == ZYM_RADIO_OEM) return;
    if (broker->owner != ZYM_RADIO_LOST) {
        uint8_t *frame;
        uint8_t size;
        enum zym_spi_rx received = zym_spi_receive(&frame, &size);
        if (received == ZYM_SPI_LINK_LOST) zym_broker_link_lost(broker);
        if (received == ZYM_SPI_FRAME) {
            enum zym_radio_rx route = zym_broker_irq(broker, frame, size);
            if (route == ZYM_RADIO_PASS_TO_OEM && !stock_event(frame, size))
                zym_broker_link_lost(broker);
            if (route != ZYM_RADIO_HOLD_SPI) zym_spi_release_frame();
        }
        if (zym_spi_station_events & ZYM_STATION_DROPPED) {
            broker->draining = true;
            zym_spi_station_events &= (uint8_t)~ZYM_STATION_DROPPED;
            memset(http, 0, sizeof(*http));
        }
        if (broker->draining && broker->owner == ZYM_RADIO_FREE && !broker->http_sockets &&
            !(zym_spi_station_events & ZYM_DHCP_FAILED) && (WLAN_FLAGS & 6) == 6) {
            zym_cc3000_begin(radio, radio->port);
            broker->draining = false;
        }
    }
    if (zymatic_wifi_server_poll(broker, radio, http, key, uid, now, mode))
        zym_spi_release_frame();
    if (broker->owner == ZYM_RADIO_LOST) zym_spi_close_if_owned();
}
