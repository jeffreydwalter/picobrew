"""Exercise the 1.1.14 normal physical Home path and failed-radio handling.

The first run scripts the radio boundary for repeatable handoffs. The second
links the AVR board driver and simulates the electrical response at its SPI
pins. The separate layout builds measure each complete firmware application.
"""
import argparse
import json
from pathlib import Path
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE.parent / "layout"))
import compact
import hooks
import pause
import compact_build
import offline_program
import usb_stock


def verify(toolchain, original, directory, recipe_pause=False, real_radio=False):
    compact.stage_source(directory)
    hooks.stage(directory, compact=True)

    if recipe_pause:
        pause.stage(directory, compact=True)
        pause.verify_extended_http(ROOT, directory / "extended-pause-requests")

    def run(tool, *args, **kwargs):
        return subprocess.run([toolchain / tool, *args], cwd=directory, check=True, **kwargs)

    objects = []
    sources = [directory / "src" / name for name in ("overlay.c", "scheduler.c", "stock.S", "offline_recipe.c")]
    sources += [directory / name for name in ("zymatic_api_w5100.c", "zymatic_api.c")]
    if real_radio:
        wifi = HERE.parent
        sources += [wifi / (name + ".c") for name in (
            "cc3000_board", "cc3000_spi", "cc3000_spi_bus", "cc3000_broker", "server", "http_session", "cc3000")]
        sources.append(wifi / "layout/state.c")
    else:
        sources.append(HERE / "board_fixture.c")
    for source in sources:
        obj = source.stem + ".o"
        run("avr-gcc", "-mmcu=atmega1284p", "-Os", "-std=gnu11", "-Wall", "-Wextra", "-Werror",
            "-fno-jump-tables", "-fno-tree-switch-conversion", "-ffunction-sections", "-fdata-sections",
            "-DZYMATIC_WIFI_COMPACT", "-I", HERE.parent, "-I", directory,
            "-I", directory / "src", "-c", source, "-o", obj)
        objects.append(obj)
    retain = (*hooks.COMPACT_ENTRIES, "zymatic_menu_button", "zymatic_sync_or_local",
              "zymatic_start_session", "zymatic_begin_brew", "zymatic_recover_recipe",
              "zymatic_after_loop", "zymatic_brew_button")
    if recipe_pause:
        retain += (*pause.COMMANDS, pause.COMPACT_BUTTON)
    run("avr-gcc", "-mmcu=atmega1284p", "-nostartfiles", "-nostdlib", "-Wl,--gc-sections",
        "-Wl,-T," + str(HERE / "fixture.ld"), *["-Wl,--undefined=" + name for name in retain],
        *objects, "-Wl,--start-group", "-lc", "-lgcc", "-Wl,--end-group", "-o", "fixture.elf")
    symbols = {}
    for line in run("avr-nm", "--defined-only", "fixture.elf", text=True, stdout=subprocess.PIPE).stdout.splitlines():
        if len(fields := line.split()) == 3:
            symbols[fields[2]] = int(fields[0], 16)
    run("avr-objcopy", "-j", ".text", "-O", "binary", "fixture.elf", "fixture-app.bin")
    image = bytearray(hooks.compose(original, symbols, compact=True))
    if recipe_pause:
        image = bytearray(pause.compose(image, symbols, compact=True))
    image.extend(bytes([0xff]) * (0x1bc00 - len(image)))
    image.extend((directory / "fixture-app.bin").read_bytes())
    for symbol, sites in compact_build.OFFLINE.items():
        if symbol in retain:
            for site, expected in sites:
                if image[site:site+4] != bytes.fromhex(expected):
                    raise ValueError(f"Invalid pinned offline instruction at {site:#x}")
                image[site:site+4] = hooks.call(symbols[symbol])
    for site, symbol in ((0x831e, "zymatic_menu_button"), (0xf488, "zymatic_brew_button")):
        if image[site:site+4] != hooks.call(0x1122c):
            raise ValueError(f"Invalid pinned button instruction at {site:#x}")
        image[site:site+4] = hooks.call(symbols[symbol])
    recipe = offline_program.pack_program()
    address = symbols["provision_recipe"]
    image[address:address+len(recipe)] = recipe
    (directory / "stock.bin").write_bytes(original)
    (directory / "fixture.bin").write_bytes(image)
    (directory / "symbols.json").write_text(json.dumps(symbols))
    subprocess.run(["node", HERE / "stock_guards.cjs", directory / "stock.bin",
                    directory / "fixture.bin", directory / "symbols.json"], check=True)


if __name__ == "__main__":
    args = argparse.ArgumentParser(description=__doc__)
    args.add_argument("--toolchain", required=True, type=Path)
    args.add_argument("--oem-file", required=True, type=Path)
    args.add_argument("--recipe-pause", action="store_true")
    opts = args.parse_args()
    source = usb_stock.decode_application(usb_stock.unpack_source(opts.oem_file.read_bytes()))
    if not usb_stock.is_original_app(source):
        raise ValueError("Expected the original Zymatic 1.1.14 application")
    for actual_radio in (False, True):
        with tempfile.TemporaryDirectory() as td:
            verify(opts.toolchain.resolve(), source, Path(td), opts.recipe_pause and not actual_radio, actual_radio)
