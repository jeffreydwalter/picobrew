"""Exercise complete ESP service operations on in-memory chips with real package bytes."""

from contextlib import redirect_stdout
from dataclasses import replace
import hashlib
import importlib.util
import io
import os
import struct
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import MagicMock, patch
import zipfile
import zlib

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools"))
import esp_usb as usb


class MemoryDevice:
    def __init__(self, chip, contents):
        self.contents = bytearray(contents)
        self.identity = usb.Identity(chip, "102030405060", 0x1540ef if chip == "ESP8266" else 0x1640ef,
                                     len(contents))
        self.reads = 0
        self.writes = []

    def verify_identity(self):
        pass

    def read(self):
        self.reads += 1
        return bytes(self.contents)

    def write(self, offset, data):
        if offset % usb.SECTOR or len(data) % usb.SECTOR or offset + len(data) > len(self.contents):
            raise AssertionError("physical write outside sector-aligned flash")
        self.writes.append(offset)
        self.contents[offset:offset+len(data)] = data


def single_factory_table():
    rows = [(1, 2, 0x9000, 0x5000, b"nvs"), (1, 0, 0xe000, 0x2000, b"otadata"),
            (0, 0, 0x10000, 0x1f0000, b"factory"), (1, 0x82, 0x200000, 0x200000, b"spiffs")]
    entries = b"".join(struct.pack("<HBBII16sI", 0x50aa, *row, 0) for row in rows)
    checksum = b"\xeb\xeb" + b"\xff" * 14 + hashlib.md5(entries).digest()
    table = (entries + checksum).ljust(0xc00, b"\xff")
    assert usb.sha(table) == usb.LEGACY_STILL_TABLE_SHA256
    return table


def single_factory_parts(model):
    _, parts = usb.reference(model)
    return {"bootloader.bin": parts["bootloader.bin"], "partitions.bin": single_factory_table(),
            "application.bin": parts["application.bin"], "spiffs.bin": usb.reference("picostill")[1]["spiffs.bin"]}


def device(model, version=None):
    if model == "picoferm":
        version = version or "0_2_7"
        chip = MemoryDevice("ESP8266", b"\xff" * (2*usb.MIB))
        origin = 0
        chip.contents[-12:] = b"saved-RF-cal"
    else:
        version = version or ("0_0_35" if model == "picostill" else "0_0_119")
        chip = MemoryDevice("ESP32", b"\xff" * (4*usb.MIB))
        _, parts = usb.reference(model)
        if model in ("picostill","z-series") and "recovery.bin" in parts:
            # The specimen starts with the historically supported single-factory
            # layout. Never import a new release partition table as a stock fact.
            parts = dict(parts)
            parts["partitions.bin"] = single_factory_table()
        for key in ("bootloader.bin", "partitions.bin"):
            offset, _ = usb.ESP32_LAYOUT[key]
            chip.contents[offset:offset+len(parts[key])] = parts[key]
        chip.contents[0x9030:0x9040] = b"WiFi calibration"
        origin = 0x10000
    filename = model.replace("-", "") + "_" + version + ".bin"
    stock = next((usb.ROOT / "firmware" / model / "oem").rglob(filename)).read_bytes()
    chip.contents[origin:origin+len(stock)] = stock
    return chip


class USBTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        # The Windows runner's temporary directory may inherit a shared ACL.
        # Exercise the same private Hub data directory creation used in service.
        self.data = Path(tmp.name).absolute() / "hub-data"
        output = io.StringIO()
        redirects = redirect_stdout(output)
        redirects.__enter__()
        self.addCleanup(redirects.__exit__, None, None, None)

    def run_action(self, action, model, target, replies=()):
        def ask(_):
            return next(answers)
        answers = iter(replies)
        usb.operation(action, model, target, self.data, ask=ask)

    def test_all_three_full_roundtrips_with_real_releases_and_oem(self):
        for model in ("picoferm", "picostill", "z-series"):
            with self.subTest(model=model):
                chip = device(model)
                self.data = self.data / model
                before = bytes(chip.contents)
                self.run_action("install", model, chip, [chip.identity.mac])
                self.assertGreaterEqual(chip.reads, 3)
                self.assertTrue(chip.writes)
                if model == "picoferm":
                    _, components = usb.picoferm_ab_reference()
                    boot = components["bootstrap.bin"]
                    self.assertEqual(chip.contents[:len(boot)], boot)
                    target = components["bank-0.bin"]
                    start = 0x3000
                else:
                    _, components = usb.reference(model)
                    target = components["application.bin"]
                    start = usb.esp_stock_partitions.selected_app_slice(chip.contents).start
                self.assertEqual(chip.contents[start:start+len(target)], target)
                preserved = slice(-0x5000, None) if model == "picoferm" else slice(0x9000, 0xe000 if "otadata.bin" in components else 0x10000)
                self.assertEqual(chip.contents[preserved], before[preserved])
                if "recovery.bin" in components:
                    self.assertEqual(start, 0xf0000 if model=="z-series" else 0x100000)
                    recovery = components["recovery.bin"]
                    self.assertEqual(chip.contents[0x10000:0x10000 + len(recovery)], recovery)
                    self.assertEqual(chip.contents[0xe000:0x10000], components["otadata.bin"])
                if model != "picoferm":
                    self.assertEqual(chip.contents[0x1000:0x8000], before[0x1000:0x8000])
                # Restore even when neither application's boot metadata is readable.
                chip.contents[:0x10000] = bytes(0x10000)
                self.run_action("restore", model, chip, ["RESTORE", chip.identity.mac])
                self.assertEqual(chip.contents, before)
                path = usb.backup_path(self.data, chip.identity)
                usb.private_files.assert_private(path)
                self.assertEqual(usb.load_original(path, chip.identity, model), before)

    def test_usb_pairing_key_uses_verified_filesystem_and_retry_preserves_machine(self):
        import secrets
        import spiffs_format
        for model in ("picoferm", "picostill", "z-series"):
            with self.subTest(model=model):
                chip = device(model)
                data = self.data / model
                key = secrets.token_hex(32)
                usb.operation("install", model, chip, data, ask=lambda _: chip.identity.mac, api_key=key)
                layout = usb.FERM_LAYOUT if model == "picoferm" else usb.ESP32_LAYOUT
                profile = "esp8266-2m" if model == "picoferm" else "esp32-4m"
                if model == "z-series" and "recovery.bin" in usb.reference(model)[1]:
                    layout,profile=usb.Z_LAYOUT,"esp32-z-recovery"
                start, size = layout["spiffs.bin"]
                image = bytes(chip.contents[start:start+size])
                self.assertTrue(spiffs_format.inspect_image(image, profile, {"/pb/api.key": key.encode()})["exact_contents"])
                writes = len(chip.writes)
                usb.operation("install", model, chip, data, ask=lambda _: self.fail("retry wrote"), api_key=key)
                for bad_key in (secrets.token_hex(32), "0"*64):
                    with self.assertRaises(ValueError):
                        usb.operation("install", model, chip, data, api_key=bad_key)
                self.assertEqual(len(chip.writes), writes)
                package = usb.picoferm_ab_reference() if model == "picoferm" else usb.reference(model)
                damaged = bytearray(package[1]["spiffs.bin"])
                damaged[-1] ^= 1
                with self.assertRaisesRegex(ValueError,"verified empty"):
                    usb.esp_provisioning.stage(damaged,profile,key)

    def test_refuse_wrong_model_and_unknown_active_flash_before_any_write(self):
        for model, selection in (("picostill", "picoferm"), ("picostill", "z-series"), ("picoferm", "picoferm")):
            chip = device(model)
            if model == selection:
                chip.contents[0] ^= 1
            with self.assertRaises(ValueError):
                self.run_action("install", selection, chip)
            self.assertEqual(chip.writes, [])
            self.assertFalse(list(self.data.rglob("*.zip")))

    def test_z_oem_eeprom_boiler_and_wired_selection_survive_conversion(self):
        chip = device("z-series")
        original = chip.contents
        rows = [(1,2,0x9000,0x5000,b"nvs"),(1,0,0xe000,0x2000,b"otadata"),
                (0,0,0x10000,0x280000,b"factory"),(1,0x99,0x290000,0x1000,b"eeprom"),
                (1,0x82,0x291000,0x16f000,b"spiffs")]
        records = b"".join(struct.pack("<HBBII16sI",0x50aa,*r,0) for r in rows)
        table = records + b"\xeb\xeb"+b"\xff"*14+hashlib.md5(records).digest()
        original[0x8000:0x8c00] = table.ljust(0xc00,b"\xff")
        board = bytearray(120)
        board[4]=1
        board[116]=1
        original[0x290000:0x290000+120]=board
        key="12"*32
        _,target=usb.install_target(bytes(original),"z-series",api_key=key)
        files=usb.esp_provisioning._read_migration_image(target[0x291000:])["files"]
        self.assertEqual(files["/pb/zboard.seed"],board)
        self.assertEqual(files["/pb/api.key"],key.encode())
        # Unknown board revisions must stop before a write.
        original[0x290000]=2
        with self.assertRaisesRegex(ValueError,"Unrecognized Z board settings"):
            usb.install_target(bytes(original),"z-series",api_key=key)

    def test_z_stack_label_is_eligible_to_capture_but_not_qualified_for_install(self):
        chip = device("z-series")
        for model in usb.Z_STACKS:
            with self.subTest(model=model), self.assertRaisesRegex(ValueError, "physical Z model"):
                self.run_action("install", model, chip)
        self.assertEqual(chip.writes, [])
        path = usb.backup_path(self.data, chip.identity)
        self.assertEqual(usb.load_original(path, chip.identity, "z2"), chip.contents)

    def test_picostill_usb_migration_preserves_the_live_device(self):
        import secrets
        chip = device("picostill")
        stock = bytes(chip.contents)
        key = secrets.token_hex(32)
        _, current_parts = usb.reference("picostill")
        if "recovery.bin" not in current_parts:
            self.skipTest("No PicoStill factory-recovery package in this index")
        legacy = single_factory_parts("picostill")
        app = legacy["application.bin"]
        approved = patch.object(usb, "legacy_applications", return_value=[
            {"binary_bytes": len(app), "binary_sha256": usb.sha(app)}])
        approved.start()
        self.addCleanup(approved.stop)
        with patch.object(usb, "reference", side_effect=lambda model: ("1.2.6", dict(legacy))):
            usb.operation("install", "picostill", chip, self.data, ask=lambda _: chip.identity.mac, api_key=key)
        # Simulate changed calibration and used filesystem space between boots.
        chip.contents[0x9110:0x9116] = b"config"
        chip.contents[-1024:-1015] = b"brew data"
        used = bytes(chip.contents)
        self.assertTrue(usb.esp_provisioning.matches_stored_esp32_key(chip.contents[0x200000:0x400000], key))
        writes = len(chip.writes)
        with self.assertRaisesRegex(ValueError, "pairing"):
            usb.operation("install", "picostill", chip, self.data, api_key=secrets.token_hex(32))
        self.assertEqual(len(chip.writes), writes)
        usb.operation("install", "picostill", chip, self.data, ask=lambda _: chip.identity.mac, api_key=key)
        self.assertEqual(usb.esp_stock_partitions.selected_app_slice(chip.contents).start, 0x100000)
        writes = len(chip.writes)
        usb.operation("install", "picostill", chip, self.data, ask=lambda _: self.fail("retried migration wrote"), api_key=key)
        self.assertEqual(len(chip.writes), writes)
        self.assertEqual(chip.contents[0x200000:], used[0x200000:])
        self.assertEqual(chip.contents[0x9000:0xe000], used[0x9000:0xe000])
        self.assertEqual(usb.load_original(usb.backup_path(self.data, chip.identity), chip.identity, "picostill"), stock)

    def test_z_usb_migration_preserves_storage_and_original(self):
        import secrets
        import esp_provisioning
        chip=device("z-series")
        original=bytes(chip.contents)
        key=secrets.token_hex(32)
        parts = single_factory_parts("z-series")
        app = parts["application.bin"]
        approved = patch.object(usb, "legacy_applications", return_value=[
            {"binary_bytes": len(app), "binary_sha256": usb.sha(app)}])
        approved.start()
        self.addCleanup(approved.stop)
        with patch.object(usb,"reference",return_value=("1.2.8",parts)):
            usb.operation("install","z-series",chip,self.data,ask=lambda _:chip.identity.mac,api_key=key)
        chip.contents[0x9100:0x9109]=b"unit data"
        before=bytes(chip.contents)
        damaged=chip.contents[0x200000+252]
        chip.contents[0x200000+252] ^= 1
        count=len(chip.writes)
        with self.assertRaises(ValueError):
            usb.operation("install","z-series",chip,self.data,api_key=key)
        self.assertEqual(count,len(chip.writes))
        chip.contents[0x200000+252]=damaged
        usb.operation("install","z-series",chip,self.data,ask=lambda _:chip.identity.mac,api_key=key)
        base=usb.backup_path(self.data,chip.identity)
        self.assertEqual(usb.load_original(base,chip.identity,"z-series"),original)
        capture=base.with_name("before-repartition-"+usb.sha(before)+".bin")
        self.assertEqual(usb.private_files.read_regular(capture,len(before),private=True),before)
        self.assertEqual(chip.contents[:0x8000],before[:0x8000])
        self.assertEqual(chip.contents[0x9000:0xe000],before[0x9000:0xe000])
        self.assertEqual(usb.esp_stock_partitions.selected_app_slice(chip.contents).start,0xf0000)
        self.assertEqual(esp_provisioning._read_migration_image(before[0x200000:])["files"],
                         esp_provisioning._read_migration_image(chip.contents[0x291000:])["files"])
        usb.operation("install","z-series",chip,self.data,ask=lambda _:self.fail("retry wrote"),api_key=key)

    def test_picostill_refuses_mixed_stock_and_custom_slots(self):
        chip = device("picostill")
        table = chip.contents[0x8000:0x8080]
        struct.pack_into("<I", table, 2*32 + 8, usb.MIB)
        table += struct.pack("<HBBII16sI", 0x50aa, 0, 0x10, 0x110000, 0xe0000, b"ota_0", 0)
        chip.contents[0x8000:0x8c00] = table.ljust(0xc00, b"\xff")
        _, components = usb.reference("picostill")
        custom = components["application.bin"]
        chip.contents[0x110000:0x110000 + len(custom)] = custom
        with self.assertRaises(ValueError):
            self.run_action("backup", "picostill", chip)
        self.assertFalse(list(self.data.rglob("*.zip")))
        self.assertEqual(chip.writes, [])

    def test_picoferm_unknown_geometry_or_occupied_regions_preserve_a_backup(self):
        for index in (None, usb.MIB + 5):
            chip = device("picoferm", "0_1_0" if index is None else None)
            if index is not None:
                chip.contents[index] = 0x5a
            self.data = self.data / str(index)
            with self.assertRaisesRegex(ValueError, "boot parameters|Unidentified|Unknown data or geometry|Existing flash data"):
                self.run_action("install", "picoferm", chip)
            self.assertEqual(chip.writes, [])
            self.assertEqual(usb.load_original(usb.backup_path(self.data, chip.identity), chip.identity, "picoferm"),
                             chip.contents)

    def test_unstable_read_and_denied_confirmations_never_write(self):
        chip = device("picoferm")
        contents = bytes(chip.contents)
        with patch.object(chip, "read", side_effect=[contents, bytes(len(contents))]), self.assertRaisesRegex(ValueError, "disagree"):
            self.run_action("install", "picoferm", chip)
        self.assertFalse(list(self.data.rglob("*.zip")))
        with self.assertRaisesRegex(ValueError, "confirmation"):
            self.run_action("install", "picoferm", chip, ["no"])
        chip.contents[0] = 0
        for answers in (["no"], ["RESTORE", "no"]):
            with self.assertRaisesRegex(ValueError, "cancelled"):
                self.run_action("restore", "picoferm", chip, answers)
        with patch.object(chip, "verify_identity", side_effect=ValueError("device changed")), self.assertRaisesRegex(ValueError, "changed"):
            self.run_action("restore", "picoferm", chip, ["RESTORE", chip.identity.mac])
        self.assertEqual(chip.writes, [])

    def test_private_archive_wrong_uid_geometry_or_corruption_rejected(self):
        chip = device("picoferm")
        self.run_action("backup", "picoferm", chip)
        path = usb.backup_path(self.data, chip.identity)
        for identity in (replace(chip.identity, mac="112233445566"), replace(chip.identity, jedec=0x1540aa)):
            with self.assertRaisesRegex(ValueError, "identity"):
                usb.load_original(path, identity, "picoferm")
        chip.identity = replace(chip.identity, mac="223344556677")
        with self.assertRaisesRegex(ValueError, "no private"):
            self.run_action("restore", "picoferm", chip)
        with path.open("r+b") as file:
            file.seek(200000)
            file.write(b"invalid archive bytes")
        with self.assertRaises(zipfile.BadZipFile):
            usb.load_original(path, replace(chip.identity, mac="102030405060"), "picoferm")
        self.assertEqual(chip.writes, [])

    def test_failed_write_or_verification_remains_recoverable(self):
        chip = device("picoferm")
        before = bytes(chip.contents)
        writes = chip.write

        def interrupt(offset, data):
            writes(offset, data)
            raise OSError("serial disconnected")
        with patch.object(chip, "write", side_effect=interrupt), self.assertRaises(OSError):
            self.run_action("install", "picoferm", chip, [chip.identity.mac])
        with patch.object(chip, "write"), self.assertRaisesRegex(ValueError, "verification failed"):
            self.run_action("restore", "picoferm", chip, ["RESTORE", chip.identity.mac])
        self.run_action("restore", "picoferm", chip, ["RESTORE", chip.identity.mac])
        self.assertEqual(chip.contents, before)

    @unittest.skipUnless(importlib.util.find_spec("esptool"), "Install requirements-usb.txt for SDK integration")
    def test_real_sdk_compressed_write_and_closed_device_session(self):
        import esptool
        import serial
        # Only the transport and the attached chip are simulated. Espressif's
        # full image/security checks, compressor, block writer and MD5 verifier run.
        for model in ("picostill", "picoferm"):
            with self.subTest(model=model):
                chip = device(model)
                rom = MagicMock()
                rom.CHIP_NAME = chip.identity.chip
                rom.secure_download_mode = rom.stub_is_disabled = rom.IS_STUB = False
                rom.get_secure_boot_enabled.return_value = False
                rom.get_flash_encryption_enabled.return_value = False
                rom.read_mac.return_value = bytes.fromhex(chip.identity.mac)
                stub = rom.run_stub.return_value
                stub.CHIP_NAME = chip.identity.chip
                stub.IMAGE_CHIP_ID = 0
                stub.get_major_chip_version.return_value = 3
                stub.secure_download_mode = False
                stub.get_encrypted_download_disabled.return_value = False
                stub.get_secure_boot_enabled.return_value = False
                stub.flash_id.return_value = chip.identity.jedec
                stub.read_mac.return_value = bytes.fromhex(chip.identity.mac)
                stub.IS_STUB = True
                stub.FLASH_SECTOR_SIZE = usb.SECTOR
                stub.FLASH_WRITE_SIZE = 16384
                stub.BOOTLOADER_FLASH_OFFSET = 0 if model == "picoferm" else 0x1000
                pending = bytearray()
                def begin(_size, compressed, _address):
                    return (compressed + stub.FLASH_WRITE_SIZE - 1) // stub.FLASH_WRITE_SIZE
                stub.flash_defl_begin.side_effect = begin
                stub.flash_defl_block.side_effect = lambda block, _seq, timeout: pending.extend(block)
                def verify_written(_address, _size):
                    return hashlib.md5(zlib.decompress(pending)).hexdigest()
                stub.flash_md5sum.side_effect = verify_written
                port = "COM42" if os.name == "nt" else "/dev/null"
                with patch.object(esptool, "detect_chip", return_value=rom) as detect, patch.object(serial, "Serial") as serial_open:
                    with usb.open_device(port, model) as link:
                        self.assertEqual(link.identity, chip.identity)
                        link.verify_identity()
                        self.assertEqual(stub.WRITE_FLASH_ATTEMPTS, 1)
                        if model == "picoferm":
                            _, components = usb.picoferm_ab_reference()
                            start, image = 0x3000, components["bank-0.bin"]
                        else:
                            _, components = usb.reference(model)
                            start, image = usb.ESP32_LAYOUT["application.bin"][0], components["application.bin"]
                        payload = image.ljust(usb.MIB, b"\xff")
                        link.write(start, payload)
                        self.assertEqual(zlib.decompress(pending), payload)
                    serial_open.assert_called_once_with(port=None, baudrate=115200, exclusive=True)
                    self.assertFalse(serial_open.return_value.dtr)
                    self.assertFalse(serial_open.return_value.rts)
                    detect.assert_called_once_with(serial_open.return_value, 115200, connect_mode="no_reset", connect_attempts=1)
                    self.assertFalse(stub.flash_defl_finish.call_args.kwargs["reboot"])
                    serial_open.return_value.__exit__.assert_called_once()

    @unittest.skipUnless(importlib.util.find_spec("esptool"), "Install requirements-usb.txt for SDK integration")
    def test_rom_security_and_chip_checks_precede_every_flash_access(self):
        import esptool
        import serial
        port = "COM42" if os.name == "nt" else "/dev/null"
        for chip, encryption, secure_boot in (("ESP32", True, False), ("ESP32", False, True), ("ESP32-S3", False, False)):
            rom = MagicMock()
            rom.CHIP_NAME = chip
            rom.secure_download_mode = rom.IS_STUB = rom.stub_is_disabled = False
            rom.get_secure_boot_enabled.return_value = secure_boot
            rom.get_flash_encryption_enabled.return_value = encryption
            with patch.object(esptool, "detect_chip", return_value=rom), patch.object(serial, "Serial"):
                with self.assertRaises(ValueError), usb.open_device(port, "picostill"):
                    self.fail("Protected or incompatible device accepted")
            rom.run_stub.assert_not_called()

    def test_multiple_captures_keep_first_original_and_changed_machine_settings(self):
        chip = device("picoferm")
        original = bytes(chip.contents)
        self.run_action("backup", "picoferm", chip)
        path = usb.backup_path(self.data, chip.identity)
        chip.contents[-1] ^= 1
        self.run_action("backup", "picoferm", chip)
        self.run_action("backup", "picoferm", chip)
        self.assertEqual(usb.load_original(path, chip.identity, "picoferm"), original)
        self.assertEqual(usb.load_original(next(path.parent.glob("stock-*.zip")), chip.identity, "picoferm"), chip.contents)
        self.assertFalse(chip.writes)


if __name__ == "__main__":
    unittest.main()
