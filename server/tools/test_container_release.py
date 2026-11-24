import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import container_release as images


class ContainerReleaseTest(unittest.TestCase):
    def test_only_published_commit_receives_stable_tags(self):
        stable, dev = "1" * 40, "2" * 40
        self.assertEqual(images.select("", dev, "v1.0.0", stable), (dev, ""))
        self.assertEqual(images.select("", stable, "v1.0.0", stable), (stable, "v1.0.0"))
        self.assertEqual(images.select("v1.0.0", "", "v1.0.0", stable), (stable, "v1.0.0"))
        with self.assertRaises(ValueError):
            images.select("v0.9.0", "", "v1.0.0", stable)

    def test_archive_and_public_downloads_identify_same_source_and_binaries(self):
        commit = "1" * 40
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            entries, assets = {}, {}
            for host in images.LINUX:
                name = "picobrew-hub-" + host
                data = host.encode()
                (root / name).write_bytes(data)
                sha = hashlib.sha256(data).hexdigest()
                entries[host] = {"name": name, "bytes": len(data), "sha256": sha}
                assets[name] = "sha256:" + sha
            manifest = root / "releases.json"
            manifest.write_text(json.dumps({"tag": "v1.0.0", "source_commit": commit, "hubs": entries}))
            assets[manifest.name] = "sha256:" + images.digest(manifest)
            out = root / "out"
            images.copy_binaries(root, out, "v1.0.0", commit, assets)
            for item in entries.values():
                self.assertEqual((out / item["name"]).read_bytes(), (root / item["name"]).read_bytes())
            with self.assertRaisesRegex(ValueError, "revision"):
                images.copy_binaries(root, out, "v1.0.0", "2" * 40, assets)
            with self.assertRaisesRegex(ValueError, "Public download"):
                images.copy_binaries(root, out, "v1.0.0", commit, {**assets, "picobrew-hub-linux-arm": "bad"})
            (root / "picobrew-hub-linux-arm").write_bytes(b"damaged")
            with self.assertRaisesRegex(ValueError, "executable differs"):
                images.copy_binaries(root, out, "v1.0.0", commit)
