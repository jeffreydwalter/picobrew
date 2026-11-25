# USB service for PicoFerm, PicoStill and Z brewing modules

The Hub host can read a stock appliance, install its standalone firmware, and restore its original external SPI flash. The host needs access to the appliance's service UART and Espressif ROM download mode. The helpers have passed offline tests with actual package and OEM bytes. The service connectors, boot after installation, and physical restoration still need verification on each appliance.

| Appliance | Stock capture and restoration | Standalone installation |
| --- | --- | --- |
| PicoFerm | Identifies the full archived OEM boot image and application at flash address zero. | Requires the published 2 MiB layout and matching boot header. Checks for unidentified data before expanding the application or initializing SPIFFS. Retains the EEPROM, RF and SDK tail. |
| PicoStill | Checks the bootloader, partition table, boot selection and OEM identity of each populated application slot. | Requires compatible bootloader and flash mode, at least 4 MiB of physical flash, and room for the reference layout. Preserves the physical bootloader, NVS and OTA selection. |
| Z-series brewing module | Validates the active OEM 0.0.116 or 0.0.119 image. Each module has its own MAC and capture. | Requires the individual module running 0.0.119, the PicoStill-class ESP32 layout checks, and the Z-series package. All four stack labels (`z1` through `z4`) support per-module capture and restore. The mapping between stack labels and custom firmware is unverified. |

## On the Hub host

1. Bring the appliance to idle. Use a service connection with verified voltage, ground, serial signals and download-mode controls for the specific board. A USB charging connector may carry power only. Keep the machine's normal output controls inactive during service.
2. Install the host dependency in the Python environment used by the Hub service command. From the source checkout run `python -m pip install -r firmware/common/requirements-usb.txt`. The account needs permission to open the selected serial port and create private files in the Hub data directory. Use the same account for capture, installation and restoration.
3. Enter the chip's ROM download mode manually and choose the explicit local serial port. Example values are `/dev/cu.usbserial-…` on macOS, `/dev/ttyUSB0` on Linux, and `COM4` on Windows. Close any other serial console.
4. Open the Hub USB setup or restoration instructions. For service directly from the checkout, use the following command with the actual model, port and **absolute** Hub data path.

```
python firmware/common/tools/esp_usb.py backup --model picoferm --port /dev/ttyUSB0 --data /path/to/hub/data
```

Use `install` or `restore` in place of `backup` for the corresponding operation. The tool keeps one checked serial connection and never sends a reset command. Between operations re-enter the **ROM** download mode. A successful USB command leaves an Espressif service stub running in RAM. Put the board back in normal boot mode and power-cycle it when the command reports success. A flash readback verifies the stored bytes. The first normal boot and device behavior must be checked separately.

## What is saved

Before conversion the host reads the complete flash twice, matches the active application to the recorded OEM hash, and saves a verified copy under `stock-firmware/esp/<rom-mac>/original.zip` in the Hub data directory. This archive includes the flash bootloader, all application slots, flash-backed settings and other unit-specific bytes as they were captured. On ESP32 each populated application partition must match a preserved stock image for the same family. The complete capture preserves inactive slots and private data byte for byte. External controllers and eFuses are outside the backup and write operations. The Z controller checks the separate MSP at startup and can update it with the production payload also embedded in OEM 0.0.119.

Keep the archive and later `stock-<sha256>.zip` captures private. They can contain Wi-Fi credentials and calibration. The original is immutable. If the stock device changes before a later conversion, the host also keeps that capture. Restore uses the first original and verifies its hash, the hardware MAC, model family, chip, and physical flash ID and capacity. A damaged custom application can be restored when the chip ROM is reachable. Restoration replaces the flash-backed settings and all slots with the original capture, and removes the custom device API. The publicly archived OEM updates contain individual application images. They do not include this unit's entire flash or personal calibration and cannot replace a missing original.

Conversion checks the versioned ZIP hash and the digest, address and maximum length of each component. It replaces the application and initializes the packaged SPIFFS filesystem, and changes the ESP32 partition table when a qualified layout requires it. The retained NVS/EEPROM may hold OEM network or calibration data. Private copies of the pre-install filesystem and any displaced OEM slots remain in the full backup. Each new write uses whole changed erase sectors and the tool verifies the **entire** resulting flash before reporting success. If the first conversion is interrupted, return to ROM download mode and run `restore` for the same MAC.

For a paired PicoFerm with a recognized two-bank installation, USB Install retains the existing filesystem and calibration. The worker verifies a private full-flash capture, writes and checks the inactive bank, then commits and checks the new boot record. Repeat Install after an interruption using the same Hub, physical unit and package. [PicoFerm bank and recovery details](../../picoferm/modified/dual-bank-updates.md)

PicoStill USB setup installs a protected factory recovery image at `0x10000` and the normal application at `0x100000`. It writes the initial boot selector after checking the destination region. For an existing single-slot PicoStill, the Hub checks its saved same-unit original, the running firmware against the local release index, and the stored device key. This migration retains the live filesystem and NVS byte for byte. After reboot the Hub can send matching firmware over Wi-Fi. A factory recovery installation and interrupted-transfer trial on a physical PicoStill remain outstanding.

## Pair the installed appliance

When you install through the Hub, it provisions the device key and saves it with that appliance’s identity. After setup, choose **Add machine** in the Hub and enter the appliance’s network address. If it needs Wi-Fi credentials, use the supported network setup for that appliance.

For installation with the command-line tool, use the appliance’s physical provisioning UART at 115200 baud while idle. A valid saved OEM network may already have been imported. Use a private terminal with local echo and logging disabled and send each command as a complete line. Supply a fresh random 32-byte key as 64 lowercase hexadecimal characters. The Wi-Fi separator is **one literal tab**.

```
key <64 lowercase hexadecimal characters>
wifi <SSID><TAB><password>
status
```

The application acknowledges saved settings. After Wi-Fi association, find the machine’s LAN address on its display, serial console, or router’s client list. Add the machine to the Hub with that address and the same device key. Stock capture and restore never request that key. Provisioning and radio-specific behavior are described in the [physical console documentation](provisioning.md), [PicoFerm guide](../../picoferm/modified/README.md), [PicoStill guide](../../picostill/modified/README.md), and [Z-series guide](../../z-series/modified/README.md).

The pinned host library implements [Espressif's flash read/write and verification commands](https://docs.espressif.com/projects/esptool/en/release-v4/esp32/esptool/basic-commands.html). The service refuses encrypted flash, secure boot, unknown active stock images, unmatched hardware, or an incompatible layout.
