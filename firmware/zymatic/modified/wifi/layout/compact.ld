OUTPUT_FORMAT("elf32-avr")
OUTPUT_ARCH(avr:51)
ENTRY(zymatic_setup)
MEMORY {
  low (rx) : ORIGIN = 0x10b6, LENGTH = 0xbc0
  crc (rx) : ORIGIN = 0x2178, LENGTH = 0xfe
  encoder (rx) : ORIGIN = 0x2f72, LENGTH = 0x2b6
  wifi_fields (rx) : ORIGIN = 0x352c, LENGTH = 0xf6
  wired_fields (rx) : ORIGIN = 0xccc2, LENGTH = 0x276
  oled_copy (rx) : ORIGIN = 0x16016, LENGTH = 0x38
  oled_zero (rx) : ORIGIN = 0x160ea, LENGTH = 0x154
  keyboard1 (rx) : ORIGIN = 0xa802, LENGTH = 0xcc
  keyboard2 (rx) : ORIGIN = 0xa90e, LENGTH = 0x142
  display (rx) : ORIGIN = 0x16240, LENGTH = 0x672
  app (rx) : ORIGIN = 0x1b920, LENGTH = 0x26e0
  globals (rw) : ORIGIN = 0x802c8a, LENGTH = 0x300
}
SECTIONS {
  .oled_init : {
    __oled_init_start = .;
    KEEP(wifi_cc3000_board.o(.text .text.*))
    KEEP(wifi_cc3000_spi_bus.o(.text .text.*))
    KEEP(wifi_cc3000_spi.o(.text.zym_spi_close_if_owned .text.zym_spi_stock_selected))
    KEEP(wifi_cc3000_spi.o(.text.zym_spi_bind))
    *(.text.zymatic_home_menu)
    KEEP(wifi_server.o(.text .text.*))
  } > display
  .net_encoder : {
    __net_encoder_start = .;
    KEEP(wifi_cc3000_spi.o(.text.bind_from .text.zym_spi_receive))
  } > encoder
  .net_wifi_fields : {
    __net_wifi_fields_start = .;
    KEEP(wifi_cc3000_spi.o(.text.zym_spi_yield_to_stock .text.zym_spi_resume_from_stock .text.byte_io .text.zym_spi_record_irq))
  } > wifi_fields
  .net_reader : {
    __net_reader_start = .;
    KEEP(wifi_cc3000_spi.o(.text.zym_spi_submit .text.owns_radio* .text.finish))
  } > wired_fields
  .crc_hole : {
    KEEP(*(.crc_hole))
    KEEP(wifi_cc3000_broker.o(.text.zym_broker_begin .text.zym_broker_release_oem .text.zym_broker_link_lost))
  } > crc
  .oled_transfer : {
    __oled_transfer_start = .;
    *(.text.zym_wifi_about_info)
  } > oled_copy
  .oled_clear : {
    __oled_clear_start = .;
    KEEP(wifi_cc3000_broker.o(.text.zym_broker_poll))
  } > oled_zero
  .key_one : {
    __key_one_start = .;
    *(.text.reg_io)
  } > keyboard1
  .key_two : {
    __key_two_start = .;
    *(.text.zym_wifi_home_choice)
  } > keyboard2
  .low : {
    KEEP(*(.low_helpers))
    KEEP(wifi_cc3000_broker.o(.text.submit .text.zym_broker_acquire_oem))
    KEEP(wifi_cc3000.o(.text .text.*))
    KEEP(wifi_http_session.o(.text .text.*))
    KEEP(wifi_cc3000_spi.o(.text.within_budget .text.quarantine))
  } > low
  .text : {
    KEEP(wifi_cc3000_broker.o(.text .text.*))
    KEEP(wifi_cc3000_spi.o(.text .text.*))
    *(.text .text.*)
    *(.progmem .progmem.* .progmem1.data*)
    KEEP(*(.provision))
    . = ALIGN(2);
    KEEP(*(.image_crc))
  } > app
  .bss (NOLOAD) : {
    *(.bss .bss.* COMMON)
    __extension_bss_end = .;
  } > globals
  /* Constants in ordinary C address space would be treated as SRAM addresses;
     both regions require explicit placement and far flash reads on this MCU. */
  .unexpected : { *(.data .data.* .rodata .rodata.* .init* .fini* .eeprom*) } > globals
  ASSERT(SIZEOF(.unexpected) == 0, "Uninitialized AVR data or near-flash constants")
  /DISCARD/ : { *(.comment .note.*) }
}
oem_setup = 0x11168;
oem_serial_events = 0x1a1c6;
oem_millis = 0x19d94;
oem_button_event = 0x1122c;
