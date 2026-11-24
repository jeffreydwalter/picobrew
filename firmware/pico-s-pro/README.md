# Pico S/Pro

| Directory | Contents |
| --- | --- |
| `oem/device/` | Verified 0.1.34 application image extracted from the Pico S backup historically labeled “Pico Pro” |
| `oem/upstream/` | Unmodified files from the pinned Chiefwigms firmware archive |
| `reverse-engineered/` | Exact buildable OEM reconstruction before our v1.x features, from commit `ea887709e72c493456583114034cde4aad746099` |
| `modified/` | Stable 1.0.0 firmware for Pico S and Pico Pro, device website, recipes, Hub recovery and tests |
| `modified/recovery/` | Separate experimental 0.1.0 image1 recovery candidate, its original source/package and independent build/tests |

Both source folders retain their version-specific build and device tools under
`tools/`, their dependency manifests, tests and documentation. They can be built
from their own directory. General repository tooling lives at root `tools/`.

The pre-v1.x reconstruction retains its documented buffer, I/O and allocator
repairs; it is not recovered original vendor source or a byte-identical OEM
rebuild. Its earlier `legacy/` directory is decompiler evidence. The exact
snapshot is checked by `source-manifest.json`; develop new features in
`modified/`.

## Build from the repository root

```sh
make firmware MACHINE=pico-s-pro
make firmware-test MACHINE=pico-s-pro
make oem MACHINE=pico-s-pro
```

Bootstrap each source before its first ARM build: `make firmware-bootstrap` for
the modified firmware, and
`make -C firmware/pico-s-pro/reverse-engineered bootstrap` for the OEM reconstruction.
Follow the respective component guide for Python dependencies.

## Hub recovery

USB setup installs the model-specific brewing application, its website and
the shared 1.0.0 Hub recovery image. A provisioned Pico S or Pro can enter
recovery for a wireless update and confirm the new application after restart.
[Setup and update instructions](modified/docs/hub-ota.md)

The [archived 0.1.0 web recovery candidate](modified/recovery/README.md)
has its own pinned source, build and validation records.

For the detailed current status and installation instructions, start with
[modified firmware](modified/README.md). The [OEM reconstruction README](reverse-engineered/README.md)
records its own evidence and limits. The Hub server is at root [`server/`](../../server/).
