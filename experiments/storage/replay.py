#!/usr/bin/env python3
"""Matched layout experiment; reads an existing isolated 0.7.8 replay archive.

Verifies fixture bytes, header linkage, claimed PoW and merkle root. Does not
perform full consensus, witness-commitment or selected-chain validation. Run
against fresh output directories; never points at or mutates user profiles.
"""
import argparse, concurrent.futures, ctypes, hashlib, json, os, pathlib, sqlite3, threading, time


def dhash(b): return hashlib.sha256(hashlib.sha256(b).digest()).digest()

def vint(b, p):
    n = b[p]; p += 1
    if n < 253: return n, p
    size = {253: 2, 254: 4, 255: 8}[n]
    return int.from_bytes(b[p:p+size], 'little'), p+size

def transactions(body):
    n, p = vint(body, 80); txrows, outrows, leaves = [], [], []
    for pos in range(n):
        start = p; p += 4
        segwit = body[p:p+2] == b'\0\1'
        if segwit: p += 2
        stripped_start = p
        ins, p = vint(body, p)
        for _ in range(ins):
            p += 36; size, p = vint(body, p); p += size + 4
        outs, p = vint(body, p)
        vals = []
        for i in range(outs):
            value = int.from_bytes(body[p:p+8], 'little'); p += 8
            size, p = vint(body, p); p += size
            vals.append((i, value))
        stripped_end = p
        if segwit:
            for _ in range(ins):
                items, p = vint(body, p)
                for _ in range(items):
                    size, p = vint(body, p); p += size
        end = p + 4
        encoded = body[start:start+4] + body[stripped_start:stripped_end] + body[p:end] if segwit else body[start:end]
        leaf = dhash(encoded); leaves.append(leaf); txrows.append((pos, leaf[::-1].hex()))
        outrows.extend((pos, i, value) for i, value in vals); p = end
    assert p == len(body), (p, len(body))
    nodes = leaves
    while len(nodes) > 1:
        if len(nodes) % 2: nodes = nodes + nodes[-1:]
        nodes = [dhash(nodes[i] + nodes[i+1]) for i in range(0, len(nodes), 2)]
    assert nodes and nodes[0] == body[36:68], 'merkle root mismatch'
    return txrows, outrows

def load(manifest, archive, headers):
    doc = json.loads(manifest.read_text(encoding='utf-8-sig'))
    rows = doc['selected_blocks']; blocks = []; previous = None
    with headers.open('rb') as hs:
        for item in rows:
            h = item['height']; bh = item['block_hash']
            raw = (archive / 'blocks' / bh[:2] / (bh + '.blk')).read_bytes()
            assert len(raw) == item['bytes'] and hashlib.sha256(raw).hexdigest() == item['sha256']
            assert dhash(raw[:80])[::-1].hex() == bh
            hs.seek(h*80); assert hs.read(80) == raw[:80]
            if previous is not None: assert raw[4:36] == previous
            previous = dhash(raw[:80])
            bits = int.from_bytes(raw[72:76], 'little'); exponent = bits >> 24; mantissa = bits & 0x007fffff
            assert bits & 0x00800000 == 0 and exponent >= 3
            assert int.from_bytes(previous, 'little') <= mantissa << (8*(exponent-3))
            tx, out = transactions(raw); assert len(tx) == item['transactions']
            blocks.append((h, bh, tx, out))
    return blocks

def connect(path):
    db = sqlite3.connect(path, timeout=60)
    db.execute('PRAGMA journal_mode=WAL'); db.execute('PRAGMA synchronous=FULL')
    db.execute('CREATE TABLE IF NOT EXISTS tx (height INTEGER, position INTEGER, txid TEXT, PRIMARY KEY(height,position)) WITHOUT ROWID')
    db.execute('CREATE TABLE IF NOT EXISTS outputs (height INTEGER, position INTEGER, n INTEGER, value INTEGER, PRIMARY KEY(height,position,n)) WITHOUT ROWID')
    db.execute('CREATE TABLE IF NOT EXISTS checkpoints (consumer TEXT, height INTEGER, hash TEXT, PRIMARY KEY(consumer,height)) WITHOUT ROWID')
    db.commit(); return db

def insert(db, block, kinds):
    h, bh, tx, out = block
    with db:
        if 'tx' in kinds: db.executemany('INSERT INTO tx VALUES (?,?,?)', ((h,p,t) for p,t in tx))
        if 'outputs' in kinds: db.executemany('INSERT INTO outputs VALUES (?,?,?,?)', ((h,p,n,v) for p,n,v in out))
        db.executemany('INSERT INTO checkpoints VALUES (?,?,?)', ((kind,h,bh) for kind in kinds))

def rollback_test(db):
    try:
        with db:
            db.execute("INSERT INTO checkpoints VALUES ('injected', -1, 'failed')")
            raise RuntimeError('injected before commit')
    except RuntimeError: pass
    assert db.execute("SELECT COUNT(*) FROM checkpoints WHERE consumer='injected'").fetchone()[0] == 0

def quantile(xs, q):
    return sorted(xs)[min(len(xs)-1, int((len(xs)-1)*q))] if xs else None

def digest_rows(db, table):
    d=hashlib.sha256()
    for row in db.execute('SELECT * FROM '+table+' ORDER BY 1,2,3'):
        d.update(json.dumps(row, separators=(',', ':')).encode()+b'\n')
    return d.hexdigest()

def os_metrics():
    if os.name != 'nt': return {}
    class IO(ctypes.Structure):
        _fields_=[(n,ctypes.c_ulonglong) for n in ('read_operations','write_operations','other_operations','read_bytes','write_bytes','other_bytes')]
    class Memory(ctypes.Structure):
        _fields_=[('cb',ctypes.c_ulong),('faults',ctypes.c_ulong)]+[(n,ctypes.c_size_t) for n in ('peak_rss','rss','quota_peak_paged','quota_paged','quota_peak_nonpaged','quota_nonpaged','pagefile','peak_pagefile')]
    kernel=ctypes.windll.kernel32; psapi=ctypes.windll.psapi
    kernel.GetCurrentProcess.restype=ctypes.c_void_p
    kernel.GetProcessIoCounters.argtypes=(ctypes.c_void_p,ctypes.POINTER(IO))
    psapi.GetProcessMemoryInfo.argtypes=(ctypes.c_void_p,ctypes.POINTER(Memory),ctypes.c_ulong)
    handle=kernel.GetCurrentProcess(); io=IO(); memory=Memory(); memory.cb=ctypes.sizeof(memory)
    result={}
    if kernel.GetProcessIoCounters(handle,ctypes.byref(io)): result['io']={n:getattr(io,n) for n,_ in IO._fields_}
    if psapi.GetProcessMemoryInfo(handle,ctypes.byref(memory),memory.cb): result['rss_bytes']=memory.rss; result['process_lifetime_peak_rss_bytes']=memory.peak_rss
    return result

def run(out, blocks, mode, warm):
    out.mkdir(); names = ['tx','outputs'] if mode == 'separate_parallel' else ['shared']
    paths = {n: out / (n+'.sqlite') for n in names}
    for p in paths.values():
        db = connect(p); rollback_test(db); db.close()
    def apply(block):
        if mode == 'shared_coordinated':
            db=connect(paths['shared']); insert(db, block, ('tx','outputs')); db.close()
        else:
            def one(kind):
                db=connect(paths[kind] if mode == 'separate_parallel' else paths['shared']); insert(db, block, (kind,)); db.close()
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool: list(pool.map(one, ['tx','outputs']))
    for b in blocks[:warm]: apply(b)
    stop = threading.Event(); latencies=[]; failures=[]
    def poll():
        dbs=[sqlite3.connect(p, timeout=60) for p in paths.values()]
        try:
            while not stop.is_set():
                t=time.perf_counter()
                for db in dbs: db.execute('SELECT consumer, MAX(height) FROM checkpoints GROUP BY consumer').fetchall()
                latencies.append(time.perf_counter()-t); time.sleep(.05)
        except Exception as exc: failures.append(repr(exc))
        finally:
            for db in dbs: db.close()
    before=os_metrics(); thread=threading.Thread(target=poll); thread.start(); started=time.perf_counter()
    for b in blocks[warm:]: apply(b)
    elapsed=time.perf_counter()-started; stop.set(); thread.join(); after=os_metrics(); assert not failures, failures
    results = {}
    for name,path in paths.items():
        db=sqlite3.connect(path)
        assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
        results[name]={'tx':db.execute('SELECT COUNT(*) FROM tx').fetchone()[0], 'outputs':db.execute('SELECT COUNT(*) FROM outputs').fetchone()[0], 'checkpoints':db.execute('SELECT COUNT(*) FROM checkpoints').fetchone()[0], 'digests':{table:digest_rows(db,table) for table in ('tx','outputs','checkpoints')}}
        # Reorg model: remove last two heights and all corresponding checkpoints
        cut=blocks[-3][0]
        with db:
            for table in ('tx','outputs','checkpoints'): db.execute('DELETE FROM '+table+' WHERE height > ?', (cut,))
        for table in ('tx','outputs','checkpoints'): assert db.execute('SELECT COUNT(*) FROM '+table+' WHERE height > ?', (cut,)).fetchone()[0] == 0
        db.close()
    for block in blocks[-2:]: apply(block)
    for name,path in paths.items():
        db=sqlite3.connect(path)
        for table in ('tx','outputs','checkpoints'):
            assert db.execute('SELECT COUNT(*) FROM '+table).fetchone()[0] == results[name][table]
            assert digest_rows(db,table) == results[name]['digests'][table]
        db.close()
    return {'seconds': elapsed, 'io_process_delta':{k:after['io'][k]-before['io'][k] for k in after.get('io',{}) if k in before.get('io',{})}, 'memory':after, 'timed_blocks':len(blocks)-warm, 'polls':len(latencies), 'poll_p50_ms':quantile(latencies,.5)*1000 if latencies else None, 'poll_p95_ms':quantile(latencies,.95)*1000 if latencies else None, 'poll_max_ms':max(latencies)*1000 if latencies else None, 'files_bytes_after_reorg_replay':sum(p.stat().st_size for p in out.iterdir() if p.is_file()), 'counts':results, 'rollback_and_reorg_replay':'PASS'}

def main():
    ap=argparse.ArgumentParser(); ap.add_argument('--manifest',type=pathlib.Path,required=True); ap.add_argument('--archive',type=pathlib.Path,required=True); ap.add_argument('--headers',type=pathlib.Path,required=True); ap.add_argument('--out',type=pathlib.Path,required=True); ap.add_argument('--warm',type=int,default=16); ap.add_argument('--order',choices=('shared-first','separate-first'),default='shared-first'); args=ap.parse_args()
    assert not args.out.exists(), 'Output must be new'; blocks=load(args.manifest,args.archive,args.headers)
    assert 0 < args.warm < len(blocks); args.out.mkdir(parents=True)
    print('Verified',len(blocks),'real blocks,',sum(len(b[2]) for b in blocks),'transactions',flush=True)
    report={'qualification':'Controlled prototype, two projected indexes, not Gateway parity or full consensus. SQLite FULL WAL; synthetic status polling; not a physical I/O measurement.', 'fixture_manifest_sha256':hashlib.sha256(args.manifest.read_bytes()).hexdigest(), 'blocks':len(blocks),'transactions':sum(len(b[2]) for b in blocks),'output_rows':sum(len(b[3]) for b in blocks),'prepopulated_blocks':args.warm}
    order=['shared_coordinated','shared_parallel','separate_parallel'] if args.order=='shared-first' else ['separate_parallel','shared_parallel','shared_coordinated']
    report['layout_order']=order
    for mode in order: report[mode]=run(args.out/mode,blocks,mode,args.warm)
    a=report['shared_coordinated']['counts']['shared']; b=report['separate_parallel']['counts']
    assert a['tx']==b['tx']['tx'] and a['outputs']==b['outputs']['outputs'] and a['checkpoints']==b['tx']['checkpoints']+b['outputs']['checkpoints']
    assert a['digests']['tx']==b['tx']['digests']['tx'] and a['digests']['outputs']==b['outputs']['digests']['outputs']
    assert a['digests']==report['shared_parallel']['counts']['shared']['digests']
    (args.out/'result.json').write_text(json.dumps(report,indent=2)+'\n'); print(json.dumps(report,indent=2),flush=True)
if __name__=='__main__': main()
