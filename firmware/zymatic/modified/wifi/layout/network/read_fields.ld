OUTPUT_FORMAT("elf32-avr")
OUTPUT_ARCH(avr:51)
SECTIONS { .one 0x33a0 : { *(.wifi) *(.text .text.*) } .two 0xccba : { *(.ethernet) } }
