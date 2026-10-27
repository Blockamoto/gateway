import argparse,importlib.util,json,os,subprocess,time
from bitmap_pilot_paths import workspace,stop_owned
from pathlib import Path

root=Path(__file__).resolve().parents[1]
parser=argparse.ArgumentParser();parser.add_argument('--out',required=True);args=parser.parse_args()
out=workspace(args.out)
spec=importlib.util.spec_from_file_location('measure',root/'scripts/benchmark-index-replay.py')
h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
seed=out/'seed'
for name in ('seed', 'seed-test.log', 'seed-process.json'):
    if os.path.lexists(out/name): raise FileExistsError(out/name)
env=dict(os.environ,GATEWAY_BITMAP_PILOT_SEED=str(seed),GATEWAY_BITMAP_PILOT_OUT=str(out))
started=time.perf_counter(); first_output=None;peak=0;peak_os=0;reason=None
with (out/'seed-test.log').open('xb') as log:
    p=subprocess.Popen([str(out/'pilot.test.exe'),'-test.run=^TestBitmapPopulatedPilotSeed$','-test.count=1','-test.v','-test.timeout=4m'],cwd=root,env=env,stdout=log,stderr=subprocess.STDOUT,creationflags=subprocess.CREATE_NO_WINDOW)
    try:
      while p.poll() is None:
          sample=h.process_metrics(p);peak=max(peak,sample.get('rss_bytes',0));peak_os=max(peak_os,sample.get('process_peak_rss_bytes',0))
          if first_output is None and (out/'seed-test.log').stat().st_size:
              first_output=time.perf_counter()-started
          if peak>1536*1024*1024 or time.perf_counter()-started>240:
              reason='owned process exceeded 1.5 GiB working set or 240 s';p.terminate();break
          time.sleep(.1)
      p.wait(timeout=10)
    finally:
        stop_owned(p)
measurement={'pid':p.pid,'exit_code':p.returncode,'process_seconds':time.perf_counter()-started,'first_output_seconds':first_output,'sampled_peak_working_set_bytes':peak,'os_peak_working_set_bytes':peak_os,'stopped_reason':reason,'scope':'isolated 20k synthetic seed; process only; not real Bitcoin history'}
with (out/'seed-process.json').open('x',encoding='utf-8') as receipt:
    receipt.write(json.dumps(measurement,indent=2)+'\n')
print(json.dumps(measurement,indent=2),flush=True)
if p.returncode:raise SystemExit(p.returncode)

