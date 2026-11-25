# Shared standalone API contract

The shared engine uses the existing Pico S/Pro HTTP transport and API version 1.
S/Pro and C keep their current implementation and wire compatibility. New Z,
PicoStill and PicoFerm adapters use `picobrew/device_services.hpp`; advertised
features describe implemented adapters, not a promise inferred from a model.

## Transport and discovery

The browser assets and `GET /api/v1/health` are public. Other API routes require
the device's 64 lowercase hexadecimal bearer key. Missing provisioning returns
503 `api_key_not_provisioned`; incorrect credentials return 401 `unauthorized`.
No default key, wildcard CORS, cloud account or external browser dependency is
introduced. HTTP requests and responses retain a bounded 4096-byte arena.

`GET /api/v1` keeps `api_version`, `firmware_version`, `mode`, `model`,
`model_name`, `model_family`, `commands`, `front_panel`, `history_persistent`,
`events_persistent`, `telemetry_forwarding`, `dedicated_pause_resume`, recipe
capacities and history capacities. It also supplies `capabilities` with boolean
`recipes`, `brewing`, `heating`, `pump`, `fans`, `temperature`, `pressure`,
`battery`, `calibration`, `fermentation`, and `front_panel` members. Clients
must tolerate the older S/Pro/C response without this additional object.
`settings_fields` lists the supported preference names. `command_fields` maps
each advertised command to its `allowed` and `required` argument-name arrays.

Initial new model identities are `z-series` / `Pico Z` / `z-series`,
`picostill` / `PicoStill` / `picostill`, and
`picoferm` / `PicoFerm` / `picoferm`. Z1/Z2 identity must not be guessed.
An unavailable optional capability has no active route and returns 404.

## Status and readings

`GET /api/v1/status` retains identity, device UID, uptime, readiness, busy/stop
state, stop reason, command, numeric device state/phase/controller status,
compartment, wort/thermoblock/target temperatures, step timing/name/count,
recipe name, session ID, optional filter state and encoder bounds. It adds a
human-readable `state`, optional `temperature_f`, `pressure_kpa`, `battery_v`,
and arrays `sensors` and `actuators`. Unsupported or invalid readings are null;
NaN/infinity are never JSON numbers. Temperature is Fahrenheit on the wire,
independent of the browser's display preference. Milliseconds wrap at 32 bits.

Each sensor has `id`, `label`, `kind`, `unit`, and `value`. Kinds are
`temperature`, `pressure`, `voltage`, `battery`, `speed`, or `other`. Units are
`F`, `kPa`, `V`, `%`, `rpm`, or `raw`. Unestablished pressure calibration/units
must remain an explicitly raw sensor and `pressure_kpa:null`. Still preserves
four named temperature channels and its actual pressure reading; no channel is
silently discarded. Each actuator has `id`, `label`, `kind` (`pump`, `fan`, or
`heater`), `enabled`, normalized-percent `level`, and `rpm`; unknown values are
null. A reported actuator does not itself grant a direct-control command.

`recovery` is null when the device has no interrupted-brew checkpoint feature.
Otherwise it contains `available`, `valid`, `recipe_name`, `step_name`,
`current_step`, `step_seconds`, and `elapsed_seconds`. A valid available
checkpoint has a nonempty recipe name. Preparation checkpoints may have an empty
step name and `current_step:-1`; zero remaining seconds can describe preparation
or an untimed phase. Names are at most 31 printable ASCII bytes, current step is
-1..255, remaining seconds are 0..2147483647 and elapsed seconds are 0..4294967295.
Malformed metadata produces an explicit invalid-snapshot error, not a resumable
button. An invalid stored checkpoint uses `valid:false`, even when
`available:false`, and remains visible for explicit discard.

When dedicated session controls are supported, the authenticated status response
includes `can_pause` and `can_resume`. Each flag refers to the current native
runtime step. The brewer rechecks the same conditions when executing the
command. Use the front panel for setup instructions and program-specific choices.
A client must see a true flag before offering the corresponding shortcut.

### Native Z state

A device advertising `z_state_v1:true` serves authenticated `GET /api/v1/z/state`.
It returns the stock Z state fields, including the original spelling of
`SecondsEllapsed`. `WortTemp`, `ThermoBlockTemp`, `DrainTemp`, `AmbientTemp` and
`TargetTemp` are Celsius. `TargetTemp` is the last controller setpoint and may
remain set after heating stops. Use the common status to show active heating.
`ValvePosition` is the physical position, while `Location` accounts for
`UsePicoPakMode`. The remaining fields are `KegPumpOn`, `DrainPumpOn`,
`PauseReason`, `rssi`, `StepName` and `SecondsRemaining`. RSSI is zero when
the Wi-Fi link is inactive.

In the device website's Brew journal, **Record live status** records common status and
native Z state in consecutive requests. It defaults to five seconds and accepts
intervals from two seconds to one hour. Recording ends at disconnect or at
4,096 samples or 4 MiB of sample data. Download before closing or refreshing the
tab. Recorded brew sessions have independent, persistent storage.

## Commands, recipes and preferences

`POST /api/v1/commands` accepts `command` plus only the arguments advertised for
that command. The common typed argument vocabulary is `recipe_id`, `delta`,
`click`, `channel`, `level`, `enabled`, `value`, `duration_seconds`, and `confirm`.
Unknown/duplicate fields and nonfinite numbers are rejected. Integer fields
require integers; delta is -100..100, level is 0..100 percent, channel is a
nonnegative device channel index, and duration is an unsigned number of seconds.
The adapter enforces actual device bounds and allowed states. Commands must
copy borrowed input before returning 202. Stop latches inhibition synchronously;
foreground cancellation may then continue. The browser never automatically
accepts preparation prompts or starts a session while pairing.

On Pico S, Pro, C and Z, **Brew Recipe** starts a saved program using `brew`
and its `recipe_id`. A client connected to older firmware can also offer
**Manual Brew** when the device advertises `manual`.

Still commands are `start` (native manual mode), `essential-oils`, `clean`,
`stop`, `set-pump` with `enabled`, and `set-fan` with channel 1/2, level 0..100
and optional `enabled`. Original fan, temperature and pressure interlocks stay
in the execution path. Ferm commands are `start`, `stop`, `calibrate`, `sample`,
and `set-sampling-interval` with integer `value` 60..86400 seconds (default 300).
Ferm calibration requires `confirm:true` after the browser displays the native
depressurization prerequisite. This is an explicit action, never a default.

Devices that implement interrupted-brew recovery advertise `resume-interrupted`
with `confirm` allowed and required, and `discard-interrupted` with no arguments.
Resuming requires `confirm:true` after the user reviews the water, vessel and
filter. Saved progress can lag the interruption by up to 30 seconds. Resumption
uses the saved remaining step time and can return to native preparation prompts.
Loading metadata, connecting a browser, importing history or restarting the
device never activates outputs. A pending or invalid checkpoint must be resolved
before starting another recipe. The adapter rechecks checkpoint/state validity
when executing either command. Existing live-session `pause`/`resume` commands
remain separate. Discard removes the checkpoint only after an explicit user
action; clients do not add a device `confirm` argument when none is advertised.

The common engine owns a persistent 16-record recipe catalogue with 15 steps
per recipe. The existing S/Pro schema and bounds apply: IDs 1..20 ASCII
alphanumerics/underscore/hyphen, names 1..31 printable ASCII, step names 1..21,
locations 1..6, target 0 or 60..212 F, active minutes 0..599 and drain minutes
0..120. An adapter must additionally validate the native conversion. Locations
keep device-specific recovered meaning; they are not interchangeable physical
compartments across models. `zero_minute_steps` is explicitly advertised.
When discovery advertises `recipe_create_only`, send `create_only: true` in a recipe PUT when creating a new recipe or importing a copy. An existing ID returns 409 `recipe_exists` and keeps the saved recipe. Omitting the field preserves the usual create-or-update behavior.

When discovery advertises `recipe_conditional_write`, GET or PUT of a single recipe returns a positive `revision`. Send that value as `if_revision` in a PUT or DELETE JSON body to modify the version you read. A change to the saved recipe catalog, including deletion and recreation, returns HTTP 409 `recipe_changed`. Reopen the recipe to review changes. A PUT can also round-trip the returned `revision` field. Supply only one revision field and omit `create_only: true` when updating.
The common engine exposes catalogue reads to the local front panel and passes
the full selected recipe to the adapter's brew command. Mutations require an
idle device and verified persistent writes. Defaults are offered only when an
adapter provides a reviewed, attributed starter set; no Z defaults are inferred
from S/Pro compartment numbers. Accessories do not advertise recipe capacity.

### Z Program Maker recipes

When discovery reports `z_program_maker: true`, the same recipe PUT accepts
`{"id":"my-program","create_only":true,"z_program":{...}}`. GET returns the
ID and the original-format program under `z_program`. Regular recipes continue
to use `name` and `steps`. A stored program starts through the usual brew
command and machine preparation prompts.

The program has `Name`, `IsBeer`, `UsePicoPakMode`, `Steps` and optionally
`IsMetric` for display. Program and step names take 1–20 printable ASCII
characters. The catalog supports up to 15 steps. For active steps, `StepType`
is 1 and `Location` is 1 for keg, 2 for mash, or 3–6 for adjuncts 1–4.
`Temp` is Celsius, with an accepted range of −29 to 98 to cover original
metric and imperial entries. A null or nonpositive target turns heating off.
`Time` and `Drain` are integer seconds (up to 86,400 and 7,200). A pause uses
`StepType: 2`, `Name` and optional `Time`; the native runtime waits for
acknowledgement. Unknown fields, mixed program encodings and out-of-range
values are rejected before storage.

Settings use the existing `temperature_unit` (`F`/`C`), `language`
(`en`/`es`/`fr`), `detailed_status` and `auto_cycle` (automatically advance the Z brew status screens), only where advertised. PATCH is a
nonempty subset and preserves unrelated fields. Common persistence validates
before saving; application failures return an explicit error, never a success
claim. No Wi-Fi credentials, API key or calibration values are exposed as
ordinary preferences.

## Display pages

Existing S/Pro/C display responses remain unchanged. The new adapter response
adds `rotation`, `offset`, `total_bytes`, `next_offset`, and `revision` to
`width`, `height`, `format`, and hexadecimal `data`. A page contains at most
1024 raw bytes. `GET /api/v1/display` returns metadata and page zero; additional
pages use `GET /api/v1/display/<offset>?revision=<revision>`. Numeric syntax and
page boundaries are checked; a changed generation returns 409 `display_changed`
and the client retries the complete image. A framebuffer copy may be frozen
for a paged read, but metadata and bytes must refer to the same revision.

Z uses width 128, height 64, format `row-gray4-msb-hex`, rotation 0, total 4096
bytes. Byte `y*64+floor(x/2)` holds even x in the high nibble and odd x in the
low nibble. Values 0..15 map to grayscale. `page-lsb-hex` retains the existing
monochrome packing; apply the advertised rotation (legacy S/Pro assumes 180).
Devices without a physical display return 404 and have no encoder control.

## Events, history and Hub forwarding

Events retain the existing bounded eight-entry volatile ring and fields
`sequence`, `uptime_ms`, `kind`, and `detail`. History is device-owned and
persistent: eight sessions with an explicitly advertised bounded record pool,
samples at the captured device interval plus state changes, explicit truncation/storage errors,
restart recovery and paged export. The first eleven history columns retain
their S/Pro names/order. Additional columns may include `temperature_f`,
`pressure_kpa`, `battery_v`, followed by each named sensor/actuator reading;
`fields` defines exact sample order. Clients must index by field name and must
not assume accessory records represent brewing or heating. Session identity is
allocated from a persistent device counter; unverified/corrupt storage does
not silently reset it. An interrupted session is recorded, never auto-started.

Discovery reports `history_sample_interval_ms`; each retained session reports
its own `sample_interval_ms`. The default is 15000 ms. Sensor devices may declare
a stable interval from 15000 to 86400000 ms; PicoFerm uses its configured acquisition
interval, default 300000 ms. Old session metadata retains the original interval
after a later setting change. History has 4224 records across eight sessions,
returns up to 32 rows that fit the 4096-byte response arena, and uses `next_offset`
as the authoritative page continuation. A full record pool or exhausted 32-bit
elapsed-time range reports truncation rather than silently wrapping history.

`GET`/`PUT /api/v1/telemetry` keeps the existing enabled/host/port configuration
and delivery counters. Forwarding is optional, defaults off, contains no device
key, and uses the existing versioned Hub status envelope. Device operation and
history continue without the Hub. Slow/unreachable networks are bounded and
must not block actuator/sensor service. No firmware OTA route is implied.

### Archived Z cloud recipes

On a brewer advertising `z_cloud_program_v1`, a saved recipe can carry `z_cloud: {"start_water_ml":13100,"is_beer":true}` with the usual `name`, `id` and `steps`. This selects the Z cloud compiler. Source locations 0–5 mean keg, mash and adjuncts 1–4. Location 6 is an operator pause. Hold and drain are whole minutes. A zero temperature keeps the previous heat setting. The device uses the original raw Fahrenheit values and measured local boiling point when compiling. A positive active target below 60 °F requires an immediately preceding operator pause for chiller setup. The Hub reads back and compares the entire saved program before reporting a transfer. Starting water must be 1–16,163 mL, within the stock keg planning ceiling.

### Original PicoPak programs on Z

`z_picopak_v1:true` advertises the dedicated PicoPak compiler on a Z. It requires the operator's original pack and Z PicoPak Adapter. Add `z_pak` to a named recipe containing the original steps. Required members are positive `recipe_id` (PicoRecipeID) and `beer_id` (PicoBeerID), original `abv` and `ibu` indices, and `step_options` containing one entry per source step in order. Each option contains `action` (0 for a flow step or 1 for an operator pause). Optional `min_time` and `max_time` are whole minutes. Omit absent bounds. Preserve direct source locations 1 through 6. An optional `selected_abv` or `selected_ibu` (0–10) changes timing only when the original target is 1–9 and a corresponding active mash or hop step has both bounds. The device executes the native strength calculation, setup prompts and 6.81 L water charge. The PicoPak and Z cloud formats are mutually exclusive.

## Z Group Brewing and PicoStill

A Z advertising `group_brewing_v1` accepts authenticated reads and writes at `/api/v1/group-brew`. A read reports a recent local start and the current invitation. An offer identifies a saved slot and revision for a recipe, or selects rinse or clean. Accepting the offer enters the normal preparation flow. A Z with `z_picopak_v1` can include `selected_abv` and `selected_ibu` in a kit invitation. Each is an optional index from 0 to 10 for this brew. An omitted setting uses the value saved with the slot. The source and recipient each keep their saved recipe. The Hub handles membership and per-brewer consent. [Group operations and expiry](../../z-series/modified/README.md#group-brewing)

A Z advertising `group_z_programs_v1` accepts `action:"z_program"` with a `program_id`. Supported IDs are 2 (Drain), 3 (Rack), 4 (Circulate), 5 and 6 (Sous vide without and with keg), 7 (PicoStill manual), 20–23 (Cold Brew, Hybrid, Hot and Custom coffee), 26 (PicoStill boil point) and 27 (Chill). Both ends must support this capability. Coffee requires `water_l`, `temperature_f`, `intensity` and `time_seconds`. The water limits come from the original 1–4.2 US gallon and 4–15.5 L pickers. Temperature is 100–200 °F, intensity is an integer from 1 to 15, and time is 60–5,400 seconds in one-minute increments. Accepting coffee loads the original program and presents the separate Start control. Reads include a localized `program_name` for display. The device determines that name.

A Z advertising `picostill_brewer_link_v1` exposes the paired accessory at `/api/v1/picostill`. The brewer reports the accessory's four temperature readings, pressure, pump, cooling fan and program state during a session. The paired UID and verification status are readable between sessions. [Pairing, logging and commands](../../z-series/modified/README.md#using-a-picostill-with-the-z)
