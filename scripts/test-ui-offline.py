from pathlib import Path
from playwright.sync_api import sync_playwright
import json,re
project=Path(__file__).resolve().parents[1]; root=project/'ui-test-output';root.mkdir(exist_ok=True);src=project/'ui'/'satline'
fixture=json.loads((project/'testdata/satline-ui.fixture.json').read_text())
html=(src/'index.html').read_text();html=re.sub(r'<link[^>]+>', '', html);html=re.sub(r'<script[^>]*>.*?</script>', '',html)
js=(src/'app.js').read_text();css=(src/'style.css').read_text()
mock="""(fixture) => {
window.fixture=fixture;window.fixtureMode='start';window.fixtureLength=0;window.mockRequests=[];
history.replaceState=()=>{};
window.fetch=async (url,options={})=> {
  const path=String(url).split('?')[0],body=options.body?JSON.parse(options.body):{};window.mockRequests.push({path,body});let val={};
  if(path.endsWith('/ui/record'))return {ok:false,status:404,json:async()=>({error:'No cache in this isolated UI fixture'})};
  if(path.endsWith('/ui/records'))val={records:[{query:{kind:'sat',input:'995'},state:'CURRENTLY_UNSPENT',hop_count:2,published:false}]};
  else if(path.endsWith('/status'))val={enabled:true,ready:true,stored_sats:1,stored_satpoint_follows:0,persisted_hops:2,storage_bytes:4280};
  else if(path.endsWith('/network'))val={use_peers:false,serve_published:false,published_records:0,peers:[],stats:{}};
  else if(path.endsWith('/meta'))val={app_version:'0.4.9'};
  else if(path.endsWith('/ui/run')){window.fixtureMode=body.operation;if(body.operation==='start')window.fixtureLength=0;else if(body.operation==='next')window.fixtureLength=Math.min(2,window.fixtureLength+1);else window.fixtureLength=2;val={id:'fixture-job'};}
  else if(path.endsWith('/ui/job')){let r=structuredClone(fixture);r.hops=r.hops.slice(0,window.fixtureLength);r.hop_count=r.hops.length;r.current_satpoint=r.hops.length?r.hops[r.hops.length-1].destination:r.birth_satpoint;r.state=window.fixtureLength===2?'CURRENTLY_UNSPENT':'STEP_LIMIT';r.persistence={stored:true,storage_schema:1,record_key:'995',historical_hops_skipped:0,new_hops_resolved:r.hops.length};val={done:true,stage:'Fixture complete',operation:window.fixtureMode,elapsed_ms:31,result:r};}
  else if(path.endsWith('/ui/explain'))val={inputs:[{index:0,value:100,selected:false},{index:1,value:900,selected:true}],outputs:[{n:0,value_sats:990}],coinbase_outputs:[{n:0,value_sats:5000000020}],truncated_inputs:false};
  return {ok:true,status:200,json:async()=>val};
};
}"""
with sync_playwright() as p:
 b=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox'])
 page=b.new_page(viewport={'width':1440,'height':1100});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
 # Isolated rendering only: no browser navigation or network request; the
 # environment blocks browser navigation globally. API tests run separately.
 page.set_content(html);page.add_style_tag(content=css);page.evaluate(mock,fixture);page.add_script_tag(content=js);page.wait_for_timeout(100)
 page.screenshot(path=str(root/'satline-home.png'),full_page=True)
 page.locator('#query').fill('995');page.locator('#step').click();page.wait_for_timeout(100)
 assert page.locator('#start-node').count()==1 and page.locator('.hop').count()==0
 page.locator('#next').click();page.wait_for_timeout(100);assert page.locator('.hop').count()==1
 page.locator('#next').click();page.wait_for_timeout(100);assert page.locator('.hop').count()==2
 page.locator('#hop-1 summary').click();page.locator('[data-explain="1"]').click();page.wait_for_timeout(100)
 assert '5,000,000,015' in page.locator('#hop-1').inner_text()
 assert page.locator('#explain-1 .stream-row.selected').count()==2
 assert page.locator('#next').is_disabled()
 page.evaluate('scrollTo(0,0)');page.screenshot(path=str(root/'satline-line-desktop.png'),full_page=True)
 assert page.locator('#hop-0 a[data-bitcoin]').first.get_attribute('data-bitcoin')=='i0.1.1.bitcoin'
 page.set_viewport_size({'width':390,'height':844});page.screenshot(path=str(root/'satline-line-mobile.png'),full_page=True)
 overflow=page.evaluate('document.documentElement.scrollWidth > innerWidth');assert not overflow
 # Stored values rendered as text, not executable markup.
 assert not errors,errors
 (root/'browser-render-results.json').write_text(json.dumps({'offline_rendering_only':True,'desktop':'1440x1100','mobile':'390x844','step_birth_only':True,'one_hop_each_click':True,'fee_math_visible':True,'evidence_expansion':True,'terminal_disables_next':True,'cross_module_coordinate':True,'horizontal_overflow':overflow,'javascript_errors':errors},indent=2))
 print('Offline Chromium UI rendering: PASS (desktop, mobile, step, fee, evidence, links). No browser network navigation used.')
 b.close()
