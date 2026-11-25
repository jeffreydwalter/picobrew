# Zymatic brew history

The extended Zymatic build records a brew on its ATmega1284P while offline and while the Wi-Fi radio is busy with the stock brew controller. A paired Hub can read the journal after the brewer reconnects. Local and Hub-selected recipes use the original interpreter. An OEM recovery that reenters the interpreter starts a new history ID so the earlier interrupted record remains identifiable.

| Stored measurement | Source in stock 1.1.14 |
| --- | --- |
| Elapsed time | Time since this interpreter invocation, in whole seconds. The brewer has no wall clock for this journal. |
| Wort and thermoblock temperatures | Fahrenheit readings in SRAM at `0x23dc` and `0x23e0`, rounded to a tenth of a degree. Missing or invalid readings are returned as `null`. |
| Step, target and route | The zero-based index at `0x23c0` selects a six-byte step in the active recipe. That step supplies its programmed target °F and route. A route of 0 selects the keg, 1 the mash, 2 through 5 the four hop additions, and 6 a pause. These describe the program's instructions. The journal has no valve-position feedback. |

The first available reading, subsequent step changes, a nominal one-minute interval and program exit trigger samples. A row can be recorded at most once per elapsed second. Measurements outside an active step carry null step, target and route. Recording yields to physical input, faults and low stack headroom. There can be gaps while the stock program blocks or uses another screen. The reported duration covers the retained samples.

The journal holds up to **222 samples total across eight sessions**. At one-minute spacing, 222 samples span 221 minutes. Step changes consume additional rows. When all rows belong to the running brew, recording preserves its existing rows and flags truncation. As later sessions need space, the recorder evicts whole older sessions. A normal return and the original cancellation path have distinct terminal states. After power loss, committed rows are available under their previous history ID and an unfinished session is identified as interrupted.

## EEPROM ownership

The 1.1.14 application uses the lower EEPROM addresses for configuration, networking, fault and session recovery data. The firmware checksum and factory marker occupy the high end. The journal reserves only `0x0100` through `0x0fef`. At first enrollment it checks that this space is erased or cleared. It also recognizes an interrupted initial sequence write. Unrecognized allocations inhibit history writes. Existing configuration and commissioning addresses lie outside this range. [Stock address and recipe evidence](../reverse-engineered/README.md)

| EEPROM addresses | Purpose |
| --- | --- |
| `0x0100–0x010f` | Two alternating sequence allocations with CRC-16. |
| `0x0110–0x020f` | Eight session headers, each with its numeric ID, recipe name, starting row, checksum and outcome. |
| `0x0210–0x0fef` | 222 sixteen-byte samples with an owning session ID, measurements and CRC-16. |

The writer invalidates the old tag, updates and reads back each changed byte, then commits the new tag last. Mounting rebuilds indexes from EEPROM. The factory commissioning loop still clears every session name and sample. Its only exception on a previously enrolled unit is the sixteen sequence bytes that prevent a new brew from colliding with an existing Hub journal entry. Restoring the OEM image restores the original full-chip Factory Reset.

## Reading the journal

The extended build exposes these records under **Brew history** on the paired local site. Select a session to see its samples. Both API routes use the paired device key. `GET /api/v1/history` returns a `fields` array and a newest-first `sessions` array. The Hub uses a session's numeric ID to read `GET /api/v1/history/samples/0?id=<id>`. A page contains up to three rows. When present, `next_offset` gives the next page. A unit that cannot verify its EEPROM returns an unavailable response.

The host regression suite exercises power loss at each byte-write boundary, timer rollover, corrupt records, cancellation, EEPROM ownership, factory erasure, session eviction and requests through the modeled W5100. Physical EEPROM endurance, controller latency and behavior through real mains interruptions still need hardware qualification.
