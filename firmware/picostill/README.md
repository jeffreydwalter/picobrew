# PicoStill

The complete upstream images for this family are in [`oem/upstream/`](oem/upstream/).
They retain their source filenames and bytes; see the repository-wide
[import manifest](../upstream-manifest.json).

[`reverse-engineered/`](reverse-engineered/README.md) contains the recovered
application, controller, physical inputs/outputs, sensor acquisition,
calibration, network/server/storage behavior and diagnostic paths, with
original instruction replay and a complete pinned ESP32 SDK link. The six
preserved releases are application containers with verified segment checksums
and appended SHA-256.

The [standalone 1.0.0 firmware](modified/README.md) provides manual,
essential-oils and cleaning programs through the physical controls, device
website and Hub. The API reports four temperatures, pressure and fan speeds.
Pump and fan controls retain the native sensor interlocks.

Build with `make firmware MODEL=picostill`. The Hub checks the ESP32 flash
layout during USB setup and provisions recovery for later Wi-Fi updates.
Physical installation, calibration and operating trials are pending.
[Wireless updates](modified/README.md#wireless-updates)
