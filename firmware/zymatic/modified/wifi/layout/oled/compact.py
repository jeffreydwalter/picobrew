"""Measure the flash recovered by sharing original Zymatic OLED SPI command transactions."""
import argparse
from hashlib import sha256
from pathlib import Path
import struct
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parents[2] / "tools"))
import usb_stock

TRANSFER = 0x15e46
CLEAR = 0x1604e
INIT = 0x1623e
END = 0x168b2
DISPLAY_SHA256 = "0629ca4787cb1a7f48edac56c7f5e4900e00c9066ce6b9836bcba6ddfec2714e"
RET = bytes.fromhex("0895")


def compose(original):
    if sha256(original[TRANSFER:END]).hexdigest() != DISPLAY_SHA256:
        raise ValueError("Unrecognized Zymatic 1.1.14 display routines")
    candidate = bytearray(original)
    candidate[TRANSFER:END] = b"\xff" * (END - TRANSFER)
    labels = {}
    refs = []
    pos = TRANSFER

    def label(name):
        labels[name] = pos

    def emit(data):
        nonlocal pos
        candidate[pos:pos + len(data)] = data
        pos += len(data)

    def relative(op, name):
        refs.append((pos, op, name))
        emit(b"\0\0")

    def commands(values):
        # r24 carries SPDR's argument. The named subroutine uses
        # the stock GPIO loads, status-poll loop and chip-select deassertion.
        for value in values:
            emit(struct.pack("<H", 0xe080 | ((value & 0xf0) << 4) | (value & 15)))
            relative(0xd000, "send")

    # The same code can execute at a different PC: all direct external calls in
    # the retained slices are absolute, and all relative edges stay in their slices.
    emit(original[TRANSFER:0x15e52])
    commands((0x15, 0, 0x3f, 0x75, 0, 0x3f))
    emit(original[0x15fc2:CLEAR])  # enter pixel-data mode, transfer 4096 bytes
    label("send")
    emit(bytes.fromhex("082e"))  # mov r0, r24 (the stock status poll overwrites r0)
    emit(original[0x1631c:0x16344])  # CS high, D/C low, CS low
    emit(bytes.fromhex("0ebc"))  # out SPDR, r0
    emit(original[0x16348:0x1635a])  # poll SPIF, then CS high
    emit(RET)

    label("setup")
    emit(original[INIT:0x1631c])  # original pin/SPI configuration and reset delays
    if pos > CLEAR:
        raise ValueError("Transfer and initialization overlap the clear entry")
    setup_end = pos
    pos = CLEAR
    emit(original[CLEAR:0x16068])  # save Y and clear all 1024 bytes
    emit(bytes.fromhex("ed01"))  # movw Y, X (display object)
    commands((0x24, 0, 0x40, 0x40, 0))
    # The stock X-based final deassertion leaves X at its mask byte and sets
    # SREG from ADIW then OR. Maintain this for interrupt/debug observers.
    emit(original[0x16230:0x16232] + original[0x16234:0x16236])
    emit(original[0x16238:INIT])
    label("init_commands")
    commands((0xae, 0xb3, 0x91, 0xa8, 0x3f, 0xa2, 0x4c, 0xa1, 0, 0xa0, 0x50,
              0xad, 2, 0x81, 0x60, 0x86, 0xbe, 2, 0xbf, 0x0e, 0xa5, 0xaf, 0xa4))
    emit(original[0x168ac:END])
    clear_end = pos
    pos = setup_end
    relative(0xc000, "init_commands")
    setup_end = pos
    if setup_end > CLEAR or clear_end > INIT:
        raise ValueError("OLED code overlaps a preserved entry")
    pos = INIT
    relative(0xc000, "setup")
    for site, op, name in refs:
        delta = (labels[name] - (site + 2)) // 2
        if not -2048 <= delta < 2048:
            raise ValueError("AVR relative branch outside range")
        struct.pack_into("<H", candidate, site, op | (delta & 0xfff))
    return bytes(candidate), [(setup_end, CLEAR), (clear_end, INIT), (pos, END)]


def compose_keyboard(original, toolchain, directory):
    if sha256(original[0xa74c:0xaa50]).hexdigest() != "0051e7cf7999559a365ce1678615b13c11a8ca479fdc28bd71cca10248794a74":
        raise ValueError("Unrecognized Zymatic 1.1.14 key drawing routines")
    image = bytearray(original)
    elf = directory / "keyboard.elf"
    subprocess.run([toolchain / "avr-gcc", "-mmcu=atmega1284p", "-Os", "-std=gnu11",
                    "-nostdlib", "-Wl,--relax,-T," + str(HERE / "keyboard.ld"),
                    HERE / "keyboard.S", HERE / "keyboard.c", "-o", elf], check=True)
    ranges = []
    for section, start, end in ((".first", 0xa788, 0xa8ce), (".second", 0xa90a, 0xaa50)):
        binary = directory / (section + ".bin")
        subprocess.run([toolchain / "avr-objcopy", "-O", "binary", "-j", section, elf, binary], check=True)
        code = binary.read_bytes()
        if not 0 < len(code) <= end - start:
            raise ValueError("Keyboard code does not fit")
        image[start:end] = code + b"\xff" * (end - start - len(code))
        ranges.append((start + len(code), end))
    return bytes(image), ranges


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--oem-file", type=Path, required=True)
    parser.add_argument("--toolchain", type=Path, required=True)
    args = parser.parse_args()
    original = usb_stock.decode_application(usb_stock.unpack_source(args.oem_file.read_bytes()))
    candidate, holes = compose(original)
    with tempfile.TemporaryDirectory() as temp:
        stock, test = (Path(temp) / name for name in ("stock.bin", "candidate.bin"))
        candidate, keys = compose_keyboard(candidate, args.toolchain.resolve(), Path(temp))
        holes += keys
        stock.write_bytes(original)
        test.write_bytes(candidate)
        subprocess.run(["node", HERE / "compare.cjs", stock, test], check=True)
    for start, end in holes:
        print(f"[{start:#x}, {end:#x}) {end - start} bytes")
    print(f"Total reclaimed: {sum(end - start for start, end in holes)} bytes")


if __name__ == "__main__":
    main()
