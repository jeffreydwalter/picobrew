import hashlib
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class SpiffsCapacityEvidenceTests(unittest.TestCase):
    def test_final_evidence_matches_actual_engine_and_filesystem_sources(self):
        report = json.loads((ROOT / 'analysis/spiffs-capacity-validation.json').read_text())
        self.assertTrue(report['all_supported_profiles_pass'])
        for name, expected in report['source_sha256'].items():
            self.assertEqual(hashlib.sha256((ROOT / name).read_bytes()).hexdigest(), expected, name)
        profiles = [case for suite in report['results'].values() for case in suite['profiles']]
        supported = [case for case in profiles if case['model'] == 'picoferm' or case['partition_bytes'] != 0xFB000]
        self.assertEqual(len(supported), 4)
        z, = [case for case in supported if case['model'] == 'z-series']
        self.assertEqual((z['partition_bytes'], z['recipe_slots'], z['steps_per_recipe']), (0x16f000, 16, 15))
        for case in supported:
            self.assertEqual(case['points'], 4224)
            self.assertTrue(case['full_history'] and case['restart_preserved'] and case['last_page_valid'])
            self.assertTrue(case['replacement_reserve'])
            self.assertEqual(case['next_session'], 2)
        self.assertEqual(report['results']['capacity_esp32']['object_index_alignment'], 0)

    def test_undersized_stress_and_initial_failures_remain_explicit(self):
        current = json.loads((ROOT / 'analysis/spiffs-capacity-validation.json').read_text())
        stress = current['results']['capacity']['profiles'][1]
        self.assertEqual(stress['model'], 'picostill')
        self.assertFalse(stress['full_history'])
        self.assertTrue(stress['last_page_valid'])
        initial = json.loads((ROOT / 'analysis/spiffs-capacity-initial.json').read_text())
        self.assertFalse(initial['all_supported_profiles_pass'])
        self.assertIn('50-byte', initial['historical_status'])


if __name__ == '__main__':
    unittest.main()
