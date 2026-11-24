#ifndef ZYMATIC_CC3000_BROKER_H
#define ZYMATIC_CC3000_BROKER_H
#include "cc3000.h"

enum zym_radio_owner { ZYM_RADIO_FREE, ZYM_RADIO_OEM, ZYM_RADIO_HTTP, ZYM_RADIO_LOST };
enum zym_radio_rx { ZYM_RADIO_PASS_TO_OEM, ZYM_RADIO_HOLD_SPI, ZYM_RADIO_DISCARD };

/* Members are accessed in one serialized driver context. Board code must
 * protect transitions shared with the SPI IRQ. Never mask interrupts for a
 * radio wait. The broker borrows one RX buffer with reception paused, just
 * as the stock completion decoder does. No copy of OEM payloads is made. */
struct zym_cc3000_broker {
    enum zym_radio_owner owner;
    uint16_t expected, rx_size;
    uint8_t http_sockets, submitted_socket;
    const uint8_t *rx;
    zym_cc3000_submit try_write;
    void *hardware;
    bool await_data : 1;
    bool oem_queued : 1;
    bool draining : 1;
};

void zym_broker_begin(struct zym_cc3000_broker *b, zym_cc3000_submit bounded_spi,
                      void *hardware);
/* Acquire before the original operation builds a command, release only after
 * its last synchronous response/data. A deferred OEM caller has priority. */
bool zym_broker_acquire_oem(struct zym_cc3000_broker *b);
void zym_broker_release_oem(struct zym_cc3000_broker *b);
/* Replace the sole RX classifier call at OEM byte address 0x15522.
 * HOLD leaves SPI paused and the packet unchanged for the foreground poll.
 * DISCARD releases the held physical packet without passing it to the OEM. */
enum zym_radio_rx zym_broker_irq(struct zym_cc3000_broker *b,
                                 const uint8_t *frame, uint16_t size);
/* A true return releases the borrowed radio RX packet. Caller clears the OEM
 * event-ready flag and resumes SPI exactly once, after this poll returns. */
bool zym_broker_poll(struct zym_cc3000_broker *b, struct zym_cc3000 *s,
                     uint32_t now, struct zym_cc3000_event *out);
void zym_broker_link_lost(struct zym_cc3000_broker *b);
#endif
