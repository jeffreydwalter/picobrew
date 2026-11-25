# Pico S/Pro standalone firmware and local API

The standalone profile moves recipe selection, session identity, preferences,
and control into the appliance. It serves a browser interface and a versioned
REST API on the local network, without a PicoBrew account or a replacement
PicoBrew server. The existing controller, display, encoder, and program
workflows remain the execution path.

Firmware **1.0.0** ships as separate Pico S and Pico Pro images.
The owner reported installing it on a Pico S. Physical brewing, interlock
behavior and power-loss recovery still require validation. See the
[hardware acceptance record](hardware-validation.md) and [Hub update procedure](hub-ota.md).

The complete machine-readable contract is [openapi.json](openapi.json).
Authentication setup is in the [key provisioning guide](standalone-provisioning.md).
The [optional telemetry guide](telemetry.md) covers forwarding live snapshots to
another computer, including a Python standard-library receiver and SQLite archive.

## Pair from the Hub or device website

Open the device website for its current temperatures and brewing state. To connect a
Hub or browser for the first time, choose **Pair**, then use the brewer's physical
dial to open **Settings → About → Pair with Hub**. Compare the eight characters
on both screens. Turn the dial to **Pair** and press it to approve. The default
selection is **Cancel**. A previously paired Hub can open a signed-in device
tab directly. **Forget key** disconnects that tab.

Pairing requires a device key provisioned during USB setup. A client creates a
fresh, cryptographically random 32-byte nonce, encodes it as 64 lowercase hex
characters and posts it as JSON to `/api/v1/pairing/request`. The pending request
expires after 90 seconds. Physical approval or an authenticated
`POST /api/v1/pairing/grant` makes the ticket claimable for 30 seconds.
`POST /api/v1/pairing/claim` returns the per-unit key once, only to that ticket's
holder. The device site accepts Hub tickets in a URL fragment, removes the
fragment on load and validates the redeemed credential using protected endpoints.
The brewer serves HTTP on the local network. Use a trusted LAN.

## Device identity

Stable firmware reports `model`, `model_name`, and `model_family` on
`GET /api/v1`, `GET /api/v1/health`, and `GET /api/v1/status`. The model is
`pico-pro` / `Pico Pro` or `pico-s` / `Pico S`; the shared family is always
`pico-s-pro`. It comes from the compiled image, not a guess from the UID.
Older shared images do not report an exact S/Pro variant. The Hub uses the
authenticated discovery response to identify the model automatically.

## Restart the brewer

When authenticated discovery reports `system_restart: true`, send an authenticated
`POST /api/v1/system/restart` with `Content-Type: application/json` and body `{}`.
The brewer must be at the main menu with the controller idle, all heating and
pump outputs off, and the stop latch clear. A busy brewer responds with HTTP
409. An accepted request returns HTTP 202 and `{"accepted":true}` before the
controller restarts. Reconnect to the device after it boots. Installed firmware,
the device key, Wi-Fi credentials, recipes and settings are preserved.

## Build and software checks

Use a Linux build environment with Python 3.12 or a release providing
`tarfile.data_filter`, a host C/C++ compiler, and Make. From the `firmware/pico-s-pro/modified/` directory:

```sh
python3 -m pip install -r requirements-test.txt
python3 tools/bootstrap_dependencies.py
make standalone MODEL=pico-pro
make standalone MODEL=pico-s
make standalone-test
```

The bootstrap installs the pinned Energia CC3200 SDK and ARM compiler locally.
See [SDK build documentation](sdk-build.md) and
[dependencies/lock.json](../dependencies/lock.json) for provenance, hashes, and
alternate SDK/compiler paths.

| Target | Purpose |
| --- | --- |
| `make` or `make all` | Build the standalone firmware; `all` now defaults to `standalone` |
| `make standalone` | Package browser files for serial flash and build/link the native standalone image |
| `make standalone-test` | Check browser assets/API catalog and run HTTP, native network-adapter, API/store, history, forwarding, receiver and control tests |
| `make standalone-storage-test` | Build the appliance image and check flash-file retry behavior through its actual ARM SDK code |
| `make standalone-verify` | Build standalone firmware and run its host, ARM control, and native storage checks |
| `make web-test` | Run the browser workflow suite against the real portable C API with simulated appliance I/O |
| `make firmware` | Build/check the retained legacy recovered firmware profile |
| `make verify` | Run the retained legacy recovery verification targets |

The standalone application and eligible SDK sources use link-time optimization
(LTO) and removal of unused sections to fit the CC3200 memory budget. Native
startup and runtime compatibility/allocator providers retain their intended
non-LTO boundaries. The legacy profile keeps its separate build and validation
path.

`MODEL` accepts `pico-pro` (the default) or `pico-s`. The direct command is
`python3 tools/build_firmware.py --standalone --model pico-s`. Each model has
its own application archives and native outputs, preventing a model switch
from reusing the other model's objects. Unsupported machine names fail before
building. An explicitly reused archive must match both model and profile.

Standalone outputs are under `build/standalone/<model>/firmware/`:

| File | Purpose |
| --- | --- |
| `<model>-standalone.bin` | Raw native firmware candidate |
| `<model>-standalone.elf` | Executable with symbols/debug information |
| `<model>-standalone.map` | Link map for memory and provider inspection |
| `firmware-report.json` | Source/artifact hashes, image-layout checks, RAM allocations, and validation scope |
| `native/compile_report.json` | Actual compile commands, hashes, and diagnostics |
| `<model>-standalone.update.txt` | Legacy-format image envelope. The Hub uses a ZIP containing the raw application and its matching website. |

Use the report from a successful current build for binary size, BSS, reserved
stack/heap, and remaining RAM. These measurements must not be inferred from a
previous legacy image. Link/layout success does not measure live stack usage
or prove timing on the appliance.

The checked-in [Pico S](../firmware/standalone/pico-s/firmware-report.json)
and [Pico Pro](../firmware/standalone/pico-pro/firmware-report.json) reports
give the exact sizes and hashes of the 1.0.0 images. Both reserve
32 KiB each for stack and heap. Their shared website totals 28,303 compressed
bytes in eight serial-flash blocks.

The checked-in `analysis/standalone-validation.json` is an earlier development
snapshot, including its historical source hashes and browser execution status.
It is not regenerated by the build and must not be used as the current branch's
pass/fail record. Use the new build report and the CI run for the exact commit.

For browser tests:

```sh
python3 -m pip install -r requirements-web-test.txt
python3 -m playwright install chromium
make web-test
```

An existing Chromium executable can be selected with `PICOBREW_CHROMIUM`.
The [branch workflow](../../../../.github/workflows/standalone.yml) runs the browser
suite in GitHub Actions. Inspect the run for the exact commit: a successful
firmware job does not imply the browser job passed. Some restricted local
environments prevent Chromium from creating its required sockets; that is
an unexecuted local browser suite, not a passing result.

The API/store host evidence is in
[analysis/standalone-api-tests.json](../analysis/standalone-api-tests.json).
Its scope includes shared request/response buffers, malformed JSON, reboot
simulation, storage failures, and partial writes. Physical flash behavior and
controller execution are outside that suite.

The native storage suite executes the production ARM file adapter, Energia
SLFS wrapper and SimpleLink filesystem functions while simulating their
command transport. It checks that missing-file, capacity and file-entry
errors permit a later retry, metadata failures release the handle, and an
attempted second open preserves an existing file. This covers the SDK's
negative-handle failure behavior; it does not test physical flash timing or
power-loss recovery.

## Bring up a device

1. Connect the idle brewer to the Hub computer by USB. In the Hub, open
   **Brewery → Add machine → USB setup and recovery**, select the physical
   model and follow the prompts. The Hub saves and verifies the original
   flash, provisions a unique key and installs the model-specific application,
   matching website and Hub recovery. [USB setup](../../../../server/docs/firmware-packages.md#usb-setup-backup-and-stock-restoration)
2. Boot normally and use the brewer’s Network menu to join Wi-Fi. Choose
   **Add machine** from the completed USB job to pair using its saved key.
   If the brewer was provisioned earlier, select it on the network and use
   the dial pairing described above. [Hub connection guide](../../../../server/docs/device-connections.md)
3. Open the device website from the paired Hub, or browse directly to the
   brewer’s LAN address. Its public status shows current readings. Pair the
   browser to use the protected controls.

Protected API requests use `Authorization: Bearer <device-key>`. USB setup
provisions the key. A missing key returns `503 api_key_not_provisioned` on
protected routes, and incorrect credentials return `401 unauthorized`.

This implementation serves HTTP on port 80 by default. HTTP does not encrypt
the key or traffic; operate on a trusted LAN, or provide a TLS tunnel outside
the appliance. No CORS access, cloud relay, remote account system, or Internet
exposure is configured.

The browser uses same-origin requests, no CDN, and the API's discovered
capabilities. It displays live controller values and the appliance's OLED,
offers encoder rotation/click, edits/imports/exports recipes, launches supported
programs, graphs and exports recorded brews, changes display preferences,
configures optional forwarding, and provides an interactive API explorer.
The key is retained in the tab's `sessionStorage`; Disconnect removes it.
Disconnecting the browser does not stop a program already running on the Pico.

Local operation does not require successful cloud registration or an Internet
connection. LAN connectivity is required to use the browser/API. Wi-Fi setup,
board calibration, and hardware maintenance still use their existing appliance
workflows.

## API basics and examples

The examples below assume Bash, curl, a running appliance, and the matching
private key file. Replace the example LAN address:

```bash
PICO_URL='http://192.168.1.100'
PICO_API_KEY="$(tr -d '\r\n' < private/api.key)"

pico_api() {
    curl --silent --show-error \
        --header "Authorization: Bearer ${PICO_API_KEY}" "$@"
}

curl --silent --show-error "${PICO_URL}/api/v1/health"
pico_api "${PICO_URL}/api/v1"
pico_api "${PICO_URL}/api/v1/status"
```

The key is read locally; it is not printed or included in a firmware image.
Do not copy a real Authorization header into a public issue or build log.

Requests use HTTP/1.1 and JSON. POST, PUT, and PATCH require
`Content-Type: application/json` and `Content-Length`; curl supplies the latter
for the bodies shown here. The body limit is 4095 bytes. The server handles one
connection at a time, so clients should serialize requests. Chunked transfer,
pipelining, query parameters other than the documented history `id`, and cross-origin browser calls are
unsupported.

| Route | Behavior |
| --- | --- |
| `GET /api/v1` | API version, capacities, command names, and implemented capabilities |
| `GET /api/v1/health` | Public readiness, key provisioning status, and uptime |
| `GET /api/v1/status` | Controller/workflow state, temperatures, timing, session, filter, and encoder |
| `GET /api/v1/display` | Current 128 × 64 monochrome framebuffer |
| `GET /api/v1/recipes` | IDs, names, step counts, and capacity |
| `GET /api/v1/recipes/{id}` | Complete stored recipe |
| `PUT /api/v1/recipes/{id}` | Create or replace a recipe while idle |
| `DELETE /api/v1/recipes/{id}` | Remove a recipe while idle; no request body |
| `POST /api/v1/recipes/restore-defaults` | Add missing starter PicoPak programs while idle; no body; preserve existing IDs |
| `POST /api/v1/commands` | Queue a supported operation or operate the virtual encoder |
| `GET /api/v1/settings` | Read supported nonsecret preferences |
| `PATCH /api/v1/settings` | Persist a partial preference update while idle |
| `GET /api/v1/events` | Read up to eight recent RAM events |
| `GET /api/v1/history` | Current/latest recording, retained brew inventory, capacities and sample fields |
| `GET /api/v1/history/samples/{offset}?id={id}` | Read up to 32 samples from a specific retained brew |
| `GET /api/v1/telemetry` | Read optional forwarding configuration and delivery status |
| `PUT /api/v1/telemetry` | Save the complete optional forwarding configuration while idle |

Successful recipe creation returns 201, replacement/read returns 200, deletion
returns 204 with an empty body, and an accepted command returns 202.
Applications must not parse JSON from a 204 response.

Dispatcher errors contain `error` and `message`; transport errors may contain
only `error`. Common statuses are 400 for invalid input, 401 for authentication,
404 for an absent resource, 405 for an unsupported method, 409 for an operation
that cannot run in the current state, 413 for an oversized body, 503 for
unavailable initialization/storage/authentication, and 507 for exhausted recipe
capacity. The OpenAPI document describes the route-specific contracts.

## Recipes: sixteen records, fifteen steps each

When discovery advertises `recipe_conditional_write`, GET or PUT of a single recipe returns a positive `revision`. Send that value as `if_revision` in a PUT or DELETE JSON body to modify the version you read. A change to the saved recipe catalog, including deletion and recreation, returns HTTP 409 `recipe_changed`. Reopen the recipe to review changes. A PUT can also round-trip the returned `revision` field. Supply only one revision field and omit `create_only: true` when updating.

Create a local recipe:

```bash
pico_api --request PUT \
    --header 'Content-Type: application/json' \
    --data-binary '{
      "name":"House ale",
      "steps":[
        {"name":"Mash","location":1,"target_f":152,"minutes":60,"drain_minutes":2}
      ]
    }' \
    "${PICO_URL}/api/v1/recipes/house-ale"

pico_api "${PICO_URL}/api/v1/recipes/house-ale"
pico_api "${PICO_URL}/api/v1/recipes"
```

This example illustrates the schema, not a validated brewing program or
physical compartment assignment. Review a recipe against the actual appliance
before running it.

| Field | Bounds/meaning |
| --- | --- |
| URL `id` | Stable, case-sensitive, client-selected ID: 1–20 ASCII letters, digits, `_`, or `-` |
| `id` in PUT body | Optional; must exactly match the URL if supplied |
| Recipe `name` | 1–31 printable ASCII characters |
| `steps` | 1–15 ordered steps |
| Step `name` | 1–21 printable ASCII characters |
| `location` | Integer 1–6 using the existing firmware's compartment numbering |
| `target_f` | Integer 0 for unheated, or 60–212 °F |
| `minutes` | Integer 0–599 active minutes; 0 waits for the target temperature without an additional hold, preserving native PicoPak heating steps. Check discovery's `zero_minute_steps` capability before sending 0 to older firmware. |
| `drain_minutes` | Optional integer 0–120; defaults to 0 |

Location numbers retain the recovered firmware semantics. This API does not
assign invented physical names to locations 1–6; confirm routing against the
actual machine. Targets and recipe fields remain Fahrenheit even when the
display preference is Celsius.

Unknown fields, duplicate JSON keys, fractional numbers in integer fields,
invalid IDs, control characters, and non-ASCII labels are rejected. The local
font and stored labels use printable ASCII. A PUT replaces the entire recipe;
a recipe GET result can be edited and sent back as a PUT.

Recipes are selected by their stored IDs through both REST and **Brew Recipe**
on the front panel. Stable firmware installs eight attributed archived PicoPak
programs when migrating an empty catalogue. Existing nonempty collections are
preserved. The migration is recorded in the catalogue, so later edits and
deletions survive restarts and upgrades, including an intentionally empty list.
See [starter recipes and recovery](recipe-catalogue.md) for IDs and provenance.

**Restore starter recipes** in the device web app calls
`POST /api/v1/recipes/restore-defaults`. Send `Content-Type: application/json`
and `Content-Length: 0`. A 200 response contains `added`, `preserved`, and
`capacity`. Matching IDs are kept unchanged. If all missing starters cannot fit,
507 is returned before writing. Discovery exposes `restore_default_recipes`,
`starter_recipe_count`, and `zero_minute_steps` so clients can check support.
These programs contain no ingredient bill or independently verified water
setup; use the matching PicoPak or reviewed equivalent ingredients and setup.

After you have finished using a recipe, delete it with:

```bash
pico_api --request DELETE "${PICO_URL}/api/v1/recipes/house-ale"
```

Recipe mutations and preference writes are allowed only at the idle main-menu
boundary. A queued command, active workflow, submenu, or initialization makes
the control layer busy and these requests return 409.

### Persistence and uncertain writes

The local store uses fixed SLFS filenames for catalog/data banks. Client IDs
never become flash paths. Records have a version, generation, explicit byte
layout, and CRC. A recipe update writes and reads back the inactive recipe bank
before committing the alternate catalog bank. Settings, the session counter,
and resumable-brew checkpoints have their own bank pairs. The catalog includes a checksummed starter-migration flag. Early
development images that predate this flag cannot reliably read the newer
format. Back up the recipes before returning to one of those images.

A successful write requires the expected byte count, close result, exact file
length, and readback. Missing files are distinguished from I/O failures;
malformed records are preserved for recovery rather than silently reset.

**A 503 during a write does not prove the old value is still selected.** The
write or commit may have succeeded before verification failed. A short overwrite
can also produce a valid complete new record when the existing tail already
matches. After an error or lost response, GET the resource and compare it with
the intended state before deciding whether to retry. The same caution applies
to settings. DELETE may already have committed, in which case a subsequent GET
returns 404.

Host tests cover old-or-valid-new behavior across partial-write boundaries.
They do not constitute a physical power-loss test. A first-ever torn catalog
creation with no valid bank fails closed and requires recovery; there is no
automatic format or destructive reset endpoint.

Starter installation first establishes a verified empty catalogue. Each new
recipe record is verified before one alternate-bank catalogue commit makes the
complete starter set visible. A failure after the verified empty baseline can
retry on boot; the first-ever torn baseline still follows the fail-closed rule.

After an interrupted brew, a valid checkpoint can offer an explicit continue
or abort choice. The offer does not start a session, energize outputs, consume
the checkpoint, or create a brew-history entry. Continue uses the matching
program and remaining phase durations; changed, missing or corrupt recipes
are rejected. Abort commits a tombstone so an older checkpoint cannot return.
Automatic recovery requires a valid standalone checkpoint. Legacy `restart.txt` records cannot resume a brew.

## Commands and the remote front panel

Start a saved recipe:

```bash
pico_api --request POST \
    --header 'Content-Type: application/json' \
    --data-binary '{"command":"brew","recipe_id":"house-ale"}' \
    "${PICO_URL}/api/v1/commands"
```

A 202 response acknowledges queuing. It does not mean the program completed,
the heater started, or every on-screen prompt was answered. Follow status and
the display; start commands execute through the existing workflows at the
main-menu boundary.

| Command | Behavior |
| --- | --- |
| `brew` | Run a local recipe; requires a valid existing `recipe_id` |
| `sous-vide`, `cold-brew` | Open the existing cooking/brewing program flow |
| `rinse`, `first-rinse`, `deep-clean`, `descale`, `circulate` | Run the corresponding existing maintenance flow |
| `drain-reservoir`, `drain-filter`, `rack-beer` | Run the corresponding existing utility flow |
| `utilities`, `settings` | Open the existing front-panel menu |
| `encoder` | Rotate and/or click the existing UI encoder |
| `stop` | Latch output inhibition and cancel/unwind the current local workflow |
| `clear-stop` | Clear the latch only when the workflow has returned to the idle main menu |

Program commands other than `brew` take only `command`. There are no arbitrary
program parameters, raw actuator controls, or automatic acceptance of the
appliance's preparation prompts.

Operate the virtual knob:

```bash
pico_api --request POST --header 'Content-Type: application/json' \
    --data-binary '{"command":"encoder","delta":-1}' \
    "${PICO_URL}/api/v1/commands"

pico_api --request POST --header 'Content-Type: application/json' \
    --data-binary '{"command":"encoder","click":true}' \
    "${PICO_URL}/api/v1/commands"
```

`delta` is an integer from -100 to 100 and `click` is a boolean. At least one
actual movement or click is required. Movement is clamped by the current
screen's encoder limits. Remaining program setup, prompts, and the existing
user pause/resume menu can be operated this way. Dedicated `pause` and
`resume` commands are not implemented.

### Stop and filter removal

Send `{"command":"stop"}` to request a latched stop. Subsequent heater/pump
output attempts are inhibited while the foreground workflow unwinds. Status
reports `stopped:true` and a `stop_reason`:

| Reason | Meaning |
| --- | --- |
| `none` | No latched stop |
| `requested` | A stop command latched inhibition |
| `filter-removed` | The output/control path detected filter removal and latched inhibition |

The filter-removal path sets an interrupt-safe latch; it does not run blocking
UI or network work inside the interrupt. Reinsert/inspect the filter, allow the
workflow to return to the main menu, and then explicitly send
`{"command":"clear-stop"}`. Clear-stop returns 409 while the workflow remains
busy or the controller is not idle. Clearing the latch does not resume an
interrupted recipe.

The remote stop path depends on the network and foreground polling. It is not
a substitute for a physical cutoff and has no hardware-validated latency
guarantee. Thermal and peripheral fault behavior still requires bench
verification.

## Status, display, settings, and reporting

Status reports `ready`, `busy`, `stopped`, `stop_reason`, current control
context, recovered numeric device/controller/phase codes, current compartment,
wort/thermoblock/target temperatures, the step counter, recipe and step names,
session ID, filter state, and encoder position/bounds.

Temperature values are Fahrenheit rounded to one decimal. They are null before
readiness or for invalid/out-of-range values. `uptime_ms` is a wrapping 32-bit
millisecond counter; no RTC or wall-clock timestamp is assumed.
`step_seconds` is the existing controller's step counter.

The display response contains 1024 raw panel-order framebuffer bytes encoded
as 2048 hex characters. Its format is `page-lsb-hex`: raw pixel `(x,y)` is
bit `y & 7` in byte `x + floor(y / 8) * 128`. The Pico's graphics use a fixed
180-degree rotation. To reproduce the upright front-panel image, screen
pixel `(x,y)` must read raw pixel `(127-x,63-y)`. The browser applies this
mapping; the API does not rotate or alter the raw bytes. Read the display
to understand a prompt before sending encoder actions. The browser's
up/down arrows send encoder deltas `-1` and `+1`, respectively.

Update display preferences:

```bash
pico_api --request PATCH --header 'Content-Type: application/json' \
    --data-binary '{"temperature_unit":"C","language":"en","detailed_status":true}' \
    "${PICO_URL}/api/v1/settings"
```

Supported fields are `temperature_unit` (`F` or `C`), `language` (`en`,
`es`, or `fr`), and `detailed_status` (boolean). PATCH accepts a nonempty
subset. This route does not return or modify Wi-Fi passwords, API keys, board
calibration, or direct outputs.

`GET /api/v1/events` returns at most eight recent events in sequence order.
The oldest is overwritten when the ring fills; all events disappear on reboot.
Each entry has `sequence`, `uptime_ms`, `kind`, and `detail`; text is bounded.
The response explicitly reports `volatile:true`. The Activity screen and its
event export have been removed; this endpoint remains accessible through the
API explorer.

A separate flash-backed counter allocates local session identifiers before a
session begins. Identifiers include a device-UID prefix and sequence. Persisted
session identity is separate from the bounded persistent brew history described below.

## Hub connection and telemetry

The Pico can send a full current status snapshot every 15 seconds to PicoBrew
Hub on your network. Forwarding defaults off until the Hub configures a destination. Add the brewer
in PicoBrew Hub; the firmware website shows read-only **Hub connection** status
and does not ask for a receiver address.
`GET /api/v1/telemetry` reads the destination and volatile delivery counters;
`PUT /api/v1/telemetry` saves all three configuration fields, `enabled`, `host`,
and `port`, while the brewer is idle. The host is an IPv4 address or ASCII DNS
name, without a URL scheme or path; the default port is 8080.

The Pico sends `POST /api/v1/telemetry` to that receiver with a versioned envelope
and the same readings as `GET /api/v1/status`. The upload contains no device API
key. Delivery is best effort, with no offline queue or historical backfill.
Brewing and on-device history continue independently of the receiver.

The [forwarding guide](telemetry.md) includes the complete contract, DNS behavior,
status meanings, API examples, and an optional macOS/Linux standard-library
receiver that stores snapshots in SQLite. This is plain HTTP for a trusted LAN.

## Persistent brew history and export

The Pico records each recipe brew. Existing histories can also include manual brews from earlier firmware. It does not require an open
browser, Wi-Fi association, Internet access, a Mac recorder, or a replacement
server. Settings and maintenance programs do not create history entries.

`GET /api/v1/history` returns current/latest metadata plus a newest-first
`sessions` array. At most eight entries share **24 data chunks / 4,224 samples**;
this is a shared capacity, not eight guaranteed full brews. Snapshots use 20
bytes each, normally every 15 seconds plus meaningful state changes. Three or
four ordinary 3–4-hour brews, or two 6-hour brews, are useful estimates;
phase events and partially used final chunks reduce retention. A very long
active brew can occupy the entire pool. Once its capacity is reached, `truncated`
is set explicitly; the beginning is never silently overwritten.

The oldest completed/interrupted history is removed when another entry or
chunk is needed. Export does not delete history. Choose a retained brew on the
**Brew history** page to see temperature curves, phase/step changes, gaps, and
all sampled fields. JSON and CSV downloads are assembled in the browser from
32-row pages. Each page requires the selected `id`; a removed id returns 409
so clients cannot accidentally combine two brews. The UI refreshes a visible
recording every 15 seconds; that refresh does not control recording.

Samples contain elapsed milliseconds, wort/thermoblock/target temperatures,
step time, phase, controller status, compartment, step index, ready/busy/stop/
filter/gap flags, and device state. Temperatures in the API/export are Fahrenheit;
the graph honors the display preference. Times are elapsed from the start of
the brew; no real-time clock or wall-clock brew-start timestamp is assumed.

The active RAM tail is checkpointed about once a minute using alternating
verified tail/catalog files. Completed chunks are individually CRC-checked.
A restart preserves the last committed data and marks an unfinished recording
`interrupted`; up to the uncommitted tail can be lost. A storage error or sampling
gap is exposed rather than filled with invented readings. Flash work occurs in
the guarded foreground service, independently of the HTTP listener.

History uses 28 unmirrored 4 KiB allocations (112 KiB), including the two catalog
and two checkpoint banks. Inspect the target appliance's actual allocation
layout and free extents before installation; the example directory listing
in the flash-layout guide does not establish its available capacity.

## Embedded API explorer and firmware identity

The **API** tab provides all documented operations, path/query inputs, example
JSON bodies, HTTP status and response bodies. Its catalog is generated from
`docs/openapi.json`; `make standalone-test` checks for drift. Requests share the
console's bounded request queue and current device key. Selecting an endpoint
never sends it automatically. The explorer is served from the Pico without a
CDN or Swagger UI runtime.

Firmware version is **1.0.0**, defined once in `include/picobrew/version.h`. The
OLED startup label, console footer, health/discovery responses and build report
use it. REST `api_version` remains 1. The major version marks the standalone
release; it does not mean physical acceptance testing is complete.

## How cloud behavior is replaced

The standalone profile is selected by `PICO_STANDALONE=1`. The legacy
recovered profile remains available for comparison. The standalone native
entry binds these local components:

| Component | Responsibility |
| --- | --- |
| `platform/energia_http_server.cpp` | SimpleLink listener, bounded nonblocking socket I/O, key loading, and block file reads/stat |
| `src/http_server.c` | HTTP framing, limits, bearer authentication, static assets, and request dispatch |
| `src/local_api.c` | Versioned JSON routes, schema validation, status snapshots, and recent events |
| `src/local_control.c` | Foreground service callback, command queue, virtual encoder, output-stop latch, and safe workflow boundaries |
| `src/local_store.c` | Local recipes, catalog commits, preferences, and session sequence |
| `src/local_history.c`, `platform/energia_history.cpp` | Bounded persistent brew histories, independent native file handles, snapshots and paged reads |
| `src/local_telemetry.c`, `platform/energia_telemetry.cpp` | Optional destinations, live status uploads, idle-only DNS resolution and bounded socket progress |
| `src/network/api.c` standalone adapters | Redirect the recovered recipe/session/reporting callbacks to the local services |

The service callback is polled from existing foreground waits and UI/program
loops. Polling is guarded against recursion, masked interrupts, interrupt
context, and selected shared SPI devices. The network handler queues start
commands; it does not execute a long brewing workflow inside a socket handler.
Status copies the small volatile controller snapshot under an interrupt mask,
then formats and sends it with interrupts enabled.

| Former cloud responsibility | Standalone behavior |
| --- | --- |
| Registration/identity | Load the local device identity; no account registration request |
| Recipe catalog/fetch | Read the local recipe catalog and selected record |
| PicoPak creation | Select stored local recipe IDs directly |
| Session creation | Commit a local monotonically increasing sequence and allocate an ID |
| State, step, progress, error reports | Add bounded RAM events and expose current status |
| Server-required maintenance actions | No cloud mandate; user chooses local maintenance flows |
| Telemetry upload | Hub-managed live snapshots; local status and retained brew history remain independent |
| Firmware discovery/download | Disabled in this profile |
| PicoStill cloud authorization/control | Unsupported; fails closed |

The firmware build checks that the former domain and selected cloud-update/
registration route strings are absent from the standalone binary. That static
check complements the source-level adapters; it is not a captured network trace.

## Maintenance and wireless updates

Completed brews are counted on the brewer. An idle main menu and the local website suggest a rinse after each brew and Deep Clean every three. At 20 brews, the descaling reminder applies to reservoir water containing minerals. A completed descale makes Deep Clean due. The status response includes `maintenance.due` when the brewer can read its care history. See the [Hub firmware update guide](../../../../server/docs/firmware-packages.md) for recovery setup and verification.

## Remaining scope

The native SDK copy for standalone omits Energia's unused global eight-client
`WiFiClient` registry. This hash-verified patch is recorded separately in
`dependencies/lock.json`; the listener owns raw SimpleLink sockets. The legacy
SDK keeps its registry. LTO also removes unused code without reducing the
original 32 KiB stack or 32 KiB heap reservations.

Wi-Fi can be configured through the appliance's existing Settings screens.
Changing networks disconnects the previous association, clears cached addresses,
requests DHCP and requires a fresh IPv4 acquisition event before reporting
success. Timeouts and failed leases report connection failure. The SDK
autoconnect policy is restored after the new lease; this is not a new independent
background network manager.
Inherited fatal hardware-error halts that mask interrupts still require a
physical reset; HTTP intentionally does not run in those halted contexts.

This branch does not implement PicoStill support,
unlimited history, dedicated pause/resume routes, automatic
cloud-account recipe migration, or remote credential/calibration management.
Some capabilities remain reachable through existing front-panel screens via
the encoder rather than having a dedicated JSON resource.

The OpenAPI contract is the starting point for another client, including
Flutter. It does not promise support for routes that are absent from capability
discovery. Physical acceptance of the 1.0.0 candidate, including real-network
and flash timing, remains outstanding. Use the CI run for the exact commit to
check browser execution and software results. The earlier candidate's physical
boot does not establish those results for the new firmware.

