#!/usr/bin/env python3
"""Audit built PE resources and exact installer payload equality; stdlib only.

This checks file structure, not execution or trust/signing on Windows.
"""
from __future__ import annotations
import argparse, hashlib, json, struct
from pathlib import Path

def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()

def inspect_pe(data: bytes) -> dict:
    if data[:2] != b'MZ':
        raise ValueError('Not an MZ executable')
    pe = struct.unpack_from('<I', data, 0x3c)[0]
    if data[pe:pe+4] != b'PE\x00\x00':
        raise ValueError('Missing PE signature')
    machine, sections = struct.unpack_from('<HH', data, pe+4)
    optional_size = struct.unpack_from('<H', data, pe+20)[0]
    optional = pe+24
    if machine != 0x8664 or struct.unpack_from('<H', data, optional)[0] != 0x20b:
        raise ValueError('Expected Windows amd64 PE32+')
    directory = optional+112
    resource_rva, resource_size = struct.unpack_from('<II', data, directory+16)
    certificate_offset, certificate_size = struct.unpack_from('<II', data, directory+32)
    table = optional+optional_size
    section_list=[]
    for i in range(sections):
        at=table+40*i
        virtual_size,rva,raw_size,raw_offset=struct.unpack_from('<IIII',data,at+8)
        section_list.append((rva,max(virtual_size,raw_size),raw_offset,raw_size))
    def offset(rva: int) -> int:
        for start,size,raw,raw_size in section_list:
            if start <= rva < start+size:
                delta=rva-start
                if delta >= raw_size: raise ValueError('Unbacked resource RVA')
                return raw+delta
        raise ValueError('Resource RVA outside sections')
    base=offset(resource_rva);leaves={}
    def walk(relative: int, keys: tuple=()) -> None:
        if len(keys)>3: raise ValueError('Unexpected resource depth')
        at=base+relative;named,ids=struct.unpack_from('<HH',data,at+12)
        if named+ids>1000:raise ValueError('Unbounded resource directory')
        for i in range(named+ids):
            name,target=struct.unpack_from('<II',data,at+16+8*i)
            if name & 0x80000000:raise ValueError('Unexpected named icon resource')
            path=keys+(name,)
            if target & 0x80000000:walk(target & 0x7fffffff,path)
            else:
                rva,size,_,_=struct.unpack_from('<IIII',data,base+target)
                start=offset(rva);payload=data[start:start+size]
                if len(payload)!=size:raise ValueError('Truncated PE resource')
                leaves[path]=payload
    walk(0)
    return {'machine':'amd64','subsystem':struct.unpack_from('<H',data,optional+68)[0],
            'resource_size':resource_size,'authenticode_present':bool(certificate_offset and certificate_size),
            'leaves':leaves}

def main() -> None:
    ap=argparse.ArgumentParser();ap.add_argument('--build',type=Path,required=True);ap.add_argument('--out',type=Path,required=True);args=ap.parse_args()
    root=Path(__file__).resolve().parents[1]
    ico=(root/'ui/shell/gateway.ico').read_bytes()
    _,kind,count=struct.unpack_from('<HHH',ico);assert kind==1 and count==7
    images={}
    for i in range(count):
        w,h,colors,reserved,planes,bits,size,at=struct.unpack_from('<BBBBHHII',ico,6+16*i)
        images[i+1]=ico[at:at+size]
    results=[]
    for name,expected_subsystem in [('GatewayClient.exe',3),('GatewayOnDemand.exe',2),('GatewayNativeHost.exe',3),('gateway-client-v0.5.4-installer.exe',2)]:
        raw=(args.build/name).read_bytes();pe=inspect_pe(raw);leaves=pe.pop('leaves')
        assert pe['subsystem']==expected_subsystem,(name,'subsystem')
        for identifier,payload in images.items():assert leaves[(3,identifier,1033)]==payload,(name,identifier,'icon mismatch')
        group=leaves[(14,101,1033)]
        assert struct.unpack_from('<HHH',group)==(0,1,count)
        assert not pe['authenticode_present']
        results.append({'file':name,'bytes':len(raw),'sha256':sha(raw),**pe,'icon_images':7,'icon_group':101,'icon_pixels_equal_checked_in_assets':True})
    installer=(args.build/'gateway-client-v0.5.4-installer.exe').read_bytes()
    exact=[]
    for name in ('GatewayClient.exe','GatewayOnDemand.exe','GatewayNativeHost.exe'):
        payload=(args.build/name).read_bytes();position=installer.find(payload)
        assert position>=0,(name,'installer mismatch')
        exact.append({'file':name,'sha256':sha(payload),'installer_byte_offset':position,'exact_match':True})
    companion=[]
    for path in sorted((root/'browser-companion').iterdir()):
        if path.is_file():
            payload=path.read_bytes();position=installer.find(payload)
            assert position>=0,(path.name,'companion mismatch')
            companion.append({'file':path.name,'sha256':sha(payload),'exact_match':True})
    linux=(args.build/'gateway-client').read_bytes();assert linux[:4]==b'\x7fELF'
    result={'status':'PASS','kind':'Binary structure and exact payload audit. Not native Windows runtime verification.',
            'windows':results,'installer_payloads':exact,'installer_companion_files':companion,
            'linux':{'file':'gateway-client','bytes':len(linux),'sha256':sha(linux),'format':'ELF'},
            'windows_runtime_tested':False,'authenticode_signed':False}
    args.out.parent.mkdir(parents=True,exist_ok=True);args.out.write_text(json.dumps(result,indent=2)+'\n')
    print('PASS: four Windows PE resource trees, seven icon images each, exact installer executable and companion payloads; ELF present.')
if __name__=='__main__':main()
