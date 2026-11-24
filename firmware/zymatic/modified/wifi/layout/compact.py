"""Link candidate radio code alongside the compact AVR application and check its flash budget."""

import argparse
import io
import os
from pathlib import Path
import shlex
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile
import zlib

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools"))
sys.path.insert(0, str(ROOT / "wifi/integration"))
import hooks
import pause
import compact_build as compact
import oem_tables as tables
import usb_stock
import zymatic_build as extended
from network.compact import compose as compact_network
from oled import compact as compact_oled


READ_HOOKS = {
    "sensor-crc", "pgm-copy", "pgm-wifi-print", "pgm-serial-print", "pgm-bitmap",
}


def stage_source(directory):
    # The small first-install profile is archived independently of the extended profile.
    repo = ROOT.parents[2]
    relative = ROOT.relative_to(repo)
    archive = subprocess.check_output(["git", "archive", compact.SOURCE_REVISION, str(relative)], cwd=repo)
    with tarfile.open(fileobj=io.BytesIO(archive)) as source:
        for member in source:
            path = Path(member.name).relative_to(relative) if member.isfile() else None
            if path is None or path.suffix not in (".c", ".h", ".S", ".ld"):
                continue
            if len(path.parts) > 1 and path.parts[0] not in ("src", "site"):
                continue
            target = directory / path
            target.parent.mkdir(exist_ok=True)
            target.write_bytes(source.extractfile(member).read())
    api = directory / "zymatic_api.c"
    source = api.read_text()
    if source.count(compact.PREVIEW_VERSION) != 1:
        raise ValueError("Unexpected compact release identity")
    source = source.replace(compact.PREVIEW_VERSION, extended.VERSION)
    # All compact response strings and the website stay inside flash bank 1.
    # Match the extended runtime's ROM pointer width while retaining the archived responses.
    macros = {
        "typedef uint_farptr_t FlashAddress;": "typedef const __flash1 char *FlashAddress;",
        "#define ADDRESS(s) pgm_get_far_address(s)": "#define ADDRESS(s) ((FlashAddress)(uintptr_t)(s))",
        "static const char v[] PROGMEM = s; ADDRESS(v);": "static const __flash1 char v[] = s; v;",
        "#define READ(s) pgm_read_byte_far(s)": "#define READ(s) ((uint8_t)*(s))",
    }
    for before, after in macros.items():
        if source.count(before) != 1:
            raise ValueError("Unexpected compact AVR flash addressing")
        source = source.replace(before, after)
    api.write_text(source)
    # The extended CRT checks the full application before expanding original SRAM.
    # Avoid computing the same full CRC a second time in compact OEM setup.
    overlay = directory / "src/overlay.c"
    source = overlay.read_text()
    check = "    if (!zymatic_image_valid()) zymatic_image_invalid();\n"
    if source.count(check) != 1:
        raise ValueError("Unexpected compact checksum initialization")
    overlay.write_text(source.replace(check, ""))
    for name in ("oem_flash.S", "crt_data.S"):
        shutil.copyfile(ROOT / "src" / name, directory / "src" / name)


def link_application(directory, toolchain):
    def tool(name, *args):
        return subprocess.check_output([toolchain / name, *args], cwd=directory, text=True)

    if (tool("avr-gcc", "-dumpversion").strip() != "7.3.0"
            or "2.26.20160125" not in tool("avr-ld", "--version")):
        raise ValueError("Use the Arduino AVR GCC 7.3.0-atmel3.6.1-arduino7 toolchain")
    stage_source(directory)
    hooks.stage(directory, compact=True)
    pause.stage(directory, compact=True)
    flags = ["-mmcu=atmega1284p", "-Os", "-std=gnu11", "-Wall", "-Wextra", "-Werror",
             "-ffunction-sections", "-fdata-sections", "-fno-jump-tables",
             "-fno-tree-switch-conversion", "-mcall-prologues", "-fno-tree-dominator-opts", "-fmerge-all-constants", "-I", directory]
    runtime = sorted((directory / "src").glob("*.c")) + sorted((directory / "src").glob("*.S"))
    runtime += sorted(directory.glob("zymatic_api*.c"))
    objects = []
    for source in runtime:
        name = source.stem + ".o"
        tool("avr-gcc", *flags, "-flto", "-c", source, "-o", name)
        objects.append(name)
    components = ["cc3000", "http_session", "server", "cc3000_broker", "cc3000_spi_bus", "cc3000_spi", "cc3000_board"]
    for name in components:
        output = "wifi_" + name + ".o"
        tool("avr-gcc", *flags, "-DZYMATIC_WIFI_COMPACT", "-c", ROOT / "wifi" / (name + ".c"), "-o", output)
        objects.append(output)
    tool("avr-gcc", *flags, "-I", ROOT / "wifi", "-c", Path(__file__).with_name("state.c"), "-o", "state.o")
    objects.append("state.o")
    elf = "compact.elf"
    entries = [*pause.COMMANDS, pause.COMPACT_BUTTON, *hooks.COMPACT_ENTRIES, "candidate_radio", "candidate_broker", *compact.OFFLINE, "zymatic_crt_data", "zymatic_factory_compare", "zymatic_factory_stamp",
               "zymatic_after_loop", "zymatic_menu_button", "zymatic_brew_button", "zymatic_read_stock_serial"]
    tool("avr-gcc", "-mmcu=atmega1284p", "-Os", "-mcall-prologues", "-fno-tree-dominator-opts", "-fmerge-all-constants", "-ffunction-sections", "-fdata-sections", "-flto", "-nostartfiles", "-nostdlib",
         "-Wl,--gc-sections", "-Wl,--relax", *[f"-Wl,--undefined={name}" for name in entries],
         "-Wl,-T," + str(Path(__file__).with_name("compact.ld")), *objects,
         *[f"-Wl,--defsym,{name}={address:#x}" for name, address, _, _ in tables.LIBRARY],
         "-Wl,--start-group", "-lc", "-lgcc", "-Wl,--end-group", "-o", elf)
    symbols = {}
    for line in tool("avr-nm", "--defined-only", elf).splitlines():
        parts = line.split()
        if len(parts) == 3:
            symbols[parts[2]] = int(parts[0], 16)
    pieces = {}
    for section in ("text", "low", "crc_hole", "net_encoder", "net_wifi_fields", "net_reader", "oled_transfer", "oled_clear", "oled_init", "key_one", "key_two"):
        filename = section + ".bin"
        tool("avr-objcopy", "-O", "binary", "-j", "." + section, elf, filename)
        pieces[section] = (directory / filename).read_bytes()
    return symbols, pieces


def compact_layout(original, toolchain, directory):
    if not usb_stock.is_original_app(original):
        raise ValueError("Use the original Zymatic 1.1.14 image")
    tables.verify_library(original)
    network, windows = compact_network(original, toolchain, directory)
    network, oled_windows = compact_oled.compose(network)
    windows.extend(oled_windows)
    network, key_windows = compact_oled.compose_keyboard(network, toolchain, directory)
    windows.extend(key_windows)
    composed = bytearray(network)
    symbols, pieces = link_application(directory, toolchain)
    for name, (start, end) in zip(("net_encoder", "net_wifi_fields", "net_reader", "oled_transfer", "oled_clear", "oled_init", "key_one", "key_two"), windows, strict=True):
        part = pieces[name]
        if (symbols["__" + name + "_start"] != start or not 0 < len(part) <= end - start
                or composed[start:end] != b"\xff" * (end - start)):
            raise ValueError("Unexpected network replacement window " + name)
        composed[start:start + len(part)] = part
    composed = bytearray(pause.compose(hooks.compose(composed, symbols, compact=True), symbols, compact=True))
    _, template = extended.template()
    patches = compact.patch_list(symbols, 8080)
    read_hooks = READ_HOOKS | {"startup-initializer"}
    # These are the only extended instructions used in the compact application.
    read_symbols = {name: symbols[name] for name in ("zymatic_sensor_crc", "zymatic_pgm_copy",
                    "zymatic_pgm_wifi_print", "zymatic_pgm_serial_print", "zymatic_pgm_bitmap", "zymatic_crt_data")}
    additional = [row for row in extended.patch_list(template["symbols"] | read_symbols) if row[0] in read_hooks]
    if {row[0] for row in additional} != read_hooks:
        raise ValueError("Missing OEM read or initialization hook")
    for name, address, before, after in patches + additional:
        if composed[address:address + len(before)] != before:
            raise ValueError("OEM instruction differs at " + name)
        composed[address:address + len(after)] = after
    packed = tables.pack_seek_pool(bytes(composed[tables.POOL:tables.POOL_END]))
    low, crc = pieces["low"], pieces["crc_hole"]
    low_end = tables.SEEK_END + len(low)
    if low_end > tables.POOL_END or len(crc) > 254:
        raise ValueError("Low-flash sections exceed the original tables")
    if composed[tables.CRC_TABLE:tables.CRC_TABLE + 256] != tables.sensor_crc_table():
        raise ValueError("Unexpected OEM sensor CRC table")
    composed[tables.POOL:tables.POOL_END] = (packed + low).ljust(tables.POOL_END - tables.POOL, b"\xff")
    composed[tables.CRC_TABLE:tables.CRC_TABLE + 256] = (b"\xff" + crc).ljust(256, b"\xff")
    initializer = tables.pack_crt(bytes(composed[tables.CRT:tables.CRT + tables.CRT_SIZE]))
    composed[tables.CRT:] = initializer.ljust(tables.HIGH_START - tables.CRT, b"\xff") + pieces["text"]
    if len(composed) > compact.LIMIT or symbols["provision_crc"] != len(composed) - 4:
        raise ValueError("Composed application crosses the boot boundary or is missing its final CRC")
    fields = compact.provisioning(bytes(range(32)), "0a" * 16, "brewery.lan:8080")
    fields["provision_recipe"] = compact.recipes.pack_program(None)
    for name, raw in fields.items():
        address = symbols[name]
        if not tables.HIGH_START <= address <= len(composed) - len(raw) or any(composed[address:address+len(raw)]):
            raise ValueError("Invalid test provisioning field " + name)
        composed[address:address + len(raw)] = raw
    checksum = symbols["provision_legacy_sum"]
    struct.pack_into("<H", composed, checksum, compact.legacy_sum(composed))
    struct.pack_into("<I", composed, len(composed) - 4, zlib.crc32(composed[:-4]))
    return bytes(composed), symbols, low_end


def simulate(directory, original, image, symbols, original_hex, toolchain):
    flags = os.environ.get("SIMAVR_FLAGS")
    if flags is None:
        flags = subprocess.check_output(["pkg-config", "--cflags", "--libs", "simavr"], text=True)
    base, binary = directory / "stock.bin", directory / "synthetic.bin"
    base.write_bytes(original)
    binary.write_bytes(image)
    pause.verify_composed_pause(base, binary, directory / "compact.elf", toolchain, compact=True)
    stock_hex = directory / "stock.hex"
    stock_hex.write_bytes(original_hex)
    subprocess.run(["node", ROOT.parent / "reverse-engineered/tests/stock-controls.cjs", stock_hex, binary], check=True, timeout=60)
    compiler = os.environ.get("CC", "cc")
    for harness in ("simavr_tables", "simavr_overlay"):
        executable = directory / harness
        subprocess.run([compiler, "-O2", ROOT / "tests" / (harness + ".c"), *shlex.split(flags),
                        "-o", executable], check=True)
        if harness == "simavr_tables":
            subprocess.run([executable, base, binary], check=True, timeout=60)
            continue
        args = [executable, binary, str(symbols["zymatic_hub_host"] - 0x800000),
                str(symbols["__extension_bss_end"] - 0x800000),
                str(symbols["zymatic_hub_authority"] - 0x800000), str(symbols["zymatic_image_invalid"])]
        for case in ("api", "corrupt", "factory-good", "factory-bad"):
            subprocess.run([*args, case, base], check=True, timeout=45)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--oem-file", required=True, type=Path, help="Verified original Zymatic 1.1.14 ZIP or HEX")
    parser.add_argument("--toolchain", required=True, type=Path, help="Directory containing pinned avr-gcc")
    parser.add_argument("--simulate", action="store_true", help="Run OEM table and compact application tests in simavr")
    args = parser.parse_args()
    original_hex = usb_stock.unpack_source(args.oem_file.read_bytes())
    original = usb_stock.decode_application(original_hex)
    with tempfile.TemporaryDirectory() as temporary:
        directory = Path(temporary)
        image, symbols, low_end = compact_layout(original, args.toolchain.resolve(), directory)
        if args.simulate:
            simulate(directory, original, image, symbols, original_hex, args.toolchain.resolve())
        # AVR instruction alignment rules out the odd final byte of the table.
        aligned_end = tables.POOL_END & ~1
        print(f"Compact application: {len(image)} bytes. Contiguous free low flash "
              f"[{low_end:#x}, {aligned_end:#x}): {aligned_end - low_end} bytes. "
              f"Free before the smallest OEM boot boundary: {compact.LIMIT - len(image)} bytes. "
              f"Static SRAM including broker and radio: {symbols['__extension_bss_end'] - 0x800000 - compact.BSS}/768 bytes.")


if __name__ == "__main__":
    main()
