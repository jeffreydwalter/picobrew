# Firmware packages in PicoBrew Hub

Open a machine's **Firmware** view to see wireless update versions for its exact model. Choose a version and click **Install**. The Hub fetches the selected archive, validates it and saves it for reuse. **Download** saves that version to your computer. You can downgrade to an earlier compatible OTA release. The running version is marked **Already installed**.

If this Hub installed an earlier 1.0.0 build on a Pico S, Pro, C, Z module or PicoStill, **Install revised build** appears for a recognized installation receipt and matching final package. Before writing, the Hub also checks the application digest on the connected device. After verification the button reads **Already installed**. An earlier PicoFerm 1.0.0 rejects a replacement with the same version at the device. For a paired PicoFerm with a recognized dual-bank installation, connect USB and choose **Install standalone firmware** using the Hub that holds its original backup and device key. The Hub saves and verifies the live flash, updates the inactive bank and preserves the previous bank, pairing and recorded history. An unrecognized layout stops the operation. [PicoFerm USB procedure](../../firmware/picoferm/modified/dual-bank-updates.md#installing-over-wi-fi) Zymatic firmware updates also use USB.

## GitHub source and version checks

**Settings → Firmware** contains the GitHub repository and optional branch, tag, or commit. The default is `jeffreydwalter/picobrew` on its default branch. A compatible source publishes `firmware/releases.json`. The Hub pins that catalog and its archives to the same Git commit. Each entry also records the build's source revision. A manual **Check for Updates** in the machine view refreshes the metadata immediately.

While physical devices are registered, the Hub checks daily. The Brewery page displays a notice when a newer release is available or when the public 1.0.0 release replaces a recognized development build. Failed checks preserve the previous catalog and retry after an hour. A successful check downloads only metadata. Device keys and registered machine identifiers stay on the Hub.

The public 1.0.0 catalog has seven wireless packages covering Pico S, Pico Pro, both Pico C displays, generic Z modules, PicoStill, and PicoFerm. The picker filters them for the selected machine. Pico C recovery checks the application's display marker, and the Z and accessory packages specify their recovery protocol. The Hub checks the connected device’s identity, protocol and flash layout before transfer. Both Zymatic 1.0.0 builds are provisioned over USB with the owner’s key. [USB setup and stock restoration](#usb-setup-backup-and-stock-restoration)

The revised builds retain version **1.0.0**. The default branch catalog identifies each package by its SHA-256. The original GitHub tag identifies the first public build.

A generic Z module and PicoStill transfer only their application over Wi-Fi. Each machine’s protected recovery and existing filesystem stay in place. PicoFerm transfers the application built for its inactive bank. After reboot the Hub reads the running image hash before confirming the new bank. A failed or unconfirmed PicoFerm trial returns to the previous bank on the next reset. Earlier single-application installations need USB setup to gain these capabilities.

To publish a new release, add its verified ZIP under `firmware/<family>/modified/releases/<model>/[display/]<version>.zip` and append an entry to `firmware/releases.json`. CI reads every archive and checks that current model-specific applications are represented.

The earlier package upload and single-revision acquisition APIs remain available for external tools. The current source layout is:

- `firmware/pico-s-pro/modified/firmware/standalone/<model>/firmware-report.json`
- The adjacent `<model>-standalone.bin` image.
- `firmware/pico-s-pro/modified/firmware/web/manifest.json` and its five gzip
  payloads.

The standalone package API accepts Pico S, Pico Pro, and both supported Pico C display variants. Reference and historical builds remain accessible in the repository.

## Upload a package

The package upload API accepts a ZIP containing exactly these
eight files, without an outer directory:

| ZIP path | Contents |
| --- | --- |
| `firmware-report.json` | The unmodified build report |
| `<model>-standalone.bin` | The raw model-specific application image |
| `web/manifest.json` | The matching browser manifest |
| `web/index.html.gz` | Machine webpage |
| `web/app.js.gz` | Machine browser application |
| `web/api-explorer.js.gz` | Machine API explorer |
| `web/history.js.gz` | Machine history interface |
| `web/style.css.gz` | Machine browser styles |

The filename of the ZIP is not used to determine its model or version. A raw
binary, legacy update envelope, ELF, or source-code archive is not a complete
package. The firmware's matching website files are required.

From a full source checkout, build and package the selected model with:

```sh
make firmware-package MODEL=pico-s
make firmware-package MODEL=pico-pro
```

To package an existing committed image without running a compiler:

```sh
python3 firmware/pico-s-pro/modified/tools/package_hub_firmware.py --model pico-s
```

The CLI also accepts `--firmware-directory`, `--web-directory`, and `--output`
for a separately built image and its matching assets. The generated ZIP is
placed under the component's ignored `build/` directory by default. Packaging
is deterministic and checks the inputs before publishing the ZIP.

## What validation means

Both upload and GitHub fetch use the same Hub validator. It checks the model,
family and standalone profile, actual binary length and SHA-256, image vectors
and memory bounds, and the exact browser manifest and payload hashes. It also
checks bounded gzip decompression, original website lengths and hashes, and
the fixed machine file destinations and allocation limits. Archives with
missing, duplicate, unexpected, unsafe, or oversized entries are rejected.

The Hub derives package metadata itself and records how it obtained the bytes.
An uploaded archive cannot claim that the Hub fetched it from GitHub. A saved
package ID is its archive SHA-256; downloading it checks that the cached bytes
still match that identity. Identical archives reuse the existing saved record
and its original acquisition provenance.

These checks establish package consistency and detect corruption. Build
reports and their hashes are not publisher signatures, independent physical
hardware identification, or evidence of a successful installation. A model
match compares declared package and machine model information.

## Installation

An idle, connected Pico S, Pico Pro, or Pico C with provisioned Hub recovery and standalone firmware advertising `hub_ota_v1` can install a matching protocol-1 package. Open the machine's **Firmware** view and choose a version and click **Install**. For Pico C, USB setup installs recovery for the specific SSD1325 or SSD1309 display. Record the independently verified display controller on the paired brewer’s Firmware page. The application, recovery, and package must all match that variant. Installation is available on a normally started Hub. The Hub verifies the brewer's identity before transfer and confirms the installed image and version after restart. The firmware view reports interrupted or unconfirmed updates and offers an explicit retry for the same package.

### Restore the original Pico S/Pro firmware over Wi-Fi

Open the machine’s **Firmware → Restore original firmware over Wi-Fi**. The Hub offers this action on an idle, paired brewer that reports `stock_restore_ota_v1` and matches the recorded original S/Pro flash profile. If an earlier development build lacks this feature, first install the current standalone release over Wi-Fi on the eligible brewer. Keep it powered on throughout the restore.

The Hub asks you to acknowledge the loss of standalone controls and wireless updates, then confirm the restore. It prefers this brewer’s saved original. If needed, it obtains and verifies the two pinned OEM images from GitHub. A temporary installer runs in a third flash slot, so it can independently write and read back both the original **0.1.34 application** and **0.0.3 recovery**. It preserves the loader, network settings and brewer identity.

After restart, check the brewer’s display. Choose **I see the original PicoBrew startup screen** to record your observation when prompted. If the final acknowledgement was interrupted, the receipt distinguishes what you saw from the last step the Hub verified. If power or Wi-Fi dropped during boot selection after both images were verified, the receipt records that you saw stock startup and that the Hub could not verify the boot selection. An acknowledged boot selection is recorded separately. The original application cannot confirm startup to the Hub. Returning to custom firmware requires USB. Other brewers can use the USB procedure below. The wireless path has been exercised against simulated storage, the original loader and compiled ARM code. Physical restore and power-interruption trials are still pending.

### USB setup, backup and stock restoration

Connect the idle machine or its USB service adapter to the computer running the Hub. Open **Add machine → USB setup and recovery**, select the detected port, physical model and operation, and follow the power or reset prompts. When restoring a paired machine, its **Firmware → Restore original firmware via USB** action also checks its registered identity before a write. The dialog reports the current stage and resumes after a browser reload.

On a successful installation, choose **Add machine** and enter the brewer’s network address. The Hub pairs its saved key with the identity verified over USB. Machine-specific Wi-Fi setup may still be needed. Keep power and the cable connected until readback completes. The Hub stores the original and the private pairing files under its persistent data directory. Keep a private backup of that directory. USB setup is available on a normal Hub with access to the port. In a Linux container, map the selected serial device into the container and allow the Hub user to access it.

The downloadable Hub includes the USB installer for its platform. The final confirmation shows the firmware selected for the connected machine. On a Zymatic, initial conversion uses the application that fits the stock boot layout. A previously converted Zymatic can use the extended application when the installer verifies the smaller boot region on that particular board. [Zymatic USB procedure](../../firmware/zymatic/modified/README.md)

| Hardware | Initial conversion | Return to stock |
| --- | --- | --- |
| Pico S and Pico Pro | The CH340 installer provisions a key, the application and website, and the separate OTA recovery. Select the physical model marked on the brewer. | Restore its original app and recovery and verify boot selection. The private backup must match the live UID and allocations. Pinned public app/recovery images are available for the individually documented S/Pro unit. |
| Pico C | Select SSD1325 or SSD1309. The original application must independently match that display. Install the key, application, website, and the matching display’s wireless recovery. | Restore the original application and recovery from this brewer’s verified backup. |
| PicoFerm | Recognized OEM application, compatible 2 MiB ESP8266 flash and boot settings. | Restore the same device’s complete original SPI flash, including calibration and settings. |
| PicoStill | Recognized active OEM application and compatible ESP32 boot and partition layout. | Restore and verify the same ESP32’s complete original SPI flash. |
| Z-series (Z1–Z4 stacks) | Service one module at a time. The generic standalone package requires independently identified stock 0.0.119 and a compatible ESP32 layout. | Back up and restore the same ESP32 on a qualifying 0.0.116 or 0.0.119 module. The separate MSP controller lies outside this capture. |
| Zymatic | Install the private 1.0.0 compact application over rear USB from verified stock 1.1.14. Configure the Hub address and optional offline recipe. | Back up readable AVR program flash and restore the verified 1.1.14 application over the rear USB port. Windows PicoFlash can also use the locally verified HEX. EEPROM, bootloader repair and CC3000 radio firmware are outside the application restore. |

A Z4 has four independent 2.5 US gallon brewing units. Each can run its own recipe while stacked or racked. On factory firmware, **Group Brewing** offers the other units the same recipe and can be disabled in Settings. With standalone 1.0.0, qualify and back up each unit separately, then add it to the Hub at its own address. You can start and monitor separate brews. Starting a session on one unit does not send the factory group invitation to the others. A stack label alone cannot identify a compatible controller. [PicoBrew Z FAQ](https://picobrew.com/about/faq_z) · [Z Series setup manual](https://www.picobrew.com/about/manual?p=z_manual.txt)

#### Command-line workflows from a firmware checkout

The browser uses the programmer bundled with the Hub. The following commands are optional developer tools for a separate USB computer with the full repository and its toolchain. Use that computer’s own data directory, then privately copy its saved originals and keys when moving to a different host.

##### Pico S, Pro and C

Prepare the Python environment and CH340 dependencies in the [macOS USB procedure](../../firmware/pico-s-pro/modified/docs/macos-usb-installation.md). For example:

```sh
./picobrew-hub usb-backup --model pico-s --data ./data --checkout ..
./picobrew-hub usb-install --model pico-s --key-file /absolute/private/path/api.key --backup /absolute/path/to/saved-original.bin --data ./data --checkout ..
./picobrew-hub usb-restore --model pico-s --data ./data --checkout ..
```

For Pico C, use `--model pico-c --display ssd1325` or `--model pico-c --display ssd1309` on each command. Initial setup can capture the original directly when you omit `--backup`. The CC3200 adapter accepts the supported 1 MiB ROM4 layout with a 48-block application slot and 16-block recovery slot. Newly captured full-flash originals are read twice and stored by model, display and device identity. Restoration uses the saved original; use `--device UID` to select a particular brewer, or `--backup PATH` to select a specific capture. Firmware, boot selection and live identity must pass independent verification before writing. Each programming session requires a manual power-on or reset. Keep the private original and matching API key. The older `usb-restore` command without a model remains specific to the historically documented S/Pro device.

#### PicoFerm, PicoStill and Z

Use a verified USB-to-serial service connection and put the *idle* appliance into its ROM download mode. Install the pinned dependencies in the checkout's root virtual environment. From the repository root, run `.venv/bin/python -m pip install -r firmware/common/requirements-usb.txt` (Windows uses `.venv\Scripts\python.exe`). If no root environment exists, create it with `python -m venv .venv`. Example commands from `server/`:

```sh
./picobrew-hub usb-backup --model picoferm --port SERIAL_PORT --data ./data --checkout ..
./picobrew-hub usb-install --model picoferm --port SERIAL_PORT --data ./data --checkout ..
./picobrew-hub usb-restore --model picoferm --port SERIAL_PORT --data ./data --checkout ..
```

Replace `SERIAL_PORT` with the actual local device path or Windows COM port. Setup saves and verifies the original before proposing a write. A restore uses the MAC- and flash-matched private original. Public OEM application updates omit boot, storage or calibration regions needed for that operation. When using the command-line tool directly, complete key and Wi-Fi setup over the device’s serial console. The Hub’s USB installer provisions and saves the device key for pairing. See the [ESP USB service procedure](../../firmware/common/docs/esp-usb.md).

##### Zymatic

Connect the idle, powered Zymatic through its rear USB port. Install the AVRDUDE and Python dependencies in the [Zymatic USB guide](../../firmware/zymatic/modified/README.md), and substitute the machine’s actual local serial port:

```sh
./picobrew-hub usb-backup --model zymatic --port SERIAL_PORT --data ./data --checkout ..
./picobrew-hub usb-install --model zymatic --port SERIAL_PORT --data ./data --checkout .. --key-file "/absolute/private/zymatic.key" --hub-host brewery.lan:8080
./picobrew-hub usb-restore --model zymatic --port SERIAL_PORT --data ./data --checkout ..
```

The backup compares two reads of the ATmega1284P program flash. A stable read can include protected or unreadable regions, and the AVR signature identifies the processor model rather than an individual machine. Stock restoration uses a verified factory application. If the installed program matches the factory 1.1.14 application byte for byte, backup also saves its verified application bytes for future offline restoration. The tool can use that copy, a saved original HEX, a vendor HEX/ZIP supplied with `--oem-file`, or the verified ZIP from the pinned support archive. Omit `--port` to prepare a local HEX for Windows PicoFlash. After restarting, confirm **1.1.14** under **About Zymatic** and disconnect the USB cable before running any program.

The [stock Zymatic adapter](stock-zymatic.md) can serve recipes to the unmodified machine. Both custom 1.0.0 profiles support authenticated monitoring over Ethernet and from the Home menu or a programmed recipe pause over Wi-Fi. The extended profile adds dial control and retained history. Select **Use in USB setup** from a Zymatic offline recipe preview, or choose an exported recipe JSON file in USB setup. The command-line installer also accepts `--offline-program /absolute/path/zymatic-program.json`. [Firmware capabilities](../../firmware/zymatic/modified/application.md)

Full end-to-end hardware qualification is pending for the expanded USB profiles. Host checks exercise the original artifacts, transfer plans, refusal paths and readback with simulated devices.

## Storage and API

The source configuration and package metadata live in the Hub database;
validated ZIPs live in its private firmware cache. Include the entire Hub data
directory in a disaster-recovery backup to preserve them. The normal workspace
ZIP for recipes, equipment, and brewing history does not include firmware
packages or source configuration. Individual packages can be downloaded from
the firmware manager.

All endpoints below use the normal owner session. Mutations also require the
same-origin CSRF header. No endpoint accepts an arbitrary download URL or
executes code from a package.

| Method | API path | Behavior |
| --- | --- | --- |
| GET / PUT | `/api/v1/firmware/settings` | Read or save `repository` and `ref` |
| GET / POST | `/api/v1/firmware/updates` | Read cached versions and notices or check the configured source now |
| POST | `/api/v1/firmware/releases/{packageID}/stage` | Fetch and verify the selected catalog archive for download or install |
| GET | `/api/v1/firmware/github` | Inspect model packages at one resolved source commit |
| POST | `/api/v1/firmware/github/import` | Fetch `{repository, commit, model}` into the Hub |
| GET | `/api/v1/firmware/packages` | List saved, validated packages |
| POST | `/api/v1/firmware/packages` | Upload a ZIP as multipart field `file` |
| GET / DELETE | `/api/v1/firmware/packages/{packageID}` | Read metadata or remove a package |
| GET | `/api/v1/firmware/packages/{packageID}/download` | Download the cached ZIP after checking its hash |
| GET | `/api/v1/devices/{id}/firmware` | Read compatible OTA packages, USB and wireless stock restore availability, and the last restoration receipt |
| POST | `/api/v1/devices/{id}/firmware/install` | Confirm the package and brewer with `package_id`, `device_uid` and `confirm: true`. HTTP 202 starts a tracked update. |
| POST | `/api/v1/devices/{id}/firmware/restore-stock` | Start or resume the wireless stock restore with `device_uid`, `acknowledge_loss_of_hub: true` and `confirm_restore: true`. Returns HTTP 202. |
| POST | `/api/v1/devices/{id}/firmware/stock-started` | Record the owner's observation of the stock startup screen with `device_uid` and the restoration receipt’s `started` timestamp. Requires verified application and recovery images and a boot selection attempt. A lost selection acknowledgment produces `owner_reported`. A verified selection produces `owner_confirmed`. |

New imports return HTTP 201; an identical saved archive returns HTTP 200.
Changing the repository after browsing invalidates an import request for the
old source. GitHub errors and rate limits leave saved packages unchanged.
