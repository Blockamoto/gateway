'use strict';
// Address totals come from an authenticated local snapshot, never from a page of receipts.
window.GatewayAddressView=(()=>{
  const known=n=>Number.isSafeInteger(n)&&n>=0;
  const number=n=>known(n)?n.toLocaleString('en-GB'):'Unavailable';
  // Routing hints only: the backend validates SEC keys and indexes the exact
  // locking script. Never derive a P2PKH address or combine key encodings here.
  function destination(output={}){
    const value=typeof output==='string'?output:output?.destination||output?.address||'';
    const isPublicKey=typeof value==='string'&&/^(?:(?:02|03)[0-9a-f]{64}|04[0-9a-f]{128})$/i.test(value);
    return {value:isPublicKey?value.toLowerCase():value,isPublicKey,label:isPublicKey?'P2PK public key':'Address'};
  }
  function model(value={}){
    const ready=value.state==='ready',snapshot=value.snapshot||{};
    const exact=ready&&known(snapshot.height)&&!!snapshot.block_hash&&!!snapshot.commitment;
    return {...value,exact,balance:value.mode==='current'&&exact&&known(value.current_balance_sats)?value.current_balance_sats:null,count:value.mode==='current'&&exact&&known(value.current_output_count)?value.current_output_count:null};
  }
  function mount({root,address,api,onNavigate,onBuild}){
    const doc=root.ownerDocument,el=(tag,text,cls)=>{const n=doc.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
    let dead=false,generation=0,current=null;
    root.classList.remove('embed-host');root.replaceChildren();
    const target=destination(address),header=el('header',undefined,'page-heading'),heading=el('div');heading.append(el('div',target.isPublicKey?'BITCOIN / P2PK':'BITCOIN / ADDRESS','eyebrow'),el('h1',target.label),el('p',address,'mono break'));if(target.isPublicKey)heading.append(el('p','Outputs using this exact P2PK locking script. P2PKH outputs and other public-key encodings are counted separately.','small muted'));header.append(heading);
    const balance=el('section',undefined,'panel address-balance');balance.setAttribute('aria-label','Balance at the saved UTXO snapshot');balance.append(el('div','Current balance','eyebrow'));
    const amount=el('p','Unavailable','address-balance-value');amount.id='address-balance';const position=el('p','Reading the saved current-address snapshot…','muted small');position.id='address-snapshot';const status=el('p','','small');status.setAttribute('role','status');const actions=el('div',undefined,'actions');balance.append(amount,position,status,actions);
    function buildButton(id,label,rebuildSnapshot=null){const b=el('button',label);b.type='button';b.onclick=()=>onBuild?.(id,rebuildSnapshot);return b;}
    function section(mode,label){
      const details=el('details',undefined,'panel address-outputs'),summary=el('summary',label),note=el('p','','muted small'),list=el('div',undefined,'address-output-list'),message=el('p','','small'),controls=el('div',undefined,'actions'),more=el('button','Load more'),restart=el('button','Refresh from first page');
      details.id='address-'+mode;summary.id='address-'+mode+'-summary';message.setAttribute('role','status');more.type=restart.type='button';more.hidden=true;restart.hidden=true;controls.append(more,restart);if(mode==='historical')controls.append(buildButton('address-history','Review historical range'));details.append(summary,note,list,message,controls);
      const s={mode,details,summary,note,list,message,more,restart,value:null,cursor:'',loading:false,serial:0};
      details.addEventListener('toggle',()=>{if(details.open&&!s.value&&!s.loading)load(s);});more.onclick=()=>load(s,true);restart.onclick=()=>load(s);return s;
    }
    const utxos=section('current','Current UTXOs'),history=section('historical','Historical outputs');history.note.textContent='Optional creation-range history. Received outputs alone do not establish a current balance.';
    root.append(header,balance,utxos.details,history.details);
    function renderBalance(value){current=model(value);amount.textContent=current.balance===null?'Unavailable':number(current.balance)+' sats';balance.dataset.state=current.state||'not_built';
      position.textContent=current.exact?'Saved UTXO tip · block '+number(current.snapshot.height)+' · '+current.snapshot.block_hash:'No verified current-address snapshot is available.';
      status.textContent=(current.note||'')+(current.exact?' '+String(current.snapshot.validation||'Local validation').replaceAll('_',' ')+'. '+(current.snapshot.consensus_validated?'Consensus validated.':'Full consensus validation has not been performed.'):'');
      actions.replaceChildren();if(!current.exact)actions.append(buildButton(['dependency_missing','unavailable'].includes(current.state)?'utxo-state':'address-state',['dependency_missing','unavailable'].includes(current.state)?'Open UTXO set':['rebuild_required','corrupt'].includes(current.state)?'Review Current UTXOs rebuild':'Build Current UTXOs',['rebuild_required','corrupt'].includes(current.state)?current.snapshot:null));
      utxos.summary.textContent='Current UTXOs'+(current.count===null?'':' · '+number(current.count));
    }
    function outputRow(output,mode){const row=el('article',undefined,'address-output-row'),main=el('div'),link=el('button',output.output,'text-button mono');link.type='button';link.onclick=()=>onNavigate?.(output.output);main.append(link,el('p',output.script_type||'Unknown script type','small muted'));const facts=el('div',undefined,'address-output-facts');facts.append(el('strong',number(output.value_sats)+' sats'));
      const label=output.spent_status==='unspent'?(mode==='current'?'Unspent at snapshot':'Unspent'):output.spent_status==='spent'?'Spent':'Spend status unknown';facts.append(el('span',label,'small muted'));if(output.spent_status==='spent'&&output.spender){const spend=el('button','Spending input '+output.spender,'text-button mono');spend.type='button';spend.onclick=()=>onNavigate?.(output.spender);facts.append(spend);}row.append(main,facts);return row;
    }
    function renderPage(s,value,append){s.value=value;const usable=value.state==='ready'&&(s.mode!=='current'||current?.exact);s.cursor=usable?value.next_cursor||'':'';if(!append||!usable)s.list.replaceChildren();for(const output of usable?value.outputs||[]:[])s.list.append(outputRow(output,s.mode));s.more.hidden=!usable||!value.has_more||!s.cursor;s.restart.hidden=false;
      const ranges=(value.coverage||[]).map(r=>number(r.from)+'–'+number(r.to)).join(', '),gaps=(value.gaps||[]).map(r=>number(r.from)+'–'+number(r.to)).join(', ');
      if(s.mode==='historical'){s.summary.textContent='Historical outputs'+(known(value.received_output_count)?' · '+number(value.received_output_count):'');s.note.textContent=(known(value.received_sats)?'Received in indexed creation ranges: '+number(value.received_sats)+' sats. ':'')+'Creation coverage: '+(ranges||'not established')+'. '+(gaps?'Gaps: '+gaps+'. ':'')+(value.complete?'Covered ranges are complete. ':'Coverage is incomplete. ')+'These receipts do not establish a current balance.'+(known(value.snapshot?.height)&&value.snapshot?.block_hash?' Snapshot block '+number(value.snapshot.height)+' · '+value.snapshot.block_hash+'.':'');}
      else{s.note.textContent=current?.exact?'Outputs present at the exact saved UTXO tip. Values exclude mempool changes.':'Current output membership is unavailable.';}
      s.message.textContent=value.state!=='ready'?(value.note||'This index is not ready.'):(value.outputs||[]).length?'':append?'No further outputs in this snapshot.':value.complete?'No outputs in this snapshot or covered creation ranges.':'No outputs found in the available coverage; absence elsewhere is unknown.';
    }
    async function load(s,append=false){if(dead||s.loading)return;const ticket=++s.serial,view=generation;s.loading=true;s.more.disabled=s.restart.disabled=true;s.message.textContent='Loading '+(s.mode==='current'?'current UTXOs':'historical outputs')+'…';
      try{const url='/api/v1/address/view?address='+encodeURIComponent(address)+'&mode='+s.mode+'&limit=50'+(append?'&cursor='+encodeURIComponent(s.cursor):'');const value=await api(url);if(dead||view!==generation||ticket!==s.serial)return;if(value.address!==address||value.mode!==s.mode)throw Error('The address response does not match this view.');if(append&&value.state==='ready'&&JSON.stringify([value.snapshot?.height,value.snapshot?.block_hash,value.snapshot?.commitment])!==JSON.stringify([s.value?.snapshot?.height,s.value?.snapshot?.block_hash,s.value?.snapshot?.commitment]))throw Error('The address snapshot changed during pagination.');if(s.mode==='current')renderBalance(value);renderPage(s,value,append);}
      catch(error){if(dead||view!==generation||ticket!==s.serial)return;s.message.textContent=error.message+' Refresh from the first page to use the current snapshot.';s.restart.hidden=false;s.list.replaceChildren();s.cursor='';s.more.hidden=true;if(s.mode==='current')renderBalance({mode:'current',state:'waiting',note:error.message});}
      finally{if(!dead&&ticket===s.serial){s.loading=false;s.more.disabled=s.restart.disabled=false;}}
    }
    load(utxos);
    return {teardown(){dead=true;generation++;},refresh(){return load(utxos);}};
  }
  return {model,mount,destination};
})();
