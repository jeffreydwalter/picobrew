"""Exercise Home lease transitions with real wired-driver register instrumentation."""
import os
from pathlib import Path
import subprocess
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]

if __name__ == "__main__":
    with tempfile.TemporaryDirectory() as td:
        test = Path(td) / "scheduler"
        subprocess.run([os.environ.get("CC", "cc"), "-std=c11", "-O1", "-Wall", "-Wextra", "-Werror",
                        "-DZYM_SCHEDULER_TEST", "-I", ROOT, "-I", HERE.parent, HERE / "scheduler.c",
                        HERE / "scheduler_test.c", ROOT / "zymatic_api_w5100.c", "-o", test], check=True)
        for scenario in ("normal", "timeout", "disconnected", "rewire", "clock-stopped"):
            subprocess.run([test, scenario], check=True)
            print(f"Wi-Fi scheduler: {scenario} passed")
