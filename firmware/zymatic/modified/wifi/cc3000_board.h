#ifndef ZYMATIC_CC3000_BOARD_H
#define ZYMATIC_CC3000_BOARD_H

#include "server.h"
#include "cc3000_spi.h"

/* Use only after stock Wi-Fi association/DHCP and Ethernet context release. */
bool zym_cc3000_board_begin(struct zym_cc3000_broker *broker, struct zym_cc3000 *radio,
                            uint16_t port);
void zym_cc3000_board_poll(struct zym_cc3000_broker *broker, struct zym_cc3000 *radio,
                           ZymaticNetwork *http, const uint8_t key[32], const char *uid,
                           uint32_t now, uint8_t mode);
#endif
