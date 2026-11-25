"""Read the classic ESP32 bootloader, partition table and next boot selection."""

import binascii
import hashlib
from pathlib import Path
import struct
import sys
from typing import NamedTuple

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "tools"))
from firmware_analysis.esp_image import parse_esp32

SECTOR = 0x1000
TABLE = 0x8000
ERASED = b"\xff"


class Partition(NamedTuple):
    name: str
    kind: int
    subtype: int
    offset: int
    size: int

    @property
    def end(self):
        return self.offset + self.size


def _verify_bootloader(flash):
    # An OEM application on an erased or damaged boot region is not a stock backup.
    region = flash[SECTOR:TABLE]
    if len(region) != TABLE - SECTOR or region[0] != 0xe9 or not 1 <= region[1] <= 16:
        raise ValueError("Missing classic ESP32 bootloader at 0x1000")
    end = 24
    for _ in range(region[1]):
        if end + 8 > len(region):
            raise ValueError("Truncated ESP32 bootloader")
        end += 8 + struct.unpack_from("<I", region, end + 4)[0]
    end = (end // 16 + 1) * 16 + 32 * region[23]
    if end > len(region):
        raise ValueError("ESP32 bootloader crosses the partition table")
    parse_esp32(region[:end], bootloader=True)
    return SECTOR + end


def partitions(flash):
    table = flash[TABLE:TABLE + 0xc00]
    if len(table) != 0xc00:
        raise ValueError("Missing ESP32 partition table at 0x8000")
    result = []
    for pos in range(0, len(table), 32):
        row = table[pos:pos + 32]
        if row[:2] == b"\xeb\xeb":
            if row[2:16] != ERASED * 14 or row[16:] != hashlib.md5(table[:pos]).digest():
                raise ValueError("ESP32 partition table checksum mismatch")
            pos += 32
            break
        if row == ERASED * 32:
            break
        magic, kind, subtype, offset, size, label, flags = struct.unpack("<HBBII16sI", row)
        if magic != 0x50aa or flags != 0:
            raise ValueError("Unsupported ESP32 partition record or flags")
        name, _, padding = label.partition(b"\x00")
        if any(padding) or not name or not name.isascii() or not name.decode("ascii").isprintable():
            raise ValueError("Invalid ESP32 partition label")
        part = Partition(name.decode("ascii"), kind, subtype, offset, size)
        alignment = 0x10000 if kind == 0 else SECTOR
        if (not size or size % SECTOR or offset % alignment or offset < TABLE + SECTOR
                or part.end > len(flash)):
            raise ValueError("ESP32 partition outside flash or misaligned")
        if any(p.name == part.name or (p.offset < part.end and offset < p.end)
               or (kind == 0 or (kind, subtype) == (1, 0)) and (p.kind, p.subtype) == (kind, subtype)
               for p in result):
            raise ValueError("Overlapping or duplicate ESP32 partitions")
        result.append(part)
    else:
        raise ValueError("Unterminated ESP32 partition table")
    if table[pos:] != ERASED * (len(table) - pos) or not result:
        raise ValueError("Invalid trailing ESP32 partition data")
    return result


def selected_app_slice(flash):
    """Return the app selected on normal boot with the ESP-IDF factory/OTA rules.

    Decline ambiguous/invalid OTA metadata instead of guessing a fallback app.
    See Espressif ESP-IDF v3.2.5 bootloader_utility_get_selected_boot_partition.
    """
    _verify_bootloader(flash)
    table = partitions(flash)
    factory = next((p for p in table if (p.kind, p.subtype) == (0, 0)), None)
    slots = sorted((p for p in table if p.kind == 0 and 0x10 <= p.subtype < 0x20),
                   key=lambda p: p.subtype)
    if any(p.subtype != 0x10 + i for i, p in enumerate(slots)):
        raise ValueError("Noncontiguous ESP32 OTA app slots")
    ota = next((p for p in table if (p.kind, p.subtype) == (1, 0)), None)
    if ota is None and factory is None:
        raise ValueError("Missing factory app and OTA selection partition")
    sequences = []
    if ota:
        if ota.size < 2 * SECTOR:
            raise ValueError("Truncated ESP32 OTA selection partition")
        for offset in (ota.offset, ota.offset + SECTOR):
            record = flash[offset:offset + 32]
            if record == ERASED * 32:
                continue
            seq, state, crc = struct.unpack("<I20xII", record)
            if (not 0 < seq < 0x80000000 or crc != binascii.crc32(record[:4], 0xffffffff)
                    or state not in (2, 0xffffffff)):
                raise ValueError("Corrupt or unconfirmed ESP32 OTA boot selection")
            sequences.append(seq)
    selected = factory or (slots[0] if slots else None)
    if slots and sequences:
        selected = slots[(max(sequences) - 1) % len(slots)]
    if selected is None:
        raise ValueError("Missing a bootable ESP32 application partition")
    return slice(selected.offset, selected.end)


def verify_app_slots(flash, matches):
    """Require every non-erased app partition in a stock backup to match the family."""
    view = memoryview(flash)
    for part in partitions(flash):
        if part.kind == 0 and flash.count(0xff, part.offset, part.end) != part.size:
            if not matches(view[part.offset:part.end]):
                raise ValueError("Unrecognized application in stock flash partition " + part.name)


def qualify_install_layout(flash, *, preserve_z_eeprom=False):
    """Check that a reference package can retain NVS and all opaque data regions."""
    if len(flash) < 0x400000:
        raise ValueError("ESP32 reference layout requires at least 4 MiB of flash")
    table = partitions(flash)
    nvs = [p for p in table if (p.kind, p.subtype) == (1, 2)]
    if len(nvs) != 1 or (nvs[0].name, nvs[0].offset, nvs[0].size) != ("nvs", 0x9000, 0x5000):
        raise ValueError("Stock NVS does not match the preserved reference NVS region")
    boot_end = _verify_bootloader(flash)
    # Reject writes where an undeclared hole, including boot-sector padding,
    # contains opaque data. Only application and known SPIFFS data may be replaced.
    if flash[boot_end:TABLE] != ERASED * (TABLE - boot_end):
        raise ValueError("Unidentified data after stock ESP32 bootloader")
    if flash[TABLE + 0xc00:TABLE + SECTOR] != ERASED * 0x400:
        raise ValueError("Unidentified data in ESP32 partition-table sector")
    cursor = 0x10000
    for p in sorted(table, key=lambda item: item.offset):
        if (p.kind, p.subtype) == (1, 0) and (p.offset, p.size) != (0xe000, 0x2000):
            raise ValueError("Stock OTA selection differs from the preserved reference region")
        if p.end <= 0x9000 or p.offset >= 0x400000:
            continue
        if p == nvs[0] or (p.kind, p.subtype) == (1, 0):
            continue
        z_eeprom = preserve_z_eeprom and p == Partition("eeprom", 1, 0x99, 0x290000, SECTOR)
        if p.kind != 0 and (p.kind, p.subtype) != (1, 0x82) and not z_eeprom:
            raise ValueError("Reference layout would displace stock data partition " + p.name)
        if p.offset < 0x10000:
            raise ValueError("Stock partition overlaps reserved reference metadata")
        if p.offset > cursor and flash[cursor:p.offset] != ERASED * (p.offset - cursor):
            raise ValueError("Unidentified data between stock partitions")
        cursor = min(p.end, 0x400000)
    if flash[cursor:0x400000] != ERASED * (0x400000 - cursor):
        raise ValueError("Unidentified data in reference filesystem region")
