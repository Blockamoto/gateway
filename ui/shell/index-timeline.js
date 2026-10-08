'use strict';
// A shared block-height viewport. Pixel aggregation never changes coverage;
// partial pixels remain visibly distinct from fully covered pixels.
window.GatewayIndexTimeline = (() => {
  const height = n => Number.isSafeInteger(n) && n >= 0 && n < Number.MAX_SAFE_INTEGER;
  const format = n => height(n) ? n.toLocaleString('en-GB') : 'Unknown';
  const blockCount = n => format(n) + (n === 1 ? ' block' : ' blocks');
  const count = ranges => ranges.reduce((n, r) => n + r.to - r.from + 1, 0);
  const labelRange = r => r.from === r.to ? format(r.from) : format(r.from) + '–' + format(r.to);
  function normalizeRanges(ranges) {
    const result = [];
    for (const r of (Array.isArray(ranges) ? ranges : []).filter(r => r && height(r.from) && height(r.to) && r.to >= r.from).map(r => ({from:r.from, to:r.to})).sort((a,b) => a.from-b.from || a.to-b.to)) {
      const last = result.at(-1);
      if (last && r.from <= last.to + 1) last.to = Math.max(last.to, r.to);
      else result.push(r);
    }
    return result;
  }
  function subtractRanges(ranges, gaps) {
    const cuts = normalizeRanges(gaps), out = []; let first = 0;
    for (const r of normalizeRanges(ranges)) {
      let from = r.from;
      while (first < cuts.length && cuts[first].to < from) first++;
      for (let i = first; i < cuts.length && cuts[i].from <= r.to; i++) {
        if (cuts[i].from > from) out.push({from, to:cuts[i].from-1});
        from = Math.max(from, cuts[i].to+1);
        if (from > r.to) break;
      }
      if (from <= r.to) out.push({from, to:r.to});
    }
    return out;
  }
  function clampView(view, extent) {
    extent = height(extent) ? extent : 0;
    const from = height(view?.from) ? view.from : 0, to = height(view?.to) ? Math.max(from,view.to) : Math.max(from,extent);
    const span = Math.min(extent+1, to-from+1), start = Math.max(0,Math.min(from,extent-span+1));
    return {from:start,to:start+span-1};
  }
  function coverageBins(ranges, view, width) {
    const rs = normalizeRanges(ranges), span = view.to-view.from+1;
    if (!(span > 0) || !Number.isFinite(width) || width < 1) return [];
    const bins = Math.min(4096,Math.floor(width),span), out = []; let cursor=0;
    for (let i=0;i<bins;i++) {
      const from=view.from+Math.floor(i*span/bins), to=view.from+Math.floor((i+1)*span/bins)-1;
      while (cursor<rs.length && rs[cursor].to<from) cursor++;
      let covered=0;
      for (let j=cursor;j<rs.length && rs[j].from<=to;j++) covered += Math.max(0,Math.min(to,rs[j].to)-Math.max(from,rs[j].from)+1);
      if (covered) out.push({x:(from-view.from)/span*width,width:(to-from+1)/span*width,count:covered,total:to-from+1,from,to});
    }
    return out;
  }
  function trackModel(definition, snapshot) {
    const base=window.GatewayIndexCards.model(definition,snapshot), projected=snapshot.timeline?.tracks?.find(t=>t.id===definition.id);
    const instances=(snapshot.instances||[]).filter(i=>i.definition===definition.id);
    const invalid=instances.some(i=>/stale_reorg|anchor_unavailable/.test(i.verification||''));
    // Checkpoints prove processing, not the continued presence of source bytes.
    const coverage=projected ? subtractRanges(projected.coverage,projected.gaps) : ['headers','blocks'].includes(definition.id) || invalid ? [] : subtractRanges(instances.flatMap(i=>i.coverage||[]),instances.flatMap(i=>i.gaps||[]));
    return {...base,coverage,total:count(coverage),complete:projected ? projected.coverage_complete !== false : !['headers','blocks'].includes(definition.id),note:projected?.note||'',sources:projected?.sources||[],stale:invalid,projection:projected};
  }
  function mount({document:doc,root,workspace,api,refresh,select,notify,buildRange,openEntity}) {
    const el=(tag,text,cls)=>{const n=doc.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
    const button=(text,id,title,action)=>{const n=el('button',text);n.type='button';if(id)n.id=id;if(title){n.title=title;n.setAttribute('aria-label',title);}n.addEventListener('click',action);return n;};
    const entries=new Map();let snapshot={},selected='',selection=null,healthy=false,busy=false,view={from:0,to:0},extent=0,fit=true,drawing=false;
    root.classList.add('index-timeline');
    const inspector=el('section',undefined,'timeline-inspector');inspector.id='timeline-inspector';inspector.setAttribute('aria-label','Selection inspector');
    const inspectorTop=el('div',undefined,'timeline-inspector-top'),identity=el('div'),eyebrow=el('div','INDEX WORKSPACE','eyebrow');
    const title=el('h2','Select an index');title.id='timeline-selection-title';const status=el('span','','timeline-state');
    const subtitle=el('p','Explore your local coverage. Select a track, range or block.','timeline-description');
    identity.append(eyebrow,title,subtitle);inspectorTop.append(identity,status);
    const meta=el('div',undefined,'timeline-selection-meta');meta.id='timeline-selection-meta';
    const controls=el('div',undefined,'timeline-inspector-controls'),switches=el('div',undefined,'timeline-switches'),actions=el('div',undefined,'timeline-selection-actions');
    function makeSwitch(text,control) {
      const label=el('label',undefined,'index-switch');label.append(el('span',text));const input=el('input');input.type='checkbox';input.setAttribute('role','switch');const track=el('span',undefined,'switch-track');track.setAttribute('aria-hidden','true');label.append(input,track);input.addEventListener('change',()=>change(control,input.checked));switches.append(label);return {label,input};
    }
    const on=makeSwitch('Indexing','on'),live=makeSwitch('Follow new blocks','live');
    const focus=button('Focus selection','timeline-focus','Zoom to selected coverage',()=>focusSelection());
    const inspect=button('Open in Explorer','timeline-open-entity',null,()=>{if(selection&&selection.kind==='block'&&healthy&&!current()?.locked)openEntity?.(selected,selection.from);});
    const build=button('Build this range','timeline-build-range',null,()=>{if(selection&&current()?.definition.buildable&&!current()?.locked&&healthy&&!busy){advanced.open=true;buildRange?.(selected,selection.from,selection.to);}});
    actions.append(focus,inspect,build);controls.append(switches,actions);
    const notice=el('p','','timeline-note');notice.id='timeline-selection-note';
    const advanced=el('details',undefined,'timeline-details');advanced.id='timeline-details';const summary=el('summary','Settings, jobs & records');advanced.append(summary);if(workspace)advanced.append(workspace);
    inspector.append(inspectorTop,meta,controls,notice,advanced);
    const editor=el('section',undefined,'timeline-editor');editor.setAttribute('aria-label','Index timeline');
    const toolbar=el('div',undefined,'timeline-toolbar'),toolbarTitle=el('div',undefined,'timeline-toolbar-title');toolbarTitle.append(el('strong','Chain timeline'),el('span','BLOCK HEIGHT','timeline-unit'));
    const navigation=el('div',undefined,'timeline-navigation');
    const left=button('←','timeline-pan-left','Pan to earlier blocks',()=>pan(-.6)),right=button('→','timeline-pan-right','Pan to later blocks',()=>pan(.6));
    const zoomOut=button('−','timeline-zoom-out','Zoom out',()=>zoom(2)),zoomIn=button('+','timeline-zoom-in','Zoom in',()=>zoom(.25)),fitButton=button('Fit chain','timeline-fit',null,()=>{fit=true;redraw();});
    navigation.append(left,right,zoomOut,zoomIn,fitButton);
    const jump=el('form',undefined,'timeline-jump');jump.id='timeline-jump';const jumpLabel=el('label','Go to block');jumpLabel.htmlFor='timeline-height';const jumpInput=el('input');jumpInput.id='timeline-height';jumpInput.type='text';jumpInput.inputMode='numeric';jumpInput.pattern='[0-9]+';jumpInput.placeholder='Block height';jumpInput.autocomplete='off';const go=el('button','Go');go.type='submit';jump.append(jumpLabel,jumpInput,go);
    jump.addEventListener('submit',e=>{e.preventDefault();const value=Number(jumpInput.value);if(!/^\d+$/.test(jumpInput.value)||!height(value)||value>extent){jumpInput.setCustomValidity('Enter a block height from 0 to '+format(extent)+'.');jumpInput.reportValidity();return;}jumpInput.setCustomValidity('');if(!selected){const d=ordered()[0];if(d)select(d.id,'providers');}fit=false;view=clampView({from:Math.max(0,value-16),to:Math.max(0,value-16)+32},extent);choose({id:selected,kind:'block',from:value,to:value});});
    jumpInput.addEventListener('input',()=>jumpInput.setCustomValidity(''));
    toolbar.append(toolbarTitle,navigation,jump);
    const rulerRow=el('div',undefined,'timeline-ruler-row'),rulerLabel=el('div','INDEX / LOCAL COVERAGE','timeline-ruler-label'),ruler=el('div',undefined,'timeline-ruler');ruler.id='timeline-ruler';rulerRow.append(rulerLabel,ruler);
    const tracks=el('div',undefined,'timeline-tracks'),empty=el('p','Reading local coverage…','timeline-empty');
    const footer=el('div',undefined,'timeline-footer'),legend=el('div',undefined,'timeline-legend');
    for(const [cls,text] of [['saved','Available locally'],['partial','Mixed coverage · zoom in'],['requested','Requested work']]){const item=el('span',undefined,'timeline-legend-item'),swatch=el('i',undefined,'timeline-swatch '+cls);swatch.setAttribute('aria-hidden','true');item.append(swatch,el('span',text));legend.append(item);}
    const viewportLabel=el('span','','timeline-viewport-label');viewportLabel.id='timeline-viewport-label';footer.append(legend,viewportLabel);
    const instructions=el('p','Select a track for settings. Select a range to focus it; zoom in to inspect individual blocks. Use + / − to zoom and ← / → to pan when a track is focused.','timeline-instructions');instructions.id='timeline-instructions';
    const coverageDetails=el('details',undefined,'timeline-exact'),coverageSummary=el('summary','Exact coverage'),coverageList=el('div',undefined,'timeline-exact-list');coverageDetails.append(coverageSummary,coverageList);
    const tooltip=el('div',undefined,'timeline-tooltip');tooltip.hidden=true;tooltip.setAttribute('role','status');
    editor.append(toolbar,rulerRow,tracks,empty,footer,instructions,coverageDetails,tooltip);root.append(inspector,editor);
    function ordered(){return [...(snapshot.definitions||[])].sort((a,b)=>(a.id==='headers'?-1:b.id==='headers'?1:Number(!!a.locked)-Number(!!b.locked)));}
    function current(){const d=(snapshot.definitions||[]).find(d=>d.id===selected);return d?trackModel(d,snapshot):null;}
    function dimensions(){const values=[snapshot.timeline?.tip_height,snapshot.timeline?.target_height];for(const d of snapshot.definitions||[])for(const r of trackModel(d,snapshot).coverage)values.push(r.to);for(const job of snapshot.jobs||[])if(!(snapshot.definitions||[]).find(d=>d.id===job.index)?.locked)values.push(job.to);extent=Math.max(0,...values.filter(height));view=fit?{from:0,to:extent}:clampView(view,extent);root.dataset.from=String(view.from);root.dataset.to=String(view.to);root.dataset.fit=String(fit);}
    function makeTrack(d) {
      const row=el('div',undefined,'timeline-track');row.dataset.index=d.id;
      const head=button('',null,null,()=>select(d.id,d.buildable?'build':'providers'));head.className='timeline-track-select';
      const number=el('span','','timeline-track-number'),name=el('strong',d.name),state=el('span','','timeline-track-state'),detail=el('span','','timeline-track-count');head.append(number,name,state,detail);
      const trackBody=el('div',undefined,'timeline-track-body'),canvas=el('canvas',undefined,'timeline-lane');canvas.dataset.index=d.id;canvas.tabIndex=0;canvas.setAttribute('role','button');canvas.setAttribute('aria-describedby','timeline-instructions');
      const placeholder=el('span','','timeline-track-placeholder');trackBody.append(canvas,placeholder);row.append(head,trackBody);tracks.append(row);
      const e={row,head,number,name,state,detail,canvas,placeholder,d};entries.set(d.id,e);
      canvas.addEventListener('click',event=>hit(e,event));
      canvas.addEventListener('dblclick',()=>{if(selection?.id===d.id)focusSelection();});
      canvas.addEventListener('keydown',event=>{
        if(event.key==='+'||event.key==='='){event.preventDefault();zoom(.25);}else if(event.key==='-'){event.preventDefault();zoom(2);}else if(event.key==='Home'){event.preventDefault();fit=true;redraw();}else if(event.key==='ArrowLeft'||event.key==='ArrowRight'){event.preventDefault();if(view.to-view.from<64&&selection?.kind==='block'&&selection.id===d.id){const n=Math.max(0,Math.min(extent,selection.from+(event.key==='ArrowLeft'?-1:1)));choose({id:d.id,kind:'block',from:n,to:n});if(n<view.from||n>view.to){view=clampView({from:Math.max(0,n-16),to:Math.max(0,n-16)+32},extent);fit=false;redraw();}}else pan(event.key==='ArrowLeft'?-.6:.6);}else if(event.key==='Enter'||event.key===' '){event.preventDefault();if(view.to-view.from<64){const n=Math.floor((view.from+view.to)/2);choose({id:d.id,kind:'block',from:n,to:n});}else select(d.id,d.buildable?'build':'providers');}
      });
      canvas.addEventListener('wheel',event=>{if(event.ctrlKey||event.metaKey){event.preventDefault();const rect=canvas.getBoundingClientRect();zoom(event.deltaY>0?2:.5,Math.max(0,Math.min(1,(event.clientX-rect.left)/rect.width)));}else if(event.shiftKey){event.preventDefault();pan((event.deltaY||event.deltaX)>0?.25:-.25);}},{passive:false});
      canvas.addEventListener('pointermove',event=>showTooltip(e,event));canvas.addEventListener('pointerleave',()=>{tooltip.hidden=true;});canvas.addEventListener('blur',()=>{tooltip.hidden=true;});
      return e;
    }
    function projectHit(e,event) {
      const rect=e.canvas.getBoundingClientRect(),width=Math.max(1,rect.width),span=view.to-view.from+1,px=Math.max(0,Math.min(width-.001,event.clientX-rect.left));
      const h=Math.min(view.to,view.from+Math.floor(px/width*span)),m=trackModel(e.d,snapshot);
      if(span<=64)return {id:e.d.id,kind:'block',from:h,to:h};
      const binSize=Math.max(1,Math.ceil(span/width)),start=Math.max(view.from,h-binSize+1),end=Math.min(view.to,h+binSize-1);
      const nearby=m.coverage.filter(r=>r.from<=end&&r.to>=start);
      if(nearby.length===1&&nearby[0].from<=h&&nearby[0].to>=h)return {id:e.d.id,kind:'range',...nearby[0]};
      if(nearby.length)return {id:e.d.id,kind:'range',from:start,to:end};
      const gap=subtractRanges([{from:0,to:extent}],m.coverage).find(r=>r.from<=h&&r.to>=h);
      return {id:e.d.id,kind:'range',...(gap||{from:h,to:h})};
    }
    function hit(e,event){if(trackModel(e.d,snapshot).locked){select(e.d.id,'catalog');return;}choose(projectHit(e,event));}
    function choose(value){if(!value.id)return;if(selected!==value.id)select(value.id,(snapshot.definitions||[]).find(d=>d.id===value.id)?.buildable?'build':'providers');selected=value.id;selection=value;renderInspector();draw();}
    function showTooltip(e,event){const s=projectHit(e,event),m=trackModel(e.d,snapshot),present=count(intersect(m.coverage,s));tooltip.textContent=m.locked?'Locked · '+(e.d.lock_reason||'Available in a later release'):s.kind==='block'?'Block '+format(s.from)+' · '+(present?'available locally':m.complete?'not stored locally':'not established locally'):'Blocks '+labelRange(s)+' · '+format(present)+' available';const rect=editor.getBoundingClientRect();tooltip.style.left=Math.max(8,Math.min(rect.width-280,event.clientX-rect.left+12))+'px';tooltip.style.top=Math.max(8,event.clientY-rect.top-42)+'px';tooltip.hidden=false;}
    const intersect=(ranges,r)=>ranges.filter(x=>x.from<=r.to&&x.to>=r.from).map(x=>({from:Math.max(r.from,x.from),to:Math.min(r.to,x.to)}));
    function zoom(factor,anchor=.5){dimensions();const span=view.to-view.from+1,target=Math.max(1,Math.min(extent+1,Math.round(span*factor))),center=view.from+span*anchor;view=clampView({from:Math.max(0,Math.round(center-target*anchor)),to:Math.max(0,Math.round(center-target*anchor))+target-1},extent);fit=false;redraw();}
    function pan(fraction){const span=view.to-view.from+1,from=Math.max(0,view.from+Math.round(span*fraction));view=clampView({from,to:from+span-1},extent);fit=false;redraw();}
    function focusSelection(){const m=current();if(!m)return;const r=selection?.kind!=='track'?selection:m.coverage.length?{from:m.coverage[0].from,to:m.coverage.at(-1).to}:null;if(!r)return;const padding=r.to===r.from?4:Math.max(1,Math.round((r.to-r.from+1)*.04));view=clampView({from:Math.max(0,r.from-padding),to:Math.min(extent,r.to+padding)},extent);fit=false;redraw();}
    function setMeta(values){meta.replaceChildren();for(const [label,value] of values){const cell=el('div',undefined,'timeline-stat');cell.append(el('span',label),el('strong',value));meta.append(cell);}}
    function renderInspector() {
      const m=current();if(!m){title.textContent='Select an index';advanced.hidden=true;controls.hidden=true;return;}
      const s=selection||{id:selected,kind:'track'},d=m.definition,isTrack=s.kind==='track';
      inspector.dataset.index=selected;inspector.dataset.kind=s.kind;eyebrow.textContent=isTrack?'TRACK INSPECTOR':s.kind==='block'?'BLOCK INSPECTOR':'RANGE INSPECTOR';
      title.textContent=isTrack?d.name:s.kind==='block'?'Block '+format(s.from):'Blocks '+labelRange(s);
      status.textContent=!healthy?'Status unavailable':m.locked?'Locked':d.id==='headers'?(snapshot.timeline?.syncing?'Syncing headers':m.total?'Headers available':'Waiting for headers'):m.state;
      status.dataset.locked=String(m.locked);subtitle.textContent=isTrack?d.theory||'Local index coverage':d.name+' · '+(m.locked?'Locked':s.kind==='block'?'Inspect this position in the chain.':'An exact block-height selection.');
      const selectedRanges=isTrack?m.coverage:intersect(m.coverage,s),available=count(selectedRanges),length=isTrack?extent+1:s.to-s.from+1;
      setMeta(isTrack?[['Available locally',blockCount(m.total)+(m.complete?'':' · mapped so far')],['Separate ranges',format(m.coverage.length)],['Local header tip',format(snapshot.timeline?.tip_height)]]:[['Selection',blockCount(length)],['Available locally',format(available)+' / '+format(length)],['Coverage',available===length?'Complete':available?'Partial':m.complete?'Not stored':'Not established']]);
      controls.hidden=false;switches.hidden=!isTrack||!d.buildable||m.locked;
      on.input.checked=m.on;live.input.checked=m.live;on.input.disabled=live.input.disabled=!healthy||busy||m.locked||!d.buildable;
      on.input.setAttribute('aria-label',d.id+' On');live.input.setAttribute('aria-label',d.id+' Live');
      on.label.title='Allow indexing work. Turning off keeps saved records.';live.label.title='Catch up from saved progress and follow new blocks. This may fetch many blocks.';
      focus.disabled=m.locked||isTrack&&!m.coverage.length;focus.hidden=m.locked;inspect.hidden=s.kind!=='block'||m.locked;inspect.disabled=!healthy;
      build.hidden=isTrack||!d.buildable||m.locked;build.disabled=!healthy||busy;
      const blockDefinition=(snapshot.definitions||[]).find(d=>d.id==='blocks');
      const bodyAvailable=s.kind==='block'&&blockDefinition&&trackModel(blockDefinition,snapshot).coverage.some(r=>r.from<=s.from&&r.to>=s.from);
      if(s.kind==='block'&&!bodyAvailable)inspect.textContent='Fetch & open in Explorer';else inspect.textContent='Open in Explorer';
      advanced.hidden=m.locked;summary.textContent=d.buildable?'Settings, jobs & records':'Header data sources & details';if(workspace)workspace.hidden=m.locked;
      notice.textContent=m.locked?d.lock_reason||'This index is locked while Headers and Blocks are tested.':!healthy?'Showing the last known coverage. Controls are paused until local status is available.':d.id==='headers'?'Headers link the chain together. They do not contain the transactions inside each block.':d.id==='blocks'?'Filled ranges show block files available on this computer. Processing a block does not always keep its source file. Files are checked again when opened.':m.note||'Filled ranges show committed local results. They do not imply that source block files are retained.';
      if(!m.locked&&!m.complete)notice.textContent+=' Coverage is partial: blank areas may contain data that has not been mapped yet.';
      if(isTrack&&d.buildable&&!m.locked)notice.textContent+=m.retained?' Following new blocks continues from saved progress.':' Follow new blocks catches up all applicable history from block '+format(m.initialFrom)+'. It may download many source blocks.';
      if(m.stale)notice.textContent+=' Chain verification needs attention; inspect the source details before relying on saved results.';
      if(s.kind==='block'&&!bodyAvailable&&!m.locked)notice.textContent+=' Opening this block in Explorer may fetch it from your configured sources.';
      const errors=snapshot.timeline?.errors||[];if(errors.length)notice.textContent+=' '+errors.join(' ');
      const signature=JSON.stringify([selected,m.coverage,view]);if(coverageList.dataset.key!==signature){coverageList.dataset.key=signature;coverageList.replaceChildren();coverageSummary.textContent='Exact coverage · '+format(m.coverage.length)+' range'+(m.coverage.length===1?'':'s');const rs=intersect(m.coverage,view);for(const r of rs.slice(0,100)){const b=button(labelRange(r),null,'Select blocks '+labelRange(r),()=>choose({id:selected,kind:r.from===r.to?'block':'range',...r}));b.disabled=m.locked;coverageList.append(b);}if(!rs.length)coverageList.append(el('p',m.complete?'No local coverage in this view.':'No established coverage in this view.'));if(rs.length>100)coverageList.append(el('p','Showing the first 100 visible ranges. Zoom in to inspect the rest.'));}
    }
    function draw() {
      dimensions();ruler.replaceChildren();const span=view.to-view.from+1;
      const rulerWidth=ruler.clientWidth||800,steps=Math.max(2,Math.min(6,Math.floor(rulerWidth/130)));
      const ticks=new Set([view.from,view.to]);for(let i=1;i<steps;i++)ticks.add(view.from+Math.floor(span*i/steps));
      for(const h of [...ticks].sort((a,b)=>a-b)){const tick=el('span',format(h));tick.style.left=((h-view.from+.5)/span*100)+'%';if(span>64&&h===view.from)tick.className='first';if(span>64&&h===view.to)tick.className='last';ruler.append(tick);}
      const tip=snapshot.timeline?.tip_height;viewportLabel.textContent=format(view.from)+'–'+format(view.to)+' · '+blockCount(span)+(fit?' · Fit chain':' · Focused view');
      fitButton.setAttribute('aria-pressed',String(fit));zoomIn.disabled=span<=1;zoomOut.disabled=span>=extent+1;left.disabled=view.from===0;right.disabled=view.to===extent;
      root.dataset.from=String(view.from);root.dataset.to=String(view.to);root.dataset.fit=String(fit);
      for(const e of entries.values()) {
        const m=trackModel(e.d,snapshot),canvas=e.canvas,rect=canvas.getBoundingClientRect(),w=Math.max(1,rect.width),h=80,dpr=Math.min(2,doc.defaultView?.devicePixelRatio||1);
        canvas.width=Math.round(w*dpr);canvas.height=h*dpr;canvas.dataset.from=String(view.from);canvas.dataset.to=String(view.to);canvas.dataset.coverage=JSON.stringify(m.coverage);canvas.setAttribute('aria-label',e.d.name+' timeline, blocks '+labelRange(view)+', '+format(m.total)+' locally available'+(m.locked?', locked':''));
        const ctx=canvas.getContext('2d');if(!ctx)continue;ctx.scale(dpr,dpr);
        const dark=doc.documentElement.dataset.theme==='dark',colors={grid:dark?'#284139':'#dfdfd5',fill:e.d.id==='headers'?(dark?'#96c9ad':'#517965'):(dark?'#91bdcf':'#548496'),partial:dark?'#dcb97c':'#86682d',ink:dark?'#e8f0e8':'#233e30',selection:dark?'#d9edb8':'#335c41',job:dark?'#c8ac74':'#9a7435'};
        ctx.fillStyle=colors.grid;for(const tick of ticks){const x=(tick-view.from+.5)/span*w;ctx.fillRect(Math.floor(x),0,1,h);}
        if(!healthy)ctx.globalAlpha=.45;
        for(const bin of coverageBins(m.coverage,view,w)) {
          if(m.locked)break;
          const full=bin.count===bin.total;ctx.fillStyle=full?colors.fill:colors.partial;
          ctx.fillRect(bin.x,25,Math.max(.8,bin.width),30);
          if(!full){ctx.fillStyle=dark?'#182d26':'#fffdf8';ctx.fillRect(bin.x,31,Math.max(.8,bin.width),4);ctx.fillRect(bin.x,45,Math.max(.8,bin.width),4);}
        }
        const job=m.job;if(!m.locked&&job&&['running','waiting','queued','pausing','paused','catching_up'].includes(job.state)&&height(job.from)&&height(job.to)&&job.from<=view.to&&job.to>=view.from){const x=(Math.max(view.from,job.from)-view.from)/span*w,end=(Math.min(view.to,job.to)+1-view.from)/span*w;ctx.strokeStyle=colors.job;ctx.setLineDash([4,4]);ctx.strokeRect(x+.5,17,Math.max(1,end-x-1),46);ctx.setLineDash([]);}
        if(span<=64&&!m.locked){for(let i=0;i<span;i++){const x=i/span*w,bw=w/span;ctx.fillStyle=colors.grid;ctx.fillRect(x,23,1,34);if(bw>42){const block=view.from+i;ctx.fillStyle=m.coverage.some(r=>r.from<=block&&r.to>=block)?(dark?'#182d26':'#ffffff'):colors.ink;ctx.font='10px ui-monospace, monospace';ctx.textAlign='center';ctx.fillText(String(block),x+bw/2,43,Math.max(1,bw-6));}}}
        ctx.globalAlpha=1;
        if(height(tip)&&tip>=view.from&&tip<=view.to){const x=(tip+1-view.from)/span*w;ctx.strokeStyle=colors.fill;ctx.setLineDash([2,3]);ctx.beginPath();ctx.moveTo(Math.min(w-1,x),4);ctx.lineTo(Math.min(w-1,x),h-4);ctx.stroke();ctx.setLineDash([]);}
        if(selection&&selection.kind!=='track'&&selection.from<=view.to&&selection.to>=view.from){const x=(Math.max(view.from,selection.from)-view.from)/span*w,end=(Math.min(view.to,selection.to)+1-view.from)/span*w;ctx.strokeStyle=colors.selection;ctx.lineWidth=e.d.id===selected?2:1;ctx.globalAlpha=e.d.id===selected?1:.3;ctx.strokeRect(x+1,e.d.id===selected?12:4,Math.max(1,end-x-2),e.d.id===selected?56:72);ctx.globalAlpha=1;}
      }
      renderInspector();
    }
    function redraw(){tooltip.hidden=true;draw();}
    function render(value=snapshot) {
      snapshot=value;dimensions();const defs=ordered(),ids=new Set(defs.map(d=>d.id));
      for(const [id,e] of entries)if(!ids.has(id)){e.row.remove();entries.delete(id);}
      for(const [i,d] of defs.entries()){const e=entries.get(d.id)||makeTrack(d),m=trackModel(d,snapshot);e.d=d;e.number.textContent=String(i+1).padStart(2,'0');e.name.textContent=d.name;e.head.setAttribute('aria-pressed',String(d.id===selected));e.head.setAttribute('aria-label','Select '+d.name+(m.locked?' (locked)':''));e.row.dataset.selected=String(d.id===selected);e.row.dataset.locked=String(m.locked);e.state.textContent=!healthy?'Status unavailable':m.locked?'Locked':d.id==='headers'?(snapshot.timeline?.syncing?'Syncing':m.total?'Foundation':'Waiting'):m.state;e.detail.textContent=m.locked?'Available in a later release':blockCount(m.total)+(m.complete?'':' mapped')+' · '+format(m.coverage.length)+' range'+(m.coverage.length===1?'':'s');e.placeholder.textContent=m.locked?(d.lock_reason||'Locked · Available in a later release'):!m.coverage.length?(m.complete?'No local coverage yet':'Local coverage not established'):'';e.placeholder.hidden=!!m.coverage.length&&!m.locked;e.head.title=m.locked?d.lock_reason||'Locked':d.theory||d.name;if(tracks.children[i]!==e.row)tracks.insertBefore(e.row,tracks.children[i]||null);}
      empty.hidden=defs.length>0;root.dataset.healthy=String(healthy);draw();
    }
    async function change(control,checked) {
      const m=current();if(!m||!healthy||busy||m.locked){renderInspector();return;}
      const id=m.definition.id;busy=true;renderInspector();try{const action=control==='on'?(checked?'start':'stop'):(checked?'enable':'disable'),result=await api('live',{index:id,action});snapshot={...snapshot,live:[...(snapshot.live||[]).filter(p=>p.index!==id),result]};notify(control==='live'?(checked?'Following new blocks. Gateway will catch up from saved progress.':'Following new blocks is off. Saved data remains available.'):(checked?'Indexing is on. Choose a range, or follow new blocks.':'Indexing stopped. Saved data remains available.'));await refresh();}catch(error){notify(error.message,true);}finally{busy=false;render();}
    }
    const Resize=doc.defaultView?.ResizeObserver;if(Resize)new Resize(()=>{if(!drawing){drawing=true;doc.defaultView.requestAnimationFrame(()=>{drawing=false;draw();});}}).observe(ruler);
    const Mutation=doc.defaultView?.MutationObserver;if(Mutation)new Mutation(()=>draw()).observe(doc.documentElement,{attributes:true,attributeFilter:['data-theme']});
    return {render,health:ok=>{healthy=ok;render();},select:id=>{const changed=selected!==id;selected=id;if(changed||!selection)selection=id?{id,kind:'track'}:null;else selection={id,kind:'track'};if(changed)advanced.open=false;render();},model:id=>trackModel((snapshot.definitions||[]).find(d=>d.id===id)||{id},snapshot),getView:()=>({...view,fit})};
  }
  return {normalizeRanges,subtractRanges,clampView,coverageBins,trackModel,mount};
})();
