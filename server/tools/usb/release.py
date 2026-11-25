"""Assemble and verify the six host workers for a tagged public release."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import time
import urllib.error
import urllib.request
import zipfile

from build_worker import AVR, AVR_RELEASE, AVR_SOURCE, ROOT, sha, worker_inputs

SOURCES = {
    AVR_SOURCE[0]: (AVR_RELEASE + AVR_SOURCE[0], AVR_SOURCE[1]),
    "cc3200tool-c233289.zip": (
        "https://github.com/toniebox-reverse-engineering/cc3200tool/archive/c2332898a1ededc0bdd01ec279b23366d213f81e.zip",
        "17c7d1abb8cfb90bbf3189cd64c51ab4b06bca36547c51f1f96d4d551da6d097",
    ),
    "libusb-package-1.0.30.1.tar.gz": (
        "https://github.com/pyocd/libusb-package/archive/09711f4056efd55f5591ecfad1826c83b411c7c2.tar.gz",
        "4d3921a0c303f078b014ef73fc221a2a5a003375627dd298257a0bd7190e2760",
    ),
    "libusb-1.0.30.tar.gz": (
        "https://github.com/libusb/libusb/archive/87a55632db62c9bdc58cd31d3ccfa673f1bb017f.tar.gz",
        "26733c3eee2475c062e14f80a62ece4b33ee3b33fc0c88b2758964412a42b7e0",
    ),
    "esptool-4.11.0.tar.gz": (
        "https://files.pythonhosted.org/packages/22/74/fa0a4ff680d4b299135abc1ca06c0b8b58451adff9a0d4461ce62b03af96/esptool-4.11.0.tar.gz",
        "496571e4f6e36f7dc9a730dd485c4a9d522c9e7d6bb90ea2fec0a049275fbfad",
    ),
    "pyusb-1.3.1.tar.gz": (
        "https://files.pythonhosted.org/packages/00/6b/ce3727395e52b7b76dfcf0c665e37d223b680b9becc60710d4bc08b7b7cb/pyusb-1.3.1.tar.gz",
        "3af070b607467c1c164f49d5b0caabe8ac78dbed9298d703a8dbf9df4052d17e",
    ),
    "pyserial-3.5.tar.gz": (
        "https://files.pythonhosted.org/packages/1e/7d/ae3f0a63f41e4d2f6cb66a5b57197850f919f59e558159a4dd3a818f5082/pyserial-3.5.tar.gz",
        "3c77e014170dfffbd816e6ffc205e9842efb10be9f58ec16d3e8675b4925cddb",
    ),
    "pyinstaller-6.22.3.tar.gz": (
        "https://files.pythonhosted.org/packages/63/41/f90302845945abd4ed647933ff5ee7c6ac93983187be67f897b6cb613331/pyinstaller-6.22.3.tar.gz",
        "05eb2f5615503e72939a7224d68b4aff572c6b0438ee4a17d0a4b481f399362d",
    ),
}


TAG = re.compile(r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)")
CANDIDATE = re.compile(r"usb-candidate-[1-9]\d*-[1-9]\d*")
PINS = ROOT / "server/tools/usb-prepare/releases.json"
RELEASE_BASE = "https://github.com/jeffreydwalter/picobrew/releases/download/"


def source_tag():
    source = (ROOT / "server/internal/server/server.go").read_text(encoding="utf-8")
    versions = re.findall(r'^const Version = "([^"]+)"$', source, flags=re.MULTILINE)
    if len(versions) != 1 or not TAG.fullmatch("v" + versions[0]):
        raise ValueError("Expected one semantic Hub release version in server.go")
    return "v" + versions[0]


REQUIRED_WORKFLOWS = {
    "server.yml", "repository.yml", "standalone.yml", "recovery.yml",
    "oem-reconstruction.yml", "pico-c-reconstruction.yml", "pico-c-standalone.yml",
    "standalone-reference.yml", "zseries-reconstruction.yml", "zymatic.yml",
}


def pending_workflows(runs):
    latest = {}
    for run in sorted(runs, key=lambda item: (item["id"], item["run_attempt"])):
        name = Path(run["path"]).name
        if name != "hub-usb-tools.yml":
            latest[name] = run
    for name, run in latest.items():
        if run["status"] == "completed" and run["conclusion"] != "success":
            raise ValueError(f"{name} finished with {run['conclusion']}")
    return (REQUIRED_WORKFLOWS - latest.keys()) | {
        name for name, run in latest.items() if run["status"] != "completed"
    }


def wait_for_ci(commit):
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("Invalid source commit")
    repo = "repos/jeffreydwalter/picobrew"
    deadline = time.monotonic() + 3600
    while time.monotonic() < deadline:
        head = subprocess.check_output(
            ["gh", "api", repo + "/git/ref/heads/master", "--jq", ".object.sha"], text=True
        ).strip()
        if head != commit:
            return "superseded"
        data = subprocess.check_output(["gh", "api", repo + "/actions/runs"
                f"?head_sha={commit}&branch=master&event=push&per_page=100"], text=True)
        response = json.loads(data)
        if response["total_count"] > len(response["workflow_runs"]):
            raise ValueError("Cannot verify all workflow results for the release commit")
        if not pending_workflows(response["workflow_runs"]):
            return "ready"
        time.sleep(20)
    raise TimeoutError("Release commit still has unfinished or missing CI")


def workers(directory):
    assets = {}
    inputs = worker_inputs()
    for key in sorted(AVR):
        asset = json.loads((directory / (key + ".json")).read_text())
        name = "picobrew-usb-" + key + (".exe" if key.startswith("windows") else "")
        worker = directory / name
        notices = directory / f"usb-notices-{key}.zip"
        if (
            asset["platform"] != key or asset["name"] != name
            or asset["bytes"] != worker.stat().st_size or asset["sha256"] != sha(worker)
            or asset.get("inputs_sha256") != inputs
            or asset.get("notices_sha256") != sha(notices)
        ):
            raise ValueError("USB artifact differs from this checkout or its provenance: " + key)
        with zipfile.ZipFile(notices) as archive:
            required = {"versions.json", "PicoBrew-LICENSES.md", "PicoBrew-LICENSE.txt", "Hub-LICENSE.txt", "AVRDUDE-LICENSE.txt"}
            if archive.testzip() is not None or not required.issubset(archive.namelist()):
                raise ValueError("Invalid USB notices for " + key)
        assets[key] = asset
    return assets


def pin(tag, candidate, directory):
    if tag != source_tag() or not CANDIDATE.fullmatch(candidate):
        raise ValueError("Invalid production or candidate release tag")
    pins = {"tag": tag, "candidate_tag": candidate, "assets": workers(directory)}
    # During a coordinated replacement the old production URL still resolves,
    # but its worker hashes can differ from the newly reviewed source pins.
    if PINS.exists() and json.loads(PINS.read_text())["tag"] == tag:
        pins["prefer_candidate"] = True
    PINS.write_text(json.dumps(pins, indent=2) + "\n", encoding="utf-8")


def current_pins():
    """A reviewed pin may be reused while every worker input still matches."""
    pins = json.loads(PINS.read_text())
    if not TAG.fullmatch(pins["tag"]) or not CANDIDATE.fullmatch(pins.get("candidate_tag", "")):
        return False
    assets = pins["assets"]
    digest = worker_inputs()
    return set(assets) == set(AVR) and all(asset.get("inputs_sha256") == digest for asset in assets.values())


def stage(key, directory, release_tag):
    pins = json.loads(PINS.read_text())
    tag, candidate = pins["tag"], pins.get("candidate_tag", "")
    if not TAG.fullmatch(tag) or not CANDIDATE.fullmatch(candidate):
        raise ValueError("Pin six current USB candidates before releasing")
    asset = pins["assets"][key]
    expected_name = "picobrew-usb-" + key + (".exe" if key.startswith("windows") else "")
    if (asset["name"] != expected_name or asset["platform"] != key or
            asset["inputs_sha256"] != worker_inputs()):
        raise ValueError("USB inputs changed. Build and pin new release candidates.")
    directory.mkdir(exist_ok=True, parents=True)
    tags = [tag]
    if tag == release_tag:
        tags = [candidate, tag] if pins.get("prefer_candidate") else [tag, candidate]
    for name, expected in [(expected_name, asset["sha256"]), (f"usb-notices-{key}.zip", asset["notices_sha256"])]:
        for index, asset_tag in enumerate(tags):
            try:
                response = urllib.request.urlopen(RELEASE_BASE + asset_tag + "/" + name, timeout=180)
                break
            except urllib.error.HTTPError as error:
                error.close()
                if error.code != 404 or index + 1 == len(tags):
                    raise
        with response, (directory / name).open("wb") as out:
            shutil.copyfileobj(response, out)
        if sha(directory / name) != expected:
            raise ValueError("Downloaded release asset differs from its source pin: " + name)
    (directory / (key + ".json")).write_text(json.dumps(asset, indent=2) + "\n", encoding="utf-8")


def assemble(tag, directory, commit):
    if tag != source_tag() or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("Invalid production release tag or commit")
    assets = workers(directory)
    if assets != json.loads(PINS.read_text())["assets"]:
        raise ValueError("Released workers must match the source bootstrap checksums")
    hubs = {}
    for key, asset in assets.items():
        hub = directory / asset["name"].replace("picobrew-usb-", "picobrew-hub-", 1)
        if not hub.is_file() or hub.stat().st_size < asset["bytes"]:
            raise ValueError("Missing USB-enabled Hub for " + key)
        hubs[key] = {"name": hub.name, "bytes": hub.stat().st_size, "sha256": sha(hub)}
    manifest = {"tag": tag, "source_commit": commit, "assets": assets, "hubs": hubs}
    (directory / "releases.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    with zipfile.ZipFile(directory / "usb-programmer-sources.zip", "w") as archive:
        for name, (url, digest) in SOURCES.items():
            with urllib.request.urlopen(url, timeout=120) as response:
                data = response.read(100 * 1024 * 1024)
            if hashlib.sha256(data).hexdigest() != digest:
                raise ValueError("Programmer source digest mismatch: " + name)
            archive.writestr(name, data)
    expected = {"releases.json", "usb-programmer-sources.zip", "SHA256SUMS.txt"}
    for key in assets:
        expected.update([assets[key]["name"], hubs[key]["name"], key + ".json", f"usb-notices-{key}.zip"])
    unexpected = {item.name for item in directory.iterdir()} - expected
    if unexpected:
        raise ValueError("Unexpected release files: " + ", ".join(sorted(unexpected)))
    checksum_file = directory / "SHA256SUMS.txt"
    products = sorted(path for path in directory.iterdir() if path.is_file() and path != checksum_file)
    checksum_file.write_text("".join(f"{sha(path)}  {path.name}\n" for path in products), encoding="ascii")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest="action", required=True)
    actions.add_parser("source-tag", aliases=["next-tag"])
    actions.add_parser("current-pins")
    checks = actions.add_parser("check-ci")
    checks.add_argument("commit")
    candidate = actions.add_parser("pin")
    candidate.add_argument("tag")
    candidate.add_argument("candidate")
    candidate.add_argument("directory", type=Path)
    download = actions.add_parser("stage")
    download.add_argument("platform", choices=AVR)
    download.add_argument("directory", type=Path)
    download.add_argument("tag")
    publish = actions.add_parser("assemble")
    publish.add_argument("tag")
    publish.add_argument("directory", type=Path)
    publish.add_argument("commit")
    args = parser.parse_args()
    if args.action in ("source-tag", "next-tag"):
        print(source_tag())
    elif args.action == "current-pins":
        print("true" if current_pins() else "false")
    elif args.action == "check-ci":
        print(wait_for_ci(args.commit))
    elif args.action == "pin":
        pin(args.tag, args.candidate, args.directory)
    elif args.action == "stage":
        stage(args.platform, args.directory, args.tag)
    else:
        assemble(args.tag, args.directory, args.commit)
