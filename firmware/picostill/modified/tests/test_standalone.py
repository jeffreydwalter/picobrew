"""Execute the actual PicoStill standalone owner and controller with fault ports."""
import os
from pathlib import Path
import subprocess
import unittest

ROOT=Path(__file__).resolve().parents[1]
OEM=ROOT.parent/'reverse-engineered'
COMMON=ROOT.parents[1]/'common'

class StandaloneTests(unittest.TestCase):
    def test_controller_api_history_and_faults(self):
        build=ROOT/'build/native';build.mkdir(parents=True,exist_ok=True)
        sources=[OEM/'src'/name for name in ('core.cpp','enums.cpp','animations.cpp','sensors.cpp')]
        sources += [ROOT/'src'/name for name in ('hardware.cpp','controller.cpp','standalone.cpp','brewer_link.cpp')]
        sources += [COMMON/'src'/name for name in ('engine.cpp','api.cpp','values.cpp','records.cpp','history.cpp','history_records.cpp','telemetry.cpp')]
        output=build/'standalone-native'
        transport=build/'http.o'
        flags=['-O1','-g','-Wall','-Wextra','-Werror','-fsanitize=address,undefined','-fno-omit-frame-pointer']
        subprocess.run(['gcc','-std=c11',*flags,'-I'+str(COMMON/'include'),'-c',str(COMMON/'src/http_server.c'),
                        '-o',str(transport)],check=True)
        subprocess.run(['g++','-std=c++11',*flags,'-no-pie',
                        '-I'+str(ROOT/'include'),'-I'+str(OEM/'include'),'-I'+str(COMMON/'include'),'-I'+str(COMMON/'src'),
                        *map(str,sources),str(transport),str(ROOT/'tests/standalone_native.cpp'),'-o',str(output)],check=True)
        environment=dict(os.environ,ASAN_OPTIONS='detect_leaks=0:halt_on_error=1',UBSAN_OPTIONS='halt_on_error=1')
        result=subprocess.run([str(output)],env=environment,text=True,capture_output=True)
        self.assertEqual(result.returncode,0,result.stdout+result.stderr)
        print(result.stdout.strip())
        (build/'standalone-native.log').write_text(result.stdout)

if __name__=='__main__':unittest.main()
