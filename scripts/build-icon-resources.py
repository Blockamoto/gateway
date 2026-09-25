#!/usr/bin/env python3
"""Build reproducible Windows icon resources, with no resource compiler required.

PNG/ICO inputs are checked-in assets. The generated COFF has one .rsrc section
and IMAGE_REL_AMD64_ADDR32NB relocations for each resource data entry. Go links
*_windows_amd64.syso only into Windows amd64 binaries. This script uses stdlib.
"""
from __future__ import annotations
import argparse,struct
from pathlib import Path

SIZES=(16,24,32,48,64,128,256)

def resource_object(icon: bytes) -> bytes:
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
    types=[(3,images),(14,[(101,bytes(group))])]
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
    result=resource_object((root/'ui/shell/gateway.ico').read_bytes())
    targets=[args.out] if args.out else [root/'gateway_windows_amd64.syso',root/'packaging/gateway-launcher/gateway_windows_amd64.syso',root/'packaging/native-host/gateway_windows_amd64.syso']
    for path in targets:path.parent.mkdir(parents=True,exist_ok=True);path.write_bytes(result)
    print('Windows icon COFF resources:', ', '.join(str(p) for p in targets))

if __name__=='__main__':main()
