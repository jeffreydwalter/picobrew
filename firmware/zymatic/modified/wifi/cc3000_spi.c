/* Experimental physical SPI layer for the pinned Zymatic 1.1.14 image. */
#include "cc3000_spi.h"
#include "cc3000_spi_bus.h"
#include <avr/io.h>
#include <stddef.h>
#include <util/atomic.h>

#define OEM8(addr)  (*(volatile uint8_t *)(uintptr_t)(addr))
#define OEM16(addr) (*(volatile uint16_t *)(uintptr_t)(addr))
#define OEM32(addr) (*(volatile uint32_t *)(uintptr_t)(addr))
#define OEM_SPI_STATE OEM32(0x252d)
#define OEM_INT2_HANDLER OEM16(0x2b1f)
#define OEM_POLL_ENABLED OEM8(0x263c)
#define OEM_TX_CREDITS OEM16(0x2659)
#define OEM_PACKETS_SENT OEM32(0x2661)

enum { UNBOUND, ACTIVE, RX_HELD, QUARANTINED, STOCK, STOCK_ONLY };
static volatile uint8_t link; /* OLED supervisor reads active stock ownership. */
static volatile uint8_t irq_edge;
volatile uint8_t zym_spi_station_events;
struct transaction { uint8_t spcr, spi2x, started, millis; uint16_t polls; };

/* Install this callback instead of the stock 0x12b08 SPI IRQ body. */
void zym_spi_record_irq(void) { irq_edge = 1; }

static bool bind_from(uint8_t initial)
{
    bool ok = false;
    ATOMIC_BLOCK(ATOMIC_RESTORESTATE) {
        /* The OEM has initialized a normal radio generation, has no held
         * RX/data/completion, and uses PC4 CS and PB2/INT2. The checked
         * radio mode is the divider written by its own setup routine. */
        if (link == initial && OEM8(0x230b) == 0 && OEM_SPI_STATE == 2 &&
            OEM16(0x2533) == 0x2535 && OEM8(0x263a) == 0xde && OEM8(0x25b7) == 0xde &&
            OEM16(0x263d) == 0 && OEM16(0x263f) == 0 && OEM16(0x265f) == 0 &&
            OEM8(0x24b7) == 2 && (EIMSK & _BV(INT2)) &&
            ((OEM_POLL_ENABLED && OEM_INT2_HANDLER == 0x12b08uL/2) ||
             (!OEM_POLL_ENABLED && !OEM_INT2_HANDLER)) &&
            (EICRA & (_BV(ISC20)|_BV(ISC21))) == _BV(ISC21) &&
            (DDRC & PORTC & _BV(PC4)) && !(DDRB & _BV(PB2)) &&
            OEM8(0x2523) == (_BV(SPE)|_BV(MSTR)|_BV(CPHA)) && OEM8(0x2524) == 1 &&
            (TCCR0B & 7u) == 3u) { /* 16 MHz, stock Timer0 at F_CPU/64 */
            OEM_INT2_HANDLER = (uint16_t)(uintptr_t)zym_spi_record_irq;
            OEM_POLL_ENABLED = 0; /* stop the separate foreground IRQ fallback */
            irq_edge = !(PINB & _BV(PB2));
            link = ACTIVE;
            ok = true;
        }
    }
    return ok;
}

bool zym_spi_bind(void)
{
    return bind_from(link == STOCK_ONLY ? STOCK_ONLY : UNBOUND);
}

static void quarantine(void)
{
    ATOMIC_BLOCK(ATOMIC_RESTORESTATE) {
        if (OEM_INT2_HANDLER == (uint16_t)(uintptr_t)zym_spi_record_irq ||
            OEM_INT2_HANDLER == 0x12b08uL/2) EIMSK &= (uint8_t)~_BV(INT2);
        OEM_POLL_ENABLED = 0;
        irq_edge = 0;
        link = QUARANTINED;
    }
}

void zym_spi_close_if_owned(void)
{
    if (link == ACTIVE || link == RX_HELD) quarantine();
}

bool zym_spi_stock_selected(void)
{
    return link >= STOCK && (DDRC & _BV(PC4)) && !(PORTC & _BV(PC4));
}

/* A stuck or reconfigured Timer0 cannot cause an infinite wait. The single
 * allowance covers CS handshaking and every SPI byte of this transaction.
 * 192 stock Timer0 ticks are 768 us; this is a bench policy, not a timing
 * guarantee supplied by TI. The work counter also bounds foreground CPU use. */
static bool within_budget(struct transaction *t)
{
    if (!t->polls) return false;
    --t->polls;
    return (uint8_t)(TCNT0 - t->started) < 192 && (uint8_t)(OEM8(0x2b25) - t->millis) < 2;
}

static bool byte_io(struct transaction *t, uint8_t tx, uint8_t *rx)
{
    SPDR = tx;
    do {
        uint8_t status = SPSR;
        if (status & _BV(WCOL)) return false;
        if (status & _BV(SPIF)) {
            *rx = SPDR;
            return (SPCR & (_BV(SPE)|_BV(MSTR))) == (_BV(SPE)|_BV(MSTR));
        }
    } while (within_budget(t));
    return false;
}

static bool owns_radio(void)
{
    if (link != ACTIVE && link != RX_HELD) return false;
    if (OEM_INT2_HANDLER != (uint16_t)(uintptr_t)zym_spi_record_irq ||
        OEM_POLL_ENABLED || OEM8(0x230b) || OEM_SPI_STATE != 2 ||
        OEM16(0x2533) != 0x2535 || OEM8(0x25b7) != 0xde) {
        quarantine();
        return false;
    }
    return true;
}

bool zym_spi_yield_to_stock(struct zym_cc3000_broker *b)
{
    if (!owns_radio() || link == RX_HELD || (zym_spi_station_events & ZYM_STATION_DROPPED) ||
        !zym_broker_acquire_oem(b)) return false;
    bool ok;
    ATOMIC_BLOCK(ATOMIC_RESTORESTATE) {
        ok = !irq_edge && (PINB & _BV(PB2)) && (PORTC & _BV(PC4));
        if (ok) {
            link = STOCK;
            OEM_INT2_HANDLER = 0x12b08uL/2;
            OEM_POLL_ENABLED = 1;
        }
    }
    if (!ok) zym_broker_release_oem(b);
    return ok;
}

bool zym_spi_resume_from_stock(struct zym_cc3000_broker *b)
{
    if (b->owner != ZYM_RADIO_OEM || !bind_from(STOCK)) return false;
    zym_broker_release_oem(b);
    return true;
}

void zym_spi_stock_restart(struct zym_cc3000_broker *b)
{
    if (link == STOCK) {
        /* The OEM owns the bus/IRQ. It continues its original startup even
         * though HTTP descriptors from the preceding generation are invalid. */
        link = STOCK_ONLY;
        zym_broker_link_lost(b);
    }
}

static void select_radio(struct transaction *t)
{
    t->started = TCNT0;
    t->millis = OEM8(0x2b25); /* low byte of the stock millis() clock */
    t->polls = 1024;
    t->spcr = SPCR;
    t->spi2x = SPSR & _BV(SPI2X);
    PORTC &= (uint8_t)~_BV(PC4);
    SPCR = OEM8(0x2523);
    SPSR = OEM8(0x2524);
    (void)SPSR;
    (void)SPDR; /* clear old SPIF/WCOL after changing owners */
}

static void finish(struct transaction *t, bool ok)
{
    PORTC |= _BV(PC4);
    if (!ok) {
        SPCR &= (uint8_t)~_BV(SPE); /* terminate the AVR's local shift engine */
        quarantine();              /* no CC3000 reset or PD7 toggle */
    }
    SPCR = t->spcr;
    SPSR = t->spi2x;
    zym_spi_release_radio();
}

enum zym_cc3000_submit_result zym_spi_submit(void *unused, const uint8_t *frame, uint8_t size)
{
    (void)unused;
    if (!owns_radio() || !frame || size < 4 || size > ZYM_CC3000_MAX_TX_FRAME)
        return ZYM_WIFI_BAD_LINK;
    bool data = frame[0] == 2;
    if (data ? (size < 5 || frame[1] != 0x81 || frame[4] || frame[3] != size-5)
             : (frame[0] != 1 || frame[3] != size-4)) return ZYM_WIFI_BAD_LINK;
    if (link == RX_HELD || !(SREG & _BV(SREG_I)) || !zym_spi_take_radio()) return ZYM_WIFI_BUSY;
    struct transaction t;
    bool started = false;
    ATOMIC_BLOCK(ATOMIC_RESTORESTATE) {
        if (!irq_edge && (PINB & _BV(PB2)) && (!data || OEM_TX_CREDITS)) {
            select_radio(&t);
            started = true;
        }
    }
    if (!started) { zym_spi_release_radio(); return ZYM_WIFI_BUSY; }
    while (PINB & _BV(PB2)) {
        if (!within_budget(&t)) { finish(&t, false); return ZYM_WIFI_BAD_LINK; }
    }
    ATOMIC_BLOCK(ATOMIC_RESTORESTATE) {
        /* Consume the write-ready edge. A subsequent reply has a separate
         * falling edge; the foreground receive path also checks the pin. */
        irq_edge = 0;
        EIFR = _BV(INTF2);
        if (data) {
            --OEM_TX_CREDITS;
            ++OEM_PACKETS_SENT;
        }
    }
    uint8_t padded = size | 1u;
    uint8_t header[5] = {1, 0, padded, 0, 0}, ignored;
    bool ok = true;
    for (uint8_t i=0; ok && i<5; ++i) ok = byte_io(&t, header[i], &ignored);
    for (uint8_t i=0; ok && i<size; ++i) ok = byte_io(&t, frame[i], &ignored);
    if (ok && padded != size) ok = byte_io(&t, 0, &ignored);
    if (ok) ok = within_budget(&t);
    /* A partial write may have consumed a real chip credit. Never refund
     * or retry ambiguous bytes. Stock credit events account for success. */
    finish(&t, ok);
    return ok ? ZYM_WIFI_SENT : ZYM_WIFI_BAD_LINK;
}

void zym_spi_release_frame(void)
{
    if (link == RX_HELD) link = ACTIVE;
}

enum zym_spi_rx zym_spi_receive(uint8_t **result, uint8_t *size)
{
    uint8_t *frame = (uint8_t *)(uintptr_t)0x253a;
    if (!size || !result) return ZYM_SPI_LINK_LOST;
    *result = NULL;
    *size = 0;
    if (!owns_radio()) return ZYM_SPI_LINK_LOST;
    if (link == RX_HELD || !(SREG & _BV(SREG_I)) || !zym_spi_take_radio()) return ZYM_SPI_NO_FRAME;
    struct transaction t;
    bool ready;
    ATOMIC_BLOCK(ATOMIC_RESTORESTATE) {
        /* A finished or spurious edge with IRQ high has no readable frame.
         * This level check also services an edge missed during a flush. */
        irq_edge = 0;
        ready = !(PINB & _BV(PB2));
        if (ready) select_radio(&t);
    }
    if (!ready) { zym_spi_release_radio(); return ZYM_SPI_NO_FRAME; }
    uint8_t ignored;
    bool ok = true;
    for (uint8_t i=0; ok && i<5; ++i) ok = byte_io(&t, 3, &ignored);
    for (uint8_t i=0; ok && i<5; ++i) ok = byte_io(&t, 3, &frame[i]);
    uint16_t rest = 0;
    bool pad = false;
    if (ok && frame[0] == 2) {
        rest = (uint16_t)frame[4] << 8 | frame[3];
        ok = frame[2] <= rest;
        pad = !(rest & 1);
    } else if (ok && frame[0] == 4 && frame[3]) {
        rest = frame[3]-1;
        pad = rest & 1;
    } else ok = false;
    if (rest > ZYM_SPI_MAX_HCI-5u || rest + (uint8_t)pad > 120u) ok = false;
    for (uint8_t i=0; ok && i<rest; ++i) ok = byte_io(&t, 3, &frame[5+i]);
    if (ok && pad) ok = byte_io(&t, 3, &ignored);
    if (ok) ok = within_budget(&t);
    finish(&t, ok);
    if (!ok) return ZYM_SPI_LINK_LOST;
    link = RX_HELD;
    *result = frame;
    *size = 5+(uint8_t)rest;
    return ZYM_SPI_FRAME;
}
