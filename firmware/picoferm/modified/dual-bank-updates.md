# PicoFerm wireless updates

PicoFerm 1.0.0 uses a permanent USB-provisioned loader and two application banks. The Hub uses the same package for initial USB setup and subsequent Wi-Fi updates. Physical installation, Wi-Fi operation and battery removal during a real update still need qualification. [Release package](releases/picoferm/1.0.0.zip)

## Flash ownership

The 2 MiB ESP8266 layout preserves the original filesystem and SDK addresses. A permanently installed loader chooses the application at reset. Each application embeds its local website.

| Flash byte range (end exclusive) | Contents |
| --- | --- |
| `0x000000–0x001000` | Permanent bootstrap |
| `0x001000–0x003000` | One independent boot record per application |
| `0x003000–0x080000` | Bank A, linked at `0x40203010` for instruction flash |
| `0x080000–0x083000` | Reserved |
| `0x083000–0x100000` | Bank B, linked at `0x40283010` |
| `0x100000–0x1fb000` | SPIFFS, including the device key, network settings and history |
| `0x1fb000–0x200000` | Existing EEPROM, radio and SDK sectors |

USB setup first requires the established two complete, matching reads, recognized stock application, chip geometry and device-specific original backup. It rejects unknown data anywhere in the area reserved for the two banks. The Hub's worker writes the initial confirmed Bank A and selector, provisions the verified filesystem, writes the boot sector last and verifies the entire flash against the intended image. An interrupted first conversion can require the ESP8266 ROM serial loader and the private backup. Conversion also recognizes a previously paired single-image PicoFerm when its installed application digest matches the release index, its device key matches the Hub, and the unused application area matches the same unit’s verified stock backup. In that case it copies all existing filesystem and SDK sectors unchanged. Unknown layouts are refused.

The bootstrap and both applications use the repository's pinned Arduino ESP8266 2.4.1 SDK and LX106 compiler. The new loader uses their ROM SPI and linker definitions. Each linker script used by the bootstrap and bank build is hash checked. The builder verifies the application identity and the bytes and addresses of every section against both real linked ELFs. [Original Arduino loader](https://github.com/esp8266/Arduino/blob/2.4.1/bootloaders/eboot/eboot.c) · [Build and ELF checks](tools/build_ab.py)

## Installing over Wi-Fi

Early 1.0.0 builds and the battery-saving 1.0.0 build have different image digests.
The updater in the early builds rejects another image with the same version before
writing the inactive bank. Owners of those builds can wait for a later numbered
release or use the USB stock recovery and setup flows with their verified original
backup. Restoring that backup also restores its original settings and filesystem.
Archive any wanted recordings in the Hub first.

1. The authenticated Hub reads `GET /api/v1/firmware/ota`, verifies the PicoFerm UID, confirmed running image and bootstrap fingerprint, and chooses the opposite bank from the package.
2. When the machine is idle, `POST /enter` reserves it for the update. The current application stays in memory and reports recovery mode. `POST /begin` validates the target identity, version, size and SHA-256. The reply includes a 16-character session ID and the next offset.
3. `POST /chunk` accepts up to 1,024 bytes of hex-encoded data per call, with four-byte-aligned offset and length. The receiver erases only the inactive bank's sectors and verifies every write by readback. Repeating an identical chunk returns the current offset. A conflicting or out-of-order chunk fails. The client can reconnect to the same running session, or start over at offset zero following a power cycle. `POST /abort` and a two-minute inactive session return the machine to normal operation.
4. `POST /finish` verifies the whole image again, including SHA-256, ESP checksum, fixed model/version and bank-specific link addresses. The receiver commits the pending record last and restarts. The loader verifies the image and marks the trial used before transferring control.
5. The Hub checks the returning UID, exact bank, version, image length and SHA-256, then calls `POST /confirm` with the UID and version. Failure to confirm within two minutes restarts into the prior confirmed bank. A power interruption during trial also returns to the prior confirmed bank. The bootstrap, the running image, the shared filesystem and SDK tail are excluded from OTA erase and program ranges. Downgrades use the same package and confirmation checks.

The current app and a trial share the filesystem. Future firmware that changes its persistent file formats needs backward-compatible storage or migration before its release can use this rollback path. The device cannot roll back independent hardware or filesystem faults.

## Reproducing the bench checks

With the existing PicoFerm build dependencies installed:

```sh
make -C firmware/picoferm/modified ab-test
python3 firmware/picoferm/modified/tools/validate_memory.py --ab \
  --toolchain firmware/common/build/dependencies/xtensa-lx106-elf \
  --arduino firmware/common/build/dependencies/esp8266-2.4.1 \
  --build-dir firmware/picoferm/modified/build/ab \
  --output firmware/picoferm/modified/build/memory-ab
```

The native NOR tests run the actual selector and HTTP receiver. They interrupt writes and erases, including trial and confirmation records, test retries and rejected packages, and transfer both real linked images. The USB test composes the first installation from a preserved OEM application, rejects occupied regions and checks deterministic archive bytes. The 32-bit memory replay reserves the SDK and network budget and exercises the real SPIFFS and allocator. The real radio, boot ROM, RAM high-water mark, USB power/reset behavior and mains or battery interruption still need bench measurements. [Fault injection](tests/test_ab_update.py) · [USB planning tests](tests/test_ab_package.py)

A release archive contains `ota-manifest.json`, `bootstrap.bin`, `bank-0.bin`, `bank-1.bin` and `spiffs.bin`. Only the inactive bank image transfers over Wi-Fi. The release entry must specify `kind: picoferm-ab`, `ota_protocol: esp8266-ab-v1`, the archive SHA-256, and Bank A's size and digest. The selector fingerprint and the canonical blank filesystem are independently pinned. [Package builder](tools/package_ab.py) · [USB validation](tools/usb_ab.py)
