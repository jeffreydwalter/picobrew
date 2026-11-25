# Stock Zymatic recipe sync

A stock Zymatic fetches its recipes from an HTTP server. The Hub can serve this request alongside its usual standalone-firmware API. The adapter follows the Wi-Fi and Ethernet request formats documented for firmware **1.1.14**. A physical sync and brewing trial are pending. [Firmware protocol reference](https://github.com/hotzenklotz/picobrew-server/blob/33bff7fd2234d26a911895acfcd54b741955cd1e/docs/PicoBrew-API.md)

## Send a recipe

1. Read the machine serial in **About Zymatic**. Connect the stock brewer to your LAN. Configure the brewer's LAN DNS to resolve `picobrew.com` to the Hub or a local reverse proxy. Older clients may also use `www.picobrew.com`. The brewer connects to TCP port **80**. For a Hub listening on 8080, forward the uppercase `/API/` routes from LAN port 80 to the Hub. Keep the owner interface on its usual address. This protocol uses plain HTTP and machine/account identifiers on the local network. Keep the stock routes off the public internet.
2. Save a recipe from the wizard using the **Zymatic** profile, or copy an original Zymatic program from the community catalog. For a saved ingredient recipe choose **Send**. For a native program or library selection use the recipe transfer action.
3. Choose **Stock Zymatic · sync menu**, enter the serial, and preview. For older unmapped steps or ingredient recipes designed for another model, choose a keg, mash, or hop path for each step. The preview shows the actual transmitted names, whole-degree Fahrenheit targets, hold and drain times. A program compiled for another machine stays in its original format.
4. Choose **Publish to brewer menu**. Select the **PicoBrew Hub** account on the brewer and open or refresh its recipe list. Published maintenance programs appear in the brewer’s cleaning list. Select a program on the machine to begin.

Publishing pins that library revision and leaves other menu entries in place. Editing a saved recipe requires publishing the new revision. **Remove from menu** takes effect on the next fetch. Each stock menu holds ten recipes, each with at most 25 steps. The Hub shortens display names to fit the stock buffers and rejects durations above 255 minutes and targets above 207 °F. A fetched menu is evidence of a request from the brewer. Its presence on the LCD requires device confirmation.

The adapter handles account setup, update checks, both spellings of recipe sync, session IDs and recipe/reset-point recovery for the current session. The firmware sends its own reset point. The Hub retains the selected program for recovery after its menu entry changes. Live stock telemetry charts and remote heating or dial controls are not implemented. Simulation keeps the external brewer routes off.

## Custom Zymatic 1.0.0

Set `--hub-host brewery.lan:8080` during USB setup to point both network clients directly at the Hub. On Ethernet, pair the brewer with its private API key. In **Send**, selecting that paired brewer opens the same menu workflow and fills in its reported stock serial when available. Choose a program on the brewer using the dial. Both build profiles provide wired status and OLED monitoring. A brewer with a verified 1 KiB boot section can also run the extended profile for paired dial input and retained history. [Firmware service](../../firmware/zymatic/modified/README.md)

## Download an offline program

In a saved recipe’s **Send** dialog, choose **Zymatic · offline program for USB setup**, then **Preview offline programs**. Review the steps and select **Use in USB setup**. Connect an idle Zymatic to the Hub computer and confirm installation. The Hub writes the reviewed schedule along with the firmware and verifies the complete flash. The machine stores one offline program. You can also choose **Download offline program** and select that JSON file in a later USB setup. The command-line installer accepts the same file with `--offline-program PATH`. [Zymatic USB setup](../../firmware/zymatic/modified/README.md)

## Compatibility beyond Zymatic

Standalone S, Pro, C and generic Z use the Hub's REST adapters. PicoStill and PicoFerm expose their respective standalone capabilities. The community server documents separate stock protocols for the Pico, Z-series, Still and Ferm families, so additional adapters are feasible on a model-by-model basis. A stock protocol can expose telemetry or fetch recipes without providing remote controls or OTA. [Community implementation](https://github.com/chiefwigms/picobrew_pico/tree/36acae3003845b72568ef5f7d6cf168acb6f9b69/app/main)
