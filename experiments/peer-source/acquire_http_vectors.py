#!/usr/bin/env python3
"""Bounded public-HTTP fallback for inscription fixtures; verification is separate."""
import argparse, hashlib, importlib.util, json, pathlib, shutil, sys, urllib.request
ROOT=pathlib.Path(__file__).resolve().parents[2]
BUILD=(ROOT/'build').resolve()
ORIGIN='https://mempool.space'
HEADER_SHA='534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155'
def sha(p):
    with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
def h256(b):return hashlib.sha256(hashlib.sha256(b).digest()).digest()
def main():
    p=argparse.ArgumentParser();p.add_argument('--out',type=pathlib.Path,required=True);p.add_argument('--timeout',type=int,default=25);a=p.parse_args()
    out=a.out.resolve();assert out!=BUILD and out.is_relative_to(BUILD) and not out.exists() and 1<=a.timeout<=60
    out.mkdir(parents=True); inputs=out/'inputs'; inputs.mkdir(); headers=BUILD/'baseline-078-owned-01'/'inputs'/'headers.bin'
    assert sha(headers)==HEADER_SHA;shutil.copyfile(headers,inputs/'headers.bin');assert sha(inputs/'headers.bin')==HEADER_SHA
    sys.dont_write_bytecode=True;spec=importlib.util.spec_from_file_location('peer_vectors',pathlib.Path(__file__).with_name('acquire_vectors.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
    report={'result':'PARTIAL','transport':'public-https-fixture-fallback','origin':ORIGIN,'documentation':'https://mempool.space/docs/api/rest','header_sha256':HEADER_SHA,'fetched':[],'limitations':['HTTP transport is not ordinary Bitcoin peer acquisition','Go block and complete witness verification must follow before use as evidence']}
    try:
        for case in m.CASES:
            url=ORIGIN+'/api/block/'+case['block_hash']+'/raw'
            try:
                req=urllib.request.Request(url,headers={'User-Agent':'Gateway-isolated-fixture-check/0.8'})
                with urllib.request.urlopen(req,timeout=a.timeout) as response:
                    assert response.status==200 and response.geturl()==url,'unexpected HTTP response or redirect'
                    raw=response.read(4_000_001)
                assert 81<=len(raw)<=4_000_000,'invalid block length'
                with (inputs/'headers.bin').open('rb') as f:f.seek(case['height']*80);header=f.read(80)
                assert raw[:80]==header and h256(header)[::-1].hex()==case['block_hash'],'pinned header mismatch'
                dest=out/'blocks'/(case['block_hash']+'.blk');dest.parent.mkdir(exist_ok=True)
                with dest.open('xb') as f:f.write(raw)
                report['fetched'].append(dict(case,status='fetched_unverified',url=url,bytes=len(raw),sha256=hashlib.sha256(raw).hexdigest(),filename=str(dest.relative_to(out))))
                print('HTTP fixture fetched at height',case['height'],'with pinned header; body/witness validation pending.',flush=True)
            except Exception as exc:
                report['fetched'].append(dict(case,status='unavailable',error_type=type(exc).__name__))
                print('HTTP fixture unavailable at height',case['height'],'(',type(exc).__name__,').',flush=True)
        if len(report['fetched'])==3 and all(x['status']=='fetched_unverified' for x in report['fetched']):report['result']='FETCHED'
    finally:
        (out/'acquisition-result.json').write_text(json.dumps(report,indent=2)+'\n')
    print(report['result'])
if __name__=='__main__':main()
