#ifndef ZYMATIC_WIFI_SERVER_H
#define ZYMATIC_WIFI_SERVER_H

#include "cc3000_broker.h"
#include "http_session.h"

/* Call in the foreground while the radio owns the shared HTTP context.
 * Before handing the context to Ethernet, mark the broker's link lost and
 * poll once. This clears the client and request, including its credentials.
 * A true return releases the borrowed SPI receive frame. */
bool zymatic_wifi_server_poll(struct zym_cc3000_broker *broker, struct zym_cc3000 *radio,
                              ZymaticNetwork *http, const uint8_t key[32],
                              const char *uid, uint32_t now, uint8_t mode);
#endif
