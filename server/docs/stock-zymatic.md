# Stock Zymatic recipe sync

A stock Zymatic fetches its recipes from an HTTP server. The Hub can serve this request alongside its usual standalone-firmware API. The adapter follows the Wi-Fi and Ethernet request formats documented for firmware **1.1.14**. A physical sync and brewing trial are pending. [Firmware protocol reference](https://github.com/hotzenklotz/picobrew-server/blob/33bff7fd2234d26a911895acfcd54b741955cd1e/docs/PicoBrew-API.md)

## Send a recipe

1. Read the machine serial in **About Zymatic**. Connect the stock brewer to your LAN. Configure the brewer's LAN DNS to resolve `picobrew.com` to the Hub or a local reverse proxy. Older clients may also use `www.picobrew.com`. The brewer connects to TCP port **80**. For a Hub listening on 8080, forward the uppercase `/API/` routes from LAN port 80 to the Hub. Keep the owner interface on its usual address. This protocol uses plain HTTP and machine/account identifiers on the local network. Keep the stock routes off the public internet.
2. Save a recipe from the wizard using the **Zymatic** profile, or copy an original Zymatic program from the community catalog. For a saved ingredient recipe choose **Send**. For a native program or library selection use the recipe transfer action.
3. Choose **Stock Zymatic · sync menu**, enter the serial, and preview. For older unmapped steps or ingredient recipes designed for another model, choose a keg, mash, or hop path for each step. The preview shows the actual transmitted names, whole-degree Fahrenheit targets, hold and drain times. A program compiled for another machine stays in its original format.
4. Choose **Publish to brewer menu**. Select the **PicoBrew Hub** account on the brewer and open or refresh its recipe list. Published maintenance programs appear in the brewer’s cleaning list. Select a program on the machine to begin.

Publishing pins that library revision and leaves other menu entries in place. Editing a saved recipe requires publishing the new revision. **Remove from menu** takes effect on the next fetch. Each stock menu holds ten recipes, each with at most 25 steps. The Hub shortens display names to fit the stock buffers and rejects durations above 255 minutes and targets above 207 °F. A fetched menu is evidence of a request from the brewer. Its presence on the LCD requires device confirmation.

Open **Brew journal** during a stock or custom Zymatic brew to follow reports from its Wi-Fi or Ethernet client. The session page refreshes while open. Its graph shows wort, primary heater, control board and backup heater temperatures as the brewer sends them, normally at most once per minute. The timeline shows step changes and error or reset reports. The JSON and CSV exports include the readings, step text, reported states and error codes. Times are when the Hub received a report. A missed Wi-Fi step report can leave a gap in the timeline. A paired custom Zymatic's separately imported on-device history is labeled **Machine history**.

The Hub keeps the original recipe and the brewer's reset point so an interrupted session can resume even after the menu changes. Error reporting alone does not close a recoverable session. The brewer's end message carries no outcome, so the journal marks the session **Ended**. The stock Zymatic transfer dialog shows the latest 32 errors that the brewer reported outside a known session. First setup registers the four sensor identifiers by channel. The stock firmware check reports no newer OEM release. A custom firmware installation uses the Hub's USB workflow. Stock heating and dial controls remain on the brewer. Simulation keeps the external brewer routes off.

## Custom Zymatic 1.0.0

Set `--hub-host brewery.lan:8080` during USB setup to point both network clients directly at the Hub. On Ethernet, pair the brewer with its private API key. In **Send**, selecting that paired brewer opens the same menu workflow and fills in its reported stock serial when available. Choose a program on the brewer using the dial. Both build profiles provide wired status and OLED monitoring. A brewer with a verified 1 KiB boot section can also run the extended profile for paired dial input and retained history. [Firmware service](../../firmware/zymatic/modified/README.md)

## Download an offline program

In a saved recipe’s **Send** dialog, choose **Zymatic · offline program for USB setup**, then **Preview offline programs**. Review the steps and select **Use in USB setup**. Connect an idle Zymatic to the Hub computer and confirm installation. The Hub writes the reviewed schedule along with the firmware and verifies the complete flash. The machine stores one offline program. You can also choose **Download offline program** and select that JSON file in a later USB setup. The command-line installer accepts the same file with `--offline-program PATH`. [Zymatic USB setup](../../firmware/zymatic/modified/README.md)

## Physical acceptance check

Use this procedure with a stock 1.1.14 Zymatic and a Hub reachable on its LAN. Record the Hub revision, firmware shown in **About Zymatic**, connection type and local test date. This physical check is pending.

1. Configure DNS and LAN port 80 as described above. Publish one distinct, previously reviewed program for the correct serial. Record the recipe name, revision and steps shown in the Hub preview.
2. On the brewer, select the **PicoBrew Hub** account and refresh its recipes. Confirm that the expected name appears on the actual LCD. Record the display observation and the Hub's menu-request time separately.
3. Prepare the machine for that program according to its owner manual and loading instructions. Select and start it at the physical dial. In **Brew journal**, verify that the session appears for this brewer and that reported temperatures and step changes agree with observations at the machine.
4. When the operator ends the session normally, verify that the journal records **Ended** and retains its final reports after reloading. Download the CSV and check the observed times and sensor fields against the journal. Record any missing report or mismatch and the network interface used. If both Wi-Fi and Ethernet must be qualified, repeat the check on the other interface.

## Compatibility beyond Zymatic

Standalone S, Pro, C and generic Z use the Hub's REST adapters. Original Pico S, Pro and C use separate menu and report routes. Stock Z uses its local Program Maker interface. PicoStill and PicoFerm expose their respective standalone capabilities. [Connect a stock Pico or Z](stock-brewers.md) · [Community implementation](https://github.com/chiefwigms/picobrew_pico/tree/36acae3003845b72568ef5f7d6cf168acb6f9b69/app/main)
