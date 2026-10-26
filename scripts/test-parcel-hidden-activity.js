'use strict';
// Exercise the shell's real Activity renderer with restored saved intent.
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const sandbox={window:{}};
vm.createContext(sandbox);
vm.runInContext(fs.readFileSync('ui/shell/workspace.js','utf8'),sandbox);
const source=fs.readFileSync('ui/shell/app.js','utf8'),start=source.indexOf('async function showIndexActivity()'),end=source.indexOf('async function showBlockStorage()',start);
assert(start>=0&&end>start,'Activity entry point must remain available');
const summary={innerHTML:''},root={innerHTML:''};let status={},scheduled=0,reads=0;
Object.assign(sandbox,{sequence:1,root,heading:()=>'',notice:message=>{throw Error(message)},$:id=>{assert.equal(id,'index-job-summary');return summary;},api:async path=>{assert.equal(path,'/api/v1/index/status');reads++;return status;},setTimeout:()=>{scheduled++;return scheduled;}});
vm.runInContext(source.slice(start,end),sandbox);
async function run(){
 for(const index of ['bitmap-parcels','bitmap-parcels-canonical','bitmap-parcels-canonical-v2','bitmap-parcels-standard-v1','bitmap-parcels-'+'1'.repeat(32),'bitmap-parcel-chain-dev-v1']){
  status={job:{id:'saved',index,state:'paused',height:792435,error:'Saved Parcel evidence is retained'}};
  const before=JSON.stringify(status);await sandbox.showIndexActivity();
  assert.doesNotMatch(summary.innerHTML,/parcel/i,'Activity hides every restored generation');assert.match(summary.innerHTML,/No index job started/);assert.equal(JSON.stringify(status),before);
 }
 status={definitions:[{id:'custom-alias',kind:'parcel'}],job:{id:'saved',index:'custom-alias',state:'paused'}};
 await sandbox.showIndexActivity();assert.doesNotMatch(summary.innerHTML,/custom-alias|parcel/i,'Kind metadata hides custom aliases');
 status={job:{id:'kept',index:'tx-locator',state:'paused',from:10,to:20,height:15}};
 await sandbox.showIndexActivity();assert.match(summary.innerHTML,/TX ID Locators/);assert.match(summary.innerHTML,/Committed through 15/);
 assert.equal(reads,8);assert.equal(scheduled,8,'Activity keeps its normal read-only refresh');
 console.log('Activity hidden-index checks PASS: restored generations/custom aliases absent; ordinary saved work retained; API documents unchanged.');
}
run().catch(error=>{console.error(error);process.exitCode=1;});
