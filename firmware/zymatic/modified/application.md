# Zymatic standalone firmware

Both 1.0.0 builds use the brewing controller from PicoBrew 1.1.14. The Hub selects a build from the verified application space on each brewer. **About Zymatic** and the local API report version **1.0.0**. The API advertises the features the installed build supports. [Flash layout and compatibility evidence](flash-layout.md)

| Feature | Compact | Extended |
| --- | --- | --- |
| Stock recipe menu and physical dial | Yes | Yes |
| Sync recipes and sessions with a configured Hub over Ethernet or Wi-Fi | Yes | Yes |
| One USB-provisioned recipe available offline | Yes | Yes |
| Device website | Status with device key | Public temperatures and state, dial pairing and authenticated controls |
| Wi-Fi website and API | Home menu and read-only recipe pauses | Home menu, physical About pairing prompt, and read-only recipe pauses |
| Hub front panel | Read the idle display | Mirror the OLED and send a dial movement or press when the current screen accepts it |
| Brew history on the machine | Hub polling while connected | Eight retained session headers and a shared pool of 222 time-stamped samples |
| Firmware installation and stock restoration | USB | USB on a unit with a verified boot window |

Open **Settings → Appearance** on either device site and choose **System**, **Light**, or **Dark**. The browser remembers the selection for that brewer. System follows the operating system as its appearance changes.

The extended device website and Hub use the original menu for brew control. Turn and press act on the displayed selection. The machine rejects input if the screen has changed, the frame is stale, a fault is active, or the current screen requires local input. During a program, the remote dial can press the original button where the firmware permits it. Pairing approval always uses the physical dial. [Controller evidence](../reverse-engineered/README.md)

History records the measurements the Zymatic actually supplies. Interrupted sessions stay available after reboot. The Hub can import them when the brewer returns to the Home menu on Wi-Fi or reconnects over Ethernet. [Recorded channels and retention](history.md)

## Pair a brewer

Enter the address of the Zymatic’s active network interface in **Add machine**. For Wi-Fi, leave the brewer at the Home menu. Use Ethernet for monitoring throughout an active brew. Heating, circulation and draining use the original Wi-Fi client to send brew reports to the configured Hub. The local listener resumes at a programmed pause or when the brew returns home. With the extended build, compare the code in the Hub with the one on the brewer’s **About Zymatic** screen and approve it at the physical dial. The Hub stores the key. A paired owner can use **Manage machine → Open machine website**. A compact brewer uses the key saved during USB setup. [Original network protocols](../reverse-engineered/network/README.md)

## Installation and recovery

First-time USB setup uses the compact build, which fits below every supported boot boundary. USB verification reads the full flash twice. A backup of the stock application or an Optiboot filename alone cannot establish how much space a particular brewer reserves for its bootloader.

To qualify an expanded application window, capture the configured fuse and lock bits through the external programmer on the same provisioned brewer. The installer checks the prior paired UID and compares the live boot bytes against the independent capture before choosing an image. Both the private API key and identity carry forward. Stock restoration clears the recognized custom application extent and verifies the full flash after writing. [Boot evidence and recovery](recovery.md) · [USB setup](README.md)

The private reference package builder defaults to the compact profile. Use `tools/package.py --profile extended` to compose the extended image for simulator testing. Each manifest includes its build profile and minimum boot boundary. Keep personalized images in private storage. A generic HEX updater does not perform the Hub’s fit, identity or recovery checks.

CI compares the reconstructed OEM control paths and executes the overlay, screen handling, EEPROM recorder and packed OEM assets in an AVR simulator. Physical brewing, power-loss recovery on a real unit and installation of an expanded application still need hardware trials. Network firmware installation remains unavailable. [Compact build and memory details](compact-application.md) · [Offline recipe format](offline-recipe.md)

The extended device website serves `/favicon.ico` from the Zymatic itself. The file contains the original 16×16 frame of [PicoBrew’s favicon](https://picobrew.com/favicon.ico), with its original colors and transparency. The USB installer also recognizes both initial 1.0.0 profiles and previously provisioned development images from 1.2.0 and 1.3.0–1.3.3. It verifies the entire private application before preserving its identity, key and offline recipe.

On the extended build, select **Brew history** on the paired local site to read a retained
session. It shows the outcome and samples from all available pages, with elapsed
seconds, temperatures, flow path and step number. Each new front-panel image
appears after its complete, consistent OLED frame arrives. The site shows the
manufacturer's rinse, inline-filter and deep-clean intervals. [Zymatic manual, care and maintenance](https://picodevblob.blob.core.windows.net/content/zymatic/Zymatic_Manual.pdf#page=23)

On the extended build, the main menu and device site prompt for a rinse and an inline-filter check after a completed brew. After three brews they also recommend Deep Clean. A completed stock rinse acknowledges the rinse and filter reminder together. The operator inspects the filter. A completed stock deep clean resets the brew count and calls for a rinse. Interrupted runs leave prior reminders in place. The status and public overview report these reminders under `maintenance.due` when retained history is available.

The private installer uses the checked-in compressed site. To regenerate it
after a website edit, run `npm ci --ignore-scripts --prefix tools` followed by
`node tools/build_site.cjs` from this directory. CI checks the generated bytes
and their fit in the W5100 transmit buffer.
