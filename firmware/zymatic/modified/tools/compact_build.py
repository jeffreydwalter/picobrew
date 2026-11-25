"""Compose compact Zymatic firmware under the smallest stock boot boundary."""

import hashlib
import json
from pathlib import Path
import re
import struct
import zlib

import offline_program as recipes
import usb_stock as stock
from zymatic_build import INITIAL_RELEASE, RECIPE_MENU_PATCH, VERSION, has_oem_recipe_label, hub_port_patches

ROOT = Path(__file__).resolve().parents[1]
TEMPLATE = ROOT / "extension"
SOURCE_REVISION = "b278fe1735d6bc983c107c55120c2a67b4ac6ae3"
START = 0x1bc00
LIMIT = 0x1e000
BSS = 0x2c8a
PROFILE = "compact"
PREVIEW_VERSION = "1.2.0"
FIELDS = {"provision_key": 32, "provision_uid": 33, "provision_host": 64, "provision_recipe": recipes.SIZE}

SERIAL_READS = (
    0x6548, 0x6e18, 0x8776, 0x8f52,
    0xb9c8, 0xbff6, 0xc15e, 0xd31c, 0xd99a, 0xdd02,
)

OFFLINE = {
    "zymatic_sync_or_local": [(0xdb88, "0e946f6d"), (0xdb94, "0e946f6d")],
    "zymatic_begin_brew": [(0xf732, "0e94e025")],
    "zymatic_start_session": [(0xd646, "0e94896a")],
    "zymatic_recover_recipe": [(0x100ca, "0e94436c")],
    "zymatic_wifi_or_offline": [(0x110ba, "0e941959")],
    "zymatic_wifi_boot_branch": [(0x110c2, "80910c23")],
}
DERIVED = {"provision_legacy_sum": 2, "provision_crc": 4}


def legacy_sum(image):
    return 2 * sum(image[i] | image[i+1] << 8 for i in range(0, 0x10000, 4)) & 0xffff


def sha(data):
    return hashlib.sha256(data).hexdigest()


def template(version=VERSION):
    if version == VERSION:
        directory = TEMPLATE / "compact"
        manifest_hash = "b79e19e6967bdcf48a9baab497a3938a551d6a75a594222c63fe1795e6bbe248"
    elif version == INITIAL_RELEASE:
        directory = TEMPLATE / INITIAL_RELEASE / PROFILE
        manifest_hash = "0c236a65358e2b8583b6d57c053509493b234387fe17f51f464693bce1d18840"
    elif version == PREVIEW_VERSION:
        directory = TEMPLATE / PREVIEW_VERSION
        manifest_hash = "6471500f3f04e2435ce17d96de557219eab657c7401e791721e62f3b745d4df1"
    else:
        raise ValueError("Unknown compact Zymatic application version")
    raw = (directory / "extension.json").read_bytes()
    if sha(raw) != manifest_hash:
        raise ValueError(f"Zymatic {version} compact metadata differs from its verified manifest")
    record = json.loads(raw)
    code = (directory / "extension.bin").read_bytes()
    if (record["extension_sha256"] != sha(code)
            or len(code) != record["extension_bytes"] or not 0 < len(code) <= LIMIT - START):
        raise ValueError("The AVR extension differs from its recorded sources or binary; rebuild the template")
    for name, size in (FIELDS | DERIVED).items():
        offset = record["symbols"][name] - START
        if not 0 <= offset <= len(code) - size or code[offset:offset+size] != bytes(size):
            raise ValueError("The public extension must contain empty private provisioning fields")
    if record["symbols"]["provision_crc"] + 4 != START + len(code):
        raise ValueError("Full image CRC must be the last field")
    return code, record


def immediate(op, register, value):
    return struct.pack("<H", op | ((register - 16) << 4) | ((value & 0xf0) << 4) | (value & 15))


def load_pointer(register, address):
    return immediate(0xe000, register, address & 255) + immediate(0xe000, register+1, address >> 8)


def call(address):
    if not START <= address < LIMIT or address % 2:
        raise ValueError("AVR extension entry falls outside application flash")
    return b"\x0e\x94" + struct.pack("<H", address // 2)


def patch_list(symbols, port=80, version=VERSION, *, legacy_menu=False):
    end = symbols["__extension_bss_end"] - 0x800000
    host = symbols["zymatic_hub_host"] - 0x800000
    authority = symbols["zymatic_hub_authority"] - 0x800000
    if not BSS <= authority or authority + 64 > end:
        raise ValueError("Hub authority exceeds reserved SRAM")
    if not BSS <= host < end <= BSS + 0x300 or host + 64 > end:
        raise ValueError("AVR extension data exceeds the reserved SRAM region")
    # Sites refer to the exact decoded 1.1.14 image. Each edit is reversible,
    # has a mandatory preimage and is covered by the original application hash.
    patches = [
        ("setup", 0x1a7c4, bytes.fromhex("0e94b488"), call(symbols["zymatic_setup"])),
        ("foreground", 0x1a7d4, bytes.fromhex("0e94e3d0"), call(symbols["zymatic_after_loop"])),
        ("menu-button", 0x831e, bytes.fromhex("0e941689"), call(symbols["zymatic_menu_button"])),
        ("brew-button", 0xf488, bytes.fromhex("0e941689"), call(symbols["zymatic_brew_button"])),
        ("factory-stamp", 0x4a78, bytes.fromhex("7a836983"), call(symbols["zymatic_factory_stamp"])),
        ("factory-check", 0x7adc, bytes.fromhex("e616f706"), call(symbols["zymatic_factory_compare"])),
        ("zero-bss-high", 0x2332, bytes.fromhex("1ce2"), immediate(0xe000, 17, end >> 8)),
        ("zero-bss-low", 0x233c, bytes.fromhex("aa38"), immediate(0x3000, 26, end & 255)),
        ("malloc-start", 0x1bb10, struct.pack("<H", BSS), struct.pack("<H", end)),
    ]
    if version == VERSION and not legacy_menu:
        patches.append(RECIPE_MENU_PATCH)
    for pc in SERIAL_READS:
        patches.append(("stock-serial", pc, bytes.fromhex("0e94a121"),
                        call(symbols["zymatic_read_stock_serial"])))
    for name, original, locations in (
            ("wifi-host", 0x14d, [0x2eaa, 0x4d08, 0x9a82]),
            ("ethernet-host", 0x886, [0xa734, 0x1032c, 0x1037c])):
        for i, pc in enumerate(locations):
            pointer = authority if i == 0 else host
            patches.append((name, pc, load_pointer(22, original), load_pointer(22, pointer)))
    patches.extend(hub_port_patches(port))
    # About Zymatic and the startup banner both convert three literal unsigned
    # components with base-10 itoa. The stock wire protocol remains 1.1.14.
    _, minor, patch = map(int, version.partition("-")[0].split("."))
    for label, pc, old, new in [("about-minor", 0x8428, 1, minor), ("about-patch", 0x844c, 14, patch),
                                ("boot-minor", 0xa444, 1, minor), ("boot-patch", 0xa468, 14, patch)]:
        patches.append((label, pc, immediate(0xe000, 24, old), immediate(0xe000, 24, new)))
    for symbol, sites in OFFLINE.items():
        for pc, before in sites:
            instruction = call(symbols[symbol])
            if symbol == "zymatic_wifi_boot_branch":
                instruction = b"\x0c\x94" + instruction[2:]
            patches.append((symbol, pc, bytes.fromhex(before), instruction))
    return patches


def hub_origin(authority):
    host, separator, port = authority.partition(":")
    if len(authority) > 63 or not all(re.fullmatch(r"[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?", s)
                                      for s in host.split(".")):
        raise ValueError("Use a Hub hostname or IPv4 address with optional port, at most 63 ASCII characters")
    if separator and (not re.fullmatch(r"[1-9][0-9]{0,4}", port) or int(port) > 65535):
        raise ValueError("The Hub TCP port must be from 1 to 65535")
    return host, int(port) if separator else 80


def provisioning(key, uid, hub_host):
    if not isinstance(key, bytes) or len(key) != 32 or key in (bytes(32), b"\xff"*32):
        raise ValueError("Supply a private, non-default 32-byte API key")
    if not re.fullmatch(r"[0-9a-f]{32}", uid) or uid == "0"*32:
        raise ValueError("Supply the brewer's 32-character lowercase hexadecimal Hub UID")
    return {"provision_key": key, "provision_uid": uid.encode("ascii") + b"\0",
            "provision_host": hub_host.encode("ascii").ljust(64, b"\0")}


def build_application(original, key, uid, hub_host="picobrew.com", offline_program=None, *, version=VERSION, legacy_menu=False):
    if not stock.is_original_app(original):
        raise ValueError("Use the verified, decoded application from the Zymatic 1.1.14 archive")
    image = bytearray(original + b"\xff"*(START - len(original)))
    code, record = template(version)
    image.extend(code)
    symbols = record["symbols"]
    _, port = hub_origin(hub_host)
    patches = patch_list(symbols, port, version, legacy_menu=legacy_menu)
    for name, pc, before, after in patches:
        if image[pc:pc+len(before)] != before:
            raise ValueError(f"Original 1.1.14 bytes differ at {name}")
        image[pc:pc+len(after)] = after
    fields = provisioning(key, uid, hub_host)
    fields["provision_recipe"] = recipes.pack_program(offline_program)
    for name, raw in fields.items():
        where = symbols[name]
        image[where:where+len(raw)] = raw
    where = symbols["provision_legacy_sum"]
    image[where:where+2] = struct.pack("<H", legacy_sum(image))
    image[-4:] = struct.pack("<I", zlib.crc32(image[:-4]))
    metadata = {
        "schema_version": 1, "model": "zymatic", "firmware_version": version.partition("-")[0], "build_profile": PROFILE,
        "oem_application_sha256": stock.APP_SHA256, "binary_sha256": sha(image),
        "application_bytes": len(image), "uid": uid, "hub_host": hub_host, "hub_port": port,
        "offline_program": recipes.unpack_program(fields["provision_recipe"]),
        "extension_start": START, "extension_sha256": record["extension_sha256"],
        "compiler": record["compiler"], "first_possible_bootloader": LIMIT, "minimum_boot_start": LIMIT,
        "sram_reserved_bytes": symbols["__extension_bss_end"] - 0x800000 - BSS,
        "config": {name: {"address": symbols[name], "bytes": size} for name, size in FIELDS.items()},
        "patches": [{"name": name, "address": pc, "bytes": len(before)}
                    for name, pc, before, _ in patches],
    }
    return bytes(image), metadata


def recognize_private_application(flash, *, version=VERSION):
    """Identify only a verified composition. Private key material is never returned."""
    code, record = template(version)
    length = START + len(code)
    if len(flash) < length:
        return None
    symbols = record["symbols"]
    application = bytes(flash[:length])
    values = {name: application[symbols[name]:symbols[name]+size] for name, size in FIELDS.items()}
    try:
        uid = values["provision_uid"][:-1].decode("ascii")
        host = values["provision_host"].split(b"\0", 1)[0].decode("ascii")
        _, port = hub_origin(host)
        recipe = recipes.unpack_program(values["provision_recipe"])
    except ValueError:
        return None
    recovered = bytearray(application[:stock.APP_SIZE])
    legacy_menu = has_oem_recipe_label(application)
    for name, pc, before, after in patch_list(symbols, port, version, legacy_menu=legacy_menu):
        if recovered[pc:pc+len(after)] != after:
            return None
        recovered[pc:pc+len(before)] = before
    if not stock.is_original_app(recovered):
        return None
    try:
        expected, metadata = build_application(bytes(recovered), values["provision_key"], uid, host, recipe,
                                               version=version, legacy_menu=legacy_menu)
    except ValueError:
        return None
    if expected != application:
        return None
    return metadata

