#!/usr/bin/env python3
"""Compare two saved skew runs and verify equivalent fixture and results."""
import argparse, json, pathlib
p=argparse.ArgumentParser(); p.add_argument('unretained',type=pathlib.Path); p.add_argument('retained',type=pathlib.Path); args=p.parse_args()
a=json.loads(args.unretained.read_text()); b=json.loads(args.retained.read_text())
for key in ('fixture_sha256','blocks','prepopulated_blocks','order'): assert a[key]==b[key],key
assert a['retained_blocks_limit']==0 and b['retained_blocks_limit']>0
for mode in ('shared','separate'):
    x,y=a[mode],b[mode]
    for key in ('counts','digests','checkpoint_digests','slow_delay_seconds_per_block','timed_blocks_per_consumer'): assert x[key]==y[key],(mode,key)
    for name,d in [('unretained',x),('retained',y)]:
        fields=('seconds','process_cpu_seconds','fast_completion_seconds','slow_completion_seconds','maximum_fast_lead_blocks','broker_loads','broker_overlap_hits','broker_retained_hits','broker_evictions','peak_active_bodies','peak_estimated_active_bytes','peak_retained_bytes','peak_combined_estimated_bytes')
        print(mode,name,json.dumps({key:d[key] for key in fields},sort_keys=True))
print('Fixture, projection and checkpoint digests match; timing and cache conditions remain observational.')
