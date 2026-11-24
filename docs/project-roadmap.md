# Standalone firmware project roadmap

## Goal and layout

Reverse engineer the firmware for **Pico C, Pico S/Pro, Z-series and Zymatic**
so every supported machine offers the same standalone operating model, REST
API and browser experience as the custom S/Pro firmware. For setup and everyday use, see the [PicoBrew owner guide](../README.md).

Each machine has `firmware/<machine>/{oem,reverse-engineered,modified}`:
original images, the preserved OEM reconstruction, and our custom firmware.
The complete upstream archive also includes PicoFerm and PicoStill images.
The shared Hub is self-contained in [`server/`](../server/README.md), with
general repository tools at root `tools/`.

Each firmware port implements the common public behavior through its own
hardware drivers, brewing controls and storage limits. The optional Hub uses
the same fleet, recipe and brew-history workflows across supported machines.

## Current support

| Machine or archive | Firmware state | Live Hub operation |
| --- | --- | --- |
| Pico S/Pro | Original application image, frozen 396-file reconstruction, and separate S and Pro standalone builds. The standalone firmware supports separate OTA recovery on verified layouts. | REST controls, recipes, history, and wireless updates on eligible machines. Complete brew and power-loss qualification remain machine-specific. |
| Pico C | Original 0.1.34 images, OEM reconstruction, and model-specific standalone builds for SSD1325 and SSD1309 displays. | Model and display discovery, recipes, history, live readings, and controller-scoped packages. USB setup requires evidence of the physical display controller. USB setup provisions wireless recovery for that display. Physical installation and brewing trials remain outstanding. |
| Z-series (Z1–Z4 stacks) | Two original releases preserved. Each module uses the shared standalone application. Initial USB conversion verifies its active stock 0.0.119 image and flash layout. | Discovery, direct recipe and Program Maker transfers, grayscale display, controls, history, and wireless updates after USB provisions protected recovery. Service each module individually. Physical qualification is pending. |
| Zymatic | Original 1.1.14 brew controller and AVR extensions. USB setup uses the compact application for first conversion and offers the extended application on a verified boot layout. Both ship as 1.0.0. | Stock machines can sync recipes and sessions. Both custom layouts support wired monitoring. The extended application adds dial pairing, remote front-panel controls, and EEPROM history. Hardware trials are pending. |
| PicoFerm | Sixteen originals preserved. Standalone 1.0.0 measures fermentation temperature, native pressure and battery voltage. USB setup provisions two application banks for Wi-Fi updates. | Start or stop a recording, set the sample interval, calibrate pressure and attach a recording to a batch. The PicoFerm sends readings to the paired Hub independently of the brewer. |
| PicoStill | Six originals preserved. Standalone 1.0.0 implements the accessory controls and supports Wi-Fi application updates after USB provisions its recovery partition. | Native modes, pump and two fan channels, temperature and pressure, session history and live readings. |

Recipe-planning profiles, archived programs and simulator results do not
establish live support. See the [S/Pro firmware status](../firmware/pico-s-pro/modified/README.md),
[Hub coverage matrix](../server/docs/coverage.md),
[OEM source manifest](../firmware/pico-s-pro/source-manifest.json) and
[complete upstream manifest](../firmware/upstream-manifest.json).

On eligible Pico S and Pro machines, the main firmware hands an update to the separately installed recovery application. The Hub transfers the application and matching website, then verifies the new version after restart. Physical interruption and recovery testing remains incomplete. The archived 0.1.0 web recovery candidate documents an earlier design and has its own build evidence. [Current Hub OTA procedure](../firmware/pico-s-pro/modified/docs/hub-ota.md) · [Archived recovery candidate](../firmware/pico-s-pro/modified/recovery/README.md)

## Shared behavior

The [firmware parity matrix](firmware-parity.md) records which workflows are
shared and which controls depend on the actual machine hardware.

The [S/Pro API guide](../firmware/pico-s-pro/modified/docs/standalone-api.md)
and [OpenAPI contract](../firmware/pico-s-pro/modified/docs/openapi.json) are
the starting point for the common device contract:

- Local startup, installed recipes and brewing without a vendor cloud or Hub.
- A device-hosted browser interface, settings, recipe editing and API explorer.
- Versioned status, capabilities, recipes, commands, settings and retained
  brew-history resources, authenticated with a per-device key.
- Firmware-owned heating, pumping, timing, preparation prompts and stop
  behavior, with local and remote interfaces using the same machine state.
- Shared Hub recipe management, reviewed deployment and history collection,
  with model-specific capacities and supported operations exposed explicitly.

Each port must preserve these workflows while reporting its actual sensors,
storage and brewing capabilities.

## Work ahead

Keep originals and frozen reconstruction baselines unchanged, and retain every
remaining original-execution limitation in the component evidence. Software
source/image builds and the shared local API/browser/Hub are implemented for
the available S/Pro, C, generic Z and accessory targets. Physical qualification
still requires matching device backups/layouts, real sensor/output tests,
heap/stack timing measurements, controlled restart tests and operating trials.
Zymatic has its own original AVR application and requires trials on its own hardware. Pico C needs physical installation and operating trials for each display variant. Updated releases and their model identities are recorded in the [firmware release index](../firmware/releases.json).

Community servers and archives are read-only research sources. The Hub connects directly to supported custom firmware and serves the stock Zymatic’s original recipe and session requests over the LAN. [Original Zymatic protocol support](../server/docs/stock-zymatic.md)
