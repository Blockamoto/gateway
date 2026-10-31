'use strict';
// Source-identical retained-layer controls; headless code QA, not live acceptance.
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
  await page.setContent('<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><main class="recipe-editor"><fieldset data-recipe="ordered" id="facts"></fieldset></main></body></html>');
  for(const name of ['theme.css','style.css','recipe-editor.css'])await page.addStyleTag({content:fs.readFileSync(path.join(root,'ui','shell',name),'utf8')});
  await page.addScriptTag({path:path.join(root,'ui','shell','recipe-ordered-editor.js')});
  await page.evaluate(()=>{window.applied=0;window.editor=GatewayOrderedRecipeEditor.mount(document.getElementById('facts'),{onApply(){applied++}})});
  for(const name of ['output-values.json','output-scripts.json','output-values-scripts.json']){
   const actual=JSON.parse(fs.readFileSync(path.join(root,'recipes','builtins',name),'utf8'));
   assert.equal(await page.evaluate(definition=>editor.load(definition),actual),true);assert.deepEqual(await page.evaluate(()=>editor.definition()),actual);
  }
  await page.getByLabel('Origin height',{exact:true}).fill('1');await page.getByLabel('Ordered recipe name',{exact:true}).fill('My retained output fields');
  const operations=await page.getByLabel('Add instruction type',{exact:true}).locator('option').evaluateAll(options=>options.map(option=>option.value));assert(operations.includes('emit'));assert(!operations.some(value=>['read','put','delete'].includes(value)));
  await page.locator('summary').filter({hasText:/^1\. if$/}).click();await page.locator('summary').filter({hasText:/^When true$/}).click();await page.locator('summary').filter({hasText:/^2\. emit$/}).click();
  const step=page.locator('summary').filter({hasText:/^2\. emit$/}).locator('..');await step.locator('summary').filter({hasText:/^Value$/}).click();
  await step.getByRole('button',{name:'Add object field',exact:true}).click();const last=step.getByLabel('Object key',{exact:true}).last();await last.fill('source_height');await last.press('Tab');
  await step.locator('summary').filter({hasText:/^Value for source_height$/}).click();const child=step.locator('summary').filter({hasText:/^Value for source_height$/}).locator('..');
  await child.getByLabel('Expression type',{exact:true}).selectOption('field');await child.getByLabel('Source field',{exact:true}).selectOption('height');
  const definition=await page.evaluate(()=>editor.definition());assert.equal(definition.origin,1);assert.equal(definition.name,'My retained output fields');assert.equal(definition.ordered.steps[0].body[0].body[1].expr.object.source_height.field,'height');
  await page.getByRole('button',{name:'Apply ordered changes',exact:true}).click();assert.equal(await page.evaluate(()=>applied),1);
  if(out)await page.screenshot({path:path.join(out,'output-fields-wide.png'),fullPage:true});await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  if(out)await page.screenshot({path:path.join(out,'output-fields-narrow.png'),fullPage:true});assert.deepEqual(requests,[]);assert.deepEqual(errors,[]);
  console.log('Output fields browser PASS: three actual layer documents, sparse operations, block-1 origin, custom field, Apply, narrow layout and no network work.');
 }finally{await browser.close()}
}
main().catch(error=>{console.error(error);process.exitCode=1});
