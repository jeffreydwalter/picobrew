# Zymatic 1.0.0 compact build

The front panel runs PicoBrew’s original Zymatic 1.1.14 brew controller. The extension adds a configurable recipe server, a local recipe for offline use, an Ethernet status page and a read-only API for the Hub. Choose a recipe and operate the brewer with its physical dial. The display shows **1.0.0** at startup and in **About Zymatic**. [OEM reconstruction](../reverse-engineered/README.md) · [Offline recipe and control flow](offline-recipe.md)

## Connections

Supply the Hub’s hostname or IPv4 address when building or installing, for example `brewery.lan:8080`. Both original network clients use the destination for outbound PicoBrew recipe and session requests. The DNS client receives the hostname alone. The HTTP client receives the configured port and full host header. The default is `picobrew.com` on port 80. Update the provisioned destination over USB if the Hub moves.

When Ethernet is active, open the brewer’s own IP address in a browser and enter its device key to view status. A paired Hub can read temperatures, uptime and the real front panel. Wired program execution exposes a running state when the stock interpreter reaches its physical button check. The API defers full display capture during that time. Original network calls, recovery, and some blocking screens can delay monitoring. Wi-Fi supports the outbound recipe and session protocol. Incoming HTTP is implemented for the wired W5100. Firmware updates use USB. [Network evidence and routes](../reverse-engineered/network/README.md)

## Build a private image

The standard USB installer composes the application locally. The checked-in extension contains empty provisioning fields. The completed image contains the owner’s private device key, UID, Hub destination and selected offline recipe. Keep the resulting image and HEX in private storage. To build directly from repository root, supply an existing private key file with exactly 64 hexadecimal digits and the matching 32-character lowercase hexadecimal Hub UID.

```sh
python3 firmware/zymatic/modified/tools/build.py \
  --oem-file /path/to/Zymatic_1_1_14.zip \
  --key-file /private-directory/api.key \
  --uid "$ZYMATIC_UID" --hub-host brewery.lan:8080 \
  --out-dir /private-directory/zymatic-build
```

The output directory contains the address-aware Intel HEX, the contiguous application bytes and a manifest. Add `--offline-program /path/to/recipe.json` to select a native Zymatic program. The builder validates the program, exact OEM application SHA-256, every modified instruction, compiler template and final image bounds. [Original file identity and provenance](../oem/README.md) · [USB setup and restoration](README.md)

## Image layout and integrity

All addresses in the table are byte addresses, with hexadecimal bounds. The ATmega1284P provides 128 KiB of flash and 16 KiB of SRAM. The supported USB operation targets the AVR application. The CC3000 has its own firmware. [Microchip ATmega1284P documentation](https://www.microchip.com/en-us/product/atmega1284p)

| Region | Contents |
| --- | --- |
| Flash `[0x00000, 0x1bbca)` | The 113,610-byte OEM release with the reviewed instruction and pointer changes listed in the manifest. |
| Flash `[0x1bbca, 0x1bc00)` | 54-byte gap to the next 256-byte page. Installation requires this gap and the added application pages to be available. |
| Flash `[0x1bc00, 0x1e000)` | At most 9,216 bytes for the compiled extension, per-device fields, recipe and whole-image CRC. The linker rejects an overflow. Boot flash begins at or above the upper bound. |
| SRAM from `0x2c8a` | Extension globals. Both the original startup clear boundary and initial heap pointer move to their true end. The socket uses fixed memory. |

After reset, the extension recomputes the CRC-32 of the entire composed program through the final provisioning byte before calling the original setup. Corruption stops the CPU at this point. The stock diagnostic also computes its low-flash checksum and reads the board’s EEPROM stamp. The custom comparison requires the actual low-flash checksum to match the image and the board stamp to identify the pinned stock base release. Boot uses the existing watchdog while checking the image.

The original EEPROM release-migration constants stay at 1.1.14. Choosing Factory Reset on the front panel runs PicoBrew’s original commissioning routine, which clears all 4 KiB of EEPROM. During its checksum write the extension requires the live sum to match the composed application before recording the 1.1.14 base-release checksum. This preserves the EEPROM format that the original firmware checks on return to stock. Initial installation on a commissioned 1.1.14 brewer does not require a factory reset. The USB helper preserves EEPROM.

## Rebuild and check the extension

The installer composes each private application from the verified OEM bytes and an unprovisioned overlay. The compact overlay is built from the original compact source revision with the discovery version set to 1.0.0 and the current compressed status page. The firmware also updates the startup and About displays. CI checks the complete compiled overlay and symbol manifest against the files shipped in the USB worker. [Pinned compact sources](https://github.com/jeffreydwalter/picobrew/tree/b278fe1735d6bc983c107c55120c2a67b4ac6ae3/firmware/zymatic/modified)

For a developer rebuild, use a full Git checkout and the Arduino AVR GCC 7.3.0-atmel3.6.1-arduino7 toolchain. Set `ARDUINO_AVR_BIN` to its `bin` directory. [Arduino toolchain index and checksums](https://downloads.arduino.cc/packages/package_index.json)

```sh
python3 firmware/zymatic/modified/tools/compile_extension.py --profile compact \
  --toolchain "$ARDUINO_AVR_BIN" --out-dir /tmp/zymatic-compact
cmp firmware/zymatic/modified/extension/compact/extension.bin /tmp/zymatic-compact/extension.bin
cmp firmware/zymatic/modified/extension/compact/extension.json /tmp/zymatic-compact/extension.json
ZYMATIC_TEST_HEX=/path/to/Zymatic_1_1_14.zip python3 -m unittest discover -s firmware/zymatic/modified/tests
python3 firmware/zymatic/modified/tests/run_simavr.py --oem-file /path/to/Zymatic_1_1_14.zip
```

The last command requires a C compiler and simavr 1.8 with its headers and pkg-config metadata. The cycle checks use its clock-divider-aware SPI simulation. The [CI workflow](../../../.github/workflows/zymatic.yml) pins the source archive and shows the build commands. It boots a private synthetic image on an emulated ATmega1284P, checks the physical button path, serves HTTP over a modeled SPI Ethernet controller and tests corruption before setup. It also executes the commissioning writer, restores the original application on the same virtual MCU and tests its checksum diagnostic against the retained EEPROM. A mismatched live checksum during commissioning halts before writing the stamp. At the stock program-button check, the service returns immediately on a press, a latched fault, or masked interrupts. It defers to the original program if stack headroom is low. The integration test checks the program-button CPU budget and defers OLED requests. Physical setup, network operation alongside a real brew, stack headroom under interrupts, power cycling and stock return still require hardware qualification. [simavr 1.8](https://github.com/buserror/simavr/releases/tag/v1.8)
