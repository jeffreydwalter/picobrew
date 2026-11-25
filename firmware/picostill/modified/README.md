# PicoStill standalone firmware 1.0.0

This is a complete, linked standalone application for the recovered PicoStill
ESP32 board. It operates the original four thermistor channels, pressure
sensor, vacuum pump, two fans, three LEDs and active-low button. Its versioned
local API, embedded browser, authentication, durable history, preferences,
events and optional Hub telemetry share the portable implementation in
[`firmware/common`](../../common/README.md). Local operation requires no OEM
website, cloud account, DNS response or Hub connection.

The original firmware images and pre-custom reconstruction remain unchanged
under `../oem` and `../reverse-engineered`. This modified application uses the
recovered calculations, calibration constants, GPIO/I2C board driver and native
controller. The copied `src/hardware.cpp` and `src/controller.cpp` make the
foreground sensor, tachometer, LED and overtemperature deadlines safe across
the 32-bit millisecond rollover. Other native controller behavior remains
intact. Unsupported diagnostic programs and original cloud/update listeners
are not exposed by the standalone API.

After a distillation program is used, the local website and authenticated status show a condenser-cleaning reminder. After cleaning the removable parts, acknowledge the reminder while PicoStill is idle.

## Paired operation with a Z

After pairing in the Hub or the Z’s local site, start the manual or boil-point
program on the Z and follow its setup screens. The PicoStill keeps its own
native pump, cooling and sensor interlocks. Its local button and **Stop**
command remain available throughout the session. Operate the accessory's
standalone cleaning program while the paired session is stopped.

The authenticated `/api/v1/brewer-link` endpoint verifies the target UID
and grants one brewer an exclusive session. The brewer must renew it within
6.5 seconds. Pressing the accessory button releases the session and cuts the
outputs, including during setup. The Z then stops when it detects the lost
session. A new session requires a fresh claim. See the
[Z pairing and qualification notes](../../z-series/modified/README.md#using-a-picostill-with-the-z).

## Wireless updates

After USB setup provisions wireless recovery, the Hub can update an idle PicoStill over its local network. Factory recovery starts with pump and fan power off. It reads the saved device key and Wi-Fi settings, checks the package identity, and writes the application and its embedded website together. Settings, history and the factory recovery copy occupy separate flash regions.

The Hub verifies the running machine’s ID, version, image length and SHA-256 after restart. A lost connection can resume the acknowledged byte offset during the same recovery boot. A power interruption during transfer leaves factory recovery selected and the Hub restarts the upload. A valid image that fails after boot may require USB service. Physical installation and power-cut trials remain outstanding.

A previously converted PicoStill needs a single USB migration using its saved original and existing Hub key. The installer verifies the old application and retains the entire live filesystem and NVS. The authenticated update endpoint is `/api/v1/firmware/ota` for the `esp32-factory-v1` protocol. See the [USB service procedure](../../common/docs/esp-usb.md).

## Applicable S/Pro parity

| Feature | PicoStill behavior |
| --- | --- |
| Local browser and API | Embedded capability-driven browser and `/api/v1`; bounded common HTTP transport on port 80 |
| Authentication | Explicit 64-character lowercase hexadecimal API key; no default key; the common transport checks every protected request |
| Persistent preferences | Display temperature unit F/C, checked and committed while idle |
| Program controls | `start` (native manual), `essential-oils`, `clean` (native requested cleaning), `stop` |
| Actuator controls | `set-pump` with `enabled`; `set-fan` with `channel` 1/2 and `level` 0–100, optional `enabled` |
| Status and readings | All four temperatures, pressure, pump state, both fan levels and measured RPM |
| History | Up to 4,224 samples across eight retained sessions; per-session channel metadata, paged reads, restart interruption state and explicit storage/truncation flags |
| Events and Hub | Local events and optional asynchronous telemetry; disabled telemetry does not open an outbound connection |
| Physical operation | Immediate stop and an idle selection menu using the same persistent reservation and sensor interlocks |

The board has no heater, brewing recipe interpreter, display or encoder.
Discovery reports those capabilities as absent, the browser omits their
controls, and unsupported API routes reject requests. The optional Hub supports
this accessory without treating it as a brewer.

A paired Hub can open the device website with a single-use ticket that expires
after 30 seconds. Direct visits can show public status or accept a saved
device key. Initial enrollment uses the device's USB credentials. This
model has no display for comparing a pairing code.

## Units and native interlocks

The OEM controller calculates temperatures in Celsius and its browser labels
pressure as psi. The common API exposes all four temperature channels in
Fahrenheit and pressure in kPa. `temperature_f` is the **pot** channel; the full
`sensors` array preserves `inlet`, `outlet`, `pot`, `ambient` and `pressure`
separately. Conversion is `C × 1.8 + 32` and `psi × 6.894757293168`.
Unsuccessful current reads are marked invalid instead of displaying a stale
number as a fresh reading.

Starting requires initialized hardware, a sufficiently large mounted
filesystem, valid in-range native sensor metrics, and a **verified persistent
session identity and schema before any output is enabled**. The pump requires
fan 2. A manual fan-2 command cannot turn it off or reduce it below 61% while
the pump runs, retaining the native tach-monitoring threshold. A manual pump-off
request is latched at the GPIO boundary, so a native state transition cannot
briefly energize it. Stop drives the physical pump, fan PWM and fan power low
before returning acceptance, including when a logical state is stale.

The native controller retains its outlet overtemperature holdoff, hysteresis,
pressure recovery condition, fan-speed and sensor-error checks, and 20-second
cleaning program. An unrecoverable fault ends recording and leaves outputs
inhibited. No interrupted program restarts automatically. Sensor monitoring
continues while idle and after a stop.

## Physical button

With a program running, pressing GPIO 13 stops it on the next foreground poll.
While idle, hold the button for five seconds to enter or leave program
selection. The menu initially selects manual operation (fast green). A single
click cycles to essential oils (fast yellow), then cleaning (fast red), then
manual again. A double-click starts the selected program. Selection alone does
not energize outputs, and a rejected start keeps the output gate closed.

## Provisioning and networking

Use the 115200-baud physical UART while idle. The shared parser never echoes
input and accepts these complete lines:

```
key <64 lowercase hexadecimal characters>
wifi <SSID><TAB><password>
status
```

`<TAB>` is one literal tab. A supplied network can have a 1–32-byte SSID and an
empty, 8–63-character WPA passphrase or 64-hex PSK. The SDK adapter uses checked
RAM configuration to support all 32 SSID bytes and avoid writing credentials
back to OEM NVS. Association is asynchronous. Once connected, the UART prints
the local browser address. This profile uses the configured local network; it
does not create an open setup access point.

First startup may read the original `eeprom/eeprom` NVS blob and import its
version-zero bounded credential record only if modified network state is
definitely absent. It never calls the original storage initializer, which can
rewrite NVS. A corrupt modified record blocks import. Explicit physical
provisioning can repair the requested modified value.

The shared ESP32 startup preserves the historical CPU/PSRAM/log/Bluetooth/init
sequence but returns an NVS initialization failure instead of erasing and
retrying. Such a failure inhibits the hardware controller. Filesystem mount
also never formats a blank or damaged partition. Use the separately prepared
reference filesystem image for a clean installation; runtime startup does not
invent or erase a layout.

## Build and evidence

The development compiler and SDK pins are inherited from
`../reverse-engineered/dependencies`. With those checked dependencies:

```sh
python firmware/picostill/modified/tools/build_esp32.py \
  --toolchain /path/to/xtensa-esp32-elf \
  --arduino /path/to/arduino-esp32-9a7946e6859130146e56231c27a4aa177d9175a1
python -m unittest discover -s firmware/picostill/modified/tests -v
```

The offline converter requires `pyserial==3.5` because its pinned module imports
pyserial even when no device operation is requested. The build links the
application, recovery service and pinned Arduino SDK, producing
`build/esp32/picostill-standalone.elf`, its map, `application.bin` and
`build-report.json`. Modified `app_main` owns an 8,192-byte appliance task and
calls checked startup; the SDK's erase-capable `initArduino` and original
cloud/update application symbols are absent.

The 4 MiB USB layout reserves 960 KiB each for factory recovery at `0x10000` and
the normal application at `0x100000`. It keeps the 2 MiB SPIFFS partition at
`0x200000` and NVS at `0x9000`. USB sets the initial boot selector at `0xe000`
to the normal application. The [USB service procedure](../../common/docs/esp-usb.md)
checks the installed layout and preserves a private same-unit flash backup before conversion. Flash capacity, wiring,
existing partition contents, controller behavior and runtime stack/heap
high-water require physical installation qualification before appliance use.

[`analysis/validation.json`](analysis/validation.json) binds the actual native
owner, controller, common Engine and test inputs to source hashes. ASan/UBSan
checks sensor conversions, start/clean/stop and fault transitions, persistence
failure before actuation, the physical menu, GPIO inhibition, fan/pump
interlocks and timer rollover including a deadline equal to zero.
The [1.0.0 package](releases/picostill/1.0.0.zip) contains the release image, ELF, build report and installation manifest. The [1.2.0 link report](analysis/esp32-build.json) records retained symbols and image checks from the earlier single-slot build. Shared evidence separately proves the full 4,224-record filesystem
capacity, storage transactions, bounded HTTP and telemetry, browser behavior
and credential provisioning.
