#!/usr/bin/env python3
"""Chromium UI checks through intercepted HTTP requests to the real local API.
The application's HTML, assets, origin and CSP are retained. This is Linux UI
QA, not a native Windows installation, extension approval or public-network test.
"""
from __future__ import annotations
import argparse,hashlib,json,subprocess,tempfile,time,urllib.request,urllib.error,urllib.parse
from pathlib import Path
from playwright.sync_api import sync_playwright

def main():
 ap=argparse.ArgumentParser();ap.add_argument('--binary',type=Path,required=True);ap.add_argument('--out',type=Path,required=True);args=ap.parse_args();out=args.out.resolve();out.mkdir(parents=True,exist_ok=True)
 errors=[];console_errors=[];requests=[];observations=[]
 with tempfile.TemporaryDirectory(prefix='gateway-ui-052-') as td:
  d=Path(td);settings={'core_disabled':True,'core_mount_disabled':True,'network_disabled':True,'onboarded':True,'privacy_mode':True,'share_cache':False,'serve_data':False,'rpc_auth_mode':'auto','cache_blocks':True,'graph_index':True,'ord_enabled':True,'satline_enabled':True,'satline_use_peers':False,'satline_serve_published':False}
  (d/'settings.json').write_text(json.dumps(settings));log=(out/'ui-runtime.log').open('w')
  proc=subprocess.Popen([str(args.binary.resolve()),'-data',td,'-no-open','-no-tray'],stdout=log,stderr=subprocess.STDOUT)
  try:
   deadline=time.monotonic()+15
   while not (d/'runtime.json').exists():
    if proc.poll() is not None:raise RuntimeError('Gateway exited before startup')
    if time.monotonic()>deadline:raise TimeoutError('Gateway startup')
    time.sleep(.05)
   base=json.loads((d/'runtime.json').read_text())['url'].rstrip('/');origin=urllib.parse.urlsplit(base).netloc
   def api(path):
    with urllib.request.urlopen(base+path,timeout=10) as response:return json.load(response)
   def proxy(route):
    q=route.request;u=urllib.parse.urlsplit(q.url)
    if u.netloc!=origin:raise AssertionError('Unexpected browser network destination: '+q.url)
    headers={k:v for k,v in q.all_headers().items() if k.lower() not in ('host','content-length','connection','accept-encoding')}
    req=urllib.request.Request(q.url,data=q.post_data_buffer,headers=headers,method=q.method)
    requests.append({'path':u.path,'method':q.method})
    try:response=urllib.request.urlopen(req,timeout=15)
    except urllib.error.HTTPError as e:response=e
    body=response.read();headers={k:v for k,v in response.headers.items() if k.lower() not in ('content-length','transfer-encoding','connection','content-encoding')}
    route.fulfill(status=response.code,headers=headers,body=body)
   with sync_playwright() as pw:
    browser=pw.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox'])
    page=browser.new_page(viewport={'width':1440,'height':1080},reduced_motion='reduce')
    page.route('**/*',proxy);page.on('pageerror',lambda e:errors.append(str(e)));page.on('console',lambda m:console_errors.append(m.text) if m.type=='error' else None)
    response=page.goto(base,wait_until='networkidle');assert "script-src 'self'" in response.headers['content-security-policy']
    page.wait_for_selector('.seek-hero');page.wait_for_function("document.querySelector('[data-seek-status]').textContent.includes('Offline')")
    assert page.locator('.seek-emblem img').evaluate('(img)=>img.complete&&img.naturalWidth>0')
    def capture(name):
     for width,height in [(1440,1080),(390,844)]:
      page.set_viewport_size({'width':width,'height':height});page.wait_for_timeout(80)
      overflow=page.evaluate('document.documentElement.scrollWidth>innerWidth')
      assert not overflow,(name,width,'page overflow')
      observations.append({'surface':name,'width':width,'overflow':overflow})
      page.screenshot(path=str(out/f'{name}-{width}.png'),full_page=True)
    capture('home')
    page.locator('[data-route="discovery.gateway"]').first.click();page.wait_for_selector('[data-seek-advertisement]');capture('discovery')
    assert page.locator('[data-discovery-action="pause"]').is_disabled()
    page.locator('[data-route="sync.gateway"]').first.click();page.wait_for_selector('.coverage-row');capture('data-sync')
    assert page.locator('svg.coverage-track').count()==5
    page.locator('[data-route="peers.gateway"]').first.click();page.wait_for_function("document.querySelector('#content').textContent.includes('Core')");capture('peers')
    page.locator('[data-gateway-setup]').first.click();page.wait_for_selector('#gateway-setup[open]')
    names=['Welcome','Your Bitcoin connection','Components and storage','Windows integration','Browser Companion','Participation and privacy','Enter Gateway']
    for stage,title in enumerate(names):
     page.wait_for_function('(title)=>document.querySelector("#setup-title").textContent===title',arg=title)
     if stage==1:
      assert page.locator('[name="connection-mode"]').count()==2
      page.locator('[name="connection-mode"][value="standalone"]').check();page.locator('#setup-mount').uncheck()
     if stage==2:assert page.locator('#setup-prepare').count()==0
     if stage==3:
      for name in ['setup-startup','setup-god','setup-domain']:assert not page.locator('#'+name).is_checked()
     if stage==4:
      assert page.locator('#setup-open-browser').inner_text()=='Copy extensions address & open browser'
      assert page.locator('#setup-open-folder').inner_text()=='Open companion folder'
      page.locator('#setup-profile').fill('Profile 2')
     if stage==5:page.locator('#setup-network').uncheck()
     for width,height in [(1440,1080),(390,844)]:
      page.set_viewport_size({'width':width,'height':height});page.wait_for_timeout(50)
      assert not page.evaluate('document.documentElement.scrollWidth>innerWidth'),(title,width)
      assert not page.evaluate('document.querySelector("#gateway-setup").scrollWidth>document.querySelector("#gateway-setup").clientWidth+2'),(title,width,'dialog overflow')
      observations.append({'surface':'setup-'+str(stage+1),'title':title,'width':width,'overflow':False})
     if stage in (0,4,6):page.screenshot(path=str(out/f'setup-{stage+1}-390.png'),full_page=True)
     page.locator('#setup-next').click()
    page.wait_for_function('!document.querySelector("#gateway-setup").open')
    snap=api('/api/v1/setup')
    assert snap['progress']['completed'] and snap['progress']['integration_reviewed'] and snap['progress']['profile']=='Profile 2'
    assert snap['settings']['core_disabled'] and snap['settings']['core_mount_disabled'] and snap['settings']['network_disabled']
    assert len(snap['pending'])>=3 and not snap['browser']['native_active']
    page.locator('[data-gateway-setup]').first.click();page.wait_for_function('document.querySelector("#setup-title").textContent==="Enter Gateway"');page.locator('#setup-close').click()
    page.wait_for_timeout(3200) # At least one further network poll after navigation.
    assert not errors,errors
    assert not console_errors,console_errors
    browser.close()
   (out/'ui-results.json').write_text(json.dumps({'status':'PASS','binary_sha256':hashlib.sha256(args.binary.read_bytes()).hexdigest(),'environment':'Linux Chromium; HTTP interception forwards to real running local Gateway API, preserving application origin, assets and CSP. Not native Windows or extension approval.','surfaces':observations,'origin_and_csp_retained':True,'logo_loaded':True,'two_connection_choices':True,'missing_mount_preparation_hidden':True,'portable_integrations_unselected':True,'browser_actions_separate':True,'resume_and_profile_preserved':True,'pending_tasks_remain_pending':True,'javascript_errors':errors,'console_errors':console_errors,'requests':requests},indent=2))
   print('PASS: home, discovery, Data & Sync, peers, seven setup stages at desktop/mobile sizes; real local API and CSP; privacy and resume preserved.')
  finally:
   proc.terminate()
   try:proc.wait(timeout=5)
   except subprocess.TimeoutExpired:proc.kill();proc.wait()
   log.close()
if __name__=='__main__':main()
