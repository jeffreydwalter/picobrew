# Standalone firmware packages for Pico Z, PicoStill and PicoFerm

The public release for all three targets is **1.0.0**. The [firmware catalog](../../releases.json) pins each ZIP by SHA-256 and records its model, protocol, build source revision and application digest. For PicoFerm, the catalog's application digest identifies bank A. The package manifest identifies both banks.

| Target | Release package | Wireless update on a USB-provisioned device |
| --- | --- | --- |
| One qualified Pico Z module | [Z-series 1.0.0](../../z-series/modified/releases/z-series/1.0.0.zip) | Factory recovery writes and verifies the application. The module retains its filesystem between wireless updates. |
| PicoStill | [PicoStill 1.0.0](../../picostill/modified/releases/picostill/1.0.0.zip) | Factory recovery writes and verifies the application, which includes the device website. |
| PicoFerm | [PicoFerm 1.0.0](../../picoferm/modified/releases/picoferm/1.0.0.zip) | The permanent loader selects between two application banks. A trial must be confirmed or the next boot returns to the previous bank. |

For initial conversion, use **Brewery → Add machine → USB setup and recovery**. The Hub verifies the device and its existing flash layout, saves a private backup and prepares the model-specific installation. A Z1–Z4 stack label alone cannot identify a compatible module. Physical installation and power-loss qualification are pending for these three targets. [USB requirements by model](../../../server/docs/firmware-packages.md#usb-setup-backup-and-stock-restoration)

## Build a package

From the repository root, choose `z-series`, `picostill` or `picoferm`. For example, to acquire the pinned SDK and toolchain and build the Z package:

```sh
make firmware-bootstrap MODEL=z-series
make firmware-package MODEL=z-series
```

Z-series and PicoStill use [package_reference.py](../tools/package_reference.py). It takes the compiled ELF and matching build report, checks source, header and SDK hashes, then generates the component files and `manifest.json` under the model's `modified/build/package/` directory. For each published ESP32 ZIP, `manifest.json` lists component sizes, offsets and hashes. The package includes `partitions.csv` and the compiled partition table.

PicoFerm uses [build_ab.py](../../picoferm/modified/tools/build_ab.py) to link the bootstrap and both applications, then [package_ab.py](../../picoferm/modified/tools/package_ab.py) to create `firmware/picoferm/modified/build/package/picoferm.zip`. The ZIP contains the bootstrap, both bank images, a prepared filesystem and `ota-manifest.json`. The build directory retains the ELFs and link maps. [PicoFerm boot and update protocol](../../picoferm/modified/dual-bank-updates.md)

## Release layouts

Both ESP32 packages use the `esp32-factory-v1` protocol on a compatible 4 MiB layout. The SDK bootloader and partition table occupy the same addresses on both targets. USB provisions the boot selection record and preserves NVS. The following values are the byte offset and reserved size from the published partition maps.

| Region | Pico Z offset / size | PicoStill offset / size |
| --- | --- | --- |
| NVS | `0x9000 / 0x5000` | `0x9000 / 0x5000` |
| Boot selection | `0xe000 / 0x2000` | `0xe000 / 0x2000` |
| Factory recovery | `0x10000 / 0xe0000` | `0x10000 / 0xf0000` |
| Application | `0xf0000 / 0x1a1000` | `0x100000 / 0xf0000` |
| SPIFFS | `0x291000 / 0x16f000` | `0x200000 / 0x200000` |

PicoFerm uses `esp8266-ab-v1` on a compatible 2 MiB part. The bootstrap occupies sector zero. The two application banks start at `0x3000` and `0x83000`. The existing SPIFFS region begins at `0x100000`, and the EEPROM, radio and SDK settings begin at `0x1fb000`. The package's `spiffs.bin` is an empty formatted filesystem for initial setup. The USB worker handles migration of a recognized installation, including its existing settings and history. [ESP USB setup](esp-usb.md)

## Verification

For ESP32, the package verifier checks the application checksum and digest, compares its ELF sections against the image, decodes the partition table and checks its geometry and MD5, and inspects the formatted filesystem. It verifies the model and version descriptors of both bootable images. Z-series has a separate recovery ELF. On PicoStill, the same compiled image serves as recovery when booted from the factory partition. [ESP32 package checks](../tools/package_reference.py)

For PicoFerm, the builder checks the bootstrap and bank-specific ELF addresses and compares their sections with each image. The reader checks the target, version, bank offsets, image digests and bootstrap digest recorded in the A/B manifest and checks the blank SPIFFS image. [PicoFerm package checks](../../picoferm/modified/tools/usb_ab.py)

## Earlier single-application builds

The [reference-package validation report](../analysis/reference-package-validation.json) records an earlier generation of the three targets. Those ESP32 builds placed one factory application at `0x10000`. The earlier PicoFerm used eboot followed by a single application at `0x1000`. For a historical ELF and matching build report, `package_reference.py --model MODEL --elf ... --build-report ...` selects that layout from the report and checks its source inputs. The [validation script](../tools/validate_reference_packages.py) reproduced each archived build's packaging twice and tested rejection of corruption, altered geometry and stale inputs.

USB migration can recognize particular older applications by their [recorded lengths and hashes](../dependencies/legacy-usb-applications.json). The release ZIPs and their current checksums are listed in the [firmware catalog](../../releases.json).
