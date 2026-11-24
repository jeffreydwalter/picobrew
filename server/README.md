# PicoBrew Hub

The Hub runs on your computer or Raspberry Pi and serves a browser interface for machine controls, recipes, and brew history. For first-time setup, machine compatibility, and your first brew, start with the [PicoBrew owner guide](../README.md).

Custom Pico S, Pro, C, generic Z-series, PicoStill, and PicoFerm firmware reports its controls and sensors to the Hub. Custom Zymatic firmware offers a wired interface whose capabilities depend on the installed build. An unmodified Zymatic 1.1.14 can fetch recipes from the Hub over its original LAN protocol. Stock Pico S, Pro, C, and Z-series firmware have no equivalent Hub adapter. [Feature coverage](docs/coverage.md) · [Stock Zymatic setup](docs/stock-zymatic.md)

## Run locally on Windows, macOS or Linux

For a ready-to-run executable, follow the [owner setup guide](../README.md#set-up-the-hub). To build the current Hub from source, install [Go 1.26 or newer](https://go.dev/doc/install) and Git. The website and SQLite driver are included. From Terminal or PowerShell, get the source and enter the Hub directory.

```sh
git clone https://github.com/jeffreydwalter/picobrew.git
cd picobrew/server
```

If GNU Make is installed, run the following command. The equivalent Go commands below work without Make.

```sh
make server-start
```

This builds the Go executable with its website and native USB tools, then starts it
in the foreground. The first supported-platform build downloads one size- and
SHA-256-verified USB worker from the project release. Later builds reuse the
verified local file. Release executables already include it. Both entry points use the same
`server/data` directory; an isolated server copy uses its own `data` directory.
Press **Ctrl-C** to stop, including when the browser is receiving live updates.

For a build without starting, use `make server` at the repository root or `make`
inside `server/`. The output is `picobrew-hub.exe` on Windows and `picobrew-hub`
on macOS/Linux. Both use the existing SQLite data files directly.

If Make is not installed, run the following from the directory containing
`go.mod`, `cmd/` and `internal/` (`server/` in the full checkout):

```sh
go run ./tools/usb-prepare
go build -trimpath ./cmd/picobrew-hub
```

An unstaged raw Go build supports network operations. To produce a USB-enabled build from local programmer sources, run the native builder in the full repository and set `USB_WORKER_LOCAL` to its output directory when invoking Make.

Then start it with `./picobrew-hub --data ./data` on macOS/Linux, or
`.\picobrew-hub.exe --data .\data` in Windows PowerShell. The executable serves
its own website even when launched from another directory; use `--data` to
choose where that launch stores its database.

Open **http://127.0.0.1:8080**. Authenticated mode listens on the host's network interfaces at port 8080 so machines can report back to the Hub; an explicit `--listen` still overrides that default. On first launch the server prints a one-time setup token. Enter it in the browser and choose an owner password of at least 12 characters. Subsequent visits use that password. Sessions expire after 24 hours or a server restart.

On **Brewery → Add machine**, select **Pico standalone firmware**. Enter a name and a **Machine URL** that the Hub host can reach. Click **Add machine** and follow the pairing instructions. On firmware with dial pairing, confirm the displayed code on the brewer. If you just completed USB setup in the Hub, continue from that setup so the Hub can use the key it saved. Older firmware can use **Use an existing key instead**.

The Hub checks the machine identity and discovers its controls. Use the brewer’s current IP if its hostname resolves on your browser’s computer but fails on the Hub host. Open **Manage machine** on the machine page to test the connection, open its local website on pairing-capable firmware, or copy its device key. [Machine connections and API keys](docs/device-connections.md)

## What you can do

- **Manage a fleet.** The **Brewery** page shows each machine’s connection, state, temperature, and active recipe. You can follow simultaneous brews there and open any machine for its controls.
- **Operate standalone devices.** C/S/Pro and Z expose their physical screen and dial, installed recipes and advertised maintenance programs. PicoStill exposes its actual pump, fans, temperatures, pressure and operating modes. PicoFerm exposes fermentation sessions, samples, its sampling interval and explicitly confirmed pressure calibration. The console exposes only the capabilities reported by that device.
- **Create a recipe.** Choose a brewer, enter grain weights and hop times, and set the mash rests. Review the bin loading list and schedule before saving. The wizard uses the selected equipment profile, with Z batches sized for one module. Choose **Advanced editor** to carry the draft into the full editor for unusual schedules or detailed planning. Zymatic recipes can be published to the stock brewer’s sync menu.
- **Curate recipes.** Maintain ingredients, mash/boil steps, water chemistry, fermentation and carbonation details, tags and notes. Every save creates a revision; simultaneous edits are detected. Import/export JSON and BeerXML, and aggregate selected recipes into a shopping list.
- **Browse community recipes.** Open **Recipes → Browse all** for ingredient recipes with machine programs across the supported brewing profiles. **Sources & credits → Browse source archive** also includes original PicoPak schedules and records with unresolved instructions. Copy a recipe to **My recipes** to edit it. [Catalog contents and provenance](docs/community-catalog.md)
- **Send a recipe.** Save measured model defaults in Settings, preview the proportional ingredient load and estimated yield for the target model, review warnings and compartment routing, then deploy the compiled recipe to one idle machine. Deployment and starting are separate actions. The original library recipe stays intact.
- **Transfer several programs.** Select library entries and preview what fits on a brewer. Existing recipes are preserved by default; matching updates require an explicit choice. Confirm the reviewed batch to write and verify each program. Receipts identify verified, skipped and unconfirmed entries, including after a Hub restart. The machine page can also edit the installed slots used by the brewer's own interface.
- **Keep the brew record.** The Hub imports available readings from machine history and charts temperatures alongside machine state. Add notes, rate the brew, or export the session as JSON or CSV. Open a batch in **Brew journal → Ferment** to attach its PicoFerm recording. Each batch keeps its readings when the monitor moves to another keg. [Link a PicoFerm](docs/device-connections.md)
- **Create recipes with AI.** In the full recipe editor, choose **Ask the brew assistant** to generate a structured draft, optionally using a saved recipe as a starting point. Review the draft in the recipe editor before saving, adapting or deploying it.
- **Get help with a brew.** Opt into AI monitoring in the Start confirmation or on the active machine page. Monitoring follows that one brew and saves observations at most once every five minutes. Ask follow-up questions beside the live readings, then open the brew's journal entry for analysis of its recorded results. Provider setup is optional; machine commands still require your explicit action.
- **Build other clients.** Browse the authenticated API explorer or download `/api/v1/openapi.json`. The same API serves the UI, with session authentication, CSRF protection and idempotent command receipts.

### Connect the brew assistant

In **Settings → Brew assistant**, choose a provider and a **Connection method**:

- **ChatGPT subscription:** choose **OpenAI / ChatGPT → ChatGPT subscription**, click **Sign in with ChatGPT**, then open the sign-in link. Sign-in completes automatically when the browser is on the Hub host and its temporary local callback can listen on port 1455. Keep Settings open; it checks the pending connection for up to ten minutes. For a remote Hub or an occupied callback port, copy the full address from the final browser tab into **Completed sign-in URL** and click **Finish ChatGPT sign-in**. The final tab may show a connection error at `http://127.0.0.1:1455/auth/callback`; copy its address anyway. This works when the Hub runs on a different computer or NAS. The Hub uses [OpenAI’s public-app sign-in flow](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), registers its own public OAuth client, keeps the registration and tokens encrypted, and uses PKCE. It does not need an OpenAI API key. An unfinished sign-in can be resumed from Settings after a refresh or Hub restart; restarting sign-in invalidates the earlier attempt.
- **Claude subscription:** install the [official native Claude Code app](https://code.claude.com/docs/en/setup), version **2.1.259 or newer**, on the Hub host. Run `claude auth login` as the same operating-system user that runs the Hub and choose your Claude account. Select **Anthropic / Claude → Claude subscription**, then **Check Claude connection**. The Hub calls the official app with tools, hooks, MCP and session persistence disabled; it does not read or copy Claude's credential files. For a container, Claude Code must be installed and signed in inside that container as the Hub user; the standard image does not include it.
- **API key:** choose **API key · provider API billing**, enter an OpenAI, Anthropic, or [Google Gemini API key](https://aistudio.google.com/apikey), and click **Connect and load models**. Discovery does not save a supplied key; **Save assistant settings** saves it encrypted. API calls use provider API billing and any eligible account credits.

After connecting, select a model, enable the assistant and save. ChatGPT subscription and API-key connections load their provider model catalog. Claude subscription offers the official `default`, `sonnet`, `opus` and `haiku` aliases; their availability depends on the signed-in plan, and this is not a live account-entitlement list. **Refresh models** reloads the selected connection. The Hub reads all model pages for Anthropic and Gemini. For Gemini it shows models that advertise content generation. Gemini uses your Google AI Studio project’s API quota and billing; a listing alone does not establish compatibility with the chat endpoint or inference permission. Manual model entry remains available.

Changing provider or connection method clears the previous catalog. Subscription calls never fall back to a saved API key. Subscription limits, model access and account settings still apply. **Disconnect ChatGPT** removes the Hub's saved ChatGPT registration and tokens; Claude sign-out is managed through its native app. Browser storage remembers only that ChatGPT setup is in progress, never authorization URLs, callback codes or credentials.

Enabling a provider does not start monitoring. Choose monitoring separately for
each brew. An opted-in Start waits for the new, matching machine session; this
pending choice expires after 15 minutes and is canceled if the Hub restarts.
Once bound, monitoring stays with that exact session and ends when it finishes
or another program starts. Saved advice appears with that brew and is included
in its JSON export. The AI can interpret available readings and suggest actions;
it cannot operate heaters, pumps, or machine controls.

### Equipment starting points

The C/S/Pro profiles use **5 L and approximately 50% efficiency** from Small Brew Companion's documented recipe-scaling guidance, with source links visible in Settings. The Pro's nominal 5 L batch is also supported by PicoBrew's published specifications. The Hub limits grain to 2 kg on these models and supports a lower working limit for your insert. Hop varieties sharing a physical slot share a first addition time and a combined weight limit. The schedule can revisit that load later in the boil. [Equipment profiles and sources](docs/equipment-profiles.md)

## Brewing workflow

1. Create an ingredient recipe in **Recipes → Add recipe → Create for a brewer**, import one, or save an editable copy from **Browse all**. The brewer chosen in the wizard sets the source batch size, ingredient limits, and hop slots.
2. Open the saved recipe, choose **Send**, and select the **Target machine**. The Hub uses the target’s profile and scales all measured ingredients in the same proportion to fit the grain basket, hop slots, and applicable batch limits. The resulting volume is an estimated yield.
3. Review the scaled ingredients, loading instructions, and program. The Hub routes supported mash and boil additions automatically. A source with ambiguous flow paths or an unsupported step carries a deployment blocker. Existing native programs retain their original schedule and model restrictions.
4. Choose **Send to machine** on a valid preview when the standalone brewer is ready. The Hub reads back the installed program. For an original Zymatic, choose **Stock Zymatic · sync menu** and **Publish to brewer menu**, then select the program on the brewer. [Stock Zymatic setup](docs/stock-zymatic.md)
5. On a compatible standalone brewer, choose **Open** on the transfer receipt to see the installed recipe selected on the brewer. Choose **Start recipe** and confirm **Start program** to send the REST start command. You can enable AI monitoring in that confirmation. If the brewer displays a preparation question, answer it using the Hub’s mirrored front panel.
6. Watch progress on the fleet or machine page, where you can also enable or disable monitoring for the current brew. The journal imports available retained samples. Open that brew's journal entry for notes, a rating, exports and optional AI result analysis.

A successful HTTP command response means the firmware accepted the request. It is not evidence that heating or pumping has begun. An uncertain command delivery is recorded and never automatically retried. Dedicated pause/resume appears only when advertised by the device. C/S/Pro retains its existing front-panel workflow; the Hub does not infer unsupported controls. See [family controls and history](docs/family-devices.md) for the Z and accessory behavior.

## Run with Docker

```sh
docker compose up --build -d
docker compose logs hub
```

Prebuilt multi-architecture images (`linux/amd64`, `linux/arm64`, and `linux/arm/v7`) are also published to `ghcr.io/jeffreydwalter/picobrew-hub:latest` (replace `build: .` with `image: ghcr.io/jeffreydwalter/picobrew-hub:latest` in `compose.yaml` to pull instead of building locally).

The default Compose file publishes port `8080` on the host so machines can reach the Hub, and persists the SQLite database and encryption key in the `hub-data` volume. Use the setup token from the logs to create the owner account. The image runs as a nonroot user with a read-only application filesystem.

The Compose project name is pinned to `hub`, preserving the default
`hub_hub-data` volume after moving the source directory from `hub/` to `server/`.
Existing installations that use a custom project name should continue supplying
that same `-p` option or `COMPOSE_PROJECT_NAME` value.

For a specific interface, change the published host address to the desired LAN address. Binding only to `127.0.0.1` deliberately prevents a physical machine from reporting back. For a public HTTPS site, terminate TLS at a reverse proxy and add `--secure-cookies` to the server command. Preserve the request Host header; do not cache `/api/` responses or buffer `/api/v1/events`. Example Caddy configuration:

```caddyfile
brew.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

A cloud host needs a private route to the machines, such as a site VPN. This release does not contain an outbound site agent or NAT traversal service. Keep the brewer's HTTP API on that private route; only the authenticated hub needs to be browser-accessible. The on-device UI works independently when the hub is unavailable.

## Firmware packages

Set the GitHub repository and optional branch in **Settings → Firmware**. The Hub checks daily and displays update notices on **Brewery**. On the machine page, open **Firmware** and choose **Check for Updates** to refresh immediately. Selecting **Install** or **Download** fetches the matching package on demand. An idle Pico S, Pro, C, supported Z module, PicoStill, or PicoFerm can install over Wi-Fi once USB setup has provisioned its recovery layout. Pico C also requires the package for its verified display controller. Both Zymatic profiles update through the Hub’s USB setup. [Firmware packages and restoration](docs/firmware-packages.md)

## Live readings and receiver settings

**Brewer preferences → Live readings** shows **Receiver host**, **Receiver port**, and **Send live readings**. New connections start with a verified address for this Hub. On a PicoFerm, **Device preferences → Live readings → Use this Hub** lets the Hub recover its receiver address after repeated delivery failures. The Hub keeps the previous address until it confirms a reading at the replacement. You can also choose a destination manually or turn forwarding off. [PicoFerm pairing and reconnection](docs/device-connections.md)

Settings can be saved while the machine is offline or busy. The Hub applies the saved values after reconnecting and when the brewer is idle, after verifying its identity. Saving confirms that the Hub retained the settings; the connection status separately reports whether they have reached the machine. Unchanged machine settings are not rewritten. The firmware website shows the destination and delivery status without a separate receiver form.

The Hub normally fills the initial host and port from its reachable address. A deployment-wide `--device-url http://hub-address:port` (or `HUB_DEVICE_URL`) can supply the initial HTTP origin for Docker/NAS port mappings or a private route behind an HTTPS browser proxy. Inferred destinations are verified as this Hub before being used. Once settings are saved, reconnects and restarts preserve them. The firmware sender uses HTTP and IPv4; the receiver must be reachable from the brewer. A deliberately loopback-only listener cannot accept a callback from another machine. These reporting limitations do not stop authenticated polling or history reads.

The Hub accepts the standalone envelope at `POST /api/v1/telemetry` after the device UID is known. That envelope is unsigned. The Hub stores it as an explicitly untrusted observation and does not use it to authorize commands or replace authenticated polled status. Full archived brew history comes from the authenticated history API. Reporting does not make an otherwise unreachable machine remotely controllable.

## Data and recovery

The data directory contains `hub.sqlite` (with its WAL/SHM files while running) and `master.key`. The key encrypts device and AI credentials in the database. Do not commit this directory.

The browser's **Export workspace** action downloads portable recipe, machine and brew records without passwords or API keys. It is a data export, not a full disaster-recovery image.

For a complete backup, stop the server, copy the **entire data directory**, and restart. Restore that directory to a stopped server using the same application version or a version with a compatible migration. Preserve `master.key` with the database; a new key cannot decrypt previous credentials. Docker users should stop the Compose service while archiving/restoring the `hub-data` volume. See [operations](docs/operations.md).

## Development and tests

Node is only needed for frontend development and its tests. Supported versions
are **22.22.2 or newer in 22.x**, **24.15.0 or newer in 24.x**, or **26.0.0 and
newer**. If you use `nvm` on macOS/Linux, `nvm install` followed by `nvm use`
selects the checked `.nvmrc` version, 22.22.2. Updating the repository does not
upgrade your installed Node runtime.

After changing frontend source, regenerate the committed website package with
`npm --prefix web ci` and `npm --prefix web run build:bundle`. Commit the bundle
and its manifest together with the source changes. CI rebuilds the frontend and
rejects stale bundle content or source hashes. Normal `go build` and
`make server-start` use this verified package without invoking npm.

```sh
# Go tests, including SQLite, HTTP adapters, concurrency and command outcomes
go test ./...
go vet ./...
python -m unittest discover -s tools -p "test_*.py"

# Frontend typecheck / optimized build and focused unit tests
npm --prefix web ci
npm --prefix web run build
npm --prefix web test

# Browser integration tests
cd web
npx playwright install chromium
npm run test:browser
```

Use `python3` instead of `python` if that is your Python command. The normal Hub
build and tests are CGo-free. Go's optional race instrumentation requires a C
compiler: use `go test -race ./...` or `make test GO_TEST_FLAGS=-race` in a
configured development environment.

The explicit process checks build an isolated workspace, exercise paths with
spaces and failed builds, hold an authenticated live event stream open, send a
native terminal interrupt, and verify saved data after restarting. From `server/`:

```sh
python tools/check_server_start.py
python tools/check_server_start.py --make
```

These checks use an empty machine registry, temporary data, and a process PATH
without Node or npm. They require Python only for testing; building the Hub
requires Go, and the one-command startup also uses Make.

If Chromium is installed separately, set `CHROMIUM_PATH` to its executable. The browser suite starts and stops its own isolated Hub.

In a full project checkout, the Python suite also compares catalog starters
with `../firmware/pico-s-pro/modified/data/starter-recipes.json`. An isolated
server checkout skips that cross-component check. Set
`CATALOG_REQUIRE_FIRMWARE=1` to require it, as the repository CI does.

For interactive development run the Go server and `npm --prefix web run dev`.
Vite proxies `/api` to the Go server. The server serves its embedded production
website by default; `--web web/dist` explicitly selects a local development
build instead. `HUB_PASSWORD` can provision a password noninteractively only
when no owner account exists. Existing owner credentials are never overwritten
by that variable.

## Project map

| Directory | Responsibility |
|---|---|
| `cmd/picobrew-hub` | Process configuration and graceful shutdown |
| `internal/server` | HTTP API, owner sessions, encrypted credentials, fleet orchestration, history import, optional AI monitor |
| `internal/device` | Standalone device adapters and capability discovery |
| `internal/recipe` | Canonical recipe validation, adaptation, native compilation, JSON/BeerXML exchange |
| `internal/catalog` | Pinned offline recipes, native programs, original records, illustrations and source provenance |
| `internal/store` | SQLite migrations, record versions, encrypted blobs, deduplicated telemetry |
| `internal/webui` | Embedded compiled website, dependency notices and build manifest |
| `internal/ai` | Bounded OpenAI, Anthropic, and Gemini clients with no machine tools |
| `internal/domain` | Shared server models |
| `web` | React/TypeScript UI, SCSS, unit and browser tests |
| `docs` | Architecture, operations, support coverage and verification record |

The implementation is independent of GoMVC and of the existing Python PicoBrew servers. Their public capabilities informed the product scope; protocol compatibility is based on the standalone firmware's published contract. See [architecture](docs/architecture.md) and [coverage](docs/coverage.md).
