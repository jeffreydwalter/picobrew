# A brew day with PicoBrew Hub

The screenshots show the Hub and Pico Local interfaces with example machine readings and a USB adapter fixture.

## See what’s brewing

Open **Brewery** to see all your machines together. Each brewer has its own row, so you can follow two brews at once. During a session, look for the current step, wort temperature and target. If a brewer needs attention, open its row to see the prompt. An offline machine stays in the list.

![Brewery showing multiple connected machines and their individual progress](images/brewery.png)

## Use the front panel

Choose **Open controls** on a brewer that supports remote control. **Front panel** mirrors its display. Use **Up**, **Down** and **Press dial** to navigate the menu and respond to prompts. The readings beside the screen come from that machine. A monitoring-only connection offers **View machine**.

Under **Saved recipes**, select an installed recipe and choose **Start recipe** when the brewer is loaded and ready. **Stop and lock outputs** stops heating and pumping on a supported brewer and keeps them off until you unlock them. When the brewer is already idle, the button reads **Lock outputs**. To open the brewer’s own site on a paired machine that supports it, choose **Manage machine → Open machine website**. When the machine reports a rinse, cleaning cycle, or other care task is due, the Hub shows the reminder on both its Brewery row and its machine page. **Cleaning and maintenance** gives the instructions for that model.

![Simulated Pico S showing its menu, dial controls and saved recipe selector](images/front-panel.png)

## Build a recipe and load the brewer

In **Recipes**, open **Browse all** to find a starting recipe. To start from scratch, choose **Add recipe → Create for a brewer**. That same menu offers **Import recipe** for an existing file and **Ask the brew assistant** when you want help drafting a recipe. The wizard takes you through **Brewer**, **Ingredients**, **Mash** and **Review**. Assign each hop variety to its physical slot. Varieties sharing a slot share an addition time, and their combined weight counts toward its capacity. The **Advanced editor** gives you individual mash rests and other recipe settings.

When you choose **Send**, select the destination and open **Preview recipe**. Check the estimated yield, grain weight and each hop load before filling the step filter. The Hub scales ingredients together to fit the brewer. If the source leaves an essential quantity or operation unresolved, the preview explains what prevents transfer. **Send to machine** saves the program. Starting it is a separate action. Stock Zymatic owners can retrieve a queued recipe through the machine’s sync menu. [Recipe transfers](../server/docs/recipe-transfers.md) · [Stock Zymatic](../server/docs/stock-zymatic.md)

![Hop slot two holding Cascade and Centennial, with a shared addition time and combined weight](images/recipe-builder.png)

To see how a catalog recipe fits your machine before saving it, open **Recipes → Browse all**, choose a recipe and select your brewer under **View brewer version**. Named **Hub adaptations** explain the choices made in reconstructing that version. In *Heinielicious (Unfined)*, the source gives no unit for the Irish moss, so this edition leaves it out. For the Pico S shown below, the loading table calls for **5.2 L of starting water** and estimates **4.07 L of wort**. Use the table for that brewer’s quantities and the timing of its three hop additions. **View original recipe** opens the archived source locally.

![Heinielicious Unfined edition showing its source review and the three hop additions scaled for a Pico S](images/recipe-edition.png)

## Review the brew and follow fermentation

Open **Brew journal** and select a session. On **Session measurements**, click or tap a reading to pin it. The chart aligns temperatures with machine state so you can see what changed at that moment. Add a timestamped note or use the arrow keys to inspect adjacent samples. **Session timeline** records reported transitions, and **CSV** and **JSON** export the session for comparison.

Use **Fermentation and carbonation** to track the stages of a planned brew after it leaves the brewer. Record when you start and finish each stage, then keep tasting notes and a rating with the session.

With a standalone PicoFerm on the keg, open that batch in **Brew journal** and select **Ferment**. Choose the monitor, start recording on the PicoFerm or its Hub page, then attach the recording to the batch. The session number and time distinguish recordings made on the same day. The PicoFerm reports directly to the Hub, so you can switch the brewer off. [PicoFerm pairing and recordings](../server/docs/device-connections.md)

![Brew journal with a session’s temperature traces and machine-state timeline](images/brew-journal.png)

## Firmware, device website, and integrations

For a first-time conversion, connect the idle machine by USB to the computer running the Hub. Open **Brewery → Add machine → USB setup and recovery**, select the connection and model, then choose **Back up original firmware** or **Install standalone firmware**. Follow the model-specific prompts and keep USB connected until verification finishes.

![Hub USB setup dialog with a simulated USB adapter, Pico S selected, and Install standalone firmware available](images/usb-setup.png)

For later updates, open **Firmware** on the machine’s front panel in the Hub. The version picker offers compatible wireless upgrades and downgrades on machines provisioned with network recovery. On a Pico C the package must also match the verified display controller. [Firmware installation and recovery](../server/docs/firmware-packages.md)

A Pico S or Pro running standalone firmware also serves **Pico Local** at its own network address. Before pairing, you can see the brewer’s state and temperatures. On a paired brewer, **Manage machine → Open machine website** opens the local controls and history using the Hub’s saved pairing. [Pairing and the brewer website](../server/docs/device-connections.md)

![Pico Local showing an idle simulated Pico Pro and its wort and thermoblock temperatures before the browser connects](images/standalone-public.png)

**API explorer** in the Hub documents the endpoints for managing multiple machines and the shared recipe library.

**Settings** includes appearance, equipment preferences, data export, and the optional brew assistant. Once a provider is connected, the assistant can draft a recipe, discuss a live brew you opt into monitoring, or review a finished session. You review the draft and operate the brewer yourself. [Brew assistant setup](../server/README.md#connect-the-brew-assistant)
