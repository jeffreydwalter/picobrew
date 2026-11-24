"""Install experimental Wi-Fi hooks into a staged 1.1.14 extended runtime."""
from pathlib import Path
import struct

HERE = Path(__file__).resolve().parent
CLIENT_GUARDS = (
    0xbab4, 0xbf9e, 0xc36a, 0xcbbc, 0xd08c, 0xd0c8, 0xd12e, 0xd19a,
    0xd2c8, 0xd45a, 0xd52a, 0xd54e, 0xd580, 0xd5d8, 0xd88c, 0xd8ac,
    0xd8d8, 0xd906, 0xd942, 0xdaf2, 0xddf2, 0xe238,
)
# Control points in the OEM application, distinct from asynchronous IRQ and
# board callbacks. Healthy calls go to the original destination unchanged.
CALLS = {
    "zym_wifi_start": (0x153c2, (0x1266e,)),
    "zym_wifi_stop": (0x15388, (0x124ac,)),
    "zym_wifi_client_new": (0x3ae0, (0x3c86, 0x588e)),
    "zym_wifi_client_setup": (0x8880, (0x9168, 0x9bd4, 0x9f3c, 0xb26e)),
    "zym_wifi_config": (0xb6b2, (0xc620, 0x1024a)),
    "zym_wifi_about_info": (0x7d38, (0x855e,)),
    "zym_wifi_setup_menu": (0x81f0, (0x10494,)),
}
ENTRIES = (*CALLS, "zym_wifi_client", "zym_spi_request_display")
COMPACT_ENTRIES = (*ENTRIES, "zymatic_home_menu")

def replace_once(source, old, new):
    if source.count(old) != 1:
        raise ValueError(f"Wrong 1.1.14 runtime integration site: {old!r}")
    return source.replace(old, new)

def stage(runtime, compact=False):
    """Source integration applied only inside the temporary bench builder."""
    src = runtime / "src"
    if compact:
        # The compact compiler builds the radio separately, with the archived
        # project root as its include path. Supply its matching declarations.
        for header in HERE.parent.glob("*.h"):
            (src / header.name).write_bytes(header.read_bytes())
    for name in ("scheduler.c", "scheduler.h", "stock.S"):
        data = (HERE / name).read_text()
        if "ZymKey" in (runtime / "zymatic_api.h").read_text():
            data = data.replace("const uint8_t key[32]", "ZymKey key").replace("const char *uid", "ZymUid uid")
        if compact:
            data = data.replace("ZYMATIC_OUTER", "0")
        (src / name).write_text(data)
    path = src / "overlay.c"
    s = replace_once(path.read_text(), '#include "zymatic_api.h"', '#include "zymatic_api.h"\n#include "scheduler.h"')
    s = replace_once(s, "zymatic_network_poll(&network,", "zym_wifi_network_poll(&network,")
    if compact:
        s += """
#define OEM_ENTRY(address,type,args) ((type (*) args)(uintptr_t)((address)/2))
uint8_t zymatic_home_menu(const char *title, uint8_t count, uint8_t selected)
{
    /* COMPACT_HOME */
    return OEM_ENTRY(0x81f0,uint8_t,(const char *,uint8_t,uint8_t))(title,count,selected);
}
"""
    marker = "    /* COMPACT_HOME */" if compact else "    uint8_t due = zymatic_maintenance();"
    s = replace_once(s, marker, """    zym_wifi_home_enter();
    if (zym_wifi_pending()) {
        poll_network(ZYMATIC_OUTER);
        return zym_wifi_home_choice(0xff, oem_millis());
    }
""" + marker)
    s = replace_once(s, "    return OEM_ENTRY(0x81f0,uint8_t,(const char *,uint8_t,uint8_t))(title,count,selected);",
                     """    selected = OEM_ENTRY(0x81f0,uint8_t,(const char *,uint8_t,uint8_t))(title,count,selected);
    return zym_wifi_home_choice(selected, oem_millis());""")
    if not compact:
        s = replace_once(s, "    uint8_t rampz=RAMPZ;\n    char code[9];", "    zym_wifi_home_enter();\n    uint8_t rampz=RAMPZ;\n    char code[9];")
    s += """
/* Called after the About pairing wait. The stock information page itself
 * performs a synchronous network query; end the loan-free scope first. */
void zym_wifi_about_info(void)
{
    do {
        wdt_reset();
        poll_network(ZYMATIC_OUTER);
    } while (zym_wifi_home_choice(4, oem_millis()) == 0xff);
    if (!zym_wifi_quarantined) OEM_ENTRY(0x7d38,void,(void))();
}

uint8_t zym_wifi_setup_menu(const char *title, uint8_t count, uint8_t selected)
{
    char warning[21];
    if (zym_wifi_quarantined) {
        static const char text[] PROGMEM = "Restart for network";
        uint8_t rampz = RAMPZ;
        strcpy_PF(warning, pgm_get_far_address(text));
        RAMPZ = rampz;
        title = warning;
    }
    selected = OEM_ENTRY(0x81f0,uint8_t,(const char *,uint8_t,uint8_t))(title,count,selected);
    return zym_wifi_quarantined && selected < 2 ? 4 : selected;
}
"""
    if compact:
        # The compact profile has no custom pairing wait in About. There
        # is never an HTTP lease in this stock submenu.
        start = s.index("    do {", s.index("void zym_wifi_about_info("))
        end = s.index("    if (!zym_wifi_quarantined)", start)
        s = s[:start] + s[end:]
        s = s.replace("ZYMATIC_OUTER", "0")
    path.write_text(s)
    path = runtime / "zymatic_api_w5100.c"
    path.write_text(path.read_text() + """
/* No register or CS access here. On returning to Ethernet, the existing
 * service checks the configured port and protocol before closing our socket. */
void zym_wired_abandon(ZymaticNetwork *net) { forget(net); net->phase = ABANDON; }
""")
    path = src / "offline_recipe.c"
    s = replace_once(path.read_text(), "static bool local_menu;", "#include \"scheduler.h\"\nstatic bool local_menu;")
    s = replace_once(s, "if (!zymatic_offline_boot || SRAM8(0x230b) || SRAM8(0x230c))",
                     "if (!zym_wifi_quarantined && (!zymatic_offline_boot || SRAM8(0x230b) || SRAM8(0x230c)))")
    path.write_text(s)

def call(address):
    if address < 0 or address % 2 or address >= 0x20000:
        raise ValueError(f"Invalid ATmega1284P code address {address:#x}")
    return struct.pack("<HH", 0x940e, address // 2)

def compose(image, symbols, compact=False):
    output = bytearray(image)
    def checked(at, before, after):
        if len(before) != len(after) or output[at:at+len(before)] != before:
            raise ValueError(f"Wrong original instruction at {at:#x}")
        output[at:at+len(before)] = after
    # Both paths change the shared SPI mode, flush display 0x1693, then
    # return. Verify the whole replaced tail so a changed flush target or
    # additional side effect cannot be hidden by the trampoline.
    tail = bytes.fromhex("8cb5837f8cbd83e996e10e9423af0895")
    display_jump = b"\x0c\x94" + call(symbols["zym_spi_request_display"])[2:]
    for site in (0x5044, 0x8738):
        checked(site, tail, display_jump + tail[4:])
    if compact:
        checked(0x105da, call(0x81f0), call(symbols["zymatic_home_menu"]))
    for dest, (old, sites) in CALLS.items():
        for site in sites:
            checked(site, call(old), call(symbols[dest]))
    # These exact LDS pairs feed OR r24,r25 then branch solely on whether the
    # stock heap-owned radio client exists. Replace its value for THIS check
    # only; preserve registers, pointer ownership and the original branch.
    for site in CLIENT_GUARDS:
        if output[site+8:site+10] != bytes.fromhex("892b"):
            raise ValueError(f"Expected 1.1.14 client null test at {site:#x}")
        checked(site, bytes.fromhex("8091bc169091bd16"), call(symbols["zym_wifi_client"]) + bytes(4))
    return bytes(output)
