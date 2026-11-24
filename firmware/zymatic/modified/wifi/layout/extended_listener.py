"""Link the entire experimental radio into the extended board's real flash windows."""

from pathlib import Path
import json
import shutil
import subprocess
import sys

import flash_constants

# Ordinals match the inspected network, display and keyboard regions returned
# by their ROM composers. Preserve each function's natural AVR instruction alignment.
SECTIONS = [
    ("encoder", ("zym_spi_submit", "byte_io")),
    ("wifi_fields", ("zymatic_api_reply_byte", "zymatic_history_ready")),
    ("wired_fields", ("zymatic_api_more", "zym_spi_request_display", "read_at")),
    ("oled_transfer", ("step_index",)),
    ("oled_clear", ("number", "submit", "zym_spi_bind")),
    ("oled_init", ("zymatic_image_valid", "zym_wifi_network_poll")),
    ("key_one", ("reg_io",)),
    ("key_two", ("bind_from", "zymatic_factory_matches")),
    ("request0", ("unlikely.get32",)),
    ("request1", ("crc",)),
    ("request2", ("transfer",)),
    ("request3", ("after_care",)),
    ("request4", ("whitespace",)),
    ("request5", ("zymatic_pair_reset",)),
    ("request6", ("read_eeprom",)),
    ("request7", ("zym_spi_record_irq",)),
    ("request8", ("append",)),
    ("socket0", ("forget",)),
    ("socket1", ("zym_wifi_before_restart",)),
    ("socket2", ("zym_spi_release_radio",)),
    ("socket3", ("zym_spi_stock_restart",)),
    ("socket4", ("expected_live_sum",)),
]
ENTRIES = (
    "candidate_radio", "candidate_broker", "zym_cc3000_board_begin", "zym_cc3000_board_poll",
    "zym_spi_stock_restart", "zym_spi_yield_to_stock", "zym_spi_resume_from_stock", "zym_spi_request_display",
)


def link(root, runtime, toolchain, windows, original, fragments):
    wifi = Path(__file__).resolve().parents[1]
    src = runtime / "src"
    for path in sorted(wifi.glob("*.[ch]")):
        # Match the actual API's typed flash credentials on both transports.
        source = path.read_text().replace("const uint8_t key[32]", "ZymKey key").replace("const char *uid", "ZymUid uid")
        (src / path.name).write_text(source)
    shutil.copyfile(wifi / "layout/state.c", src / "candidate_state.c")
    sys.path.insert(0, str(wifi / "integration"))
    import hooks
    import pause
    hooks.stage(runtime)
    pause.stage(runtime, compact=False, api_already_staged=True)
    entries = (*pause.COMMANDS, *ENTRIES, *hooks.ENTRIES, "zym_wifi_network_poll")
    linker = src / "overlay.ld"
    # This entire fixed-address 1.1.14 function is byte-identical to memcpy
    # in the pinned AVR libc, including its two relative branch offsets.
    memcpy_code = bytes.fromhex("fb01dc0102c001900d9241505040d8f70895")
    if original[0x2686:0x2698] != memcpy_code:
        raise ValueError("Original AVR memcpy differs")
    script = linker.read_text() + "\nmemcpy = 0x2686;\n"
    for (name, functions), (start, end) in zip(SECTIONS, windows, strict=True):
        script = script.replace("MEMORY {", f"MEMORY {{\n  {name} (rx) : ORIGIN = {start:#x}, LENGTH = {end-start:#x}")
        selectors = " ".join(f"*(.text.{func})" for func in functions)
        if name == "encoder":
            selectors += " KEEP(*(.low_care))"
        script = script.replace("SECTIONS {", f"SECTIONS {{\n  .{name} : {{ {selectors} }} > {name}")
    script = script.replace("*(.low_functions)", "*(.low_functions) *(.text.zymatic_history_poll) *(.text.json_uint) *(.text.equal)")
    linker.write_text(script)
    compiler = runtime / "tools/compile_extension.py"
    source = compiler.read_text()
    for old, new in {
        "entry_points = [": f"entry_points = [{', '.join(map(repr, entries))}, ",
        '"-Wl,--gc-sections",': '"-ffunction-sections", "-fdata-sections", "-Wl,--gc-sections",',
        'invoke("avr-objcopy", "-j", ".text",':
            f'shutil.copyfile(work / "extension.elf", {str(root / "linked.elf")!r})\n        invoke("avr-objcopy", "-j", ".text",',
    }.items():
        if source.count(old) != 1:
            raise ValueError("The base AVR compiler interface changed")
        source = source.replace(old, new)
    source = source.replace('"-fmerge-all-constants",', '"-fmerge-all-constants", "-fshort-enums",')
    map_path = root / "extension.map"
    source = source.replace('"-Wl,--relax",', f'"-Wl,--relax", "-Wl,-Map={map_path}",')
    compiler.write_text(source)
    command = [sys.executable, compiler, "--toolchain", toolchain, "--out-dir", runtime / "extension"]
    bins = []
    if fragments:
        # The sizing link may cross the application limit before constants
        # have an address. The final ELF must pass the real linker limits.
        sizing = subprocess.run(command, capture_output=True, text=True)
        if sizing.returncode and (not map_path.is_file() or "overflowed by" not in sizing.stderr):
            raise RuntimeError("Unable to size the AVR application: " + sizing.stderr)
        bins = flash_constants.assign(map_path.read_text(), fragments)
        linker.write_text(flash_constants.stage(script, bins))
    subprocess.run(command, check=True)

    packed = bytearray(original)
    for (name, _), (start, end) in zip(SECTIONS, windows, strict=True):
        path = root / (name + ".bin")
        subprocess.run([toolchain / "avr-objcopy", "-j", "." + name, "-O", "binary", root / "linked.elf", path], check=True)
        code = path.read_bytes()
        if not 0 < len(code) <= end - start or packed[start:end] != b"\xff" * (end-start):
            raise ValueError("AVR code overlaps an occupied ROM window " + name)
        packed[start:start+len(code)] = code
    if bins:
        flash_constants.store(packed, bins, toolchain, root)
    symbols = json.loads((runtime / "extension/extension.json").read_text())["symbols"]
    return pause.compose(hooks.compose(bytes(packed), symbols), symbols, compact=False)
