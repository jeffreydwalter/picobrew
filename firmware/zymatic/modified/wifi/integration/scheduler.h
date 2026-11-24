#ifndef ZYMATIC_WIFI_SCHEDULER_H
#define ZYMATIC_WIFI_SCHEDULER_H

#include "cc3000_board.h"

/* RAM initializes to unbound. Actual UI hooks delimit the scopes where
 * 1.1.14 cannot start a synchronous stock network request. */
void zym_wifi_home_enter(void);
uint8_t zym_wifi_home_choice(uint8_t selected, uint32_t now);
uint8_t zym_wifi_pending(void);
void zym_wifi_network_poll(ZymaticNetwork *http, const uint8_t key[32], const char *uid,
                           uint32_t now, uint8_t mode);
void zym_wifi_before_restart(void);
/* AVR-GCC packs the first bit into bit 0. stock.S uses SBRC/SBRS on
 * this same byte; binary fixtures verify both clear and set encodings. */
struct zym_wifi_flags {
    uint8_t quarantined:1, ownership:2, transport:2, inside_safe:1;
};
extern struct zym_wifi_flags zym_wifi_flags;
#define zym_wifi_quarantined (zym_wifi_flags.quarantined)
#endif
