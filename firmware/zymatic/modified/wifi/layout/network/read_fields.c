#include <stdint.h>
#include <stdbool.h>
#define ENTRY(address, type) ((type)(uintptr_t)((address)/2))
typedef uint8_t (*ReadBody)(char *, uint16_t);
typedef uint16_t (*FirstField)(const char *);
typedef uint32_t (*NumberField)(const char *, void *, uint16_t);
/* Both 1.1.14 readers return six slash-terminated numeric fields. Keep the
 * original integer conversions and the transport-specific result flags. */
uint8_t parse(uint16_t *first, uint32_t *second, uint32_t *third, uint32_t *fourth,
              uint32_t *fifth, uint32_t *sixth, uint8_t *clear, uint8_t *link, bool wireless)
{
    char body[513];
    char *field = body;
    uint32_t *outputs[] = {second, third, fourth, fifth, sixth};
    char *fields[6];
    body[0] = 0;
    body[512] = 0;
    ReadBody read = wireless ? ENTRY(0x3280, ReadBody) : ENTRY(0xcbf2, ReadBody);
    if (!read(body, 512) || !body[0] || body[0] == '!') return 0;
    for (uint8_t i = 0; i < 6; ++i) {
        fields[i] = field;
        while (*field && *field != '/') ++field;
        if (*field != '/') return 0;
        *field++ = 0;
    }
    *first = ENTRY(0x2622, FirstField)(fields[0]);
    for (uint8_t i = 0; i < 5; ++i) {
        *outputs[i] = ENTRY(0x2426, NumberField)(fields[i+1], 0, 0);
    }
    *clear = 0;
    *link = wireless;
    if (wireless) *(volatile uint8_t *)(uintptr_t)0x247b = 1;
    return 1;
}
