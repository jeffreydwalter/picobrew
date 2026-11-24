# Archived development firmware 1.2.0

This record describes an earlier development build. See the [stable 1.0.0 packages](../firmware/standalone/README.md) for current downloads and installation.

This release gives Pico S and Pico Pro separate build identities and lets
PicoBrew Hub manage the connection for live readings. The REST compatibility
version remains `/api/v1`.

## Select the physical model when building

From `firmware/pico-s-pro/modified/`:

```sh
make standalone MODEL=pico-pro
make standalone MODEL=pico-s
```

The equivalent CLI is `python3 tools/build_firmware.py --standalone --model pico-s`.
The default is `pico-pro`, retaining the historical build choice. Archives,
objects, native SDK outputs and image files are isolated under
`build/standalone/<model>/`. Each image filename contains its model, such as
`pico-s-standalone.bin`. Skipping the archive build checks the recorded model
and profile before linking, including when an explicit output directory is used.

The single `PICO_MODEL` compile definition selects `model`, `model_name`, and
`model_family` on discovery, health and status responses. It also selects the
serial startup banner, OLED splash and information-page model name. The browser
uses the reported model for its title and branding. The Hub can therefore identify
a newly connected brewer without asking the user to select S or Pro.

The shared implementation supports only `pico-pro` and `pico-s`. A different
filename or build label cannot establish Pico C, Z or Zymatic support; those
machines require their own ports. A firmware's model field identifies the
selected build, not a hardware probe. Install the image matching the appliance.

## Hub-managed connection

Add the brewer in PicoBrew Hub with its URL and provisioned API key. The Hub
configures telemetry through the existing authenticated API. The brewer website's
**Settings → Hub connection** section shows the destination and delivery status.
It no longer offers destination editing, including through the browser API
explorer. `GET` and authenticated `PUT /api/v1/telemetry` remain available for
Hub management and explicit API integrations, and saved destinations survive
the upgrade.

Local brewing and retained brew history continue independently of Hub availability.
Telemetry is best-effort current snapshots; reconnecting does not backfill them.

## Packages and verification

Model packages live under `firmware/standalone/<model>/`; their reports record
exact model identity, hashes, memory layout, source inputs and browser manifest.
The five shared browser files remain in `firmware/web/` and must match the image's
report. Use `python3 tools/verify_firmware_package.py --model pico-s` after a
fresh build to compare a package with its matching model output.

Native tests compile the actual API twice and verify both identities on all
three endpoints. They also reject unsupported preprocessor and CLI values and
mismatched reused archives. Browser tests cover model branding and read-only
Hub status on desktop and mobile. The physical installation, brewing and
hardware acceptance status remains separately recorded in
[hardware-validation.md](hardware-validation.md); a successful build or simulation
does not establish those results.
