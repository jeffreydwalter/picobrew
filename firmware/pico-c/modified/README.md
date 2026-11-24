# Pico C standalone firmware 1.0.0

This is a complete source build of the standalone Pico C application, based on
its frozen [OEM reconstruction](../reverse-engineered/README.md). It retains
the recovered C controller, peripheral mappings, recipe/maintenance behavior,
two-row UI and scrolling prompts. The new local API, storage, history, browser
and Hub connection follow the existing S/Pro standalone contract.

Choose **Brew Recipe** on the front panel to run a saved recipe. Earlier manual brews remain in history.

Both native CC3200 images link and pass the documented software checks. They
have **not been installed, run on a physical Pico C, or used for brewing**. The
OEM comparison evidence and the new standalone tests have different scopes;
neither establishes physical timing, wiring compatibility or power-loss safety.

## Explicit controller builds

| Selection | Logical display | Rotation | Buffer / transferred bytes | Raw image | Unallocated RAM |
| --- | --- | --- | --- | --- | --- |
| `ssd1325` | 128 × 64 | 180° | 1,024 / 1,024 | 167,400 bytes | 2,320 bytes |
| `ssd1309` | 128 × 32 | 0° | 1,024 / 1,024 | 167,296 bytes | 2,320 bytes |

The alternate OEM implementation transfers all 1,024 bytes despite exposing a
32-row logical viewport. This behavior is preserved. The API returns the full
raw buffer and explicit geometry; both websites render only the logical rows.
The native link retains a 32 KiB SDK stack and a 32 KiB heap in both variants.
Unallocated RAM is the link-time remainder, not a measurement of runtime use.

The model is always `pico-c` / **Pico C**, family `pico-c`. `display_variant` is
selected explicitly when compiling. Neither string detects physical hardware.
Verify the controller independently before choosing an image. Hub package
applicability requires matching model, authenticated variant and a separately
saved hardware observation tied to the appliance UID. Ordinary Hub connection,
telemetry setup and package downloads do not require that hardware confirmation.

## Implemented behavior

- Offline startup and local recipe selection, with sixteen recipe slots and up
  to fifteen explicitly routed steps per recipe. Eight attributed starter
  programs use the same local slots as user recipes.
- Authenticated API v1, a device website, live OLED/encoder access, maintenance
  commands, settings and a deferred command queue.
- A stop latch that inhibits heater and pump writes and cancels blocking C
  workflows, including marquee prompts, character editing and the C drain
  sequence. Filter removal also trips the ISR-safe output inhibition path.
- CRC/readback-verified recipe, preference and recovery storage. Recovery is an
  explicit offer for a matching unchanged recipe, with outputs and session
  creation left off until the user accepts.
- Persistent brew history, bounded history export, and optional live readings.
  The Hub saves and applies host, port and enabled state when the machine
  connects or the settings change. The device website shows the connection.
- Bounded Wi-Fi retries, fresh DHCP lease validation, foreground-only HTTP and
  shared-SPI exclusion. The legacy cloud registration/download paths are
  disabled in this profile; native builds reject remaining cloud protocol
  strings.

PicoStill is not advertised. The legacy factory recovery action is disabled.
The Hub can update either Pico C display build over Wi-Fi once the device has been provisioned with its matching independent recovery and key. The [USB setup and stock restoration tool](docs/standalone-provisioning.md) verifies each brewer against its own original backup. Physical USB and OTA qualification is pending for both displays. No build or test command
connects to, programs or operates a brewer.

## Build and package

Use Python 3.12, a C/C++ host compiler and Make. From this directory:

```sh
python3 -m pip install -r requirements-test.txt
python3 tools/bootstrap_dependencies.py
make standalone MODEL=pico-c DISPLAY_VARIANT=ssd1325
make standalone MODEL=pico-c DISPLAY_VARIANT=ssd1309
make firmware-package MODEL=pico-c DISPLAY_VARIANT=ssd1309
```

Or use the repository router:

```sh
make firmware MODEL=pico-c DISPLAY_VARIANT=ssd1325
make firmware-package MODEL=pico-c DISPLAY_VARIANT=ssd1309
```

`--display ssd1325` / `--display ssd1309` is required by the component Python
build tools. There is no implicit hardware default. `PICOBREW_ARM_PREFIX` and
`PICOBREW_SDK` can select an already provisioned pinned compiler and SDK.

Each target lives in `build/standalone/pico-c/<display>/firmware/`, containing
the native ELF, raw image, map, build report, and optional legacy-envelope
encoding. The distributable image/report pairs are under
`firmware/standalone/pico-c/<display>/`; website files are under `firmware/web/`.
A Hub upload ZIP contains exactly the report, raw image, browser manifest and
five compressed browser files. Package validation covers their hash chain,
geometry, ARM vectors, RAM bounds and model/controller selection. It does not
provide a publisher signature or establish physical compatibility.

## Verification

```sh
make standalone-test
make tooling-test
make standalone-arm-test
make standalone-storage-test MODEL=pico-c DISPLAY_VARIANT=ssd1325
make standalone-storage-test MODEL=pico-c DISPLAY_VARIANT=ssd1309
python3 tools/verify_firmware_package.py --model pico-c --display ssd1325
python3 tools/verify_firmware_package.py --model pico-c --display ssd1309
python3 -m pip install -r requirements-web-test.txt
python3 -m playwright install chromium
make web-test
```

The host suite compiles the actual C application for both display variants.
Native storage tests execute the linked ARM bridge, SLFS and SimpleLink driver
with simulated command transport. The ARM interrupt suite executes the real
IPSR/PRIMASK guards and filter trip. Browser tests serve the real portable C API
with simulated flash/I/O and include the SSD1309 viewport on desktop/mobile.

See [standalone validation](analysis/standalone-validation.json) for the checked
artifact identities and the difference between local and CI evidence. All 18
local browser cases passed with Chrome for Testing 158.0.8127.36, covering
desktop/mobile workflows, the SSD1309 viewport and history/export isolation.
The report records the checked source and screenshot hashes. Remote CI remains
pending; the local browser result does not qualify physical installation or
brewing.

## Source and API documentation

[Source provenance](analysis/source-provenance.json) records frozen C and S/Pro
inputs. The C OEM base is commit `faf70ae`; its application source snapshot
SHA-256 is `e1d747afcb2efa75c7d4656e8d4efef7a1b5d99f10f03bd09ad08fcd86cdbbef`.
The C port applies standalone hooks to that recovered application; it does not
substitute S/Pro hardware source for C-specific behavior.

- [API and application behavior](docs/standalone-api.md)
- [Machine-readable API](docs/openapi.json)
- [USB setup, API key and stock restoration](docs/standalone-provisioning.md)
- [Hub connection and optional telemetry receiver](docs/telemetry.md)
