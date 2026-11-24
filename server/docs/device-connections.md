# Machine connections and API keys

## Manage machines from Brewery

**Brewery → Add machine** searches the Hub computer’s directly connected private IPv4 networks for PicoBrew devices. Select a standalone brewer or accessory under **On your network** to fill in its name and address. You can edit either before adding it. On larger networks, **Check again** covers additional addresses. If the Hub runs on another subnet or inside an isolated container, you can enter the **Machine URL** manually whenever the Hub has a route to it.

The original Z Program Maker and PicoStill interfaces can also appear here. **Open device website** opens Program Maker or the Still’s readings page. A stock PicoFerm exposes its setup page while the Hub computer is connected to the PicoFerm’s own setup Wi-Fi. During normal fermentation it sleeps between outbound reports. A Zymatic that recently contacted this Hub’s recipe sync service offers **Use for recipe sync**, which selects its serial number for the recipe library’s stock transfer flow. For stock Zymatic network setup, see [Stock Zymatic recipe sync](stock-zymatic.md).

**Adding a PicoFerm with standalone firmware** uses the device key from USB setup. The Hub writes its reachable HTTP hostname or IPv4 address and port to the PicoFerm and verifies the saved settings. The PicoFerm posts temperature, pressure and battery readings directly to the Hub and uses the same destination after a restart. Open **Settings → Hub connection** on the PicoFerm’s local site to see where it sends readings and the result of its latest delivery.

After a restart the PicoFerm retries its saved receiver. If deliveries keep failing, the paired Hub can detect this, verify a different address for itself and update an idle PicoFerm. This works while the Hub can still reach the PicoFerm at its registered address. If the PicoFerm’s own address also changed, update **Manage machine → Edit connection**. The Hub keeps the old destination until it verifies a replacement. Manually saved destinations and disabled forwarding stay as chosen. For an older registration, select **Device preferences → Live readings → Use this Hub** to enable this behavior.

**Assign a PicoFerm to a brewer** from **Ferment** on the brewer’s Hub page, or **Linked brewer** on the PicoFerm’s page. Choose **Link to brewer only** for an equipment assignment. On compatible brewer firmware, the physical **Ferment** menu lists monitors already registered with that Hub. The Hub must be running to confirm a menu selection. Use the Hub for the Zymatic or a brewer with older firmware.

**Monitor a batch** by opening the brewing session in **Brew journal → Ferment** and choosing the monitor on that keg. Start the recording on the PicoFerm or its Hub page. In **Ferment**, select that recording when it appears to open its measurements from the batch. Moving the PicoFerm to the next keg preserves the previous batch’s recording.

On firmware that supports pairing, compare the code in the browser with the one on the brewer under **About → Pair with Hub** and approve it at the physical dial. On a pairing-capable Zymatic, use Ethernet and **About Zymatic**. When continuing from a completed Hub USB setup, the form uses the key saved with that job and checks the machine’s identity. For models without dial pairing or for an existing key file, choose **Use an existing key instead**.

Once registered, open **Manage machine** on its machine page for **Test connection**, **Edit connection**, or **Remove machine**. Connection management stays available offline. **Monitor this machine** enables or disables the Hub connection. The Hub reads the reported model when available and pins the first authenticated hardware UID. Older firmware can require manual model selection. A changed UID blocks the connection. Connection changes and removal are blocked while the last verified state reports an active operation.

**Brewer preferences** contains the machine's display preferences and a **Live readings** form. Receiver host, receiver port, and **Send live readings** are always visible for a physical machine. The live-data settings stay editable while the appliance is offline; display settings still require its connection.

The Hub stores the receiver settings and sends them to the machine when it connects or when the settings are saved. New connections start with this Hub's verified address. Existing custom host/port values and disabled sending settings are preserved. On a PicoFerm, **Device preferences → Live readings → Use this Hub** selects the current Hub again and enables automatic receiver recovery.

Saving while offline or busy retains the changes in the Hub and shows that application is pending. The Hub verifies the appliance identity and applies the settings after reconnecting and once the machine is idle. A successful save is distinct from a confirmed device update. These options do not change the Hub's saved machine address or API key. No destination form is required on the firmware website.

Removal requires confirmation and removes the Hub registration and saved credential. Archived brew records and their sensor samples remain available in **Brew journal**. The Hub attempts to disable its own reporting connection on a reachable idle appliance; it does not disable a custom external destination or another Hub's connection. The action does not send a firmware erase, recipe deletion, or stop command to the appliance.

Old `#/settings/new-device` links redirect to the Brewery add-machine dialog.

## One provisioned key per physical brewer

The Hub encrypts the device key it obtained during pairing or USB setup. The owner can use **Manage machine → Open machine website** on a connected machine that advertises pairing, or **Copy device API key** for manual sign-in. Ordinary machine responses and workspace exports omit credentials.

For manual enrollment, upload the private `api.key` file from setup or enter its 64 hexadecimal characters under **Machine API key**. The S/Pro provisioning tool generates a random 32-byte key and stores it at `/local/api.key` on that brewer. Multiple clients for one brewer use the same key. Use a distinct key for each physical machine.

A manual registration can be saved while the brewer is offline. Authentication and UID verification happen when the Hub reaches it. Use **Replace saved key** after provisioning a different key on the same brewer. Editing the registration updates the Hub’s copy.

### Shared-key advisory

The Hub marks registrations with **Shared API key** when their saved hexadecimal key values match another registered physical machine's. Disabled registrations are included; simulators are excluded. Editing, replacing a credential, removing a registration, and restarting the Hub recompute the advisory.

This warning does not reject valid existing credentials or rotate firmware keys. It also does not prove that two records refer to the same physical brewer, that either credential authenticates, or that a key is unique outside this Hub. Check whether the second record is an accidental duplicate. If there really are two brewers, provision a distinct key on each appliance and then update the corresponding Hub connection.

Only the advisory boolean is returned by the API as `key_reused`. Private comparison fingerprints remain in server memory; neither the fingerprints nor the credentials are included in device responses or workspace exports. Saved keys remain encrypted in the Hub's data directory. A credential that cannot be decrypted still produces a connection error.

For firmware-side provisioning and rotation, use the [standalone provisioning guide](../../firmware/pico-s-pro/modified/docs/standalone-provisioning.md). Changing a Hub registration is not a substitute for that operation.
