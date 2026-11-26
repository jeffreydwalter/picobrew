"""Exercise the Z119 group invitation and dial against the recovered host archive."""

import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest

TESTS = Path(__file__).resolve().parent
OEM = TESTS.parents[1] / "reverse-engineered"


def build_group_dial(binary, compile_flags=(), link_flags=()):
    # Direct unittest invocation after firmware-bootstrap uses the same pinned
    # JSON tree as make firmware-test. The OEM build also has its own cache.
    make = ["make", "-s", "-j4", "json-adapter"]
    pin = json.loads((OEM / "dependencies/arduinojson.json").read_text())
    shared = OEM.parents[1] / "common/build/dependencies" / pin["directory"]
    if "ARDUINOJSON_DIR" not in os.environ and shared.is_dir():
        make.append(f"ARDUINOJSON_DIR={shared}")
    subprocess.run(make, cwd=OEM, check=True)
    subprocess.run(shlex.split(os.environ.get("CXX", "c++")) + [
        "-std=c++11", "-O1", "-Wall", "-Wextra", "-Werror", "-pedantic", *compile_flags,
        "-I", str(OEM / "include"), str(TESTS / "oem_group_dial.cpp"),
        str(OEM / "build/libzseries_oem_json.a"), *link_flags, "-o", str(binary),
    ], check=True)


class OEMGroupDial(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        temp = tempfile.TemporaryDirectory(prefix="z119-group-dial-")
        cls.addClassCleanup(temp.cleanup)
        cls.binary = Path(temp.name) / "group-dial"
        build_group_dial(cls.binary)

    def run_scenario(self, scenario):
        subprocess.run([str(self.binary), scenario], timeout=10, check=True)

    def test_dial_starts_downloaded_recipe(self):
        self.run_scenario("start")

    def test_dial_exit_discards_offer_after_replacing_working_recipe(self):
        self.run_scenario("exit")

    def test_same_receiver_exits_then_starts_a_different_offer(self):
        self.run_scenario("exit-then-start")

    def test_unanswered_offer_expires_with_downloaded_recipe_in_buffer(self):
        self.run_scenario("timeout")

    def test_recent_input_discards_offer_before_replacing_working_recipe(self):
        self.run_scenario("recent-input")

    def test_malformed_download_leaves_working_recipe(self):
        self.run_scenario("invalid-download")


if __name__ == "__main__":
    unittest.main()
