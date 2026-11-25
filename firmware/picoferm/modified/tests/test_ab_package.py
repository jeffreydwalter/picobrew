import hashlib
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock
import zipfile

ROOT=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(ROOT/'tools'))
sys.path.insert(0,str(ROOT.parents[1]/'common/tools'))
import esp_usb
import package_ab
import usb_ab

class USBPackage(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        directory=os.getenv('PICOFERM_AB_BUILD')
        if not directory:raise unittest.SkipTest('Set PICOFERM_AB_BUILD to the cross-linked bundle')
        cls.build=Path(directory)
        cls.fs=(cls.build/'spiffs.bin').read_bytes()
        cls.package=package_ab.pack(cls.build,cls.fs)
        cls.version=json.loads((cls.build/'ota-manifest.json').read_text())['version']
        cls.parts=usb_ab.parse(cls.package,cls.version)
        cls.stock=sorted((ROOT.parent/'oem/upstream').glob('picoferm_*.bin'))[-1].read_bytes()
        cls.source=cls.stock+(b'\xff'*(0x200000-len(cls.stock)))
    def test_usb_real_archive_and_publication_guard(self):
        self.assertEqual(package_ab.pack(self.build,self.fs),self.package)
        digest=hashlib.sha256(self.package).hexdigest()
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)
            firmware=root/'firmware';firmware.mkdir()
            release={'kind':'picoferm-ab','model':'picoferm','firmware_version':self.version,'archive_sha256':digest,
                     'binary_sha256':hashlib.sha256(self.parts['bank-0.bin']).hexdigest(),
                     'binary_bytes':len(self.parts['bank-0.bin']),'ota_protocol':'esp8266-ab-v1'}
            (firmware/'releases.json').write_text(json.dumps({'releases':[release]}))
            original=ROOT.parents[1]/'upstream-manifest.json'
            (firmware/'upstream-manifest.json').write_bytes(original.read_bytes())
            package=firmware/'picoferm/modified/releases/picoferm'/f'{self.version}.zip'
            package.parent.mkdir(parents=True);package.write_bytes(self.package)
            with mock.patch.object(esp_usb,'ROOT',root):
                key='bc'*32
                version,target=esp_usb.install_target(self.source,'picoferm',api_key=key)
                self.assertEqual(version,self.version)
                self.assertEqual(target[:len(self.parts['bootstrap.bin'])],self.parts['bootstrap.bin'])
                self.assertEqual(target[0x3000:0x3000+len(self.parts['bank-0.bin'])],self.parts['bank-0.bin'])
                self.assertIn(key.encode(),target[usb_ab.FS])
                self.assertEqual(target[0x83000:0x100000],b'\xff'*usb_ab.BANK_SIZE)
                self.assertEqual(target[usb_ab.FS.stop:],self.source[usb_ab.FS.stop:])
                self.assertEqual(list(esp_usb.changed_ranges(self.source,target,0))[-1][0],0)
                with self.assertRaises(ValueError):esp_usb.install_target(target,'picoferm',api_key=key)
                # Simulate a previously paired, published single-bank machine with
                # an additional allocated filesystem page and changed SDK data.
                old=self.parts['bank-0.bin']
                legacy=dict(binary_bytes=len(old),binary_sha256=hashlib.sha256(old).hexdigest())
                fingerprints=firmware/'common/dependencies/legacy-usb-applications.json'
                fingerprints.parent.mkdir(parents=True)
                fingerprints.write_text(json.dumps({'picoferm':[legacy], 'picoferm-ab':[]}))
                running=bytearray(self.source)
                running[:len(old)]=old
                running[usb_ab.FS]=target[usb_ab.FS]
                running[0x102309:0x10230d]=b'logs'
                running[0x1fe100:0x1fe105]=b'radio'
                migrated=usb_ab.migrate(running,self.source,target,key,[legacy])
                self.assertEqual(migrated[:usb_ab.FS.start],target[:usb_ab.FS.start])
                self.assertEqual(migrated[usb_ab.FS.start:],running[usb_ab.FS.start:])
                self.assertEqual(usb_ab.migrate(migrated,self.source,target,key,[legacy]),migrated)
                with self.assertRaises(ValueError):usb_ab.migrate(running,self.source,target,'ff'*32,[legacy])
                occluded=bytearray(running);occluded[0x83200]=7
                with self.assertRaises(ValueError):usb_ab.migrate(occluded,self.source,target,key,[legacy])

                class Device:
                    identity=esp_usb.Identity('ESP8266','aabbccddeeff',0x1540ef,0x200000)
                    def __init__(self,image):self.flash=bytearray(image);self.writes=[]
                    def read(self):return bytes(self.flash)
                    def verify_identity(self):pass
                    def write(self,address,data):
                        self.writes.append(address)
                        self.flash[address:address+len(data)]=data
                device=Device(running)
                data=root/'backup';path=esp_usb.backup_path(data,device.identity)
                esp_usb.save_original(path,self.source,device.identity,'picoferm')
                esp_usb.operation('install','picoferm',device,data,ask=lambda _:device.identity.mac,api_key=key)
                self.assertEqual(device.flash,migrated)
                self.assertEqual(device.writes[-1],0)
                device.writes.clear()
                esp_usb.operation('install','picoferm',device,data,ask=lambda _:device.identity.mac,api_key=key)
                self.assertEqual(device.writes,[])
                esp_usb.operation('restore','picoferm',device,data,ask=lambda q:'RESTORE' if 'RESTORE:' in q else device.identity.mac)
                self.assertEqual(device.flash,self.source)
                package.write_bytes(self.package[:-1])
                with self.assertRaises(ValueError):esp_usb.install_target(self.source,'picoferm',api_key=key)
    def test_unknown_parts_are_preserved(self):
        for where in (len(self.stock)+16,0x86000,0x180000):
            flash=bytearray(self.source);flash[where]=42
            with self.assertRaises(ValueError):usb_ab.target(bytes(flash),len(self.stock),self.parts,None)
        with self.assertRaises(ValueError):usb_ab.target(self.source[:0x100000],len(self.stock),self.parts,None)
    def test_rejects_forged_slot_and_filesystem(self):
        with zipfile.ZipFile(io.BytesIO(self.package)) as z:items={n:z.read(n) for n in z.namelist()}
        for wrong in ('bank-1.bin','spiffs.bin'):
            bad=dict(items)
            if wrong=='bank-1.bin':
                bad[wrong]=bad['bank-0.bin']
                manifest=json.loads(bad['ota-manifest.json'])
                manifest['images'][1]['sha256']=hashlib.sha256(bad[wrong]).hexdigest()
                bad['ota-manifest.json']=json.dumps(manifest).encode()
            else:bad[wrong]=bad[wrong][:-1]+bytes([bad[wrong][-1]^1])
            buf=io.BytesIO()
            with zipfile.ZipFile(buf,'w',compression=zipfile.ZIP_DEFLATED) as z:
                for k,v in bad.items():z.writestr(k,v)
            with self.assertRaises(ValueError):usb_ab.parse(buf.getvalue(),self.version)

if __name__=='__main__':unittest.main(verbosity=2)
