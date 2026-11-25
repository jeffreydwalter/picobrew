#!/usr/bin/env python3
"""Compile/link the complete PicoFerm standalone owner with pinned ESP8266 2.4.1.

Produces a reference ELF/map and checked image, not a qualified installation.
All source/SDK inputs and retained/forbidden control symbols are recorded.
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

def digest(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def check_pin(base,pin):
    for name,sha in json.loads(pin.read_text())['identity_sha256'].items():
        if digest(base/name)!=sha:raise ValueError('Pinned dependency mismatch: '+name)

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--toolchain',type=Path,required=True)
    parser.add_argument('--arduino',type=Path,required=True)
    parser.add_argument('--ab-layout', action='store_true', help='Build the separate PicoFerm A/B USB bootstrap and two OTA images')
    parser.add_argument('--output',type=Path,default=ROOT/'build/esp8266')
    args=parser.parse_args();toolchain=args.toolchain.resolve();arduino=args.arduino.resolve();output=args.output.resolve()
    check_pin(toolchain,OEM/'dependencies/toolchain.json');check_pin(arduino,OEM/'dependencies/arduino-esp8266.json')
    output.mkdir(parents=True,exist_ok=True);sdk=arduino/'tools/sdk';core=arduino/'cores/esp8266'
    binary=toolchain/'bin';cc=binary/'xtensa-lx106-elf-gcc';cxx=binary/'xtensa-lx106-elf-g++';ar=binary/'xtensa-lx106-elf-ar'
    flags=['-DPICOBREW_NO_RECIPE_LIBRARY','-Os','-Wall','-Wextra','-Werror','-mlongcalls','-mtext-section-literals','-ffunction-sections','-fdata-sections','-falign-functions=4',
           '-fno-fast-math','-ffp-contract=off','-D__ets__','-DICACHE_FLASH','-U__STRICT_ANSI__','-DESP8266','-DARDUINO_ARCH_ESP8266','-DARDUINO=10805','-DF_CPU=80000000L','-DLWIP_OPEN_SRC']
    includes=[ROOT/'include',ROOT/'platform/esp8266',OEM/'include',COMMON/'include',COMMON/'platform/esp8266',
              core,core/'umm_malloc',core/'libb64',core/'spiffs',arduino/'variants/generic',sdk/'include',sdk/'lwip/include',sdk/'libc/xtensa-lx106-elf/include']
    sdk_sources=[p for p in sorted(core.rglob('*'))if p.suffix in ('.c','.cpp','.S')]
    for library in ('Wire','EEPROM','ESP8266WiFi/src'):
        folder=arduino/'libraries'/library;includes.append(folder)
        sdk_sources.extend(p for p in sorted(folder.rglob('*'))if p.suffix in ('.c','.cpp','.S') and 'examples'not in p.parts)
    own=list(sorted((ROOT/'src').glob('*.cpp')))+list(sorted((ROOT/'platform/esp8266').glob('*.cpp')))
    own += [OEM/'src'/name for name in ('core.cpp','hardware.cpp')]
    own += [COMMON/'src'/name for name in ('engine.cpp','api.cpp','values.cpp','records.cpp','history.cpp','history_records.cpp','telemetry.cpp','provisioning.cpp','verified_storage.cpp','common_web_assets.cpp','http_server.c')]
    own += [COMMON/'platform/esp8266'/name for name in ('storage.cpp','telemetry.cpp','http.cpp')]
    recovery=FIRMWARE/'pico-s-pro/modified/recovery/recovery'
    if args.ab_layout:
        import build_ab
        build_ab.prepare_loader(output, arduino, cc)
        includes.append(output)
        flags.append('-DPICOFERM_AB')
        includes += [ROOT/'platform/ab', COMMON/'src', recovery]
        own += [ROOT/'platform/ab/boot.c', ROOT/'platform/ab/transfer.cpp', recovery/'sha256.c']
    includes=['-I'+str(p) for p in includes];object_sets=[[],[]];evidence=[[],[]]
    for external,sources in enumerate((own,sdk_sources)):
        for source in sources:
            relative=source.relative_to(arduino if external else FIRMWARE).as_posix()
            obj=output/(('sdk_'if external else'owned_')+relative.replace('/','_')+'.o');cache=obj.with_suffix('.json')
            cpp=source.suffix=='.cpp';compiler=cxx if cpp else cc
            language=['-std=c++11','-fno-exceptions','-fno-rtti']if cpp else ['-std=gnu99']if source.suffix=='.c'else['-x','assembler-with-cpp']
            chosen=flags if not external else [f for f in flags if f not in ('-Wall','-Wextra','-Werror')]+['-w']
            command=language+chosen+includes
            identity=dict(source_sha256=digest(source),compiler_sha256=digest(compiler),flags=command)
            previous=json.loads(cache.read_text())if cache.exists()else{}
            # Always recompile owned code so any shared/SDK header edit is seen.
            if not external or not obj.exists()or previous.get('identity')!=identity or previous.get('object_sha256')!=digest(obj):
                subprocess.run([str(compiler)]+command+['-c',str(source),'-o',str(obj)],check=True)
                cache.write_text(json.dumps(dict(identity=identity,object_sha256=digest(obj)))+'\n')
            object_sets[external].append(obj);evidence[external].append(dict(source=relative,source_sha256=digest(source),object_sha256=digest(obj),object_bytes=obj.stat().st_size))
    archives=[]
    for index,name in enumerate(('libpicoferm_standalone.a','libarduino_required.a')):
        archive=output/name
        if archive.exists():archive.unlink()
        subprocess.run([str(ar),'rcs',str(archive)]+[str(p)for p in object_sets[index]],check=True);archives.append(archive)
    properties=dict(line.split('=',1)for line in (arduino/'platform.txt').read_text().splitlines()if line and not line.startswith('#')and'='in line)
    libraries=properties['compiler.c.elf.libs'].replace('{build.lwip_lib}','-llwip_gcc').split()
    elf=output/'picoferm-standalone.elf';map_file=elf.with_suffix('.map');linker='eagle.flash.2m.ld'
    link=['-nostdlib','-L'+str(sdk/'lib'),'-L'+str(sdk/'ld'),'-L'+str(sdk/'libc/xtensa-lx106-elf/lib'),'-T',linker,'-u','call_user_start','-Wl,--gc-sections',
          '-Wl,-static','-Wl,-wrap,system_restart_local','-Wl,-wrap,spi_flash_read','-Wl,-Map='+str(map_file),'-Wl,--cref','-Wl,--start-group',*map(str,archives),*libraries,'-Wl,--end-group','-Wl,-EL','-o',str(elf)]
    if args.ab_layout:
        import build_ab
        build_ab.build(output, arduino, cc, link)
        return
    subprocess.run([str(cc),*link],check=True,cwd=sdk/'lib')
    symbols=subprocess.check_output([str(binary/'xtensa-lx106-elf-nm'),'-C','-n',str(elf)],text=True)
    functions='\n'.join(line for line in symbols.splitlines()if len(line.split())>=3 and line.split()[1]in('T','t','W','w'))
    required=[' call_user_start',' user_init',' setup',' loop','picoferm::standalone::Controller::poll()',
              'picoferm::standalone::BoundedAcquisition::poll(', 'picobrew::standalone::Engine::poll(bool)',
              'picobrew::standalone::Provisioning::feed(', 'picobrew::standalone::esp8266::HttpListener::receive(',
              'picobrew::standalone::esp8266::SpiffsStorage::begin()', 'picobrew::standalone::esp8266::AsyncTelemetry::send(',
              'picoferm::standalone::LocalControls::poll_button()', 'picoferm::standalone::LocalControls::poll_leds(', 'pico_http_server_poll']
    forbidden=['picoferm::oem::Adc::wait_','picoferm::oem::Adc::sample()', 'picoferm::oem::Acquisition::advance(',
               'picoferm::oem::Application::', 'picoferm::oem::HttpServer::', 'picoferm::oem::Backend::',
               'picoferm::oem::Button::poll()', 'picoferm::oem::Leds::poll()']
    for name in required:
        if name not in symbols:raise ValueError('Missing runtime symbol: '+name)
    for name in forbidden:
        if name in functions:raise ValueError('Unbounded/cloud OEM path retained: '+name)
    retained={}
    for line in symbols.splitlines():
        if any(name in line for name in required):
            address,kind,name=line.split(None,2);retained[name]=hex(int(address,16))
    size=subprocess.check_output([str(binary/'xtensa-lx106-elf-size'),str(elf)],text=True).strip()
    sys.path.insert(0,str(COMMON/'tools'))
    import build_esp8266_image
    image=output/'application.bin'
    image_report=build_esp8266_image.build(elf,arduino,image)
    report=dict(schema_version=1,model='picoferm',firmware_version='1.0.0',complete_application_linked=True,flash_image_produced=True,hardware_tested=False,installation_qualified=False,
                compiler=subprocess.check_output([str(cxx),'--version'],text=True).splitlines()[0],compiler_sha256=digest(cxx),flags=flags,
                owned_sources=evidence[0],sdk_sources=evidence[1],required_symbols=retained,forbidden_symbols_absent=forbidden,
                elf_sha256=digest(elf),map_sha256=digest(map_file),build_script_sha256=digest(__file__),sdk_main_sha256=digest(core/'core_esp8266_main.cpp'),
                app_image=dict(file=image.name,bytes=image.stat().st_size,sha256=digest(image),encoding=image_report),
                dependency_pins={p.name:digest(p)for p in sorted((OEM/'dependencies').glob('*.json'))},
                original_linker_scripts={name:digest(sdk/'ld'/name)for name in (linker,'eagle.app.v6.common.ld','eagle.rom.addr.v6.ld')},
                headers={p.relative_to(FIRMWARE).as_posix():digest(p)for base in (ROOT/'include',ROOT/'platform',OEM/'include',COMMON/'include',COMMON/'src',COMMON/'platform/esp8266')for p in sorted(base.rglob('*'))if p.suffix in('.hpp','.h')},
                size=size,stack_scope='Pinned LX106 GCC4.8.2 does not support -fstack-usage. This link is not a physical stack/heap high-water measurement.',
                scope='Complete modified PicoFerm controller, common authenticated HTTP/browser/history, bounded ADC, verified SPIFFS, physical provisioning and asynchronous optional telemetry linked with unmodified pinned SDK startup. The 2-MiB linker is a reference matching OEM image header, not an individual flash-layout qualification.')
    (output/'build-report.json').write_text(json.dumps(report,indent=2)+'\n');print(size);print('Linked PicoFerm standalone and generated byte-verified reference image; unbounded OEM waits/cloud paths are absent')

if __name__=='__main__':main()
