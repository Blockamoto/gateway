'use strict';
window.GatewayBlockViewport = (() => {
// Adapted from Bitfeed (MIT, Mononaut): utils/misc.js, TxMondrianPoolScene.js,
// TxBlockScene.js, TxController.js and utils/easing.js. See LICENSE.
function squareSize(tx, mode) {
  return mode === 'value'
    ? Math.max(1, Math.ceil(Math.log10(Math.max(1, tx.value))) - 5)
    : Math.max(1, Math.ceil(Math.sqrt(tx.vbytes / 256)));
}

// Upstream's Mondrian free-square layout fills the first available slot in
// transaction order. Smaller transactions fill the gaps around larger ones.
class MondrianLayout {
  constructor(width) { this.width = width; this.rows = []; }

  addRow() {
    const row = { y: this.rows.length, slots: [], map: new Map() };
    this.rows.push(row);
    return row;
  }

  addSlot(slot) {
    if (slot.r <= 0) return;
    const row = this.rows[slot.y];
    const existing = row.map.get(slot.x);
    if (existing) {
      existing.r = Math.max(existing.r, slot.r);
      return existing;
    }
    const index = row.slots.findIndex((other) => other.x > slot.x);
    if (index < 0) row.slots.push(slot);
    else row.slots.splice(index, 0, slot);
    row.map.set(slot.x, slot);
    return slot;
  }

  removeSlot(slot) {
    const row = this.rows[slot.y];
    row.map.delete(slot.x);
    row.slots.splice(row.slots.indexOf(slot), 1);
  }

  fillSlot(slot, size) {
    const right = slot.x + size;
    const top = slot.y + size;
    this.removeSlot(slot);
    for (let y = slot.y; y < top; y++) {
      const row = this.rows[y];
      if (row) {
        const collisions = [];
        let excess = 0;
        for (const candidate of row.slots) {
          if (!(candidate.x + candidate.r < slot.x || candidate.x >= right)) {
            collisions.push(candidate);
            excess = Math.max(excess, candidate.x + candidate.r - slot.x - slot.r);
          }
        }
        if (right < this.width && !row.map.has(right)) {
          this.addSlot({ x: right, y, r: slot.r - size + excess });
        }
        for (const collision of collisions) {
          collision.r = slot.x - collision.x;
          if (collision.r <= 0) this.removeSlot(collision);
        }
      } else {
        this.addRow();
        if (slot.x > 0) this.addSlot({ x: 0, y, r: slot.x });
        if (right < this.width) this.addSlot({ x: right, y, r: this.width - right });
      }
    }
    for (let y = Math.max(0, slot.y - size); y < slot.y; y++) {
      const row = this.rows[y];
      for (let index = 0; index < row.slots.length; index++) {
        const candidate = row.slots[index];
        if (candidate.x < right && candidate.x + candidate.r > slot.x && candidate.y + candidate.r >= slot.y) {
          const oldWidth = candidate.r;
          candidate.r = slot.y - candidate.y;
          if (candidate.r <= 0) this.removeSlot(candidate);
          const remaining = { x: candidate.x + candidate.r, y: candidate.y, w: oldWidth - candidate.r, h: candidate.r };
          while (remaining.w > 0 && remaining.h > 0) {
            if (remaining.w <= remaining.h) {
              this.addSlot({ x: remaining.x, y: remaining.y, r: remaining.w });
              remaining.y += remaining.w;
              remaining.h -= remaining.w;
            } else {
              this.addSlot({ x: remaining.x, y: remaining.y, r: remaining.h });
              remaining.x += remaining.h;
              remaining.w -= remaining.h;
            }
          }
        }
      }
    }
    return { x: slot.x, y: slot.y };
  }

  place(size) {
    for (const row of this.rows) {
      const slot = row.slots.find((candidate) => candidate.r >= size);
      if (slot) return this.fillSlot(slot, size);
    }
    const row = this.addRow();
    return this.fillSlot(this.addSlot({ x: 0, y: row.y, r: this.width }), size);
  }
}

function pack(transactions, mode = 'value') {
  const tiles = transactions.map((tx) => ({ tx, s: squareSize(tx, mode) }));
  const width = Math.max(1, Math.ceil(Math.sqrt(tiles.reduce((sum, tile) => sum + tile.s ** 2, 0))));
  const layout = new MondrianLayout(width);
  let height = 0;
  for (const tile of tiles) {
    Object.assign(tile, layout.place(tile.s));
    height = Math.max(height, tile.y + tile.s);
  }
  return { tiles, width, height };
}

// BitFeed's painted square is half a grid unit smaller than its occupied slot.
// Fitting the complete grid changes only the common scale and translation.
function tileGeometry(tile, layout) {
  const span = Math.max(layout.width - .5, layout.height - .5, 1);
  const unit = 100 / span;
  return {
    x: (100 - Math.max(0, layout.width - .5) * unit) / 2 + tile.x * unit,
    y: (100 - Math.max(0, layout.height - .5) * unit) / 2 + tile.y * unit,
    size: (tile.s - .5) * unit,
    gap: .5 * unit
  };
}

function navigation(root, doc) {
  const win=doc.defaultView || window, saved=new Map(), pointers=new Map();
  let surface=null, key='', value={zoom:1,x:0,y:0}, drag=null, pinch=null, space=false, panMode=false, suppressUntil=0, dead=false;
  const now=()=>win.performance?.now?.() ?? Date.now();
  const bounds=()=>root.querySelector('.viewport-stage')?.getBoundingClientRect() || root.getBoundingClientRect();
  function remember(){if(!key)return;saved.delete(key);saved.set(key,{...value});while(saved.size>32)saved.delete(saved.keys().next().value);}
  function paint(){if(surface)surface.style.transform='translate('+value.x+'px,'+value.y+'px) scale('+value.zoom+')';root.dataset.zoom=String(value.zoom);root.classList.toggle('viewport-grab',space||panMode);root.classList.toggle('viewport-move-content',space||panMode);root.classList.toggle('viewport-dragging',Boolean(drag?.moved||pinch));root.querySelector('[data-viewport-pan]')?.setAttribute('aria-pressed',String(panMode));}
  function zoom(factor,clientX,clientY){const rect=bounds(),x=(clientX??rect.left+rect.width/2)-rect.left,y=(clientY??rect.top+rect.height/2)-rect.top,next=Math.max(.001,Math.min(32,value.zoom*factor)),ratio=next/value.zoom;value={zoom:next,x:x-(x-value.x)*ratio,y:y-(y-value.y)*ratio};paint();remember();}
  function fit(){
    const svg=surface?.querySelector('.transaction-flow-svg'),extent=svg?.flowLayout?.bounds,matrix=svg?.getScreenCTM(),rect=bounds();
    if(extent&&matrix&&rect.width>0&&rect.height>0){
      const points=[[extent.x,extent.y],[extent.x+extent.width,extent.y+extent.height]].map(([x,y])=>({x:(matrix.a*x+matrix.c*y+matrix.e-rect.left-value.x)/value.zoom,y:(matrix.b*x+matrix.d*y+matrix.f-rect.top-value.y)/value.zoom}));
      const width=points[1].x-points[0].x,height=points[1].y-points[0].y;
      const scale=Math.max(.001,Math.min(1,Math.max(1,rect.width-24)/Math.max(1,width),Math.max(1,rect.height-24)/Math.max(1,height)));
      value={zoom:scale,x:(rect.width-width*scale)/2-points[0].x*scale,y:(rect.height-height*scale)/2-points[0].y*scale};
    }else value={zoom:1,x:0,y:0};
    paint();remember();
  }
  function attach(node,nextKey,moveContent=false){remember();surface=node;key=String(nextKey||'');panMode=moveContent;value={...(saved.get(key)||{zoom:1,x:0,y:0})};paint();}
  function detach(){remember();surface=null;key='';drag=null;pinch=null;panMode=false;space=false;pointers.clear();paint();}
  const editable=target=>Boolean(target?.closest?.('input,textarea,select,[contenteditable=true],.viewport-navigation,[data-flow-inscription-menu],[role=menuitem]'));
  const wheel=event=>{if(!surface||editable(event.target))return;event.preventDefault();const units=event.deltaMode===1?16:event.deltaMode===2?Math.max(1,bounds().height):1,dx=event.deltaX*units,dy=event.deltaY*units;if(!event.ctrlKey&&!event.metaKey&&Math.abs(dx)>Math.abs(dy)){value.x-=dx;paint();remember();}else zoom(Math.exp(-Math.max(-800,Math.min(800,dy))*.002),event.clientX,event.clientY);};
  const down=event=>{if(!surface||event.button!==0||editable(event.target))return;suppressUntil=0;pointers.set(event.pointerId,{x:event.clientX,y:event.clientY});if(pointers.size===2){const [a,b]=[...pointers.values()];pinch={distance:Math.hypot(a.x-b.x,a.y-b.y),x:(a.x+b.x)/2,y:(a.y+b.y)/2};drag=null;root.setPointerCapture?.(event.pointerId);event.preventDefault();}else if(pointers.size===1){drag={id:event.pointerId,x:event.clientX,y:event.clientY,startX:value.x,startY:value.y,moved:false};}paint();};
  const move=event=>{if(!pointers.has(event.pointerId)||!surface)return;pointers.set(event.pointerId,{x:event.clientX,y:event.clientY});if(pinch&&pointers.size===2){const [a,b]=[...pointers.values()],distance=Math.hypot(a.x-b.x,a.y-b.y),x=(a.x+b.x)/2,y=(a.y+b.y)/2;if(pinch.distance>0)zoom(distance/pinch.distance,pinch.x,pinch.y);value.x+=x-pinch.x;value.y+=y-pinch.y;pinch={distance,x,y};paint();remember();event.preventDefault();return;}if(!drag||drag.id!==event.pointerId)return;const dx=event.clientX-drag.x,dy=event.clientY-drag.y;if(!drag.moved&&Math.hypot(dx,dy)<5)return;if(!drag.moved)root.setPointerCapture?.(event.pointerId);drag.moved=true;value.x=drag.startX+dx;value.y=drag.startY+dy;paint();remember();event.preventDefault();};
  const up=event=>{const moved=Boolean(drag?.moved||pinch);pointers.delete(event.pointerId);if(moved)suppressUntil=now()+400;if(drag?.id===event.pointerId)drag=null;if(pointers.size<2)pinch=null;if(!pointers.size)drag=null;if(root.hasPointerCapture?.(event.pointerId))root.releasePointerCapture(event.pointerId);paint();};
  const cancel=()=>{if(drag?.moved||pinch)suppressUntil=now()+400;drag=null;pinch=null;pointers.clear();space=false;paint();};
  const click=event=>{if(!editable(event.target)&&now()<suppressUntil){event.preventDefault();event.stopImmediatePropagation();}};
  const keyDown=event=>{if(event.code!=='Space'||editable(event.target)||!surface)return;if(!root.contains(doc.activeElement)&&!root.matches?.(':hover'))return;space=true;paint();event.preventDefault();};
  const keyUp=event=>{if(event.code==='Space'){space=false;paint();}};
  root.addEventListener('wheel',wheel,{passive:false});root.addEventListener('pointerdown',down);root.addEventListener('pointermove',move);root.addEventListener('pointerup',up);root.addEventListener('pointercancel',up);root.addEventListener('lostpointercapture',cancel);root.addEventListener('click',click,true);doc.addEventListener('keydown',keyDown);doc.addEventListener('keyup',keyUp);win.addEventListener('blur',cancel);
  return {attach,detach,zoom,fit,pan:()=>{panMode=!panMode;paint();},mode:()=>panMode,state:()=>({...value,key}),teardown(){if(dead)return;dead=true;detach();root.removeEventListener('wheel',wheel);root.removeEventListener('pointerdown',down);root.removeEventListener('pointermove',move);root.removeEventListener('pointerup',up);root.removeEventListener('pointercancel',up);root.removeEventListener('lostpointercapture',cancel);root.removeEventListener('click',click,true);doc.removeEventListener('keydown',keyDown);doc.removeEventListener('keyup',keyUp);win.removeEventListener('blur',cancel);}};
}


  const esc = value => String(value ?? '').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const number = value => Number.isFinite(value) ? value.toLocaleString('en-GB') : 'Unknown';
  function mount({root, document:doc, onTransaction, onInput, onOutput, onInscription}) {
    if(!root)throw Error('A shared viewport is required.');
    const el=(tag,text,cls)=>{const node=doc.createElement(tag);if(text!==undefined)node.textContent=text;if(cls)node.className=cls;return node;};
    let dead=false,kind='empty',selected=null,lastMap=null,lastFlow=null,scene=null,stage=null;
    const nav=navigation(root,doc),host=root.closest('#workspace-viewport');
    function controls(){const tools=el('div',undefined,'viewport-navigation');tools.setAttribute('role','toolbar');tools.setAttribute('aria-label','Viewport navigation');for(const [label,text,action]of [['Zoom out','−',()=>nav.zoom(1/1.25)],['Fit view','⌖',()=>nav.fit()],['Zoom in','+',()=>nav.zoom(1.25)],['Pan view','✥',()=>nav.pan()]]){const button=el('button',text);button.type='button';button.setAttribute('aria-label',label);button.title=label==='Pan view'?'Pan view: temporarily pause content interaction for dragging and zooming':label;if(label==='Pan view'){button.dataset.viewportPan='true';button.setAttribute('aria-pressed',String(nav.mode()));}button.onclick=action;tools.append(button);}root.append(tools);}
    function frame(node,key,resource,moveContent=false){scene=node;node.classList.add('viewport-scene');controls();stage=el('div',undefined,'viewport-stage');stage.append(node);root.append(stage);if(host)host.dataset.resource=resource;nav.attach(node,key,moveContent);sizeMap();}
    function sizeMap(){if(!stage||kind!=='block-map')return;const rect=stage.getBoundingClientRect();stage.style.setProperty('--viewport-map-size',Math.max(0,Math.min(rect.width,rect.height)*.92)+'px');}
    function disposeFlow(){for(const svg of root.querySelectorAll('.transaction-flow-svg'))svg.disposeFlow?.();}
    function clear(){disposeFlow();nav.detach();root.replaceChildren();kind='empty';selected=null;lastMap=null;lastFlow=null;scene=null;stage=null;root.dataset.kind=kind;}
    function message(text,state='idle'){if(dead)return;clear();root.dataset.state=state;const node=el('p',text,'viewport-empty');node.setAttribute('role',state==='error'?'alert':'status');root.append(node);}
    function block(map) {
      if(dead)return;
      if(!Array.isArray(map?.transactions)||map.total!==map.transactions.length||!Number.isSafeInteger(map.height))throw Error('The complete block map was not returned.');
      const positions=new Set();
      for(const [position,tx] of map.transactions.entries()){if(tx.index!==position||positions.has(tx.index)||!Number.isSafeInteger(tx.output_sats)||tx.output_sats<0)throw Error('The block map contains invalid transaction positions.');positions.add(tx.index);}
      clear();lastMap=map;kind='block-map';root.dataset.kind=kind;
      const scene=el('div',undefined,'viewport-block-map');scene.id='viewport-block-map';scene.dataset.kind=kind;scene.dataset.height=map.height;scene.dataset.hash=map.hash;scene.dataset.total=map.total;scene.dataset.ready='true';
      scene.setAttribute('aria-label','Block '+number(map.height)+', '+number(map.total)+' transactions sized by output value');
      const layout=pack(map.transactions.map(tx=>({...tx,value:tx.output_sats,vbytes:tx.vsize})),'value');
      const summary='Block '+number(map.height)+' · '+number(map.total)+' transactions · size by sats';
      const caption=el('p',summary,'viewport-caption viewport-map-caption');caption.setAttribute('role','status');caption.setAttribute('aria-atomic','true');
      let hovered=null,focused=null,captionSource='hover';
      const updateCaption=()=>{caption.textContent=(captionSource==='focus'?(focused??hovered):(hovered??focused))??summary;};
      const buttons=[];
      for(const tile of layout.tiles) {
        const tx=tile.tx,button=el('button',undefined,'viewport-map-tx'+(tx.coinbase?' coinbase':''));button.type='button';button.dataset.mapTx=tx.index;button.dataset.outputSats=tx.output_sats;button.tabIndex=buttons.length?-1:0;
        const box=tileGeometry(tile,layout);button.style.left=box.x+'%';button.style.top=box.y+'%';button.style.width=box.size+'%';button.style.height=box.size+'%';
        const label='Transaction '+tx.index+' · '+number(tx.output_sats)+' sats'+(tx.coinbase?' · coinbase':'')+(tx.inscription?' · '+number(tx.inscription_count)+' inscription'+(tx.inscription_count===1?'':'s'):'');button.setAttribute('aria-label',label);
        const ins=tx.inscription;
        if(ins) {
          button.dataset.inscription=ins.coordinate;
          const preview=ins.preview;
          if(preview?.kind==='image'&&typeof preview.data_url==='string'&&preview.data_url.length<100000&&/^data:image\/(?:png|jpeg|gif);base64,[A-Za-z0-9+/=]+$/.test(preview.data_url)) {
            const image=el('img');image.alt='First inscription in transaction '+tx.index;image.src=preview.data_url;image.loading='lazy';image.decoding='async';image.draggable=false;button.append(image);
          } else if(preview?.kind==='text')button.append(el('span',String(preview.text||'').slice(0,640),'viewport-map-text'));
          else button.append(el('span','◈','viewport-map-inscription'));
        }
        button.addEventListener('pointerenter',()=>{hovered=label;captionSource='hover';updateCaption();});
        button.addEventListener('pointerleave',()=>{if(hovered===label)hovered=null;updateCaption();});
        button.addEventListener('focus',()=>{focused=label;captionSource='focus';updateCaption();});
        button.addEventListener('blur',()=>{if(focused===label)focused=null;updateCaption();});
        button.addEventListener('click',()=>{select(tx.index);onTransaction?.(tx,map);});
        button.addEventListener('keydown',event=>{
          if(!['ArrowLeft','ArrowRight','ArrowUp','ArrowDown','Home','End'].includes(event.key))return;
          event.preventDefault();const at=buttons.indexOf(button),direction=['ArrowLeft','ArrowUp'].includes(event.key)?-1:1,index=event.key==='Home'?0:event.key==='End'?buttons.length-1:Math.max(0,Math.min(buttons.length-1,at+direction));
          buttons.forEach((item,n)=>item.tabIndex=n===index?0:-1);buttons[index]?.focus();
        });
        buttons.push(button);scene.append(button);
      }
      const surface=el('div',undefined,'viewport-block-surface');surface.append(scene);frame(surface,'block:'+map.hash,'block');root.append(caption);sizeMap();root.dataset.state='ready';
    }
    function select(index){selected=index;for(const button of root.querySelectorAll('[data-map-tx]')){const match=Number(button.dataset.mapTx)===index;button.classList.toggle('selected',match);button.setAttribute('aria-pressed',String(match));}}
    function flow(data,context={}) {
      if(dead)return;clear();kind='transaction-flow';lastFlow={data,context};root.dataset.kind=kind;root.dataset.state='ready';
      const box=el('div',undefined,'viewport-transaction-flow');box.id='viewport-transaction-flow';box.dataset.kind=kind;box.dataset.txIndex=context.index??'';box.dataset.height=context.height??'';
      function draw(){if(!window.GatewayTransactionFlow)return;const rect=stage?.getBoundingClientRect()||root.getBoundingClientRect();disposeFlow();box.replaceChildren(window.GatewayTransactionFlow.render(doc,data,{width:Math.max(280,rect.width),height:Math.max(180,rect.height),onInput:value=>onInput?.(value,context),onOutput:value=>onOutput?.(value.datum,context),onInscription:entry=>onInscription?.(entry)}));}
      if(!window.GatewayTransactionFlow){message('The transaction flow renderer is unavailable. Reload Gateway and retry.','error');return;}
      frame(box,'transaction:'+(data.txid||context.height+'.'+context.index),'transaction');
      root.append(el('p',data.note||'Unknown input values are not inferred.','viewport-caption'));
      const unresolved=(data.inscription_context?.entries||[]).filter(entry=>entry.state!=='verified_reveal_output');if(unresolved.length){const notes=el('div',undefined,'viewport-inscription-unresolved');notes.setAttribute('aria-label','Inscriptions without a verified reveal output');for(const entry of unresolved){const button=el('button','◈ '+(entry.coordinate||'Inscription '+entry.index)+' · '+String(entry.state||'unresolved').replaceAll('_',' '));button.type='button';button.onclick=()=>onInscription?.(entry);notes.append(button);}root.append(notes);}draw();
    }
    function content(node,context={}){if(dead)return;clear();kind='inscription';root.dataset.kind=kind;root.dataset.state='ready';frame(node,'inscription:'+(context.address||node.querySelector('iframe')?.src||''),'inscription',String(context.contentType||'').startsWith('image/'));}
    message('Select a block, transaction or inscription.');
    const win=doc.defaultView||window,Observer=win.ResizeObserver;const observer=Observer?new Observer(()=>{sizeMap();if(kind==='transaction-flow'&&lastFlow&&scene&&window.GatewayTransactionFlow){const rect=stage?.getBoundingClientRect()||root.getBoundingClientRect();disposeFlow();scene.replaceChildren(window.GatewayTransactionFlow.render(doc,lastFlow.data,{width:Math.max(280,rect.width),height:Math.max(180,rect.height),onInput:value=>onInput?.(value,lastFlow.context),onOutput:value=>onOutput?.(value.datum,lastFlow.context),onInscription:entry=>onInscription?.(entry)}));}}):null;observer?.observe(root);
    return {block,flow,content,message,clear,select,fit:()=>nav.fit(),state:()=>({kind,selected,total:lastMap?.total,height:lastMap?.height,view:nav.state()}),teardown(){observer?.disconnect();clear();nav.teardown();dead=true;}};
  }
  function flowSVG(data) {
    const ins=Array.isArray(data?.inputs)?data.inputs:[],outs=Array.isArray(data?.outputs)?data.outputs:[],count=Math.max(ins.length,outs.length,1),height=Math.min(1800,Math.max(230,count*45+60));
    const maximum=Math.max(1,...ins.map(input=>Number(input.value)||0),...outs.map(output=>Number(output.value_sats)||0)),row=(height-70)/count,thickness=value=>value==null?0:Math.max(value>0?2:0,Math.min(row*.75,50)*(value/maximum));
    let shapes='';
    ins.forEach((input,index)=>{const y=45+index*row;shapes+='<text x="8" y="'+(y-8)+'">vin '+index+' · '+esc(input.value==null?'unknown':number(input.value)+' sats')+'</text>';if(input.value!=null)shapes+='<path class="band" d="M 150 '+y+' C 235 '+y+', 235 '+height/2+', 320 '+height/2+'" stroke-width="'+thickness(input.value)+'"><title>'+esc(input.outpoint)+': '+esc(number(input.value))+' sats</title></path>';});
    outs.forEach((output,index)=>{const y=45+index*row;shapes+='<path class="band out-band" d="M 340 '+height/2+' C 420 '+height/2+', 420 '+y+', 500 '+y+'" stroke-width="'+thickness(output.value_sats)+'"><title>Output '+esc(output.n)+': '+esc(number(output.value_sats))+' sats</title></path><text x="505" y="'+(y-8)+'">vout '+esc(output.n)+' · '+esc(number(output.value_sats))+'</text>';});
    return '<svg viewBox="0 0 720 '+height+'" role="img" aria-label="Transaction input and output values, in original order"><rect x="318" y="'+(height/2-35)+'" width="24" height="70" rx="8" class="flow-center"/>'+shapes+'<text x="245" y="'+(height-10)+'">Fee: '+esc(data.fee==null?'not yet established':number(data.fee)+' sats')+'</text></svg>';
  }
  return {mount,pack,squareSize,tileGeometry,flowSVG};
})();
