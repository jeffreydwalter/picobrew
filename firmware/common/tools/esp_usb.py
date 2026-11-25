#!/usr/bin/env python3
"""Back up, convert or restore one identified PicoBrew ESP service port."""

import argparse
from contextlib import contextmanager
from dataclasses import asdict, dataclass
import hashlib
import io
import json
import os
from pathlib import Path
import re
import stat
import sys
import uuid
import zipfile

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools"))
sys.path.insert(0, str(ROOT / "firmware/z-series/modified/tools"))
sys.path.insert(0, str(ROOT / 'firmware/picoferm/modified/tools'))
import usb_ab
import private_files
import esp_provisioning
import esp_stock_partitions
import usb_profile as z_profile

MIB = 1024 * 1024
SECTOR = 4096
Z_STACKS = ("z1", "z2", "z3", "z4")
MODELS = ("picoferm", "picostill", "z-series", *Z_STACKS)
ESP32_LAYOUT = {"bootloader.bin": (0x1000, 0x7000), "partitions.bin": (0x8000, SECTOR),
                "application.bin": (0x10000, 0x1f0000), "spiffs.bin": (0x200000, 0x200000)}
STILL_LAYOUT = {**ESP32_LAYOUT, "otadata.bin": (0xe000, 0x2000), "recovery.bin": (0x10000, 0xf0000),
                "application.bin": (0x100000, 0xf0000)}
Z_LAYOUT = {**ESP32_LAYOUT, "otadata.bin": (0xe000, 0x2000), "recovery.bin": (0x10000, 0xe0000),
            "application.bin": (0xf0000, 0x1a1000), "spiffs.bin": (0x291000, 0x16f000)}
LEGACY_STILL_TABLE_SHA256 = "cc5b9d52da361e51fdf767a6adf777909c44d0546a1f80d0f3bae3aa154a1ab9"
FERM_LAYOUT = {"application.bin": (0, MIB), "spiffs.bin": (MIB, 0xfb000)}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def family(model):
    if model not in MODELS:
        raise ValueError("Unsupported ESP appliance model")
    return "z-series" if model in Z_STACKS else model


@dataclass(frozen=True)
class Identity:
    chip: str
    mac: str
    jedec: int
    size: int

    def check(self, model):
        if self.chip != ("ESP8266" if model == "picoferm" else "ESP32"):
            raise ValueError("The ROM chip does not match the requested model")
        if not re.fullmatch(r"[0-9a-f]{12}", self.mac) or int(self.mac[:2], 16) & 1 or int(self.mac, 16) == 0:
            raise ValueError("Invalid hardware MAC")
        if self.size not in (MIB, 2*MIB, 4*MIB, 8*MIB, 16*MIB) or not 0 < self.jedec < 0xffffff:
            raise ValueError("Unrecognized physical flash geometry")


def recognized_original(flash, model):
    prefix = f"firmware/{model}/oem/upstream/{model}_"
    manifest = json.loads((ROOT / "firmware/upstream-manifest.json").read_bytes())
    originals = [item for item in manifest["files"] if item["destination_path"].startswith(prefix)]

    def match(app):
        return next((item for item in originals if item["size"] <= len(app)
                     and sha(app[:item["size"]]) == item["sha256"]), None)

    app = memoryview(flash)
    if model == "picostill":
        app = app[esp_stock_partitions.selected_app_slice(flash)]
        esp_stock_partitions.verify_app_slots(flash, match)
    original = match(app)
    if original is None:
        raise ValueError("The active application does not match a preserved OEM image for this model")
    return original["destination_path"][len(prefix):-4].replace("_", "."), original["size"]


def stock_version(flash, model):
    if family(model) == "z-series":
        return z_profile.qualify_stock(flash, model)
    return recognized_original(flash, model)[0]


def legacy_applications(model):
    # Only the identity of a retired application may authorize USB migration.
    path = ROOT / "firmware/common/dependencies/legacy-usb-applications.json"
    return json.loads(path.read_bytes())[model]


def reference(model):
    release_index = json.loads((ROOT / "firmware/releases.json").read_bytes())["releases"]
    entries = [r for r in release_index if r["model"] == model and r["kind"] == "reference"
               and re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", r["firmware_version"])]
    if not entries:
        raise ValueError("No reference package is published for this model")
    release = max(entries, key=lambda r: tuple(map(int, r["firmware_version"].split("."))))
    version = release["firmware_version"]
    path = ROOT / f"firmware/{model}/modified/releases/{model}/{version}.zip"
    archive = private_files.read_regular(path, 32*MIB)
    if sha(archive) != release["archive_sha256"]:
        raise ValueError("The USB reference archive differs from the release index")
    layout = FERM_LAYOUT if model == "picoferm" else ESP32_LAYOUT
    components = {}
    with zipfile.ZipFile(io.BytesIO(archive)) as package:
        manifest = json.loads(package.read("manifest.json"))
        if manifest.get("schema_version") != 1 or manifest.get("model") != model:
            raise ValueError("Invalid USB package model or schema")
        if manifest.get("ota_protocol"):
            if (model not in ("picostill","z-series") or manifest["ota_protocol"] != "esp32-factory-v1"
                    or manifest.get("ota_image") != "application.bin"):
                raise ValueError("Unsupported USB reference update protocol")
            layout = Z_LAYOUT if model == "z-series" else STILL_LAYOUT
        for item in manifest["components"]:
            name = item["file"]
            if name in components or layout.get(name) != (item["offset"], item["maximum_bytes"]):
                raise ValueError("Invalid USB package component address")
            data = package.read(name)
            if not 0 < len(data) == item["bytes"] <= item["maximum_bytes"] or sha(data) != item["sha256"]:
                raise ValueError("Invalid USB package component")
            components[name] = data
    if set(components) != set(layout) or sha(components["application.bin"]) != release["binary_sha256"]:
        raise ValueError("Invalid USB package application or component list")
    return version, components


def picoferm_ab_reference():
    """Select only a reviewed A/B archive pinned in the installed release index."""
    entries = json.loads((ROOT / "firmware/releases.json").read_bytes())["releases"]
    candidates = [r for r in entries if r.get("model") == "picoferm" and r.get("kind") == "picoferm-ab"]
    if not candidates:
        return None

    def version_key(entry):
        version = entry["firmware_version"]
        if not re.fullmatch(r"[0-9]{1,5}(\.[0-9]{1,5}){2}", version):
            raise ValueError("Invalid PicoFerm A/B release version")
        return tuple(map(int, version.split(".")))

    release = max(candidates, key=version_key)
    version = release["firmware_version"]
    path = ROOT / f"firmware/picoferm/modified/releases/picoferm/{version}.zip"
    archive = private_files.read_regular(path, 2*MIB)
    if sha(archive) != release["archive_sha256"]:
        raise ValueError("PicoFerm A/B archive differs from the release index")
    parts = usb_ab.parse(archive, version)
    if (release.get("binary_sha256") != sha(parts["bank-0.bin"])
            or release.get("binary_bytes") != len(parts["bank-0.bin"])
            or release.get("ota_protocol") != "esp8266-ab-v1"):
        raise ValueError("PicoFerm A/B index disagrees with bank zero or its protocol")
    return version, parts


def install_target(flash, model, *, api_key=None):
    if model in Z_STACKS:
        raise ValueError("This physical Z model has no independently mapped installation package")
    ab = picoferm_ab_reference() if model == "picoferm" else None
    if ab:
        version, parts = ab
        _, size = recognized_original(flash, model)
        return version, usb_ab.target(flash, size, parts, api_key)
    version, parts = reference(model)
    layout = FERM_LAYOUT if model == "picoferm" else ESP32_LAYOUT
    if model == "picoferm":
        if len(flash) != 2*MIB or flash[:4] != parts["application.bin"][:4]:
            raise ValueError("The live PicoFerm boot parameters or flash size differ from the reference package")
        # ESP8266 has no readable partition table. The OEM app proves its own
        # extent, not the purpose of other populated bytes in the candidate ranges.
        _, original_size = recognized_original(flash, model)
        expansion = flash[original_size:len(parts["application.bin"])]
        fs = parts["spiffs.bin"]
        old_fs = flash[MIB:MIB + len(fs)]
        if expansion.strip(b"\xff") or old_fs != fs and old_fs.strip(b"\xff"):
            raise ValueError("Unidentified PicoFerm data overlaps the proposed application or filesystem")
    else:
        if model == "z-series":
            z_profile.qualify_install_layout(flash, model)
        else:
            esp_stock_partitions.qualify_install_layout(flash)
        # Reuse the physical bootloader, which carries the unit's flash settings.
        # The stock loader has passed checksum/digest verification. A different
        # loader is accepted only if it already runs this exact factory layout.
        pinned = parts.pop("bootloader.bin")
        live = flash[0x1000:0x1000 + len(pinned)]
        end = -32 if pinned[23] else len(pinned)
        same_loader = live[:2] == pinned[:2] and live[4:end] == pinned[4:end]
        table = parts["partitions.bin"]
        existing_layout = flash[0x8000:0x8000 + len(table)] == table
        if live[2] != pinned[2]:
            raise ValueError("The stock ESP32 bootloader flash mode differs from the reference")
        if not same_loader and not existing_layout:
            raise ValueError("The stock bootloader has no verified match for this factory layout")
    protected_z = model == "z-series" and "recovery.bin" in parts
    if protected_z:
        slots = esp_stock_partitions.partitions(flash)
        eeprom = esp_stock_partitions.Partition("eeprom", 1, 0x99, 0x290000, SECTOR)
        if any(p != eeprom and p.kind != 0 and (p.kind,p.subtype) not in ((1,2),(1,0),(1,0x82)) for p in slots):
            raise ValueError("The Z partition table contains additional data partitions")
        filesystems = [p for p in slots if (p.kind,p.subtype) == (1,0x82)]
        if len(filesystems)>1: raise ValueError("Multiple Z filesystems require independent recovery")
        source = flash[filesystems[0].offset:filesystems[0].end] if filesystems else b""
        # Only the pinned SDK's named 4 KiB sector is recognized. Full stock bytes stay in the private board backup.
        seed = flash[eeprom.offset:eeprom.offset+120] if eeprom in slots else None
        parts["spiffs.bin"] = esp_provisioning.migrate_z_files(source, parts["spiffs.bin"], api_key, paired=False, board_seed=seed)
    elif api_key is not None:
        profile = "esp8266-2m" if model == "picoferm" else "esp32-4m"
        parts["spiffs.bin"] = esp_provisioning.stage(parts["spiffs.bin"], profile, api_key)
    if "recovery.bin" in parts:
        layout = Z_LAYOUT if protected_z else STILL_LAYOUT
        if protected_z:
            for name,role in (("application.bin","OTA1"),("recovery.bin","REC1")):
                marker = f"PicoBrew {role}|z-series|{version}".encode().ljust(64,b"\0")
                if parts[name][0x120:0x160] != marker: raise ValueError("Unrecognized Z image identity")
        elif model != "picostill" or parts["recovery.bin"] != parts["application.bin"]:
            raise ValueError("Invalid PicoStill factory recovery")
        # The USB boot-selection record is writable only inside identified stock
        # otadata, or in erased bytes where the original omitted the partition.
        slots = esp_stock_partitions.partitions(flash)
        if not any((p.kind, p.subtype) == (1, 0) for p in slots) and flash[0xe000:0x10000].strip(b"\xff"):
            raise ValueError("Unidentified data overlaps the proposed OTA selection")
    desired = bytearray(flash)
    for name, data in parts.items():
        start, _ = layout[name]
        desired[start:start + len(data)] = data
    if model != "picoferm" and esp_stock_partitions.selected_app_slice(desired).start != layout["application.bin"][0]:
        raise ValueError("Installed partition metadata would not select the reference application")
    return version, bytes(desired)


def installed_factory_matches(current, original, key, model):
    layout = Z_LAYOUT if model == "z-series" else STILL_LAYOUT
    _, parts = reference(model)
    if "recovery.bin" not in parts or current[0x1000:0x8000] != original[0x1000:0x8000]:
        return False
    for name in ("partitions.bin", "recovery.bin", "application.bin"):
        offset = layout[name][0]
        if current[offset:offset+len(parts[name])] != parts[name]:
            return False
    start,size = layout["spiffs.bin"]
    return (esp_stock_partitions.selected_app_slice(current).start == layout["application.bin"][0]
            and esp_provisioning.matches_stored_esp32_key(current[start:start+size], key))


def migrate_factory_layout(current, original, key, model):
    """Preserve the running filesystem and NVS when introducing protected recovery."""
    layout = Z_LAYOUT if model == "z-series" else STILL_LAYOUT
    version, parts = reference(model)
    if ("recovery.bin" not in parts or sha(current[0x8000:0x8c00]) != LEGACY_STILL_TABLE_SHA256
            or esp_stock_partitions.selected_app_slice(current) != slice(0x10000, 0x200000)):
        raise ValueError("The installed ESP32 layout was not recognized")
    if not esp_provisioning.matches_stored_esp32_key(current[0x200000:0x400000], key):
        raise ValueError("The connected device key does not match the Hub's saved pairing")
    old = next((r for r in legacy_applications(model) if 0 < r["binary_bytes"] <= 0x1f0000
                and sha(current[0x10000:0x10000+r["binary_bytes"]])==r["binary_sha256"]), None)
    if not old:
        raise ValueError("The installed application did not match a published binary")
    # Legacy USB setup replaced exactly one application, one table and SPIFFS.
    # Refuse to repurpose any unexplained data that appeared in the app tail.
    tail = 0x10000 + old["binary_bytes"]
    if current[:0x8000] != original[:0x8000] or current[tail:0x200000] != original[tail:0x200000]:
        raise ValueError("The bootloader or unused application region differs from the saved original")
    esp_stock_partitions.qualify_install_layout(current)
    target = bytearray(current)
    if model == "z-series":
        start,size = layout["spiffs.bin"]
        target[start:start+size] = esp_provisioning.migrate_z_files(current[0x200000:0x400000],parts["spiffs.bin"],key,paired=True)
    for name in ("otadata.bin", "recovery.bin", "application.bin", "partitions.bin"):
        start = layout[name][0]
        target[start:start+len(parts[name])] = parts[name]
    if esp_stock_partitions.selected_app_slice(target).start != layout["application.bin"][0]:
        raise ValueError("The protected layout does not select the verified application")
    return version, bytes(target)


def preserve_before_migration(path, image):
    # The stock capture must never be overwritten by a modified capture.
    destination = path.with_name("before-repartition-"+sha(image)+".bin")
    try:
        saved = private_files.read_regular(destination,len(image),private=True)
    except FileNotFoundError:
        pending = destination.with_name(".migration-"+uuid.uuid4().hex)
        file = private_files.new_private_file(pending)
        try:
            file.write(image)
            private_files.publish(file,pending,destination)
        except BaseException:
            private_files.discard(file,pending)
            raise
        file.close()
        if os.name == "posix":
            fd = os.open(path.parent,os.O_RDONLY)
            try: os.fsync(fd)
            finally: os.close(fd)
        saved = private_files.read_regular(destination,len(image),private=True)
    if saved != image: raise ValueError("Pre-migration capture failed readback")


def backup_path(root, identity):
    if not root.is_absolute():
        raise ValueError("Pass an absolute Hub data directory")
    private_files.ensure_private_directory(root, parents=True, exist_ok=True)
    directory = root
    for name in ("stock-firmware", "esp", identity.mac):
        directory /= name
        private_files.ensure_private_directory(directory, exist_ok=True)
        private_files.assert_private(directory, directory=True)
    return directory / "original.zip"


def load_original(path, identity, model):
    data = private_files.read_regular(path, identity.size + 8192, private=True)
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        entries = archive.infolist()
        if (sorted(i.filename for i in entries) != ["flash.bin", "identity.json"]
                or any(i.compress_type != zipfile.ZIP_STORED for i in entries)
                or archive.getinfo("flash.bin").file_size != identity.size
                or archive.getinfo("identity.json").file_size > 4096):
            raise ValueError("Invalid full-flash backup archive")
        metadata = json.loads(archive.read("identity.json"))
        flash = archive.read("flash.bin")
    if metadata != backup_metadata(flash, identity, model):
        raise ValueError("Backup model, ROM identity, geometry or digest differs")
    return flash


def backup_metadata(flash, identity, model):
    return {"schema_version": 1, "model_family": family(model), "hardware": asdict(identity),
            "oem_version": stock_version(flash, model), "flash_sha256": sha(flash)}


def save_original(path, flash, identity, model):
    # All publication paths are exclusive. A second backup job cannot replace an original.
    tmp = path.parent / (".capture-" + uuid.uuid4().hex)
    file = private_files.new_private_file(tmp)
    try:
        with zipfile.ZipFile(file, "w", compression=zipfile.ZIP_STORED) as archive:
            archive.writestr("identity.json", json.dumps(backup_metadata(flash, identity, model)))
            archive.writestr("flash.bin", flash)
        private_files.publish(file, tmp, path)
    except BaseException:
        private_files.discard(file, tmp)
        raise
    file.close()
    if os.name == "posix":
        fd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)


def changed_ranges(current, desired, boot_sector):
    """Coalesce adjacent changed erase sectors; publish the boot entry last."""
    start = None
    for offset in range(0, len(current) + SECTOR, SECTOR):
        changed = offset != boot_sector and current[offset:offset+SECTOR] != desired[offset:offset+SECTOR]
        if changed and start is None:
            start = offset
        if not changed and start is not None:
            yield start, desired[start:offset]
            start = None
    boot = slice(boot_sector, boot_sector + SECTOR)
    if current[boot] != desired[boot]:
        yield boot_sector, desired[boot]


def operation(action, model, device, root, ask=input, *, api_key=None):
    if api_key is not None and action != "install":
        raise ValueError("Key provisioning is available only during initial installation")
    ident = device.identity
    ident.check(model)
    path = backup_path(root, ident)
    print(f"{model} · {ident.chip} · MAC {ident.mac} · {ident.size // MIB} MiB.")
    original = None
    try:
        original = load_original(path, ident, model)
    except FileNotFoundError:
        if action == "restore":
            raise ValueError("This unit has no private full-flash original. Public OEM files are application updates.")
    print("Reading the complete physical flash…", flush=True)
    current = device.read()
    if len(current) != ident.size:
        raise ValueError("Short physical flash read")
    if action in ("install", "backup"):
        try:
            version = stock_version(current, model)
        except ValueError:
            # A lost browser reply may follow a verified write. Confirm the
            # exact intended target against this MAC's original and stable key.
            # A used/changed filesystem must never be reset on a retry.
            if action != "install" or api_key is None or original is None:
                raise
            if device.read() != current:
                raise ValueError("The full-device reads disagree; no USB write was made") from None
            protected = model in ("picostill","z-series") and "recovery.bin" in reference(model)[1]
            if protected:
                already_installed = installed_factory_matches(current,original,api_key,model)
            else:
                version,wanted = install_target(original,model,api_key=api_key)
                already_installed = current == wanted
            if already_installed:
                device.verify_identity()
                print("The installed image and private key were verified. No further write is needed.")
                return
            if protected and sha(current[0x8000:0x8c00]) == LEGACY_STILL_TABLE_SHA256:
                version, target = migrate_factory_layout(current, original, api_key, model)
                if model == "z-series": preserve_before_migration(path,current)
                print(f"USB setup will install {model} {version} with wireless recovery and retain its settings and history.")
                return write_target(action, device, ident, current, target, ask)
            if model == "picoferm" and picoferm_ab_reference() is not None:
                target = usb_ab.migrate(current, original, wanted, api_key, legacy_applications(model))
                print(f"USB setup will retain the paired PicoFerm's settings and history for {version}.")
                return write_target(action, device, ident, current, target, ask)
            raise ValueError("The machine no longer matches the intended USB installation. Its contents were preserved.") from None
        print(f"Identified active OEM firmware {version}. Verifying with a second full read…", flush=True)
        if device.read() != current:
            raise ValueError("The full-device reads disagree; capture failed")
        if original is None:
            save_original(path, current, ident, model)
            original = load_original(path, ident, model)
            if original != current:
                raise ValueError("Private archive readback differs from the physical capture")
        if original != current:
            # An OEM update or settings change must not discard either physical capture.
            newer = path.with_name("stock-" + sha(current) + ".zip")
            try:
                captured = load_original(newer, ident, model)
            except FileNotFoundError:
                save_original(newer, current, ident, model)
                captured = load_original(newer, ident, model)
            if captured != current:
                raise ValueError("Current stock capture could not be verified")
            print(f"Also preserved the current stock contents in {newer}")
        print(f"Verified original for this device: {path}")
        if action == "backup":
            return
        version, target = install_target(current, model, api_key=api_key)
        print(f"USB setup will install {model} {version} and initialize the machine filesystem. "
              "This reference build still needs operating trials on physical hardware.")
    else:
        target = original
        if ask("Restore all original slots, settings and calibration, and remove the custom Hub interface? Type RESTORE: ") != "RESTORE":
            raise ValueError("Restore cancelled before writing")
    return write_target(action, device, ident, current, target, ask)


def write_target(action, device, ident, current, target, ask):
    if current == target:
        print("The complete flash already matches the requested contents.")
        return
    if ask(f"Type the device MAC {ident.mac} to confirm {action}: ").strip().lower() != ident.mac:
        raise ValueError("Device confirmation did not match; cancelled before writing")
    device.verify_identity()
    boot = 0 if ident.chip == "ESP8266" else 0x8000
    for offset, data in changed_ranges(current, target, boot):
        device.write(offset, data)
    if device.read() != target:
        raise ValueError("Full-flash verification failed. Keep the unit in download mode and run restore.")
    print("Full physical flash verified. Exit download mode and reset the idle appliance.")


class EspDevice:
    def __init__(self, stub, identity):
        self.stub = stub
        self.identity = identity

    def verify_identity(self):
        # flash_id() caches its first reply. Read the physical JEDEC ID and
        # eFuse MAC again after the user confirms and before the first erase.
        self.stub.cache["flash_id"] = None
        if bytes(self.stub.read_mac()).hex() != self.identity.mac or self.stub.flash_id() != self.identity.jedec:
            raise ValueError("The connected device changed; cancelled before writing")

    def read(self):
        percent_reported = 0

        def progress(done, total):
            nonlocal percent_reported
            percent = 10 * ((done * 10) // total)
            if percent > percent_reported:
                print(f"Flash read {percent}%", flush=True)
                percent_reported = percent
        try:
            return self.stub.read_flash(0, self.identity.size, progress)
        except RuntimeError:
            # Some SDK packet errors include unparsed bytes in their messages.
            raise ValueError("A complete verified flash read failed. Check the service connection.") from None

    def write(self, address, data):
        import esptool
        file = io.BytesIO(data)
        file.name = "verified flash sectors"
        args = argparse.Namespace(addr_filename=[(address, file)], chip=self.identity.chip.lower(),
                                  compress=True, no_compress=False, no_stub=False, force=False,
                                  encrypt=False, encrypt_files=None, ignore_flash_encryption_efuse_setting=False,
                                  erase_all=False, flash_size="keep", flash_freq="keep", flash_mode="keep")
        esptool.write_flash(self.stub, args)


@contextmanager
def open_device(port, model):
    try:
        import serial
        import esptool
        from esptool.cmds import detect_flash_size
        from esptool.util import flash_size_bytes
    except ImportError as exc:
        raise ValueError("Install firmware/common/requirements-usb.txt with this Python interpreter") from exc
    if esptool.__version__ != "4.11.0":
        raise ValueError("Install the pinned version from firmware/common/requirements-usb.txt")
    if os.name == "nt":
        if not re.fullmatch(r"COM[1-9][0-9]*", port, re.I):
            raise ValueError("Choose an explicit local COM port")
    elif not port.startswith("/dev/") or not stat.S_ISCHR(os.stat(port).st_mode):
        raise ValueError("Choose an explicit local serial device under /dev")
    session = serial.Serial(port=None, baudrate=115200, exclusive=True)
    session.dtr = session.rts = False
    session.port = port
    with session:
        rom = esptool.detect_chip(session, 115200, connect_mode="no_reset", connect_attempts=1)
        expected = "ESP8266" if model == "picoferm" else "ESP32"
        if rom.CHIP_NAME != expected or rom.secure_download_mode or rom.stub_is_disabled:
            raise ValueError("Unexpected chip or restricted ROM download mode")
        if rom.IS_STUB:
            raise ValueError("Re-enter the chip ROM download mode before starting a new operation")
        if expected == "ESP32" and (rom.get_secure_boot_enabled() or rom.get_flash_encryption_enabled()):
            raise ValueError("This device has protected boot or encrypted flash")
        mac = bytes(rom.read_mac()).hex()
        stub = rom.run_stub()
        # esptool's normal command can reopen a lost port. Retrying must repeat hardware identification.
        stub.WRITE_FLASH_ATTEMPTS = 1
        size = detect_flash_size(stub)
        if not size:
            raise ValueError("Unknown JEDEC flash capacity")
        ident = Identity(expected, mac, stub.flash_id(), flash_size_bytes(size))
        ident.check(model)
        stub.flash_set_parameters(ident.size)
        yield EspDevice(stub, ident)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("backup", "install", "restore"))
    parser.add_argument("--port", required=True)
    parser.add_argument("--model", choices=MODELS, required=True)
    parser.add_argument("--data", type=Path, required=True)
    args = parser.parse_args()
    try:
        with open_device(args.port, args.model) as device:
            operation(args.action, args.model, device, args.data)
    except (OSError, ValueError, EOFError, RuntimeError, zipfile.BadZipFile) as exc:
        parser.exit(1, f"USB operation stopped: {exc}\n")


if __name__ == "__main__":
    main()
