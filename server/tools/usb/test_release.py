import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest
import urllib.error
from unittest.mock import patch
import zipfile

import release


class ProductionReleaseTest(unittest.TestCase):
    def test_tag_tracks_source_even_when_earlier_tags_exist(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(release, "ROOT", Path(tmp)):
            source = Path(tmp, "server/internal/server/server.go")
            source.parent.mkdir(parents=True)
            for version in ("1.0.0", "1.0.10", "2.4.0"):
                source.write_text(f'package server\nconst Version = "{version}"\n')
                self.assertEqual(release.source_tag(), "v" + version)
            for version in ("1.02.1", "dev", '1.0.0"\nconst Version = "2.0.0'):
                source.write_text(f'const Version = "{version}"\n')
                with self.assertRaises(ValueError):
                    release.source_tag()

    def test_same_version_stages_only_reviewed_worker_from_candidate_or_published_tag(self):
        key = "linux-arm64"
        worker, notices = b"new worker", b"new notices"
        def digest(data):
            return hashlib.sha256(data).hexdigest()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            output, pins = root / "output", root / "pins.json"
            name = "picobrew-usb-linux-arm64"
            tag, candidate = release.source_tag(), "usb-candidate-111-1"
            assets = {key: {"platform": key, "name": name, "inputs_sha256": "verified",
                            "sha256": digest(worker), "notices_sha256": digest(notices)}}
            pins.write_text(json.dumps({"tag": tag}))
            with patch.object(release, "PINS", pins), patch.object(release, "workers", return_value=assets), \
                    patch.object(release, "worker_inputs", return_value="verified"):
                release.pin(tag, candidate, root)
                self.assertTrue(json.loads(pins.read_text())["prefer_candidate"])
                for retired in (False, True):
                    requests = []
                    def download(url, timeout):
                        asset_tag, _, asset_name = url.removeprefix(release.RELEASE_BASE).partition("/")
                        requests.append(asset_tag)
                        if retired and asset_tag == candidate:
                            raise urllib.error.HTTPError(url, 404, "absent", {}, None)
                        return io.BytesIO(worker if asset_name == name else notices)
                    with patch("urllib.request.urlopen", side_effect=download):
                        release.stage(key, output, tag)
                    self.assertEqual(requests, [candidate, tag, candidate, tag] if retired else [candidate, candidate])
                    self.assertEqual((output / name).read_bytes(), worker)
                with patch("urllib.request.urlopen", return_value=io.BytesIO(b"unexpected")) as fetch:
                    with self.assertRaisesRegex(ValueError, "differs"):
                        release.stage(key, output, tag)
                    self.assertEqual(fetch.call_count, 1)

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
            tag = release.source_tag()
            for key in release.AVR:
                suffix = ".exe" if key.startswith("windows") else ""
                name = "picobrew-usb-" + key + suffix
                worker = assets / name
                worker.write_bytes(key.encode())
                notices = assets / f"usb-notices-{key}.zip"
                with zipfile.ZipFile(notices, "w") as archive:
                    archive.writestr("versions.json", "{}")
                    for notice in ("PicoBrew-LICENSES.md", "PicoBrew-LICENSE.txt", "Hub-LICENSE.txt", "AVRDUDE-LICENSE.txt"):
                        archive.writestr(notice, notice)
                metadata = {"name": name, "platform": key, "bytes": worker.stat().st_size,
                            "sha256": release.sha(worker), "inputs_sha256": fingerprint,
                            "notices_sha256": release.sha(notices)}
                (assets / (key + ".json")).write_text(json.dumps(metadata))
                (assets / name.replace("usb-", "hub-")).write_bytes(b"Hub" + key.encode())
            with patch.object(release, "PINS", pins), patch.object(release, "worker_inputs", return_value=fingerprint), patch.object(release, "SOURCES", {}):
                release.pin(tag, "usb-candidate-123-2", assets)
                self.assertTrue(release.current_pins())
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
                    self.assertFalse(release.current_pins())
                    with self.assertRaisesRegex(ValueError, "checkout"):
                        release.assemble(tag, assets, "b" * 40)
                (assets / "picobrew-usb-linux-arm").write_bytes(b"unverified")
                with self.assertRaisesRegex(ValueError, "provenance"):
                    release.assemble(tag, assets, "b" * 40)
