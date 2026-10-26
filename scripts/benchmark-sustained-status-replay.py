"""Owned synthetic-history / authenticated-block replay. No user profiles."""
import argparse, hashlib, importlib.util, json, shutil, threading, time, urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('gateway_replay', ROOT / 'scripts/benchmark-index-replay.py')
h = importlib.util.module_from_spec(spec); spec.loader.exec_module(h)
h.IDS = ('tx-locator', 'bitmap-districts-standard-v1')

def main():
    ap = argparse.ArgumentParser(); ap.add_argument('--binary', type=Path, required=True); ap.add_argument('--out', type=Path, required=True); ap.add_argument('--fixture', type=Path, required=True); ap.add_argument('--points', type=Path, required=True); ap.add_argument('--gate', type=Path); args=ap.parse_args()
    binary=args.binary.resolve(strict=True); out=args.out.resolve(); assert not out.exists(); out.mkdir()
    fixture=args.fixture.resolve(strict=True); points=args.points.resolve(strict=True)
    manifest, headers, rows = h.validate_fixture(fixture, 32)
    archive=out/'archive'; archive.mkdir()
    for row in rows:
        target=archive/'blocks'/row['block_hash'][:2]/row['block_hash']; target.parent.mkdir(parents=True,exist_ok=True)
        shutil.copyfile(fixture/row['filename'],target.with_suffix('.blk'))
        h.write(target.with_suffix('.json'),dict(schema=1,verifier_version=2,hash=row['block_hash'],height=row['height'],sha256=row['sha256'],bytes=row['bytes']))
    profile=out/'profile'; (profile/'headers').mkdir(parents=True); shutil.copyfile(headers, profile/'headers/headers.bin')
    dest=profile/'indexes/browse/tx-locator'; dest.parent.mkdir(parents=True); shutil.copytree(points,dest)
    h.write(profile/'settings.json', dict(network_disabled=True, headers_paused=True, core_disabled=True, core_mount_disabled=True, serve_data=False, share_cache=False, onboarded=True, lan_discovery=False, graph_index=False, cache_blocks=False, privacy_mode=True, archive_dir=str(archive)))
    point_files=list((points/'commits').glob('*.json')); assert len(point_files)==3000
    point_inventory=h.inventory(points)
    example=h.read_json(point_files[0])['transactions']['block']
    result=dict(binary=str(binary), binary_sha256=h.sha(binary), manifest_sha256=h.sha(fixture/'manifest.json'), header_sha256=h.sha(headers), point_fixture_sha256=hashlib.sha256(h.canonical(point_inventory)).hexdigest(), points=3000, point_files=len(point_files), point_commit_bytes=sum(x.stat().st_size for x in point_files), synthetic_rows_per_point=example['tx count'], blocks=32, actual_transactions=sum(r['transactions'] for r in rows), profile=str(profile), scopes='synthetic header-anchored browse facts; real Bitcoin bodies; no peer networking')
    rt=h.Runtime(binary, profile, out); poll=[]; samples=[]; errors=[]
    try:
        rt.start()
        cold=time.perf_counter(); rt.call('/api/v1/index/status',timeout=600); result['entry_status_seconds']=time.perf_counter()-cold
        req={'in':rows[0]['height'], 'out':rows[-1]['height'], 'in_hash':rows[0]['block_hash'], 'out_hash':rows[-1]['block_hash'], 'consumers':[dict(index=i,armed=True,live=False,mode='full' if i=='tx-locator' else 'lean') for i in h.IDS]}
        plan=rt.call('/api/v1/index/run/plan',req,timeout=600); h.write(out/'plan.json',plan); assert plan['ready'],plan
        if args.gate:
            print('READY_FOR_TIMED_PHASE', flush=True)
            deadline=time.monotonic()+600
            while not args.gate.exists():
                assert time.monotonic()<deadline,'timing gate expired'; time.sleep(.25)
        pre=h.process_metrics(rt.process); started=time.perf_counter()
        run_id=rt.call('/api/v1/index/run/start',dict(request=req),timeout=600)['run']['id']
        def ui():
            try:
                def get(path):
                    request=urllib.request.Request(rt.endpoint+path,headers={'Origin':rt.endpoint})
                    with urllib.request.urlopen(request,timeout=600) as response:
                        raw=response.read(); assert response.status==200
                    return json.loads(raw),len(raw)
                first=True
                while True:
                    path='/api/v1/index/status' if first else '/api/v1/index/progress'
                    first=False; begin=time.perf_counter(); doc,size=get(path)
                    entry=dict(path=path,started_seconds=begin-started,seconds=time.perf_counter()-begin,bytes=size,run_state=doc.get('run',{}).get('run',{}).get('state'),observation=doc.get('observation'))
                    poll.append(entry)
                    if entry['run_state'] in ('complete','complete_with_errors','failed','cancelled','error','rebuild_required'):
                        if path.endswith('/progress'):
                            begin=time.perf_counter(); _,size=get('/api/v1/index/status'); poll.append(dict(path='/api/v1/index/status',started_seconds=begin-started,seconds=time.perf_counter()-begin,bytes=size,terminal=True))
                        break
                    time.sleep(1)
            except Exception as exc: errors.append(repr(exc))
        worker=threading.Thread(target=ui); worker.start()
        while True:
            run=rt.call('/api/v1/index/run',timeout=120)['run']; assert run['id']==run_id
            samples.append(dict(seconds=time.perf_counter()-started,state=run['state'],consumers=run['consumers'],counts=run['counts']))
            if run['state']=='complete': break
            assert run['state'] not in ('complete_with_errors','failed','cancelled','error','rebuild_required'),run
            assert time.perf_counter()-started < 1800,'replay timeout'; time.sleep(.2)
        result['elapsed_seconds']=time.perf_counter()-started
        after=h.process_metrics(rt.process); worker.join(timeout=600); assert not worker.is_alive() and not errors,errors
        result['run']=run; result['counts']=run['counts']; result['ui_requests']=poll; result['io_delta']={k:after['io'][k]-pre['io'][k] for k in after.get('io',{}) if k in pre.get('io',{})}; result['peak_rss_bytes']=after.get('process_peak_rss_bytes'); result['startup_seconds']=rt.startup_seconds
        h.write(out/'progress.json',samples); h.write(out/'api-calls.json',rt.calls)
        rt.stop(); result['semantics']=h.store_semantics(profile,rows); result['result']='PASS'; h.write(out/'result.json',result)
        print(json.dumps({k:result[k] for k in ('result','elapsed_seconds','entry_status_seconds','ui_requests','io_delta','peak_rss_bytes')},indent=2),flush=True)
    except BaseException as e:
        result['result']='FAIL'; result['error']=repr(e); h.write(out/'result.json',result); h.write(out/'progress.json',samples); h.write(out/'api-calls.json',rt.calls); raise
    finally: rt.stop()

if __name__=='__main__': main()
