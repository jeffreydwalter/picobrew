#!/usr/bin/env python3
"""Build the complete PicoStill standalone owner and reference application image.

Uses the pinned historical SDK, checked startup, actual GPIO/I2C drivers and
shared local API/browser/storage/network ports. Never contacts or flashes a
device. The application image is for the explicit common reference layout.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys

ROOT=Path(__file__).resolve().parents[1]
OEM=ROOT.parent/'reverse-engineered'
COMMON=ROOT.parents[1]/'common'
FIRMWARE=COMMON.parent
REPOSITORY=FIRMWARE.parent

def digest(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def verify_identity(base,pin):
    for name,expected in json.loads(pin.read_text())['identity_sha256'].items():
        if digest(base/name)!=expected:raise ValueError('Pinned dependency mismatch: '+name)

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--toolchain',type=Path,required=True)
    parser.add_argument('--arduino',type=Path,required=True)
    parser.add_argument('--output',type=Path,default=ROOT/'build/esp32')
    args=parser.parse_args();toolchain=args.toolchain.resolve();arduino=args.arduino.resolve();output=args.output.resolve()
    verify_identity(toolchain,OEM/'dependencies/toolchain.json');verify_identity(arduino,OEM/'dependencies/arduino-esp32.json')
    output.mkdir(parents=True,exist_ok=True);sdk=arduino/'tools/sdk';binary=toolchain/'bin'
    cc=binary/'xtensa-esp32-elf-gcc';cxx=binary/'xtensa-esp32-elf-g++';ar=binary/'xtensa-esp32-elf-ar'
    flags=['-Os','-Wall','-Wextra','-Werror','-Wno-unused-parameter','-mlongcalls','-ffunction-sections','-fdata-sections',
           '-fno-fast-math','-ffp-contract=off','-DESP_PLATFORM','-DESP32','-DARDUINO_ARCH_ESP32','-DARDUINO=10805',
           '-DF_CPU=240000000L','-DCORE_DEBUG_LEVEL=0','-DHAVE_CONFIG_H']
    includes=[ROOT/'include',ROOT/'platform/esp32',OEM/'include',OEM/'platform/esp32',COMMON/'include',COMMON/'src',COMMON/'platform/esp32',COMMON/'platform/esp32/ota',
              arduino/'cores/esp32',arduino/'variants/esp32']
    sdk_sources=[p for p in sorted((arduino/'cores/esp32').iterdir()) if p.suffix in ('.c','.cpp','.S') and p.name!='main.cpp']
    for library in ('Wire','WiFi'):
        folder=arduino/'libraries'/library/'src';includes.append(folder)
        sdk_sources.extend(p for p in sorted(folder.rglob('*')) if p.suffix in ('.c','.cpp','.S'))
    includes += sorted(p for p in (sdk/'include').iterdir() if p.is_dir())
    own=list(sorted((ROOT/'src').glob('*.cpp')))+list(sorted((ROOT/'platform/esp32').glob('*.cpp')))
    own += [COMMON/'platform/esp32/ota'/name for name in ('service.cpp','factory_flash.cpp')]
    own += [OEM/'src'/name for name in ('core.cpp','enums.cpp','animations.cpp','sensors.cpp')]
    own += [OEM/'platform/esp32/arduino_ports.cpp']
    own += [COMMON/'src'/name for name in ('engine.cpp','api.cpp','values.cpp','records.cpp','history.cpp','history_records.cpp','telemetry.cpp',
                                         'provisioning.cpp','verified_storage.cpp','common_web_assets.cpp','http_server.c')]
    own += [COMMON/'platform/esp32'/name for name in ('network_ports.cpp','storage_ports.cpp','startup.cpp','station.cpp')]
    include_flags=['-I'+str(p) for p in includes];object_sets=[[],[]];evidence=[[],[]]
    for external,sources in enumerate((own,sdk_sources)):
        for source in sources:
            relative=source.relative_to(arduino if external else FIRMWARE).as_posix()
            obj=output/(('sdk_' if external else 'owned_')+relative.replace('/','_')+'.o');cache=obj.with_suffix('.json')
            cpp=source.suffix=='.cpp';compiler=cxx if cpp else cc
            language=['-std=gnu++11','-fno-exceptions','-fno-rtti'] if cpp else ['-std=gnu99'] if source.suffix=='.c' else ['-x','assembler-with-cpp']
            chosen=flags if not external else [f for f in flags if f not in ('-Wall','-Wextra','-Werror')]+['-w']
            identity=dict(source_sha256=digest(source),compiler_sha256=digest(compiler),flags=language+chosen+include_flags)
            previous=json.loads(cache.read_text()) if cache.exists() else {}
            # Owned modules always rebuild so edits to any contract/SDK header
            # cannot reuse an object produced against a different definition.
            if not external or not obj.exists() or previous.get('identity')!=identity or previous.get('object_sha256')!=digest(obj):
                subprocess.run([str(compiler),*language,*chosen,*include_flags,'-c',str(source),'-o',str(obj)],check=True)
                cache.write_text(json.dumps(dict(identity=identity,object_sha256=digest(obj)))+'\n')
            object_sets[external].append(obj)
            evidence[external].append(dict(source=relative,source_sha256=digest(source),object_sha256=digest(obj),object_bytes=obj.stat().st_size))
    archives=[]
    for index,name in enumerate(('libpicostill_standalone.a','libarduino_required.a')):
        archive=output/name
        if archive.exists():archive.unlink()
        subprocess.run([str(ar),'rcs',str(archive),*map(str,object_sets[index])],check=True);archives.append(archive)
    properties=dict(line.split('=',1) for line in (arduino/'platform.txt').read_text().splitlines() if line and not line.startswith('#') and '=' in line)
    libraries=properties['compiler.c.elf.libs'].split();elf=output/'picostill-standalone.elf';map_file=elf.with_suffix('.map')
    scripts=('esp32_out.ld','esp32.common.ld','esp32.rom.ld','esp32.peripherals.ld','esp32.rom.spiram_incompatible_fns.ld')
    link=['-nostdlib','-L'+str(sdk/'lib'),'-L'+str(sdk/'ld')]
    for script in scripts:link+=['-T',script]
    link+=['-u','ld_include_panic_highint_hdl','-u','call_user_start_cpu0','-u','app_main','-u','esp_app_desc','-u','picostill_update_identity','-Wl,--gc-sections','-Wl,-static',
           '-Wl,--undefined=uxTopUsedPriority','-u','__cxa_guard_dummy','-u','__cxx_fatal_exception','-Wl,-Map='+str(map_file),'-Wl,--cref',
           '-Wl,--start-group',*map(str,archives),*libraries,'-Wl,--end-group','-Wl,-EL','-o',str(elf)]
    subprocess.run([str(cc),*link],check=True)
    symbols=subprocess.check_output([str(binary/'xtensa-esp32-elf-nm'),'-C','-n',str(elf)],text=True)
    functions='\n'.join(line for line in symbols.splitlines() if len(line.split())>=3 and line.split()[1] in ('T','t','W','w'))
    required=[' app_main','picostill::standalone::Device::poll()','picostill::standalone::Device::execute(',
              'picostill::standalone::OutputGuard::inhibit()','picostill::oem::Controller::poll()',
              'picostill::oem::ArduinoPorts::initialize_hardware(', 'picobrew::standalone::Engine::poll()',
              'picobrew::standalone::Provisioning::feed(', 'picobrew::standalone::esp32::initialize_arduino()',
              'picobrew::standalone::esp32::connect_station(', 'picobrew::standalone::esp32::SpiffsFiles::mount()',
              'picobrew::standalone::esp32::TelemetrySocket::send(', 'pico_http_server_poll',
              'picobrew::esp32_ota::Service::handle(', 'picobrew::esp32_ota::FactoryFlash::finish(',
              'esp_ota_begin', 'esp_ota_set_boot_partition']
    forbidden=[' initArduino',' loopTask(', 'picostill::oem::Machine::', 'picostill::oem::Application::',
               'picostill::oem::Backend::','picostill::oem::FirmwareUpdate::','picostill::oem::CredentialStorage::','picostill::oem::HttpServer::']
    for name in required:
        if name not in functions:raise ValueError('Missing linked standalone symbol: '+name)
    for name in forbidden:
        if name in functions:raise ValueError('OEM cloud/erase-capable startup retained: '+name)
    retained={}
    for line in functions.splitlines():
        if any(name in line for name in required):
            address,kind,name=line.split(None,2);retained[name]=hex(int(address,16))
    image=output/'application.bin';esptool=arduino/'tools/esptool.py'
    subprocess.run([sys.executable,str(esptool),'--chip','esp32','elf2image','--flash_mode','dio','--flash_freq','40m','--flash_size','4MB','-o',str(image),str(elf)],check=True)
    sys.path.insert(0,str(REPOSITORY/'tools'))
    from firmware_analysis.esp_image import parse_esp32
    parsed=parse_esp32(image.read_bytes())
    version = "1.0.0"
    marker = ("PicoBrew OTA1|picostill|" + version).encode().ljust(64, b"\0")
    if image.read_bytes()[0x120:0x160] != marker or image.stat().st_size > 0xf0000:
        raise ValueError("PicoStill OTA descriptor or slot length differs from the installer contract")
    size=subprocess.check_output([str(binary/'xtensa-esp32-elf-size'),str(elf)],text=True).strip()
    headers={p.relative_to(FIRMWARE).as_posix():digest(p) for base in (ROOT/'include',ROOT/'platform',OEM/'include',OEM/'platform/esp32',COMMON/'include',COMMON/'src',COMMON/'platform/esp32') for p in sorted(base.rglob('*')) if p.suffix in ('.hpp','.h')}
    report=dict(schema_version=1,model='picostill',firmware_version=version,ota_protocol='esp32-factory-v1',complete_application_linked=True,flash_image_produced=True,
                hardware_tested=False,installation_qualified=False,compiler=subprocess.check_output([str(cxx),'--version'],text=True).splitlines()[0],
                compiler_sha256=digest(cxx),flags=flags,owned_sources=evidence[0],sdk_sources=evidence[1],headers=headers,
                required_symbols=retained,forbidden_symbols_absent=forbidden,elf_sha256=digest(elf),map_sha256=digest(map_file),
                app_image=dict(file=image.name,bytes=image.stat().st_size,sha256=digest(image),parsed=parsed),
                image_converter_sha256=digest(esptool),build_script_sha256=digest(__file__),
                dependency_pins={p.name:digest(p) for p in sorted((OEM/'dependencies').glob('*.json'))},
                original_linker_scripts={name:digest(sdk/'ld'/name) for name in scripts},size=size,task_stack_bytes=8192,
                scope='Complete modified PicoStill task, real GPIO/I2C, repaired rollover timers, common local API/browser/history, verified storage/provisioning and asynchronous optional telemetry. App binary fits the explicit 4-MiB standalone reference layout. No OEM partition-table or physical installation qualification is inferred.')
    (output/'build-report.json').write_text(json.dumps(report,indent=2)+'\n')
    print(size);print('Linked PicoStill standalone and generated checksummed reference application image')
if __name__=='__main__':main()
