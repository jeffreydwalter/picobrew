# Standalone family controls

The Hub reads the authenticated device identity, capabilities, command argument
descriptions and supported settings before presenting controls. During pairing it
can also save a reachable address for the machine to send readings to the Hub.
Operating the machine requires an explicit command. The Hub records its outcome
and leaves an uncertain command for the owner to review.

| Family | Controls and observations |
| --- | --- |
| C/S/Pro | Existing monochrome front panel, dial, local recipes, advertised native programs, stop latch, preferences and retained history. C keeps its explicit display geometry and separate firmware package checks. |
| Generic Z-series | Actual 128 × 64 grayscale panel, dial, local canonical recipes, native programs and advertised pause/resume. Named temperature, pressure and actuator readings remain available. The identity does not establish a Z1/Z2 mapping. |
| Zymatic | Both standalone profiles offer Ethernet monitoring, Wi-Fi monitoring from the Home menu and programmed pauses, recipe sync and one USB-provisioned offline recipe. The extended profile adds remote dial inputs and retained history. Stock 1.1.14 can fetch recipes and report sessions to the Hub. [Zymatic profiles](../../firmware/zymatic/modified/application.md) |
| PicoStill | Start, essential-oils, cleaning and stop commands. While a program runs, the pump and fan controls show the reported settings for the selected output. The page also displays reported temperatures and pressure. No recipe catalog, heater controls or front-panel view is invented. |
| PicoFerm | Start or stop recording fermentation, take a reading, set the interval while idle, or calibrate pressure. A manual reading can retry a sensor fault. The page shows temperature, native pressure and battery, without brew or heater controls. |

To follow a keg in the Hub, open its batch in **Brew journal**, select **Ferment**, and choose the PicoFerm. Start recording on the PicoFerm or its Hub page, then attach that recording to the batch. The brewer can be off. [Linking, moving and reconnecting a PicoFerm](device-connections.md)

PicoFerm calibration requires an explicit confirmation after removing the unit
from its keg or adapter, removing its GRAY seal and fully depressurizing it.
Opening or cancelling this dialog sends no calibration command. Sampling
interval is an integer from 60 through 86,400 seconds. The default form starts
at 300 seconds; it is labeled as an entry value rather than an observed setting.

PicoFerm pressure has no established physical unit in the recovered OEM source.
Its named reading is explicitly `raw`; normalized `pressure_kpa` remains null.
The Hub does not infer a conversion. Other readings follow their declared units,
including Fahrenheit temperatures and the normalized PicoStill kPa channel.

## Z recipes and front panel

Choose **Send** to save a program on a connected standalone Z. The Hub checks the individual module’s step limit before transfer and reads back the saved program. Select **Brew Recipe** on the dial, or start the saved recipe from the website or Hub when ready. Each module stores up to 16 programs. Updated modules accept up to 20 steps in a Z Program Maker file. Earlier firmware and the other Z recipe formats accept up to 15 steps.

| Recipe in the Hub | What the Z runs |
| --- | --- |
| Ingredient recipe | The Hub builds a program for the Z from the measured ingredients and schedule. Its steps select the keg, mash basket or a specific hop compartment. An unheated step switches heating off. |
| Z Program Maker file | The brewer reads the original Celsius targets, hold and drain times in seconds, setup options and pauses. The PicoPak mode setting selects the adjunct routing. |
| Archived Z cloud schedule | The brewer uses the source starting-water quantity, temperatures, times and pause markers. A zero temperature keeps the previous heat setting. The Hub archive includes 39 such schedules. |
| Original PicoPak program | Use a physical PicoPak and the Z PicoPak Adapter. The native controller prompts for the adapter and water. For kits with authored timing ranges, selected strength and bitterness change mash and hop contact times. |

The two archived program formats contain machine instructions. Brewing from loose ingredients also requires the ingredient bill and loading instructions. A compatible stock **0.0.119** Z accepts a Program Maker JSON export through its own website as its single working program. Stock Program Maker cannot import the cloud or original PicoPak formats. [Native Z recipe formats](../../firmware/z-series/modified/oem-parity.md#what-a-z-recipe-specifies) · [Stock Program Maker export limits](beer-crafter.md#machine-programs)

The Hub assembles the four Z display pages under one adapter lock, pins their
revision and rejects inconsistent dimensions, offsets or data. A revision change
discards the entire frame and retries at most twice. The browser preserves all
16 gray levels, nibble order and upright row order. Existing mono orientation
and the Pico C logical viewport remain unchanged.

## Interrupted brews

When the device advertises checkpoint recovery, the machine page displays its
saved recipe, step or preparation phase, remaining step time and elapsed time.
A pending checkpoint blocks new programs until it is resumed or discarded.
An invalid checkpoint remains visible for discard even when it is unavailable
for resumption. Loading this page never resumes the machine.

Resume asks the user to verify the water, vessel and filter, explains that saved
progress can lag the interruption by up to 30 seconds, and sends the explicitly
confirmed `resume-interrupted` command. The device resumes its saved remaining
time and may return to preparation prompts. Discard has a separate confirmation
and sends only `discard-interrupted` to the firmware. Live-session pause/resume
and clearing a stop latch do not perform checkpoint recovery.

## History

The session journal preserves the legacy eleven fields and every appended
normalized, sensor and actuator field. Field names and label/unit metadata come
from the selected session's sample pages. Charts group compatible units and
label native raw values; the sample table and JSON/CSV exports preserve all
channels without filling missing readings with invented zeroes.

Pico S, Pro, C, Z, PicoStill and PicoFerm retain up to 4,224 rows across eight sessions. An extended Zymatic retains eight sessions sharing 222 samples. The compact Zymatic relies on the Hub for live monitoring. Page size on the 4,224-row devices can vary up
to 32; `next_offset` controls continuation. The importer bounds the total read,
rejects a schema change during a download, checks retained metadata again after
reading, and reuses a previously verified prefix only when the same retained
window still exists. Truncated or overwritten source history remains explicit.

Each session's `sample_interval_ms` is retained in Hub history and JSON exports.
Changing PicoFerm's sampling interval for a later session does not relabel an
older session. A changed interval invalidates an imported prefix, and malformed
intervals are rejected. Older firmware that omits this field remains supported
without inventing a cadence. The local device browser likewise exports the
selected session's interval instead of the newest session's setting.

## Verification boundary

`internal/device/family_test.go` covers authenticated protocol fixtures, exact
argument values, display paging, and named history decoding.
`internal/server/family_test.go` covers Hub confirmation/idempotency, identity,
schema persistence, precision-preserving export and append/ring synchronization.
`internal/recipe/z_canonical_test.go` checks direct-valve compilation and archived
grammar rejection. `web/tests/family-controls.spec.ts` exercises all three new
families in Chromium at desktop and mobile sizes using local API fixtures.

These checks use the actual server/browser source and local fixtures. They do
not contact hardware, establish safe physical installation, or claim stock
firmware compatibility. The server remains self-contained under `server/`;
building or running it does not read sibling firmware sources.

In a Z’s brewer preferences, **PicoStill** can pair a registered standalone accessory or a same-LAN PicoStill with original firmware 0.0.35. For the original firmware enter the accessory’s LAN address and factory UID. [Stock setup and connection-loss behavior](../../firmware/z-series/modified/README.md#using-a-picostill-with-the-z)
