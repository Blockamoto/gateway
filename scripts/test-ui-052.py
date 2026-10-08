#!/usr/bin/env python3
"""Linux Chromium rendering + live local API bridge, NOT native Windows QA.
Starts an isolated, offline Gateway profile. Browser fetch is bridged through
Python urllib because this environment blocks ordinary browser networking.
"""
from __future__ import annotations
import argparse,base64,hashlib,json,re,subprocess,tempfile,time,urllib.request,urllib.error
from pathlib import Path
from playwright.sync_api import sync_playwright

def main():
 ap=argparse.ArgumentParser();ap.add_argument('--binary',type=Path,required=True);ap.add_argument('--out',type=Path,required=True);args=ap.parse_args()
 root=Path(__file__).resolve().parents[1];out=args.out.resolve();out.mkdir(parents=True,exist_ok=True)
 errors=[];requests=[];observations=[]
 with tempfile.TemporaryDirectory(prefix='gateway-ui-052-') as td:
  data=Path(td)
  settings={'core_disabled':True,'core_mount_disabled':True,'network_disabled':True,'headers_paused':False,'onboarded':True,'rpc_auth_mode':'auto','rpc_port':8332,'privacy_mode':True,'share_cache':False,'cache_blocks':True,'graph_index':True,'ord_enabled':True,'satline_enabled':True,'serve_data':False,'satline_use_peers':False,'satline_serve_published':False}
  (data/'settings.json').write_text(json.dumps(settings))
  log=(out/'ui-runtime.log').open('w');proc=subprocess.Popen([str(args.binary.resolve()),'-data',td,'-no-open','-no-tray'],stdout=log,stderr=subprocess.STDOUT)
  try:
   deadline=time.monotonic()+15
   while not (data/'runtime.json').exists():
    if proc.poll() is not None: raise RuntimeError('Gateway exited; see ui-runtime.log')
    if time.monotonic()>deadline:raise TimeoutError('runtime metadata')
    time.sleep(.05)
   runtime=json.loads((data/'runtime.json').read_text());base=runtime['url']
   def request(q):
    path=q['path'];method=q.get('method','GET');body=q.get('body')
    if not path.startswith('/'):raise ValueError('Only local paths allowed in UI test bridge')
    requests.append({'path':path,'method':method})
    headers={'Content-Type':'application/json'}
    req=urllib.request.Request(base+path,data=body.encode() if body else None,headers=headers,method=method)
    try:
     with urllib.request.urlopen(req,timeout=15) as r:return {'status':r.status,'text':r.read().decode()}
    except urllib.error.HTTPError as e:return {'status':e.code,'text':e.read().decode()}
   html=request({'path':'/'})['text']
   html=re.sub(r'<link[^>]+>', '',html)
   html=re.sub(r'<script[^>]+src=[^>]+></script>', '',html)
   with sync_playwright() as pw:
    browser=pw.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox'])
    page=browser.new_page(viewport={'width':1440,'height':1100},reduced_motion='reduce')
    page.on('pageerror',lambda e:errors.append(str(e)))
    page.expose_function('gatewayTestBridge',request)
    page.set_content(html)
    page.evaluate('''() => {history.replaceState=()=>{};history.pushState=()=>{};window.fetch=async(path,opts={})=>{const r=await window.gatewayTestBridge({path:String(path),method:opts.method||'GET',body:opts.body||null});return {ok:r.status>=200&&r.status<300,status:r.status,statusText:String(r.status),json:async()=>JSON.parse(r.text),text:async()=>r.text}};}''')
    mark='data:image/svg+xml;base64,'+base64.b64encode(request({'path':'/shell/gateway-mark.svg'})['text'].encode()).decode()
    for name in ('style.css','network.css','discovery.css'):page.add_style_tag(content=request({'path':'/shell/'+name})['text'].replace('/shell/gateway-mark.svg',mark))
    for name in ('discovery.js','app.js','network.js'):page.add_script_tag(content=request({'path':'/shell/'+name})['text'].replace('/shell/gateway-mark.svg',mark))
    page.wait_for_function("document.querySelector('#network-summary').textContent.toLowerCase().includes('offline')")
    assert page.locator('.seek-emblem img').evaluate('(img)=>img.complete&&img.naturalWidth>0')
    def capture(name):
     for width,height in [(1440,1100),(390,844)]:
      page.set_viewport_size({'width':width,'height':height});page.wait_for_timeout(70)
      overflow=page.evaluate('document.documentElement.scrollWidth>innerWidth');assert not overflow,(name,width,'overflow')
      observations.append({'surface':name,'width':width,'overflow':False})
      page.screenshot(path=str(out/f'{name}-{width}.png'),full_page=True)
    capture('home')
    page.locator('[data-route="discovery.gateway"]').first.click();page.wait_for_selector('[data-seek-advertisement]');capture('discovery')
    page.locator('[data-route="sync.gateway"]').first.click();page.wait_for_selector('.coverage-row');capture('data-sync')
    assert page.locator('svg.coverage-track').count()==5
    page.locator('[data-route="peers.gateway"]').first.click();page.wait_for_function("document.querySelector('#content').textContent.includes('Core')");capture('peers')
    page.locator('[data-gateway-setup]').first.click();page.wait_for_selector('#gateway-setup[open]')
    names=['Welcome','Your Bitcoin connection','Components and storage','Windows integration','Browser Companion','Participation and privacy','Enter Gateway']
    for stage,title in enumerate(names):
     page.wait_for_function('(title)=>document.querySelector("#setup-title").textContent===title',arg=title)
     for width,height in [(1440,1100),(390,844)]:
      page.set_viewport_size({'width':width,'height':height});page.wait_for_timeout(60)
      overflow=page.evaluate('document.documentElement.scrollWidth>innerWidth')
      dialog_overflow=page.evaluate('document.querySelector("#gateway-setup").scrollWidth>document.querySelector("#gateway-setup").clientWidth+2')
      assert not overflow and not dialog_overflow,(title,width,overflow,dialog_overflow)
      observations.append({'stage':stage,'title':title,'width':width,'overflow':overflow,'dialog_overflow':dialog_overflow})
     if stage in (0,4,6):
      page.screenshot(path=str(out/f'setup-{stage+1}-mobile.png'),full_page=True)
      page.set_viewport_size({'width':1440,'height':1100});page.screenshot(path=str(out/f'setup-{stage+1}-desktop.png'),full_page=True)
     if stage==1:
      assert page.locator('[name="connection-mode"]').count()==2
      page.locator('input[name="connection-mode"][value="standalone"]').check();page.locator('#setup-mount').uncheck()
     if stage==2:assert page.locator('#setup-prepare').count()==0
     if stage==3:
      for name in ['setup-startup','setup-god','setup-domain']:assert not page.locator('#'+name).is_checked()
     if stage==4:
      assert page.locator('#setup-open-browser').inner_text()=='Copy extensions address & open browser'
      assert page.locator('#setup-open-folder').inner_text()=='Open companion folder'
      assert 'pending' in page.locator('#setup-browser-state').inner_text().lower()
      page.locator('#setup-profile').fill('Profile 2')
     if stage==5:page.locator('#setup-network').uncheck()
     page.locator('#setup-next').click()
    page.wait_for_function('!document.querySelector("#gateway-setup").open')
    snap=json.loads(request({'path':'/api/v1/setup'})['text'])
    assert snap['progress']['integration_reviewed']
    assert snap['progress']['completed'] and len(snap['pending'])>=3 and not snap['browser']['native_active']
    assert snap['settings']['core_disabled'] and snap['settings']['core_mount_disabled'] and snap['settings']['network_disabled']
    assert not snap['settings']['satline_use_peers'] and not snap['settings']['satline_serve_published']
    page.locator('[data-gateway-setup]').first.click();page.wait_for_function('document.querySelector("#setup-title").textContent==="Enter Gateway"')
    assert snap['progress']['profile']=='Profile 2'
    page.locator('#setup-close').click();page.locator('#headers-pause').click();page.wait_for_timeout(100)
    headers=json.loads(request({'path':'/api/v1/network'})['text']);assert headers['headers']['headers_paused'] if 'headers_paused' in headers['headers'] else json.loads(request({'path':'/api/v1/setup'})['text'])['settings']['headers_paused']
    page.wait_for_timeout(3200)
    assert not errors,errors
    browser.close()
   (out/'ui-results.json').write_text(json.dumps({'status':'PASS','binary_sha256':hashlib.sha256(args.binary.read_bytes()).hexdigest(),'environment':'Linux Chromium rendering with Python bridge to real local Gateway HTTP API. Assets obtained from the tested binary. Logo embedded as identical SVG data bytes for this renderer. Browser network blocked by platform policy; native origin/CSP/Windows integration not runtime tested here.','stages':observations,'completed_with_visible_pending_tasks':True,'resume_last_stage':True,'profile_choice_preserved':True,'privacy_preserved':True,'header_pause_persisted':True,'javascript_errors':errors,'requests':requests},indent=2))
   print('PASS: discovery/home/coverage/peers and seven setup stages, desktop + mobile, pending readiness, resume, privacy, header pause. Linux rendering/API bridge only.')
  finally:
   proc.terminate()
   try:proc.wait(timeout=5)
   except subprocess.TimeoutExpired:proc.kill();proc.wait()
   log.close()
if __name__=='__main__':main()
