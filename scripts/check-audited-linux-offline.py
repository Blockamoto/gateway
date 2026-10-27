#!/usr/bin/env python3
"""Offline exact-binary startup/restart and native API checks on disposable data."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tempfile

EXPECTED='64a9baddba41f0e0233c62de3b21b34ce5bcac80b579055e5f16d9ad3e9727c2'

def checked_binary(path):
    if path.is_symlink() or not path.is_file():
        raise RuntimeError('Audited binary must be a regular file')
    with path.open('rb') as f:
        digest=hashlib.file_digest(f,'sha256').hexdigest()
    if digest != EXPECTED:
        raise RuntimeError('Audited binary SHA-256 mismatch; execution refused')

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary',type=Path,required=True)
    p.add_argument('--source',type=Path,required=True)
    p.add_argument('--out',type=Path,required=True)
    a=p.parse_args()
    checked_binary(a.binary) # Deliberately before any executable invocation or output creation.
    if sys.platform != 'linux' or [name for _,name in socket.if_nameindex()] != ['lo']:
        raise RuntimeError('Require an isolated Linux network namespace with loopback only')
    if os.getuid() == 0 or os.geteuid() == 0:
        raise RuntimeError('Audited checks must run as a non-root user')
    status=Path('/proc/self/status').read_text(encoding='utf-8')
    if not any(line.split()==['NoNewPrivs:', '1'] for line in status.splitlines()):
        raise RuntimeError('Audited checks require no-new-privileges')
    tmp=Path(os.environ['TMPDIR']).resolve(strict=True)
    out=a.out.resolve()
    if out==tmp or not out.is_relative_to(tmp) or out.exists():
        raise RuntimeError('Output must be a fresh child of isolated TMPDIR')
    source=a.source.resolve(strict=True)
    fixture=source/'scripts/test-bitmap-standard-native-api.py'
    spec=importlib.util.spec_from_file_location('audited_native_fixture',fixture)
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    out.mkdir(mode=0o700)
    profile=module.prepare_profile(out)
    sentinel=profile/'audited-check-sentinel.txt';sentinel.write_text('owned disposable profile\n',encoding='utf-8')
    calls=[]
    for run in (1,2):
        checked_binary(a.binary)
        runtime=module.Runtime(a.binary,profile,out/f'start-{run}.log',calls)
        try:
            runtime.start()
            result=runtime.call('/api/v1/settings')
            settings=result['settings']
            for key in ('network_disabled','headers_paused','core_disabled','core_mount_disabled'):
                if settings.get(key) is not True: raise RuntimeError('Offline setting unavailable: '+key)
            if settings.get('lan_discovery') is not False: raise RuntimeError('LAN discovery was enabled')
            if sentinel.read_text(encoding='utf-8')!='owned disposable profile\n': raise RuntimeError('Disposable profile did not survive restart')
        finally:
            runtime.stop()
    checked_binary(a.binary)
    child=subprocess.Popen([sys.executable,str(fixture),'--binary',str(a.binary),'--out',str(out/'bitmap-api')],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
    try:
        stdout,stderr=child.communicate(timeout=180)
    except subprocess.TimeoutExpired:
        os.killpg(child.pid,signal.SIGKILL)
        child.communicate()
        raise RuntimeError('Native Bitmap API test timed out; its owned process group was stopped') from None
    except BaseException:
        if child.poll() is None:
            os.killpg(child.pid,signal.SIGKILL)
            child.communicate()
        raise
    if child.returncode:
        raise RuntimeError('Native Bitmap API test failed: '+stdout[-4000:]+stderr[-4000:])
    print('PASS: exact audited SHA-256; loopback-only network namespace; disposable offline startup/restart; native Bitmap API')
    print('Limit: synthetic genesis-only fixtures, no populated index or live network acceptance')

if __name__=='__main__': main()
