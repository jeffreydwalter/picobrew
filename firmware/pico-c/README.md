# Pico C

The complete upstream images for this family are in [`oem/upstream/`](oem/upstream/).
They retain their source filenames and bytes; see the repository-wide
[import manifest](../upstream-manifest.json).

[`reverse-engineered/`](reverse-engineered/) contains the typed OEM application
reconstruction and complete native CC3200 builds for the SSD1325 and SSD1309
display variants. Its documentation separates original-instruction verification,
bounded-memory repairs, and hardware qualification. No physical installation or
brewing validation is claimed.

[Standalone firmware 1.0.0](modified/README.md) adds the local API, device
website, recipes, history and Hub-managed live readings to the recovered C
application. Both display variants have a 1.0.0 application and matching
1.0.0 recovery. USB setup provisions the brewer. Subsequent Hub updates use
Wi-Fi once the Hub has the independently verified display variant. Physical
installation, brewing and power-loss testing remain pending for both variants.
[Setup and recovery](modified/docs/standalone-provisioning.md)

The build/API identity is `pico-c` / **Pico C**, in family `pico-c`.
Firmware follows the shared [model identity contract](../../docs/machine-identity.md).
Build through the root router with `MODEL=pico-c` and an explicit
`DISPLAY_VARIANT=ssd1325` or `DISPLAY_VARIANT=ssd1309`. The compiled model/display
identity does not detect hardware or qualify the wrong initial flash.
