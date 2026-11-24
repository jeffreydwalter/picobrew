# Zymatic 1.0.0 extended flash layout

This build composes a 130,036-byte ATmega1284P application from the recognized PicoBrew 1.1.14 image. Its final USB page ends at byte address `0x1fc00`. A unit must have a verified boot section starting there. A unit reserving 2, 4 or 8 KiB, or one whose boot section is unknown, uses the compact 122,628-byte application. [Installation and boot evidence](recovery.md)

| Byte range, end exclusive | Original use | Extended build contents |
| --- | --- | --- |
| `0x0254–0x1c77` | 6,691 bytes of text and bitmap data | 3,682-byte lossless block dictionary, then 2,994 bytes of extension code and website starting at `0x10b6` |
| `0x2177–0x2277` | 256-byte Dallas/Maxim sensor CRC-8 table | 252 bytes of code starting at `0x2178`, including the bitwise equivalent CRC |
| `0x1b24a–0x1b920` | Beginning of the SRAM startup data | 1,730-byte encoding of all 2,432 initialized SRAM bytes followed by padding |
| `0x1b920–0x1fc00` | End of the startup data and unused application space | 17,108 bytes of AVR code, website asset, private provisioning fields and image CRC |
| `0x1fc00–0x20000` | Terminal 1 KiB | Reserved for the verified bootloader |

Addresses are flash **byte** addresses. The bootloader reads and writes 256-byte pages. The linker reserves 768 bytes for custom static state at SRAM address `0x2c8a`. The builder checks the actual linked lengths, bounds, hashes and provisioning fields before producing the private image. Exact opcodes and both before/after instruction encodings for each patch are in [the composition code](tools/zymatic_build.py). The layout, dictionary and five reused AVR library function hashes are defined in [the packer](tools/oem_tables.py) and [linker script](src/overlay.ld).

## Original controller reads

The original image contains 69 `LPM` or `ELPM` instructions in its executable range. Each of the following groups has a distinct address source.

| Stock instruction sites | Data source and handling |
| --- | --- |
| `0x2662`, `0x11cd8`, `0x1a870` | The flash string copier and both print loops receive original pointers. Their replacements look up bytes in the dictionary. Pointers outside the original dictionary range read raw flash. |
| `0x15954` | All three callers of the stock bitmap renderer supply original bitmap addresses (`0x742`, `0x752`, `0x1768`). The renderer uses the same byte lookup. Its output still goes through the original drawing routine. |
| `0x15ac0` | The stock glyph reader indexes the unmodified font beginning at `0x1c77`. |
| `0x81a2–0x81d6`, `0xb6d4–0xb760`, `0xbcee–0xbd00`, `0xc56e–0xc570`, `0xf828–0xf834`, `0x105b0–0x105b2` | These instructions fetch 16-bit entries from the original low-address pointer tables before `0x0254`. The downstream byte readers receive the original pointers. |
| `0x16298–0x162e0`, `0x1989a–0x198b6`, `0x19f16–0x19f3c`, `0x1a014–0x1a044`, `0x1a076–0x1a0ae` | The stock GPIO and pin lookup tables follow the sensor table at `0x2277` or later. |
| `0x194da` | The 1-Wire sensor routine indexes the Dallas/Maxim CRC table. Its replacement computes the same byte with the reflected polynomial `0x8c`. |
| `0x1b1ba–0x1b1bc` | The stock startup code walks the original constructor-address table. |
| `0x2326` | The stock reset initializer is replaced at `0x2316`. The replacement verifies the composed image CRC, decodes the initializer, then returns to stock startup at `0x2330`. The heap start moves above the extension globals. |
| `0x4a5c–0x4a62`, `0x7ac0–0x7ac6` | Stock factory checksum loops read the actual application bytes. The builder derives the new checksum for this private image. The recognition hooks handle the stock and custom factory stamps. |

The original SRAM positions of strings, objects and controller variables are retained. The decoder locates each 128-byte block from its offset table, then follows backward references to recover individual bytes without a persistent decompression buffer. The extension calls the original `strcpy`, unsigned 16- and 32-bit division functions, and GCC prologue/epilogue helpers. Their stock instruction sequences match the pinned compiler library byte for byte.

## Verification

The AVR simulator runs each replaced table-read sequence against both the stock and composed images for every original address in the pool, neighboring addresses and every input to the sensor CRC table. The 54,104 cases compare registers, flags, program continuation and destination memory. The stock and composed startup paths also compare initialized SRAM before constructors. In these runs the slowest single table lookup consumed 2,456 cycles at 16 MHz (about 0.16 ms). The independent stock-control regression runs 494 AVR executions. [Paired table tests](tests/simavr_tables.c) · [Cold-boot and Ethernet tests](tests/run_simavr.py)

The recognizer reverses the patches, reconstructs the stock data and verifies the recovered 1.1.14 application hash. It then recomposes the image with the recovered private fields and requires an exact byte match. Physical installation, power-loss recovery and brewing remain hardware qualification steps. [Version and hardware requirements](application.md)
