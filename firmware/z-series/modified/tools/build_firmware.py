#!/usr/bin/env python3
"""Build the standalone Z ELF and checked reference application image.

No partition geometry, bootloader or physical compatibility is assumed here.
"""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import struct

ROOT=Path(__file__).resolve().parents[1]
OEM=ROOT.parent/'reverse-engineered'
COMMON=ROOT.parents[1]/'common'
def digest(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def build_recovery(args,output,objects,esptool,version):
    """Link only the immutable receiver, network transports, storage, and pump/heat cutoff."""
    from package_reference import verify_esp32_descriptor
    sdk=args.arduino/'tools/sdk'
    binary=args.toolchain/'bin'
    directory=output/'link'
    selected=['firmware/common/platform/esp32/'+name for name in (
        'ota/factory_flash.cpp','ota/service.cpp','network_ports.cpp','startup.cpp','station.cpp','storage_ports.cpp')]
    selected+=['firmware/common/src/'+name for name in ('provisioning.cpp','verified_storage.cpp','http_server.c','values.cpp','records.cpp')]
    selected+=['firmware/z-series/modified/platform/esp32/'+name for name in ('msp_bus.cpp','output_cutoff.cpp','factory_application.cpp','wired_network.cpp')]
    elf=output/'recovery.elf'
    props=dict(line.split('=',1) for line in (args.arduino/'platform.txt').read_text().splitlines()
               if line and not line.startswith('#') and '=' in line)
    command=[str(binary/'xtensa-esp32-elf-gcc'),'-nostdlib','-L'+str(sdk/'lib'),'-L'+str(sdk/'ld')]
    for script in ('esp32_out.ld','esp32.common.ld','esp32.rom.ld','esp32.peripherals.ld','esp32.rom.spiram_incompatible_fns.ld'):
        command+=['-T',script]
    for symbol in ('ld_include_panic_highint_hdl','call_user_start_cpu0','esp_app_desc','z_recovery_identity','picobrew_z_recovery_main','uxTopUsedPriority','__cxa_guard_dummy','__cxx_fatal_exception'):
        command+=['-u',symbol]
    command+=['-Wl,--defsym,app_main=picobrew_z_recovery_main','-Wl,--gc-sections','-Wl,-static','-Wl,--start-group']
    command += [str(directory/('owned_'+name.replace('/','_')+'.o')) for name in selected]
    command += [str(objects/'libzseries_recovered_esp32.a'),str(objects/'libethernet2_adapted.a'),str(directory/'libarduino_required.a')]
    subprocess.run([*command,*props['compiler.c.elf.libs'].split(),'-Wl,--end-group','-Wl,-EL','-o',str(elf)],check=True)
    nm=subprocess.check_output([str(binary/'xtensa-esp32-elf-nm'),'-C',str(elf)],text=True)
    for name in ('FirmwareApplication::setup(', 'CoreApplication::tick(', 'HeadApplication::setup(', 'Adapter::execute(', 'Engine::begin('):
        if name in nm:raise ValueError('Factory recovery linked an appliance controller: '+name)
    for name in ('picobrew_z_recovery_main','z_recovery_identity','force_outputs_off()','FactoryFlash::finish('):
        if name not in nm:raise ValueError('Missing factory recovery symbol: '+name)
    image=output/'recovery.bin'
    subprocess.run([sys.executable,str(esptool),'--chip','esp32','elf2image','--flash_mode','dio','--flash_freq','40m','--flash_size','4MB',
                    '-o',str(image),str(elf)],check=True)
    receiver=image.read_bytes()
    verify_esp32_descriptor(receiver,version,'z-series',recovery=True)
    if not receiver[0x160:].startswith(b'PicoBrew W5500/1\0'):
        raise ValueError('Missing Z wired transport marker in recovery descriptor')
    return {'path':image.name,'sha256':digest(image),'bytes':image.stat().st_size,'elf_sha256':digest(elf),'source_files':selected}


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    for option in ('toolchain','arduino','arduinojson','ethernet2','output'):
        parser.add_argument('--'+option,type=Path,required=True)
    parser.add_argument('--objects',type=Path,help='Reuse an already checked OEM object directory')
    args=parser.parse_args()
    output=args.output.resolve();output.mkdir(parents=True,exist_ok=True)
    objects=args.objects.resolve() if args.objects else output/'oem-objects'
    if not args.objects:
        subprocess.run([sys.executable,str(OEM/'tools/build_esp32_objects.py'),
            '--toolchain',str(args.toolchain),'--arduino',str(args.arduino),
            '--arduinojson',str(args.arduinojson),'--ethernet2',str(args.ethernet2),
            '--output',str(objects)],check=True)
    command=[sys.executable,str(OEM/'tools/link_esp32_application.py'),
        '--toolchain',str(args.toolchain),'--arduino',str(args.arduino),
        '--objects',str(objects),'--output',str(output/'link'),
        '--entry',str(ROOT/'platform/esp32/application_main.cpp'),'--profile','standalone']
    for folder in [ROOT/'include',ROOT/'platform/esp32',COMMON/'include',COMMON/'src',COMMON/'platform/esp32',COMMON/'platform/esp32/ota']:
        command+=['--extra-include',str(folder)]
    sources=sorted((COMMON/'src').glob('*.cpp'))+[COMMON/'src/http_server.c']
    sources+=sorted((COMMON/'platform/esp32').glob('*.cpp'))+sorted((ROOT/'src').glob('*.cpp'))
    sources += sorted((COMMON/'platform/esp32/ota').glob('*.cpp'))
    sources += [ROOT/'platform/esp32'/name for name in ('msp_bus.cpp','output_cutoff.cpp','factory_application.cpp','wired_network.cpp')]
    for source in sources:command+=['--extra-source',str(source)]
    subprocess.run(command,check=True)
    report=json.loads((output/'link/link-report.json').read_text())
    esptool=args.arduino/'tools/esptool.py'
    application=output/'application.bin'
    subprocess.run([sys.executable,str(esptool),'--chip','esp32','elf2image',
        '--flash_mode','dio','--flash_freq','40m','--flash_size','4MB','-o',str(application),str(output/'link/application.elf')],check=True)
    spec=importlib.util.spec_from_file_location('pinned_esptool',esptool)
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    with application.open('rb') as stream:image=module.ESP32FirmwareImage(stream)
    if image.checksum!=image.calculate_checksum() or not image.append_digest or image.calc_digest!=image.stored_digest:
        raise ValueError('Generated application image checksum/digest mismatch')
    if image.flash_mode!=2 or image.flash_size_freq!=0x20 or application.stat().st_size>0x1f0000:
        raise ValueError('Application image does not fit the explicit 4-MiB reference configuration')
    layout_path=output/'target-memory-layout.bin'
    subprocess.run([str(args.toolchain/'bin/xtensa-esp32-elf-objcopy'),
        '--dump-section','.rodata.picobrew_z_memory='+str(layout_path),str(output/'link/application-entry.o')],check=True)
    layout=struct.unpack('<6I',layout_path.read_bytes())
    report['target_memory_bytes']=dict(zip(('application_owner','task_stack','core_owner','head_owner','adapter','engine_handle'),layout))
    report['application_image']={'path':'application.bin','sha256':digest(application),'bytes':application.stat().st_size,
        'entrypoint':hex(image.entrypoint),'flash_mode':'dio','flash_frequency_mhz':40,'reference_flash_bytes':0x400000,
        'reference_partition_offset':0x10000,'reference_partition_bytes':0x1f0000,
        'checksum_verified':True,'appended_sha256_verified':True,'esptool_sha256':digest(esptool),
        'segments':[{'address':hex(segment.addr),'bytes':len(segment.data),'sha256':hashlib.sha256(segment.data).hexdigest()} for segment in image.segments]}
    report['flash_image_produced']=True
    report['installation_qualified']=False
    report['device_model']='z-series'
    report['variant_mapping']='Z1/Z2 mapping has not been established from an original device backup'
    report['task_stack_bytes']=32768
    report['scope']+=' The standalone profile replaces remote service stages with authenticated local API/history/telemetry and a local recipe catalogue. Persistent storage is checked without automatic formatting. A latched output inhibit and bounded MSP bus transactions are modified policies. Stack frame sizes are compiler estimates, not measured runtime stack or heap high-water marks.'
    sys.path.insert(0,str(COMMON/'tools'))
    from package_reference import verify_esp32_descriptor
    version='1.0.0'
    verify_esp32_descriptor(application.read_bytes(),version,'z-series')
    report.update(model='z-series',firmware_version=version,ota_protocol='esp32-factory-v1')
    report['application_image'].update(reference_partition_offset=0xf0000,reference_partition_bytes=0x1a1000)
    report['recovery_image']=build_recovery(args,output,objects,esptool,version)
    report['build_script_sha256']=digest(__file__)
    (output/'standalone-build-report.json').write_text(json.dumps(report,indent=2)+'\n')

if __name__=='__main__':main()
