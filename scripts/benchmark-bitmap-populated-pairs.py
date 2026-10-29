import argparse,ctypes,hashlib,importlib.util,json,os,shutil,subprocess,time
from ctypes import wintypes
from pathlib import Path
from bitmap_pilot_paths import workspace,inspect_tree,io_path,stop_owned
root=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('measure',root/'scripts/benchmark-index-replay.py');h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
def fixture_roots(directory):
    summary=json.loads((directory/'pilot-summary.json').read_text())
    store=directory/'indexes/bitmap-development-v1/bitmap-districts-standard-v1'
    head=json.loads((store/'head.json').read_text())
    if head['commitment']!=summary['commitment']: raise ValueError('head and summary commitments differ')
    roots={k:summary[k] for k in ('winner_root','candidate_root','target_root','block_root')}
    for value in roots.values():
        if h.sha(store/'pages'/value[:2]/(value+'.json'))!=value: raise ValueError('summary page checksum mismatch')
    return {'commitment':head['commitment'],'roots':roots}
parser=argparse.ArgumentParser();parser.add_argument('--out',required=True);parser.add_argument('--reverse',action='store_true');args=parser.parse_args()
out=workspace(args.out)
seed=out/'seed';inspect_tree(seed);baseline=out/'baseline-apply.test.exe';current=out/'current-apply.test.exe'
if args.reverse:
    baseline=out/'reverse-baseline-v2.test.exe';current=out/'reverse-current-v2.test.exe'
seed_roots=fixture_roots(seed);seed_hash=hashlib.sha256(h.canonical(seed_roots)).hexdigest()
samples=[]
order=[(3,'current'),(4,'baseline')] if args.reverse else [(1,'baseline'),(2,'current')]
tag='reverse' if args.reverse else 'first'
# Refuse all collisions before creating any profile copy or receipt.
for n,label in order:
    for suffix in ('', '.log'):
        path=out/('%02d-%s%s'%(n,label,suffix))
        if os.path.lexists(path): raise FileExistsError(path)
    path=out/('pairs-%s-partial-%02d.json'%(tag,n))
    if os.path.lexists(path): raise FileExistsError(path)
result_path=out/('pairs-'+tag+'-result.json')
if os.path.lexists(result_path): raise FileExistsError(result_path)
def cpu_seconds(process):
    values=[wintypes.FILETIME() for _ in range(4)]
    if not ctypes.windll.kernel32.GetProcessTimes(wintypes.HANDLE(int(process._handle)),*(ctypes.byref(x) for x in values)):
        return None
    ticks=lambda x:(int(x.dwHighDateTime)<<32)|int(x.dwLowDateTime)
    return (ticks(values[2])+ticks(values[3]))/1e7
def system_times():
    values=[wintypes.FILETIME() for _ in range(3)]
    if not ctypes.windll.kernel32.GetSystemTimes(*(ctypes.byref(x) for x in values)):return None
    ticks=lambda x:(int(x.dwHighDateTime)<<32)|int(x.dwLowDateTime)
    return tuple(ticks(x) for x in values)
class MemoryStatus(ctypes.Structure):
    _fields_=[('length',wintypes.DWORD),('load',wintypes.DWORD)]+[(name,ctypes.c_ulonglong) for name in ('total_phys','available_phys','total_page','available_page','total_virtual','available_virtual','available_extended_virtual')]
def available_memory():
    value=MemoryStatus();value.length=ctypes.sizeof(value)
    return value.available_phys if ctypes.windll.kernel32.GlobalMemoryStatusEx(ctypes.byref(value)) else None
for n,label in order:
    target=out/('%02d-%s'%(n,label))
    inspect_tree(seed)
    shutil.copytree(io_path(seed),io_path(target),symlinks=True)
    inspect_tree(target)
    if fixture_roots(target)!=seed_roots: raise ValueError('copied fixture summary differs from seed')
    exe=baseline if label=='baseline' else current
    env=dict(os.environ,GATEWAY_BITMAP_PILOT_APPLY=str(target),GATEWAY_BITMAP_PILOT_OUT=str(out))
    started=time.perf_counter();first=None;peak=0;ospeak=0;reason=None;io_first=None;io_last=None;cpu_last=None;host_previous=system_times();host_samples=[];available=[]
    log_path=out/('%02d-%s.log'%(n,label))
    with log_path.open('xb') as log:
        p=subprocess.Popen([str(exe),'-test.run=^TestBitmapPopulatedPilotApply$','-test.count=1','-test.v','-test.timeout=4m'],cwd=root,env=env,stdout=log,stderr=subprocess.STDOUT,creationflags=subprocess.CREATE_NO_WINDOW)
        try:
          while p.poll() is None:
              sample=h.process_metrics(p);peak=max(peak,sample.get('rss_bytes',0));ospeak=max(ospeak,sample.get('process_peak_rss_bytes',0))
              if sample.get('io'):
                  if io_first is None:io_first=sample['io']
                  io_last=sample['io']
              cpu=cpu_seconds(p)
              if cpu is not None:cpu_last=cpu
              host=system_times()
              if host and host_previous:
                  idle=host[0]-host_previous[0];total=(host[1]-host_previous[1])+(host[2]-host_previous[2])
                  if total>0:host_samples.append(100*(total-idle)/total)
              host_previous=host
              free=available_memory()
              if free is not None:available.append(free)
              if first is None and log_path.stat().st_size:first=time.perf_counter()-started
              if peak>1536*1024*1024 or time.perf_counter()-started>240:
                  reason='owned process exceeded 1.5 GiB working set or 240 s';p.terminate();break
              time.sleep(.1)
          p.wait(timeout=10)
        finally:
            stop_owned(p)
    sample={'order':n,'label':label,'pid':p.pid,'exit_code':p.returncode,'process_seconds':time.perf_counter()-started,'first_output_seconds':first,'sampled_peak_working_set_bytes':peak,'os_peak_working_set_bytes':ospeak,'sampled_process_cpu_seconds':cpu_last,'sampled_process_io_delta':{k:io_last[k]-io_first[k] for k in io_last if k in io_first} if io_first and io_last else None,'sampled_host_cpu_mean_percent':sum(host_samples)/len(host_samples) if host_samples else None,'sampled_host_cpu_peak_percent':max(host_samples) if host_samples else None,'sampled_host_available_memory_min_bytes':min(available) if available else None,'host_sample_count':len(host_samples),'stopped_reason':reason}
    if p.returncode==0:sample['apply']=json.loads((target/'apply-summary.json').read_text())
    samples.append(sample)
    with (out/('pairs-%s-partial-%02d.json'%(tag,n))).open('x',encoding='utf-8') as receipt:
        receipt.write(json.dumps(samples,indent=2)+'\n')
    print(label,'sample',n,'exit',p.returncode,'apply',sample.get('apply',{}).get('apply_seconds'),flush=True)
    if p.returncode:raise SystemExit(p.returncode)
fields=['height','block_hash','commitment','winner_root','candidate_root','target_root','block_root','winners','candidates','page_files','page_bytes']
expected={k:samples[0]['apply'][k] for k in fields}
if not all({k:s['apply'][k] for k in fields}==expected for s in samples): raise ValueError('committed results differ')
if fixture_roots(seed)!=seed_roots: raise ValueError('owned source seed summary changed')
report={'scope':'20k synthetic seed; normal next-block Apply with 500 duplicate and 500 new claims; no authenticated Bitcoin history or real throughput claim','input_check':'matching summary commitment and checksummed summary root pages; preflight does not bind summary roots to committed record or fully inventory copies','seed_root_summary_sha256':seed_hash,'baseline_binary_sha256':h.sha(baseline),'current_binary_sha256':h.sha(current),'baseline_ordinal_source_sha256':h.sha(out/'old-ordinal_state_tree.go'),'current_ordinal_source_sha256':h.sha(root/'ordinal_state_tree.go'),'samples':samples,'committed_outputs_equal':True}
if args.reverse:
    report['baseline_detached_evidence_source_sha256']=h.sha(out/'v2-old-bitmap_development_evidence.go')
    report['current_detached_evidence_source_sha256']=h.sha(root/'bitmap_development_evidence.go')
    report['baseline_overlay_note']='old detached evidence helper is overlaid for compatibility with old cache; no parent proof path is exercised by this District fixture'
with result_path.open('x',encoding='utf-8') as receipt:
    receipt.write(json.dumps(report,indent=2)+'\n')
print('PAIRED PILOT PASS: identical roots, commitment and page census',flush=True)

