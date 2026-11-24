"""Stage the output-off route-6 recipe pause for an experimental Wi-Fi build."""

from pathlib import Path
import json
import shutil
import subprocess
import tempfile

COMMANDS = {"zym_wifi_pause_wait": (0xfa28, 0x1122c), "zym_wifi_pause_dialog": (0xfaec, 0x7f9a)}
COMPACT_BUTTON = "zym_wifi_pause_physical"


def edit(path, old, new):
    source = path.read_text()
    if source.count(old) != 1:
        raise ValueError(f"Review recipe pause hook in {path.name}")
    path.write_text(source.replace(old, new))


def stage(runtime, compact, api_already_staged=False):
    runtime = Path(runtime)
    src = runtime / "src"
    header = src / "scheduler.h"
    edit(header, "inside_safe:1;", "inside_safe:1, recipe_pause:1;")
    edit(header, "#include \"cc3000_board.h\"", '#include "cc3000_board.h"\n#define ZYM_WIFI_RECIPE_PAUSE 4')
    scheduler = src / "scheduler.c"
    # Give the waiting stock caller priority while still enforcing the
    # paused request policy on any input already in the HTTP stream.
    edit(scheduler, "now, pending_choice ?", "now, zym_wifi_flags.recipe_pause ? ZYM_WIFI_RECIPE_PAUSE : pending_choice ?")
    overlay = src / "overlay.c"
    if not compact:
        marker = "    zymatic_drawn_row = displayed;"
        edit(overlay, marker, "    if (zym_wifi_flags.recipe_pause) return zym_wifi_pause_physical(encoder);\n" + marker)
    edit(overlay, '#include "scheduler.h"', '#include "scheduler.h"\n#include <avr/wdt.h>\nuint8_t zym_wifi_pause_physical(void *encoder);')
    functions = '''
/* In 1.1.14 route 6, the first wait follows writes of LOW to 4, 13, 24
 * and 25. It contains only the original LED, elapsed-time and button work.
 * Read the physical latch first. The network never supplies this answer. */
uint8_t zym_wifi_pause_physical(void *encoder)
{
    uint8_t button = oem_button_event(encoder);
    if (!button && zym_wifi_flags.recipe_pause && (SREG & _BV(SREG_I)))
        poll_network(0);
    return button;
}

uint8_t zym_wifi_pause_wait(void *encoder)
{
    if (!zym_wifi_flags.recipe_pause && !*(volatile uint8_t *)0x230b &&
        *(volatile uint8_t *)0x230c && !zym_wifi_quarantined) {
        CLEAR_INPUT
        zym_wifi_home_enter();
        zym_wifi_flags.recipe_pause = 1;
    }
    return zym_wifi_pause_physical(encoder);
}

uint8_t zym_wifi_pause_dialog(uint8_t count, uint8_t row, uint8_t timeout)
{
    uint8_t choice = OEM_ENTRY(0x7f9a,uint8_t,(uint8_t,uint8_t,uint8_t))(count,row,timeout);
    if (zym_wifi_flags.recipe_pause) {
        while (zym_wifi_home_choice(0, oem_millis()) == 0xff) {
            wdt_reset();
            poll_network(0);
        }
        zym_wifi_flags.recipe_pause = 0;
    }
    return choice;
}
'''
    with overlay.open("a") as f:
        f.write(functions.replace("CLEAR_INPUT", "" if compact else
                                  "(void)zymatic_panel_take_press();\n        zymatic_panel_changed();"))
    if not api_already_staged:
        stage_api(runtime, compact)


def stage_api(runtime, compact):
    # The compact parser has only GET routes. Both versions distinguish
    # "currently brewing" on status from "no remote dial" on this prompt.
    api = runtime / "zymatic_api.c"
    signature = "uint32_t now_ms, uint8_t program_active)\n{"
    body = ("    if (program_active == ZYM_WIFI_RECIPE_PAUSE) {\n"
            + ("" if compact else
               "        if (!n->http_error && (n->route == ENCODER || n->route >= PAIR_REQUEST))\n"
               "            n->http_error = METHOD;\n")
            + "        if (n->route != STATUS) program_active = " + ("0" if compact else "n->route == OVERVIEW")
            + ";\n    }")
    edit(api, signature, signature + "\n" + body)
    condition = "program_active" if compact else "program_active == ZYMATIC_PROGRAM"
    original = 'if (' + condition + r') literal(n, CONSTANT("\",\"state\":\"program_running\",\"busy\":true"));'
    paused = r'if (program_active == ZYM_WIFI_RECIPE_PAUSE) literal(n, CONSTANT("\",\"state\":\"recipe_pause\",\"busy\":true"));'
    edit(api, original, paused + "\n        else " + original)
    # A numeric mode extends the staged API without any board/SPI header in its parser.
    edit(api, '#include "zymatic_api.h"', '#include "zymatic_api.h"\n#define ZYM_WIFI_RECIPE_PAUSE 4')


def compose(image, symbols, compact):
    import hooks
    sites = dict(COMMANDS)
    if compact:
        sites[COMPACT_BUTTON] = (0x807c, 0x1122c)
    result = bytearray(image)
    for name, (site, original) in sites.items():
        if result[site:site+4] != hooks.call(original):
            raise ValueError(f"Wrong 1.1.14 recipe pause call at {site:#x}")
        result[site:site+4] = hooks.call(symbols[name])
    return bytes(result)


def verify_composed_pause(stock_bin, synthetic_bin, elf, toolchain, compact):
    """Run the output-off hooks and authenticated HTTP code in a final, synthetic-linked application."""
    names = subprocess.check_output([toolchain / "avr-nm", "--defined-only", elf], text=True)
    symbols = {fields[2]: int(fields[0], 16) for line in names.splitlines() if len(fields := line.split()) == 3}
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory) / "symbols.json"
        path.write_text(json.dumps(symbols))
        subprocess.run(["node", Path(__file__).with_name("stock_guards.cjs"), stock_bin, synthetic_bin,
                        path, "compact" if compact else "extended"], check=True, timeout=60)


def verify_extended_http(root, work):
    """Compile and exercise the paused policy on the extended firmware's full API."""
    work.mkdir()
    source = work / "zymatic_api.c"
    shutil.copyfile(root / source.name, source)
    stage_api(work, compact=False)
    test = work / "pause-requests"
    subprocess.run(["cc", "-std=c11", "-Wall", "-Wextra", "-Werror", "-I", root, source,
                    Path(__file__).with_name("pause_requests.c"),
                    *(root / item for item in ("zymatic_api_w5100.c", "src/history.c",
                                               "src/pairing.c", "tests/history_native.c")),
                    "-o", test], check=True)
    subprocess.run([test], check=True)
