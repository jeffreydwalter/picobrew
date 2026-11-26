# Recipe and program transfers

The community catalog is immutable. **Copy to library** creates a separate
editable record and retains source provenance. The library is independent of
each brewer's installed recipe slots. The machine page and the brewer's own
browser interface both read and edit those same installed slots.

## Ingredient recipes and native programs

Ingredient recipes have a batch volume, efficiency, ingredient quantities and a
reviewed plan. Adaptation applies the selected machine profile and validates
explicit compartment assignments. PicoPak machine programs retain exact source
temperatures, hold times, drains and routing. They have no inferred batch size
or ingredient quantities, and transfer with `batch_liters: 0`.

Native IDs are preserved. For example, the Pico Pale starter has the same
`pico-pak-64` ID in firmware and catalog copies. Copying it into the Hub library
does not create another appliance slot until a transfer is explicitly confirmed;
an identical installed program is skipped.

Zero-minute native steps are kept as zero. The Hub checks the firmware's
`zero_minute_steps` capability and will not silently change such a step to a
one-minute hold for an older firmware. Source labels that exceed the brewer's
ASCII/length limits require an explicit edit.

## Preview and confirm

Select library entries and choose a connected idle brewer. A transfer preview
reports its advertised capacity, occupied and available slots, new entries,
proposed updates, identical programs and skipped entries. It never deletes an
installed recipe. Matching IDs are preserved unless the update option is enabled.

For a single recipe, the preview names any installed program with the same ID.
**Replace on machine** authorizes replacing that specific version. The Hub
rejects a replacement if the installed contents changed after the preview.

For a new slot, firmware advertising `recipe_create_only` checks the ID and
writes in one operation. If another client claimed that ID first, the existing
program stays in place. The simulator uses the same rule. On older firmware,
the Hub rechecks the slot just before writing. An independent client can still
write between those two requests.

Pico C/S/Pro standalone firmware provides 16 recipe slots with up to 15 steps per
program. The Hub reads the device's advertised limits rather than assuming other
models use these values. Maintenance programs use the same source-model and
capacity rules. A stock Zymatic retrieves its published maintenance programs in the cleaning list. The Hub blocks sending Zymatic maintenance instructions to a Pico S, Pro or C. [Stock Zymatic setup](stock-zymatic.md)

For a standalone Z, direct transfer preserves the selected format, including original Z cloud schedules and PicoPak adapter programs when supported. Each retains its own setup prompts and routing. A registered stock 0.0.119 Z can receive a reviewed Program Maker recipe for dial start. Its own website also accepts compatible exported JSON. [Stock brewer transfers](stock-brewers.md) · [Z recipes and front panel](family-devices.md#z-recipes-and-front-panel)

Before execution, the Hub verifies device identity, idle state, current profile,
recipe revisions, current inventory and firmware capabilities against the
preview. Previews expire after 15 minutes. If those inputs change, prepare a new
preview. Each accepted write is read back before continuing to the next entry.
No transfer starts a brewing or cleaning program.

## Interrupted transfers

Receipts are stored before sending and after each verified write. A transport or
verification failure stops the batch. An unconfirmed write may have reached the
machine; it is not automatically retried or rolled back. Refresh the installed
inventory and prepare a new preview to resolve the remaining work.

After a Hub restart, an in-progress receipt becomes `interrupted`. Already
verified entries stay verified, an in-flight entry becomes `unconfirmed`, and
remaining entries are `not_sent`. The browser remembers the last receipt ID per
machine and can reopen it. A completed transfer can be read again without
repeating its writes.

## Editing an installed slot

The installed-program editor submits the expected previous program along with
the requested update. The Hub reads the machine again and rejects an edit if the
previous program has changed or disappeared. It verifies the written program by
readback. On Pico S, Pro, C and Z builds that advertise
`recipe_conditional_write`, the firmware checks the catalog revision again
when it commits a replacement or removal. If the catalog changed, refresh the
slot and review it before saving. Earlier builds only support the Hub’s
preliminary comparison, so edit from one client at a time on those builds.

## API

All actions use the existing owner session and CSRF rules.

| Method and path | Purpose |
| --- | --- |
| `POST /api/v1/devices/{id}/deploy` | Send a single reviewed recipe. To replace an existing slot, include its complete previewed program as `replace_expected`. |
| `POST /api/v1/devices/{id}/deployments` | Create a persisted preview from `profile_id`, `overwrite_existing`, and `items` containing library `recipe_id`, `source_revision`, and `batch_liters`. |
| `GET /api/v1/devices/{id}/deployments/{deployment_id}` | Read the preview or current receipt. |
| `POST /api/v1/devices/{id}/deployments/{deployment_id}/execute` | Execute the accepted preview once, using an empty JSON object. |
| `PUT /api/v1/devices/{id}/recipes/{recipe_id}` | Edit an existing slot using `recipe` and `expected` machine-program objects; the ID must stay unchanged. |

The authenticated OpenAPI document includes the request and response schemas.
