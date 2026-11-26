import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import rolling_release as release


OLD = "a" * 40
NEW = "b" * 40
LATER = "c" * 40


def write_package(root, commit, archive=b"source archive"):
    root.mkdir(exist_ok=True)
    manifest = {"tag": release.TAG, "source_commit": commit, "assets": {}, "hubs": {}}
    files = {name: name.encode() for name in release.asset_names()}
    files["usb-programmer-sources.zip"] = archive
    for key, prefix in (("assets", "picobrew-usb-"), ("hubs", "picobrew-hub-")):
        for platform in release.AVR:
            name = prefix + platform + (".exe" if platform.startswith("windows") else "")
            data = (commit + name).encode()
            files[name] = data
            manifest[key][platform] = {"name": name, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()}
    files[release.MANIFEST] = json.dumps(manifest).encode()
    files[release.CHECKSUMS] = "".join(f"{hashlib.sha256(b).hexdigest()}  {name}\n"
        for name, b in sorted(files.items()) if name != release.CHECKSUMS).encode()
    for name, data in files.items():
        (root / name).write_bytes(data)


class Remote:
    """Small API/refs double. `stop` can interrupt immediately after a durable operation."""

    def __init__(self):
        self.master = NEW
        self.rows = {}
        self.tags = {"unrelated": LATER}
        self.latest_id = None
        self.files = {}
        self.next_id = 1
        self.stop = lambda *_: None

    def head(self):
        return self.master

    def tag(self, name):
        ref = self.tags.get(name, "")
        return ref, ref

    def move(self, tag, before, after):
        assert self.tags.get(tag, "") == before
        if after:
            self.tags[tag] = after
        else:
            del self.tags[tag]
        self.stop("move", tag)

    def releases(self):
        return copy.deepcopy(list(self.rows.values()))

    def create(self, **payload):
        n = self.next_id
        self.next_id += 1
        self.rows[n] = {"id": n, "prerelease": False, "assets": [], **payload}
        self.files[n] = {}
        return copy.deepcopy(self.rows[n])

    def edit(self, n, **payload):
        tag = payload["tag_name"]
        assert not any(r["tag_name"] == tag for r in self.rows.values() if r["id"] != n)
        if not payload.get("draft", self.rows[n]["draft"]):
            assert self.tags[tag] == payload["target_commitish"]
        latest = payload.pop("make_latest", None)
        if latest == "true":
            self.latest_id = n
        self.rows[n].update(payload)
        self.stop("edit", tag)
        return copy.deepcopy(self.rows[n])

    def upload(self, tag, paths):
        r = release.by_tag(self.rows.values(), tag)
        for path in paths:
            data = path.read_bytes()
            self.files[r["id"]][path.name] = data
            r["assets"] = [a for a in r["assets"] if a["name"] != path.name]
            r["assets"].append({"name": path.name, "size": len(data), "state": "uploaded",
                                "digest": "sha256:" + hashlib.sha256(data).hexdigest()})
        self.stop("upload", tag)

    def read_asset(self, r, name):
        return self.files[r["id"]][name]

    def latest(self):
        return self.latest_id

    def delete_release(self, n):
        del self.rows[n]
        del self.files[n]
        self.stop("delete", n)


class RollingReleaseTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.directory = self.root / "new"
        self.gh = Remote()
        old = self.root / "old"
        write_package(old, OLD)
        write_package(self.directory, NEW)
        self.gh.tags[release.TAG] = OLD
        self.old = self.gh.create(tag_name=release.TAG, target_commitish=OLD, body="Original notes", draft=False)["id"]
        self.gh.latest_id = self.old
        self.gh.upload(release.TAG, old.iterdir())

    def publish(self, commit=NEW):
        release.promote(self.gh, commit, self.directory, "Release notes")

    def test_verified_swap_and_rebuilt_same_commit(self):
        self.publish()
        saved = self.gh.releases()
        self.assertEqual(len(saved), 1)
        self.assertEqual(self.gh.tags, {"unrelated": LATER, release.TAG: NEW})
        self.assertEqual(self.gh.latest_id, saved[0]["id"])
        self.assertNotEqual(self.gh.latest_id, self.old)
        self.assertEqual(len(saved[0]["assets"]), 27)
        write_package(self.directory, NEW, archive=b"different archive timestamps")
        self.publish()
        self.assertEqual(self.gh.releases(), saved)

    def test_failure_after_tag_move_restores_old_downloads(self):
        def fail(op, name):
            if op == "move" and name == release.TAG and self.gh.tags[release.TAG] == NEW:
                raise RuntimeError("tag move reply lost")
        self.gh.stop = fail
        with self.assertRaisesRegex(RuntimeError, "reply lost"):
            self.publish()
        restored = release.by_tag(self.gh.releases(), release.TAG)
        self.assertEqual(restored["id"], self.old)
        self.assertEqual(self.gh.tags[release.TAG], OLD)
        self.assertEqual(restored["body"], "Original notes")
        self.assertEqual(self.gh.latest_id, self.old)
        self.gh.stop = lambda *_: None
        self.publish()
        self.assertEqual(self.gh.tags, {"unrelated": LATER, release.TAG: NEW})

    def test_job_kill_can_resume_or_publish_superseding_master(self):
        def stop(op, name):
            if op == "move" and name == release.TAG:
                raise KeyboardInterrupt()
        self.gh.stop = stop
        with self.assertRaises(KeyboardInterrupt):
            self.publish()
        self.assertIn(self.old, self.gh.rows)
        self.gh.stop = lambda *_: None
        self.gh.master = LATER
        write_package(self.directory, LATER)
        self.publish(LATER)
        self.assertEqual(self.gh.tags, {"unrelated": LATER, release.TAG: LATER})
        self.assertEqual(len(self.gh.rows), 1)

    def test_interrupted_cleanup_resumes_after_publication(self):
        def stop(op, name):
            if op == "edit" and name == release.TAG:
                raise KeyboardInterrupt()
        self.gh.stop = stop
        with self.assertRaises(KeyboardInterrupt):
            self.publish()
        self.assertIn(self.old, self.gh.rows)
        self.assertEqual(self.gh.tags[release.TAG], NEW)
        self.gh.stop = lambda *_: None
        self.publish()
        self.assertNotIn(self.old, self.gh.rows)
        self.assertEqual(len(self.gh.rows), 1)

    def test_superseded_draft_preserves_existing_release(self):
        self.gh.stop = lambda op, name: setattr(self.gh, "master", LATER) if op == "upload" else None
        with self.assertRaises(release.Superseded):
            self.publish()
        self.assertEqual(self.gh.tags[release.TAG], OLD)
        self.assertEqual(self.gh.latest_id, self.old)

    def test_corrupt_files_and_unrelated_tag_are_rejected(self):
        (self.directory / "picobrew-hub-linux-arm").write_bytes(b"bad")
        with self.assertRaises(ValueError):
            self.publish()
        self.assertEqual(list(self.gh.rows), [self.old])
        write_package(self.directory, NEW)
        colliding = release.Rotation(NEW, self.old, OLD, OLD).backup
        self.gh.tags[colliding] = OLD
        with self.assertRaisesRegex(ValueError, "occupied"):
            self.publish()
        self.assertIn(colliding, self.gh.tags)
        self.assertEqual(list(self.gh.rows), [self.old])

    def test_bad_remote_inventory_stops_before_rotating_or_cleaning(self):
        def corrupt_stage(op, name):
            if op == "upload":
                stage = release.by_tag(self.gh.rows.values(), name)
                stage["assets"][0]["digest"] = "sha256:" + "f" * 64
        self.gh.stop = corrupt_stage
        with self.assertRaisesRegex(ValueError, "Staged downloads"):
            self.publish()
        self.assertEqual(self.gh.tags, {"unrelated": LATER, release.TAG: OLD})
        self.assertEqual(self.gh.latest_id, self.old)
        self.gh.stop = lambda *_: None
        self.publish()

    def test_external_public_tag_edit_during_upload_preserves_old_release_name(self):
        def concurrent_change(op, name):
            if op == "upload":
                self.gh.tags[release.TAG] = LATER
        self.gh.stop = concurrent_change
        with self.assertRaisesRegex(ValueError, "Public tag changed"):
            self.publish()
        self.assertEqual(self.gh.tags, {"unrelated": LATER, release.TAG: LATER})
        self.assertEqual(release.by_tag(self.gh.releases(), release.TAG)["id"], self.old)
        self.assertEqual(self.gh.latest_id, self.old)

    def test_cleanup_keeps_previous_downloads_if_rollback_ref_changes(self):
        backup = release.Rotation(NEW, self.old, OLD, OLD).backup
        def concurrent_change(op, name):
            if op == "edit" and name == release.TAG:
                self.gh.tags[backup] = LATER
        self.gh.stop = concurrent_change
        with self.assertRaisesRegex(ValueError, "Rollback tag changed"):
            self.publish()
        self.assertEqual(self.gh.tags[backup], LATER)
        self.assertEqual(self.gh.rows[self.old]["tag_name"], backup)
        self.assertEqual(len(self.gh.rows[self.old]["assets"]), 27)

    def test_fresh_install_and_existing_tag_without_release(self):
        self.gh = Remote()
        self.publish()
        self.assertEqual(self.gh.tags[release.TAG], NEW)
        self.gh = Remote()
        self.gh.tags[release.TAG] = OLD
        with self.assertRaisesRegex(ValueError, "no corresponding public release"):
            self.publish()

    def test_ambiguous_existing_release_digest_prevents_changes(self):
        self.gh.rows[self.old]["assets"].pop()
        with self.assertRaises(ValueError):
            self.publish()
        self.assertEqual(self.gh.tags, {"unrelated": LATER, release.TAG: OLD})
        self.assertEqual(self.gh.latest_id, self.old)


if __name__ == "__main__":
    unittest.main()
