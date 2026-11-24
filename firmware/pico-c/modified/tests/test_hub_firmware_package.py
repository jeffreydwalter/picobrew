#!/usr/bin/env python3
"""Exercise real release inputs, deterministic ZIPs, and corrupted package rejection."""

import gzip
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import struct
import subprocess
import sys
import tempfile
import unittest
import zipfile
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tools"))
import embed_web
import package_hub_firmware as package


class HubFirmwarePackageTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.firmware = self.directory / "firmware"
        self.web = self.directory / "web"
        shutil.copytree(ROOT / "firmware/standalone/pico-c/ssd1325", self.firmware)
        shutil.copytree(ROOT / "firmware/web", self.web)
        self.report_path = self.firmware / "firmware-report.json"
        self.binary_path = self.firmware / "pico-c-standalone.bin"
        self.manifest_path = self.web / "manifest.json"
        self.output = self.directory / "package.zip"

    def test_generated_assets_fit_existing_recovery_slots(self):
        source = self.directory / "source"
        shutil.copytree(ROOT / "web", source / "web")
        for name, limit in [("style.css", 3584), ("app.js", 7680)]:
            with self.subTest(name=name), patch.object(embed_web, "ROOT", source):
                path = source / "web" / name
                original = path.read_bytes()
                path.write_bytes(hashlib.shake_256(b"oversized asset").digest(limit + 128))
                with self.assertRaisesRegex(ValueError, f"{name}: .*recovery limit of {limit}"):
                    embed_web.generate()
                path.write_bytes(original)

    def build(self):
        return package.package_firmware("pico-c", "ssd1325", self.firmware, self.web, self.output)

    def edit_report(self, change):
        report = json.loads(self.report_path.read_bytes())
        change(report)
        self.report_path.write_text(json.dumps(report), encoding="utf-8")

    def edit_manifest(self, change):
        manifest = json.loads(self.manifest_path.read_bytes())
        change(manifest)
        raw = json.dumps(manifest).encode()
        self.manifest_path.write_bytes(raw)
        self.edit_report(lambda report: report.update(browser_storage=manifest,
                         browser_manifest_sha256=hashlib.sha256(raw).hexdigest()))

    def test_both_committed_displays_produce_identical_exact_eight_file_archives(self):
        for display in ("ssd1325", "ssd1309"):
            model = "pico-c"
            with self.subTest(display=display):
                first, second = self.directory / f"{display}-1.zip", self.directory / f"{display}-2.zip"
                package.package_firmware(model, display, output=first)
                package.package_firmware(model, display, output=second)
                self.assertEqual(first.read_bytes(), second.read_bytes())
                expected = {
                    "firmware-report.json": ROOT / "firmware/standalone" / model / display / "firmware-report.json",
                    model + "-standalone.bin": ROOT / "firmware/standalone" / model / display / (model + "-standalone.bin"),
                    "web/manifest.json": ROOT / "firmware/web/manifest.json",
                }
                expected.update({"web/" + source + ".gz": ROOT / "firmware/web" / (source + ".gz")
                                 for source, _ in package.ASSETS})
                with zipfile.ZipFile(first) as archive:
                    self.assertEqual(archive.namelist(), sorted(expected))
                    self.assertEqual(len(archive.infolist()), 8)
                    self.assertEqual(archive.comment, b"")
                    self.assertIsNone(archive.testzip())
                    for info in archive.infolist():
                        self.assertEqual(archive.read(info), expected[info.filename].read_bytes())
                        self.assertEqual(info.date_time, package.ZIP_DATE)
                        self.assertEqual(info.compress_type, zipfile.ZIP_STORED)
                        self.assertEqual(info.create_system, 3)
                        self.assertEqual(info.external_attr >> 16, stat.S_IFREG | 0o644)
                        self.assertEqual(info.extra, b"")

    def test_input_mtime_and_permissions_do_not_affect_zip_and_report_is_unchanged(self):
        original_report = self.report_path.read_bytes()
        self.build()
        original_zip = self.output.read_bytes()
        for path in [self.binary_path, self.report_path, *self.web.iterdir()]:
            os.utime(path, (946684800, 946684800))
            path.chmod(0o644)
        self.build()
        self.assertEqual(original_zip, self.output.read_bytes())
        self.assertEqual(original_report, self.report_path.read_bytes())

    def test_wrong_model_profile_or_filename_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "model"):
            package.package_firmware("pico-s", "ssd1325", self.firmware, self.web, self.output)
        original = self.report_path.read_bytes()
        for field, value in (("model", "pico-pro"), ("model_family", "pico-s-pro"),
                             ("display_variant", "ssd1309"), ("display_variant", ""),
                             ("display_geometry", {}), ("firmware_update", True),
                             ("profile", "legacy"), ("firmware_version", "../unsafe"), ("api_key_embedded", True),
                             ("files", {"binary": "../pico-c-standalone.bin"})):
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.report_path.write_bytes(original)
                self.edit_report(lambda report: report.update({field: value}))
                self.build()
        self.assertFalse(self.output.exists())

    def test_binary_hash_and_declared_length_must_match(self):
        original = self.binary_path.read_bytes()
        for raw in (original[:-1], original[:-1] + bytes([original[-1] ^ 1])):
            with self.subTest(length=len(raw)), self.assertRaises(ValueError):
                self.binary_path.write_bytes(raw)
                self.build()
        self.assertFalse(self.output.exists())

    def test_unsafe_vectors_rejected_even_with_updated_hash_and_report(self):
        original = self.binary_path.read_bytes()
        original_report = self.report_path.read_bytes()
        sp, reset = struct.unpack_from("<II", original)
        for candidate_sp, candidate_reset in ((package.IMAGE_BASE + 8, reset),
                                              (sp + 1, reset), (package.RAM_END + 8, reset),
                                              (sp, reset & ~1), (sp, package.IMAGE_BASE + 1),
                                              (sp, package.IMAGE_BASE + len(original) + 1)):
            with self.subTest(sp=candidate_sp, reset=candidate_reset), self.assertRaisesRegex(ValueError, "vector"):
                self.report_path.write_bytes(original_report)
                raw = struct.pack("<II", candidate_sp, candidate_reset) + original[8:]
                self.binary_path.write_bytes(raw)

                def update(report):
                    report["binary_sha256"] = hashlib.sha256(raw).hexdigest()
                    report["image_layout"].update(initial_stack_pointer=hex(candidate_sp),
                                                   reset_vector=hex(candidate_reset), entry=hex(candidate_reset))
                self.edit_report(update)
                self.build()

    def test_report_layout_and_segment_extent_must_match(self):
        original = self.report_path.read_bytes()
        changes = (
            lambda layout: layout.update(passed=False),
            lambda layout: layout.update(binary_bytes=True),
            lambda layout: layout.update(reset_vector="0x20004009"),
            lambda layout: layout.update(undefined_symbols=["missing_function"]),
            lambda layout: layout["load_segments"][0].update(file_bytes=1),
            lambda layout: layout["load_segments"].append(dict(layout["load_segments"][0])),
        )
        for change in changes:
            with self.subTest(change=change), self.assertRaises(ValueError):
                self.report_path.write_bytes(original)
                self.edit_report(lambda report: change(report["image_layout"]))
                self.build()

    def test_missing_browser_asset_or_manifest_is_rejected_without_output(self):
        for path in (self.web / "history.js.gz", self.manifest_path):
            data = path.read_bytes()
            path.unlink()
            with self.subTest(path=path.name), self.assertRaises(OSError):
                self.build()
            path.write_bytes(data)
        self.assertFalse(self.output.exists())

    def test_browser_mapping_allocation_and_manifest_hash_are_checked(self):
        original_manifest, original_report = self.manifest_path.read_bytes(), self.report_path.read_bytes()
        changes = (
            lambda manifest: manifest.update(fail_safe=True),
            lambda manifest: manifest.update(unknown_field=True),
            lambda manifest: manifest["assets"].pop(),
            lambda manifest: manifest["assets"].reverse(),
            lambda manifest: manifest["assets"][0].update(payload="../index.html.gz"),
            lambda manifest: manifest["assets"][0].update(file_path="/local/web/a.gz"),
            lambda manifest: manifest["assets"][0].update(max_size=4096),
            lambda manifest: manifest["assets"][0].update(allocation_blocks=True),
        )
        for change in changes:
            with self.subTest(change=change), self.assertRaises(ValueError):
                self.manifest_path.write_bytes(original_manifest)
                self.report_path.write_bytes(original_report)
                self.edit_manifest(change)
                self.build()
        self.report_path.write_bytes(original_report)
        self.manifest_path.write_bytes(original_manifest + b" ")
        with self.assertRaisesRegex(ValueError, "manifest SHA-256"):
            self.build()

    def test_gzip_integrity_bounded_source_and_trailing_members_are_checked(self):
        asset_path = self.web / "index.html.gz"
        original_manifest, original_report = self.manifest_path.read_bytes(), self.report_path.read_bytes()
        packed = asset_path.read_bytes()
        raw = gzip.decompress(packed)
        for corrupt in (packed[:-1] + bytes([packed[-1] ^ 1]), packed + gzip.compress(b"second member"),
                        gzip.compress(b"x" * (package.MAX_SOURCE + 1), mtime=0)):
            with self.subTest(size=len(corrupt)), self.assertRaisesRegex(ValueError, "gzip"):
                self.report_path.write_bytes(original_report)
                self.manifest_path.write_bytes(original_manifest)
                asset_path.write_bytes(corrupt)

                def update(manifest):
                    blocks = (len(corrupt) + 512 + 4095) // 4096
                    manifest["assets"][0].update(length=len(corrupt), allocation_blocks=blocks,
                                                  max_size=blocks * 4096 - 512,
                                                  sha256=hashlib.sha256(corrupt).hexdigest())
                self.edit_manifest(update)
                self.build()
        asset_path.write_bytes(packed)
        self.report_path.write_bytes(original_report)
        self.manifest_path.write_bytes(original_manifest)
        self.edit_manifest(lambda manifest: manifest["assets"][0].update(source_sha256=hashlib.sha256(raw + b"!").hexdigest()))
        with self.assertRaisesRegex(ValueError, "source SHA-256"):
            self.build()

    def test_invalid_input_does_not_replace_existing_zip(self):
        self.build()
        original = self.output.read_bytes()
        for key in ("model", "Model"):
            self.report_path.write_text('{"model":"pico-s","' + key + '":"pico-c"}', encoding="utf-8")
            with self.subTest(key=key), self.assertRaisesRegex(ValueError, "Duplicate JSON"):
                self.build()
            self.assertEqual(original, self.output.read_bytes())

    def test_cli_packages_custom_directory_without_compiler_and_rejects_unsupported_model(self):
        command = [sys.executable, str(ROOT / "tools/package_hub_firmware.py")]
        result = subprocess.run(command + ["--model", "pico-c", "--display", "ssd1325", "--firmware-directory", str(self.firmware),
                                          "--web-directory", str(self.web), "--output", str(self.output)],
                                capture_output=True, text=True, check=True)
        self.assertIn("8 files", result.stdout)
        result = subprocess.run(command + ["--model", "pico-pro", "--display", "ssd1325"], capture_output=True, text=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn("invalid choice", result.stderr)


if __name__ == "__main__":
    unittest.main()
