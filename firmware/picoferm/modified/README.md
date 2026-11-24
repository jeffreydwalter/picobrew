# PicoFerm standalone firmware 1.0.0

This profile contains the complete PicoFerm application owner, sensor acquisition,
local controls, authenticated API, embedded website, persistent recording and
optional Hub forwarding. It builds and links against the pinned ESP8266 Arduino
2.4.1 SDK and its original startup. The OEM archive and reconstruction remain
unchanged in their adjacent directories.

The [1.0.0 package](releases/picoferm/1.0.0.zip) contains both bank images, their hashes and the permanent bootstrap. The [1.0.0 memory replay](analysis/memory-ab-validation.json) records the tested 32-bit heap budget. Physical installation and battery-life trials remain pending. The [native controller tests](analysis/validation.json) and [1.2.0 single-bank link report](analysis/esp8266-build.json) document earlier engineering work.

The local website and authenticated status remind you to sanitize the gray seal before fitting it. With PicoFerm idle, acknowledge the reminder after sanitizing. The next monitoring session renews it.

The 1.0.0 release uses a USB-provisioned loader and two application banks. The Hub writes wireless updates to the inactive bank and confirms the new boot. An unconfirmed trial returns to the previous bank. [Wireless updates and recovery](dual-bank-updates.md)

## Operating model

| Feature | Implemented behavior |
| --- | --- |
| Identity | `picoferm` / `PicoFerm`, firmware `1.0.0`, API `/api/v1` |
| Sensors | Original temperature conversion, calibrated native pressure scale, battery voltage |
| Local recording | Explicit start/stop; no automatic session resume after restart |
| Sampling | Default 300 seconds; configurable integer 60–86400 seconds while idle |
| History | Shared 4224-record pool and eight sessions, named readings, persisted session cadence, paged export |
| Calibration | Explicit confirmation, fresh acquisition, validated offset and verified storage |
| Button/LEDs | Original GPIO/latch setup and animation definitions with clock-rollover repairs |
| Website | Embedded shared assets; no external JavaScript, fonts or cloud account |
| Network | Asynchronous Wi-Fi station connection, authenticated HTTP, optional asynchronous Hub forwarding |
| Provisioning | Physical UART commands for Wi-Fi and the device API key, with no secret echo |

PicoFerm has no heater, pump, fan, recipe engine, display or encoder. Those
capabilities and routes are absent. The local browser and Hub use advertised
capabilities to show fermentation controls and the actual sensor channels.

Adding the PicoFerm in the Hub provisions a receiver through its authenticated API.
It saves the host and port in flash and posts readings there after reboot. Check
**Settings → Hub connection** on its local site for the destination and delivery status.
The Hub can also recover a changed receiver address. [Pairing and reconnecting](../../../server/docs/device-connections.md)

This always-awake standalone profile keeps the LAN interface available. It does
not use the OEM cloud exchange, deep-sleep schedule or automatic recording
resume. Its power consumption and battery runtime therefore require physical
measurement; OEM battery-life expectations do not transfer to this profile.

A paired Hub can open the device website with a single-use ticket that expires
after 30 seconds. Direct visits can show public status or accept a saved
device key. Initial enrollment uses the device's USB credentials. This
model has no display for comparing a pairing code.

## Provisioning and local operation

The application uses the original UART at 115200 baud. After a qualified
installation with a mounted standalone filesystem, supply these commands through
the physical console. `<TAB>` below means one actual tab character.

```text
status
key <64 lowercase hexadecimal characters>
wifi <SSID><TAB><password>
```

Generate a fresh 32-byte random key on the provisioning computer, for example
with `python3 -c 'import secrets; print(secrets.token_hex(32))'`. Store it privately;
the firmware never echoes it. The SSID accepts up to 32 bytes. A password may be
empty for an open network, 8–63 printable characters, or a 64-character
hexadecimal WPA key. Provisioning changes require an idle controller, persist
through checked writes and readback, and close an existing HTTP connection before
applying a new key or network.

On its first standalone boot, the application can import valid OEM Wi-Fi
credentials and pressure calibration into separate modified records. It reads
the original 236-byte EEPROM record without writing, sanitizing or migrating it.
An existing modified record always takes precedence. Corrupt or unreadable
modified data is reported as an error and is never silently replaced by OEM
defaults.

The UART prints the LAN address after Wi-Fi connects. Open that address and pair
with the device key. Browser assets and `/api/v1/health` are public; other API
routes require the 64-character bearer key. Missing provisioning keeps protected
routes unavailable. There is no default key, captive-portal dependency, cloud
account, automatic online update or external-network prerequisite.

The browser exposes these commands:

| Command | Arguments and behavior |
| --- | --- |
| `start` | Requires a verified calibration and sensor sample; reserves persistent history before recording starts |
| `stop` | Immediately ends local recording or cancels a pending calibration commit; available on sensor/storage faults |
| `sample` | Starts a bounded fresh acquisition, including explicit recovery after an ADC fault |
| `calibrate` | Requires `confirm:true` after removing PicoFerm and its GRAY seal and fully depressurizing; stores only a newly acquired valid offset |
| `set-sampling-interval` | `value` is an integer 60–86400 seconds; rejected during recording or acquisition |

A button hold of more than two seconds explicitly starts or stops recording.
It never silently calibrates. Missing calibration or unavailable persistent
history prevents a start; the event log reports the reason. The shared
`temperature_unit` preference changes presentation, while temperature remains
Fahrenheit in API readings and history metadata.

## Acquisition, faults and persistence

The checked I2C adapter uses the recovered SDA2/SCL14 wiring and address 42.
Initialization preserves the original ADC register sequence, 3.0 V reference,
sample rate and gain. Acquisition preserves both channel muxes, their 400 ms
settling delays, four discarded conversions and the fifth retained sample, and
100 ms conversion spacing. The original signed-24-bit and temperature conversion
remain in the execution path.

The modified state machine replaces unbounded OEM readiness loops with a 1 s
power deadline, 2 s calibration deadline and 1 s conversion-ready deadline.
Readiness polls occur at bounded intervals; each foreground call performs a
bounded amount of work. I2C short reads, failed transfers, invalid pressure,
nonfinite temperature or invalid battery readings produce an explicit fault.
Old measurements become unavailable after a fault. HTTP, button processing and
stop remain serviceable; another bounded acquisition can recover the sensor.

The pressure reading is the recovered `max(raw - offset, 0) * 10 / slope` scale.
Its physical unit has not been established. It is named `pressure_native` with
unit `raw`; normalized `pressure_kpa` remains null. The firmware does not invent
a PSI or kPa conversion. Valid pressure offsets are 34470–234000, and valid slopes
are 90000–150000 with default 110840. Battery voltage uses the original ADC17
conversion factor 0.00314.

The modified calibration/interval record has its own version and checksum under
the shared `/pb/` namespace. A new offset or interval is activated only after
write and exact readback. Invalid calibration measurements leave the previous
verified value intact; storage failures do not return success. Runtime mounting
never formats a missing or damaged filesystem. Clean-install filesystem creation
is a separate, explicit packaging operation.

Each recording session captures its configured sampling interval. Changing a
later session's interval does not relabel old history. Default 300-second sampling
gives approximately 14.7 days for 4224 rows before additional state-change rows;
the bounded pool may evict older sessions. A full session reports truncation.
The 32-bit elapsed-time limit also reports truncation rather than wrapping a
long recording back to zero. Restart retains interrupted history but leaves the
device idle. Optional Hub forwarding is off by default and never contains the
device API key.

## Build and verify

From the repository root:

```sh
python3 firmware/picoferm/modified/tests/test_standalone.py

ESP8266_ARDUINO=/path/to/esp8266-2.4.1 \
  python3 firmware/common/tests/test_esp8266_http.py

make firmware-bootstrap MODEL=picoferm
make firmware-package MODEL=picoferm
```

The native suite uses the actual controller and shared Engine, not substitute
command/history implementations. It checks acquisition ordering and faults,
calibration transactions, start reservation, restart behavior, cadence,
capabilities, preferences and button/LED clock rollover in optimized and
ASan/UBSan builds. The listener suite compiles against the pinned SDK declarations
and exercises raw lwIP callback ownership, partial receives/writes, backpressure,
peer half-close, errors, generation reuse and real authenticated HTTP parsing.

The build links both bank images against the pinned SDK, verifies their segment addresses and entry points against their ELFs, and packages the bootstrap, both images and a readback-verified empty filesystem in `build/package/picoferm.zip`. Link maps and manifests are in `build/ab/`.

### Finite RAM verification

`analysis/memory-ab-validation.json` binds the actual target ELF and pinned compiler
size probes to a 32-bit execution of the real controller, Engine, provisioning,
verified transaction layer, all five pinned SPIFFS C sources and the original
SDK UMM allocator. The current shared-engine build has **27,480 bytes** between
`_heap_start` and the SDK heap end. The 6,032-byte application owner is already in
static RAM; it includes the 4,936-byte HTTP server and its 4,096-byte arena. The
SDK's 4 KiB continuation stack is also static, so neither is counted twice as a
heap allocation.

| Dynamic allocation | Pinned target bytes |
| --- | ---: |
| Engine state, with no unsupported recipe catalogue | 2,656 |
| Active 17-field descriptor array plus layout owner | 1,258 + 8 |
| Largest Ferm chunk read/write workspace | 1,644 |
| Full compact chunk record including checked header | 1,236 |
| SPIFFS owner, work buffer, descriptors and cache | 156 + 512 + 144 + 848 |
| HTTP and telemetry platform owners | 24 + 280 |

The replay holds **16 KiB of separate SDK/network reservation in sixteen
allocations** throughout initialization and nine recording sessions. It also
charges the omitted EEPROM/UART/platform allocations with the original
allocator's rounding and reserves the measured 136-byte difference between the
native and target Engine ABI. It uses a maximum-length forwarding hostname,
extreme valid 24-bit ADC readings, clock rollover, a stalled transmission, active
sample pages and retained pages while another session records. All **14,238
allocations** succeed; the peak is **27,216 bytes**, leaving **264 bytes** within the modeled budget.
The largest serialized forwarding frame in that replay is 1,200 bytes. Every
allocation returns to the allocator on teardown, apart from its fixed 8-byte
sentinel. Real filesystem calls also verify the actual `ferm-cal` key; a
map-backed calibration test alone would not establish that integration.

This is a concrete finite-heap execution with an explicit SDK/network budget.
The reserved bytes do not execute the private Wi-Fi driver or establish a
physical heap/stack high-water measurement under every network burst. Allocation
and storage errors still take their checked failure paths. The memory test can
run directly in Linux CI with `gcc-multilib` and `g++-multilib` installed:

```sh
python tools/validate_memory.py --ab --build-dir build/ab --toolchain /path/to/xtensa-lx106-elf \
  --arduino /path/to/esp8266-2.4.1 --write-report
```

Run the complete target build first, or supply its directory with `--build-dir`.
The test requires 32-bit pointers and bounds execution to 120 seconds. The
optional `--i386` argument selects the repository-pinned extracted multilib and
qemu runtime when the host cannot execute a native 32-bit program.

The unmodified `eagle.flash.2m.ld` is a reference corresponding to the OEM image
header; it does not establish the flash geometry or data layout of an individual
unit. The image is for the explicit 2-MiB reference profile; common packaging
supplies its ready-to-mount empty filesystem and exact component manifest without
flashing a device. The [USB service procedure](../../common/docs/esp-usb.md)
checks the physical flash and OEM image before installing or restoring an individual
unit. Physical acceptance still includes layout/backup verification, filesystem
mounting, sensor/calibration accuracy, button/LED operation, Wi-Fi reconnection,
power interruption, heap/stack high-water measurements and sustained recording.
