# Operations

## Configuration

| Setting | Default | Meaning |
|---|---|---|
| `--listen` | `0.0.0.0:8080` (authenticated mode); `127.0.0.1:8080` (demo) | Hub HTTP bind address; network access permits machine reporting |
| `--device-url` / `HUB_DEVICE_URL` | Automatically inferred | Optional HTTP origin advertised to machines for advanced network mappings |
| `--data` | `data` | SQLite database and encryption key directory |
| `--web` | unset | Optional directory overriding the embedded website for development |
| `--secure-cookies` | false | Mark session cookies Secure; enable for an HTTPS reverse proxy |
| `--demo` | false | Loopback-only, hardware-rejecting simulator workspace |
| `HUB_PASSWORD` | unset | Provision the initial owner account without the setup form; ignored when an account already exists |

The health endpoint is `GET /healthz`. It reports process availability and version, not a successful connection to every brewer. Device connectivity is shown by `/api/v1/devices` after authentication.

Run one process for each data directory. Starting additional instances against the same database does not create a distributed fleet scheduler. The server shuts down its HTTP listener and cancels polling/history/AI workers before closing SQLite.

## Start and stop

Run `make server-start` from the repository root or from `server/` to build
the Go executable with its bundled website and run the Hub in the foreground.
Only Go and Make are required; Node and npm are not invoked.
Both entry points use `server/data` in a full checkout. An
isolated server copy uses its own `data` directory.

Without Make, run these commands from `server/` to build the Hub with the USB worker for the host platform.

```sh
go run ./tools/usb-prepare
go build -trimpath ./cmd/picobrew-hub
```

Start `./picobrew-hub --data ./data` on macOS/Linux, or
`.\picobrew-hub.exe --data .\data` in Windows PowerShell. Release executables already include their worker. [First-time setup](../../README.md)

The SQLite format and schema are unchanged by the pure-Go driver; existing data
does not require an export/import conversion.

Press **Ctrl-C** for a clean shutdown. The Hub stops accepting connections and
ends live browser event streams, then lets ordinary in-flight requests finish
before closing the database. Idle live views no longer hold shutdown open for
the 15-second request-drain deadline. A second Ctrl-C forces exit if a request
is genuinely stuck.

## Updates

Stop the hub and make a complete data-directory backup before changing versions.
Pull the new revision and run `make server-start` using the same data directory.
The website is bundled with that revision and embedded during the Go build.
Only frontend developers need to rebuild it with Node; CI verifies that the
committed bundle matches its source. The SQLite migration gate refuses unknown
newer schemas.

Machine identity is tied to its registration and archived history. Use a new registration to replace a machine with a different model or adapter. Updating an address for the same machine is supported. The hub pins the first authenticated hardware UID; a different UID makes that registration unavailable until the original device is restored or the replacement is registered separately. If the firmware is reformatted and native history counters reset, use a new registration rather than merging reused session IDs into its previous archive.

## Recover a native installation

The Hub data directory holds firmware-source settings, the package cache, and per-machine USB backups saved by the Hub. Keep the complete directory to preserve the original images available for stock restoration. **Settings → Firmware** selects the source. The machine’s **Firmware** view checks versions and fetches a package when you download or install it. Workspace exports omit the firmware cache, source settings, and private USB backups. [Firmware packages and restoration](firmware-packages.md)

For a shell-managed process, stop it first. With the server stopped:

```sh
PICO_HUB_BACKUP="$HOME/pico-hub-data-$(date +%Y%m%d-%H%M%S).tar.gz"
tar -czf "$PICO_HUB_BACKUP" data
```

Store this archive privately. It includes the encryption key and credential database. To restore, extract it into an empty directory, preserve permissions, and run the compatible hub with `--data` pointing at the restored `data` directory. Do not mix one backup's database with another backup's master key. Keep any existing data directory under a different name until the restored server is verified.

The browser workspace ZIP excludes authentication and provider/device credentials. It is intended for portable inspection and recipe/telemetry exports. Recipes can be imported through the normal recipe import flow; a workspace ZIP is not accepted as a full restore operation.

## Recover a Compose installation

`docker compose stop hub` stops writes. Back up the named `hub-data` volume using your Docker or NAS volume-backup facility, preserving all files and ownership (container UID/GID 10001). Restore into an empty volume while the service is stopped. Start the service and inspect `/healthz` and the fleet page. Keep the volume mounted at `/data`; do not place it in the image layer or source tree.

## Connection failures

- **Host cannot be resolved:** verify the address from the server's network namespace. A browser on your Mac resolving an mDNS name does not mean a remote server or Linux container resolves it. Use the brewer's current IPv4 address when appropriate.
- **Unauthorized from brewer.** Verify the machine URL first. If its device key changed, use **Manage machine → Edit connection → Replace saved key** with the key from that machine’s setup. The Hub owner password signs you into the Hub. [Machine connections and pairing](device-connections.md)
- **Uncertain command or mutation:** inspect the front panel/current stored recipe before issuing a new action. The hub deliberately records this state rather than retrying a command that may have reached the brewer.
- **History is partial:** reconnect while the session is still retained. The next sync retries incomplete data, but samples already overwritten on the device cannot be reconstructed.
- **AI returns an error:** check the selected connection method in Settings. For ChatGPT, check its connection or sign in again. For Claude subscription, verify native Claude Code 2.1.259 or newer is installed and `claude auth login` was completed as the Hub user on the Hub host. For API keys, check the selected provider's key, model permissions, quota and billing. Subscription failures never switch to API-key billing. Advice is optional; device polling/control and local history continue without an AI provider.

## Deployment notes

Use HTTPS for browser traffic outside a trusted local environment. The proxy should forward the original Host header and stream SSE without buffering. A cloud host needs a private route to the brewer LAN. Do not forward a brewer's HTTP API directly to the public internet.

The Hub does not send emails, SMS or webhooks to third parties. There is no external messaging configuration in this release. Optional AI context goes to the provider you configure. Machine reporting starts with this Hub as its receiver. Host, port, and the live-data checkbox remain editable in Brewer preferences. Saved values persist across reconnects and restarts and are applied when the machine is available and idle.

## Publish the Hub

A push to `master` builds the Hub for six host platforms, tests each embedded USB programmer, and waits for the Hub and firmware CI runs on that exact master commit before publishing. The first production tag is `v1.0.0`. Later builds increment the patch number. The release manifest records the Git commit and artifact hashes. If a newer commit reaches `master` during a build, its run publishes the new release. A failed build leaves the previous downloads available.

When the USB programmer, bundled firmware, or packaging inputs change, prepare the source pins before merging. This lets a source build and a release executable embed exactly the same programmer, including on a Raspberry Pi. The project’s GitHub Actions job builds and tests each candidate on its target platform.

1. Open a pull request and wait for all six **Package PicoBrew Hub** builds. Download the `usb-<platform>` artifacts from that workflow run, then unpack them into one directory, for example `/tmp/picobrew-workers`.
2. With the PR source checked out and the current release tags fetched, run `python3 server/tools/usb/release.py next-tag`. Use that tag and the Actions run and attempt numbers with the pin command, for example `python3 server/tools/usb/release.py pin v1.0.0 usb-candidate-123456-1 /tmp/picobrew-workers`. The command checks the workers, their notices, the bundled inputs, and every SHA-256 before updating `server/tools/usb-prepare/releases.json`.
3. Publish a temporary GitHub prerelease under the candidate tag, targeting the candidate source commit. Attach the six `picobrew-usb-*` files and six `usb-notices-*.zip` archives. Commit the generated pins to the PR and wait for CI. Both a clean source build and the production workflow verify those files against the committed hashes.
4. Merge. After the new production release appears, verify its `SHA256SUMS.txt` and its `releases.json` commit, then delete the temporary prerelease and its candidate tag. Future builds fetch the identical files from the production tag. Keep production releases referenced by current source pins.
