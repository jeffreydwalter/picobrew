"""Exercise the Hub's stock recipe routes with original Pico and recovered Z parsers.

Invoked by TestStockRecipesWithOEMFirmware. The transport only connects to the
loopback Go test server. Each Pico image runs in its own hash-checking ARM harness.
"""
import collections
import json
import os
from pathlib import Path
import socket
import sys
import tempfile
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[1]


def check_pico(variant, fixture):
    family = "pico-s-pro" if variant == "s-pro" else "pico-c"
    if family == "pico-c":
        os.environ["PICO_C_OEM_VARIANT"] = variant
    sys.path.insert(0, str(ROOT / "firmware" / family / "reverse-engineered/tests"))
    import test_network as net
    from test_recipe import Machine

    hub = urlsplit(fixture["url"])
    assert hub.hostname == "127.0.0.1" and hub.port
    machine = Machine(False)
    # The recipe workflow fixture normally jumps one second on every clock
    # read. The network parser checks it per byte, with a 30-second LAN timeout.
    machine.reset({"clock_step": 1})
    arm = machine.arm
    machine.strput(machine.device + 1, fixture["uid"])
    incoming = collections.deque()

    def connect(cpu):
        path = cpu.cstring(cpu.reg(0))
        assert path.startswith(b"/API/pico/") and b"\r" not in path and b"\n" not in path
        with socket.create_connection((hub.hostname, hub.port), timeout=5) as conn:
            # The CC3200 writes bare LF after Host and Connection.
            conn.sendall(b"GET " + path + b" HTTP/1.0\r\nHost: picobrew.com\nConnection: close\n\n")
            with conn.makefile("rb") as rx:
                reply = rx.read(8192)
        assert reply.startswith(b"HTTP/1.0 200 "), (path, reply)
        incoming.clear()
        incoming.extend(reply)
        return 1

    # Keep the OEM response-marker loop, delimiter parser, numeric conversion,
    # query construction and optional image handling in the execution path.
    def connect_and_wait(cpu):
        connect(cpu)
        while incoming.popleft() != ord("#"):
            pass
        return 1

    arm.hook(net.MAP["pico_api_open_request"], connect)
    arm.hook(net.MAP["pico_api_make_wifi_call"], connect_and_wait)
    arm.hook(net.MAP["WiFiClient_available"], lambda cpu: len(incoming))
    arm.hook(net.MAP["WiFiClient_status"], lambda cpu: bool(incoming))
    arm.hook(net.MAP["WiFiClient_read_byte"], lambda cpu: incoming.popleft())
    arm.hook(net.MAP["pico_api_close_connection"], lambda cpu: incoming.clear())
    for name, address in net.MAP.items():
        if name.startswith("Print_"):
            arm.hook(address, lambda cpu: 0)
    for name in ("pico_recipe_parse_server_response", "pico_api_read_hash_response",
                 "pico_api_fetch_recipe", "pico_api_fetch_default_recipe_failed"):
        arm.unhook(net.MAP[name])
    # Newlib is in this image; exercise its numeric conversion too.
    from test_recipe import SDK
    for name in ("libc_atoi", "libc_atof"):
        arm.unhook(SDK[name])

    # Exercise the original dial-selection workflow with the first menu item
    # chosen at the physical input boundary. It creates/fetches the virtual
    # identifier without reading a physical tag.
    arm.unhook(net.MAP["pico_api_create_picopak"])
    arm.hook(net.MAP["pico_ui_draw_picopak_selection"], lambda cpu: 0)
    arm.hook(net.MAP["pico_menu_choose"], lambda cpu: 0)
    assert machine.invoke("pico_ui_select_recipe", [machine.recipe])["r0"] == 1
    assert machine.text(machine.recipe + 4) == "hub-420"

    # Inspect the stock list parsers and the IDs they actually stored in RAM.
    base = arm.SCRATCH
    ids, titles = base + 64, base + 1024
    status = machine.invoke("pico_api_get_recipe_list", [base, base + 4, base + 8, ids])["r0"]
    assert status == 1 and machine.w(base) == 2 and machine.w(base + 8) == 2
    assert machine.text(ids) == "420"
    status = machine.invoke("pico_api_create_picopak", [int(machine.text(ids))])["r0"]
    identifier = machine.recipe + 4
    assert status == 1 and machine.text(identifier) == "hub-420"

    def check_steps(mash_minutes, hop_minutes, abv, ibu):
        assert machine.text(machine.screen) == "Dial test"
        assert machine.b(machine.recipe + 0x210) == 2
        assert machine.dbl(machine.recipe + 0x218) == abv and machine.w(machine.recipe + 0x214) == ibu
        assert machine.w(machine.recipe + 0x228) == 5
        assert machine.w(machine.recipe + 0x230) == 2
        for index, expected in enumerate([("Mash", 2, 150, 2, mash_minutes), ("Hops", 3, 203, 1, hop_minutes)]):
            entry = machine.recipe + 0x30 + 32 * index
            actual = (machine.text(entry), machine.b(entry + 22), machine.b(entry + 23),
                      machine.w(entry + 24), machine.w(entry + 28))
            assert actual == expected, (actual, expected)

    assert machine.invoke("pico_api_fetch_default_recipe_failed", [identifier])["r0"] == 0
    check_steps(30, 10, 5.5, 35)
    status = machine.invoke("pico_api_get_associated_picopaks", [base, base + 4, base + 8, ids],
                            [titles, base + 12])["r0"]
    assert status == 1 and machine.text(ids) == "hub-420" and machine.text(titles) == "Dial test"
    assert machine.w(base + 12) == 2
    assert machine.text(titles + 22) == "Fifteen step menu exa"
    assert machine.invoke("pico_api_fetch_recipe", [ids, 5, 7])["r0"] == 0
    check_steps(34, 9, 0, 0)
    machine.strput(ids, "AABBCCDD")
    assert machine.invoke("pico_api_fetch_default_recipe_failed", [ids])["r0"] == 0
    check_steps(30, 10, 5.5, 35)
    assert machine.invoke("pico_api_fetch_default_recipe_failed", [ids + 22])["r0"] == 0
    assert machine.b(machine.recipe + 0x210) == 15
    for n in range(15):
        entry = machine.recipe + 0x30 + n * 32
        assert machine.text(entry) == f"Step {n + 1}" and machine.w(entry + 28) == n + 1


def check_z(programs):
    sys.path.insert(0, str(ROOT / "firmware/z-series/reverse-engineered/tests"))
    import local_http_application_native as native
    from local_http_application_cases import seed

    with tempfile.TemporaryDirectory() as build:
        binary = native.build(build)
        inputs = [dict(path="/recipe", json=p,
                       pending=False, pressure=101325, maximum=100,
                       current_hex=seed(1), queued_hex=seed(3)) for p in programs]
        # Repeat the hot program with a 95 °C estimated local boil, so the
        # second limit is 93 °C. This is independently calculated at the Z.
        inputs.append(dict(inputs[-1], maximum=95))
        native_program, ingredient_recipe, capped, local = native.run(binary, inputs)
    for state in (native_program, ingredient_recipe, capped, local):
        assert state["status"] == 200 and state["length"] == 0 and state["assertion"] == 0
        assert state["pending"] and state["body"] == ""
    program = native_program["buffer"]
    assert program["name"] == "Bench recipe" and "Mash" in program["strings"]
    # Z instructions take seconds and 1/256 °C.
    assert [6, 2] in program["code"] and [2, 65 * 256] in program["code"]
    assert [3, 30] in program["code"] and [5, 60] in program["code"]
    translated = ingredient_recipe["buffer"]
    assert translated["name"] == "Test Pale" and {"Mash", "Boil"} <= set(translated["strings"])
    assert [6, 2] in translated["code"] and [6, 1] in translated["code"]
    assert translated["code"].count([3, 3600]) == 2
    assert [2, int(94.45 * 256)] in capped["buffer"]["code"]
    assert [2, 96 * 256] in capped["buffer"]["code"]
    assert local["buffer"]["code"].count([2, 93 * 256]) == 2
    assert [3, 900 + int((94.45 - 93) * 162)] in local["buffer"]["code"]
    assert [3, 900 + (96 - 93) * 162] in local["buffer"]["code"]


if __name__ == "__main__":
    variant = sys.argv[1]
    fixture = json.load(sys.stdin)
    if variant == "z":
        check_z(fixture)
    else:
        check_pico(variant, fixture)
