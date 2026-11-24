# Zymatic display flash budget

The 1.1.14 OLED initialization repeats the same GPIO and SPI transaction for each of its 23 command bytes. Display refresh and clear use the same sequence to select columns, rows and display modes. The layout experiment shares those instructions while retaining the original GPIO setup, reset delays, status polling and pixel transfer loop. Each shared call uses two stack bytes for its return address. The three original function entry points stay at their original addresses.

The two stock keyboard highlighting routines also calculate the same key coordinates. Their shared replacement reads the original SRAM table and calls the original outline or fill primitive. It preserves the widths of all five special keys.

Run from the Zymatic modified directory with the dependencies from the stock AVR8js tests and the pinned Arduino AVR toolchain.

```sh
python3 wifi/layout/oled/compact.py --oem-file "$ZYMATIC_TEST_HEX" --toolchain "$ARDUINO_AVR_BIN"
```

The original and compact display code are run with equivalent simulated GPIO, display memory and SPI status reads. The comparison checks the ordering and values of bus operations, the reset delay requests, framebuffer bytes, register state and stack restoration. The keyboard comparison checks the primitive and its coordinates, dimensions and color for all 256 key indexes using two coordinate tables.

The measured total is 2,572 bytes in five distinct ranges. The largest is 1,650 contiguous bytes. Timing on the physical LCD and concurrent radio use still require board testing.
