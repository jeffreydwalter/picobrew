/* Differential execution at a proposed AVR epilogue entry. The call stack
 * contains a known return address and distinct values in the saved registers. */
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sim_avr.h>

static avr_t *load(const char *filename)
{
    static uint8_t flash[0x20000];
    FILE *f = fopen(filename, "rb");
    assert(f);
    size_t length = fread(flash, 1, sizeof flash, f);
    assert(length && fgetc(f) == EOF);
    fclose(f);
    avr_t *cpu = avr_make_mcu_by_name("atmega1284p");
    assert(cpu && avr_init(cpu) == 0);
    cpu->frequency = 16000000;
    avr_loadcode(cpu, flash, length, 0);
    return cpu;
}

static unsigned run(avr_t *cpu, unsigned start, unsigned pops, uint8_t pattern)
{
    unsigned sp = 0x3000 + pattern;
    cpu->pc = start;
    for (unsigned i = 0; i < 32; ++i) cpu->data[i] = (uint8_t)(29*i + pattern);
    for (unsigned i = 1; i <= pops; ++i) cpu->data[sp + i] = (uint8_t)(17*i + 19 + pattern);
    cpu->data[sp + pops + 1] = cpu->data[sp + pops + 2] = 0;
    cpu->data[0x5d] = (uint8_t)sp;
    cpu->data[0x5e] = (uint8_t)(sp >> 8);
    for (unsigned i = 0; i < 8; ++i) cpu->sreg[i] = (pattern >> i) & 1;
    uint64_t began = cpu->cycle;
    while (cpu->pc && cpu->cycle - began < 100 && cpu->state != cpu_Crashed) avr_run(cpu);
    assert(cpu->pc == 0 && cpu->state != cpu_Crashed);
    unsigned top = cpu->data[0x5d] | ((unsigned)cpu->data[0x5e] << 8);
    assert(top == sp + pops + 2);
    return (unsigned)(cpu->cycle - began);
}

int main(int argc, char **argv)
{
    assert(argc == 4);
    avr_t *original = load(argv[1]), *packed = load(argv[2]);
    FILE *f = fopen(argv[3], "r");
    assert(f);
    unsigned start, end, cases = 0;
    while (fscanf(f, "%x %x", &start, &end) == 2) {
        unsigned pops = (end - start)/2 - 1;
        for (unsigned pattern = 0; pattern <= 255; pattern += 17) {
            unsigned old_cycles = run(original, start, pops, pattern);
            unsigned new_cycles = run(packed, start, pops, pattern);
            assert(!memcmp(original->data, packed->data, 32));
            assert(!memcmp(original->sreg, packed->sreg, 8));
            assert(new_cycles >= old_cycles + 2 && new_cycles <= old_cycles + 3);
            ++cases;
        }
    }
    assert(cases && feof(f));
    fclose(f);
    avr_terminate(original);
    avr_terminate(packed);
    printf("%u entry-state comparisons passed with two or three extra cycles per merged return.\n", cases);
    return 0;
}
