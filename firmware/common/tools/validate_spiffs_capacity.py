#!/usr/bin/env python3
"""Fill complete engine histories on actual SPIFFS and a labelled ESP32 sizing model."""
import argparse
import json
from pathlib import Path
import sys
import tempfile

ROOT=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(ROOT/'tests'))
import esp8266_adapters_native as native

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arduino',type=Path,required=True)
    parser.add_argument('--esp32-sdk',type=Path,required=True)
    parser.add_argument('--write-report',action='store_true');args=parser.parse_args()
    native.verify_sdk(args.arduino)
    pin=json.loads((ROOT/'dependencies/esp32-spiffs-sizing.json').read_text())
    for name,expected in pin['identity_sha256'].items():
        if native.digest(args.esp32_sdk/name)!=expected:raise ValueError('ESP32 sizing identity mismatch: '+name)
    with tempfile.TemporaryDirectory(prefix='picobrew-spiffs-capacity-') as directory:
        results={kind:native.build_and_run(Path(directory)/kind,args.arduino,kind)
                 for kind in ('capacity','capacity_esp32')}
    files=['tests/esp8266_capacity_test.cpp','tests/esp8266_storage_test.cpp','tests/engine_bridge.cpp',
           'tests/esp8266_adapters_native.py','platform/esp8266/storage.cpp','platform/esp8266/storage.hpp',
           'include/picobrew/verified_storage.hpp','src/verified_storage.cpp',
           'include/picobrew/standalone_engine.hpp','include/picobrew/device_services.hpp',
           'include/picobrew/http_server.h','src/http_server.c','src/engine_internal.hpp','src/records.hpp',
           'src/json.hpp','src/recipe_record.hpp','tools/validate_spiffs_capacity.py','dependencies/esp8266-adapters.json',
           'dependencies/esp32-spiffs-sizing.json']
    files+=['src/'+name+'.cpp' for name in ('engine','api','values','records','history','history_records','telemetry')]
    report=dict(schema_version=1,results=results,all_supported_profiles_pass=all(r['passed'] for r in results.values()),
        source_sha256={name:native.digest(ROOT/name) for name in files},
        ferm_boundary='Actual production Engine, VerifiedStorage and raw ESP8266 adapter with all five unmodified pinned SPIFFS C sources, GC=5/metadata=0. Exact eagle.flash.2m.ld 0xFB000 region, page=256, block=8192. Physical flash is a deterministic NOR memory model.',
        still_boundary='Production Engine and shared transaction run on the same SPIFFS C implementation rebuilt with ESP32 sdkconfig GC=10/metadata=4/magic-length=1, unaligned object tables (49-byte header), page=256/block=4096. Both historical default.csv 0x16F000 and selected modified reference 0x200000 regions are measured. This is a sizing model, not execution of the ESP32 SDK filesystem binary. The smaller Ferm-region Still case deliberately measures an unsuitable partition.',
        z_boundary='Same ESP32 SPIFFS sizing geometry using the deployed 0x16F000 partition. Five sensors and four actuators including heater level match the Z recorder schema. All 16 catalog slots are written and read back with 15 steps each in cloud and full PicoPak strength/routing, then two 815-byte Program Maker generations with 20 steps each. The catalog survives 4,224 sample writes, restart and subsequent session creation. The native file-system test uses a modeled Z device, not real flash or actuators.',
        hardware_tested=False,oem_partition_map_claimed=False)
    if args.write_report:
        path=ROOT/'analysis/spiffs-capacity-validation.json';path.parent.mkdir(exist_ok=True)
        path.write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(results,indent=2))
    if not report['all_supported_profiles_pass']:return 1
    return 0
if __name__=='__main__':raise SystemExit(main())
