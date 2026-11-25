#!/usr/bin/env python3
"""Measure the pinned target ABI and replay actual allocations on its finite heap.

The original SPIFFS and UMM implementations execute natively at 32-bit pointer
width. Physical flash/ADC/clock/network are explicit test ports. SDK/network
memory is a separate reservation, never represented as executed Wi-Fi code.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT=Path(__file__).resolve().parents[1]
OEM=ROOT.parent/'reverse-engineered'
COMMON=ROOT.parents[1]/'common'
FIRMWARE=COMMON.parent

def digest(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def run(command):subprocess.run(list(map(str,command)),check=True)

def target_sizes(toolchain,sdk,build,output,elf):
    units={
        'engine':('owned_common_src_engine.cpp.json',COMMON/'src/engine_internal.hpp',{
            'engine_impl':'picobrew::standalone::Engine::Impl','engine':'picobrew::standalone::Engine',
            'snapshot':'picobrew::standalone::Snapshot','layout':'picobrew::standalone::detail::Layout',
            'field':'picobrew::standalone::detail::Field','point':'picobrew::standalone::detail::Point'}),
        'application':('owned_picoferm_modified_platform_esp8266_application.cpp.json',ROOT/'platform/esp8266/application.cpp',{
            'application':'picoferm::standalone::Application','controller':'picoferm::standalone::Controller','http_server':'PicoHttpServer'}),
        'storage':('owned_common_platform_esp8266_storage.cpp.json',COMMON/'platform/esp8266/storage.cpp',{
            'storage_impl':'picobrew::standalone::esp8266::SpiffsStorage::Impl','spiffs':'spiffs',
            'spiffs_work':'std::uint8_t[512]','spiffs_descriptors':'std::uint8_t[3*sizeof(spiffs_fd)]',
            'spiffs_cache':'std::uint8_t[sizeof(spiffs_cache)+3*(sizeof(spiffs_cache_page)+256)]'}),
        'http':('owned_common_platform_esp8266_http.cpp.json',COMMON/'platform/esp8266/http.cpp',{
            'http_impl':'picobrew::standalone::esp8266::HttpListener::Impl','tcp_pcb':'tcp_pcb','tcp_listener':'tcp_pcb_listen','pbuf':'pbuf'}),
        'telemetry':('owned_common_platform_esp8266_telemetry.cpp.json',COMMON/'platform/esp8266/telemetry.cpp',{
            'telemetry_impl':'picobrew::standalone::esp8266::AsyncTelemetry::Impl'})}
    sizes={};binary=toolchain/'bin'
    for name,(cache,source,types) in units.items():
        code='#define private public\n#include "'+str(source)+'"\n'
        if name=='storage':code+='#include "spiffs/spiffs_nucleus.h"\n'
        code+='extern "C" {\n'
        code+=''.join('char memory_'+key+'[sizeof('+value+')];\n' for key,value in types.items())+'}\n'
        probe=output/(name+'.cpp');probe.write_text(code);obj=output/(name+'.o')
        flags=json.loads((build/cache).read_text())['identity']['flags']
        run([binary/'xtensa-lx106-elf-g++',*flags,'-c',probe,'-o',obj])
        for line in subprocess.check_output([str(binary/'xtensa-lx106-elf-nm'),'-S',str(obj)],text=True).splitlines():
            match=re.search(r'^\S+\s+([0-9a-f]+)\s+\S\s+memory_(\w+)$',line)
            if match:sizes[match[2]]=int(match[1],16)
    probe=output/'uart.c';obj=output/'uart.o'
    probe.write_text('#include "'+str(sdk/'cores/esp8266/uart.c')+'"\nchar memory_uart[sizeof(uart_t)];\nchar memory_uart_rx_control[sizeof(struct uart_rx_buffer_)];\n')
    flags=json.loads((build/'sdk_cores_esp8266_uart.c.json').read_text())['identity']['flags']
    run([binary/'xtensa-lx106-elf-gcc',*flags,'-c',probe,'-o',obj])
    for line in subprocess.check_output([str(binary/'xtensa-lx106-elf-nm'),'-S',str(obj)],text=True).splitlines():
        match=re.search(r'^\S+\s+([0-9a-f]+)\s+\S\s+memory_(\w+)$',line)
        if match:sizes[match[2]]=int(match[1],16)
    symbols=subprocess.check_output([str(binary/'xtensa-lx106-elf-nm'),'-n',str(elf)],text=True)
    heap=int(re.search(r'^([0-9a-f]+)\s+\S\s+_heap_start$',symbols,re.M)[1],16)
    return sizes,heap

def native_build(sdk,i386,output):
    flags=['-DPICOBREW_NO_RECIPE_LIBRARY','-O2','-Wall','-Wextra','-Werror','-m32','-static','-msse2','-mfpmath=sse','-ffunction-sections','-fdata-sections',
        '-include',str(ROOT/'tests/memory_host/allocator_config.h')]
    if i386:
        sysroot=i386/'sysroot'
        flags+=['-isystem',str(sysroot/'usr/include/x86_64-linux-gnu/c++/14/32'),'-isystem',str(sysroot/'usr/include'),
            '-isystem',str(sysroot/'usr/include/x86_64-linux-gnu'),'-isystem','/usr/include/x86_64-linux-gnu',
            '-B'+str(sysroot/'usr/lib/gcc/x86_64-linux-gnu/14/32')+'/', '-B'+str(sysroot/'usr/lib32')+'/',
            '-L'+str(sysroot/'usr/lib32')]
    flags+=['-I'+str(p) for p in (ROOT/'tests/memory_host',COMMON/'tests/esp8266_sdk_host',ROOT/'include',OEM/'include',
        COMMON/'include',COMMON/'platform/esp8266',sdk/'cores/esp8266',sdk/'cores/esp8266/umm_malloc',sdk/'tools/sdk/include')]
    objects=[];sdk_sources=[]
    for name in ('spiffs_hydrogen','spiffs_nucleus','spiffs_cache','spiffs_gc','spiffs_check'):
        obj=output/(name+'.o');source=sdk/('cores/esp8266/spiffs/'+name+'.c')
        run(['gcc','-std=c99',*flags,'-Wno-unused-parameter','-Wno-sign-compare','-Wno-stringop-truncation','-c',source,'-o',obj])
        objects.append(obj);sdk_sources.append(source)
    for source in (COMMON/'src/http_server.c',sdk/'cores/esp8266/umm_malloc/umm_malloc.c'):
        obj=output/(source.stem+'.o')
        warnings=['-Wno-unused-parameter','-Wno-sign-compare','-Wno-unused-but-set-variable']
        if source.is_relative_to(sdk):
            # The pinned allocator copies its flash-resident format literals to RAM before printf.
            warnings.append('-Wno-error=format-security')
            sdk_sources.append(source)
        run(['gcc','-std=c99',*flags,*warnings,'-c',source,'-o',obj])
        objects.append(obj)
    sources=[ROOT/'tests/memory_native.cpp',ROOT/'src/standalone.cpp',ROOT/'src/calibration_store.cpp',ROOT/'src/bounded_acquisition.cpp',
        OEM/'src/hardware.cpp',OEM/'src/core.cpp',COMMON/'platform/esp8266/storage.cpp']
    sources+=[COMMON/('src/'+n+'.cpp') for n in ('engine','api','values','records','history','history_records','telemetry','provisioning','verified_storage')]
    executable=output/'memory-native'
    run(['g++','-std=c++11',*flags,*sources,*objects,'-Wl,--gc-sections','-Wl,--wrap=malloc,--wrap=calloc,--wrap=realloc,--wrap=free','-o',executable])
    # i386 and LX106 both have 32-bit pointers, but double/member alignment can
    # differ. Reserve every byte by which a target heap owner is larger.
    probe=output/'native-sizes.cpp';obj=output/'native-sizes.o'
    probe.write_text('#define private public\n#include "'+str(COMMON/'src/engine_internal.hpp')+'"\n#include "'+str(COMMON/'platform/esp8266/storage.cpp')+'"\nextern "C" {\n'
        'char memory_engine_impl[sizeof(picobrew::standalone::Engine::Impl)];\n'
        'char memory_storage_impl[sizeof(picobrew::standalone::esp8266::SpiffsStorage::Impl)];\n'
        'char memory_layout[sizeof(picobrew::standalone::detail::Layout)];\n'
        'char memory_field[sizeof(picobrew::standalone::detail::Field)];\n}\n')
    run(['g++','-std=c++11',*flags,'-c',probe,'-o',obj]);sizes={}
    for line in subprocess.check_output(['nm','-S',str(obj)],text=True).splitlines():
        match=re.search(r'^\S+\s+([0-9a-f]+)\s+\S\s+memory_(\w+)$',line)
        if match:sizes[match[2]]=int(match[1],16)
    return executable,sources,sdk_sources,sizes

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--toolchain',type=Path,required=True);parser.add_argument('--arduino',type=Path,required=True)
    parser.add_argument('--i386',type=Path,help='Optional repository-pinned extracted multilib/qemu runtime; otherwise use system g++ -m32')
    parser.add_argument('--build-dir',type=Path,default=ROOT/'build/esp8266')
    parser.add_argument('--output',type=Path,default=ROOT/'build/memory-validation')
    parser.add_argument('--ab', action='store_true', help='Validate the separately linked A/B bank firmware')
    parser.add_argument('--write-report',action='store_true');args=parser.parse_args()
    for name in ('toolchain','arduino','build_dir','output','i386'):
        if getattr(args,name) is not None:setattr(args,name,getattr(args,name).resolve())
    sys.path.insert(0,str(COMMON/'tools'));import package_reference
    elf=args.build_dir/'picoferm-standalone.elf'
    if args.ab:
        import build_ab
        from build_esp8266_image import read_elf, component
        elf=args.build_dir/'bank-0.elf'
        for bank,base in enumerate(build_ab.BANKS):
            image=args.build_dir/f'bank-{bank}.bin'
            entry,sections=read_elf(args.build_dir/f'bank-{bank}.elf')
            if sections['.irom0.text']['address']!=0x40200010+base or image.read_bytes()!=component(entry,[sections[k] for k in build_ab.SECTIONS]):
                raise ValueError('A/B heap verification needs both matching ELF/image pairs')
        report,count={'elf_sha256':digest(elf)},None
    else:
        report,count=package_reference.verify_report('picoferm',elf,args.build_dir/'build-report.json',
            FIRMWARE.parent,args.arduino,None)
    args.output.mkdir(parents=True,exist_ok=True)
    sizes,heap_start=target_sizes(args.toolchain,args.arduino,args.build_dir,args.output,elf)
    if sizes['telemetry_impl']!=280 or sizes['http_impl']!=24:raise ValueError('Update exact platform reservation for changed target owners')
    if args.ab:
        symbols = subprocess.check_output([str(args.toolchain/'bin/xtensa-lx106-elf-nm'), '-C', str(elf)], text=True)
        for unused in ('::detail::parse_recipe(', '::detail::recipe_json(', '::Impl::load_recipes('):
            if unused in symbols:
                raise ValueError('PicoFerm retains unsupported brewer-only code: '+unused)
        other_symbols=subprocess.check_output([str(args.toolchain/'bin/xtensa-lx106-elf-nm'),'-n',str(args.build_dir/'bank-1.elf')],text=True)
        if int(re.search(r'^([0-9a-f]+)\s+\S\s+_heap_start$',other_symbols,re.M)[1],16)!=heap_start:
            raise ValueError('Dual PicoFerm banks disagree on available heap')
    heap_bytes=0x3fffc000-heap_start
    executable,sources,sdk_sources,native_sizes=native_build(args.arduino,args.i386,args.output)
    if any(sizes[key]!=native_sizes[key] for key in ('field','layout')):raise ValueError('History schema allocation ABI differs')
    abi_gap=sum(max(0,sizes[key]-native_sizes[key]) for key in ('engine_impl','storage_impl'))
    # Charge each omitted platform allocation with the original allocator's
    # four-byte header/eight-byte rounding; include both UART control structs.
    platform_allocations=[sizes['telemetry_impl'],sizes['http_impl'],236,256,sizes['uart'],sizes['uart_rx_control']]
    platform=sum(((size+4+7)//8)*8 for size in platform_allocations)+abi_gap
    emulator=[str(args.i386/'usr/bin/qemu-i386')] if args.i386 else []
    result=json.loads(subprocess.check_output(emulator+[str(executable),str(heap_bytes),'16384',str(platform)],text=True,timeout=120))
    if result['pointer_bytes']!=4 or result['allocation_failures'] or not result['passed']:raise ValueError('Finite 32-bit heap replay failed')
    files=sources+[ROOT/'tests/memory_host/allocator_config.h',ROOT/'tests/memory_host/pgmspace.h',Path(__file__),
        COMMON/'src/engine_internal.hpp',COMMON/'src/json.hpp',COMMON/'src/records.hpp',COMMON/'src/recipe_record.hpp',COMMON/'include/picobrew/device_services.hpp']
    output=dict(schema_version=1,target_type_bytes=sizes,target_heap_start=hex(heap_start),target_heap_end='0x3fffc000',target_heap_bytes=heap_bytes,
        elf_sha256=report['elf_sha256'],build_source_entries_verified=count,native_type_bytes=native_sizes,target_abi_gap_reserved=abi_gap,
        platform_allocation_payloads=platform_allocations,native=result,
        native_compiler=subprocess.check_output(['g++','--version'],text=True).splitlines()[0],
        native_execution='Pinned extracted multilib/qemu-i386' if args.i386 else 'System multilib compiler and native 32-bit execution',
        phase_names=['startup','telemetry frame','session reservation','sampling','active and retained history pages','stop'],
        sdk_reservation_with_allocator_overhead=16*((((16384//16)+4+7)//8)*8),
        application_peak_excluding_sdk_reservation=result['peak_used_bytes']-16*((((16384//16)+4+7)//8)*8),
        source_sha256={p.relative_to(FIRMWARE).as_posix():digest(p) for p in files},
        sdk_sha256={p.relative_to(args.arduino).as_posix():digest(p) for p in sdk_sources+[args.arduino/('cores/esp8266/'+name) for name in
            ('umm_malloc/umm_malloc_cfg.h','HardwareSerial.cpp','uart.c','cont.h')]},
        boundary='Actual 32-bit Controller, Engine, Provisioning, VerifiedStorage and pinned SPIFFS/UMM sources. NOR, ADC, time and transport are test ports. A fragmented 16-KiB SDK/network reservation is held throughout; it is a budget test, not executed private Wi-Fi firmware. Target static owner includes HTTP arena and SDK continuation stack.',
        hardware_high_water_measured=False)
    if args.i386:output['i386_runtime_pin_sha256']=digest(FIRMWARE/'z-series/reverse-engineered/tests/toolchains/recipe-i386.json')
    if args.write_report:
        name = 'memory-ab-validation.json' if args.ab else 'memory-validation.json'
        (ROOT/'analysis'/name).write_text(json.dumps(output,indent=2)+'\n')
    print(json.dumps(output,indent=2))

if __name__=='__main__':main()
