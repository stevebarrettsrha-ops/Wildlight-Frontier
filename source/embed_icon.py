#!/usr/bin/env python3
"""Embed an ICO in a fresh, unsigned PE32+ Go launcher (no build-time packages).
Usage: python3 embed_icon.py PATH_TO_EXE PATH_TO_ICO
Refuses signed files and existing resources; preserves every original section.
Formats: https://learn.microsoft.com/en-us/windows/win32/debug/pe-format
https://devblogs.microsoft.com/oldnewthing/20120720-00/?p=7083/
"""
from pathlib import Path
import struct,sys

def align(n,a):
    return (n+a-1)//a*a

def icon_resources(ico,rva):
    reserved,kind,count=struct.unpack_from('<HHH',ico)
    if reserved or kind!=1 or not 1<=count<=32:raise ValueError('Invalid ICO header')
    images=[];group=bytearray(struct.pack('<HHH',0,1,count))
    for i in range(count):
        width,height,colors,reserved,planes,bits,size,offset=struct.unpack_from('<BBBBHHII',ico,6+16*i)
        if offset+size>len(ico):raise ValueError('Truncated ICO')
        payload=ico[offset:offset+size];images.append(payload)
        group.extend(struct.pack('<BBBBHHIH',width,height,colors,0,planes or 1,bits or 32,size,i+1))
    # Resource tree: type -> id -> neutral language -> IMAGE_RESOURCE_DATA_ENTRY.
    resources={3:{i+1:{0:img} for i,img in enumerate(images)},14:{1:{0:bytes(group)}}}
    data=bytearray();leaves=[]
    def alloc(n):
        start=len(data);data.extend(b'\0'*n);return start
    def directory(entries):
        here=alloc(16+8*len(entries));struct.pack_into('<IIHHHH',data,here,0,0,0,0,0,len(entries))
        for i,(key,value) in enumerate(sorted(entries.items())):
            entry=here+16+8*i
            if isinstance(value,dict):child=directory(value)|0x80000000
            else:child=alloc(16);leaves.append((child,value))
            struct.pack_into('<II',data,entry,key,child)
        return here
    assert directory(resources)==0
    for entry,payload in leaves:
        alloc(align(len(data),4)-len(data));offset=alloc(len(payload));data[offset:offset+len(payload)]=payload
        struct.pack_into('<IIII',data,entry,rva+offset,len(payload),0,0)
    return data

def checksum(data,skip):
    total=0
    for i in range(0,len(data),2):
        if skip<=i<skip+4:continue
        total+=int.from_bytes(data[i:i+2],'little');total=(total&65535)+(total>>16)
    total=(total&65535)+(total>>16)
    return total+len(data)

def embed(exe,ico):
    old=exe.read_bytes();b=bytearray(old)
    if b[:2]!=b'MZ':raise ValueError('Not a Windows executable')
    pe=struct.unpack_from('<I',b,60)[0]
    if b[pe:pe+4]!=b'PE\0\0':raise ValueError('Missing PE signature')
    machine,count=struct.unpack_from('<HH',b,pe+4);opt=pe+24;optlen=struct.unpack_from('<H',b,pe+20)[0]
    if machine not in (0x8664,0xaa64) or struct.unpack_from('<H',b,opt)[0]!=0x20b:raise ValueError('Expected x64 or ARM64 PE32+')
    dirs=opt+112
    if struct.unpack_from('<II',b,dirs+2*8)!=(0,0):raise ValueError('Existing resources: rebuild a fresh launcher first')
    if struct.unpack_from('<II',b,dirs+4*8)!=(0,0):raise ValueError('Refusing to modify a signed executable')
    sa,fa=struct.unpack_from('<II',b,opt+32);header_size=struct.unpack_from('<I',b,opt+60)[0]
    table=opt+optlen;new_header=table+40*count
    if new_header+40>header_size or any(b[new_header:new_header+40]):raise ValueError('No unused section-header slot')
    sections=[struct.unpack_from('<8sIIII',b,table+40*i) for i in range(count)]
    rva=align(max(va+max(vs,raw_size) for _,vs,va,raw_size,_ in sections),sa)
    raw=align(len(b),fa);data=icon_resources(ico.read_bytes(),rva);raw_size=align(len(data),fa)
    b.extend(b'\0'*(raw-len(b)));b.extend(data);b.extend(b'\0'*(raw_size-len(data)))
    struct.pack_into('<8sIIIIIIHHI',b,new_header,b'.rsrc\0\0\0',len(data),rva,raw_size,raw,0,0,0,0,0x40000040)
    struct.pack_into('<H',b,pe+6,count+1)
    initialized=struct.unpack_from('<I',b,opt+8)[0];struct.pack_into('<I',b,opt+8,initialized+raw_size)
    struct.pack_into('<I',b,opt+56,align(rva+len(data),sa));struct.pack_into('<II',b,dirs+16,rva,len(data))
    struct.pack_into('<I',b,opt+64,0);struct.pack_into('<I',b,opt+64,checksum(b,opt+64))
    for _,_,_,size,offset in sections:assert b[offset:offset+size]==old[offset:offset+size]
    temp=exe.with_suffix('.icon-tmp');temp.write_bytes(b);temp.chmod(exe.stat().st_mode);temp.replace(exe)
    print(f'{exe.name}: {len(data):,} icon-resource bytes embedded; original sections preserved.')

if __name__=='__main__':
    if len(sys.argv)!=3:raise SystemExit(__doc__)
    embed(Path(sys.argv[1]),Path(sys.argv[2]))
