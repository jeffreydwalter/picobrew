OUTPUT_FORMAT("elf32-avr")
OUTPUT_ARCH(avr:51)
MEMORY {
 app (rx) : ORIGIN = 0x1bc00, LENGTH = 0x4400
 globals (rw) : ORIGIN = 0x802c8a, LENGTH = 0x780
}
SECTIONS {
 .text : { *(.text .text.*) *(.progmem .progmem.* .progmem1.data*) KEEP(*(.provision)) } > app
 .bss (NOLOAD) : { *(.bss .bss.* COMMON) __extension_bss_end = .; } > globals
 /DISCARD/ : { *(.comment .note.*) }
}
oem_millis = 0x19d94;
oem_button_event = 0x1122c;
oem_serial_events = 0x1a1c6;
zymatic_read_stock_serial = 0x28d2;
