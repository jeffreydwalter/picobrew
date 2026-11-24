# Pico S/Pro web recovery candidate

This directory preserves the independent **0.1.0 web recovery candidate** from
commit `256751aa57741fd4e75ecba685c91ceea28d6794`. It is a separate application
intended for `/sys/mcuimg1.bin`, with an embedded webpage, authenticated package
uploads, full SHA-256 readback verification, and explicit application-slot boot
selection. It does not run the brewing controller.

This archived 0.1.0 candidate has host and build evidence only. The Hub USB
installer provisions the separate **1.0.0 Hub recovery** used for wireless
updates. Use the [Hub setup and update guide](../docs/hub-ota.md) for a
working brewer. The [candidate design](docs/web-recovery.md) describes the
older experiment and its hardware testing limits.

The archived source, build helper, linker and package retain their original
identities. The stable brewing application has its own memory layout.

## Build and check

Run these commands from `firmware/pico-s-pro/modified/recovery/`:

```sh
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements-test.txt
make bootstrap
make verify
python -m pip install -r requirements-web-test.txt
python -m playwright install chromium
make web-test
```

`PICOBREW_ARM_PREFIX` and `PICOBREW_SDK` can select an existing pinned compiler
and SDK. The compiler prefix includes the trailing `arm-none-eabi-`.

`make verify` checks every imported source hash, runs the portable core, HTTP
and simulated native-adapter tests, rebuilds the ARM candidate, and compares
the binary and source identities with the pinned package. Outputs stay in the
ignored `build/` directory. The separate browser suite exercises the real
portable recovery API over local HTTP with simulated files. These commands do
not communicate with a physical brewer.

| Artifact | Identity |
| --- | --- |
| Pinned candidate | `firmware/recovery/pico-web-recovery.bin` |
| Rebuilt candidate | `build/recovery/pico-web-recovery.bin` |
| Size | 42,984 bytes |
| SHA-256 | `55b63c1c15c829ac131ab57a44310604d136fb285935be1e98527061cea20593` |

## Preserved boundaries

Application uploads are restricted to already allocated image2/image3 files;
the candidate cannot create or relocate an application slot. Its own image1,
the secondary loader, keys, Wi-Fi files and raw flash are outside the upload
API. Uploads do not select or start an application. Boot selection is a separate
operation after the complete package is verified. Existing application slots
share their browser-file locations, so the candidate does not provide automatic
A/B rollback with independently retained browser bundles.

## Source record

[import-provenance.json](import-provenance.json) records the source commit and
all imported file identities, including the latest real-HTTP browser tests and
historical validation record. The original recovery source, package, design
notes and workflow are retained verbatim; the active workflow is at
[the repository root](../../../../.github/workflows/recovery.yml).

The imported design and validation documents describe their original branch
and the then-current standalone 1.0.0 release. Those historical references do
not override the current main-firmware documentation or establish a new CI or
hardware result. The older shared installation guide was already superseded
by the current guide and is not copied over it. Dependency notices remain under
`LICENSES/`; repository-wide attribution is in
[LICENSES.md](../../../../LICENSES.md).
