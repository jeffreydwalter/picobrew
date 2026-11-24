/* Bench-only board handoff for the OEM Zymatic 1.1.14 display and radio. */
#include "cc3000_spi_bus.h"
#include <avr/io.h>
#include <util/atomic.h>
#include <stdint.h>

enum { BUS_IDLE, BUS_RADIO, BUS_DISPLAY };
static volatile uint8_t owner;
static volatile uint8_t display_pending;

bool zym_spi_take_radio(void)
{
    bool claimed = false;
    ATOMIC_BLOCK(ATOMIC_RESTORESTATE) {
        if (owner == BUS_IDLE && !display_pending) {
            owner = BUS_RADIO;
            claimed = true;
        }
    }
    return claimed;
}

void zym_spi_release_radio(void)
{
    ATOMIC_BLOCK(ATOMIC_RESTORESTATE) {
        if (owner == BUS_RADIO)
            owner = BUS_IDLE;
    }
}

void zym_spi_flush_pending(void)
{
    bool claimed = false;
    uint8_t restore_int2 = 0;
    ATOMIC_BLOCK(ATOMIC_RESTORESTATE) {
        /* During a stock loan the original driver owns PC4. It can hold CS
         * low across foreground work and can start a new read from INT2.
         * Dormant stock Wi-Fi (including a pin still in its reset input
         * mode) must never delay the boot or wired display. */
        if (owner == BUS_IDLE && display_pending && !zym_spi_stock_selected()) {
            owner = BUS_DISPLAY;
            display_pending = 0;
            claimed = true;
            restore_int2 = EIMSK & _BV(INT2);
            EIMSK &= (uint8_t)~_BV(INT2);
        }
    }
    if (claimed) {
        /* Both patched paths set mode 0 before flushing this same object.
         * Its 1024-byte framebuffer holds the most recent drawing. */
        SPCR &= (uint8_t)~(_BV(CPOL) | _BV(CPHA));
        void (*flush)(void *) = (void (*)(void *))(0x15e46uL / 2);
        flush((void *)(uintptr_t)0x1693);
        ATOMIC_BLOCK(ATOMIC_RESTORESTATE) {
            owner = BUS_IDLE;
            EIMSK |= restore_int2;
        }
    }
}

void zym_spi_request_display(void)
{
    display_pending = 1;
    zym_spi_flush_pending();
}
