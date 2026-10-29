#!/usr/bin/env python3
"""Build reproducible Windows icon resources, with no resource compiler required.

PNG/ICO inputs are checked-in assets. The generated COFF has one .rsrc section
and IMAGE_REL_AMD64_ADDR32NB relocations for each resource data entry. Go links
*_windows_amd64.syso only into Windows amd64 binaries. This script uses stdlib.
"""
from __future__ import annotations
import argparse,re,struct
from pathlib import Path

SIZES=(16,24,32,48,64,128,256)

def version_block(key: str, value: bytes=b'', children: bytes=b'', text: bool=False) -> bytes:
    data=bytearray(b'\0'*6+key.encode('utf-16le')+b'\0\0')
    while len(data)%4:data.append(0)
    data.extend(value)
    if children:
        while len(data)%4:data.append(0)
        data.extend(children)
    struct.pack_into('<HHH',data,0,len(data),len(value)//2 if text else len(value),int(text))
    return bytes(data)

def version_info(version: str, filename: str, description: str) -> bytes:
    if not re.fullmatch(r'\d+\.\d+\.\d+',version):
        raise ValueError('Windows file version requires major.minor.patch')
    major,minor,patch=map(int,version.split('.'))
    if max(major,minor,patch)>65535:raise ValueError('Windows version component exceeds 65535')
    ms,ls=(major<<16)|minor,patch<<16
    fixed=struct.pack('<13I',0xFEEF04BD,0x10000,ms,ls,ms,ls,0x3f,0,0x40004,1,0,0,0)
    # Product metadata is descriptive, not a verified publisher identity.
    fields={'FileDescription':description,'FileVersion':version,'ProductName':'Gateway',
            'ProductVersion':version,'OriginalFilename':filename,'InternalName':Path(filename).stem}
    strings=bytearray()
    for key,value in fields.items():
        while len(strings)%4:strings.append(0)
        strings.extend(version_block(key,(value+'\0').encode('utf-16le'),text=True))
    table=version_block('StringFileInfo',children=version_block('040904b0',children=bytes(strings),text=True),text=True)
    while len(table)%4:table+=b'\0'
    translation=version_block('VarFileInfo',children=version_block('Translation',struct.pack('<HH',0x409,1200)),text=True)
    return version_block('VS_VERSION_INFO',fixed,table+translation)

def resource_object(icon: bytes, version: str, filename: str, description: str) -> bytes:
    reserved,kind,count=struct.unpack_from('<HHH',icon)
    if (reserved,kind)!=(0,1) or not 1<=count<=32:
        raise ValueError('Expected a bounded Windows icon file')
    images=[];group=bytearray(struct.pack('<HHH',0,1,count))
    for i in range(count):
        width,height,colors,reserved,planes,bits,size,offset=struct.unpack_from('<BBBBHHII',icon,6+16*i)
        image=icon[offset:offset+size]
        if len(image)!=size:raise ValueError('Truncated icon image')
        images.append((i+1,image))
        group.extend(struct.pack('<BBBBHHIH',width,height,colors,0,planes,bits,size,i+1))
    # Directory offsets are section-relative; high bits mark child directories.
    types=[(3,images),(14,[(101,bytes(group))]),(16,[(1,version_info(version,filename,description))])]
    data=bytearray();leaves=[]
    def alloc(size):
        while len(data)%4:data.append(0)
        offset=len(data);data.extend(bytes(size));return offset
    def directory(entries):
        at=alloc(16+8*entries);struct.pack_into('<IIHHHH',data,at,0,0,0,0,0,entries);return at
    root=directory(len(types))
    for i,(resource_type,items) in enumerate(types):
        td=directory(len(items));struct.pack_into('<II',data,root+16+8*i,resource_type,0x80000000|td)
        for j,(identifier,payload) in enumerate(items):
            ld=directory(1);struct.pack_into('<II',data,td+16+8*j,identifier,0x80000000|ld)
            entry=alloc(16);struct.pack_into('<II',data,ld+16,1033,entry)
            leaves.append((entry,payload))
    relocs=[]
    for entry,payload in leaves:
        offset=alloc(len(payload));data[offset:offset+len(payload)]=payload
        struct.pack_into('<IIII',data,entry,offset,len(payload),0,0)
        relocs.append(struct.pack('<IIH',entry,0,3)) # .rsrc + section-relative addend
    raw_ptr=60;reloc_ptr=raw_ptr+len(data);sym_ptr=reloc_ptr+10*len(relocs)
    header=struct.pack('<HHIIIHH',0x8664,1,0,sym_ptr,1,0,0)
    section=struct.pack('<8sIIIIIIHHI',b'.rsrc\0\0\0',0,0,len(data),raw_ptr,reloc_ptr,0,len(relocs),0,0x40300040)
    symbol=struct.pack('<8sIhHBB',b'.rsrc\0\0\0',0,1,0,3,0)
    return header+section+data+b''.join(relocs)+symbol+struct.pack('<I',4)

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--out',type=Path);args=ap.parse_args()
    root=Path(__file__).resolve().parents[1]
    version=(root/'VERSION.txt').read_text().strip()
    targets=[(root/'gateway_windows_amd64.syso','GatewayClient.exe','Gateway Client'),
             (root/'packaging/gateway-launcher/gateway_windows_amd64.syso','GatewayOnDemand.exe','Gateway desktop launcher'),
             (root/'packaging/native-host/gateway_windows_amd64.syso','GatewayNativeHost.exe','Gateway browser companion host'),
             (root/'packaging/update-helper/gateway_windows_amd64.syso','GatewayUpdateHelper.exe','Gateway update installer'),
             (root/'packaging/windows-installer/gateway_windows_amd64.syso',f'gateway-client-v{version}-installer.exe','Gateway Setup')]
    if args.out:targets=[(args.out,'GatewayClient.exe','Gateway Client')]
    icon=(root/'ui/shell/gateway.ico').read_bytes()
    for path,filename,description in targets:
        path.parent.mkdir(parents=True,exist_ok=True)
        path.write_bytes(resource_object(icon,version,filename,description))
    print('Windows icon and version resources:', ', '.join(str(p) for p,_,_ in targets))

if __name__=='__main__':main()
