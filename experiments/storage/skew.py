#!/usr/bin/env python3
"""Bounded independent-consumer skew experiment; see SKEW.md for limitations."""
import argparse, collections, concurrent.futures, contextlib, hashlib, importlib.util, json, pathlib, queue, sqlite3, sys, threading, time

spec = importlib.util.spec_from_file_location('replay', pathlib.Path(__file__).with_name('replay.py'))
replay = importlib.util.module_from_spec(spec); spec.loader.exec_module(replay)

class Broker:
    """At most two active decoded bodies, shared by overlapping requesters.

    Estimated object sizes are observed after parsing and checked against a
    fail-fast ceiling. It is not an allocator or a hard process RSS limit.
    """
    def __init__(self, rows, archive, headers, max_bodies=2, max_estimated_bytes=32<<20, max_retained_blocks=0, max_retained_bytes=32<<20):
        self.rows=rows; self.archive=archive; self.headers=headers
        self.max_bodies=max_bodies; self.max_bytes=max_estimated_bytes
        self.cv=threading.Condition(); self.active={}; self.bytes=0
        self.peak_bodies=0; self.peak_bytes=0; self.loads=0; self.hits=0; self.waits=0
        self.retained=collections.OrderedDict(); self.retained_bytes=0; self.retained_hits=0; self.evictions=0
        self.max_retained_blocks=max_retained_blocks; self.max_retained_bytes=max_retained_bytes
        self.peak_retained_bytes=0; self.peak_combined_bytes=0
    def _load(self, item):
        h=item['height']; bh=item['block_hash']
        raw=(self.archive/'blocks'/bh[:2]/(bh+'.blk')).read_bytes()
        assert len(raw)==item['bytes'] and hashlib.sha256(raw).hexdigest()==item['sha256']
        assert replay.dhash(raw[:80])[::-1].hex()==bh
        with self.headers.open('rb') as hs:
            hs.seek(h*80); assert hs.read(80)==raw[:80]
            if h:
                hs.seek((h-1)*80); assert replay.dhash(hs.read(80))==raw[4:36]
        bits=int.from_bytes(raw[72:76],'little'); exponent=bits>>24; mantissa=bits&0x007fffff
        assert bits&0x00800000==0 and exponent>=3
        assert int.from_bytes(replay.dhash(raw[:80]),'little') <= mantissa << (8*(exponent-3))
        tx,out=replay.transactions(raw); assert len(tx)==item['transactions']
        # Approximate retained Python heap for these flat objects; excludes
        # allocator overhead, parser temporaries, raw buffer and SQLite caches.
        estimate=sys.getsizeof(tx)+sys.getsizeof(out)
        for row in tx: estimate += sys.getsizeof(row)+sum(sys.getsizeof(v) for v in row)
        for row in out: estimate += sys.getsizeof(row)+sum(sys.getsizeof(v) for v in row)
        return (h,bh,tx,out), estimate
    @contextlib.contextmanager
    def borrow(self, item):
        h=item['height']
        with self.cv:
            while h not in self.active and len(self.active)>=self.max_bodies:
                self.waits+=1; self.cv.wait()
            if h in self.active:
                slot=self.active[h]; assert slot[0][1]==item['block_hash'], 'active body anchor differs'
                slot[1]+=1; self.hits+=1
            else:
                if h in self.retained:
                    block,size=self.retained[h]
                    assert block[1]==item['block_hash'], 'retained body anchor differs'
                    self.retained.pop(h); self.retained_bytes-=size; self.retained_hits+=1
                else:
                    # Serial decoding is bounded but can create head-of-line waits.
                    block,size=self._load(item); self.loads+=1
                assert self.bytes+size<=self.max_bytes, 'estimated active body budget exceeded'
                slot=[block,1,size]; self.active[h]=slot; self.bytes+=size
                self.peak_bytes=max(self.peak_bytes,self.bytes); self.peak_bodies=max(self.peak_bodies,len(self.active))
                self.peak_combined_bytes=max(self.peak_combined_bytes,self.bytes+self.retained_bytes)
        try: yield slot[0]
        finally:
            with self.cv:
                slot[1]-=1
                if not slot[1]:
                    self.bytes-=slot[2]; del self.active[h]
                    if self.max_retained_blocks and slot[2]<=self.max_retained_bytes:
                        while self.retained and (len(self.retained)>=self.max_retained_blocks or self.retained_bytes+slot[2]>self.max_retained_bytes):
                            _,(_,removed)=self.retained.popitem(last=False); self.retained_bytes-=removed; self.evictions+=1
                        self.retained[h]=(slot[0],slot[2]); self.retained_bytes+=slot[2]
                        self.peak_retained_bytes=max(self.peak_retained_bytes,self.retained_bytes)
                        self.peak_combined_bytes=max(self.peak_combined_bytes,self.bytes+self.retained_bytes)
                    self.cv.notify_all()

def anchor(headers, item):
    with headers.open('rb') as stream:
        stream.seek(item['height']*80); header=stream.read(80)
    assert replay.dhash(header)[::-1].hex()==item['block_hash'], 'selected anchor changed'

def run(base, mode, rows, archive, headers, warm, delay, retained_blocks=0, retained_bytes=32<<20):
    base.mkdir(); shared=mode=='shared'; paths={name:base/('shared.sqlite' if shared else name+'.sqlite') for name in ('tx','outputs')}
    for path in set(paths.values()):
        db=replay.connect(path); replay.rollback_test(db); db.close()
    broker=Broker(rows,archive,headers,max_retained_blocks=retained_blocks,max_retained_bytes=retained_bytes)
    for item in rows[:warm]:
        with broker.borrow(item) as block:
            for kind in ('tx','outputs'):
                db=sqlite3.connect(paths[kind],timeout=60); db.execute('PRAGMA synchronous=FULL')
                anchor(headers,item); replay.insert(db,block,(kind,)); db.close()
        del block
    broker.loads=broker.hits=broker.waits=broker.peak_bodies=broker.peak_bytes=0
    broker.retained.clear(); broker.retained_bytes=broker.retained_hits=broker.evictions=broker.peak_retained_bytes=broker.peak_combined_bytes=0
    jobs={name:queue.Queue(maxsize=2) for name in ('tx','outputs')}; lock=threading.Lock()
    state={'tx':warm-1,'outputs':warm-1}; lead=[]; failures=[]; completions=[]
    t0=time.perf_counter(); cpu0=time.process_time(); before=replay.os_metrics(); start=threading.Event()
    def feeder(kind):
        for index in range(warm,len(rows)): jobs[kind].put(index)
        jobs[kind].put(None)
    def worker(kind):
        db=sqlite3.connect(paths[kind],timeout=60); db.execute('PRAGMA synchronous=FULL'); start.wait()
        try:
            while True:
                index=jobs[kind].get()
                if index is None: return
                if kind=='outputs': time.sleep(delay)
                item=rows[index]
                with broker.borrow(item) as block:
                    anchor(headers,item)
                    replay.insert(db,block,(kind,))
                del block
                with lock:
                    assert state[kind]+1==index, 'non-consecutive completion'
                    state[kind]=index; lead.append(state['tx']-state['outputs'])
                    completions.append((kind,item['height'],time.perf_counter()-t0))
        except BaseException as exc:
            failures.append(repr(exc))
        finally: db.close()
    workers=[threading.Thread(target=worker,args=(k,),daemon=True) for k in jobs]
    feeders=[threading.Thread(target=feeder,args=(k,),daemon=True) for k in jobs]
    for thread in workers+feeders: thread.start()
    start.set()
    for thread in workers: thread.join(timeout=120)
    assert all(not t.is_alive() for t in workers), 'worker timeout'; assert not failures, failures
    for thread in feeders: thread.join(timeout=1)
    elapsed=time.perf_counter()-t0; cpu_seconds=time.process_time()-cpu0; after=replay.os_metrics()
    assert state=={'tx':len(rows)-1,'outputs':len(rows)-1}
    assert max(lead)>0, 'fast consumer never advanced ahead of intentionally delayed consumer'
    assert broker.peak_bodies<=broker.max_bodies and broker.peak_bytes<=broker.max_bytes and not broker.active
    assert len(broker.retained)<=broker.max_retained_blocks and broker.retained_bytes<=broker.max_retained_bytes
    digests={}; counts={}; checkpoint_digests={}
    for kind in jobs:
        db=sqlite3.connect(paths[kind]); assert db.execute('PRAGMA integrity_check').fetchone()[0]=='ok'
        cp=db.execute('SELECT height,hash FROM checkpoints WHERE consumer=? ORDER BY height',(kind,)).fetchall()
        assert cp==[(r['height'],r['block_hash']) for r in rows], 'missing, extra or changed checkpoint'
        checkpoint_digests[kind]=hashlib.sha256(json.dumps(cp,separators=(',',':')).encode()).hexdigest()
        digests[kind]=replay.digest_rows(db,kind); counts[kind]=db.execute('SELECT COUNT(*) FROM '+kind).fetchone()[0]; db.close()
    return {'seconds':elapsed,'process_cpu_seconds':cpu_seconds,'fast_completion_seconds':max(t for kind,_,t in completions if kind=='tx'),'slow_completion_seconds':max(t for kind,_,t in completions if kind=='outputs'),'slow_delay_seconds_per_block':delay,'maximum_fast_lead_blocks':max(lead),'timed_blocks_per_consumer':len(rows)-warm,'counts':counts,'digests':digests,'checkpoint_digests':checkpoint_digests,'broker_loads':broker.loads,'broker_overlap_hits':broker.hits,'broker_retained_hits':broker.retained_hits,'broker_evictions':broker.evictions,'broker_retained_blocks':len(broker.retained),'broker_retained_bytes':broker.retained_bytes,'peak_retained_bytes':broker.peak_retained_bytes,'peak_combined_estimated_bytes':broker.peak_combined_bytes,'retained_blocks_limit':broker.max_retained_blocks,'retained_bytes_limit':broker.max_retained_bytes,'broker_waits':broker.waits,'peak_active_bodies':broker.peak_bodies,'peak_estimated_active_bytes':broker.peak_bytes,'estimated_active_limit_bytes':broker.max_bytes,'queue_max_items':2,'process_io_delta':{k:after['io'][k]-before['io'][k] for k in after.get('io',{}) if k in before.get('io',{})},'process_memory':after,'completion_events':completions,'check':'PASS'}

def main():
    ap=argparse.ArgumentParser(); ap.add_argument('--manifest',type=pathlib.Path,required=True); ap.add_argument('--archive',type=pathlib.Path,required=True); ap.add_argument('--headers',type=pathlib.Path,required=True); ap.add_argument('--out',type=pathlib.Path,required=True); ap.add_argument('--count',type=int,default=32); ap.add_argument('--warm',type=int,default=8); ap.add_argument('--delay',type=float,default=.08); ap.add_argument('--order',choices=('shared-first','separate-first'),default='shared-first'); ap.add_argument('--retained-blocks',type=int,default=0); ap.add_argument('--retained-bytes',type=int,default=32<<20); a=ap.parse_args()
    assert not a.out.exists(); rows=json.loads(a.manifest.read_text(encoding='utf-8-sig'))['selected_blocks'][:a.count]
    assert 0 < a.warm < len(rows) and a.delay>0 and a.retained_blocks>=0 and a.retained_bytes>0
    assert all(rows[i]['height']+1==rows[i+1]['height'] for i in range(len(rows)-1))
    a.out.mkdir(parents=True); report={'fixture_sha256':hashlib.sha256(a.manifest.read_bytes()).hexdigest(),'blocks':len(rows),'prepopulated_blocks':a.warm,'order':a.order,'retained_blocks_limit':a.retained_blocks,'retained_bytes_limit':a.retained_bytes}
    for mode in (('shared','separate') if a.order=='shared-first' else ('separate','shared')):
        report[mode]=run(a.out/mode,mode,rows,a.archive,a.headers,a.warm,a.delay,a.retained_blocks,a.retained_bytes)
    assert report['shared']['digests']==report['separate']['digests'] and report['shared']['counts']==report['separate']['counts'] and report['shared']['checkpoint_digests']==report['separate']['checkpoint_digests']
    (a.out/'result.json').write_text(json.dumps(report,indent=2)+'\n'); print(json.dumps(report,indent=2),flush=True)
if __name__=='__main__': main()
