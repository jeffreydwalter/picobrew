#include "../include/picobrew/ferment_menu.h"
#include <stdio.h>
#include <string.h>

void pico_ferment_list(PicoFermentMenu *menu, uint32_t request, uint16_t page)
{
    memset(menu, 0, sizeof *menu);
    menu->request = request;
    menu->page = page;
}

bool pico_ferment_choose(PicoFermentMenu *menu, uint32_t request, uint8_t slot)
{
    if (!request || menu->result != PICO_FERM_LIST || slot >= menu->count) return false;
    menu->list_request = menu->request;
    menu->request = request;
    menu->slot = slot;
    menu->result = PICO_FERM_WAIT;
    return true;
}

typedef struct FermJson { const char *p, *end; } FermJson;

static void ferm_space(FermJson *j)
{
    while (j->p != j->end && (*j->p == ' ' || *j->p == '\t' || *j->p == '\r' || *j->p == '\n')) ++j->p;
}

static bool ferm_token(FermJson *j, char value)
{
    ferm_space(j);
    if (j->p == j->end || *j->p != value) return false;
    ++j->p;
    return true;
}

static int ferm_hex(char c)
{
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

/* Device font and on-wire names are limited to printable ASCII. Go's JSON
 * encoder escapes even printable HTML characters, e.g. '&' as "\u0026". */
static bool ferm_string(FermJson *j, char *out, size_t capacity)
{
    if (!ferm_token(j, '"')) return false;
    while (j->p != j->end) {
        unsigned char c = (unsigned char)*j->p++;
        if (c == '"') { *out = 0; return true; }
        if (c == '\\') {
            if (j->p == j->end) return false;
            c = (unsigned char)*j->p++;
            if (c == 'u') {
                if (j->end - j->p < 4 || j->p[0] != '0' || j->p[1] != '0') return false;
                int hi = ferm_hex(j->p[2]), lo = ferm_hex(j->p[3]);
                if (hi < 0 || lo < 0) return false;
                c = (unsigned char)(hi * 16 + lo);
                j->p += 4;
            } else if (c != '\\' && c != '/' && c != '"') return false;
        }
        if (c < 32 || c > 126 || capacity <= 1) return false;
        *out++ = (char)c;
        --capacity;
    }
    return false;
}

static bool ferm_number(FermJson *j, uint32_t limit, uint32_t *out)
{
    ferm_space(j);
    if (j->p == j->end || *j->p < '0' || *j->p > '9') return false;
    uint32_t n = (unsigned)(*j->p++ - '0');
    if (n > limit) return false;
    if (n) while (j->p < j->end && *j->p >= '0' && *j->p <= '9') {
        unsigned digit = (unsigned)(*j->p++ - '0');
        if (digit > limit || n > (limit - digit) / 10) return false;
        n = n * 10 + digit;
    }
    *out = n;
    return true;
}

static bool ferm_entries(FermJson *j, PicoFermentMenu *reply)
{
    if (!ferm_token(j, '[')) return false;
    if (ferm_token(j, ']')) return true;
    do {
        if (reply->count == PICO_FERM_ROWS || !ferm_token(j, '{')) return false;
        PicoFermentEntry *entry = &reply->entries[reply->count++];
        unsigned seen = 0;
        do {
            char key[8];
            if (!ferm_string(j, key, sizeof key) || !ferm_token(j, ':')) return false;
            if (!strcmp(key, "name") && !(seen & 1)) {
                if (!ferm_string(j, entry->name, sizeof entry->name) || !entry->name[0]) return false;
                seen |= 1;
            } else if (!strcmp(key, "linked") && !(seen & 2)) {
                uint32_t linked;
                if (!ferm_number(j, 1, &linked)) return false;
                entry->linked = (uint8_t)linked;
                seen |= 2;
            } else return false;
        } while (ferm_token(j, ','));
        if (seen != 3 || !ferm_token(j, '}')) return false;
    } while (ferm_token(j, ','));
    return ferm_token(j, ']');
}

static bool ferm_reply(const char *body, size_t length, PicoFermentMenu *reply)
{
    static const char *const results[] = {"", "list", "linked", "unlinked", "stale", "error"};
    FermJson j = {body, body + length};
    unsigned seen = 0;
    if (!ferm_token(&j, '{')) return false;
    do {
        char key[8];
        uint32_t n;
        unsigned field;
        if (!ferm_string(&j, key, sizeof key) || !ferm_token(&j, ':')) return false;
        if (!strcmp(key, "request")) {
            field = 1;
            if (!ferm_number(&j, UINT32_MAX, &reply->request) || !reply->request) return false;
        } else if (!strcmp(key, "result")) {
            char result[10];
            field = 2;
            if (!ferm_string(&j, result, sizeof result)) return false;
            for (n = 1; n <= PICO_FERM_ERROR && strcmp(result, results[n]); ++n) {}
            if (n > PICO_FERM_ERROR) return false;
            reply->result = (uint8_t)n;
        } else if (!strcmp(key, "page")) {
            field = 4;
            if (!ferm_number(&j, UINT16_MAX, &n)) return false;
            reply->page = (uint16_t)n;
        } else if (!strcmp(key, "more")) {
            field = 8;
            if (!ferm_number(&j, 1, &n)) return false;
            reply->more = (uint8_t)n;
        } else if (!strcmp(key, "entries")) {
            field = 16;
            if (!ferm_entries(&j, reply)) return false;
        } else return false;
        if (seen & field) return false;
        seen |= field;
    } while (ferm_token(&j, ','));
    if (!ferm_token(&j, '}')) return false;
    ferm_space(&j);
    if (j.p != j.end || (seen & 3) != 3) return false;
    if (reply->result == PICO_FERM_LIST)
        return (seen & 20) == 20 && (!reply->more || (reply->count == PICO_FERM_ROWS && reply->page < UINT16_MAX));
    return !reply->more && !reply->count;
}

unsigned pico_ferment_http(PicoFermentMenu *menu, bool post, const char *body, size_t length,
                           char *arena, size_t capacity)
{
    int count;
    unsigned status = 200;
    if (!capacity) return 500;
    if (post) {
        PicoFermentMenu reply = {0};
        if (!body || !ferm_reply(body, length, &reply)) status = 400;
        else if (!menu || reply.request != menu->request) status = 409;
        else if (menu->result != PICO_FERM_WAIT) {
            /* Only the first Hub may associate this list request with its own
             * registrations. Identical labels on two Hubs can name different
             * monitors. A final mutation acknowledgement is safe to repeat. */
            if (reply.result == PICO_FERM_LIST || reply.result != menu->result) status = 409;
        } else if (reply.result == PICO_FERM_LIST && (menu->list_request || reply.page != menu->page)) status = 409;
        else if ((reply.result == PICO_FERM_LINKED || reply.result == PICO_FERM_UNLINKED) &&
                 (!menu->list_request || (reply.result == PICO_FERM_UNLINKED) != !!menu->entries[menu->slot].linked)) status = 409;
        else if (reply.result == PICO_FERM_LIST) *menu = reply;
        else menu->result = reply.result;
        count = snprintf(arena, capacity, "%s", status == 400 ? "{\"error\":\"invalid_reply\"}" :
                         status == 409 ? "{\"error\":\"stale_request\"}" : "{}");
    } else if (!menu || !menu->request || menu->result != PICO_FERM_WAIT) {
        count = snprintf(arena, capacity, "{\"request\":0}");
    } else if (!menu->list_request) {
        count = snprintf(arena, capacity, "{\"request\":%lu,\"action\":\"list\",\"page\":%u}",
                         (unsigned long)menu->request, (unsigned)menu->page);
    } else {
        count = snprintf(arena, capacity,
                         "{\"request\":%lu,\"action\":\"%s\",\"page\":%u,\"slot\":%u,\"list_request\":%lu}",
                         (unsigned long)menu->request, menu->entries[menu->slot].linked ? "unlink" : "link",
                         (unsigned)menu->page, (unsigned)menu->slot, (unsigned long)menu->list_request);
    }
    return count >= 0 && (size_t)count < capacity ? status : 500;
}
