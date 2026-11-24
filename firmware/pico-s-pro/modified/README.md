# Pico S/Pro standalone firmware 1.0.0

Run the commands in this guide from `firmware/pico-s-pro/modified/`.
The [machine overview](../README.md) links the separate OEM image and unchanged
[pre-v1 reconstruction](../reverse-engineered/README.md). The shared
[server application](../../../server/README.md) lives at the repository root.

The Pico S/Pro implementation provides a **standalone firmware profile**
with an on-device REST API, browser console, recipe collection, settings,
local session identifiers, retained brew histories, and optional telemetry
forwarding. Firmware **1.0.0** keeps the REST contract at `/api/v1`.
It boots without a PicoBrew account, cloud recipe
server, registration request or firmware-update service. The browser assets
are compressed files on the Pico's serial flash; no companion server or CDN is required.

The standalone main menu opens saved recipes with **Brew Recipe**. Earlier manual brews remain in history.

Firmware 1.0.0 includes separate S and Pro images, built-in PicoPak recipes,
zero-minute heating steps, checkpoint recovery, Hub telemetry and
[Wi-Fi updates through independent recovery](docs/hub-ota.md). A brewer
running the earlier shared development image also labeled 1.0.0 requires
USB setup. Stable 1.0.0 reports `"release_channel":"stable"` at `/api/v1`.

Start with the [standalone guide](docs/standalone-api.md), the
[OpenAPI contract](docs/openapi.json), and the separate
[device-key provisioning procedure](docs/standalone-provisioning.md).
The [optional forwarding guide](docs/telemetry.md) includes a standard-library
HTTP receiver that archives live readings in SQLite on a Mac or Linux computer.
Use the Hub’s **USB setup and recovery** flow for the initial installation.
The installer verifies the appliance against its own backup and flash layout
before writing. [USB setup](../../../server/docs/firmware-packages.md#usb-setup-backup-and-stock-restoration)
The [flash-layout analysis](docs/flash-layout.md) describes a separate example
listing with unknown Pro/C identity.
`make` now selects the standalone build on this branch. The preserved legacy
profile remains available through `make firmware` and `make verify`.

## Backup and installation tools

Keep complete flash backups, API keys and installation reports private. A
firmware build does not establish compatibility with an appliance's board,
bootloader or flash layout. Review the exact hardware and preserve a verified
original backup before installation.

| Tool | Purpose |
| --- | --- |
| `tools/pico_uart_probe.py --transport usb` | Direct-USB handshake and complete version response |
| `tools/pico_flash_backup_v2.py` | Two full reads, byte comparison and private backup files |
| `tools/pico_inspect_backup.py` | Offline inspection with network/settings files excluded from the review ZIP |
| `tools/pico_flash_preflight.py` | Read logical boot metadata and check live firmware hashes |
| `tools/pico_flash_install.py` | Separate guarded key provisioning, application installation and stock restoration |

The probe, backup and preflight tools enforce read-only flash access. The
[guarded writer](docs/macos-usb-installation.md) checks the selected S or Pro
model, the original backup, and the brewer’s protected files and allocations
before writing. The historical backup labeled “Pico Pro” belongs to the
owner-identified Pico S. Use the model printed on the appliance.

The recovered factory reset path relies on the former cloud service and is
not an offline restore procedure. Standalone disables that action. Stock
restoration, interrupted-write recovery, interlocks and brewing still require
physical acceptance for the current release.

## Standalone features

| Capability | Implementation |
| --- | --- |
| Local operation | Cloud registration, recipe/session requests, reporting and updates replaced or disabled at compile time |
| REST API | Versioned `/api/v1` resources for status, recipes, settings, events, retained history, forwarding and commands; embedded interactive API explorer |
| Remote front panel | Live 128 × 64 OLED image plus encoder turn/click controls for existing prompts and menus |
| Programs | Local recipes, sous vide, cold brew, rinse, descale, circulation, draining and racking |
| Stop | Latched output inhibition, cooperative cancellation and idle-only clear; filter removal during interrupt-side pumping also latches a stop |
| Recipes | 16 on-device recipes, up to 15 steps each, with validated input and CRC-protected alternating records |
| Browser console | Same-origin HTML/CSS/JavaScript, recipe editing/import/export, live readings, upright OLED preview, up/down controls, history graphs and display preferences |
| Brew history | Up to eight recent entries sharing 4,224 compact samples; retention depends on brew length and state changes; persistent checkpoints, JSON/CSV export, and eviction of the oldest completed/interrupted brews |
| Optional forwarding | Hub configures live status forwarding when the brewer is added; Settings shows read-only connection status; the authenticated management API remains available |
| Wi-Fi changes | Clear old addresses and reconnect through a fresh IPv4 DHCP event before reporting success |
| Authentication | Per-device bearer key in `/local/api.key`; no shared/default credential or embedded secret |

Use the front-panel workflow for pause and resume. The Hub can update a
provisioned Pico S or Pro over Wi-Fi. [Firmware updates](docs/hub-ota.md)

The Activity screen has been removed;
`GET /api/v1/events` retains the latest eight events from the current boot and
remains available through the API explorer. Fatal firmware halts can still
require a physical reset. See the guide for the exact operating and validation limits.

### Build and test standalone

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements-test.txt
make bootstrap
make standalone-verify MODEL=pico-pro
# For a Pico S instead:
make standalone-verify MODEL=pico-s
```

Outputs are isolated by model under `build/standalone/<model>/firmware/`:
`pico-pro-standalone.bin` or `pico-s-standalone.bin`, matching ELF/map, and
`firmware-report.json`. The default `MODEL=pico-pro` preserves the historical
build choice. Use `--model pico-s` with `tools/build_firmware.py --standalone`
for the equivalent CLI build. Only S and Pro are supported by this shared
implementation. Pico C, generic Z and Zymatic each have a separate firmware
component under `firmware/`.
`PICO_MODEL` in `include/picobrew/model.h` controls REST identity, serial
branding and the OLED model name. Browser branding follows that API identity.
The build checks vectors, the complete link, the physical RAM budget, absence
of the discontinued cloud protocol strings, and separation from test fixtures.
The stable applications are **169,304 bytes** for Pico S and **169,312 bytes**
for Pico Pro. Both reserve **32 KiB each for stack and heap** and leave
**1,112 bytes of unallocated SRAM** at link time. The five compressed website
files occupy eight serial-flash blocks. The per-model [build reports](firmware/standalone/README.md)
record the linked memory layout and the reserved feature buffers. Runtime peak
stack and heap use still require measurement.
Application and SDK link-time optimization removes unused code; the legacy profile retains its
original compilation and exhaustive symbol checks.
The pinned standalone SDK patch omits Energia's unused eight-client TCP wrapper
registry; the native API listener uses SimpleLink sockets directly. Wi-Fi
reconnection uses a separate standalone-only SDK patch to require a fresh
IPv4 lease; the legacy profile retains its recovered behavior.

The optional browser suite exercises the actual portable C API/store behind a
local HTTP harness, with hardware boundaries simulated:

```sh
python -m pip install -r requirements-web-test.txt
python -m playwright install chromium
make web-test
```

The USB protocol, receive-queue and offline-inspection tests run without
physical hardware:

```sh
python -m pip install -r requirements-usb.txt
make usb-test
```

The [standalone workflow](../../../.github/workflows/standalone.yml) runs portable tests, browser workflows,
USB-tool simulations and the ARM firmware build on the feature branch. A CI workflow's presence is not
a claim that its run passed; inspect the run for the specific commit.

## Source recovery background

The base is an editable C/C++ reconstruction of the Pico Pro 0.1.34 application,
with native Energia/TI dependencies and a complete firmware build. It includes
startup and the main menu, brewing and maintenance programs, the display and
encoder, stepper/RFID protocols, networking, storage, and legacy firmware updates.

Names, types and module organization were reconstructed from binary behavior;
this is not the lost original source tree. The generated firmware is not
byte-identical to the original. Production code does not call the original
binary or depend on its application RAM addresses. The reference binary is
used only for analysis, asset extraction and comparison tests.

This directory contains the modified buildable reconstruction. `legacy/`
preserves the complete earlier decompiler-export snapshot from commit
`07359ac60c458efd59bfedc5e020344108cdf1d1`, including its source, analysis and
checksums. It is separate from the current build. The original `mcuimg2.bin`
and license notices remain available in this directory; the comparison tests use
the same original image under `reference/original-0.1.34.bin`.
`CONTENTS.sha256` records the packaged recovery snapshot, not subsequent
standalone branch edits. Use a fresh build report and the CI run for the
specific commit to validate a standalone candidate. The preserved export
retains its own `legacy/SHA256SUMS.txt`.

## Source map

| Location | What to modify |
| --- | --- |
| `src/application.c` | Complete setup/main loop and concrete bindings between all modules |
| `src/recipe.c` | Recipe decoding, per-step phase/action engine, session/restart and completion |
| `src/recipe_programs.c` | Manual brew, sous vide, cold brew and PicoStill orchestration |
| `src/recipe_utilities.c` | Prime/reprime, filter removal, vacuum, rinse, first rinse, drain, valve flushing, racking and descale |
| `src/control.c`, `src/phase.c`, `src/pid.c` | Timers, PID, thermal faults, pump/heater timing and phase lifecycle |
| `src/sensors.c`, `src/actuators.c`, `src/state.c` | Thermistors, outputs/interlocks and owned application state |
| `src/device.c`, `src/rfid.c` | Shared SPI, auxiliary controller/stepper/buzzer and Pico's MFRC522 variant |
| `src/ui/` | OLED/graphics/font, screens, menus, input editing, diagnostics and translations |
| `src/network/` | WiFi, HTTP/API, files/preferences/recovery, Still protocol, update/boot info and string helpers |
| `src/local_api.c`, `src/local_store.c`, `src/local_control.c` | Standalone REST resources, persistent records, command queue and stop latch |
| `src/local_history.c`, `platform/energia_history.cpp` | On-device brew recording, checkpoints, retention and paged reads |
| `src/local_telemetry.c`, `platform/energia_telemetry.cpp` | Optional HTTP forwarding, saved destinations and bounded socket progress |
| `src/http_server.c`, `platform/energia_http_server.cpp` | Bounded authenticated HTTP transport and native SimpleLink listener |
| `web/`, `tools/embed_web.py` | Browser source and reproducible gzip assets stored on serial flash |
| `tools/pico_telemetry_receiver.py` | Optional standard-library HTTP receiver and SQLite archive |
| `platform/` | Actual SDK bindings, appliance entry, custom TCP client and Pico memory layout |
| `include/picobrew/` | Typed application interfaces and configuration |
| `tests/`, `analysis/` | Reproducible checks, original-entry maps, limits and measured results |

The original application's 462 indexed entry points are accounted for in
`analysis/completion-audit.json`. Additional recovered callbacks and custom
library routines are also listed. An entry map establishes scope; it does not
mean every possible input or physical operating condition has been tested.

## Legacy build

The verified build uses GNU Arm Embedded 10.3-2021.10, Cortex-M4 Thumb, AAPCS and
software floating point. The pinned Energia commit and downloaded archives
are recorded with SHA-256 hashes in `dependencies/lock.json`. This is a working
rebuild toolchain; PicoBrew's exact historical compiler has not been established.

On Linux x86-64, with Python 3.12+, make and host GCC available:

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements-test.txt
make bootstrap
make firmware
make verify
```

The bootstrap installs project-local dependencies. It does not install system
packages. Allow roughly 1 GB for the compiler, SDK and build products. Other
hosts can supply a compatible installed ARM toolchain:

```sh
export PICOBREW_ARM_PREFIX=/absolute/path/to/bin/arm-none-eabi-
export PICOBREW_SDK=/absolute/path/to/energia
make firmware
```

The compiler prefix includes the trailing dash. `make build` builds the
application/adapter archives and function-test ELF. `make firmware` adds the
native SDK startup and complete appliance entry using the Pico linker script.
Neither command writes to a device or deploys to a server.

### Firmware outputs

| File under `build/firmware/` | Purpose |
| --- | --- |
| `pico-pro-recovered.bin` | Raw complete application image beginning at 0x20004000 |
| `pico-pro-recovered.elf` | Matching image with symbols/debug information |
| `pico-pro-recovered.map` | Linker map, symbols and dependency providers |
| `pico-pro-recovered.update.txt` | Original getFirmware API response body, including leading/trailing markers |
| `firmware-report.json` | Source/binary hashes, vectors, sections and RAM-budget checks |

The downloadable package also includes these generated artifacts under
`firmware/`, with native compile/link evidence under `firmware/build-evidence/`.
Rebuild into `build/firmware/` after any source change. The separate
`build/application-validation.elf` includes test fixtures and is never an
installation image.

The linker reserves the first 16 KiB of the 256 KiB device RAM for the bootloader,
keeps the native 32 KiB stack and a 32 KiB heap, and checks the resulting image fits.
The native stack is explicitly aligned to eight bytes. Compiler-provided
newlib-nano supplies compact stdio, while the same pinned compiler's full
newlib supplies the allocation family. A bounded adapter preserves the SDK's
heap ownership and supplies the allocator's failure convention. Libm/libgcc
supply math and soft-float operations. No decompiled libc, math library or
guessed SDK object storage is used. See `docs/sdk-build.md` for the exact
boundary, allocation-failure evidence and remaining limits.

## Pointing the legacy application at a community server

Edit `include/picobrew/config.h` before rebuilding. `PICO_SERVER_HOST` defaults
to the original `picobrew.com`; `PICO_SERVER_PORT` defaults to 80. This preserves
the original protocol configuration and does not establish that the original
service is operating. A compatible replacement must implement the API requests
and response formats documented in `docs/network-recovery.md`.
These settings apply to the legacy build. The standalone build ignores them
and has no cloud-server fallback.

The update text is a response body, not a raw flash image or an HTTP response
with headers. It uses Pico's original length/checksum/hex encoding. The
checksum is a legacy compatibility mechanism, not an update signature. The
package test sends the complete generated body through the rebuilt ARM
receiver and checks that its file model receives the exact compiled binary.
It does not activate a boot slot or write a physical machine.

## Legacy verification and evidence

The original firmware and reconstructed application execute in separate ARM
emulators. Test boundaries provide explicit clocks, user input and peripheral
responses. The source image cannot call the reference firmware. Full native
TCP/filesystem/hash bridge tests use the actual production ELF; the other
suites use a separately named test ELF.

The recovered legacy profile passes **29,145 behavioral and integration
scenarios**, plus **20 edit/rebuild checks** and **52 immutable asset checks**.
These counts combine exact original/rebuilt comparisons with separately
identified tests of repaired malformed-input/failure behavior. They are not a
percentage of complete path coverage. `analysis/validation-summary.json`
provides the per-suite totals, exact image hashes and limits.

The production constructor also runs directly in the emulator: four cases
exercise 1,632 assertions, including 238 callback slots across 14 service
tables. Only its clock and destructor-registration boundaries are controlled.
This begins after data/BSS initialization; it does not emulate chip reset or
physical peripheral startup.

All 257 indexed UI application entries now have direct original-versus-rebuilt
ARM cases. The UI ledger records each entry's evidence and separately labels
graphics replacements implemented through composed operations, public fields,
an inlined caller body, or an elided no-op. This establishes entry coverage;
it does not claim exhaustive path or hardware validation.

The checks cover the full 12-bit thermistor ADC domain, PID/controller and
phase transitions, auxiliary-controller exchanges, actual RFID selection,
recipe phases/statuses/actions and utility workflows, original framebuffers,
encoder/menu flows, API and stored-file formats, update streaming, and native
SDK connection behavior. The entry audit requires every indexed application
entry to have a concrete source mapping. The final summary rejects stale
source/test/image hashes, undefined symbols and old-image dependencies.

`make edit-check` changes a sensor correction only inside an isolated temporary
copy, rebuilds ARM code, and checks both changed outputs and unchanged controls.
The packaged production calibration remains at the recovered value.

## Preserved behavior and deliberate repairs

Observed quirks remain in the recovered algorithms unless a repair is recorded.
Examples include Pico-specific RFID collision behavior, the service-button
subtraction direction, certain timeout comparisons, stepper mapping, the URL
backslash escape, and a duplicate recipe-carousel increment.

Defined repairs address concrete original buffer/initialization and I/O failure
problems: bounded recipe/restart/remote strings, terminated WiFi security text,
incomplete Still telemetry rejection, UID buffer capacity, exact file-write
results, and failed boot-info operations preventing successful update/reset.
Those failure cases are tested as changed behavior, not described as equivalent.
The domain documents separate repairs from original-compatible paths.

The rebuild runtime also includes a verified allocation repair. Native tests
found that heap exhaustion could make the initially selected nano allocator
access address zero, while unchecked signed heap increments could escape the
reserved heap. The selected full-newlib allocation family and bounded SDK
adapter pass 47 native allocation scenarios, including exhaustion, overflow,
free/reuse and preservation of the old allocation when reallocation fails.

## What still needs the machine

The owner reported installing stable 1.0.0 on a Pico S. The [USB and wireless
update record](docs/hub-ota.md) covers that brewer. A controlled brew, sensor
calibration, interlock testing and interrupted-update trials still require
physical validation. Qualification of the stable Pico Pro image on a separate
Pro also remains pending. See the [hardware acceptance procedure](docs/hardware-validation.md).

## Further reading

- [Optional telemetry forwarding](docs/telemetry.md): destination settings, receiver setup, payloads and SQLite export.
- `docs/recipe-recovery.md`: recipe/session/program semantics, branch coverage and parser repairs.
- `docs/ui-recovery.md`: graphics/assets, menus/input, workflows and UI test limits.
- `docs/network-recovery.md`: server/API, files, Still, update and failure behavior.
- `docs/device-rfid-recovery.md`: SPI handshakes, card selection, device workflows and evidence.
- `docs/state-pid-recovery.md`, `docs/control-recovery.md`, `docs/phase-recovery.md`: control core details.
- `docs/sdk-build.md`, `docs/sdk-boundary-review.md`: dependency provenance and native integration.
- `reference/provenance.json`: identity and public provenance of the 162,792-byte reference image.

Upstream dependencies retain their own licenses and notices. This recovery
does not assign a new license to PicoBrew's original binary or embedded assets.
See [license boundaries](../../../LICENSES.md) for the server and bundled catalogue.
