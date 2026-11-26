"""Promote an assembled v1.0.0 after CI, preserving the previous downloads until verification."""

import argparse
from dataclasses import asdict, dataclass
import hashlib
import json
from pathlib import Path
import re
import subprocess

from release import AVR, sha, source_tag

REPO = "jeffreydwalter/picobrew"
REMOTE = "https://github.com/" + REPO + ".git"
TAG = "v1.0.0"
SHA = re.compile(r"[0-9a-f]{40}")
MARKER = "<!-- picobrew-rolling-release "
CHECKSUMS = "SHA256SUMS.txt"
MANIFEST = "releases.json"


class Superseded(Exception):
    """A newer master will publish its own verified artifacts."""


def asset_names():
    names = {CHECKSUMS, MANIFEST, "usb-programmer-sources.zip"}
    for platform in AVR:
        suffix = platform + (".exe" if platform.startswith("windows") else "")
        names.update(("picobrew-hub-" + suffix, "picobrew-usb-" + suffix,
                      platform + ".json", f"usb-notices-{platform}.zip"))
    return names


def verify_indexes(commit, inventory, manifest_bytes, checksum_bytes):
    """Match both attached indexes to all actual files or GitHub-provided asset digests."""
    if set(inventory) != asset_names():
        raise ValueError("Incomplete or unexpected release assets")
    for name, data in ((MANIFEST, manifest_bytes), (CHECKSUMS, checksum_bytes)):
        if inventory[name] != (len(data), hashlib.sha256(data).hexdigest()):
            raise ValueError(f"Release index digest differs: {name}")
    manifest = json.loads(manifest_bytes)
    if (manifest["tag"], manifest["source_commit"]) != (TAG, commit):
        raise ValueError("Release manifest identifies another version or source")
    wanted = {name: digest for name, (_, digest) in inventory.items() if name != CHECKSUMS}
    checksums = {}
    for line in checksum_bytes.decode("ascii").splitlines():
        digest, name = line.split("  ", 1)
        if name in checksums:
            raise ValueError("Duplicate checksum entry")
        checksums[name] = digest
    if checksums != wanted:
        raise ValueError("SHA256SUMS differs from release assets")
    for key, prefix in (("assets", "picobrew-usb-"), ("hubs", "picobrew-hub-")):
        if set(manifest[key]) != set(AVR):
            raise ValueError("Release manifest is missing a platform")
        for platform, item in manifest[key].items():
            name = prefix + platform + (".exe" if platform.startswith("windows") else "")
            if item["name"] != name or (item["bytes"], item["sha256"]) != inventory[name]:
                raise ValueError(f"Release executable differs from manifest: {name}")


def local_inventory(directory, commit):
    inventory = {}
    for file in directory.iterdir():
        if file.is_symlink() or not file.is_file():
            raise ValueError(f"Unexpected release entry: {file.name}")
        inventory[file.name] = (file.stat().st_size, sha(file))
    verify_indexes(commit, inventory, (directory / MANIFEST).read_bytes(), (directory / CHECKSUMS).read_bytes())
    return inventory


def assets(release):
    rows = {}
    for asset in release["assets"]:
        name = asset["name"]
        digest = asset.get("digest") or ""
        if name in rows or asset["state"] != "uploaded" or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            raise ValueError(f"Missing, duplicated or unfinished release digest: {name}")
        rows[name] = (asset["size"], digest.removeprefix("sha256:"))
    return rows


class GitHub:
    def api(self, suffix, method="GET", payload=None):
        cmd = ["gh", "api", f"repos/{REPO}/{suffix}", "-X", method]
        if payload is not None:
            cmd.extend(("--input", "-"))
        data = subprocess.check_output(cmd, input=json.dumps(payload).encode() if payload is not None else None)
        return json.loads(data) if data.strip() else None

    def releases(self):
        pages = json.loads(subprocess.check_output(
            ["gh", "api", "--paginate", "--slurp", f"repos/{REPO}/releases?per_page=100"]))
        return [r for page in pages for r in page]

    def edit(self, release_id, **payload):
        return self.api(f"releases/{release_id}", "PATCH", payload)

    def delete_release(self, release_id):
        self.api(f"releases/{release_id}", "DELETE")

    def create(self, **payload):
        return self.api("releases", "POST", payload)

    def upload(self, tag, files):
        subprocess.run(["gh", "release", "upload", tag, "--repo", REPO, "--clobber", *files], check=True)

    def latest(self):
        return self.api("releases/latest")["id"]

    def read_asset(self, release, name):
        entry = next(a for a in release["assets"] if a["name"] == name)
        return subprocess.check_output(["gh", "api", "-H", "Accept: application/octet-stream",
                                       f"repos/{REPO}/releases/assets/{entry['id']}"])

    def head(self):
        return self.api("git/ref/heads/master")["object"]["sha"]

    def tag(self, tag):
        ref = f"refs/tags/{tag}"
        output = subprocess.check_output(["git", "ls-remote", REMOTE, ref, ref + "^{}"], text=True)
        refs = {name: oid for oid, name in (line.split() for line in output.splitlines())}
        return refs.get(ref, ""), refs.get(ref + "^{}", refs.get(ref, ""))

    def move(self, tag, before, after):
        # A concurrent tag edit cannot be overwritten, including during rollback.
        ref = f"refs/tags/{tag}"
        subprocess.run(["git", "push", "--force-with-lease=" + ref + ":" + before,
                        REMOTE, after + ":" + ref], check=True)


@dataclass(frozen=True)
class Rotation:
    commit: str
    old_id: int
    old_commit: str
    old_ref: str

    @property
    def stage(self):
        return "hub-build-" + self.commit

    @property
    def backup(self):
        return f"hub-previous-{self.old_id}-{self.commit}"

    def notes(self, text):
        return text.rstrip() + "\n\n" + MARKER + json.dumps(asdict(self), sort_keys=True) + " -->"

    @classmethod
    def from_release(cls, release):
        line = (release.get("body") or "").rstrip().split("\n")[-1]
        if not (line.startswith(MARKER) and line.endswith(" -->")):
            return None
        plan = cls(**json.loads(line[len(MARKER):-4]))
        if (not SHA.fullmatch(plan.commit) or plan.commit != release["target_commitish"]
                or type(plan.old_id) is not int or plan.old_id < 0
                or (plan.old_ref or plan.old_commit or plan.old_id)
                and not (SHA.fullmatch(plan.old_ref) and SHA.fullmatch(plan.old_commit))):
            raise ValueError("Invalid managed release metadata")
        return plan


def public_verified(gh, current, commit, local=None):
    if (not current or current["tag_name"] != TAG or current["draft"] or current["prerelease"]
            or current["target_commitish"] != commit or gh.tag(TAG)[1] != commit or gh.latest() != current["id"]):
        raise ValueError("Latest public release and Git tag do not match")
    inventory = assets(current)
    verify_indexes(commit, inventory, gh.read_asset(current, MANIFEST), gh.read_asset(current, CHECKSUMS))
    if local is not None:
        for name in inventory:
            if name.startswith(("picobrew-usb-", "picobrew-hub-")) and inventory[name] != local[name]:
                raise ValueError(f"Published binary differs from the verified build: {name}")


def by_tag(rows, tag):
    matches = [r for r in rows if r["tag_name"] == tag]
    if len(matches) > 1:
        raise ValueError(f"Multiple releases claim {tag}")
    return next(iter(matches), None)


def remove_backup(gh, plan):
    if not plan or not plan.old_id:
        return
    old = by_tag(gh.releases(), plan.backup)
    ref, _ = gh.tag(plan.backup)
    if (old or ref) and ref != plan.old_ref:
        raise ValueError("Rollback tag changed")
    if old:
        if old["id"] != plan.old_id or old["target_commitish"] != plan.old_commit:
            raise ValueError("Unexpected release occupies the rollback tag")
        gh.delete_release(old["id"])
    if ref:
        gh.move(plan.backup, ref, "")


def restore(gh, staged_id, plan):
    """Undo a partial swap only while our replacement is still a draft."""
    rows = gh.releases()
    candidate = next((r for r in rows if r["id"] == staged_id), None)
    if not candidate or not candidate["draft"] or candidate["tag_name"] != plan.stage:
        return
    old = next((r for r in rows if r["id"] == plan.old_id), None)
    if plan.old_id and (not old or old["tag_name"] not in (TAG, plan.backup)):
        raise ValueError("Previous release is no longer at its recorded tag")
    current = by_tag(rows, TAG)
    if current and current["id"] != plan.old_id:
        raise ValueError("Another release already occupies v1.0.0")
    ref, _ = gh.tag(TAG)
    if ref != plan.old_ref:
        if ref != plan.commit:
            raise ValueError("Public tag changed during recovery")
        gh.move(TAG, ref, plan.old_ref)
    if old and old["tag_name"] == plan.backup:
        gh.edit(old["id"], tag_name=TAG, target_commitish=plan.old_commit, make_latest="true")
    if plan.old_id and gh.tag(plan.backup)[0]:
        # The old release is back under its own name before removing the spare tag.
        public_verified(gh, by_tag(gh.releases(), TAG), plan.old_commit)
        remove_backup(gh, plan)


def promote(gh, commit, directory, notes):
    inventory = local_inventory(directory, commit)
    def check_master():
        if gh.head() != commit:
            raise Superseded()

    check_master()
    rows = gh.releases()
    for r in rows:
        plan = Rotation.from_release(r)
        if r["draft"] and plan and r["tag_name"] == plan.stage and plan.commit != commit:
            restore(gh, r["id"], plan)
            gh.delete_release(r["id"])
    rows = gh.releases()
    current = by_tag(rows, TAG)
    if current and current["target_commitish"] == commit and not current["draft"]:
        public_verified(gh, current, commit, inventory)
        remove_backup(gh, Rotation.from_release(current))
        return
    stage = "hub-build-" + commit
    candidate = by_tag(rows, stage)
    if candidate:
        plan = Rotation.from_release(candidate)
        if not candidate["draft"] or not plan or plan.commit != commit:
            raise ValueError("Temporary release name is already in use")
    else:
        old_ref, old_commit = gh.tag(TAG)
        if current:
            public_verified(gh, current, old_commit)
            if current.get("immutable"):
                raise ValueError("GitHub marked the existing release immutable")
            remove_backup(gh, Rotation.from_release(current))
        elif old_ref and old_commit != commit:
            raise ValueError("Existing source tag has no corresponding public release")
        plan = Rotation(commit, current["id"] if current else 0, old_commit, old_ref)
        if gh.tag(stage)[0] or by_tag(rows, plan.backup) or gh.tag(plan.backup)[0]:
            raise ValueError("A temporary release reference is already occupied")
        candidate = gh.create(tag_name=stage, name="PicoBrew Hub 1.0.0", draft=True,
                              target_commitish=commit, body=plan.notes(notes))

    # Replace incomplete or interrupted uploads only on the managed private draft.
    rows = candidate["assets"]
    if any(a["name"] not in inventory for a in rows):
        raise ValueError("Unexpected file on staged release")
    ready = {a["name"] for a in rows if a["state"] == "uploaded"
             and (a["size"], a.get("digest")) == (inventory[a["name"]][0], "sha256:" + inventory[a["name"]][1])}
    files = [directory / name for name in sorted(inventory.keys() - ready)]
    if files:
        gh.upload(stage, files)
    candidate = by_tag(gh.releases(), stage)
    if not candidate or not candidate["draft"] or Rotation.from_release(candidate) != plan or assets(candidate) != inventory:
        raise ValueError("Staged downloads do not match the assembled release")
    try:
        check_master()
        if plan.old_id:
            old = next((r for r in gh.releases() if r["id"] == plan.old_id), None)
            if not old or old["target_commitish"] != plan.old_commit or old["tag_name"] not in (TAG, plan.backup):
                raise ValueError("Previous release changed during staging")
            backup_ref, _ = gh.tag(plan.backup)
            if backup_ref and backup_ref != plan.old_ref:
                raise ValueError("Unexpected rollback tag")
            if not backup_ref:
                if gh.tag(TAG)[0] != plan.old_ref:
                    raise ValueError("Public tag changed during staging")
                gh.move(plan.backup, "", plan.old_ref)
            if old["tag_name"] == TAG:
                if gh.tag(TAG)[0] != plan.old_ref:
                    raise ValueError("Public tag changed during staging")
                gh.edit(old["id"], tag_name=plan.backup, target_commitish=plan.old_commit)
        ref, _ = gh.tag(TAG)
        if ref != commit:
            if ref != plan.old_ref:
                raise ValueError("Public tag changed during staging")
            gh.move(TAG, ref, commit)
        check_master()
        published = gh.edit(candidate["id"], tag_name=TAG, target_commitish=commit, draft=False, make_latest="true")
        public_verified(gh, published, commit, inventory)
        if assets(published) != inventory:
            raise ValueError("Published assets differ from the prepared draft")
    except Exception:
        restore(gh, candidate["id"], plan)
        raise
    remove_backup(gh, plan)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--artifacts", type=Path, required=True)
    parser.add_argument("--notes", type=Path, required=True)
    args = parser.parse_args()
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if not SHA.fullmatch(args.commit) or args.commit != head or source_tag() != TAG:
        raise ValueError("Expected the exact v1.0.0 source checkout")
    try:
        promote(GitHub(), args.commit, args.artifacts, args.notes.read_text(encoding="utf-8"))
    except Superseded:
        print("A newer master revision superseded this build.")


if __name__ == "__main__":
    main()
