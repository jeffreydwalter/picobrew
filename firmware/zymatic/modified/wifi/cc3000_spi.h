#ifndef ZYMATIC_CC3000_SPI_H
#define ZYMATIC_CC3000_SPI_H
#include "cc3000_broker.h"

/* Bench transport for a fully initialized 1.1.14 radio. A future board
 * integration must route OEM operations through the same broker first. */
enum { ZYM_STATION_DROPPED = 1, ZYM_DHCP_FAILED = 2 };
extern volatile uint8_t zym_spi_station_events;
bool zym_spi_bind(void);
void zym_spi_record_irq(void);
enum zym_cc3000_submit_result zym_spi_submit(void *unused, const uint8_t *frame, uint8_t size);

#define ZYM_SPI_MAX_HCI 125
enum zym_spi_rx { ZYM_SPI_NO_FRAME, ZYM_SPI_FRAME, ZYM_SPI_LINK_LOST };
/* One frame borrowed from the original 131-byte stock SPI receive buffer.
 * Release it after the broker or OEM consumer finishes with its bytes. */
enum zym_spi_rx zym_spi_receive(uint8_t **frame, uint8_t *size);
void zym_spi_release_frame(void);
void zym_spi_close_if_owned(void);

/* At a retryable foreground entry to an OEM operation, take its entire HCI
 * lease before allowing its synchronous code to run. Return the radio only
 * after all of its command/data waits finish. A radio restart during the
 * loan invalidates the old listener and keeps the OEM in charge. */
bool zym_spi_yield_to_stock(struct zym_cc3000_broker *broker);
bool zym_spi_resume_from_stock(struct zym_cc3000_broker *broker);
void zym_spi_stock_restart(struct zym_cc3000_broker *broker);
#endif
