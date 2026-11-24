#include <stdint.h>

/* The stock SRAM key table stores row, column and label in 6 bytes per key.
 * Keep the stock runtime coordinates, including the five wider physical keys. */
extern void outline(uint16_t x, uint16_t y, uint16_t w, uint16_t h, uint16_t color);
extern void fill(uint16_t x, uint16_t y, uint16_t w, uint16_t h, uint16_t color);

void key_box(uint8_t key, uint8_t color, uint8_t filled)
{
    const volatile uint16_t *coords = (const volatile uint16_t *)(0x8a8u + 6u * key);
    uint16_t x = 6u * coords[1] - 2u;
    uint16_t y = 8u * coords[0];
    uint8_t width = 9;

    if (key >= 37 && key <= 41) {
        --x;
        if (key == 37 || key == 41)
            width = 31;
        else if (key == 39)
            width = 23;
        else
            width = 22;
    }
    if (filled)
        fill(x, y, width, 9, color);
    else
        outline(x, y, width, 9, color);
}
