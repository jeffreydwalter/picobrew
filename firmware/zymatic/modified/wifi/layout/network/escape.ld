OUTPUT_FORMAT("elf32-avr")
OUTPUT_ARCH(avr:51)
SECTIONS { .text 0x2ed0 : { *(.text .text.*) *(.progmem*) . = ALIGN(2); } }
