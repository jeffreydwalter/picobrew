"""Select and verify the three native Hub binaries for a multi-architecture image."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

REPO = "jeffreydwalter/picobrew"
TAG = re.compile(r"v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)")
SHA = re.compile(r"[0-9a-f]{40}")
LINUX = ("linux-amd64", "linux-arm64", "linux-arm")


def api(path):
    return json.loads(subprocess.check_output(["gh", "api", f"repos/{REPO}/{path}"]))


def public_release():
    release = api("releases/latest")
    tag = release["tag_name"]
    if release["draft"] or release["prerelease"] or not TAG.fullmatch(tag):
        raise ValueError("Expected a published stable Hub release")
    # Peel an annotated tag as well as accepting a lightweight release tag.
    ref = "refs/tags/" + tag
    output = subprocess.check_output(["git", "ls-remote", "origin", ref, ref + "^{}"], text=True)
    refs = {name: sha for sha, name in (line.split() for line in output.splitlines())}
    commit = refs.get(ref + "^{}", refs.get(ref, ""))
    if not SHA.fullmatch(commit):
        raise ValueError("Published release has no Git source tag")
    target = release["target_commitish"]
    if SHA.fullmatch(target) and target != commit:
        raise ValueError("Published release and Git tag identify different revisions")
    return release, commit


def select(requested_tag, run_commit, tag, released_commit):
    if requested_tag:
        if requested_tag != tag:
            raise ValueError("Choose the current published release to update the stable container tags")
        return released_commit, tag
    if not SHA.fullmatch(run_commit):
        raise ValueError("Missing verified master workflow commit")
    return run_commit, tag if run_commit == released_commit else ""


def copy_binaries(directory, destination, tag, commit, public_assets=None):
    manifest_file = directory / "releases.json"
    manifest = json.loads(manifest_file.read_bytes())
    if manifest["source_commit"] != commit or manifest["tag"] != tag:
        raise ValueError("The Hub release manifest identifies a different revision")
    if public_assets is not None and public_assets.get("releases.json") != "sha256:" + digest(manifest_file):
        raise ValueError("Public release manifest digest differs")
    destination.mkdir(parents=True, exist_ok=True)
    for platform in LINUX:
        name = "picobrew-hub-" + platform
        path = directory / name
        entry = manifest["hubs"][platform]
        if entry["name"] != name or path.stat().st_size != entry["bytes"] or digest(path) != entry["sha256"]:
            raise ValueError("Hub executable differs from the release manifest: " + name)
        if public_assets is not None and public_assets.get(name) != "sha256:" + entry["sha256"]:
            raise ValueError("Public download digest differs: " + name)
        shutil.copyfile(path, destination / name)


def digest(path):
    with path.open("rb") as src:
        return hashlib.file_digest(src, "sha256").hexdigest()


def prepare(args):
    release, released_commit = public_release()
    commit, stable_tag = select(args.release_tag, args.commit, release["tag_name"], released_commit)
    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        public_assets = None
        if stable_tag:
            source_tag = stable_tag
            public_assets = {entry["name"]: entry["digest"] for entry in release["assets"]}
            subprocess.run(["gh", "release", "download", stable_tag, "--repo", REPO, "--dir", root,
                            "--pattern", "releases.json", "--pattern", "picobrew-hub-linux-*"], check=True)
        else:
            if not re.fullmatch(r"[1-9]\d*", args.run_id):
                raise ValueError("Missing packaging workflow run")
            artifacts = api(f"actions/runs/{args.run_id}/artifacts?per_page=100")["artifacts"]
            prefix = "hub-release-"
            suffix = "-" + commit
            names = [item["name"] for item in artifacts if not item["expired"] and
                     item["name"].startswith(prefix) and item["name"].endswith(suffix)]
            if not names and api("git/ref/heads/master")["object"]["sha"] != commit:
                # A superseded packaging run deliberately omits its combined release artifact.
                return
            if len(names) != 1 or not TAG.fullmatch(source_tag := names[0][len(prefix):-len(suffix)]):
                raise ValueError("Missing verified Hub release artifact for this revision")
            subprocess.run(["gh", "run", "download", args.run_id, "--repo", REPO, "--name", names[0],
                            "--dir", root], check=True)
        copy_binaries(root, args.destination, source_tag, commit, public_assets)
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as out:
        print(f"commit={commit}\nrelease_tag={stable_tag}", file=out)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    commands = p.add_subparsers(dest="action", required=True)
    prep = commands.add_parser("prepare")
    prep.add_argument("--commit", default="")
    prep.add_argument("--run-id", default="")
    prep.add_argument("--release-tag", default="")
    prep.add_argument("--destination", required=True, type=Path)
    promote = commands.add_parser("stable-tags")
    promote.add_argument("commit")
    promote.add_argument("tag")
    args = p.parse_args()
    if args.action == "prepare":
        prepare(args)
    else:
        # Recheck immediately before moving a public alias; an older workflow
        # can finish after a release has been replaced or superseded.
        release, commit = public_release()
        if (args.tag, args.commit) != (release["tag_name"], commit):
            raise ValueError("The stable release changed during the build")
        version = args.tag[1:]
        print("\n".join(("latest", args.tag, version, version.rsplit(".", 1)[0])))


if __name__ == "__main__":
    main()
