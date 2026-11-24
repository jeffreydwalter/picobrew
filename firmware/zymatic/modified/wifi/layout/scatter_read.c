/* Invoke the linked AVR site's reader for every byte, in reverse order. */
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <sim_avr.h>

static size_t read_file(const char *name, uint8_t *buffer, size_t size)
{
    FILE *file = fopen(name, "rb");
    assert(file);
    size_t count = fread(buffer, 1, size, file);
    assert(count && fgetc(file) == EOF);
    fclose(file);
    return count;
}

int main(int argc, char **argv)
{
    assert(argc == 4);
    static uint8_t image[0x20000], asset[4096];
    size_t length = read_file(argv[1], image, sizeof image);
    size_t count = read_file(argv[2], asset, sizeof asset);
    unsigned entry = (unsigned)strtoul(argv[3], NULL, 10);
    assert(entry && entry < length);
    avr_t *cpu = avr_make_mcu_by_name("atmega1284p");
    assert(cpu && avr_init(cpu) == 0);
    avr_loadcode(cpu, image, length, 0);
    unsigned worst = 0;
    for (size_t offset = count; offset--;) {
        cpu->data[1] = 0;
        cpu->data[24] = offset;
        cpu->data[25] = offset >> 8;
        cpu->data[0x5d] = 0; cpu->data[0x5e] = 0x30;
        cpu->data[0x3001] = cpu->data[0x3002] = 0;
        cpu->pc = entry;
        uint64_t start = cpu->cycle;
        while (cpu->pc && cpu->state != cpu_Crashed && cpu->cycle - start < 2000) avr_run(cpu);
        assert(!cpu->pc && cpu->state != cpu_Crashed);
        assert(cpu->data[0x5d] == 2 && cpu->data[0x5e] == 0x30 && cpu->data[24] == asset[offset]);
        unsigned cycles = (unsigned)(cpu->cycle - start);
        if (cycles > worst) worst = cycles;
    }
    printf("All %zu compressed website bytes read back on AVR, at most %u CPU cycles per byte.\n", count, worst);
    avr_terminate(cpu);
}
