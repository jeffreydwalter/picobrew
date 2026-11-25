# Zymatic Wi-Fi memory checks

The bench builds a compact and an extended Zymatic application from the verified OEM 1.1.14 image. It links the Wi-Fi listener, checks flash and SRAM limits, and runs the composed firmware on a simulated AVR. CI also compares each layout byte for byte with the credential-free installer delta in `extension/wifi/`. The Hub’s USB worker uses those same deltas to provision each brewer from verified OEM 1.1.14 without installing a compiler.

From the Zymatic `modified` directory, run the same check as CI. It uses the project's pinned Arduino AVR GCC 7.3.0-atmel3.6.1-arduino7 toolchain, simavr 1.8, a host C compiler, Python 3.10 or newer, Git history containing the pinned compact revision and the locked Node dependencies in `../reverse-engineered/tests`. The original firmware input may be the vendor ZIP or its HEX. Each build uses a temporary directory and synthetic credentials.

```sh
npm ci --ignore-scripts --prefix ../reverse-engineered/tests
make wifi-bench ARDUINO_AVR_BIN="$ARDUINO_AVR_BIN" ZYMATIC_TEST_HEX="$ZYMATIC_TEST_HEX"
```

## What fits

| Synthetic application | Free before the boot boundary | Static extension SRAM | Existing features exercised |
| --- | ---: | ---: | --- |
| Compact | 36 bytes before `0x1e000` | 689 / 768 bytes | Ethernet API, original controls, offline recipe selection, session handling, image integrity and commissioning |
| Extended | 46 bytes before `0x1fc00` | 765 / 768 bytes | Compact features, About pairing, remote dial, live OLED, session history and the device website |

Both profiles hook the CC3000 listener into the normal Home menu and a read-only recipe pause. The extended profile also serves the physical About pairing wait. The scheduler hands the radio back to the original client before entering a stock network operation. The firmware shares one HTTP request workspace between the radio and Ethernet, and defers a competing display refresh while the radio owns the SPI bus. [Tested firmware scopes and board limits](../integration/README.md)

The compact profile uses lossless OEM table packing and reuses verified AVR library routines present in the original image. It retains its 8 KiB boot boundary. The extended build reads the UID and key directly from flash, freeing 65 bytes of SRAM. Firmware selection still depends on the installed boot layout.

## Flash layout evidence

The benchmark retains the original font, images and website assets. Sharing repeated instructions in the network parser, URL encoder, HTTP writes, Ethernet address construction, OLED and keyboard drawing frees 4,550 bytes across fixed ranges. Direct register-save sequences and identical return sequences provide additional short spans. The allocation checks branch destinations, skip instructions, possible function pointers, known patch sites and instruction alignment. The simulator compares the stock stack, registers and flags at the original continuations. Indirect entries, shared-bus timing and real brewer behavior still need physical qualification.

The extended build stores the 2,956 website bytes across 246 fragments. It reads the packed assets through a 552-byte index and places selected constants directly into the upper AVR flash bank. The integrated test checks 54,104 original and packed table accesses in each profile, reads every site byte on the AVR, and fetches the HTML, JavaScript and favicon over simulated Ethernet during a brew. The existing active-brew session recorder keeps its 64,000-cycle per-poll check.

To inspect a single part of the benchmark, run either command below.

```sh
python3 wifi/layout/tail_budget.py --toolchain "$ARDUINO_AVR_BIN" --oem-file "$ZYMATIC_TEST_HEX" --simulate
python3 wifi/layout/scatter.py --toolchain "$ARDUINO_AVR_BIN" --oem-file "$ZYMATIC_TEST_HEX" --with-listener
```

## Rear USB and network recovery

PicoBrew documents selecting a Zymatic application HEX in PicoFlash and transferring it through the rear serial USB port. The pinned 1.1.14 download ends at byte address `0x1bbca`. The earliest of the controller's four possible boot regions starts at `0x1e000`. The vendor package supplies application bytes only. PicoBrew's documented procedure gives no method for replacing the serial bootloader or changing boot fuses. [PicoBrew's firmware update guide](https://picobrewcontent.blob.core.windows.net/content/zymatic/ZYMATIC_TROUBLESHOOTING.pdf#page=13) · [Original image and updater evidence](../../../oem/README.md)

The ATmega1284P can update its boot section when the executing bootloader implements the operation and its lock bits permit the write. The BOOTSZ and BOOTRST fuses determine the region and reset entry. The MCU also supports reading fuse and lock state from software. The upstream Sanguino comparison bootloader ignores STK500 universal commands and its own installation guide uses an ISP programmer. A real brewer's bootloader and fuse settings still need identification. These memory checks exercise application code. [Microchip data sheet, chapter 27](https://ww1.microchip.com/downloads/en/DeviceDoc/Atmel-42719-ATmega1284P_Datasheet.pdf#page=359) · [Sanguino serial command handling](https://github.com/Lauszus/Sanguino/blob/5c8130f99aeecb40945ffc2a68e7e8f96d194de8/bootloaders/optiboot/optiboot.c#L521-L525) · [Sanguino ATmega1284 bootloader installation](https://github.com/Lauszus/Sanguino/blob/5c8130f99aeecb40945ffc2a68e7e8f96d194de8/bootloaders/optiboot/ATmega1284.md)
