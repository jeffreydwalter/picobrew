# Firmware by machine

Each machine has `oem/`, `reverse-engineered/` and `modified/` locations.
Version-specific tools and tests live with their corresponding source tree;
general repository tools live at root `tools/`.

The public standalone applications are **1.0.0**. The [wireless release catalog](releases.json) lists seven packages for Pico S, Pico Pro, the two Pico C displays, generic Z modules, PicoStill and PicoFerm. The Hub’s USB installer prepares a personalized Zymatic 1.0.0 application for the verified boot layout. Version numbers in the OEM column below identify the original PicoBrew firmware. [Installation and recovery by model](../server/docs/firmware-packages.md#usb-setup-backup-and-stock-restoration)

| Directory | OEM material | Source status |
| --- | --- | --- |
| [`pico-s-pro/`](pico-s-pro/README.md) | Verified device application image plus 1 upstream update envelope | Frozen pre-v1 OEM reconstruction, standalone brewing firmware with Hub OTA on provisioned units and a separately preserved recovery prototype |
| [`pico-c/`](pico-c/README.md) | 2 upstream update envelopes, including the alternate 0.1.34 image | Recovered C application and standalone builds for explicit SSD1325/SSD1309 controllers |
| [`z1/`](z1/README.md) | See the separate Z-series archive; compatibility is not yet assigned | Uses shared development under z-series; no model-specific image claim |
| [`z2/`](z2/README.md) | See the separate Z-series archive; compatibility is not yet assigned | Uses shared development under z-series; no model-specific image claim |
| [`z-series/`](z-series/README.md) | 2 unchanged upstream Z-series images | Recovered Core/Head application and standalone ESP32 source/image build, local recipes, explicit restart recovery and common API/browser |
| [`zymatic/`](zymatic/README.md) | Verified 1.1.14 HEX from a separate pinned support archive | Reconstructed AVR control paths and USB conversion for offline brewing and wired monitoring. Both custom profiles ship as 1.0.0. The extended profile adds dial pairing and session history on units with a verified boot window |
| [`picoferm/`](picoferm/README.md) | 16 unchanged upstream images | Recovered ESP8266 application and standalone sensor/recording firmware with bounded acquisition, calibration and common API/browser |
| [`picostill/`](picostill/README.md) | 6 unchanged upstream images | Recovered ESP32 application and standalone accessory controller with native pump/fan interlocks and common API/browser |

All implemented standalone profiles have source builds and software validation.
The Z and accessory packages use explicit custom reference layouts; they have
not been physically installed or qualified on an individual device. Z1/Z2
mapping and Zymatic hardware qualification are outstanding. API capabilities
describe actual hardware: PicoFerm does not advertise heating, pumps or recipes;
PicoStill does not advertise a heater or brewer recipe control.

## Complete upstream copy

All **27 files / 13,583,860 bytes** from
[`chiefwigms/picobrew_pico/app/firmware`](https://github.com/chiefwigms/picobrew_pico/tree/36acae3003845b72568ef5f7d6cf168acb6f9b69/app/firmware)
are included. [upstream-manifest.json](upstream-manifest.json) records every
original path, destination, Git blob identity, SHA-256, size and mode. No
version, alternate file or accessory family was omitted.

The S/Pro and C `.bin` files from that archive are ASCII OEM HTTP update
envelopes with hex payloads. They remain unchanged and are not presented as
raw device dumps. The separately verified S/Pro application image lives in
`pico-s-pro/oem/device/`.

Run `make verify-imports` at the repository root to check every copied file and
reconstruct the exact upstream Git subtree identity. The same check verifies
the complete pre-v1 S/Pro source snapshot. Original firmware packaging and
archive availability do not establish that an image can be flashed to another
model or that a custom port has been completed.
