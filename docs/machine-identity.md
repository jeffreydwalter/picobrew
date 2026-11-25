# Machine identity and product builds

The firmware declares its product model at compile time. This identifies the
image that was built; it does not inspect hardware or establish compatibility
with a different product. Select the intended product before building.

The canonical identifiers are recorded in [`firmware/models.json`](../firmware/models.json):

| Model ID | Display name | Source family | Custom firmware |
| --- | --- | --- | --- |
| `pico-pro` | Pico Pro | `pico-s-pro` | Implemented |
| `pico-s` | Pico S | `pico-s-pro` | Implemented |
| `pico-c` | Pico C | `pico-c` | Implemented; explicit SSD1325/SSD1309 target; physical qualification outstanding |
| `z-series` | Pico Z | `z-series` | Complete source/reference image build; physical Z1/Z2 mapping unassigned |
| `picostill` | PicoStill | `picostill` | Complete standalone accessory source/reference package |
| `picoferm` | PicoFerm | `picoferm` | Complete standalone sensor source/reference package |
| `zymatic` | PicoBrew Zymatic | `zymatic` | OEM-based applications with USB installation and stock restoration. The extended profile adds remote dial control, local pairing and history on qualified boot layouts. Physical trials pending |

The root build router accepts a model through `MODEL` or `MACHINE`:

```sh
make firmware MODEL=pico-s
make firmware MODEL=pico-pro
make firmware MODEL=pico-c DISPLAY_VARIANT=ssd1325
make firmware MODEL=pico-c DISPLAY_VARIANT=ssd1309
make firmware MODEL=z-series
make firmware MODEL=picostill
make firmware MODEL=picoferm
make firmware MODEL=zymatic HUB_HOST=brewery.lan:8080
make firmware MACHINE=pico-s
make firmware-test MODEL=pico-s
make firmware-models
```

`MACHINE=pico-s-pro MODEL=pico-s` is also supported. The original family-only
command, and a command without either selection, default to `pico-pro`.
Conflicting selections and unimplemented product models fail before invoking
the component build. Selecting a different model never relabels an S/Pro image
as a Pico C, Z, or Zymatic image.

The previously reserved `MODEL=z` spelling is accepted as an alias for the
generic `z-series` build. `MODEL=z1` and `MODEL=z2` are not enabled by that alias.
Original device backups are needed to establish those physical mappings.
The ESP32/ESP8266 reference builds use the hash-pinned Linux x86-64 toolchains
and explicit package layouts described in each component. Their USB test route
is unavailable because a qualified installer has not been established.

Within the S/Pro component, use `make standalone MODEL=pico-s` or
`python tools/build_firmware.py --standalone --model pico-s`. Build outputs are
isolated under `build/standalone/pico-s/firmware/` and
`build/standalone/pico-pro/firmware/`, with matching `<model>-standalone`
filenames. Committed installation packages use the same per-model layout under
`firmware/standalone/`. The device website assets are shared.

Pico C uses its recovered C source in `firmware/pico-c/modified/`, with outputs
under `build/standalone/pico-c/<display>/firmware/`. The explicit display selection
is required for builds/packages; dimensions or model names never choose it
implicitly. Both native images and their API identity are software-tested; a
physical installation and brewing acceptance remain outstanding.

## API contract

Model-aware firmware exposes stable identity fields alongside its existing
version and capabilities:

```json
{
  "model": "pico-s",
  "model_name": "Pico S",
  "model_family": "pico-s-pro",
  "firmware_version": "1.0.0"
}
```

The startup display, device website, REST identity, and build report derive
their identity from the same compile-time selection. Future machine ports
should use these field names and their registry identifiers. They still need
their own hardware implementation and capability checks.

The Hub reads the authenticated model during connection and associates it with
the verified device UID. The UID identifies the particular machine; the model
selects its equipment profile. Reported unsupported models cannot enable a
different machine's control adapter. Firmware that predates model reporting
can still use an explicit profile selection; it is not guessed to be a Pro.
Correcting a current machine label does not rewrite archived brew snapshots.

## Pico C display identity and package applicability

C discovery, health and status also report `display_variant` (`ssd1325` or
`ssd1309`). The Hub requires C discovery/status to agree. Display frames carry
logical width/height, rotation and separate storage/transfer sizes. SSD1309 is
128 × 32 with rotation 0 and 1,024 retained/transferred bytes; SSD1325 is
128 × 64 with rotation 2 and 1,024 bytes. Existing S/Pro frame defaults remain
unchanged when metadata is omitted.

Automatic C connection and live-data setup do not require a hardware-confirmation
step. In the machine's firmware view, an owner can separately record a physically
verified controller. This observation is tied to the pinned appliance UID and
never populated from a software model, display dimensions or running-image
string. C packages are listed as matching only when that observation, the
current authenticated variant and the package variant agree. Package upload,
catalog browsing and download remain available without this observation.

The Hub supports C package paths under
`firmware/pico-c/modified/firmware/standalone/pico-c/<display>/`, with shared C
website assets under its `firmware/web/` directory. GitHub import requires the
explicit variant and a pinned commit. These checks do not install firmware;
`install_supported` and C `firmware_update` remain false. Software identity and
package hashes alone cannot prevent a wrong initial physical flash.

## Hub-managed reporting

The Hub keeps the machine's desired live-data receiver settings: host, port,
and whether to send live readings. These fields are always visible under
**Brewer preferences → Live readings**. New connections start with this Hub's
verified reachable address; existing custom destinations and disabled sending
settings are preserved.

The Hub sends its saved values when the machine connects or the settings are
saved. Offline or busy machines receive pending changes once available and
idle, after the Hub verifies their identity. Reconnects and Hub restarts keep
the same saved values. Ordinary setup requires no receiver form on the firmware
website and no automatic/manual mode selection.

Authenticated polling and history reads remain authoritative even when
forwarded readings are unavailable. Forwarded readings do not authorize
machine commands.
