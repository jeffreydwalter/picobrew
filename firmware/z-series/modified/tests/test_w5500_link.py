"""Run the production W5500 register state machine against a fault-injecting chip."""
from pathlib import Path
import os
import subprocess
import tempfile
import unittest

class WiredLink(unittest.TestCase):
    def test_raw_frames_mac_link_loss_and_stuck_commands(self):
        source = Path(__file__).with_name('w5500_link_test.cpp')
        with tempfile.TemporaryDirectory() as temp:
            executable = Path(temp) / 'w5500-test'
            subprocess.run([os.environ.get('CXX','c++'), '-std=c++14', '-Wall', '-Wextra',
                            '-Werror', str(source), '-o', str(executable)], check=True)
            subprocess.run([str(executable)], check=True)
