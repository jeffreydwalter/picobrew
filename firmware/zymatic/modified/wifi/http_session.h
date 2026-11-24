#ifndef ZYMATIC_WIFI_HTTP_SESSION_H
#define ZYMATIC_WIFI_HTTP_SESSION_H

#include "zymatic_api.h"

enum { ZYMATIC_WIFI_CHUNK = 16 };

/* One accepted radio socket owns this parser until closed or discarded.
 * The caller checks closing before processing a tick, including a tick
 * containing the last bytes of a request. now is the OEM millis counter. */
void zymatic_wifi_connect(ZymaticNetwork *net, uint32_t now);
void zymatic_wifi_receive(ZymaticNetwork *net, const uint8_t *bytes, uint8_t length,
                          const uint8_t key[32]);
uint8_t zymatic_wifi_output(ZymaticNetwork *net, uint8_t out[ZYMATIC_WIFI_CHUNK],
                            const uint8_t key[32], const char *uid,
                            uint32_t now, uint8_t program_active);
void zymatic_wifi_sent(ZymaticNetwork *net, uint8_t count);
uint8_t zymatic_wifi_reading(const ZymaticNetwork *net);
uint8_t zymatic_wifi_closing(const ZymaticNetwork *net, uint32_t now);

#endif
