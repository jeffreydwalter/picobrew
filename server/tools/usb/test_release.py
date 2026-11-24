import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import release


class ProductionReleaseTest(unittest.TestCase):
    def test_versions_keep_first_baseline_and_retry_exact_commit(self):
        self.assertEqual(release.next_tag(["usb-tools-v1.0.1", "usb-candidate-1-2"], []), "v1.0.0")
        self.assertEqual(release.next_tag(["v1.0.9", "v1.0.10"], []), "v1.0.11")
        self.assertEqual(release.next_tag(["v1.0.0", "v1.0.1"], ["v1.0.0"]), "v1.0.0")

    def test_release_waits_for_missing_and_running_checks_on_the_pushed_revision(self):
        def run(name, status, conclusion, attempt=1):
            return {"path": ".github/workflows/" + name, "id": 19, "run_attempt": attempt,
                    "status": status, "conclusion": conclusion}
        runs = [run(name, "completed", "success") for name in release.REQUIRED_WORKFLOWS]
        runs.append(run("hub-usb-tools.yml", "in_progress", None))
        self.assertFalse(release.pending_workflows(runs))
        self.assertTrue(release.pending_workflows(runs[1:]))
        self.assertEqual(release.pending_workflows(runs + [run("server.yml", "in_progress", None, 2)]), {"server.yml"})
        with self.assertRaisesRegex(ValueError, "server.yml"):
            release.pending_workflows(runs + [run("server.yml", "completed", "failure", 2)])

    def test_pin_and_release_cover_matching_workers_hubs_and_licenses(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            assets = root / "assets"
            assets.mkdir()
            pins = root / "pins.json"
            fingerprint = "a" * 64
            tag = "v1.0.0"
            for key in release.AVR:
                suffix = ".exe" if key.startswith("windows") else ""
                name = "picobrew-usb-" + key + suffix
                worker = assets / name
                worker.write_bytes(key.encode())
                notices = assets / f"usb-notices-{key}.zip"
                with zipfile.ZipFile(notices, "w") as archive:
                    archive.writestr("versions.json", "{}")
                metadata = {"name": name, "platform": key, "bytes": worker.stat().st_size,
                            "sha256": release.sha(worker), "inputs_sha256": fingerprint,
                            "notices_sha256": release.sha(notices)}
                (assets / (key + ".json")).write_text(json.dumps(metadata))
                (assets / name.replace("usb-", "hub-")).write_bytes(b"Hub" + key.encode())
            with patch.object(release, "PINS", pins), patch.object(release, "worker_inputs", return_value=fingerprint), patch.object(release, "SOURCES", {}):
                release.pin(tag, "usb-candidate-123-2", assets)
                release.assemble(tag, assets, "b" * 40)
                manifest = json.loads((assets / "releases.json").read_text())
                self.assertEqual(manifest["source_commit"], "b" * 40)
                self.assertEqual(set(manifest["hubs"]), set(release.AVR))
                self.assertEqual(set(manifest["assets"]), set(release.AVR))
                sums = (assets / "SHA256SUMS.txt").read_text().splitlines()
                self.assertEqual(len(sums), 26)
                for entry in sums:
                    digest, name = entry.split("  ")
                    self.assertEqual(digest, hashlib.sha256((assets / name).read_bytes()).hexdigest())
                with patch.object(release, "worker_inputs", return_value="c" * 64):
                    with self.assertRaisesRegex(ValueError, "checkout"):
                        release.assemble(tag, assets, "b" * 40)
                (assets / "picobrew-usb-linux-arm").write_bytes(b"unverified")
                with self.assertRaisesRegex(ValueError, "provenance"):
                    release.assemble(tag, assets, "b" * 40)
