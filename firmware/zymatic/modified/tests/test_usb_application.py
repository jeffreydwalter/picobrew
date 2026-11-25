"""USB program and undo a real provisioned image using a memory-backed programmer."""

from hashlib import sha256
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
import zlib

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools"))
import zymatic_build as build
import compact_build
import usb_layout
import wifi_build
import usb_application
import usb_avr as avr
import usb_stock as stock


class MemoryProgrammer:
    def __init__(self, flash):
        self.flash = flash
        self.application_limit = avr.BOOT_MIN
        self.identity = ("virtual", "synthetic-device", "usb:1")
        self.writes = 0
        self.interrupt = False
        self.tear_page = None

    def run(self, work, operations, *, read_only):
        if read_only:
            signature, flash = operations
            assert signature.startswith("signature:r:") and flash.startswith("flash:r:")
            (work / signature.split(":")[2]).write_bytes(avr.SIGNATURE)
            (work / flash.split(":")[2]).write_bytes(self.flash)
            return
        assert operations == ["flash:v:read-1.bin:r", "flash:w:app-pages.bin:r"]
        assert self.flash == (work / "read-1.bin").read_bytes()
        pages = (work / "app-pages.bin").read_bytes()
        assert len(pages) <= self.application_limit and len(pages) % avr.PAGE == 0
        self.writes += 1
        if self.tear_page is not None:
            start = self.tear_page * avr.PAGE
            assert start + avr.PAGE <= len(pages)
            self.flash = pages[:start] + bytes([0x79]) * avr.PAGE + self.flash[start+avr.PAGE:]
            raise ValueError("Power lost during page programming")
        if self.interrupt:
            start = usb_application.end_page(len(pages)) - 3 * avr.PAGE
            self.flash = pages[:start] + self.flash[start:]
            raise ValueError("Interrupted programming")
        self.flash = pages + self.flash[len(pages):]


@unittest.skipUnless(os.environ.get("ZYMATIC_TEST_HEX"), "point ZYMATIC_TEST_HEX at the verified factory HEX or ZIP")
class ApplicationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.original = stock.decode_application(stock.unpack_source(Path(os.environ['ZYMATIC_TEST_HEX']).read_bytes()))

    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.data = Path(temp.name) / "hub"
        self.key = self.data / "device.key"
        # Keep program expansion erased and put nontrivial bytes into the
        # protected boot section so accidental full-chip replacement is visible.
        self.before = self.original.ljust(avr.BOOT_MIN, b'\xff') + bytes(range(256)) * ((avr.FLASH - avr.BOOT_MIN) // 256)
        self.device = MemoryProgrammer(self.before)
        controller = patch.object(avr, "Controller", return_value=self.device)
        controller.start()
        self.addCleanup(controller.stop)

    def perform(self, action, answers, **options):
        confirmations = iter(answers)
        return avr.service(self.data, "virtual", action, confirm=lambda _: next(confirmations), **options)

    def install(self, **options):
        self.perform("install", ["INSTALL ZYMATIC", "WRITE " + build.VERSION], key_file=self.key, **options)

    def test_measured_boot_window_upgrade_and_restore(self):
        boot_start = avr.FLASH - 1024
        boot = bytes(range(256)) * 4
        original_flash = self.original.ljust(boot_start, b"\xff") + boot
        self.device.flash = original_flash
        self.install(hub_host="brew.lan:8080")
        older = self.device.flash
        original_id = usb_layout.identify(older)
        self.assertEqual((original_id["firmware_version"], original_id["build_profile"]), ("1.0.0", "compact"))
        original_key = self.key.read_bytes()
        directory = self.data / "usb-stock" / "zymatic"
        digest = sha256(older).hexdigest()
        stock.save_private(directory / f"isp-flash-{digest}.bin", older)
        report = {"schema_version": 1, "interface": "atmelice_isp", "signature": "1e9705",
                  "flash_sha256": digest, "usb_backup_sha256": digest, "registers": {"hfuse":"de", "lock":"ff"},
                  "configured_boot_region": {"size_bytes":1024, "sha256": sha256(boot).hexdigest()}}
        raw = json.dumps(report).encode()
        stock.save_private(directory / f"isp-evidence-v1-{sha256(raw).hexdigest()}.json", raw)
        self.assertEqual(usb_layout.qualified_limit(directory, older, dict(original_id, uid="other")), avr.BOOT_MIN)
        self.assertEqual(usb_layout.qualified_limit(directory, older[:-1]+b"X", original_id), avr.BOOT_MIN)
        self.device.application_limit=boot_start
        self.perform("install", ["INSTALL ZYMATIC", "WRITE " + build.VERSION], key_file=self.key)
        upgraded = usb_layout.identify(self.device.flash)
        self.assertEqual((upgraded["uid"], upgraded["hub_host"], upgraded["firmware_version"]),
                         (original_id["uid"], "brew.lan:8080", build.VERSION))
        self.assertEqual(upgraded["build_profile"], "extended")
        self.assertGreater(upgraded["application_bytes"], avr.BOOT_MIN)
        self.assertEqual(self.perform("install", [], key_file=self.key), original_id["uid"])
        self.assertEqual(self.device.writes, 2)
        self.assertEqual(self.key.read_bytes(), original_key)
        self.assertEqual(self.device.flash[boot_start:], boot)
        self.restore()
        self.assertEqual(self.device.flash, original_flash)

    def test_upgrade_from_each_archived_build_preserves_identity_and_boot(self):
        self.key.parent.mkdir(mode=0o700)
        key = usb_application.read_key(self.key, create=True)
        uid = "ba" * 16
        for release in usb_layout.known_releases():
            if release in (wifi_build.COMPACT, wifi_build.EXTENDED):
                continue
            with self.subTest(version=release.VERSION):
                prior, metadata = release.build_application(self.original, key, uid, "brew.lan")
                limit = usb_layout.MAX_APPLICATION
                boot = bytes(range(256)) * 4
                flash = prior.ljust(limit, b"\xff") + boot
                self.assertEqual(usb_layout.identify(flash), metadata)
                self.assertEqual(metadata["firmware_version"], release.VERSION)
                target, current = usb_application.install_pages(flash, self.original, metadata, self.key, None, limit=limit)
                self.assertEqual(current["firmware_version"], build.VERSION)
                self.assertEqual((current["uid"], current["hub_host"]), (uid, "brew.lan"))
                new_flash = target + flash[len(target):]
                self.assertEqual(usb_layout.identify(new_flash), current)
                self.assertEqual(new_flash[limit:], boot)

    def test_upgrade_from_previous_menu_on_both_profiles(self):
        self.key.parent.mkdir(mode=0o700)
        key = bytes(range(32))
        stock.save_private(self.key, key.hex().encode())
        uid = "ab" * 16
        for release, limit, prior_sha in (
            (compact_build, avr.BOOT_MIN, "cf793d22c7a5d042b326d8b335b4124ff1e1cf06913a67a27cac8ca09c673bfb"),
            (build, usb_layout.MAX_APPLICATION, "d445722d5d50cfa44ea414bf379f71646369cf850c9ce8e881a2b73e8015ec39"),
        ):
            with self.subTest(profile=release.PROFILE):
                prior, metadata = release.build_application(self.original, key, uid, "brew.lan", legacy_menu=True)
                self.assertEqual(sha256(prior).hexdigest(), prior_sha)
                flash = prior.ljust(avr.FLASH, b"\xff")
                self.assertEqual(usb_layout.identify(flash), metadata)
                target, current = usb_application.install_pages(flash, self.original, metadata, self.key, None, limit=limit)
                expected, _ = usb_layout.best_fit(limit).build_application(self.original, key, uid, "brew.lan")
                self.assertNotEqual(prior, expected)
                self.assertEqual(target[:len(expected)], expected)
                self.assertEqual(usb_layout.identify(target), current)

    def test_wifi_images_verify_controls_and_private_fields_independently_of_crc(self):
        recipe = {"name": "Port test", "steps": [
            {"name": "Unheated circulation", "target_f": 0, "minutes": 10, "drain_minutes": 1, "location": 0}]}
        for release in (wifi_build.COMPACT, wifi_build.EXTENDED):
            with self.subTest(profile=release.PROFILE):
                image, info = release.build_application(self.original, bytes(range(31, -1, -1)), "ef"*16,
                                                        "hub.example:49152", recipe)
                self.assertEqual(release.recognize_private_application(image), info)
                for _, address, _, expected in build.hub_port_patches(49152):
                    self.assertEqual(image[address:address+len(expected)], expected)
                _, layout = release.template()
                for address in (4, layout["symbols"]["provision_host"] + len("hub.example:49152") + 1,
                                layout["symbols"]["provision_legacy_sum"], next(build.hub_port_patches(49152))[1]):
                    changed = bytearray(image)
                    changed[address] ^= 1
                    changed[-4:] = zlib.crc32(changed[:-4]).to_bytes(4, "little")
                    self.assertIsNone(release.recognize_private_application(changed))
                self.assertIsNone(release.recognize_private_application(image[:-1]))

    def test_profiles_report_same_version_and_different_capabilities(self):
        for limit, profile in ((0x1e000, "compact"), (0x1f000, "compact"),
                               (0x1f800, "compact"), (0x1fc00, "extended")):
            with self.subTest(limit=hex(limit)):
                release = usb_layout.best_fit(limit)
                image, metadata = release.build_application(self.original, bytes(range(32)), "ab" * 16)
                self.assertEqual((metadata["firmware_version"], metadata["build_profile"]), ("1.0.0", profile))
                self.assertLessEqual(len(image), limit)
                # The extended API compresses its JSON strings. The AVR HTTP tests
                # execute the actual response, including version and controls.
                self.assertEqual(release.VERSION, build.VERSION)
                self.assertEqual(usb_layout.identify(image), metadata)
                for address in (0x8428, 0x844c, 0xa444, 0xa468):
                    self.assertEqual(image[address:address + 2], build.immediate(0xe000, 24, 0))
        with self.assertRaisesRegex(ValueError, "application window"):
            usb_layout.best_fit(0x20000)

    def restore(self):
        self.perform("restore", ["RESTORE ZYMATIC", "WRITE 1.1.14"])

    def test_install_reuse_identity_and_restore_including_credential_pages(self):
        program = {"name": "My recipe", "steps": [
            {"name": "Circulate", "target_f": 0, "minutes": 3, "drain_minutes": 0, "location": 0}]}
        self.install(hub_host="brew.lan", offline_program=program)
        identity = usb_layout.identify(self.device.flash)
        self.assertEqual(identity["hub_host"], "brew.lan")
        self.assertEqual(len(identity["uid"]), 32)
        self.assertEqual(self.device.flash[avr.BOOT_MIN:], self.before[avr.BOOT_MIN:])
        self.install()
        self.assertEqual(self.device.writes, 1)
        self.install(hub_host="other.lan")
        self.assertEqual(usb_layout.identify(self.device.flash)["uid"], identity["uid"])
        self.assertEqual(usb_layout.identify(self.device.flash)["offline_program"]["name"], "My recipe")
        self.restore()
        self.assertEqual(self.device.flash, self.before)

    def test_hub_uses_each_brewers_uid_for_pairing_and_checks_it_before_restore(self):
        def key_file(uid):
            return self.data / ("usb-key-" + uid)

        uid = self.perform("install", ["INSTALL ZYMATIC", "WRITE " + build.VERSION], key_file=key_file)
        self.assertRegex(uid, "^[0-9a-f]{32}$")
        self.assertEqual(usb_layout.identify(self.device.flash)["uid"], uid)
        self.assertTrue(key_file(uid).is_file())
        self.assertEqual(self.perform("install", [], key_file=key_file, expected_uid=uid), uid)
        before, writes = self.device.flash, self.device.writes
        with self.assertRaisesRegex(ValueError, "differs from the registered brewer"):
            self.perform("restore", [], expected_uid="0"*32)
        self.assertEqual((self.device.flash, self.device.writes), (before, writes))
        self.perform("restore", ["RESTORE ZYMATIC", "WRITE 1.1.14"], expected_uid=uid)
        self.assertEqual(self.device.flash, self.before)

    def test_rejects_occupied_or_unknown_stock_and_unconfirmed_install(self):
        with self.assertRaisesRegex(ValueError, "canceled"):
            self.perform("install", ["no"], key_file=self.key)
        mixed = bytearray(self.before)
        mixed[avr.BOOT_MIN - 1] = 0x22
        self.device.flash = bytes(mixed)
        with self.assertRaisesRegex(ValueError, "Occupied flash"):
            self.install()
        self.device.flash = b'\x33' + self.before[1:]
        with self.assertRaisesRegex(ValueError, "requires verified"):
            self.install()
        self.assertEqual(self.device.writes, 0)

    def test_incomplete_install_restores_from_verified_private_transaction(self):
        self.device.interrupt = True
        with self.assertRaisesRegex(ValueError, "Interrupted"):
            self.install()
        self.assertIsNone(usb_layout.identify(self.device.flash))
        self.device.interrupt = False
        self.restore()
        self.assertEqual(self.device.flash, self.before)

    def test_interrupted_install_resumes_original_paired_identity_and_recipe(self):
        self.device.tear_page = 4
        with self.assertRaisesRegex(ValueError, "Power lost"):
            self.install(hub_host="brewing.lan:8080")
        directory = self.data / "usb-stock" / "zymatic"
        record = next(directory.glob("transaction-*.json"))
        target_id = json.loads(record.read_text())["target"]
        target = (directory / ("avr-flash-" + target_id + ".bin")).read_bytes()
        self.device.tear_page = None
        self.install()
        self.assertEqual(self.device.flash, target)

    def test_reconfigure_and_restore_through_two_independent_torn_pages(self):
        self.install(hub_host="one.lan")
        self.device.tear_page = usb_application.end_page(
            usb_layout.identify(self.device.flash)["application_bytes"]) // avr.PAGE - 1
        with self.assertRaisesRegex(ValueError, "Power lost"):
            self.install(hub_host="two.lan:8080")
        self.assertIsNone(usb_layout.identify(self.device.flash))
        # Retry recovery, with a second independent power loss after restoring
        # the complete stock application but before clearing the private data.
        self.device.tear_page = 450
        with self.assertRaisesRegex(ValueError, "Power lost"):
            self.restore()
        self.assertEqual(self.device.flash[:stock.APP_SIZE], self.original)
        self.assertNotEqual(self.device.flash, self.before)
        self.device.tear_page = None
        self.restore()
        self.assertEqual(self.device.flash, self.before)
        self.restore()
        self.assertEqual(self.device.writes, 4)

    def test_scope_and_postwrite_tail_change_fail_before_write(self):
        self.device.tear_page = 345
        with self.assertRaisesRegex(ValueError, "Power lost"):
            self.install()
        self.device.tear_page = None
        self.device.identity = ("virtual", "other-unit", "usb:1")
        with self.assertRaisesRegex(ValueError, "requires verified"):
            self.install()
        self.device.identity = ("virtual2", "synthetic-device", "usb:2")
        saved = self.device.flash
        self.device.flash = saved[:-1] + bytes([saved[-1] ^ 2])
        with self.assertRaisesRegex(ValueError, "differs from the last USB transaction"):
            self.restore()
        self.assertEqual(self.device.writes, 1)
        self.device.flash = saved
        self.restore()
        self.assertEqual(self.device.flash, self.before)

    def test_non_ordered_or_multiple_unexplained_pages_are_rejected(self):
        old = bytes(4 * avr.PAGE)
        new = bytes([1]) * (4 * avr.PAGE)
        correct = new[:avr.PAGE] + bytes([0x39]) * avr.PAGE + old[2*avr.PAGE:]
        self.assertTrue(usb_application.matches_interrupted_write(correct, old, new, len(new)))
        invalid = bytearray(correct)
        invalid[3 * avr.PAGE] = 0x34
        self.assertFalse(usb_application.matches_interrupted_write(invalid, old, new, len(new)))

    def test_completed_transaction_preserves_unrelated_later_upper_flash(self):
        self.install()
        self.restore()
        other = bytearray(self.before)
        other[400:1100] = bytes(700)
        other[avr.BOOT_MIN - 1000:avr.BOOT_MIN-900] = bytes(range(100))
        self.device.flash = bytes(other)
        self.restore()
        self.assertEqual(self.device.flash[:stock.APP_SIZE], self.original)
        self.assertEqual(self.device.flash[stock.APP_SIZE:], other[stock.APP_SIZE:])

    def test_unknown_subcommand_has_no_usb_effects(self):
        with self.assertRaisesRegex(ValueError, "Unknown"):
            self.perform("update", [])
        self.assertFalse(self.data.exists())


if __name__ == '__main__':
    unittest.main()
