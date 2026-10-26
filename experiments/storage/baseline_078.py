#!/usr/bin/env python3
"""Prepare then run a pinned 0.7.8 TX+District baseline in an owned build dir."""
import argparse, hashlib, importlib.util, json, os, pathlib, shutil, sys

ROOT=pathlib.Path(__file__).resolve().parents[2]
BUILD=(ROOT/'build').resolve()
MANIFEST_SHA='d7836322fa33db18de5aecf9d2a49039ad244be7e6bb5b0cce690438cca7bf8a'
HEADER_SHA='534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155'
BINARY_SHA='a7c48db9bbfac6a77345e8c7abe1175d9ed40cd5c6238460f9a7a2228d824137'
HARNESS_SHA='23f325a018cac636e09e7e95112f620e7f360a98ccda63c0f7861dd3e136edb4'

def sha(path):
    with path.open('rb') as f: return hashlib.file_digest(f,'sha256').hexdigest()
def owned(path):
    p=path.resolve(); assert p!=BUILD and p.is_relative_to(BUILD), 'Output must be a new child of workspace build/'
    return p
def copy_checked(source,destination,expected):
    source=source.resolve(strict=True); assert source.is_file() and sha(source)==expected, ('source digest',source)
    destination.parent.mkdir(parents=True,exist_ok=True); shutil.copyfile(source,destination)
    assert sha(destination)==expected, ('copy digest',destination)
def load_harness(path):
    sys.dont_write_bytecode=True
    spec=importlib.util.spec_from_file_location('owned_baseline_harness',path); module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    module.IDS=('tx-locator','bitmap-districts-standard-v1'); return module

def prepare(args):
    output=owned(args.out); assert not output.exists(), 'Refuse existing output'; output.mkdir(parents=True)
    manifest=args.manifest.resolve(strict=True); assert sha(manifest)==MANIFEST_SHA
    doc=json.loads(manifest.read_text(encoding='utf-8-sig')); rows=doc['selected_blocks']
    assert len(rows)==32 and doc['header_sha256']==HEADER_SHA
    assert all(row['height']==792435+i for i,row in enumerate(rows))
    copy_checked(manifest,output/'inputs/manifest.json',MANIFEST_SHA)
    copy_checked(args.headers,output/'inputs/headers.bin',HEADER_SHA)
    copy_checked(args.binary,output/'inputs/GatewayClient.exe',BINARY_SHA)
    copy_checked(args.harness,output/'inputs/benchmark-index-replay.py',HARNESS_SHA)
    archive=args.archive.resolve(strict=True)
    for row in rows:
        bh=row['block_hash']; source=archive/'blocks'/bh[:2]/(bh+'.blk')
        dest=output/'archive/blocks'/bh[:2]/(bh+'.blk'); copy_checked(source,dest,row['sha256'])
        assert dest.stat().st_size==row['bytes']
        dest.with_suffix('.json').write_text(json.dumps(dict(schema=1,verifier_version=2,hash=bh,height=row['height'],sha256=row['sha256'],bytes=row['bytes']),indent=2)+'\n')
    # Revalidate the staged real bodies and selected headers before the run.
    spec=importlib.util.spec_from_file_location('storage_replay',pathlib.Path(__file__).with_name('replay.py')); module=importlib.util.module_from_spec(spec); sys.dont_write_bytecode=True; spec.loader.exec_module(module)
    blocks=module.load(output/'inputs/manifest.json',output/'archive',output/'inputs/headers.bin')
    assert len(blocks)==32 and sum(len(b[2]) for b in blocks)==64767
    h=load_harness(output/'inputs/benchmark-index-replay.py'); inventory=h.inventory(output/'archive')
    (output/'prepared.json').write_text(json.dumps({'status':'prepared','blocks':len(rows),'transactions':64767,'manifest_sha256':MANIFEST_SHA,'header_sha256':HEADER_SHA,'binary_sha256':BINARY_SHA,'harness_sha256':HARNESS_SHA,'source_binary':str(args.binary.resolve()),'source_harness':str(args.harness.resolve()),'source_manifest':str(manifest),'archive_inventory':inventory},indent=2)+'\n')
    print('Prepared and independently checked 32 real blocks in',output,'; no executable was started.')

def run(args):
    output=owned(args.out); assert output.is_dir(); meta=json.loads((output/'prepared.json').read_text())
    assert meta['status']=='prepared'
    inputs=output/'inputs'; archive=output/'archive'; target=output/'replay'
    assert not target.exists(), 'Refuse existing replay; prepare a new directory'
    for filename,digest in [('manifest.json',MANIFEST_SHA),('headers.bin',HEADER_SHA),('GatewayClient.exe',BINARY_SHA),('benchmark-index-replay.py',HARNESS_SHA)]: assert sha(inputs/filename)==digest
    h=load_harness(inputs/'benchmark-index-replay.py'); assert h.inventory(archive)==meta['archive_inventory'], 'staged archive changed'
    rows=json.loads((inputs/'manifest.json').read_text(encoding='utf-8-sig'))['selected_blocks']
    tmp=output/'tmp'; tmp.mkdir(exist_ok=False)
    for key in ('TMP','TEMP','TMPDIR'): os.environ[key]=str(tmp)
    target.mkdir()
    result=h.run_one(inputs/'GatewayClient.exe','baseline',0,target,archive,inputs/'headers.bin',rows,args.timeout,False)
    assert h.inventory(archive)==meta['archive_inventory'], 'staged archive changed during run'
    result['archive_unchanged']=True; result['qualification']='Fresh 0.7.8 TX locator plus District; different semantics from storage prototypes, no relative throughput claim.'
    (output/'baseline-result.json').write_text(json.dumps(result,indent=2)+'\n')
    print('Baseline PASS',result['elapsed_seconds'],'seconds. No peer network or existing profile was used.')

def main():
    p=argparse.ArgumentParser(); p.add_argument('action',choices=('prepare','run')); p.add_argument('--out',type=pathlib.Path,required=True)
    p.add_argument('--manifest',type=pathlib.Path); p.add_argument('--archive',type=pathlib.Path); p.add_argument('--headers',type=pathlib.Path); p.add_argument('--binary',type=pathlib.Path); p.add_argument('--harness',type=pathlib.Path); p.add_argument('--timeout',type=float,default=1200)
    a=p.parse_args()
    if a.action=='prepare':
        assert all((a.manifest,a.archive,a.headers,a.binary,a.harness)); prepare(a)
    else: run(a)
if __name__=='__main__': main()
