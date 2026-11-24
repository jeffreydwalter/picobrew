# Guarded Pico S/Pro writes from macOS

`tools/pico_flash_install.py` provides separate key provisioning, application
installation and stock restoration. The historical fixed profile covers the owner-identified Pico S whose backup was labeled Pico Pro. The Hub can also enroll individual stock S/Pro brewers by qualifying their own backups. Both model labels use the same known OEM application. Select the label on the brewer when choosing the standalone image. See the [tested Pico S procedure](hub-ota.md).

The Pico S application and recovery passed USB installation and readback. Physical brewing and interlock qualification remain pending.
Stock restoration and interrupted-write recovery remain physically unverified.
Successful readback alone does not establish safe boot or output behavior.

| Command | Files it may write | Purpose |
| --- | --- | --- |
| `provision-key` | `/local/api.key` | Create/read back the required key, then rewrite/read back the same key using existing-file mode |
| `install` | `/local/api.key`, the five `/local/web/*.gz` files, `/sys/mcuimg2.bin` | Verify the key, install/read back the pinned browser bundle, then install/read back the pinned application |
| `install-recovery` | `/sys/mcuimg1.bin` | Install and read back the Hub recovery application on the pinned profile after main installation. See [OTA setup](hub-ota.md). |
| `restore` | `/sys/mcuimg2.bin` | Restore and read back the exact stock application extracted from the verified original backup |
| `restore-stock` | `/sys/mcuimg2.bin`, `/sys/mcuimg1.bin` | Return the profiled brewer to its original application and recovery, then verify every firmware slot |

The writer never writes the secondary bootloader, boot
record, service pack or Wi-Fi configuration. It does not format flash,
delete files, move allocations, perform raw flash writes, or automatically
start the application. A saved preflight report cannot bypass its current
session checks.

## Required local files

**Enrolling another Pico S or Pro.** Capture a complete 1 MiB stock backup with the two-pass USB reader. Use `tools/pico_flash_install.py inspect-stock --model pico-s --backup /absolute/path/to/stock.bin` to verify its identity file, filesystem allocations, bootloader, boot selection, recovery and original application offline. Use `pico-pro` for a Pico Pro. The report contains digests used to match this brewer’s private backup.

Pass `--stock-profile --model pico-s --backup /absolute/path/to/stock.bin` (or `pico-pro`) to `prepare-install`, `provision-key`, `install`, `install-recovery` and `restore-stock`. The tool checks the live identity and named allocations against the qualified backup at every USB stage. It reads both bootloader and boot-record copies and requires the logical boot selection to point to the main application. A partially erased table, an unexpected third slot, or conflicting file allocations stops the operation. It accepts different block addresses but requires the supported flash size, slot capacities, protection flags and boot format. Keep the original backup for subsequent restoration. Per-unit setup and restoration have been tested with simulated USB traffic.

**Historical fixed profile.** The examples below omit `--stock-profile` and require the exact previously verified backup. Only this historical brewer can also use the public pair of raw OEM executables via `--oem-directory`. Keep all private backups and installation reports out of shared logs and source control.

The examples use a placeholder checkout and private backup path. Set them to
your actual locations and keep the same API key throughout provisioning and
upgrades. Create the component environment if needed:

```sh
PICO_CHECKOUT="/path/to/picobrew"
PICO_ROOT="$PICO_CHECKOUT/firmware/pico-s-pro/modified"
cd "$PICO_ROOT"
python3 -m venv .venv
"$PICO_ROOT/.venv/bin/python" -m pip install -r requirements-usb.txt
mkdir -p "$PICO_CHECKOUT/firmware/backups"
```

The writer imports the repository's probe, queued-backup, preflight,
offline-inspection and private-file modules. The latter use this component's
`tools/pico_private_files.py` and the repository root's `tools/private_files.py`;
both must accompany any standalone copies of the scripts. Run the writer from
the complete checkout. The initial prompt describes both power-on and reset-button entry.

To check a custom installation offline, run `tools/pico_flash_install.py prepare-install
--model pico-s --backup /absolute/path/to/original-serial-flash.bin` with the
component's Python environment. It verifies the local backup, target firmware,
browser files, recovery image and USB helper. The three installation commands
accept `--verify-backup-uid` to require a match between the live brewer and
the original backup before each operation. `restore-stock` always performs
that identity check.

## Enter a fresh programming session

The tool holds BREAK while waiting for the CC3200 to restart. It neither
resets the board automatically nor resumes the RAM helper left by a
previous command. TI's [SWRU577, section 5.3.1](https://www.ti.com/lit/ug/swru577/swru577.pdf)
explicitly permits powering on the device **or resetting it if already
running**, after BREAK is asserted.

Two entry procedures are available:

- **Power-on procedure:** with the Pico's mains disconnected and USB
  connected, start the command. On the initial `BREAK asserted` message,
  reconnect mains and turn the Pico on.
- **Reset-button alternative:** leave mains and USB connected, start the
  command, and press and release the board's blue RESET button once when
  the initial `BREAK asserted` message appears. This follows TI's reset
  sequence; verify the board connection and do not assume every attached
  adapter supports identical reset behavior.

At the **initial** `BREAK asserted` prompt, either power on the Pico or press
and release its blue RESET button once. Pressing the reset button does not
require unplugging mains. Do not press Return. Once the handshake begins,
do not reset again or disconnect power/USB until the command exits. The later
`APPS HANDSHAKE` is handled by the tool.

## Stage 1: provision and verify the API key

Update the checkout and create the local key only
if it does not already exist:

```sh
PICO_CHECKOUT="/path/to/picobrew"
PICO_ROOT="$PICO_CHECKOUT/firmware/pico-s-pro/modified"
cd "$PICO_ROOT"
git pull --ff-only origin master

if [ ! -e "$PICO_CHECKOUT/private/api.key" ]; then
    "$PICO_ROOT/.venv/bin/python" \
        "$PICO_ROOT/tools/provision_api_key.py" \
        --output "$PICO_CHECKOUT/private/api.key"
fi
```

The key generator writes exactly 64 lowercase hexadecimal characters plus
LF with mode `0600`. The writer requires that format in a private regular
file and never prints its contents, hash or file-access token. An existing
matching device key is reused and verified; a different key causes an
abort. Do not generate a new key to work around a mismatch.

Choose either entry procedure above and run:

```sh
PICO_CHECKOUT="/path/to/picobrew"
PICO_ROOT="$PICO_CHECKOUT/firmware/pico-s-pro/modified"
PICO_BACKUP="$PICO_CHECKOUT/firmware/backups/original-serial-flash.bin"
PICO_REPORT="$PICO_CHECKOUT/firmware/backups/pico-pro-key-$(date +%Y%m%d-%H%M%S).json"

sudo "$PICO_ROOT/.venv/bin/python" \
    "$PICO_ROOT/tools/pico_flash_install.py" provision-key \
    --backup "$PICO_BACKUP" \
    --key-file "$PICO_CHECKOUT/private/api.key" \
    --output "$PICO_REPORT"
```

On the initial **`BREAK asserted`**, perform the chosen power-on or
reset-button action. Do not wait for a Return prompt.

The tool checks the live protected images, original application, boot
record, flash geometry and allocation metadata. If the key is absent,
it also requires unambiguous absence from validated FAT/name metadata
before creating it. A logical “absent” flag by itself is insufficient.
It then verifies the key, reopens it for existing-file write, rewrites
the identical bytes and verifies them again. Before reporting success,
it rereads the original application and protected state.

Expected final marker:

```text
SUCCESS: API key provisioning and existing-file write verification completed
```

Review the completed checks before proceeding to Stage 2.
The original application remains selected throughout Stage 1. Each subsequent
command needs a fresh programming session using the procedure above.

## Stage 2: install the pinned standalone candidate

The commands below install the **stable 1.0.0 Pico Pro update**. The installer recognizes earlier standalone development builds by their exact model, byte length and digest, including the original shared 1.0.0. Their bytes must match an allowed source exactly.

Proceed with the existing provisioned key. Do not rerun key provisioning after
installing standalone firmware; that separate stage intentionally requires the
original application. The exact new image size/hash and matching web manifest
are recorded in `firmware/standalone/pico-pro/firmware-report.json` and pinned in the
installer. The previously installed image's identity remains separately
allowlisted as an upgrade source.

The checked-out `firmware/web/manifest.json` and all five gzip payloads are
required alongside the application. They are validated before USB is opened.
The default bundle path is resolved from the modified firmware directory, so the install command
below does not need an extra browser-files argument. The installer verifies
live FAT/free-hole constraints, key identity, all browser file readbacks, then
the application and protected-file postchecks. It leaves local recipes,
histories, preferences and forwarding configuration in place.

Unknown or partial application content requires the explicit restore path;
normal installation does not overwrite an unknown image. A different build,
even from this branch, requires a reviewed tool/profile update.

Update this branch first:

```sh
PICO_CHECKOUT="/path/to/picobrew"
PICO_ROOT="$PICO_CHECKOUT/firmware/pico-s-pro/modified"
cd "$PICO_ROOT"
git pull --ff-only origin master
"$PICO_ROOT/.venv/bin/python" -m pip install -r requirements-usb.txt
```

With either programming-entry procedure above:

```sh
PICO_CHECKOUT="/path/to/picobrew"
PICO_ROOT="$PICO_CHECKOUT/firmware/pico-s-pro/modified"
PICO_REPORT="$PICO_CHECKOUT/firmware/backups/pico-pro-install-$(date +%Y%m%d-%H%M%S).json"

sudo "$PICO_ROOT/.venv/bin/python" \
    "$PICO_ROOT/tools/pico_flash_install.py" install \
    --backup "$PICO_CHECKOUT/firmware/backups/original-serial-flash.bin" \
    --key-file "$PICO_CHECKOUT/private/api.key" \
    --firmware "$PICO_ROOT/firmware/standalone/pico-pro/pico-pro-standalone.bin" \
    --output "$PICO_REPORT"
```

The writer first proves existing-file writing on the matching key in this
session, then installs and verifies every browser file. Only then does it open
the existing image 2 with mode `0x1000`, retaining its allocation and attributes,
and write sequentially. Opening for write
**erases the file's contents immediately**. It is not an atomic update.

The pinned installation profile requires an existing application allocation
of 48 nonmirrored 4 KiB blocks. The writer
additionally caps application payloads at 188,416 bytes, conservatively below
the capacity derived from that allocation and the SDK's size-rounding rules.
This is a derived guard, not a measured `AllocatedLen` value. The packaged
1.0.0 Pico Pro image passes it; no application-file recreation or relocation is used.
Every write requires an acknowledged command and successful loader status;
the tool does not retry an ambiguous write chunk.

After closing, it rereads every application byte, compares with the local
candidate, verifies the key, and rechecks the protected state. If the
candidate was already installed, it skips the application rewrite while
still verifying the key and final state. Expected marker:

```text
SUCCESS: standalone firmware, browser files and API key verified
```

The tool leaves the Pico in the programming session. First standalone
startup, display/controls, network access and output behavior are separate
physical validation steps described in [hardware validation](hardware-validation.md).
Keep USB connected and review the installation result before attempting
that first startup. During the application write, keep mains and USB
connected until the command has completed.

## First standalone startup

After `SUCCESS: standalone firmware, browser files and API key verified` and return to
the shell, the RAM helper is still the last program the installer started.
USB cleanup releases BREAK and returns the adapter to the macOS driver;
it does not start the installed application or verify the header's boot
signals. A normal boot with USB still attached has not been established
for this six-pin arrangement.

For the first startup, leave the Pico's mains connected, disconnect only
the USB-C cable from the SparkFun adapter, leave the six-pin header in
place, and press and release the board's blue RESET button once. This
avoids relying on an attached adapter for normal boot. Verify the actual
board connection before proceeding; this does not qualify every possible
attached-adapter or reset configuration.

Observe the display before attempting any brewing or maintenance program.
Record a blank screen, reset loop or error exactly as shown. Firmware
startup, network access and physical output behavior remain separate
checks in [hardware validation](hardware-validation.md).

The expected code path shows the Pico splash, attempts the saved Wi-Fi
connection, and reaches the regular menu. Standalone 1.0.0 shows `v1.0.0`,
using the shared version in `include/picobrew/version.h`. If an old-session
resume prompt appears, choose Abort for this initial observation.

On the front panel, open **Settings > Network > Status** and read the IP
address. If a Wi-Fi connection is needed, use **Settings > Network >
Connect to WiFi**. The firmware does not automatically configure a new
fallback access point. From a browser on the same network, open:

```text
http://<pico-ip>/api/v1/health
```

After initialization, the 1.0.0 read-only public endpoint should include
`"firmware_version":"1.0.0"`, `"model":"pico-pro"`, `"api_version":1`,
`"mode":"standalone"`,
`"ready":true`, and `"auth_configured":true`. The response's `uptime_ms`
varies. This is the running-firmware identity check; it requires no API key.
The browser console is at `http://<pico-ip>/`. Record the actual response
before starting a program.

### Wi-Fi reconnection in stable 1.0.0

Standalone 1.0.0 includes the reconnect fix in
`platform/energia_network.cpp`: it disconnects the old association, clears
the cached connection and address state, enables DHCP explicitly, and
requests the selected network. Connection success requires a fresh IPv4
DHCP event, and old-network HTTP sockets are closed before reconnecting.
The intended behavior is to obtain the new network's address without a
manual reboot. The native Wi-Fi tests cover this flow; its behavior on the
target appliance still needs physical verification for the installed release.

After changing networks, wait for the connection attempt to complete,
check **Settings > Network > Status**, and request `/api/v1/health` at the
new address. Confirm `"firmware_version":"1.0.0"` after this upgrade and compare the address
with the router's client information. The displayed saved SSID alone does
not establish that the Pico joined that network or acquired a valid lease.

## Browser authentication and observation

The public health endpoint does not verify a bearer-authenticated request.
Open `http://<pico-ip>/` in a browser. The initial **Connect to your brewer.**
dialog has a **Device API key** field and **Connect** button. On macOS, copy
the existing key directly to the clipboard without printing it:

```sh
tr -d '\r\n' < "/path/to/picobrew/private/api.key" | pbcopy
```

Paste into **Device API key** and click **Connect**. Successful protected
reads should change the indicator to **Connected** and populate Overview
with temperatures, target, step time, filter state and the live OLED.
Compare the OLED image with the physical screen and record the actual
readings before starting a program. The initial login and polling use
GET requests; they do not start a brew or maintenance cycle. The key is
stored in that browser tab's session; Disconnect removes it from the tab.

Visible readings do not establish control operation, sensor calibration,
interlocks or safe thermal behavior. Check those separately before brewing.

## Restore the original application

The stock restore source is the verified full backup, not a freshly
downloaded image or the cloud-dependent recovery application. The writer
extracts exactly 162,792 original bytes with SHA-256
`e18c240fa1cf35222674bf3b4fa992a306c6e63b0b99be8793604f199085d702`.

Start a fresh programming session with either entry procedure above:

```sh
PICO_CHECKOUT="/path/to/picobrew"
PICO_ROOT="$PICO_CHECKOUT/firmware/pico-s-pro/modified"
PICO_REPORT="$PICO_CHECKOUT/firmware/backups/pico-pro-restore-$(date +%Y%m%d-%H%M%S).json"

sudo "$PICO_ROOT/.venv/bin/python" \
    "$PICO_ROOT/tools/pico_flash_install.py" restore \
    --backup "$PICO_CHECKOUT/firmware/backups/original-serial-flash.bin" \
    --output "$PICO_REPORT"
```

Restore checks the protected images, logical boot record and named image 2
allocation. It does not require the damaged application's contents to be
readable or hash-valid. It attempts only an existing-file write, then
requires complete stock readback and protected-state checks. It leaves
the API key and all other files alone. Expected marker:

```text
SUCCESS: original firmware restored and read back exactly
```

If the filesystem cannot reopen the existing image 2, restore stops.
The loader's coarse file-info “absent” result cannot distinguish every
corrupt/interrupted state; it does not authorize deletion, recreation,
formatting or raw writes. **This bounded restore command has not yet been
tested on the physical Pico, and cannot guarantee recovery from every
interrupted write.** Do not use Factory Reset as an offline restore method.

## Restore all OEM firmware slots

Use this procedure to return the profiled Pico S to the original PicoBrew
application and recovery. Both images were verified against the same
original full-flash backup. The backup historically carries a Pico Pro label.
This public-image fallback is restricted to that brewer. For another supported unit, use its original full backup with the per-brewer profile options above.

After the restore, the brewer runs the OEM software. Custom Hub controls,
the standalone device website and wireless firmware updates become unavailable.
Reinstalling custom firmware requires USB. The tool leaves personal files
(including Wi-Fi settings and the custom device key) on the brewer.

With the brewer idle, follow the USB setup above. From the
`firmware/pico-s-pro/modified` directory, use the original images from
`../oem/device` and a new absolute report path in an existing private directory.

```sh
sudo .venv/bin/python tools/pico_flash_install.py restore-stock \
    --oem-directory ../oem/device \
    --output /absolute/path/to/new-restore-report.json
```

Alternatively, replace `--oem-directory ../oem/device` with
`--backup /absolute/path/to/original-serial-flash.bin` to use your original backup.
The tool checks both raw images against their pinned hashes or verifies the
complete backup before opening USB.

At the initial `BREAK asserted` prompt, power on the brewer or press and
release RESET once. Leave power and USB connected through verification.
Before writing, the tool compares the live brewer identity to the verified profile,
checks the flash layout, verifies the OEM bootloader and the active boot record,
and confirms slot 3 is absent. It restores slot 2 (the main application)
before slot 1 (recovery), reads both back, and skips slots that already
contain the correct bytes. It verifies the bootloader, boot record and absent
slot 3 again before reporting success.

```text
SUCCESS: OEM bootloader, boot selection, recovery and application verified; image3 absent
```

After success, restart the brewer and check its startup screen. If a write
is interrupted, keep the backup and the report. Start a fresh programming
session with a new report path. A retry requires the original allocations
and a readable bootloader and boot record. Restoring a damaged application
also requires an intact recovery slot. Restoring damaged recovery requires
the verified OEM application in slot 2. Tests cover these cases with a
simulated USB device. A complete OEM restore and subsequent boot still
await a physical trial.

## Reports and failures

Each command requires a new absolute `--output` path. It reserves a
private report and an append-only journal before opening USB, records
intent before destructive opens, and records completed operations after
acknowledgment/status checks. The report and adjacent `.journal.jsonl`
contain no key contents, key digest or file-access token. Failed runs
retain their evidence; files are never silently overwritten.

On any transport failure, unexpected response, different protected hash
or readback mismatch, the tool stops without automatically booting or
resending an uncertain write. Keep the terminal output and report. After
an application-write failure, remain in the programming workflow until
the application has been successfully verified or restored.

The original backup, probe and preflight remain usable independently.
`make usb-test` runs synthetic device/protocol tests without physical USB;
those tests include immediate erase on existing-write open and failures
after a command may already have taken effect.

### APPS handshake diagnostics

Handshake diagnostics distinguish a silent timeout, NACK and unexpected-byte
limit without storing received payload. A failure before helper execution or
filesystem access must not be described as an application write.

A timeout with no bytes, for example, is reported as:

```text
STOPPED during RAM loader setup: APPS INITIAL ACK: timed out; received 0 bytes; wait limit 5s; elapsed ...s
No Pico flash or file write was attempted
```

This is an example diagnostic, not evidence of a particular hardware cause. The report's `error.handshake` field distinguishes
timeout, NACK and the 1,024-byte bound without storing received payload.
It also records `helper_uploaded_bytes`, `helper_executed` and
`persistent_write_attempted`. No automatic retry or connection-setting
change was added. Use a new report path and the same host key for a fresh
attempt; the prior report and journal remain evidence.

## Protocol references

The wire framing and helper come from the pinned
[`cc3200tool` revision](https://github.com/toniebox-reverse-engineering/cc3200tool/blob/c2332898a1ededc0bdd01ec279b23366d213f81e/cc3200tool/cc.py).
The writer does not call its generic `write_file()` helper, which deletes
and recreates existing files.
TI's [SWRU368C, chapter 13](https://www.ti.com/lit/ug/swru368c/swru368c.pdf)
documents whole-file erase on open-for-write and fixed allocation.
The [CC3100/CC3200 filesystem API](https://software-dl.ti.com/ecs/cc31xx/APIs/public/cc31xx_simplelink/latest/html/group___file_system.html)
defines distinct read, existing-write and creation modes. See
[SWRA657](https://www.ti.com/lit/an/swra657/swra657.pdf) for allocation and
serial-flash power-interruption limitations.
