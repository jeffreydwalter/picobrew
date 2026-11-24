"""Share identical AVR register-save sequences without consuming a register or static SRAM."""
from collections import defaultdict
import re
import struct

from tail_budget import control_flow


def word(op):
    return struct.pack("<H", op)


def jump(pc):
    return struct.pack("<HH", 0x940c, pc // 2)


def push(reg):
    return word(0x920f | reg << 4)


def pop(reg):
    return word(0x900f | reg << 4)


def load_y(reg, offset):
    if not 0 <= offset <= 63:
        raise ValueError("LDD stack offset is outside its instruction range")
    return word(0x8008 | reg << 4 | (offset & 7) | (offset & 24) << 7 | (offset & 32) << 8)


def body(registers):
    # The site pushes its first two registers and calls here. Temporarily use
    # those saved registers to hold the real return PC while pushing the others.
    first, second, *remaining = registers
    n = len(registers)
    return (pop(first) + pop(second) + b"".join(map(push, remaining))
            + push(second) + push(first)
            + push(28) + push(29) + bytes.fromhex("cdb7deb7")  # Y = SP
            + load_y(first, n + 4) + load_y(second, n + 3)
            + pop(29) + pop(28) + bytes.fromhex("0895"))


def groups(original, packed, disassembly, excluded):
    insns, entries, protected = control_flow(original, disassembly)
    calls = {int(match[1], 16) for op, arg, _, comment in insns.values()
             if op in {"call", "rcall"} and (match := re.search(r"0x([0-9a-f]+)", comment or arg))}
    # Some drawing callbacks are reached only through the OEM dispatch table.
    # A PUSH immediately after a return starts a separate instruction path.
    calls.update(pc + size for pc, (op, _, size, _) in insns.items() if op in {"ret", "reti"})
    interrupt_entries = {2 * int.from_bytes(original[pc+2:pc+4], "little")
                         for pc in range(4, 0x84, 4) if original[pc:pc+2] == b"\x0c\x94"}
    possible = defaultdict(list)
    for entry in sorted(calls):
        if entry in interrupt_entries:
            continue
        pc = entry
        saved = []
        while pc in insns and insns[pc][0] == "push":
            if pc in protected or (pc != entry and pc in entries):
                break
            if any(a <= pc < b for a, b in excluded) or original[pc:pc+2] != packed[pc:pc+2]:
                break
            register = int(insns[pc][1][1:])
            if register in saved or register < 2:
                break
            saved.append(register)
            pc += 2
        # A common suffix can share a helper with a longer save sequence. A
        # fragment of five pushes or fewer would scarcely pay for the call.
        for i in range(len(saved) - 5):
            for j in range(i + 6, len(saved) + 1):
                registers = tuple(saved[i:j])
                if 28 not in registers[:2] and 29 not in registers[:2]:
                    possible[registers].append((entry + 2*i, entry + 2*j))
    chosen = []
    occupied = []
    while possible:
        scores = []
        for registers, spans in possible.items():
            spans = [span for span in spans if not any(span[0] < b and a < span[1] for a, b in occupied)]
            # Ten bytes at the call site; the helper needs a few jumps if split.
            gain = len(spans) * (2 * len(registers) - 10) - len(body(registers)) - 12
            if gain > 0:
                # A shorter shared suffix can still serve the smaller
                # routines after the larger contiguous windows are claimed.
                scores.append((gain * len(registers) ** 2, registers, spans))
        if not scores:
            break
        _, registers, spans = max(scores)
        chosen.append((registers, spans))
        occupied.extend(spans)
        del possible[registers]
    return chosen


def compose(original, packed, disassembly, excluded=()):
    chosen = groups(original, packed, disassembly, excluded)
    candidate = bytearray(packed)
    sites = [span for _, spans in chosen for span in spans]
    available = [(start+10, end) for start, end in sites]
    for start, end in available:
        candidate[start:end] = b"\xff" * (end - start)

    def store(code):
        # Prefer the lower flash bank for helper code. This leaves complete
        # upper-bank windows available for the AVR's __flash1 response literals.
        chunks = []
        while code:
            fits = [(i, a, b) for i, (a, b) in enumerate(available)
                    if b-a >= len(code) or b-a >= 8]
            if not fits:
                raise ValueError("Insufficient ROM for a register-save helper")
            i, a, b = min(fits, key=lambda t: (t[1] >= 65536, t[2]-t[1] < len(code),
                                               abs(t[2]-t[1]-len(code)), t[1]))
            available.pop(i)
            size = min(b-a, len(code)) if b-a >= len(code) else b-a-4
            chunks.append((a, code[:size]))
            code = code[size:]
            used = size + (4 if code else 0)
            if a + used < b:
                available.append((a + used, b))
        for i, (a, chunk) in enumerate(chunks):
            if i+1 < len(chunks):
                chunk += jump(chunks[i+1][0])
            candidate[a:a+len(chunk)] = chunk
        return chunks[0][0]
    for registers, spans in chosen:
        helper = store(body(registers))
        for start, end in spans:
            # The original continuation follows this region, within RJMP reach.
            skip = word(0xc000 | ((end - start - 10) // 2))
            stub = push(registers[0]) + push(registers[1]) + struct.pack("<HH", 0x940e, helper // 2) + skip
            candidate[start:start+len(stub)] = stub
    return bytes(candidate), sorted(available), sorted(sites)
