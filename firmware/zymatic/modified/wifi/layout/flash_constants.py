"""Place flash-bank-one literals and radio control stubs directly in the candidate image's vacant fragments."""
import re
import subprocess


def assign(map_text, fragments):
    # AVR __flash1 pointer reads add 0x10000 in hardware. Never place these
    # objects in bank zero even if its best-fit fragment is larger.
    if any(start < 0x10000 or end > 0x20000 or start >= end for start, end in fragments):
        raise ValueError("Invalid upper-bank constant fragment")
    text = map_text.split("\n.text ", 1)[1].split("\n.trampolines", 1)[0]
    spans = re.findall(r"^ (\.progmem(?:1)?\.data\.[.\w]+)\s+0x[\da-f]+\s+0x([\da-f]+)\s+", text, re.M)
    # These entry stubs consist only of fixed-size instructions, including
    # absolute tail jumps to the original firmware. All destinations use
    # full AVR program addresses. Keep them aligned before the literals.
    stubs = re.findall(r"^ (\.text\.zym_wifi_(?:start|stop|client|client_new|client_setup|config))\s+0x[\da-f]+\s+0x([\da-f]+)\s+", text, re.M)
    if not spans or len(dict(spans)) != len(spans):
        raise ValueError("Unrecognized linker map for bank-one literals")
    bins = [[] for _ in fragments]
    free = [end - start for start, end in fragments]
    def ordered(entries):
        sizes = [(name, int(value, 16)) for name, value in entries]
        return sorted(sizes, key=lambda item: (-item[1], item[0]))
    for section, size in ordered(stubs) + ordered(spans):
        eligible = [i for i, capacity in enumerate(free) if 0 < size <= capacity]
        if eligible:
            best = min(eligible, key=lambda i: (free[i], i))
            bins[best].append(section)
            free[best] -= size
    return [(f"const_{start:x}", start, end, sections) for (start, end), sections in zip(fragments, bins) if sections]


def stage(script, bins):
    for name, start, end, sections in bins:
        script = script.replace("MEMORY {", f"MEMORY {{\n  {name} (rx) : ORIGIN = {start:#x}, LENGTH = {end-start:#x}")
        selectors = " ".join(f"*({symbol})" for symbol in sections)
        script = script.replace("SECTIONS {", f"SECTIONS {{\n  .{name} : {{ {selectors} }} > {name}")
    return script


def store(packed, bins, toolchain, root):
    moved = 0
    for name, start, end, _ in bins:
        path = root / (name + ".bin")
        subprocess.run([toolchain / "avr-objcopy", "-j", "."+name, "-O", "binary", root / "linked.elf", path], check=True)
        data = path.read_bytes()
        if not 0 < len(data) <= end - start or packed[start:end] != b"\xff" * (end-start):
            raise ValueError("AVR constant overlaps live ROM")
        packed[start:start+len(data)] = data
        moved += len(data)
    print(f"Placed {moved} bytes of firmware constants and control stubs in {len(bins)} upper-flash fragments.", flush=True)
