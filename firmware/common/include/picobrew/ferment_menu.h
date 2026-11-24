#ifndef PICOBREW_FERMENT_MENU_H
#define PICOBREW_FERMENT_MENU_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

enum { PICO_FERM_ROWS = 4 };
typedef enum PicoFermentResult {
    PICO_FERM_WAIT, PICO_FERM_LIST, PICO_FERM_LINKED, PICO_FERM_UNLINKED,
    PICO_FERM_STALE, PICO_FERM_ERROR
} PicoFermentResult;
typedef struct PicoFermentEntry {
    char name[17];
    uint8_t linked;
} PicoFermentEntry;
typedef struct PicoFermentMenu {
    uint32_t request, list_request;
    uint16_t page;
    uint8_t count, more, slot, result;
    PicoFermentEntry entries[PICO_FERM_ROWS];
} PicoFermentMenu;

/* Each user action gets a fresh nonzero identifier. A selection keeps the
 * identifier of the list the operator actually saw. Only the Hub maps slots
 * to registered accessories and commits their associations. */
void pico_ferment_list(PicoFermentMenu *menu, uint32_t request, uint16_t page);
bool pico_ferment_choose(PicoFermentMenu *menu, uint32_t request, uint8_t slot);
/* Caller enforces authentication and GET/POST. Pass NULL for a closed menu.
 * Request and response may share the same arena; output is NUL-terminated. */
unsigned pico_ferment_http(PicoFermentMenu *menu, bool post, const char *body,
                           size_t length, char *arena, size_t capacity);

#ifdef __cplusplus
}
#endif
#endif
