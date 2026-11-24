#ifndef ZYMATIC_CC3000_SPI_BUS_H
#define ZYMATIC_CC3000_SPI_BUS_H
#include <stdbool.h>

/* Zymatic 1.1.14 bench interface. The foreground writer and interrupt reader
 * must both acquire before touching radio CS or SPI configuration. If the
 * IRQ cannot acquire, its caller must leave the radio IRQ pending and poll
 * its level again. Release only after deselecting and restoring SPI. */
bool zym_spi_take_radio(void);
void zym_spi_release_radio(void);

/* Replace only the two Timer 3 drawing epilogues beginning at byte addresses
 * 0x5044 and 0x8738. Their preceding framebuffer work still runs. Call
 * flush_pending from the foreground after the radio has returned the bus. */
void zym_spi_request_display(void);
void zym_spi_flush_pending(void);
bool zym_spi_stock_selected(void);
#endif
