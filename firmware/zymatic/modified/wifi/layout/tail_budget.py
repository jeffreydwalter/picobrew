"""Measure identical OEM AVR return sequences as candidates for fragmented asset storage."""

import argparse
import os
from pathlib import Path
import re
import shlex
import struct
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools"))
import oem_tables as tables
import usb_stock
import zymatic_build as build

SKIPS = {"cpse", "sbrs", "sbrc", "sbic", "sbis"}
INSTRUCTION = re.compile(r"^\s*([0-9a-f]+):\s+((?:[0-9a-f]{2} )+)\s*\t(\w+)\s*(.*?)\s*(?:;\s*(.*))?$")
LOAD = re.compile(r"r(\d+), 0x([\da-f]+)", re.I)


def control_flow(image, disassembly):
    instructions = {}
    for line in disassembly.splitlines():
        match = INSTRUCTION.match(line)
        if match:
            pc = int(match[1], 16)
            if 0x2300 <= pc < tables.CRT:
                instructions[pc] = match[3], match[4], len(bytes.fromhex(match[2])), match[5]
    if not instructions:
        raise ValueError("Cannot decode AVR disassembly")
    # Explicit control-flow destinations, both taken and skipped instructions.
    entries = set()
    for pc, (op, arg, size, comment) in instructions.items():
        if op.startswith("br") or op in {"jmp", "rjmp", "call", "rcall"}:
            match = re.search(r"0x([\da-f]+)", comment or arg)
            if match:
                entries.add(int(match[1], 16))
        nx = pc + size
        if nx in instructions:
            if op in SKIPS:
                entries.add(nx)
                entries.add(nx + instructions[nx][2])
            # Conservatively treat adjacent immediate loads to a register pair as a code pointer.
            other_op, other_arg, _, _ = instructions[nx]
            if op == other_op == "ldi":
                a, b = LOAD.match(arg), LOAD.match(other_arg)
                if a and b and abs(int(a[1]) - int(b[1])) == 1:
                    lo, hi = (a, b) if int(a[1]) < int(b[1]) else (b, a)
                    entries.add(2 * (int(lo[2], 16) | int(hi[2], 16) << 8))
    # Any two neighboring bytes in the original data could be an address-taken function.
    for start, end in ((0x84, 0x2300), (tables.CRT, usb_stock.APP_SIZE)):
        for position in range(start, end - 1):
            entries.add(2 * int.from_bytes(image[position:position+2], "little"))
    # Leave every documented patch site and every compiler-library signature intact.
    _, record = build.template()
    protected = set()
    for _, pc, before, _ in build.patch_list(record["symbols"]):
        protected.update(range(pc, pc + len(before), 2))
    for _, pc, size, _ in tables.LIBRARY:
        protected.update(range(pc, pc + size, 2))
    return instructions, entries, protected


def return_sequences(image, disassembly):
    instructions, entries, protected = control_flow(image, disassembly)
    sequences = []
    for end, (op, _, _, _) in instructions.items():
        if op != "ret" or end in protected:
            continue
        start = end
        while start not in entries and start - 2 not in protected:
            previous = instructions.get(start - 2)
            if previous is None or previous[0] != "pop":
                break
            start -= 2
        if end + 2 - start >= 6:
            sequences.append((start, end + 2))
    return sequences


def shared_tails(image, sequences):
    # Keep one original receiver for each identical suffix. A tail jump adds no stack frame.
    receivers = {}
    result = []
    for start, end in sorted(sequences, key=lambda seq: seq[0] - seq[1]):
        for pc in range(start, end - 4, 2):
            body = image[pc:end]
            receiver = receivers.get(body)
            if receiver is None:
                continue
            delta = (receiver - pc - 2) // 2
            if -2048 <= delta <= 2047:
                branch = struct.pack("<H", 0xc000 | (delta & 0xfff))
            else:
                branch = struct.pack("<HH", 0x940c, receiver // 2)
            result.append((pc, end, receiver, branch))
            break
        else:
            for pc in range(start, end - 4, 2):
                receivers.setdefault(image[pc:end], pc)
    return result


def check(directory, stock, image, tails):
    binary = directory / "tail-comparison.bin"
    reconstructed = bytearray(image)
    for start, end, _, branch in tails:
        reconstructed[start:end] = branch + b"\xff" * (end - start - len(branch))
    binary.write_bytes(reconstructed)
    cases = directory / "tails.txt"
    cases.write_text("".join(f"{start:x} {end:x}\n" for start, end, _, _ in tails))
    flags = os.environ.get("SIMAVR_FLAGS")
    if flags is None:
        flags = subprocess.check_output(["pkg-config", "--cflags", "--libs", "simavr"], text=True)
    harness = directory / "tails"
    subprocess.run([os.environ.get("CC", "cc"), "-O2", Path(__file__).with_name("tail_compare.c"),
                    *shlex.split(flags), "-o", harness], check=True)
    subprocess.run([harness, stock, binary, cases], check=True, timeout=60)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--toolchain", required=True, type=Path)
    parser.add_argument("--oem-file", required=True, type=Path)
    parser.add_argument("--simulate", action="store_true")
    args = parser.parse_args()
    original = usb_stock.decode_application(usb_stock.unpack_source(args.oem_file.read_bytes()))
    if not usb_stock.is_original_app(original):
        raise ValueError("Use the verified Zymatic 1.1.14 application")
    with tempfile.TemporaryDirectory() as temp:
        directory = Path(temp)
        stock = directory / "original.bin"
        stock.write_bytes(original)
        disassembly = subprocess.check_output([args.toolchain.resolve() / "avr-objdump", "-D", "-z", "-b", "binary",
                                              "-m", "avr:51", stock], text=True)
        tails = shared_tails(original, return_sequences(original, disassembly))
        if not tails:
            raise ValueError("Expected repeated 1.1.14 return sequences")
        sizes = [end - start - len(branch) for start, end, _, branch in tails]
        tiles = sum(size // 8 for size in sizes)
        print(f"{len(tails)} fragments, {sum(sizes)} gross bytes; {tiles} eight-byte tiles. "
              f"Net {6 * tiles} bytes with one two-byte address per tile.", flush=True)
        if args.simulate:
            check(directory, stock, original, tails)


if __name__ == "__main__":
    main()
