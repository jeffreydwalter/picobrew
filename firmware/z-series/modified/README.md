# Pico Z standalone firmware 1.0.0

This application composes the recovered Z119 Core, Head, program runtime,
heater, safety monitors, sensors, physical inputs, display and MSP controller
with the shared authenticated local API, browser, persistent storage and Hub
connection. It produces a complete ESP32 ELF and an independently checked
application image using the pinned historical compiler and Arduino SDK.

The identity is **`z-series` / Pico Z**. The available originals do not establish
which physical Z1/Z2 revisions use this image. The build and package are a
software reference, not a qualified initial installation. No build or test
command flashes or operates a brewer.

The idle home screen suggests Rinse after a brew and Clean after three brews. A completed cleaning program clears its reminder. The local website and authenticated status report the same machine records.

## Wireless firmware updates

Hub USB setup can provision an individually identified Z-series module with protected recovery. The Hub backs up the whole device and verifies the active stock image and partition layout first. For previously paired standalone installations, it also saves the running flash before repartitioning, checks the existing key, and verifies each migrated file and its metadata. Unreadable storage, an unknown application, or data that cannot fit stops setup before a write.

A provisioned module advertises `esp32-factory-v1`. From the machine’s Firmware panel, an idle module can restart into recovery, accept a matching application, read it back, and select it for boot. Recovery uses the existing Wi-Fi settings and device key and leaves the heater and pumps off. The 4 MiB layout reserves 896 KiB for recovery, 1,668 KiB for the application and 1,468 KiB for SPIFFS. USB recovery and OTA still require operating trials on physical hardware.

## Implemented local operation

Choose **Brew Recipe** on the front panel to select a saved recipe. The panel also
provides sous vide, coffee, maintenance, Wi-Fi and persisted language, temperature
and display preferences.
The common browser and Hub expose the same advertised capabilities, the actual
grayscale display and ordered encoder movement/clicks. Local operation and
startup do not require cloud DNS, registration or an Internet connection.
Wi-Fi setup scans while the existing connection stays active. The front panel
lists each visible SSID once and keeps its name and security with the selected
row. Back returns to the network menu during scanning and from the results.
A local IPv4 lease establishes connectivity even when cloud DNS is unavailable.

The recipe catalogue retains sixteen recipes with up to fifteen named steps.
Z recipes use direct native valve locations **1–6**; targets are unheated (`0`)
or 60–212 °F, durations are 0–599 minutes, and drains are 0–120 minutes. The
recovered Z builder still applies its valve and local boiling-temperature
limits. An unheated step explicitly stops the preceding PID before valve
movement. Original setup, filter and completion prompts remain interactive.

The local recipe format carries no starting-water quantity. For these programs,
the loading screen prompts the operator to add water according to the recipe.
The native duration estimator still uses its nominal heating volume.

The alternate Z Program Maker format preserves Celsius targets, seconds, the
PicoPak adjunct setting, beer setup prompts and scheduled operator pauses. A
program can use the keg (1), mash basket (2) and adjunct compartments (3–6).
Step type 2 requests an operator acknowledgement. Uploading a program saves
it for a separate start action. See the [recipe wire format](../../common/docs/api-schema.md#z-program-maker-recipes).

Archived native Z programs use a different grammar: locations 0–5 map to
valves 1–6, and location 6 means pause. They are retained as reference/export
data in the Hub; they are not silently interpreted as canonical API recipes.
The shared API does not advertise starter programs without an attributed,
model-specific conversion. S/Pro PicoPak data is not installed under a Z label.

Advertised commands are `brew`, `sous-vide`, `cold-brew`, `rinse`,
`clean`, `circulate`, `drain`, `rack-beer`, `chill`, `utilities`, `settings`,
`stop`, `clear-stop`, `pause`, `resume`, `encoder`, `resume-interrupted` and
`discard-interrupted`. Recipe and command fields are discoverable through
`/api/v1`. The generic API and display/history formats are documented in
[`../../common/docs/api-schema.md`](../../common/docs/api-schema.md).

## Restart recovery and output inhibition

A local brew reserves its history identity and saves an owned recipe/program
checkpoint before the native start action. Checkpoints update at instruction
changes and at most thirty seconds apart while running. They contain the
remaining native instructions, exact remaining timed wait, display references
and an owned recipe/string table. Editing or deleting a catalogue recipe cannot
change an already retained brew.

On restart, the controller cuts heater outputs and attempts a bounded MSP pump
cutoff. Loading a checkpoint never starts outputs. The physical panel, device
browser and Hub offer an explicit review/resume action. Resumption checks the
filter and controller state; its confirmation asks the operator to verify the
vessel and water. A heated hold reaches its saved target again before its
remaining timer runs. A paused program retains its acknowledgement, and an
unheated/drain stage does not introduce PID heating. A new history session
records the resumed operation; the earlier one remains interrupted.

Requested stop and normal completion write verified tombstones. A controller
fault retains the last valid checkpoint. Corrupt checkpoint banks block new
recorded operations until explicit discard, and failed checkpoint writes cut
outputs. A sudden outage can require repeating up to thirty seconds since the
last successful checkpoint; elapsed time during an outage is not counted as
brewing time.

Stop latches inhibition before returning, disables heater GPIO/PID/countdown,
cancels queued starts and sends a direct pump cutoff. The guard also covers
later native GPIO/MSP writes. Filter removal while required latches the same
stop. A short sensor reply, invalid read byte or failed sensor write cuts
outputs before returning from the acquisition boundary. MSP transactions have
a 50 ms SDK timeout and a sticky I/O fault; an explicit pump cutoff remains
allowed after a fault. Clearing a stop requires the native idle home state
and healthy controller/filter prerequisites and never resumes a session.

## Network, keys and storage

Use the physical UART provisioning interface described in
[`../../common/docs/provisioning.md`](../../common/docs/provisioning.md).
It accepts `wifi <SSID><TAB><password>`, `key <64 lowercase hex>` and `status`.
There is no default API key. Input is not echoed or sent through the OEM serial
command logger. Protected API routes remain unavailable until a valid key is
provisioned. Health, the static website and the small machine overview are public.

Open **Settings → About → Pair with Hub** to authorize the Hub or the device
website. Compare the eight characters on both screens, turn the physical
dial to **Approve**, then press it. The first action is **Cancel**. A request
expires after 90 seconds and a successful approval can be claimed once.
**Machine details** in the same menu opens the original About screen.

The modified network record supports a full 32-byte SSID and 64-hex key, uses
verified storage, and configures station credentials in RAM. Existing OEM
credentials are imported only when the modified network record is definitely
missing; an unreadable modified record is not overwritten by that migration.
The checked startup preserves NVS on initialization failure. Runtime SPIFFS
mounting never formats an unreadable partition.

The modified API transport is Wi-Fi. Original W5500 Ethernet and remote cloud
protocols remain in the OEM reconstruction; the modified settings menu does
not advertise Ethernet/cloud configuration. Local history retains 4,224 samples
across eight sessions, with named sensor/actuator schemas and explicit gaps,
truncation and interrupted sessions. Optional Hub telemetry defaults off and
uses bounded asynchronous networking.

## Build, package and verify

Use Linux x86-64, Python 3.12, Make and a native C/C++ compiler. From the
repository root:

```sh
python -m pip install -r firmware/common/requirements-build.txt
make firmware-bootstrap MODEL=z-series
make firmware-test MODEL=z-series
make firmware MODEL=z-series
make firmware-package MODEL=z-series
make verify-imports
```

`TOOLCHAIN_DIR`, `ARDUINO_DIR`, `ARDUINOJSON_DIR`, `ETHERNET2_DIR` and
`ESP8266_SDK_DIR` can select already acquired pinned references. The last SDK
provides the checked SPIFFS formatter source. `OUTPUT_DIR` and `PACKAGE_DIR`
select generated output directories. The default application output is
`build/reference/`, including `application.bin`, `link/application.elf`, its
map and `standalone-build-report.json`. The package uses `build/package/`.

The [1.0.0 release package](releases/z-series/1.0.0.zip) includes the 4 MiB
partition map for factory recovery, the standalone application and its
filesystem. For installation, use the Hub's USB setup on the individual
module. It reads the existing layout, verifies the active firmware and
checks that the module's data can be preserved before writing.

The native integration suite executes the actual recovered Core/Head/runtime
and shared Engine with explicit hardware/storage boundaries. It covers recipe
ownership and pre-start persistence, real PID stop/resume expansion, physical
input ordering, preferences, coherent grayscale pages, acquisition faults,
latched cutoff, checkpoint corruption/discard and restart recovery. Separate
tests cover the production bounded MSP transport. Target linking checks heater
and encoder interrupt dependencies in internal code and the active owner in
internal RAM. Compiler stack estimates and object sizes do not replace physical
heap/stack high-water measurements or brewing qualification.

The [1.0.0 package](releases/z-series/1.0.0.zip) includes the release build report, both ELFs, and the application and recovery images. The [earlier standalone link report](analysis/standalone-build.json) records historical source identities and target memory measurements. The [reference-package validation](../../common/analysis/reference-package-validation.json)
binds the Z, PicoStill and PicoFerm packages to their actual ELFs and build
reports, including deterministic rebuilds, filesystem readback and rejection
of corrupted images, changed addresses and stale build inputs.

## USB identification and stock restoration

Connect one serviceable ESP32 module at a time. A Z1–Z4 stack count does not
identify a firmware build. Use the [ESP USB procedure](../../common/docs/esp-usb.md)
to capture and verify the entire flash of each module. Qualification reads the
classic ESP32 bootloader, partition table, factory/OTA selection records and
compares the selected application to the archived Z-series 0.0.116 or 0.0.119
image. A stock application alone omits the original partition table,
per-device data and inactive slots.

Generic reference setup requires the module's active stock 0.0.119 application
and a flash layout that preserves its NVS. The SDK geometry and initial
filesystem are documented in the [reference package](../../common/docs/reference-packages.md).
Readback and package checks do not qualify the machine's physical operation.
A full same-module restore replays the captured ESP flash. The separate MSP
controller uses the production image embedded in stock 0.0.119. At startup
the controller owner checks its version and may update it via I²C. The ESP
backup does not contain a readback of auxiliary MSP flash. See the
[MSP reconstruction](../reverse-engineered/analysis/msp-protocol.md).
