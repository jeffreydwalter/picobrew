/* Radio boundary for the physical-dial scheduling test. Each operation has
 * an explicit input from the simulated board. No chip registers are emulated
 * here; the separate cc3000_board test runs the real SPI peripheral code. */
#include "scheduler.h"

struct zym_cc3000 candidate_radio;
struct zym_cc3000_broker candidate_broker;
volatile uint8_t gate_bind, gate_loan, gate_fault, radio_polls;

bool zym_cc3000_board_begin(struct zym_cc3000_broker *b, struct zym_cc3000 *r, uint16_t port)
{
    (void)b; (void)r; (void)port;
    return gate_bind;
}
bool zym_spi_resume_from_stock(struct zym_cc3000_broker *b) { (void)b; return true; }
bool zym_spi_yield_to_stock(struct zym_cc3000_broker *b) { (void)b; return gate_loan; }
void zym_spi_stock_restart(struct zym_cc3000_broker *b) { (void)b; }
void zym_broker_link_lost(struct zym_cc3000_broker *b) { b->owner = ZYM_RADIO_LOST; }
void zym_spi_close_if_owned(void) {}
void zym_spi_request_display(void) {}

void zym_cc3000_board_poll(struct zym_cc3000_broker *b, struct zym_cc3000 *r, ZymaticNetwork *http,
                           const uint8_t key[32], const char *uid, uint32_t now, uint8_t mode)
{
    (void)r; (void)http; (void)key; (void)uid; (void)now; (void)mode;
    ++radio_polls;
    if (gate_fault) b->owner = ZYM_RADIO_LOST;
}
