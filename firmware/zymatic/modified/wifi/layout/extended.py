"""Verify the extended application's Flash-resident HTTP credentials in a temporary checkout."""

import argparse
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[2]

def stage(directory):
    repo = ROOT.parents[2]
    component = ROOT.relative_to(repo)
    archive = subprocess.check_output(["git", "archive", "HEAD", str(component), "tools/private_files.py"], cwd=repo)
    with tarfile.open(fileobj=io.BytesIO(archive)) as source:
        for member in source:
            if member.isfile():
                target = directory / member.name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(source.extractfile(member).read())
    runtime = directory / component
    subprocess.run(["git", "apply", "--no-index", str(Path(__file__).with_name("extended-identity.patch"))],
                   cwd=runtime, check=True)
    return runtime


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--toolchain", required=True, type=Path)
    parser.add_argument("--oem-file", type=Path, help="Run the AVR regressions with this original 1.1.14 ZIP or HEX")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory() as temporary:
        runtime = stage(Path(temporary))
        subprocess.run([sys.executable, runtime / "tools/compile_extension.py", "--toolchain",
                        args.toolchain.resolve(), "--out-dir", runtime / "extension"], check=True)
        manifest = json.loads((runtime / "extension/extension.json").read_bytes())
        for name in ("provision_key", "provision_uid"):
            address = manifest["symbols"][name]
            if not 0x10000 <= address <= 0x20000 - 33:
                raise ValueError("Credential moved outside flash bank 1")
        if args.oem_file:
            subprocess.run([sys.executable, runtime / "tests/run_simavr.py", "--oem-file", args.oem_file.resolve()],
                           check=True)


if __name__ == "__main__":
    main()
