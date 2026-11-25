"""Exercise the production adapter, real Core/Head and shared persistent engine."""
from pathlib import Path
import os
import shlex
import subprocess
import sys
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[1]
OEM=ROOT.parent/'reverse-engineered'
COMMON=ROOT.parents[1]/'common'
sys.path.insert(0,str(OEM/'tests'))
from program_recipe_native import dependency

class StandaloneTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp=tempfile.TemporaryDirectory(prefix='z-standalone-')
        cls.addClassCleanup(cls.temp.cleanup)
        output=Path(cls.temp.name)
        subprocess.run(['make','-s','-j4','json-adapter','ARDUINOJSON_DIR='+str(dependency())],cwd=OEM,check=True,capture_output=True,text=True)
        http=output/'http.o'
        subprocess.run(shlex.split(os.environ.get('CC','cc'))+['-std=c11','-O1','-I'+str(COMMON/'include'),'-c',str(COMMON/'src/http_server.c'),'-o',str(http)],check=True)
        cls.binary=output/'integration'
        sources=[ROOT/'tests/standalone_integration.cpp']+sorted((ROOT/'src').glob('*.cpp'))
        sources+=sorted(p for p in (COMMON/'src').glob('*.cpp') if p.name!='common_web_assets.cpp')
        subprocess.run(shlex.split(os.environ.get('CXX','c++'))+[
            '-std=c++11','-O1','-Wall','-Wextra','-Werror','-pedantic','-fno-fast-math','-ffp-contract=off',
            '-I'+str(ROOT/'include'),'-I'+str(OEM/'include'),'-I'+str(COMMON/'include'),'-I'+str(COMMON/'src')]+
            [str(p) for p in sources]+[str(http),str(OEM/'build/libzseries_oem_json.a'),'-o',str(cls.binary)],check=True)
    def run_case(self,name):subprocess.run([str(self.binary),name],check=True,timeout=15)
    def test_group_brewing_two_brewers_and_real_dial(self):self.run_case("group-brew")
    def test_group_brewing_native_programs(self):self.run_case("group-routines")
    def test_group_brewing_interlocks_and_replay(self):self.run_case("group-interlocks")
    def test_program_maker_storage_execution_and_recovery_match_original(self):self.run_case('maker')
    def test_owned_recipe_native_program_and_reserved_history(self):self.run_case('recipe')
    def test_storage_failure_precedes_native_program_start(self):self.run_case('storage')
    def test_both_picostill_programs_execute_native_prompts_before_accessory_start(self):self.run_case('still-programs')
    def test_picostill_identity_verification_atomic_replacement_and_unpair(self):self.run_case('still-pairing')
    def test_picostill_native_cleaning_start_barrier_samples_and_link_loss(self):self.run_case('still-session')
    def test_unheated_step_executes_real_runtime_pid_stop(self):self.run_case('unheated')
    def test_latched_stop_blocks_gpio_and_msp_with_filter_clear_prerequisite(self):self.run_case('stop')
    def test_combined_encoder_input_and_custom_menu_cleanup(self):self.run_case('input')
    def test_offline_boot_and_native_session_cancel_or_ignore(self):self.run_case('offline')
    def test_persistent_settings_and_frozen_grayscale_pages(self):self.run_case('settings')
    def test_actual_z_status_schema_and_sensor_staleness(self):self.run_case('status')
    def test_restart_requires_confirmation_and_reheats_before_remaining_hold(self):self.run_case('recovery')
    def test_unheated_and_paused_recovery_preserve_output_and_acknowledgement_order(self):self.run_case('recovery-pause')
    def test_corrupt_checkpoint_rejects_start_and_explicit_discard_repairs_both_banks(self):self.run_case('recovery-corrupt')
    def test_checkpoint_failure_cuts_outputs_and_stop_or_completion_clears_recovery(self):self.run_case('recovery-end')
    def test_sensor_short_reply_and_invalid_bytes_cut_outputs_before_return(self):self.run_case('sensor-fault')
    def test_buffered_coffee_uses_native_history_without_beer_recovery(self):self.run_case('coffee-buffer')
    def test_recipe_menu_and_retired_manual_command(self):self.run_case('recipe-menu')
    def test_remote_recipe_start_wakes_native_standby(self):self.run_case('standby-start')
    def test_recipe_step_index_ignores_common_steps_and_resume_labels(self):self.run_case('step-indices')
    def test_wired_selection_busy_storage_link_loss_and_return_to_wifi(self):self.run_case('wired-network')
    def test_both_native_sous_vide_programs(self):self.run_case('sous-vide')
    def test_original_z_state_is_authenticated_and_complete(self):self.run_case('native-state')
    def test_wifi_picker_keeps_lease_and_selects_from_deduplicated_snapshot(self):self.run_case('wifi-picker')
    def test_pairing_requires_physical_selection_and_the_displayed_request(self):self.run_case('pairing')
    def test_all_native_network_entry_points_use_standalone_wifi_menu(self):self.run_case('network-menus')
    def test_rejected_resume_keeps_checkpoint_reviewable_and_discardable(self):self.run_case('recovery-rejected')
    def test_recovery_preflight_does_not_run_returning_assertions(self):self.run_case('recovery-preflight')

if __name__=='__main__':unittest.main()
