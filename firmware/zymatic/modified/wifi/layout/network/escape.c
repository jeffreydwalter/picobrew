#include <stdint.h>
/* Keys are in the same order as the factory percent strings in SRAM.
 * Firmware 1.1.14 handles a backslash using the "%7C" string. */
static const __flash char keys[] = "$&+,/:;=?@ \"<>#%{}\\^~[]`";
uint8_t *encode(const uint8_t *input, uint8_t *output)
{
    uint8_t *next = output;
    *output = 0;
    while (*input) {
        uint8_t ch = *input++;
        uint8_t index = 0;
        while (keys[index] && keys[index] != ch) ++index;
        if (keys[index]) {
            const uint8_t *escape = (const uint8_t *)(uintptr_t)(0x015a + 4 * index);
            *next++ = escape[0];
            *next++ = escape[1];
            *next++ = escape[2];
        } else {
            *next++ = ch;
        }
    }
    *next = 0;
    return output;
}
