"""Exercise fragmented storage of the extended Zymatic website under AVR simulation."""

import argparse
import json
import os
from pathlib import Path
import re
import shlex
import struct
import subprocess
import sys
import tempfile
import zlib

import extended
import tail_budget
import prologues
from network.compact import compose
from network import requests
from oled import compact as oled
from tail_budget import usb_stock, build

HERE = Path(__file__).resolve().parent
ASSETS = ("zymatic_pair_js", "zymatic_site", "zymatic_favicon")


def arrange(original, toolchain, root, runtime, with_listener=False):
    stock = root / "original.bin"
    stock.write_bytes(original)
    network, windows = compose(original, toolchain, root)
    network, display_windows = oled.compose(network)
    windows.extend(display_windows)
    network, keyboard_windows = oled.compose_keyboard(network, toolchain, root)
    windows.extend(keyboard_windows)
    network, request_windows = requests.compose(network)
    windows.extend(request_windows)
    network, socket_windows = requests.share_socket_addresses(network)
    windows.extend(socket_windows)
    dis = subprocess.check_output([toolchain / "avr-objdump", "-D", "-z", "-b", "binary",
                                   "-m", "avr:51", stock], text=True)
    # The new network parser uses existing parts of the Wi-Fi routine as well as both holes.
    # Keep all three routines, branch receivers and the timer display hooks out of the allocation.
    excluded = [(0x2ed0, 0x3228), (0x33a0, 0x3622), (0xccba, 0xcf38),
                (0x5044, 0x504a), (0x8738, 0x873e), (oled.TRANSFER, oled.END), (0xa74c, 0xaa50)]
    excluded.extend(requests.PROTECTED_RANGES + requests.SOCKET_RANGES)
    # The simulator models the external device at a few fixed instruction addresses,
    # including the original brew runner's bare RET at 0xfd4c.
    harness = (runtime / "tests/simavr_overlay.c").read_text()
    for pc in re.findall(r"avr->pc\s*=\s*(0x[0-9a-f]+)", harness):
        excluded.append((int(pc, 16), int(pc, 16) + 2))
    spare = []
    protected = []
    if with_listener:
        network, spare, protected = prologues.compose(original, network, dis, excluded)
        excluded.extend(protected)
    sequences = [(pc, end) for pc, end in tail_budget.return_sequences(original, dis)
                 if all(pc >= high or end <= low for low, high in excluded)]
    tails = tail_budget.shared_tails(original, sequences)
    tails.sort(key=lambda t: (-(t[1] - t[0] - len(t[3])), t[0]))
    payload = b""
    for name in ASSETS:
        header = (runtime / "site" / (name + ".h")).read_text()
        payload += bytes(int(value, 16) for value in re.findall(r"0x([0-9a-f]{2})", header))
    slots = [(pc+len(branch), end, (pc, end, receiver, branch)) for pc, end, receiver, branch in tails]
    slots.extend((start, end, None) for start, end in spare)
    slots.sort(key=lambda slot: (-(slot[1]-slot[0]), slot[0]))
    packed = bytearray(network)
    index = []
    used = []
    reserved = []
    # Reserve up to 1,100 bytes for bank-one literals and control stubs. The
    # original 16-bit website reader indexes at most 255 fragments. More tiny
    # fragments cost two index bytes apiece before carrying more site data.
    slack = min(1100, sum(end-start for start, end, _ in slots) - len(payload))
    def open_slot(start, end, tail):
        if tail:
            pc, _, _, branch = tail
            packed[pc:start] = branch
            packed[start:end] = b"\xff" * (end-start)
            used.append(tail)
        elif packed[start:end] != b"\xff" * (end-start):
            raise ValueError("Website or constant fragment overlaps a live instruction")
    # Reserve a small margin so the last website fragment can end halfway
    # through a run without causing the lookup table to grow needlessly.
    if with_listener:
        kept = []
        def constant_preference(slot):
            start, end, _ = slot
            size = end - start
            return max(0, 28 - size), size, start
        for start, end, tail in sorted(slots, key=constant_preference):
            if start >= 0x10000 and slack >= end - start + 16:
                slack -= end-start
                reserved.append((start, end))
                open_slot(start, end, tail)
            else:
                kept.append((start, end, tail))
        slots = sorted(kept, key=lambda slot: (-(slot[1]-slot[0]), slot[0]))
    previous = 0
    for start, end, tail in slots:
        count = min(end - start, len(payload) - previous)
        if not count:
            break
        open_slot(start, end, tail)
        packed[start:start + count] = payload[previous:previous + count]
        previous += count
        index.append((previous, start // 2))

    if previous != len(payload) or b"".join(
            packed[word*2:word*2+end-start] for (end, word), start in zip(index, [0] + [x[0] for x in index])
    ) != payload:
        raise ValueError("Unable to preserve every byte of the compressed website")
    return bytes(packed), payload, index, used, windows, reserved, protected


def install_reader(runtime, index):
    path = runtime / "zymatic_api.c"
    src = path.read_text()
    replacements = {
        "#define SCRIPT_BYTE(i) pgm_read_byte_near(zymatic_pair_js+(i))":
            "#include \"site/scatter.h\"\n#define SCRIPT_BYTE(i) candidate_site_byte(i)",
        '#define ZYMATIC_SITE_SECTION __attribute__((section(".low_web"),used))':
            "#define ZYMATIC_SITE_SECTION",
        "uint32_t start = n->route == FAVICON ? pgm_get_far_address(zymatic_favicon) : pgm_get_far_address(zymatic_site);\n            return pgm_read_byte_far(start + offset);":
            "return candidate_site_byte(sizeof(zymatic_pair_js) + offset +\n"
            "                    (n->route == FAVICON ? sizeof(zymatic_site) : 0));",
    }
    for old, new in replacements.items():
        if src.count(old) != 1:
            raise ValueError("Extended site integration changed")
        src = src.replace(old, new)
    path.write_text(src)
    rows = []
    previous = 0
    for i, (end, _) in enumerate(index):
        width = end - previous
        if rows and rows[-1][2] == width:
            rows[-1] = (end, rows[-1][1], width)
        else:
            rows.append((end, i, width))
        previous = end
    if len(index) > 255 or any(width > 255 for _, _, width in rows):
        raise ValueError("Fragment index requires more than one byte")
    runs = ",\n".join(f"    {{{end}, {first}, {width}}}" for end, first, width in rows)
    words = ", ".join(str(word) for _, word in index)
    template = HERE / "scatter_reader.h.in"
    (runtime / "site/scatter.h").write_text(template.read_text().replace("@RUNS@", runs).replace("@WORDS@", words))
    return len(rows) * 4 + len(index) * 2


def simulator(root, runtime, original, packed, asset, original_hex, prologue_ranges, toolchain):
    # The unmodified composition code checks its source family, checksummed original and all release fields.
    # Apply the candidate's additional ROM substitutions to its temporary synthetic image.
    command = [sys.executable, "-c",
               "import sys,pathlib; sys.path.insert(0,'tools'); import zymatic_build as b; "
               "p=pathlib.Path(sys.argv[1]); image,_=b.build_application((p/'original.bin').read_bytes(),"
               "bytes(range(32)),'0a'*16,'brewery.lan:8080'); (p/'base.bin').write_bytes(image)", str(root)]
    subprocess.run(command, cwd=runtime, check=True)
    result = bytearray((root / "base.bin").read_bytes())
    for i, (old, new) in enumerate(zip(original, packed)):
        if old != new:
            if result[i] != old:
                raise ValueError(f"Extended and network or asset patches overlap at {i:#x}")
            result[i] = new
    manifest = json.loads((runtime / "extension/extension.json").read_text())
    symbols = manifest["symbols"]
    struct.pack_into("<H", result, symbols["provision_legacy_sum"], build.legacy_sum(result))
    struct.pack_into("<I", result, len(result) - 4, zlib.crc32(result[:-4]))
    binary, base = root / "extended.bin", root / "original.bin"
    binary.write_bytes(result)
    if prologue_ranges:
        import pause
        pause.verify_composed_pause(base, binary, root / "linked.elf", toolchain, compact=False)
        sites = root / "prologue-ranges.txt"
        sites.write_text("".join(f"{start:x} {end:x}\n" for start, end in prologue_ranges))
        subprocess.run(["node", HERE / "push_compare.cjs", base, binary, sites], check=True, timeout=60)
    stock_hex = root / "stock.hex"
    stock_hex.write_bytes(original_hex)
    subprocess.run(["node", extended.ROOT.parent / "reverse-engineered/tests/stock-controls.cjs", stock_hex, binary], check=True, timeout=60)
    flags = os.environ.get("SIMAVR_FLAGS")
    if not flags:
        flags = subprocess.check_output(["pkg-config", "--cflags", "--libs", "simavr"], text=True)
    def harness(source):
        target = root / source.stem
        subprocess.run([os.environ.get("CC", "cc"), "-O2", source, *shlex.split(flags), "-o", target], check=True)
        return target
    subprocess.run([harness(runtime / "tests/simavr_tables.c"), base, binary], check=True, timeout=60)
    api = harness(extended.ROOT / "tests/simavr_overlay.c")
    args = [api, binary, str(symbols["zymatic_hub_host"] - 0x800000),
            str(symbols["__extension_bss_end"] - 0x800000), str(symbols["zymatic_hub_authority"] - 0x800000),
            str(symbols["zymatic_image_invalid"])]
    for case in ("api", "corrupt", "factory-good", "factory-bad", "display", "program-status",
                 "program-history", "program-website", "about-pairing", "program-display", "menu-encoder", "dialog-encoder", "program-encoder"):
        subprocess.run([*args, case, base], check=True, timeout=60)
    asset_file = root / "assets.bin"
    asset_file.write_bytes(asset)
    subprocess.run([harness(HERE / "scatter_read.c"), binary, asset_file, str(symbols["candidate_site_byte"])],
                   check=True, timeout=60)
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--toolchain", type=Path, required=True)
    parser.add_argument("--oem-file", type=Path, required=True)
    parser.add_argument("--with-listener", action="store_true", help="Exercise the radio with its normal firmware foreground hooks")
    parser.add_argument("--write-release", type=Path, help="Export the reproducible USB installer template here")
    args = parser.parse_args()
    original_hex = usb_stock.unpack_source(args.oem_file.read_bytes())
    original = usb_stock.decode_application(original_hex)
    if not usb_stock.is_original_app(original):
        raise ValueError("Expected original 1.1.14 application")
    toolchain = args.toolchain.resolve()
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        runtime = extended.stage(root)
        packed, asset, index, tails, windows, reserved, prologue_ranges = arrange(original, toolchain, root, runtime, with_listener=args.with_listener)
        index_size = install_reader(runtime, index)
        sys.path.insert(0, str(HERE / "http"))
        from routes import stage as pack_routes
        from literals import stage as pack_literals
        if args.with_listener:
            # Add pause/status text before tokenizing responses and before
            # installing the immutable route table shared by both transports.
            sys.path.insert(0, str(HERE.parent / "integration"))
            import pause
            pause.stage_api(runtime, compact=False)
        pack_routes(runtime)
        pack_literals(runtime)
        compiler = runtime / "tools/compile_extension.py"
        source = compiler.read_text()
        compiler.write_text(source.replace("entry_points = [*build.OFFLINE,",
                                           'entry_points = ["candidate_site_byte", *build.OFFLINE,'))
        if args.with_listener:
            from extended_listener import link
            packed = link(root, runtime, toolchain, windows, packed, reserved)
        else:
            subprocess.run([sys.executable, compiler, "--toolchain", toolchain, "--out-dir", runtime / "extension"], check=True)
        tail_budget.check(root, root / "original.bin", original, tails)
        manifest = simulator(root, runtime, original, packed, asset, original_hex, prologue_ranges, toolchain)
        if args.with_listener:
            import release
            release.export(original, (root / "extended.bin").read_bytes(), manifest["symbols"], "extended", args.write_release)
        low_free = build.tables.POOL_END - build.tables.SEEK_END - manifest["low_bytes"]
        high_free = build.LIMIT - build.START - manifest["extension_bytes"]
        print(f"{len(asset)} site bytes in {len(index)} fragments with a {index_size}-byte index. "
              f"The extended application has {low_free} bytes free in low flash and {high_free} in high flash. "
              f"Reclaimed {sum(end-start for start,end in windows)} bytes in the network, display and keyboard.")


if __name__ == "__main__":
    main()
