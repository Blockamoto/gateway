#!/usr/bin/env python3
"""Audit built PE resources and exact installer payload equality; stdlib only.

This checks file structure, not execution or trust/signing on Windows.
"""
from __future__ import annotations
import argparse, hashlib, json, struct
from pathlib import Path

def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()

def inspect_version(data: bytes) -> dict:
    strings={}
    def walk(at,limit):
        length,size,kind=struct.unpack_from('<HHH',data,at)
        end=at+length
        if length<6 or end>limit:raise ValueError('Invalid version block bounds')
        pos=at+6;key_start=pos
        while pos+2<=end and data[pos:pos+2]!=b'\0\0':pos+=2
        if pos+2>end:raise ValueError('Unterminated version key')
        key=data[key_start:pos].decode('utf-16le');pos=(pos+2+3)&~3
        value_end=pos+size*(2 if kind else 1)
        if value_end>end:raise ValueError('Invalid version value bounds')
        value=data[pos:value_end]
        if kind and size:strings[key]=value.decode('utf-16le').rstrip('\0')
        if key=='VS_VERSION_INFO':
            if len(value)!=52 or struct.unpack_from('<I',value)[0]!=0xFEEF04BD:raise ValueError('Invalid fixed file info')
        pos=(value_end+3)&~3
        while pos<end:pos=(walk(pos,end)+3)&~3
        return end
    walk(0,len(data))
    return strings

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
    if not resource_rva and not resource_size:
        return {'machine':'amd64','subsystem':struct.unpack_from('<H',data,optional+68)[0],
                'resource_size':0,'authenticode_present':bool(certificate_offset and certificate_size),'leaves':{}}
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
    version=(root/'VERSION.txt').read_text().strip()
    installer_name=f'gateway-client-v{version}-installer.exe'
    ico=(root/'ui/shell/gateway.ico').read_bytes()
    _,kind,count=struct.unpack_from('<HHH',ico);assert kind==1 and count==7
    images={}
    for i in range(count):
        w,h,colors,reserved,planes,bits,size,at=struct.unpack_from('<BBBBHHII',ico,6+16*i)
        images[i+1]=ico[at:at+size]
    results=[]
    for name,expected_subsystem in [('GatewayClient.exe',3),('GatewayOnDemand.exe',2),('GatewayNativeHost.exe',3),('GatewayUpdateHelper.exe',2),(installer_name,2)]:
        raw=(args.build/name).read_bytes();pe=inspect_pe(raw);leaves=pe.pop('leaves')
        assert pe['subsystem']==expected_subsystem,(name,'subsystem')
        for identifier,payload in images.items():assert leaves[(3,identifier,1033)]==payload,(name,identifier,'icon mismatch')
        group=leaves[(14,101,1033)]
        assert struct.unpack_from('<HHH',group)==(0,1,count)
        metadata=inspect_version(leaves[(16,1,1033)])
        assert metadata['ProductName']=='Gateway' and metadata['FileVersion']==version and metadata['ProductVersion']==version,(name,'version metadata')
        assert metadata['OriginalFilename']==name and metadata['FileDescription'],(name,'file identity')
        assert not pe['authenticode_present']
        results.append({'file':name,'bytes':len(raw),'sha256':sha(raw),**pe,'version_info':metadata,'icon_images':7,'icon_group':101,'icon_pixels_equal_checked_in_assets':True})
    installer=(args.build/installer_name).read_bytes()
    exact=[]
    for name in ('GatewayClient.exe','GatewayOnDemand.exe','GatewayNativeHost.exe','GatewayUpdateHelper.exe'):
        payload=(args.build/name).read_bytes();position=installer.find(payload)
        assert position>=0,(name,'installer mismatch')
        exact.append({'file':name,'sha256':sha(payload),'installer_byte_offset':position,'exact_match':True})
    compatibility=(root/'COMPATIBILITY.json').read_bytes();position=installer.find(compatibility)
    assert position>=0,('COMPATIBILITY.json','installer metadata mismatch')
    exact.append({'file':'COMPATIBILITY.json','sha256':sha(compatibility),'installer_byte_offset':position,'exact_match':True})
    guide_name = f'TESTING-{version}.md'
    guide = (root/'docs'/guide_name).read_bytes(); position = installer.find(guide)
    assert position >= 0, (guide_name, 'installer tester guide mismatch')
    exact.append({'file':guide_name,'sha256':sha(guide),'installer_byte_offset':position,'exact_match':True})
    for name in ('LICENSE', 'THIRD-PARTY-NOTICES.txt', 'GO-LICENSE.txt'):
        notice = (root/name).read_bytes(); position = installer.find(notice)
        assert notice and position >= 0, (name, 'installer license or notice mismatch')
        exact.append({'file':name,'sha256':sha(notice),'installer_byte_offset':position,'exact_match':True})
    companion=[]
    for path in sorted((root/'browser-companion').iterdir()):
        if path.is_file():
            payload=path.read_bytes();position=installer.find(payload)
            assert position>=0,(path.name,'companion mismatch')
            companion.append({'file':path.name,'sha256':sha(payload),'exact_match':True})
    linux=(args.build/'gateway-client').read_bytes();assert linux[:4]==b'\x7fELF'
    linux_helper=(args.build/'gateway-update-helper').read_bytes();assert linux_helper[:4]==b'\x7fELF'
    baseline=(args.build/'bootstrap/headers-mainnet.bin').read_bytes()
    baseline_metadata=(args.build/'bootstrap/headers-mainnet.json').read_bytes()
    metadata=json.loads(baseline_metadata)
    assert len(baseline)%80==0 and metadata['count']==len(baseline)//80
    if baseline:
        assert metadata['sha256']==sha(baseline), 'Header sidecar digest mismatch'
        assert installer.find(baseline)>=0, 'Fresh installer missing exact header sidecar'
        assert installer.find(baseline_metadata)>=0, 'Fresh installer missing header metadata'
        for name in ('GatewayClient.exe','gateway-client'):
            assert (args.build/name).read_bytes().find(baseline)<0, (name,'snapshot must remain external')
    headers={'bytes':len(baseline),'count':metadata['count'],'sha256':sha(baseline),
             'fresh_installer_payload_verified':bool(baseline),'external_to_client':bool(baseline)}
    result={'status':'PASS','kind':'Binary structure and exact payload audit. Not native Windows runtime verification.',
            'windows':results,'installer_payloads':exact,'installer_companion_files':companion,
            'linux':{'file':'gateway-client','bytes':len(linux),'sha256':sha(linux),'format':'ELF'},
            'linux_helper':{'file':'gateway-update-helper','bytes':len(linux_helper),'sha256':sha(linux_helper),'format':'ELF'},
            'headers':headers,'windows_runtime_tested':False,'authenticode_signed':False}
    args.out.parent.mkdir(parents=True,exist_ok=True);args.out.write_text(json.dumps(result,indent=2)+'\n')
    print('PASS: five Windows PE icon and version resources, exact installer executable/compatibility/companion payloads; client and helper ELF present.')
if __name__=='__main__':main()
