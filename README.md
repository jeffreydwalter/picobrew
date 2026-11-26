# PicoBrew

Brew with your own recipes and keep your brew history on your own network. This project provides **standalone firmware** for PicoBrew appliances and an optional **PicoBrew Hub** that runs on a Mac, Windows PC, Linux computer, or Raspberry Pi.

Pico S, Pico Pro, Pico C, Z-series modules, and Zymatic each serve a local website and API when running this project’s standalone firmware. You can open the brewer’s network address in a browser even when the Hub is off. For Zymatic, open its site at the Home menu over Wi-Fi or use Ethernet for monitoring throughout a brew. The brewer handles heat, pumping, and timing and can run a locally stored recipe from its dial.

The first public release is **1.0.0** for the Hub, its USB tools, and every custom firmware build.

The optional Hub lets you plan grain and hop loads, send recipes to different machines, follow simultaneous brews, and keep a searchable journal on your own computer.

![The Brewery page showing three machines and brew progress](docs/images/brewery.png)

## What you can do

| Feature | How it works |
| --- | --- |
| **Build a recipe** | Start from the offline catalog, import BeerXML, or use the wizard. Make a shopping list from your saved ingredient recipes. Assign hops to timed additions, then preview the grain bill, hop-slot loads, estimated yield, and steps for your brewer. The Hub keeps ingredient proportions when scaling and explains what prevents a transfer. |
| **Brew from your browser** | Send a program to an idle, compatible brewer, review it, and start it as a separate action. Follow multiple brews on the Brewery page. Open a machine to read its screen, answer prompts using the on-screen dial, and use the controls its firmware reports. |
| **Keep a brew journal** | Inspect temperatures and machine state together, pin notes to readings, and export CSV or JSON. Track fermentation stages and carbonation. If you use a PicoFerm, attach its recording to the batch so the readings stay with the beer when you move the monitor to another keg. |
| **Look after your machine** | See when your machine is due for a rinse or cleaning. The Hub shows the tasks reported by its firmware. Back up stock firmware and install compatible updates from the Hub. The Hub offers wireless updates when the connected machine reports compatible recovery support. |
| **Connect other tools** | Explore the Hub and device APIs. Optionally connect a brew assistant to draft a recipe or discuss a brew's recorded measurements. |

[See the feature tour and screenshots](docs/hub-tour.md) · [Compare firmware features by machine](docs/firmware-parity.md)

## Set up the Hub

### 1. Download the Hub for your computer

Choose your file below. Put it in a permanent folder called `PicoBrew`. The executable includes the website, recipe catalog, and USB programmer. Use the computer that will stay on your brewery network.

| Computer | Download |
| --- | --- |
| Mac | [Apple silicon](https://github.com/jeffreydwalter/picobrew/releases/latest/download/picobrew-hub-darwin-arm64) · [Intel](https://github.com/jeffreydwalter/picobrew/releases/latest/download/picobrew-hub-darwin-amd64) |
| Windows | [64-bit Intel or AMD](https://github.com/jeffreydwalter/picobrew/releases/latest/download/picobrew-hub-windows-amd64.exe) |
| Linux with glibc | [64-bit Intel or AMD](https://github.com/jeffreydwalter/picobrew/releases/latest/download/picobrew-hub-linux-amd64) · [64-bit ARM](https://github.com/jeffreydwalter/picobrew/releases/latest/download/picobrew-hub-linux-arm64) |
| Raspberry Pi | [64-bit Raspberry Pi OS](https://github.com/jeffreydwalter/picobrew/releases/latest/download/picobrew-hub-linux-arm64) · [32-bit Raspberry Pi OS on ARMv7 or newer](https://github.com/jeffreydwalter/picobrew/releases/latest/download/picobrew-hub-linux-arm) |

On a Mac, **Apple menu → About This Mac** shows the chip. On Linux, run `uname -m` to check the architecture. Choose Intel/AMD for `x86_64`, 64-bit ARM for `aarch64`, or the 32-bit Pi download for `armv7l`.

The release includes SHA-256 checksums and identifies the source revision used for the builds. [Release notes and checksums](https://github.com/jeffreydwalter/picobrew/releases/latest) · [Build from source](server/README.md#run-locally-on-windows-macos-or-linux)

### 2. Start the Hub

**Mac, Linux, or Raspberry Pi.** Rename the downloaded executable to `picobrew-hub`. Open a terminal in your `PicoBrew` folder and run these commands.

```sh
chmod +x ./picobrew-hub
./picobrew-hub --data ./data
```

**Windows.** Open the `PicoBrew` folder in File Explorer, type `powershell` in the address bar, and press Enter. Run this command.

```powershell
.\picobrew-hub-windows-amd64.exe --data .\data
```

Open **[http://127.0.0.1:8080](http://127.0.0.1:8080)** on that computer. For a headless Pi, open `http://<Pi's LAN IP>:8080` on another computer on the same network. Enter the setup token printed in the terminal and choose an owner password. Leave the Hub running while you use it.

Press **Ctrl+C** to stop the Hub. Restart it from the same folder to reopen your brewery. For a backup, stop the Hub and copy the entire `data` directory, including its encryption key. [Backup and operations](server/docs/operations.md)

### 3. Connect your machine

**If your brewer already runs standalone firmware**, skip to **Pair the brewer** below.

#### Convert a stock machine over USB

1. Connect the machine’s USB cable or service adapter to the **computer running the Hub**. If your Hub runs on a Raspberry Pi, plug it into the Pi. For a **Pico S, Pro, or C** with the six-pin service header, unplug the brewer from power before fitting the SparkFun USB-to-serial adapter. Match the board and adapter markings, including **GND**. Chiefwigms’s Pico C photos show where to look. [Board and circled service header](https://github.com/chiefwigms/picobrew_pico/issues/354#issuecomment-1165734746) · [SparkFun adapter and connector photos](https://github.com/chiefwigms/picobrew_pico/issues/354#issuecomment-1165779398)
2. In the Hub, open **Brewery → Add machine → USB setup and recovery**. Select the USB connection and the model printed on your machine. Choose **Install standalone firmware**. The Hub handles the backup, installation, and verification for supported hardware.
3. Follow the power-on or reset prompts in the browser. Keep USB connected until the Hub reports that verification is complete. Keep a private backup of the Hub’s `data` directory so you have the original firmware and pairing information. [USB setup and restoration by model](server/docs/firmware-packages.md#usb-setup-backup-and-stock-restoration)

![Hub USB setup showing the selected USB connection, Pico S model, and Install standalone firmware button](docs/images/usb-setup.png)

#### Pair the brewer

Join your brewery network, then open **Brewery → Add machine**. Choose your device under **On your network**, or enter its address manually. After Hub USB setup, continue with **Add machine** from the completed job so the Hub can use the pairing information it saved. For dial pairing on a Pico S, Pro, C, or Z, open **Settings → About → Pair with Hub**. On a pairing-capable Zymatic, open **About Zymatic**. Compare the code and approve it at the dial. Adding a standalone PicoFerm sets it up to send readings directly to the Hub. Open **Ferment** on the brewer’s Hub page to link it to that brewer or a batch. [Pairing instructions](server/docs/device-connections.md)

With factory firmware, **Pico S, Pro and C** can fetch recipes and report brew sessions. A **Z module on 0.0.119** can share live readings and accept a single Program Maker recipe for dial start. A **Zymatic on 1.1.14** can fetch recipes and report four temperature channels, step changes and errors. [Connect a stock brewer](server/docs/stock-brewers.md) · [Stock Zymatic setup](server/docs/stock-zymatic.md)

#### Install later updates from the Hub

Open the paired machine’s page and click the **Firmware** version beneath its front panel. Choose **Check for Updates**, select a compatible version, and click **Install** while the brewer is idle. The Hub downloads the package, transfers it, and checks the version after the brewer restarts. **Install** appears when the machine supports wireless updates and has compatible recovery firmware. If you installed a development build such as 1.3.8, update the Hub first. Its Firmware page can offer the public 1.0.0 release even though the number is lower. Brewers without compatible recovery need USB setup. The Pico S below shows the wireless flow. For a USB-only machine, use **USB setup and recovery**. [Firmware updates by model](docs/firmware-parity.md)

To change the website colors, open **Settings → Appearance** on the brewer, or **Settings → Display preferences → Appearance** in the Hub. Choose **Light**, **Dark**, or follow your system setting. Your browser remembers a separate choice for the Hub and each machine address.

On the revised Pico S, Pico Pro, Pico C and Z firmware, select **Brew Recipe** on the machine to choose a saved recipe. Recipes created in the Hub and imported PicoPak programs appear in the same list.

![Machine firmware dialog on a Pico S showing the installed version, a different version selected, and the Install button](docs/images/hub-firmware-update.png)

### 4. Send your first recipe

Open **Recipes → Browse all**, choose a recipe, and copy it to your library. Open the saved recipe, choose **Send**, select your brewer, and review the scaled quantities and schedule. After transfer, use the receipt to open the machine. Check the physical load and preparation prompts before choosing **Start recipe** on a compatible machine. [Recipe transfers](server/docs/recipe-transfers.md)

To brew together, open **Brewery → Group machines** and choose the individual brewers, including each Z module in a stack. Start a recipe on one member and accept the invitation on each brewer you want to join. For a Zymatic using its recipe menu, accept in the Hub and start the staged recipe at the dial. For a stock Z, offer a staged recipe from the Hub. On each receiving stock Z, confirm the working recipe replacement and start at the dial. Standalone participants keep separate linked brew journals. [Group Brewing by model](docs/firmware-parity.md#group-brewing)

## Which machines work?

| Machine | What to expect |
| --- | --- |
| **Pico S and Pico Pro** | Model-specific standalone firmware, local website, mirrored OLED and dial, saved recipes, history, and Hub control. An idle brewer with compatible recovery supports wireless updates. Wireless return to stock has narrower hardware requirements. |
| **Pico C** | Standalone brewing and Hub integration for the SSD1325 and SSD1309 display variants. USB setup provisions recovery for the verified display controller. The Hub can then send matching updates over Wi-Fi. Both display variants still need physical installation and brewing trials. |
| **Z-series** | With standalone firmware, manage saved recipes directly from the Hub or the brewer’s website. Send an ingredient recipe, a Program Maker program, an archived Z schedule, or an original PicoPak program if you have the pack and adapter. On stock 0.0.119, stage a compatible recipe on an explicitly selected Program Maker module, or import a Hub export on its website. Start at the dial. The shared standalone build runs on individual Z modules. Z1 through Z4 indicate the number of modules in the stack. USB setup checks each module’s active stock firmware and flash layout. A provisioned module can update over Wi-Fi, or over Ethernet with the wired-capable recovery image. Physical qualification remains pending. [Z module requirements](firmware/z-series/modified/README.md#usb-identification-and-stock-restoration) |
| **Zymatic** | Stock 1.1.14 can fetch Hub recipes. Both custom 1.0.0 builds add an offline recipe, Ethernet monitoring, and Wi-Fi access from the Home menu and programmed recipe pauses. The extended build also supports paired dial control and retained history. The USB installer checks which build fits the board. Updates use USB. Physical qualification remains pending. |
| **PicoFerm** | Record fermentation temperature, pressure on the sensor’s native scale, and battery voltage. The PicoFerm records while its Wi-Fi radio sleeps and reports directly to the Hub while the brewer is off. A short press opens a two-minute management window. After USB setup provisions both application banks, later updates use Wi-Fi. Physical installation and battery runtime still need testing. |
| **PicoStill** | Use the device website or Hub to select a still mode, operate the pump and fans, and follow temperature and pressure. A unit provisioned with recovery over USB can receive application updates over Wi-Fi. A Z running updated standalone firmware can also pair with a PicoStill on original firmware 0.0.35. Physical operating and update trials are pending. [Pair a PicoStill with a Z](firmware/z-series/modified/README.md#using-a-picostill-with-the-z) |

## Project guides

- [Hub guide](server/README.md) · [Docker setup](server/README.md#run-with-docker) · [Hardware and feature coverage](server/docs/coverage.md)
- [Firmware by machine](firmware/README.md) · [Build and verify firmware](docs/platforms.md) · [Firmware updates and recovery](server/docs/firmware-packages.md)
- [Recipe catalog and original records](server/docs/community-catalog.md) · [Brewer capacities and scaling](server/docs/equipment-profiles.md)
- [Source provenance](docs/consolidation.md) · [Licenses and third-party notices](LICENSES.md)
