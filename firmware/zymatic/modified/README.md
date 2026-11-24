# Zymatic firmware and USB setup

Zymatic **1.0.0** offers a local website and authenticated monitoring over Ethernet. Recipe and session sync work over Ethernet or Wi-Fi. One recipe is available offline after commissioning. The original controller runs the brew and enforces its interlocks. USB installation, restoration and runtime behavior have been exercised in simulation. Physical qualification is pending. [Application and verification](application.md) · [Offline recipe selection](offline-recipe.md)

## Set up from the Hub

1. Connect the idle, powered Zymatic's rear USB port to the computer running the Hub. Close PicoFlash and any serial monitors. The downloadable Hub includes the programmer for its platform.
2. Open **Brewery → Add machine → USB setup and recovery**. Choose the Zymatic connection and **Install standalone firmware**. The Hub compares two flash reads, saves the verified backup and shows the firmware it can install. Follow the prompts and keep power and USB connected through readback.
3. After installation, check **About Zymatic** for **1.0.0**. Connect the brewer to Ethernet, choose **Add machine** in the completed USB job and enter the brewer's network address. The Hub uses the device key it saved during installation. Keep a private backup of the Hub data directory.

First conversion from stock **1.1.14** installs the compact build. The extended build adds remote dial controls and retained history on a unit with independently verified boot space. Both builds use USB for firmware updates. In the same USB dialog, choose **Back up original firmware** for a backup or **Restore original firmware** to return to stock. [USB setup and restoration](../../../server/docs/firmware-packages.md#usb-setup-backup-and-stock-restoration)

## Back up or restore from a source checkout

For direct command-line use, install Python 3, `pyserial==3.5` and AVRDUDE **8.1** on the USB computer. Put the AVRDUDE executable on `PATH` and keep its packaged configuration installed. List local USB ports with `python3 -m serial.tools.list_ports -v` and select the PicoBrew port labeled `20A0:421E`. Opening the bootloader can reset the controller. [AVRDUDE 8.1 releases](https://github.com/avrdudes/avrdude/releases/tag/v8.1) · [PicoBrew updater evidence](../oem/README.md)

Run these commands from the firmware checkout and substitute the actual data directory and port, for example `COM4` on Windows. Run backup to keep a private copy of the controller’s readable flash. Use restore when you intend to replace its application with stock 1.1.14.

```sh
python3 firmware/zymatic/modified/tools/usb_stock.py --data /path/to/hub/data --action backup --port SERIAL
python3 firmware/zymatic/modified/tools/usb_stock.py --data /path/to/hub/data --action restore --port SERIAL
```

Native restore also requires an identifiable USB serial number or USB bus location. Both commands check the USB identity and AVR signature, then read the complete 128 KiB flash twice. A matching read pair is saved under `usb-stock/zymatic/avr-flash-<sha256>.bin` in private Hub data. A matching known stock application is cached separately for reuse. Read protection can limit what the bootloader reports. The capture covers the AVR flash visible over USB. The separate EEPROM, fuses, lock bits and CC3000 Wi-Fi controller are outside its scope. The read-only backup also saves a private report fingerprinting the four possible AVR boot windows. [Bootloader identification and network recovery requirements](recovery.md)

Restore prefers the verified saved stock application. You may also supply an exact original ZIP or HEX with `--oem-file FILE`. If neither a stock prefix nor the HEX is available locally, the helper retrieves the [pinned public archive](../oem/README.md) and verifies both the archive and the HEX. A mismatching local copy stops restoration. The original setup archive can also be prepared on another computer and copied over for offline use.

If stock 1.1.14 is already present the command finishes after verifying it. Otherwise it asks for two confirmations on the local terminal. Keep power and USB connected while it rechecks the full baseline on the same connection used for writing, writes the application pages and reads all 128 KiB back through a new connection. AVRDUDE’s `-D` disables a chip erase. On a stock-only restoration the page-aligned write preserves the 54 adjacent bytes after the application. Removing a recognized custom extension clears its owned pages through the verified application boundary. Both operations preserve boot flash. The tool compares the full readback to the original application plus the captured surrounding bytes. Disconnect USB afterward, restart the machine and check **About Zymatic** for **1.1.14**. [AVRDUDE command options](https://avrdudes.github.io/avrdude/8.1/avrdude_4.html)

## Install the custom application from a source checkout

From the checkout’s `server/` directory, use a brewer running the verified stock 1.1.14 release. Substitute its USB port, the Hub’s LAN address and port, and a private key path in a directory you own.

```sh
./picobrew-hub usb-install --model zymatic --port SERIAL_PORT --checkout .. \
  --data ./data --key-file "/absolute/private/zymatic.key" --hub-host brewery.lan:8080
```

Setup captures two matching flash reads, checks the original application and available flash, then asks for two confirmations before it writes. It generates a unique UID and key for that brewer. Keep the key and the private Hub data directory for later updates and restoration. To set the recipe offered offline, add `--offline-program /absolute/path/zymatic-program.json`. The default is American Brown Ale. On an already converted brewer, omitting the recipe or host retains the installed value.

Disconnect USB after verification and check **About Zymatic** for the version reported by the installer. Initial setup installs the compact **1.0.0** build. An independent boot measurement can qualify the same brewer for the extended **1.0.0** build. The USB installer reports which profile it selected. On Ethernet, pair the brewer's IP address and key in the Hub. Use the recipe menu on the brewer for both Wi-Fi and Ethernet sync. To return to stock, run the restore command above against the same Hub data directory. Restoration clears the recognized extension and its private key from program flash and checks the complete readable result.

## Use the original Windows updater

Prepare the original 1.1.14 HEX by running the following command. It reuses the verified local HEX or downloads and verifies it from the pinned archive. Add `--oem-file FILE` to import an original ZIP or HEX you already have.

```sh
python3 firmware/zymatic/modified/tools/usb_stock.py --data /path/to/hub/data
```

On the Windows computer running PicoFlash, connect the idle Zymatic over USB, power it on, choose the prepared HEX with **[…]** and select **Flash Now**. If the Hub is on a different computer, copy the verified HEX to the PicoFlash computer first. Follow PicoBrew's power and USB instructions and confirm **1.1.14** in **About Zymatic** after restart. [PicoFlash archive](https://github.com/chiefwigms/picobrew_pico/blob/36acae3003845b72568ef5f7d6cf168acb6f9b69/app/static/support/legacy/common/PicoFlash.zip) · [PicoBrew troubleshooting guide](https://picobrewcontent.blob.core.windows.net/content/zymatic/ZYMATIC_TROUBLESHOOTING.pdf#page=16)

## Validation

Run `python3 -m unittest discover -s firmware/zymatic/modified/tests` for the offline regression suite. On macOS or Linux with AVRDUDE 8.1 installed, prefix the command with `AVRDUDE_SERIAL_TEST=1` to exercise the actual AVRDUDE executable against a simulated USB serial bootloader. This checks the exact application page writes, preservation of surrounding bytes and the refusal to write after a baseline mismatch. The host workflow has been exercised in simulation. A physical Zymatic backup and restore remain to be qualified.
