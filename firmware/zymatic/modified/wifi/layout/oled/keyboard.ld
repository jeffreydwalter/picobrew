OUTPUT_FORMAT("elf32-avr")
OUTPUT_ARCH(avr:51)
ENTRY(key_outline)
outline = 0xa74c;
fill = 0xa8ce;
SECTIONS {
  .first 0xa788 : { *(.key_outline) *(.text*) }
  .second 0xa90a : { *(.key_fill) }
  ASSERT(SIZEOF(.first) <= 0x146 && SIZEOF(.second) <= 0x146,
         "Keyboard helper exceeds original ROM span")
}
