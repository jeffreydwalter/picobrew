# Standalone firmware parity

Each standalone brewer build serves a local website and API directly from the machine. The Z-series build targets one module. The Zymatic serves incoming requests over Ethernet and over Wi-Fi at the Home menu and programmed pauses, and its available controls depend on the installed memory profile. Both Zymatic builds retain the original 1.1.14 brew controller. PicoStill and PicoFerm also serve their own websites and APIs for accessory-specific functions.

The tables describe the standalone applications in this checkout. Build and simulator results establish software behavior. Installation, sensors and brewing require hardware checks. [Matching an image to a machine](machine-identity.md)

On Pico S, Pro, C and Z, **Brew Recipe** runs a saved machine recipe. On S, Pro and C, this is the menu previously called **Brew PicoPak**. Recipes made in the Hub and imported PicoPak programs use the same menu. The first 1.0.0 packages had a separate **Manual Brew** mode. The revised packages remove that entry and keep earlier sessions readable in history.

## Common behavior

| Behavior | S/Pro | Pico C | Generic Z | Zymatic | PicoStill | PicoFerm |
| --- | --- | --- | --- | --- | --- | --- |
| Local operation without a vendor cloud or Hub | Implemented | Implemented | Implemented | One USB-provisioned offline recipe | Implemented | Implemented |
| Authenticated API v1 and device identity | Implemented | Includes display variant | Z family identity | Wired Ethernet | Implemented | Implemented |
| Device website | Implemented | Implemented | Shared browser | Compact supports monitoring. Extended adds paired dial control. | Shared browser | Shared browser |
| Optional Hub connection and live readings | Implemented | Implemented | Implemented | Wired monitoring, outbound recipe sync over Ethernet or Wi-Fi | Implemented | Implemented |
| Firmware updates after initial USB setup | Wi-Fi with verified recovery | Wi-Fi with display-matched recovery | Wi-Fi on a provisioned module | Rear USB | Wi-Fi with protected recovery | Wi-Fi with dual-bank layout |
| Persistent history | Eight sessions sharing 4,224 samples | Eight sessions sharing 4,224 samples | Eight sessions sharing 4,224 samples | Extended retains eight sessions sharing 222 samples. Compact relies on Hub polling. | Eight sessions sharing 4,224 samples | Eight sessions sharing 4,224 samples |
| Default periodic history interval | 15 seconds | 15 seconds | 15 seconds | Extended samples about once a minute during a program | 15 seconds | 300 seconds, configurable from 60 to 86,400 seconds |
| Available controls and readings | Brewer profile | Brewer profile | Native Z profile | Monitoring on both profiles and eligible dial inputs on extended | Accessory profile | Sensor profile |
| Firmware target | Pico S and Pico Pro images | SSD1325 and SSD1309 images | Z-series module reference image | OEM 1.1.14 controller with a USB-installed extension sized to the boot layout | ESP32 reference image | ESP8266 reference image |

Each session preserves its sampling interval and channel metadata on S/Pro, Pico C, Z, PicoStill and PicoFerm. These builds report storage exhaustion, interrupted sessions and truncation. The Zymatic recorder additionally samples at step changes and can have gaps while the original controller is busy. [Zymatic history](../firmware/zymatic/modified/history.md)

## Zymatic

| Function | Availability |
| --- | --- |
| Programs | Both profiles retain the OEM brew controller, outbound Hub recipe sync and a provisioned offline recipe. |
| Wired device website and Hub | Compact supports read-only monitoring. Extended adds dial pairing, authenticated input against the current OLED frame, and on-device history that you can browse on the local site. |
| Firmware service | Rear USB backup, application installation and return to stock 1.1.14. First conversion uses the compact profile. Installing the extended profile requires independently measured boot space. Both profiles report release 1.0.0. |
| Remaining limits | The inbound API uses Ethernet. Firmware updates require USB. Raw actuator control and dedicated network recovery are unavailable. Physical installation, recovery, sensor and brewing trials remain outstanding. |

See the [firmware architecture and verification](../firmware/zymatic/modified/application.md).

## Controls that depend on the machine

| Capability | S/Pro and Pico C | Generic Z | PicoStill | PicoFerm |
| --- | --- | --- | --- | --- |
| Installed recipe catalogue | Sixteen recipes, up to fifteen steps | Sixteen recipes, up to fifteen steps | Not advertised | Not advertised |
| Starter recipes and restore | Attributed model recipes | Requires an attributed native conversion; not advertised | Not applicable | Not applicable |
| Physical panel and remote display/input | Native monochrome panel and encoder | Native grayscale panel, four display pages and ordered encoder input | No panel invented | No panel invented |
| Normal operating programs | Native brewer programs and maintenance | Saved beer recipes, sous vide, coffee and maintenance programs | Native manual, essential-oil and cleaning modes | Explicit recording, sampling and calibration |
| Interrupted-operation recovery | Explicit confirmation with unchanged-recipe checks | Explicit confirmation of a retained owned recipe/program checkpoint | Interrupted history; no automatic mode restart | Interrupted history; no automatic recording restart |
| Outputs | Recovered brewer controls | Recovered heater and MSP pump/valve controls | Pump and two fan channels; no heater | No heater, pump or fan controls |
| Readings | Recovered brewer sensors | Recovered brewer and auxiliary sensors | Named temperatures and pressure | Temperature, raw pressure and battery voltage |

Z canonical API recipes use direct native valves **1–6**. Archived native Z
programs use locations **0–5** for valves and **6** for pause. The Hub preserves
those archived programs as reference/export material until an explicit
conversion is supplied; it does not deploy their numbers as canonical recipes.
PicoFerm's pressure reading retains its evidenced raw units; battery readings
use the recovered ADC-to-voltage conversion and are reported in volts.

The modified Z profile uses Wi-Fi for its local API. Its recovered OEM source
also retains the original W5500 Ethernet and vendor protocols. Modified
firmware does not expose those cloud settings as required setup steps.

## Recipe checks and manufacturer care

The Hub checks ingredient recipes against the Pico S, Pro, C, generic Z and Zymatic brewing profiles. A recipe can remain in the planning library when the selected brewer cannot run it. During transfer the Hub applies the target’s limits and rejects unsupported mash methods, mash routes that bypass the grain, and schedules that would change timed hop contact. An intermediate drain can interrupt a timed hop addition. The Hub flags that timing conflict before transfer. It also blocks a generated stock Z export if an unheated step would inherit a previous heating target. Standalone Z can execute that transition. PicoFerm and PicoStill have their own session controls.

Single and bulk transfers both check the connected firmware's reported slot count, maximum steps, and support for zero-minute heating steps. During a bulk replacement the Hub reads each destination slot again before overwriting it. The transfer receipt identifies any remaining recipes that were skipped after a slot changed. The device protocol has separate read and write requests, so a simultaneous edit by a second client can still occur between the two requests.

On firmware that tracks maintenance, the machine records use and reports which tasks are due. The Hub shows those reminders on the Brewery page and on the connected machine. The local website also works when the Hub is offline. Older firmware can still show its model’s care schedule in the Hub. On Pico S, Pro and C, rinse after each brew and run Deep Clean after three brews or after descaling. For Deep Clean, put the tablet in the brewing keg on S and Pro, or in the center of the step filter on C. The 20-brew descaling interval applies when using reservoir water other than distilled or reverse osmosis water. On generic Z, rinse after each brew and clean after three brews or before extended storage. On Zymatic, clean the inline filter and rinse after each brew, then deep clean every three to four brews and rinse again. The PicoStill and PicoFerm pages cover condenser care and fermentation seal handling respectively. Opening the guidance does not start hardware. [Pico S manual](https://picobrewcontent.blob.core.windows.net/content/pico/Pico_Manual.pdf#page=127) · [Pico Pro manual](https://picobrewcontent.blob.core.windows.net/content/picopro/PicoPro_Manual.pdf#page=129) · [Pico C manual](https://picobrewcontent.blob.core.windows.net/content/picoc/PicoC_Manual.pdf#page=109) · [Z manual](https://picobrewcontent.blob.core.windows.net/content/z/Z_Manual.pdf#page=36) · [Zymatic manual](https://picodevblob.blob.core.windows.net/content/zymatic/Zymatic_Manual.pdf#page=23) · [PicoStill manual](https://picobrewcontent.blob.core.windows.net/content/picostill/PicoStill_Manual.pdf#page=58) · [PicoFerm manual](https://picobrewcontent.blob.core.windows.net/content/picoferm/PicoFerm_Manual.pdf#page=5)

## Verification and entry points

The component evidence includes native controller integration, comparisons
against original instructions, real filesystem and allocator replays, complete
SDK links, checked application images, and browser/Hub tests. Those checks have
different scopes; a passing diagnostic fixture alone does not establish a full
application or a physical-device result.

- [S/Pro source, model builds and operating contract](../firmware/pico-s-pro/modified/README.md)
- [Pico C source and explicit display variants](../firmware/pico-c/modified/README.md)
- [Z control, output inhibition and restart recovery](../firmware/z-series/modified/README.md)
- [PicoStill native controller and reference package](../firmware/picostill/modified/README.md)
- [PicoFerm bounded acquisition, memory replay and reference package](../firmware/picoferm/modified/README.md)
- [Shared API, display and history schema](../firmware/common/docs/api-schema.md)
- [Hub integration and capability limits](../server/docs/coverage.md)
- [Model identity and installation boundaries](machine-identity.md)
- [Complete Z standalone application and checked image](../firmware/z-series/modified/analysis/standalone-build.json)
- [All three reference packages: actual ELF, filesystem and rejection checks](../firmware/common/analysis/reference-package-validation.json)

Run the root `firmware-test`, `firmware`, and `firmware-package` targets with
the documented model selection. Pico C additionally requires an explicit
display variant. No build, package or test target flashes an appliance.

## Inputs still required for physical qualification

The Zymatic 1.1.14 application is preserved in a separately pinned support archive. The Z1/Z2 mappings and
complete device flash layouts are unverified. Those gaps require original
device evidence; another family's image cannot supply it. Each current image
still needs its corresponding physical installation and operating checks.

Pico S and Pro can install compatible packages over Wi-Fi after USB setup provisions their independent recovery. Pico C uses a separate recovery build for each of its two display controllers. Before offering a C installation, the Hub needs the verified controller for that machine, and recovery checks the target image again before it can boot. The C path has software and build verification. Installation and power-loss trials on both physical display variants remain outstanding. Stock C firmware and earlier standalone installations need USB setup to provision the new recovery. [S/Pro recovery](../firmware/pico-s-pro/modified/docs/hub-ota.md) · [Pico C provisioning](../firmware/pico-c/modified/docs/standalone-provisioning.md)

A qualified generic Z module and PicoStill can receive a verified application over Wi-Fi after USB provisions a separate recovery partition. On Z, USB setup backs up the existing flash, verifies that the live files fit the new layout, and checks the recipes, settings and history before writing. Service each module individually. An interrupted transfer leaves recovery available for another attempt. PicoFerm uses two application banks with a permanent boot selector. After the new image boots, the Hub checks its identity and digest before confirming it. The package, transfer and recovery paths have software tests. Physical upgrade and power-interruption tests are outstanding.

Earlier single-application layouts require USB setup. The Zymatic’s verified USB procedure preserves its serial bootloader. Its 1.1.14 image does not include that bootloader or disclose the board’s fuse settings. Network recovery on that board remains unverified. [USB service by model](../server/docs/firmware-packages.md#usb-setup-backup-and-stock-restoration)
