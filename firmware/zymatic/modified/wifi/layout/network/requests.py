"""Share repeated HTTP writes and their original five-millisecond waits in OEM 1.1.14."""
from hashlib import sha256
import struct

# Straight-line portions of the OEM Wi-Fi request builders. Their start and
# fallthrough addresses stay fixed. They contain no interior control-flow or
# skip target, potential address-taken entry from the data tables, or immediate
# word-address pair. Each pair is a half-open range of flash byte addresses.
PROTECTED_RANGES = (
    (0x42e8, 0x4308), (0x61b8, 0x61fe), (0x62aa, 0x62de),
    (0x674e, 0x67a8), (0x6afe, 0x6b5c), (0x6fdc, 0x7000),
    (0x8820, 0x8840), (0x8e82, 0x8e92), (0x8e9c, 0x8ee4),
)
ORIGINAL_HASH = "8b1908223a1fc3ec0bd331387e85edcf1b55555845dbd11e04dac996db492afe"
CLIENT = bytes.fromhex("8091bc169091bd16")   # LDS r24, 0x16bc; LDS r25, 0x16bd
WRITE = bytes.fromhex("0e94038a65e070e080e090e00e94d8ce")
RET = bytes.fromhex("0895")
ETHERNET_ADDRESSES = (0xbeee, 0xc000, 0xd210, 0xd39e, 0xd80c, 0xdd58)
SOCKET_RANGES = tuple((start, start + 42) for start in ETHERNET_ADDRESSES)


def check_ranges(instructions, entries, protected):
    allowed = {"ldi", "lds", "ldd", "std", "call", "mov", "movw", "adc", "add", "subi", "sbci", "eor"}
    for start, end in PROTECTED_RANGES + SOCKET_RANGES:
        if start not in instructions or end not in instructions:
            raise ValueError("A request window splits an instruction")
        for pc in range(start, end, 2):
            if pc in protected or (pc != start and pc in entries):
                raise ValueError(f"Overlapping code entry at {pc:#x}")
            if pc in instructions and instructions[pc][0] not in allowed:
                raise ValueError(f"Unexpected instruction at {pc:#x}")


def branch(pc, target, call=False):
    delta = (target - pc - 2) // 2
    if -2048 <= delta <= 2047:
        return struct.pack("<H", (0xd000 if call else 0xc000) | (delta & 0xfff))
    return struct.pack("<HH", 0x940e if call else 0x940c, target // 2)


def compose(image):
    if sha256(b"".join(image[a:b] for a, b in PROTECTED_RANGES)).hexdigest() != ORIGINAL_HASH:
        raise ValueError("Unrecognized 1.1.14 HTTP request code")
    # Reuse part of the longest savings window for the two subroutines. A
    # tail jump to delay() gives it the same return stack depth as the original.
    client_address = 0x6b40
    write_address = client_address + len(CLIENT) + len(RET)
    helpers = CLIENT + RET + WRITE[:-4] + branch(write_address + len(WRITE) - 4, 0x19db0)
    candidate = bytearray(image)
    windows = []
    for start, end in PROTECTED_RANGES:
        code = bytearray()
        pc = start
        while pc < end:
            for sequence, target in ((WRITE, write_address), (CLIENT, client_address)):
                if image.startswith(sequence, pc, end):
                    code.extend(branch(start + len(code), target, call=True))
                    pc += len(sequence)
                    break
            else:
                # All other instructions in these ranges have absolute operands.
                code.extend(image[pc:pc+2])
                pc += 2
        code.extend(branch(start + len(code), end))
        free_start = start + len(code)
        free_end = client_address if start <= client_address < end else end
        if free_start > free_end:
            raise ValueError("Request writer overlaps its stored helper")
        candidate[start:end] = code + b"\xff" * (end-free_start)
        windows.append((free_start, free_end))
    if candidate[client_address:client_address+len(helpers)] != b"\xff" * len(helpers):
        raise ValueError("No room for the stock request helpers")
    candidate[client_address:client_address+len(helpers)] = helpers
    return bytes(candidate), windows


def share_socket_addresses(image):
    # Each original caller stores the four IP octets and loads the same
    # W5100 client. The following configurable TCP port remains at its OEM PC.
    first, second, *_ = ETHERNET_ADDRESSES
    code = image[first:first+42]
    if (sha256(code).hexdigest() != "035004a743479ed1a274170cdcd4ff95bcd5fadbc60594ae24fc44da72e2e202"
            or any(image[a:b] != code for a, b in SOCKET_RANGES)):
        raise ValueError("Unrecognized Ethernet address construction")
    shared = first + 4
    def invocation(pc, continuation):
        prefix = code[:8] + branch(pc + 8, shared, call=True)
        return prefix + branch(pc + len(prefix), continuation)

    # The first site's prefix lives in the second site's vacated bytes, so
    # all six callers share the original instructions without a separate bank.
    second_entry = invocation(second, second+42)
    first_entry = second + len(second_entry)
    candidate = bytearray(image)
    windows = []
    for start, end in SOCKET_RANGES:
        if start == first:
            jump = branch(start, first_entry)
            emitted = jump.ljust(4, b"\xff") + code[8:] + RET
        elif start == second:
            emitted = second_entry + invocation(first_entry, first+42)
        else:
            emitted = invocation(start, end)
        if len(emitted) > end - start:
            raise ValueError("Ethernet address helper exceeds its original instructions")
        candidate[start:end] = emitted.ljust(end - start, b"\xff")
        if end - start - len(emitted) >= 4:
            windows.append((start + len(emitted), end))
    return bytes(candidate), windows
