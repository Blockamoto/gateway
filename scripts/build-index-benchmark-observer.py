#!/usr/bin/env python3
"""Build a measurement-only observer overlay of a pinned baseline in build/.

The observer counts calls and elapsed Sync time; it preserves each original IO
operation and publication order. It is never included in the delivered client.
"""
import argparse
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[1]
OBSERVER = r'''package main
import (
 "net/http"
 "os"
 "path/filepath"
 "strings"
 "sync/atomic"
 "time"
)
var benchAppendCalls,benchAppendRecords,benchWriteOpens,benchReceipts,benchActive atomic.Int64
var benchDataSyncs,benchReceiptSyncs,benchActiveSyncs,benchOtherLocatorSyncs,benchLocatorNanos atomic.Int64
var benchAtomicWrites,benchAtomicBytes,benchAtomicSyncs,benchAtomicNanos,benchFailedSyncs atomic.Int64
var benchCommitWrites,benchHeadWrites,benchBrowsePointerWrites atomic.Int64
func benchObserverSync(f *os.File,kind string) error {
 started:=time.Now();err:=f.Sync();elapsed:=time.Since(started).Nanoseconds()
 if err!=nil{benchFailedSyncs.Add(1)}
 if kind=="atomic"{benchAtomicSyncs.Add(1);benchAtomicNanos.Add(elapsed);return err}
 benchLocatorNanos.Add(elapsed)
 path:=filepath.ToSlash(f.Name())
 if strings.Contains(path,".locator-receipt-"){benchReceiptSyncs.Add(1)
 }else if strings.Contains(path,"/locators-v1/"){benchDataSyncs.Add(1)
 }else if strings.Contains(path,"active"){benchActiveSyncs.Add(1)
 }else{benchOtherLocatorSyncs.Add(1)}
 return err
}
func benchObserverOpen(path string,flags int,mode os.FileMode)(*os.File,error){
 if flags&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_APPEND)!=0{benchWriteOpens.Add(1)}
 return os.OpenFile(path,flags,mode)
}
func benchObserverAtomic(path string,size int){
 benchAtomicWrites.Add(1);benchAtomicBytes.Add(int64(size));path=filepath.ToSlash(path)
 if strings.Contains(path,"/indexes/tx-locator/commits/"){benchCommitWrites.Add(1)}
 if strings.HasSuffix(path,"/indexes/tx-locator/head.json"){benchHeadWrites.Add(1)}
 if strings.Contains(path,"/indexes/browse/tx-locator/locators/"){benchBrowsePointerWrites.Add(1)}
}
func benchObserverHTTP(w http.ResponseWriter,r *http.Request){
 if r.Method!="GET"{http.Error(w,"GET required",405);return}
 writeJSON(w,map[string]any{
 "measurement_only":true,"counts_are_io_call_attempts":true,
 "locator_shard_appends":benchAppendCalls.Load(),"locator_records_appended":benchAppendRecords.Load(),
 "locator_write_opens":benchWriteOpens.Load(),"locator_receipt_write_attempts":benchReceipts.Load(),
 "locator_active_write_attempts":benchActive.Load(),"locator_data_syncs":benchDataSyncs.Load(),
 "locator_receipt_syncs":benchReceiptSyncs.Load(),"locator_active_syncs":benchActiveSyncs.Load(),
 "locator_other_syncs":benchOtherLocatorSyncs.Load(),"locator_sync_nanoseconds":benchLocatorNanos.Load(),
 "atomic_write_attempts":benchAtomicWrites.Load(),"atomic_bytes":benchAtomicBytes.Load(),
 "atomic_syncs":benchAtomicSyncs.Load(),"atomic_sync_nanoseconds":benchAtomicNanos.Load(),
 "failed_syncs":benchFailedSyncs.Load(),"tx_locator_commit_writes":benchCommitWrites.Load(),
 "tx_locator_head_writes":benchHeadWrites.Load(),"sparse_tx_locator_pointer_writes":benchBrowsePointerWrites.Load(),
 })
}
'''


def replace_once(path, old, new):
    source = path.read_text(encoding='utf-8')
    if source.count(old) != 1:
        raise ValueError(f'{path.name}: expected one observer seam: {old[:80]!r}')
    path.write_text(source.replace(old, new), encoding='utf-8')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ref', default='cfde4c9b488b3c14456b9e54556d6b4e6a01daee')
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--go', required=True)
    args = parser.parse_args()
    out = args.out.resolve()
    build = (ROOT / 'build').resolve()
    if build not in out.parents or out.exists():
        parser.error('--out must be a new directory inside this checkout/build')
    revision = subprocess.check_output(['git', 'rev-parse', args.ref + '^{commit}'], cwd=ROOT, text=True).strip()
    archive = subprocess.check_output(['git', 'archive', revision], cwd=ROOT)
    source = out / 'source'
    source.mkdir(parents=True)
    with tarfile.open(fileobj=io.BytesIO(archive)) as package:
        package.extractall(source, filter='data')
    locator = source / 'index_locator.go'
    text = locator.read_text(encoding='utf-8')
    text = text.replace('f.Sync()', 'benchObserverSync(f,"locator")').replace('os.OpenFile(', 'benchObserverOpen(')
    text = text.replace('func (s *indexStore) appendBitcoinLocatorRecords(path string, records [][]byte) error {',
                        'func (s *indexStore) appendBitcoinLocatorRecords(path string, records [][]byte) error {\n benchAppendCalls.Add(1);benchAppendRecords.Add(int64(len(records)))')
    text = text.replace('func (s *indexStore) writeLocatorReceipt(path string, r indexLocatorReceipt) error {',
                        'func (s *indexStore) writeLocatorReceipt(path string, r indexLocatorReceipt) error {\n benchReceipts.Add(1)')
    text = text.replace('func (s *indexStore) writeActiveLocatorHead(c *indexCheckpoint) error {',
                        'func (s *indexStore) writeActiveLocatorHead(c *indexCheckpoint) error {\n benchActive.Add(1)')
    locator.write_text(text, encoding='utf-8')
    integrity = source / 'integrity.go'
    replace_once(integrity, 'func atomicWriteBytes(path string, b []byte) error {',
                 'func atomicWriteBytes(path string, b []byte) error {\n benchObserverAtomic(path,len(b))')
    integrity.write_text(integrity.read_text(encoding='utf-8').replace('f.Sync()', 'benchObserverSync(f,"atomic")'), encoding='utf-8')
    jsonfile = source / 'satline_store.go'
    replace_once(jsonfile, 'tmp, err := os.CreateTemp(filepath.Dir(path), ".satline-*.tmp")',
                 'benchObserverAtomic(path,len(b))\n tmp, err := os.CreateTemp(filepath.Dir(path), ".satline-*.tmp")')
    jsonfile.write_text(jsonfile.read_text(encoding='utf-8').replace('tmp.Sync()', 'benchObserverSync(tmp,"atomic")'), encoding='utf-8')
    replace_once(source / 'index_interface.go', 'func (a *app) registerIndexRoutes(mux *http.ServeMux) {',
                 'func (a *app) registerIndexRoutes(mux *http.ServeMux) {\n mux.HandleFunc("/api/v1/index/bench-observer",benchObserverHTTP)')
    (source / 'benchmark_observer.go').write_text(OBSERVER, encoding='utf-8')
    resource = ROOT / 'gateway_windows_amd64.syso'
    if resource.exists():
        shutil.copy2(resource, source / resource.name)
    env = dict(os.environ, GO111MODULE='off', CGO_ENABLED='0', GOOS='windows', GOARCH='amd64')
    binary = out / 'GatewayClient.exe'
    subprocess.run([args.go, 'build', '-trimpath', '-ldflags', '-s -w -X main.portablePreviewBuild=0.7.8', '-o', str(binary), '.'], cwd=source, env=env, check=True)
    receipt = dict(baseline_revision=revision, binary=str(binary), measurement_only=True,
                   changed_files=['index_locator.go', 'integrity.go', 'satline_store.go', 'index_interface.go', 'benchmark_observer.go'],
                   note='Atomic counters and Sync duration only; original IO calls, verification and publication order retained. Counter attempts equal successful operations only when failed_syncs is zero and the run succeeds.')
    (out / 'observer-build.json').write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps(receipt, indent=2))


if __name__ == '__main__':
    main()
