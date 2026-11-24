#include "scheduler.h"
#include <string.h>

#ifdef ZYM_SCHEDULER_TEST
extern uint8_t test_wired, test_wifi, test_clock;
#define CLOCK_RUNNING test_clock
#define STOCK_WIRED test_wired
#define STOCK_WIFI test_wifi
#else
#include <avr/io.h>
#define CLOCK_RUNNING ((SREG & _BV(SREG_I)) && (TIMSK0 & _BV(TOIE0)) && (TCCR0B & 7))
#define STOCK_WIRED (*(volatile uint8_t *)(uintptr_t)0x230b)
#define STOCK_WIFI  (*(volatile uint8_t *)(uintptr_t)0x230c)
#endif

extern void zym_wired_abandon(ZymaticNetwork *http);
extern struct zym_cc3000 candidate_radio;
extern struct zym_cc3000_broker candidate_broker;
enum { UNBOUND, SERVING, LOANED, STOCK_ONLY };
#define ownership zym_wifi_flags.ownership
#define transport zym_wifi_flags.transport
static uint8_t pending_choice;
enum { NEVER_SERVED, WIRED, WIRELESS, WIRE_LEFT };
static uint16_t selected_at;
struct zym_wifi_flags zym_wifi_flags;
_Static_assert(sizeof(zym_wifi_flags) == 1, "Firmware flags must occupy one byte");

static void offline(void)
{
    zym_broker_link_lost(&candidate_broker);
    zym_spi_close_if_owned();
    /* 1.1.14's successful Wi-Fi setup flag. A lost radio cannot service
     * application requests. Keep the allocated stock client intact. */
    STOCK_WIFI = 0;
    zym_wifi_quarantined = 1;
}

void zym_wifi_home_enter(void)
{
    zym_wifi_flags.inside_safe = 1;
    if (zym_wifi_quarantined || STOCK_WIRED || !STOCK_WIFI) return;
    if (ownership == UNBOUND || ownership == STOCK_ONLY) {
        if (zym_cc3000_board_begin(&candidate_broker, &candidate_radio, 80))
            ownership = SERVING;
    } else if (ownership == LOANED && zym_spi_resume_from_stock(&candidate_broker)) {
        ownership = SERVING;
    }
}

uint8_t zym_wifi_pending(void) { return pending_choice; }

uint8_t zym_wifi_home_choice(uint8_t selected, uint32_t now)
{
    if (!pending_choice && selected >= 6) return 0xff;
    if (ownership == SERVING && !zym_wifi_quarantined) {
        if (zym_spi_yield_to_stock(&candidate_broker)) {
            ownership = LOANED;
        } else {
            if (!pending_choice) {
                pending_choice = selected + 1;
                selected_at = (uint16_t)now;
            }
            if (candidate_broker.owner != ZYM_RADIO_LOST && CLOCK_RUNNING &&
                (uint16_t)(now - selected_at) < 3000) {
                /* Retain priority even after deferring to an unsolicited IRQ. */
                candidate_broker.oem_queued = true;
                return 0xff;
            }
            offline();
        }
    }
    if (pending_choice) selected = pending_choice - 1;
    pending_choice = 0;
    zym_wifi_flags.inside_safe = 0;
    return selected;
}

void zym_wifi_before_restart(void)
{
    if (ownership == LOANED) {
        zym_spi_stock_restart(&candidate_broker);
        ownership = STOCK_ONLY;
    }
}

void zym_wifi_network_poll(ZymaticNetwork *http, const uint8_t key[32], const char *uid,
                           uint32_t now, uint8_t mode)
{
    /* An OEM IRQ can make a first bind/resume temporarily unavailable.
     * Retry at the existing foreground cadence while this same menu is open. */
    if (zym_wifi_flags.inside_safe && ownership != SERVING) zym_wifi_home_enter();
    if (ownership == SERVING) {
        if (transport != WIRELESS) {
            memset(http, 0, sizeof(*http));
            transport = WIRELESS;
        }
        if (!zym_wifi_quarantined) {
            zym_cc3000_board_poll(&candidate_broker, &candidate_radio, http, key, uid,
                                  now, pending_choice ? ZYMATIC_OUTER : mode);
            if (candidate_broker.owner == ZYM_RADIO_LOST) offline();
        }
        return;
    }
    /* No Ethernet register access on Wi-Fi or after a radio failure. A stock
     * loan also keeps the HTTP context intact across an original sync. */
    if (STOCK_WIRED == 1 && !zym_wifi_quarantined) {
        if (ownership == LOANED) zym_wifi_before_restart();
        if (transport != NEVER_SERVED && transport != WIRED) zym_wired_abandon(http);
        transport = WIRED;
        zymatic_network_poll(http, key, uid, now, mode);
    } else if (transport == WIRED) {
        transport = WIRE_LEFT;
    }
}
