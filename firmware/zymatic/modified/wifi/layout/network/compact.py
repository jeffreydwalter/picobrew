"""Measure two replacements for the stock network routines using the original 1.1.14 ROM."""
import argparse
from pathlib import Path
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))
sys.path.insert(0, str(HERE.parents[2] / "tools"))
import usb_stock
from network.requests import PROTECTED_RANGES, SOCKET_RANGES, check_ranges, compose as share_requests, share_socket_addresses
from tail_budget import control_flow


def compose(original, toolchain, directory):
    if not usb_stock.is_original_app(original):
        raise ValueError("Expected the recognized Zymatic 1.1.14 application")
    candidate = bytearray(original)

    def build(name, sources, linker):
        elf = directory / (name + ".elf")
        subprocess.run([toolchain / "avr-gcc", "-Os", "-mmcu=atmega1284p", "-nostdlib",
                        "-Wl,--relax,-T," + str(HERE / linker), "-o", elf,
                        *(HERE / source for source in sources)], check=True)
        return elf

    def section(elf, name):
        binary = directory / (elf.stem + name + ".bin")
        subprocess.run([toolchain / "avr-objcopy", "-j", name, "-O", "binary", elf, binary], check=True)
        return binary.read_bytes()

    encoder = build("encoder", ["escape.c"], "escape.ld")
    readers = build("readers", ["read_fields.c", "read_fields.S"], "read_fields.ld")
    ranges = []
    for start, end, code in ((0x2ed0, 0x3228, section(encoder, ".text")),
                             (0x33a0, 0x3622, section(readers, ".one")),
                             (0xccba, 0xcf38, section(readers, ".two"))):
        if not code or len(code) > end - start:
            raise ValueError("Replacement exceeds its original region or was omitted")
        candidate[start:end] = code + b"\xff" * (end - start - len(code))
        ranges.append((start + len(code), end))
    return bytes(candidate), ranges


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--oem-file", required=True, type=Path)
    parser.add_argument("--toolchain", required=True, type=Path)
    args = parser.parse_args()
    original = usb_stock.decode_application(usb_stock.unpack_source(args.oem_file.read_bytes()))
    with tempfile.TemporaryDirectory() as temp:
        directory = Path(temp)
        candidate, ranges = compose(original, args.toolchain.resolve(), directory)
        candidate, request_windows = share_requests(candidate)
        candidate, socket_windows = share_socket_addresses(candidate)
        stock, test = directory / "original.bin", directory / "test.bin"
        stock.write_bytes(original)
        test.write_bytes(candidate)
        listing = subprocess.check_output([args.toolchain.resolve() / "avr-objdump", "-D", "-z",
                                           "-b", "binary", "-m", "avr:51", stock], text=True)
        check_ranges(*control_flow(original, listing))
        subprocess.run(["node", HERE / "compare.cjs", stock, test,
                        *(f"{a:x}-{b:x}" for a, b in PROTECTED_RANGES + SOCKET_RANGES)], check=True)
        for start, end in ranges + request_windows + socket_windows:
            print(f"[{start:#x}, {end:#x}) {end - start} bytes")


if __name__ == "__main__":
    main()
