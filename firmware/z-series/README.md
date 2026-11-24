# Pico Z family firmware

The complete upstream images for this family are in [`oem/upstream/`](oem/upstream/).
They retain their source filenames and bytes; see the repository-wide
[import manifest](../upstream-manifest.json).

[`reverse-engineered/`](reverse-engineered/README.md) contains the recovered
Z119 Core/Head application, program runtime, peripheral/network backends,
physical interface and diagnostic commands. It links with the pinned ESP32
SDK. Its per-module evidence distinguishes complete original execution,
returning assertions, modeled SDK boundaries and earlier guarded comparisons.

The [standalone 1.0.0 firmware](modified/README.md) provides a local website
and authenticated API, saved recipes, maintenance programs, brew history and
interrupted-brew recovery. Build it with `make firmware MODEL=z-series`.

The build/API identity is **`z-series` / Pico Z**. For USB conversion, the Hub
checks each module's active stock 0.0.119 application and its flash layout. A
Z1–Z4 stack label cannot identify a compatible module. Physical installation
and brewing trials are pending. [Module identification and USB setup](modified/README.md#usb-identification-and-stock-restoration)
