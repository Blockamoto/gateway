#!/usr/bin/env python3
"""Fetch three candidate Bitcoin blocks using a copied 0.7.8 client and new profile.

No existing Gateway runtime/profile is opened. Bodies here are NOT accepted as
field evidence until separately verified by the opt-in Go corpus test.
"""
import argparse, hashlib, importlib.util, json, os, pathlib, shutil, struct, sys, time, urllib.error

ROOT=pathlib.Path(__file__).resolve().parents[2]
BUILD=(ROOT/'build').resolve()
BINARY_SHA='a7c48db9bbfac6a77345e8c7abe1175d9ed40cd5c6238460f9a7a2228d824137'
HEADER_SHA='534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155'
HARNESS_SHA='23f325a018cac636e09e7e95112f620e7f360a98ccda63c0f7861dd3e136edb4'
CASES=[
 {'height':825334,'block_hash':'00000000000000000000ab688a4ea75513640091a019b3a469825800c1070f82','txid':'d5dca5ad2ec1cf84cf6b1dcf2592ae69b562fcff4f50d8a56370412e3c61ea2b','inscription_index':229},
 {'height':839704,'block_hash':'00000000000000000000561319cb5fb7015ac787156b219d43d18c0fcc8ce415','txid':'ab924ff229beca227bf40221faf492a20b5e2ee4f084524c84a5f98b80fe527f','inscription_index':1},
 {'height':844782,'block_hash':'00000000000000000001ba4032567e702c2331891081ff06abad397343753d28','txid':'c4c65ea6ea567b26e5adb645b569099683e7ff2b776cc846e9a9ea9f4cf85eac','inscription_index':7},
]

def sha(path):
    with path.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
def h256(b):return hashlib.sha256(hashlib.sha256(b).digest()).digest()
def copy_checked(src,dst,expected):
    assert src.is_file() and sha(src)==expected,('source digest',src)
    dst.parent.mkdir(parents=True,exist_ok=True); shutil.copyfile(src,dst); assert sha(dst)==expected

def main():
    ap=argparse.ArgumentParser(); ap.add_argument('--out',type=pathlib.Path,required=True); ap.add_argument('--deadline',type=int,default=180); args=ap.parse_args()
    out=args.out.resolve(); assert out!=BUILD and out.is_relative_to(BUILD) and not out.exists(); assert 30<=args.deadline<=600
    out.mkdir(parents=True); inputs=out/'inputs'; profile=out/'profile'; (profile/'headers').mkdir(parents=True)
    src=BUILD/'baseline-078-owned-01'/'inputs'
    copy_checked(src/'GatewayClient.exe',inputs/'GatewayClient.exe',BINARY_SHA)
    copy_checked(src/'benchmark-index-replay.py',inputs/'benchmark-index-replay.py',HARNESS_SHA)
    copy_checked(src/'headers.bin',profile/'headers'/'headers.bin',HEADER_SHA)
    for row in CASES:
        with (profile/'headers'/'headers.bin').open('rb') as f: f.seek(row['height']*80); header=f.read(80)
        assert len(header)==80 and h256(header)[::-1].hex()==row['block_hash'], ('selected header mismatch',row['height'])
    (inputs/'update-defaults.json').write_text(json.dumps({'schema':1,'update_mode':'manual','request_id':'e'*32}))
    (profile/'settings.json').write_text(json.dumps(dict(network_disabled=True,headers_paused=True,core_disabled=True,core_mount_disabled=True,serve_data=False,share_cache=False,onboarded=True,lan_discovery=False,graph_index=False,cache_blocks=False,privacy_mode=True)))
    tmp=out/'tmp'; tmp.mkdir()
    for k in ('TMP','TEMP','TMPDIR'):os.environ[k]=str(tmp)
    sys.dont_write_bytecode=True
    spec=importlib.util.spec_from_file_location('isolated_replay_harness',inputs/'benchmark-index-replay.py');h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
    runtime=h.Runtime(inputs/'GatewayClient.exe',profile,out); result={'result':'INCOMPLETE','candidate_count':len(CASES),'binary_sha256':BINARY_SHA,'header_sha256':HEADER_SHA,'fetched':[],'limitations':['independent body verification and complete envelope extraction must follow','one copied isolated 0.7.8 profile; no claim of live protocol parity']}
    try:
        runtime.start()
        update=runtime.call('/api/v1/updates/status',timeout=20)
        assert update.get('state')=='disabled' or (update.get('auto_check') is False and update.get('auto_install') is False), 'Could not verify updates disabled/manual before enabling peers'
        runtime.call('/api/v1/index/run/prepare',{'consumers':[dict(index='blocks',armed=True,live=False)]},timeout=30)
        settings=runtime.call('/api/v1/settings',timeout=30)['settings']; settings.update(network_disabled=False,core_disabled=True,core_mount_disabled=True,serve_data=False,share_cache=False,privacy_mode=True,lan_discovery=False)
        runtime.call('/api/v1/settings',settings,timeout=30)
        deadline=time.monotonic()+args.deadline
        # Peer initialization is asynchronous. Do not spend all per-block
        # attempts before the first Bitcoin session or selected headers exist.
        readiness_deadline=min(deadline,time.monotonic()+45)
        while time.monotonic()<readiness_deadline:
            network=runtime.call('/api/v1/network',timeout=10); headers=network.get('headers') or {}
            result['readiness']={'connected':network.get('connected'), 'enabled':network.get('enabled'),
                                 'header_height':headers.get('header_height'), 'header_ready':headers.get('ready')}
            if network.get('connected',0)>0 and (headers.get('header_height') or -1)>=max(x['height'] for x in CASES): break
            time.sleep(2)
        for case in CASES:
            success=None; attempts=0; failures=[]
            while time.monotonic()<deadline and attempts<4:
                attempts+=1
                try:
                    value=runtime.call('/api/v1/index/block-map?block='+str(case['height']),timeout=min(35,max(1,int(deadline-time.monotonic()))) )
                    assert value['hash']==case['block_hash'] and value['verification']['header_chain_match'], 'selected block was not verified by client'
                    success=value; break
                except (AssertionError, urllib.error.URLError, TimeoutError, OSError) as exc:
                    # This owned diagnostic may contain the server's error text;
                    # do not print peer details into the user's conversation.
                    failures.append({'type':type(exc).__name__,'detail':str(exc)[:1200]})
                    if time.monotonic()<deadline: time.sleep(1)
            if success is None:
                result['fetched'].append(dict(height=case['height'],status='unavailable',attempts=attempts,failures=failures)); continue
            catalog=profile/'indexes'/'blocks'/'segments-v3'/('catalog%08d.dat'%(case['height']//4096))
            with catalog.open('rb') as f: f.seek((case['height']%4096)*128); record=f.read(128)
            assert len(record)==128 and record[:4]==b'GWC3', 'Raw receipt not found'
            number,offset,length=struct.unpack_from('<IQI',record,76)
            assert 81<=length<=4_000_000
            body=profile/'indexes'/'blocks'/'segments-v3'/('blk%08d.dat'%number)
            with body.open('rb') as f: f.seek(offset+52); raw=f.read(length)
            assert len(raw)==length and h256(raw[:80])[::-1].hex()==case['block_hash']
            dest=out/'blocks'/(case['block_hash']+'.blk');dest.parent.mkdir(exist_ok=True)
            with dest.open('xb') as f:f.write(raw)
            result['fetched'].append(dict(case, status='fetched_unverified',attempts=attempts,bytes=length,sha256=hashlib.sha256(raw).hexdigest(),filename=str(dest.relative_to(out))))
            print('Fetched candidate height',case['height'],'into the isolated profile; independent checks pending.',flush=True)
        result['result']='FETCHED' if all(x.get('status')=='fetched_unverified' for x in result['fetched']) else 'PARTIAL'
    except Exception as error:
        result['error_type']=type(error).__name__; result['error']=str(error)[:300]
        raise
    finally:
        runtime.stop()
        (out/'acquisition-result.json').write_text(json.dumps(result,indent=2)+'\n')
        (out/'api-calls.json').write_text(json.dumps(runtime.calls,indent=2)+'\n')
    print(result['result'],len([x for x in result['fetched'] if x['status']=='fetched_unverified']),'of',len(CASES),'candidate blocks fetched.')
if __name__=='__main__':main()
