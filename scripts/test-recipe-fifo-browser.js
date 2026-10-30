'use strict';
// Source-identical guided controls in an isolated, network-free DOM fixture.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const root=path.resolve(__dirname,'..');
const pw=(()=>{try{return require('playwright')}catch{return require(path.join(os.homedir(),'.cache','codex-runtimes','codex-primary-runtime','dependencies','node','node_modules','playwright'))}})();
const executablePath=[process.env.GATEWAY_TEST_BROWSER,pw.chromium.executablePath(),...['ProgramFiles(x86)','ProgramFiles','LOCALAPPDATA'].flatMap(key=>process.env[key]?[path.join(process.env[key],'Microsoft','Edge','Application','msedge.exe'),path.join(process.env[key],'Google','Chrome','Application','chrome.exe')]:[])].find(value=>value&&fs.existsSync(value));
const outArg=process.argv.indexOf('--out'),out=outArg<0?null:path.resolve(process.argv[outArg+1]);if(out)fs.mkdirSync(out,{recursive:true});
async function main(){
 const browser=await pw.chromium.launch({headless:true,executablePath});
 try{
  const page=await browser.newPage({viewport:{width:1100,height:900},reducedMotion:'reduce'}),errors=[],requests=[];
  page.on('pageerror',error=>errors.push(error.message));await page.route('**/*',route=>{requests.push(route.request().url());return route.abort();});
  await page.setContent('<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><main class="recipe-editor"><fieldset data-recipe="ordered" id="fifo"></fieldset></main></body></html>');
  for(const name of ['theme.css','style.css','recipe-editor.css'])await page.addStyleTag({content:fs.readFileSync(path.join(root,'ui','shell',name),'utf8')});
  await page.addScriptTag({path:path.join(root,'ui','shell','recipe-ordered-editor.js')});
  const current=JSON.parse(fs.readFileSync(path.join(root,'recipes','builtins','sat-state.json'),'utf8')),historical=JSON.parse(fs.readFileSync(path.join(root,'recipes','builtins','sat-ranges.json'),'utf8'));
  await page.evaluate(definition=>{window.changes=0;window.applied=0;window.editor=GatewayOrderedRecipeEditor.mount(document.getElementById('fifo'),{onChange(){changes++},onApply(){applied++}});if(!editor.load(definition))throw Error('FIFO document unavailable')},current);
  assert.deepEqual(await page.evaluate(()=>editor.definition()),current);
  await page.getByRole('button',{name:'Add retained-history table',exact:true}).click();assert.equal((await page.evaluate(()=>editor.definition())).tables.length,2);
  await page.getByRole('button',{name:'Remove retained-history table',exact:true}).click();
  await page.getByLabel('Add instruction type',{exact:true}).selectOption('let');await page.getByRole('button',{name:'Add instruction',exact:true}).click();
  const number=current.ordered.steps[0].body.length+1,summary=page.locator('summary').filter({hasText:new RegExp('^'+number+'\\. let value$')});await summary.click();
  const step=summary.locator('..');await step.getByLabel('Bind variable',{exact:true}).fill('extra_ranges');await step.locator('summary').filter({hasText:/^Value$/}).click();
  await step.getByLabel('Value type',{exact:true}).selectOption('ranges');await step.getByRole('button',{name:'Add interval',exact:true}).click();
  await step.getByLabel('Interval start (exact decimal)',{exact:true}).fill('100');await step.getByLabel('Interval end, exclusive (exact decimal)',{exact:true}).fill('105');
  assert.deepEqual((await page.evaluate(()=>editor.definition())).ordered.steps[0].body.at(-1).expr,{literal:{kind:'ranges',ranges:[{start:'100',end:'105'}]}});
  // Replace the literal through the same controls with a source-based range
  // operation. No JSON entry, save, run or network endpoint participates.
  await step.getByLabel('Expression type',{exact:true}).selectOption('call');await step.getByLabel('Function',{exact:true}).selectOption('range_subsidy');
  await step.locator('summary').filter({hasText:/^Argument 1$/}).click();
  const argument=step.locator('summary').filter({hasText:/^Argument 1$/}).locator('..');
  await argument.getByLabel('Expression type',{exact:true}).selectOption('field');await argument.getByLabel('Source field',{exact:true}).selectOption('height');
  assert.deepEqual((await page.evaluate(()=>editor.definition())).ordered.steps[0].body.at(-1).expr,{call:'range_subsidy',args:[{field:'height'}]});
  await page.getByRole('button',{name:'Apply ordered changes',exact:true}).click();assert.equal(await page.evaluate(()=>applied),1);
  if(out)await page.screenshot({path:path.join(out,'fifo-guided-wide.png'),fullPage:true});
  await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'FIFO controls fit a narrow viewport');
  if(out)await page.screenshot({path:path.join(out,'fifo-guided-narrow.png'),fullPage:true});
  await page.evaluate(definition=>editor.load(definition),historical);assert.deepEqual(await page.evaluate(()=>editor.definition()),historical);
  assert.equal(await page.getByRole('button',{name:'Remove retained-history table',exact:true}).isDisabled(),true,'a referenced history table cannot disappear silently');
  assert.deepEqual(requests,[],'guided editing starts no network or indexing work');assert.deepEqual(errors,[]);
  console.log('FIFO browser PASS: actual current/history documents, history declaration, exact interval controls, source-based range function composition, Apply, narrow layout and no network work.');
 }finally{await browser.close()}
}
main().catch(error=>{console.error(error);process.exitCode=1});
