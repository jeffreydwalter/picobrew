# PicoFerm

The complete upstream images for this family are in [`oem/upstream/`](oem/upstream/).
They retain their source filenames and bytes; see the repository-wide
[import manifest](../upstream-manifest.json).

[`reverse-engineered/`](reverse-engineered/) contains verified container and memory
analysis for all 16 preserved releases. These are ESP8266 Arduino images, each
containing a first-sector eboot image followed by an application at offset 0x1000.
Both component checksums are verified. The latest 0.2.7 image embeds Arduino
ESP8266 2.4.1 source paths.

The recovered application has a complete pinned SDK link and original execution
comparisons. [Standalone 1.0.0](modified/README.md) records temperature,
pressure and battery readings. You can start a recording on the PicoFerm
or its website and link it to a brew in the Hub. The PicoFerm reports
directly to the paired Hub and keeps Wi-Fi active between readings.

Build with `make firmware MODEL=picoferm`. USB setup checks for a compatible
2 MiB flash layout and provisions two application banks for subsequent Wi-Fi
updates. Physical installation, calibration and battery-life trials are pending.
[Wireless updates and recovery](modified/dual-bank-updates.md)
