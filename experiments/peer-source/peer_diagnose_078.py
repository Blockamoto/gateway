#!/usr/bin/env python3
"""Reproduce one 0.7.8 local request in an isolated diagnostic profile."""
import argparse, hashlib, importlib.util, json, os, pathlib, shutil, struct, sys, time, urllib.error, urllib.request
ROOT=pathlib.Path(__file__).resolve().parents[2]; BUILD=(ROOT/'build').resolve()
def digest(p):
    with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
def main():
    p=argparse.ArgumentParser();p.add_argument('--out',type=pathlib.Path,required=True);p.add_argument('--wait',type=int,default=45);a=p.parse_args()
    out=a.out.resolve();assert out!=BUILD and out.is_relative_to(BUILD) and not out.exists() and 1<=a.wait<=90
    out.mkdir(parents=True); inputs=out/'inputs';profile=out/'profile';(profile/'headers').mkdir(parents=True); inputs.mkdir()
    src=BUILD/'baseline-078-owned-01'/'inputs'
    for name,sha,dest in [('GatewayClient.exe','a7c48db9bbfac6a77345e8c7abe1175d9ed40cd5c6238460f9a7a2228d824137',inputs/'GatewayClient.exe'),('benchmark-index-replay.py','23f325a018cac636e09e7e95112f620e7f360a98ccda63c0f7861dd3e136edb4',inputs/'benchmark-index-replay.py'),('headers.bin','534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155',profile/'headers'/'headers.bin')]:
        assert digest(src/name)==sha;shutil.copyfile(src/name,dest);assert digest(dest)==sha
    (inputs/'update-defaults.json').write_text(json.dumps({'schema':1,'update_mode':'manual','request_id':'f'*32}))
    (profile/'settings.json').write_text(json.dumps(dict(network_disabled=True,headers_paused=True,core_disabled=True,core_mount_disabled=True,serve_data=False,share_cache=False,onboarded=True,lan_discovery=False,graph_index=False,cache_blocks=False,privacy_mode=True)))
    tmp=out/'tmp';tmp.mkdir()
    for name in ('TMP','TEMP','TMPDIR'):os.environ[name]=str(tmp)
    sys.dont_write_bytecode=True;spec=importlib.util.spec_from_file_location('owned_harness',inputs/'benchmark-index-replay.py');h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
    rt=h.Runtime(inputs/'GatewayClient.exe',profile,out); report={'result':'INCOMPLETE','request':'/api/v1/index/block-map?block=825334','readiness':[]}
    try:
        rt.start(); u=rt.call('/api/v1/updates/status',timeout=20)
        assert u.get('state')=='disabled' or (u.get('auto_check') is False and u.get('auto_install') is False), 'updates could not be verified manual'
        rt.call('/api/v1/index/run/prepare',{'consumers':[dict(index='blocks',armed=True,live=False)]},timeout=30)
        settings=rt.call('/api/v1/settings')['settings'];settings.update(network_disabled=False,core_disabled=True,core_mount_disabled=True,serve_data=False,share_cache=False,privacy_mode=True,lan_discovery=False)
        rt.call('/api/v1/settings',settings,timeout=30)
        start=time.monotonic();last={}
        while time.monotonic()-start<a.wait:
            n=rt.call('/api/v1/network',timeout=10); headers=n.get('headers') or {}
            last={'seconds':round(time.monotonic()-start,2),'enabled':n.get('enabled'),'connected':n.get('connected'),'bootstrap_running':n.get('bootstrap_running'),'bootstrap_error_present':bool(n.get('bootstrap_error')),'header_height':headers.get('header_height'),'header_count':headers.get('header_count'),'header_ready':headers.get('ready')}
            report['readiness'].append(last)
            if n.get('connected',0)>0 and (headers.get('header_height') or -1)>=825334:break
            time.sleep(2)
        before=rt.call('/api/v1/network',timeout=10); report['download_before']=before.get('block_download')
        request=urllib.request.Request(rt.endpoint+report['request'],headers={'Origin':rt.endpoint})
        try:r=urllib.request.urlopen(request,timeout=45)
        except urllib.error.HTTPError as exc:r=exc
        with r:
            raw=r.read((16<<20)+1);report['response_http_status']=r.status
        assert len(raw)<=16<<20, 'local response exceeds handler limit'
        try:report['response_json']=json.loads(raw)
        except (ValueError,UnicodeDecodeError):report['response_text']=raw.decode('utf-8','replace')
        after=rt.call('/api/v1/network',timeout=10); report['download_after']=after.get('block_download')
        report['connected_after']=after.get('connected'); report['gateway_locked']=after.get('gateway_locked')
        if report['response_http_status']==200:
            value=report['response_json']; verification=value['verification']
            assert value['hash']=='00000000000000000000ab688a4ea75513640091a019b3a469825800c1070f82'
            assert verification['header_chain_match'] and verification['merkle_root'] and verification['witness_commitment'] and not verification['consensus_validated']
            height=825334;root=profile/'indexes'/'blocks'/'segments-v3';catalog=root/('catalog%08d.dat'%(height//4096))
            with catalog.open('rb') as f:f.seek((height%4096)*128);row=f.read(128)
            assert len(row)==128 and row[:4]==b'GWC3';number,offset,length=struct.unpack_from('<IQI',row,76)
            with (root/('blk%08d.dat'%number)).open('rb') as f:f.seek(offset+52);body=f.read(length)
            assert len(body)==length
            report['retained_body_sha256']=hashlib.sha256(body).hexdigest();report['retained_body_bytes']=len(body)
            report['matches_independently_checked_http_fixture']=report['retained_body_sha256']=='4ab2f4938c1e4692c1d4d9adf712b28ecfc70117a2e36ef954de6f1691239ee7'
            assert report['matches_independently_checked_http_fixture']
        report['response_bytes']=len(raw);report['result']='OBSERVED'
        print('Readiness:',json.dumps(last),'HTTP status',report['response_http_status'],flush=True)
    except Exception as exc:
        report['error_type']=type(exc).__name__;report['error']=str(exc)[:400];raise
    finally:
        rt.stop();(out/'diagnostic.json').write_text(json.dumps(report,indent=2)+'\n')
        (out/'api-calls.json').write_text(json.dumps(rt.calls,indent=2)+'\n')
if __name__=='__main__':main()
