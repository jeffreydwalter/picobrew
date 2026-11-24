"""Reproduce the public, unprovisioned Zymatic AVR extension from source."""

import argparse
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile

import compact_build
import compact_site
import zymatic_build as build


def compile_compact(toolchain, destination):
    # These are the exact sources of the smaller overlay used on first conversion.
    # Build them independently of the extended runtime and compare both profiles in CI.
    with tempfile.TemporaryDirectory() as temporary:
        work = Path(temporary)
        archive = work / "source.tar"
        subprocess.run(["git", "archive", "-o", str(archive), compact_build.SOURCE_REVISION,
                        "firmware/zymatic/modified", "tools/private_files.py"],
                       cwd=build.ROOT.parents[2], check=True)
        with tarfile.open(archive) as source:
            for entry in source:
                if entry.isfile():
                    target = work / entry.name
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(source.extractfile(entry).read())
        root = work / "firmware/zymatic/modified"
        compact_site.stage(root)
        path = root / "zymatic_api.c"
        code = path.read_text()
        if code.count(compact_build.PREVIEW_VERSION) != 1:
            raise ValueError("Expected one firmware version in pinned compact discovery source")
        path.write_text(code.replace(compact_build.PREVIEW_VERSION, build.VERSION))
        command = [sys.executable, str(root / "tools/compile_extension.py"), "--out-dir", str(destination.resolve())]
        if toolchain:
            command.extend(("--toolchain", str(toolchain.resolve())))
        subprocess.run(command, check=True)


def compile_extension(toolchain, destination):
    src = build.ROOT / "src"
    compiler = str(toolchain / "avr-gcc") if toolchain else shutil.which("avr-gcc")
    if not compiler:
        raise ValueError("Install the Arduino AVR GCC 7.3.0-atmel3.6.1-arduino7 toolchain")
    tools = Path(compiler).resolve().parent
    def invoke(tool, *args, cwd=None):
        return subprocess.check_output([str(tools / tool), *map(str, args)], cwd=cwd, text=True).strip()
    if invoke("avr-gcc", "-dumpversion") != "7.3.0" or "2.26.20160125" not in invoke("avr-ld", "--version"):
        raise ValueError("Use Arduino AVR GCC 7.3.0-atmel3.6.1-arduino7 and its binutils")
    with tempfile.TemporaryDirectory() as temporary:
        work = Path(temporary)
        sources = sorted(src.glob("*.c")) + sorted(src.glob("*.S")) + sorted(build.ROOT.glob("zymatic_api*.c"))
        flags = ["-mmcu=atmega1284p", "-Os", "-std=gnu11", "-Wall", "-Wextra", "-Werror",
                 "-ffunction-sections", "-fdata-sections", "-fno-jump-tables",
                 "-fno-tree-switch-conversion", "-fmerge-all-constants", "-finline-limit=10", "-fno-tree-dominator-opts", "-mcall-prologues", "-flto"]
        for path in sources:
            invoke("avr-gcc", *flags, "-I", build.ROOT, "-c", path, "-o", path.stem + ".o", cwd=work)
        entry_points = [*build.OFFLINE, "zymatic_factory_compare", "zymatic_factory_stamp", "zymatic_after_loop",
                        "zymatic_sensor_crc", "zymatic_pgm_copy", "zymatic_pgm_bitmap", "zymatic_original_byte",
                        "zymatic_pgm_wifi_print", "zymatic_pgm_serial_print", "zymatic_crt_data",
                        "zymatic_home_menu", "zymatic_menu_selection", "zymatic_dialog_button", "zymatic_run_program", "zymatic_about_wait",
                        "zymatic_menu_button", "zymatic_brew_button", "zymatic_read_stock_serial"]
        invoke("avr-gcc", "-mmcu=atmega1284p", "-Os", "-mcall-prologues", "-flto",
               "-fmerge-all-constants", "-finline-limit=10", "-fno-tree-dominator-opts", "-nostartfiles", "-nostdlib", "-Wl,--gc-sections", "-Wl,--relax",
               *[f"-Wl,--undefined={symbol}" for symbol in entry_points],
               "-Wl,-T," + str(src / "overlay.ld"),
               *[p.stem + ".o" for p in sources], "-Wl,--start-group", "-lc", "-lgcc", "-Wl,--end-group",
               "-o", "extension.elf", cwd=work)
        invoke("avr-objcopy", "-j", ".text", "-O", "binary", "extension.elf", "extension.bin", cwd=work)
        invoke("avr-objcopy", "-j", ".low", "-O", "binary", "extension.elf", "low.bin", cwd=work)
        invoke("avr-objcopy", "-j", ".crc_hole", "-O", "binary", "extension.elf", "crc-hole.bin", cwd=work)
        symbols = {}
        wanted = set(entry_points) | set(build.FIELDS) | set(build.DERIVED) | {
            "zymatic_image_invalid", "__extension_bss_end", "zymatic_setup",
            "zymatic_hub_host", "zymatic_hub_authority",
        }
        for line in invoke("avr-nm", "extension.elf", cwd=work).splitlines():
            parts = line.split()
            if len(parts) == 3 and parts[2] in wanted:
                symbols[parts[2]] = int(parts[0], 16)
            if len(parts) == 3 and parts[2] in ("malloc", "calloc", "realloc", "free"):
                raise ValueError("AVR foreground extension must not have a separate heap")
        if symbols.keys() != wanted:
            raise ValueError("Missing AVR integration symbols")
        build.patch_list(symbols)
        output = (work / "extension.bin").read_bytes()
        low = (work / "low.bin").read_bytes()
        crc_code = (work / "crc-hole.bin").read_bytes()
        record = {"crc_hole_bytes":len(crc_code), "crc_hole_sha256":build.sha(crc_code), "low_bytes": len(low), "low_sha256": build.sha(low), "compiler": "Arduino AVR GCC 7.3.0-atmel3.6.1-arduino7",
                  "source_sha256": build.sources_sha(), "extension_sha256": build.sha(output),
                  "extension_bytes": len(output), "symbols": symbols}
        destination.mkdir(parents=True, exist_ok=True)
        (destination / "extension.bin").write_bytes(output)
        (destination / "low.bin").write_bytes(low)
        (destination / "crc-hole.bin").write_bytes(crc_code)
        (destination / "extension.json").write_text(json.dumps(record, indent=2, sort_keys=True) + "\n")
        print(f"Compiled {len(output)} bytes of code/config and {symbols['__extension_bss_end']-0x800000-build.BSS} bytes of SRAM")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", choices=("compact", "extended"), default="extended")
    parser.add_argument("--toolchain", type=Path, help="Directory containing avr-gcc and avr-binutils")
    parser.add_argument("--out-dir", required=True, type=Path)
    args = parser.parse_args()
    try:
        compiler = compile_compact if args.profile == "compact" else compile_extension
        compiler(args.toolchain, args.out_dir)
    except (ValueError, OSError, subprocess.CalledProcessError) as exc:
        parser.exit(1, f"{exc}\n")
