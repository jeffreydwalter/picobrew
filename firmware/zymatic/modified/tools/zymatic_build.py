"""Compose a private Zymatic application from the verified OEM image and reviewed AVR extension."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import struct
import zipfile
import zlib

import offline_program as recipes
import usb_stock as stock
import oem_tables as tables

ROOT = Path(__file__).resolve().parents[1]
TEMPLATE = ROOT / "extension"
START = tables.HIGH_START
LIMIT = 0x1fc00
BSS = 0x2c8a
VERSION = "1.0.0"
PROFILE = "extended"
INITIAL_RELEASE = "1.0.0-initial"
ARCHIVED_MANIFESTS = {
    INITIAL_RELEASE: "84dae779c27ad23dbed576352cda79ab92c0e29ccd37137197ac2f6a85d1942e",
    "1.3.3": "c279cbc302297af4238f28de1dd61174383c79b4a4f59910885ce7c79a2dc55e",
    "1.3.2": "6eda0d88047c6171f2e138386180add9cf0be201f2bdac7750cff0a26d573173",
    "1.3.0": "cc70f6d3ea0d137def6b669533b8b12fba25110b33220be4f0dbb3798f55f9e1",
    "1.3.1": "e8187c77d7bbca5934130d3028c20de1448c1f9671460426d982473fb5da3fb3",
}
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

# First row in the six-entry 1.1.14 Home menu. Keep subsequent flash addresses fixed.
RECIPE_MENU_ADDRESS = 0x00ce
OEM_RECIPE_LABEL = b"Brew a Recipe\0"
RECIPE_MENU_PATCH = ("brew-recipe-label", RECIPE_MENU_ADDRESS, OEM_RECIPE_LABEL,
                     b"Brew Recipe\0".ljust(len(OEM_RECIPE_LABEL), b"\0"))


def has_oem_recipe_label(image):
    return image.startswith(OEM_RECIPE_LABEL, RECIPE_MENU_ADDRESS)


def legacy_sum(image):
    return 2 * sum(image[i] | image[i+1] << 8 for i in range(0, 0x10000, 4)) & 0xffff


def sha(data):
    return hashlib.sha256(data).hexdigest()


def sources_sha():
    digest = hashlib.sha256()
    paths = [*(ROOT / "src").iterdir(), *ROOT.glob("zymatic_api*"), *(ROOT / "site").glob("*.h"), *ROOT.glob("zymatic_*.h")]
    for path in sorted(set(paths)):
        if path.suffix in (".c", ".h", ".S", ".ld"):
            digest.update(path.relative_to(ROOT).as_posix().encode("ascii") + b"\0" + path.read_bytes() + b"\0")
    return digest.hexdigest()


def template_directory(version):
    if version == VERSION:
        return TEMPLATE
    if version in ARCHIVED_MANIFESTS:
        return TEMPLATE / version
    raise ValueError("Unknown Zymatic extension version")


def seek_layout(version):
    return version in (VERSION, INITIAL_RELEASE, "1.3.3")


def template(version=VERSION):
    directory = template_directory(version)
    metadata = (directory / "extension.json").read_bytes()
    record = json.loads(metadata)
    if version in ARCHIVED_MANIFESTS:
        if sha(metadata) != ARCHIVED_MANIFESTS[version]:
            raise ValueError(f"Zymatic {version} extension metadata differs from the released manifest")
    elif record["source_sha256"] != sources_sha():
        raise ValueError("The AVR extension differs from its recorded sources; rebuild the template")
    code = (directory / "extension.bin").read_bytes()
    if (record["extension_sha256"] != sha(code)
            or len(code) != record["extension_bytes"] or not 0 < len(code) <= LIMIT - START):
        raise ValueError("The AVR extension differs from its recorded sources or binary; rebuild the template")
    low = (directory / "low.bin").read_bytes()
    if len(low) != record["low_bytes"] or sha(low) != record["low_sha256"] or not (
            0 < len(low) <= tables.POOL_END - (tables.SEEK_END if seek_layout(version) else tables.PACKED_END)):
        raise ValueError("Invalid low-flash extension")
    crc_code = (directory / "crc-hole.bin").read_bytes()
    if len(crc_code) != record["crc_hole_bytes"] or sha(crc_code) != record["crc_hole_sha256"] or not 0 < len(crc_code) <= 254:
        raise ValueError("Invalid CRC-table region")
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
    if not (START <= address < LIMIT or tables.SEEK_END <= address < tables.POOL_END or tables.CRC_TABLE < address < tables.CRC_TABLE+256) or address % 2:
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
        ("startup-initializer", 0x2316, bytes.fromhex("1ae0a0e0"), b"\x0c\x94"+call(symbols["zymatic_crt_data"])[2:]),
        ("sensor-crc", 0x194da, bytes.fromhex("e4916150"), call(symbols["zymatic_sensor_crc"])),
        ("pgm-copy", 0x2662, bytes.fromhex("05900d92"), call(symbols["zymatic_pgm_copy"])),
        ("pgm-wifi-print", 0x11cd8, bytes.fromhex("84918823"), call(symbols["zymatic_pgm_wifi_print"])),
        ("pgm-serial-print", 0x1a870, bytes.fromhex("64916623"), call(symbols["zymatic_pgm_serial_print"])),
        ("pgm-bitmap", 0x15954, bytes.fromhex("e4912e2f"), call(symbols["zymatic_pgm_bitmap"])),
        ("dialog-button", 0x807c, bytes.fromhex("0e941689"), call(symbols["zymatic_dialog_button"])),
        ("selected-row", 0x832c, bytes.fromhex("0e94e788"), call(symbols["zymatic_menu_selection"])),
        ("brew-session", 0xffea, bytes.fromhex("0e947f7b"), call(symbols["zymatic_run_program"])),
        ("about-pairing", 0x855a, bytes.fromhex("0e947d22"), call(symbols["zymatic_about_wait"])),
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
    if seek_layout(version):
        patches.append(("home-care", 0x105da, bytes.fromhex("0e94f840"), call(symbols["zymatic_home_menu"])))
    for pc in SERIAL_READS:
        patches.append(("stock-serial", pc, bytes.fromhex("0e94a121"),
                        call(symbols["zymatic_read_stock_serial"])))
    for name, original, locations in (
            ("wifi-host", 0x14d, [0x2eaa, 0x4d08, 0x9a82]),
            ("ethernet-host", 0x886, [0xa734, 0x1032c, 0x1037c])):
        for i, pc in enumerate(locations):
            pointer = authority if i == 0 else host
            patches.append((name, pc, load_pointer(22, original), load_pointer(22, pointer)))
    for label, register, locations in (
        ("wifi-port", 18, [0x42ae, 0x603c, 0x61a0, 0x6290, 0x63b6, 0x65e0,
                           0x671a, 0x6ae6, 0x6fc2, 0x87ee, 0x8e68, 0x901c]),
        ("ethernet-port", 20, [0xba10, 0xbf18, 0xc02a, 0xc228, 0xcae8, 0xcfbc,
                               0xd23a, 0xd3c8, 0xd836, 0xda36, 0xdd82, 0xdef6])):
        for pc in locations:
            patches.append((label, pc, load_pointer(register, 80), load_pointer(register, port)))
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
    tables.verify_library(original)
    image = bytearray(original)
    directory = template_directory(version)
    code, record = template(version)
    symbols = record["symbols"]
    _, port = hub_origin(hub_host)
    patches = patch_list(symbols, port, version, legacy_menu=legacy_menu)
    for name, pc, before, after in patches:
        if image[pc:pc+len(before)] != before:
            raise ValueError(f"Original 1.1.14 bytes differ at {name}")
        image[pc:pc+len(after)] = after
    low = (directory / "low.bin").read_bytes()
    if image[tables.CRC_TABLE:tables.CRC_TABLE+256] != tables.sensor_crc_table():
        raise ValueError("Unexpected 1.1.14 sensor CRC table")
    crc_code = (directory / "crc-hole.bin").read_bytes()
    image[tables.CRC_TABLE:tables.CRC_TABLE+256] = (b"\xff" + crc_code).ljust(256, b"\xff")
    packer = tables.pack_seek_pool if seek_layout(version) else tables.pack_pool
    packed = packer(bytes(image[tables.POOL:tables.POOL_END]))
    image[tables.POOL:tables.POOL_END] = (packed + low).ljust(tables.POOL_END - tables.POOL, b"\xff")
    initializer = tables.pack_crt(bytes(image[tables.CRT:tables.CRT + tables.CRT_SIZE]))
    image[tables.CRT:] = initializer.ljust(START - tables.CRT, b"\xff") + code
    if len(image) != START + len(code) or len(image) > LIMIT:
        raise ValueError("Composed application exceeds available flash")
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
        "compiler": record["compiler"], "first_possible_bootloader": 0x1e000,
        "minimum_boot_start": next(bound for bound in (0x1e000, 0x1f000, 0x1f800, LIMIT) if len(image) <= bound),
        "low_extension_start": tables.SEEK_END if seek_layout(version) else tables.PACKED_END, "low_extension_sha256": record["low_sha256"],
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
    try:
        if seek_layout(version):
            raw_pool = tables.unpack_seek_pool(bytes(application[tables.POOL:tables.SEEK_END]))
        else:
            raw_pool = tables.unpack_pool(bytes(application[tables.POOL:tables.PACKED_END]))
        initializer = tables.unpack_crt(application[tables.CRT:START])
    except ValueError:
        return None
    recovered = bytearray(application[:tables.CRT] + initializer)
    recovered[tables.POOL:tables.POOL_END] = raw_pool
    recovered[tables.CRC_TABLE:tables.CRC_TABLE+256] = tables.sensor_crc_table()
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


def intel_hex(image):
    def row(address, kind, payload):
        fields = bytes([len(payload)]) + struct.pack(">H", address) + bytes([kind]) + payload
        return ":" + (fields + bytes([-sum(fields) & 255])).hex().upper() + "\n"
    records = []
    for offset in range(0, len(image), 16):
        if offset and not offset & 0xffff:
            records.append(row(0, 4, struct.pack(">H", offset >> 16)))
        records.append(row(offset & 0xffff, 0, image[offset:offset+16]))
    return ("".join(records) + row(0, 1, b"")).encode("ascii")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--oem-file", type=Path, required=True)
    parser.add_argument("--key-file", type=Path, required=True)
    parser.add_argument("--uid", required=True)
    parser.add_argument("--hub-host", default="picobrew.com")
    parser.add_argument("--offline-program", type=Path, help="Optional native Zymatic program JSON")
    parser.add_argument("--out-dir", required=True, type=Path)
    parser.add_argument("--profile", choices=("compact", "extended"), default="compact")
    args = parser.parse_args()
    from usb_layout import release_for
    release = release_for(args.profile)
    try:
        original = stock.decode_application(stock.unpack_source(stock.private_files.read_regular(
            args.oem_file, stock.MAX_ZIP)))
        text = stock.private_files.read_regular(args.key_file, 256, private=True).strip()
        if not re.fullmatch(rb"[a-fA-F0-9]{64}", text):
            raise ValueError("The private key file must contain exactly 64 hexadecimal digits")
        recipe = recipes.read_program(args.offline_program)
        image, report = release.build_application(original, bytes.fromhex(text.decode("ascii")), args.uid, args.hub_host, recipe)
        target = args.out_dir.absolute()
        stock.private_files.ensure_private_directory(target, parents=True, exist_ok=True)
        stock.private_files.assert_private(target, directory=True)
        outputs = {
            f"zymatic-{VERSION}.bin": image, f"zymatic-{VERSION}.hex": intel_hex(image),
            "manifest.json": (json.dumps(report, indent=2) + "\n").encode("utf-8"),
        }
        for name, contents in outputs.items():
            try:
                saved = stock.private_files.read_regular(target / name, len(contents), private=True)
            except FileNotFoundError:
                continue
            if contents != saved:
                raise ValueError(f"Choose an empty output directory; the existing {name} differs")
        for name, contents in outputs.items():
            stock.save_private(target / name, contents)
        print(f"Built private Zymatic {VERSION} {release.PROFILE} application in {target} ({len(image)} bytes)")
    except (OSError, ValueError, zipfile.BadZipFile) as exc:
        parser.exit(1, f"{exc}\n")


if __name__ == "__main__":
    main()
