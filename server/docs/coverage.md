# Feature and hardware coverage

For setup instructions and the machine-by-machine owner guide, see the [project README](../../README.md). The matrix below records implemented workflows and their verification limits. [Firmware roadmap](../../docs/project-roadmap.md)

| Area | Implemented | Boundary |
|---|---|---|
| Fleet | Persistent machine registry, independent polling, connection status, live readings for simultaneous brews, explicit brew groups, individual invitations and linked journals | One owner; no team roles or remote-site agent |
| Pico S/Pro | Standalone API v1, mirrored OLED and dial, programs, stop latch, preferences, history, pairing on capable builds, and Hub OTA through separate recovery | Wireless updates require an eligible idle brewer with verified recovery and boot layout. Qualification applies to the individual machine. |
| Pico C | Standalone brewing, mirrored OLED and dial, recipes, history and wireless updates for the SSD1325 and SSD1309 variants. | USB setup provisions recovery for the verified display. Physical installation and brewing trials are pending for both variants. |
| Generic Z-series | Standalone controls, grayscale display, sensor history, and direct transfer of ingredient recipes, Program Maker files, original Z schedules and compatible PicoPak adapter programs. Each qualifying module supports network updates after USB provisioning. A wired-capable receiver also supports Ethernet. | Stock Z 0.0.119 owners can import exported JSON in the brewer’s Program Maker. The standalone installer checks each module’s stock image and layout. Physical qualification is pending. |
| PicoStill | Native operating modes, pump and fan controls, temperature and pressure readings, history, wireless application updates and authenticated pairing with a Z for manual and boiling-point programs. | Wi-Fi updates require USB-provisioned recovery. Physical trials are pending. |
| PicoFerm | Fermentation recordings, sampling interval, pressure calibration, temperature and battery readings. The Hub links a recording to a batch and receives readings directly. | Pressure uses the sensor’s native scale. Wi-Fi updates require a USB-provisioned dual-bank layout. Physical installation and battery runtime tests are pending. |
| Zymatic | Stock 1.1.14 fetches published recipes and reports sessions over Wi-Fi or Ethernet. Custom firmware adds a device website and Hub monitoring. On verified boot layouts, the extended application also supports dial pairing, remote dial input, and retained history. | First USB conversion uses the compact profile. Both custom profiles ship as 1.0.0. Wi-Fi serves the Home menu, read-only programmed pauses and the extended pairing prompt. Use Ethernet for continuous monitoring during a brew. Physical installation and brewing qualification are pending. [Stock setup](stock-zymatic.md) · [Firmware features](../../firmware/zymatic/modified/application.md) |
| Recipe library | CRUD, revision conflict protection, archived revisions, ingredient plans and distinct native-program editor | Program-only records have no inferred ingredients, batch volume or efficiency; ingredient and historical style references are available in the [Beer Crafter workflow](beer-crafter.md) |
| Community catalog | **Recipes → Browse all** lists measured recipes with programs for all five brewing profiles. **Sources & credits → Browse source archive** includes original schedules and unresolved records. | Source records retain their original measurements and attribution. Additional PDF manuals can be installed locally. |
| Brewing details | Water chemistry, fermentation/carbonation metadata, mash and boil attributes | Metadata does not imply a connected fermenter or automatic addition hardware |
| Adaptation | The selected target machine determines the brewing profile. The Hub scales all measured ingredients together for the usable basket and slot capacities, generates supported routes, and checks the resulting program. | Efficiency and yield are estimates. Ambiguous flow paths, unrepresentable steps, and unresolved source instructions block transfer. |
| Recipe deployment | Single and bulk transfer; capacity preview, explicit matching-ID updates, profile/revision/inventory checks, native write/readback, durable receipts and interruption recovery | Does not start automatically; an uncertain write stops the batch and is never automatically repeated or rolled back |
| Installed recipes | Read, edit with expected-value check, deploy/overwrite, choose for brewing, remove while idle | Writes and removals check the catalog revision on capable firmware. Earlier builds check the previous contents before sending. |
| History | Retained-session imports, durable samples, named sensor/actuator channels and units, temperature/state graphs, notes/rating, CSV/JSON | Cannot recover overwritten source samples; old start times may be unknown |
| AI recipe creation | Brew assistant with validated drafts for review in the recipe editor | Requires a configured provider connection, using a supported subscription sign-in or API key. |
| AI model discovery | Account model catalog, automatic lookup with a saved key, pre-save connection, refresh and manual entry | Model-list permission and chat compatibility are distinct; discovery does not invoke inference |
| AI monitoring | Opt-in at start or during a live brew; exact-session consent, contextual questions, five-minute observations saved with that brew | Advisory; no low-level control or live recipe/temperature overrides; consent does not carry into the next brew |
| AI analysis | Analysis in the selected journal entry, saved results and JSON export, downsampled bounded context | No invented measurements; raw archive/export remains available; a removed machine does not remove archived analysis |
| Shopping list | Aggregate selected recipes by ingredient and unit | Inventory purchasing, supplier integrations and stock deduction are absent |
| Machine identity | Read C/S/Pro, generic Z-series and accessory identities after authenticated UID verification; explicit fallback for older S/Pro firmware | Reported identity describes the installed build, not independent hardware detection; C package applicability also requires a separate UID-bound controller observation |
| Telemetry forwarding | Automatic Hub connection on pairing; persistent advanced manual override in the Hub; ingest known-UID unsigned observations | Unsigned forwarding is not authoritative status or a control channel |
| Data | SQLite persistence, encrypted credentials, portable nonsecret ZIP, full-directory recovery instructions | No browser upload/restore of a full encrypted data directory |
| API | Authenticated REST endpoints, OpenAPI schemas/catalog, in-app explorer, SSE | Hub authentication differs from the appliance's bearer-key API |
| Packaging | Source, pinned dependencies, Dockerfile, Compose, Go/frontend/browser CI | No managed hosting or app-store package |

## Machine controls and care

Machine pages use firmware discovery to choose controls. On Pico S, Pro and C, Deep Clean opens the same preparation prompts as the Utilities menu. Firmware with the corresponding dedicated endpoints also offers **Restart brewer** in Manage machine and **Restore missing starter recipes** in the saved recipe view. The Hub sends both through its confirmed, idempotent commands API. The brewer checks its live state again before acting.

The machine pages and completed brew journal show the manufacturer's care instructions. When firmware reports a task is due, the Hub displays a reminder on the Brewery page and on that machine’s page. [Care reminders by model](../../docs/firmware-parity.md#recipe-checks-and-manufacturer-care)

## Prior projects considered

The product scope was informed by these primary project descriptions and documentation:

- [chiefwigms/picobrew_pico](https://github.com/chiefwigms/picobrew_pico): local cloud replacement, appliance-specific protocol handling and session/recipe workflows.
- [hotzenklotz/picobrew-server](https://github.com/hotzenklotz/picobrew-server): Zymatic protocol and local recipe/session server.
- [PicoClaw](https://github.com/cetezadi/picobrew_picoclaw): another community PicoBrew server implementation.
- [Small Brew Companion documentation](https://www.smallbrewcompanion.com/docs/): recipe creation, equipment/brewing profiles, brewing logs, ingredients/water/fermentation/carbonation and shopping-list workflows.
- [Standalone Pico S/Pro API contract](../../firmware/pico-s-pro/modified/docs/standalone-api.md) and [OpenAPI](../../firmware/pico-s-pro/modified/docs/openapi.json): the implemented hardware contract in this checkout and the reference for subsequent firmware ports.

The Hub implements its own standalone device adapters and its own adapter for the original Zymatic protocol. Stock Pico C, S, Pro, and Z-series devices cannot be registered as standalone machines. A recipe planning profile identifies a recipe target independently of firmware support for live operation.
