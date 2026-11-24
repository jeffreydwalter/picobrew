# Zymatic foreground Wi-Fi scheduling

The experimental compact and extended images start the listener from the original six-item Home menu after the stock Wi-Fi setup has associated and acquired an address. Home shares the HTTP parser with the Ethernet service. The scheduler selects a single transport and discards a previous transport's client state when the interface changes.

When the operator chooses a menu item, the scheduler keeps that choice while a pending radio transaction finishes. The original action runs once, with the stock driver in control of the radio. The extended image also serves the local website during the physical About pairing prompt. Before the next About screen queries the network, it passes the radio back to the stock driver. Normal brewing and its outbound session traffic use the stock driver.

If the radio stalls, Home can continue with the commissioned offline recipe. The normal service checks take their disconnected paths, and opening Settings shows “Restart for network” for the unavailable connection. The private heap object stays allocated and stock retains the physical dial. A normal access-point disconnect is handled separately by the radio driver, which waits for association and DHCP.

## Wi-Fi during a recipe

Both experimental profiles can serve a read-only website and API during the recipe's programmed pause (route 6). The stock firmware switches off its four outputs before the pause. The original code still decides when to prompt the operator. The physical dial confirms or cancels. A pending browser request cannot make that choice. The Hub can recognize this interval from authenticated status as `recipe_pause` with `busy` set to true. [Pause hooks and request policy](pause.py)

The stock driver retains the radio throughout heating, recirculation and draining. A physical filter removal can occur on any control pass. The stock code reads the switch twice, saves and switches off the four outputs, sends error 8, waits for the physical switch, sends error 208 and restores the saved outputs. The filter wait has its own input loop. The normal brew button poll cannot finish a pending HTTP transaction from inside that loop. The OEM log sender also takes a pointer into a temporary stack frame. It advances the recording time even when the send returns failure. The normal sample and signal-strength checks become due after 60,000 and 425,000 ms respectively. [Original firmware instruction tests](../tests/cc3000_oem.cjs) · [Sample lifetime and filter sequence](../tests/cc3000_hot_brew.cjs)

To share the radio continuously, a sender must retain each sample's values and session ID before the original stack frame returns, order filter removal and reinsertion ahead of session close, and advance the transfer from both the normal loop and the filter wait. Verification should interrupt a browser receive between its completion and data packet, remove and reinsert the filter, and finish the last step with both a healthy and an unavailable radio. Compare timestamps, original output transitions, report order and resumption of the physical program. A measured board run must also establish the controller's service interval during those transfers.

## Verification

From this directory, run the scheduler tests with the host's C compiler.

```sh
python3 verify_native.py
```

The instruction tests use the project's pinned Arduino AVR compiler, the original 1.1.14 ZIP or HEX, Node.js, and the dependencies in `firmware/zymatic/reverse-engineered/tests`.

```sh
python3 verify_avr.py --toolchain /path/to/avr/bin --oem-file /path/to/Zymatic_1_1_14.zip

# Include both physical choices in the route-6 pause and its read-only HTTP policy.
python3 verify_avr.py --toolchain /path/to/avr/bin --oem-file /path/to/Zymatic_1_1_14.zip --recipe-pause
```

The tests execute all six original Home selection paths, track the W5100 register boundary, compare the stock service branches and check access to a saved nine-step recipe after radio loss. A second test uses the compiled radio driver, models the CC3000's SPI pins, and stalls a byte after the HTTP port bind request. These are off-device checks. The full firmware layouts have separate size, packaging, and simulation tests.
