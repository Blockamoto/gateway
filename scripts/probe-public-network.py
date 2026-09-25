#!/usr/bin/env python3
"""Bounded real runtime probe. A failed bootstrap is evidence of failure, not a pass."""
import argparse,hashlib,json,subprocess,tempfile,time,urllib.request
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--binary',type=Path,required=True);p.add_argument('--out',type=Path,required=True);args=p.parse_args();args.out.mkdir(parents=True,exist_ok=True)
with tempfile.TemporaryDirectory(prefix='gateway-public-052-') as td:
 d=Path(td);(d/'settings.json').write_text(json.dumps({'core_disabled':True,'core_mount_disabled':True,'network_disabled':False,'onboarded':True,'rpc_auth_mode':'auto','privacy_mode':True,'share_cache':False,'serve_data':False,'satline_use_peers':False,'satline_serve_published':False}))
 log=(args.out/'public-runtime.log').open('w');proc=subprocess.Popen([str(args.binary.resolve()),'-data',td,'-no-open','-no-tray'],stdout=log,stderr=subprocess.STDOUT)
 samples=[]
 try:
  deadline=time.monotonic()+12
  while not (d/'runtime.json').exists():
   if proc.poll() is not None:raise RuntimeError('Runtime exited before API startup')
   if time.monotonic()>deadline:raise TimeoutError('runtime startup')
   time.sleep(.05)
  runtime=json.loads((d/'runtime.json').read_text());url=runtime['url']
  for _ in range(8):
   with urllib.request.urlopen(url+'/api/v1/network',timeout=5) as r:s=json.load(r)
   # Persist observable status, never the runtime authentication token.
   s.pop('data_directory',None);samples.append(s);time.sleep(1.5)
  live=max(x['connected'] for x in samples)
  result={'binary_sha256':hashlib.sha256(args.binary.read_bytes()).hexdigest(),'environment':'Linux container, fresh profile, Core disabled, no mount, no supplied peers. Actual runtime bootstrap, not a fixture.','status':'OBSERVED_CONNECTION_REQUIRES_FURTHER_ACCEPTANCE' if live else 'NO_PUBLIC_CONNECTION_ESTABLISHED','elapsed_seconds':12,'max_connections':live,'max_header_height':max(x['headers']['header_height'] for x in samples),'automatic_public_gateway_discovery':any(x['gateway_connected'] for x in samples),'samples':samples,'interpretation':'This does not establish unrestricted public-network acceptance or native Windows behavior.'}
  (args.out/'public-network-attempt.json').write_text(json.dumps(result,indent=2));print(json.dumps({k:v for k,v in result.items() if k!='samples'},indent=2))
 finally:
  proc.terminate()
  try:proc.wait(timeout=5)
  except subprocess.TimeoutExpired:proc.kill();proc.wait()
  log.close()
