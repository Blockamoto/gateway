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
  // Indexing selections are inclusive interval sets, independent of the playhead.
  // Toggling a block may split a range; the resulting hole must survive a build.
  function toggleBlock(ranges, block) {
    const normalized=normalizeRanges(ranges);
    if(!height(block))return normalized;
    return normalized.some(r=>r.from<=block&&r.to>=block)?subtractRanges(normalized,[{from:block,to:block}]):normalizeRanges([...normalized,{from:block,to:block}]);
  }
  function selectBlock(ranges, anchor, block, {extend=false,toggle=false}={}) {
    if(!height(block))return {ranges:normalizeRanges(ranges),anchor:height(anchor)?anchor:null};
    if(extend&&height(anchor))return {ranges:toggle?normalizeRanges([...normalizeRanges(ranges),{from:Math.min(anchor,block),to:Math.max(anchor,block)}]):[{from:Math.min(anchor,block),to:Math.max(anchor,block)}],anchor};
    return {ranges:toggle?toggleBlock(ranges,block):[{from:block,to:block}],anchor:block};
  }
  function selectionMarkers(ranges) {
    return normalizeRanges(ranges).flatMap((r,range)=>r.from===r.to?[{range,edge:'single',height:r.from}]:[{range,edge:'in',height:r.from},{range,edge:'out',height:r.to}]);
  }
  function nearestMarker(ranges, reference) {
    const markers=selectionMarkers(ranges);if(!markers.length||!height(reference?.height))return null;
    return markers.find(m=>m.height===reference.height&&m.edge===reference.edge)||markers.find(m=>m.height===reference.height)||markers.filter(m=>m.edge===reference.edge).sort((a,b)=>Math.abs(a.height-reference.height)-Math.abs(b.height-reference.height))[0]||markers.sort((a,b)=>Math.abs(a.height-reference.height)-Math.abs(b.height-reference.height))[0];
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
    const base=window.GatewayIndexCards.model(definition.owner?{...definition,id:definition.owner}:definition,snapshot), projected=snapshot.timeline?.tracks?.find(t=>t.id===definition.id);
    const instances=(snapshot.instances||[]).filter(i=>i.definition===(definition.owner||definition.id));
    const invalid=instances.some(i=>/stale_reorg|anchor_unavailable/.test(i.verification||''));
    // Checkpoints prove processing, not the continued presence of source bytes.
    const coverage=projected ? subtractRanges(projected.coverage,projected.gaps) : ['headers','blocks','txio-links','txio-values','txio-scripts'].includes(definition.id) || invalid ? [] : subtractRanges(instances.flatMap(i=>i.coverage||[]),instances.flatMap(i=>i.gaps||[]));
    // A saved live origin is a build choice, not the catalog's potential span.
    const suggestedFrom=['inscriptions','headers','blocks','tx-locator'].includes(definition.id)?window.GatewayIndexCards.model(definition,{}).initialFrom:null;
    const potentialFrom=height(projected?.potential_from)?projected.potential_from:height(suggestedFrom)?suggestedFrom:null;
    const state=definition.id==='utxo-state'&&window.GatewayUTXOPanel?window.GatewayUTXOPanel.model(snapshot).label:base.state;
    return {...base,state,definition,coverage,total:count(coverage),complete:projected ? projected.coverage_complete !== false : !['headers','blocks','txio-links','txio-values','txio-scripts'].includes(definition.id),potentialFrom,note:projected?.note||'',sources:projected?.sources||[],stale:invalid,projection:projected};
  }
  const layers=[['txio-links','Positional links'],['txio-values','Values'],['txio-scripts','ScriptPubKeys']];
  const trackColors={headers:['#517965','#96c9ad'],blocks:['#548496','#91bdcf'],'txio-links':['#6b5ba6','#b7a5ec'],'txio-values':['#9a6728','#e2b875'],'txio-scripts':['#247c78','#7acdc7'],'utxo-state':['#3e719f','#8dbce8']};
  const trackColor=(id,dark)=> (trackColors[id]||trackColors.blocks)[dark?1:0];
  function childDefinitions(snapshot){const owner=(snapshot.definitions||[]).find(d=>d.id==='txo-spender');return owner?layers.map(([id,name])=>({...owner,id,name,owner:owner.id,theory:name+' retained by TXIO. This layer reports its own verified coverage.'})):[];}
  function established(definition,snapshot) {
    const id=definition.id;
    if(id==='headers')return true;
    if(definition.locked)return false;
    return trackModel(definition,snapshot).total>0 || (snapshot.instances||[]).some(i=>i.definition===id&&(i.checkpoint||i.coverage?.length)) || [...(snapshot.jobs||[]),snapshot.job].some(j=>j?.id&&(j.index===id||j.outputs?.includes(id))) || (snapshot.live||[]).some(p=>p.index===id&&(p.enabled||p.updated||p.on||p.stopped||p.paused));
  }
  function rulerMarks(view,width) {
    const span=view.to-view.from+1,target=Math.max(1,span/Math.max(2,width/115)),power=10**Math.floor(Math.log10(target));
    const step=([1,2,5,10].find(n=>n*power>=target)||10)*power,marks=[];
    for(let h=Math.ceil(view.from/step)*step;h<=view.to&&marks.length<100;h+=step)marks.push({height:h,kind:'regular',label:format(h)});
    for(const [period,kind,prefix,minPixels] of [[210000,'halving','Halving',95],[2016,'difficulty','Difficulty',105]]) {
      if(period/span*width<minPixels)continue;
      for(let h=Math.max(period,Math.ceil(view.from/period)*period);h<=view.to&&marks.length<240;h+=period){if(kind==='difficulty'&&marks.some(m=>m.kind==='halving'&&Math.abs(m.height-h)/span*width<100))continue;marks.push({height:h,kind,label:prefix+' '+format(h/period),detail:prefix+' boundary · block '+format(h)});}
    }
    return marks.sort((a,b)=>a.height-b.height);
  }
  function mount({document:doc,root,workspace,api,refresh,select:selectIndex,notify,buildRange,openEntity,onFocus,onDetail,onEntity,loadHierarchy,loadRevealMarkers,onInscription}) {
    const el=(tag,text,cls)=>{const n=doc.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
    const button=(text,id,title,action)=>{const n=el('button',text);n.type='button';if(id)n.id=id;if(title){n.title=title;n.setAttribute('aria-label',title);}n.addEventListener('click',action);return n;};
    const entries=new Map(),indexSelections=new Map();let snapshot={},selected='',selection=null,playhead=null,healthy=false,busy=false,view={from:0,to:0},extent=0,fit=true,drawing=false,selectionChanging=false,pendingMarkerFocus=null;
    const storageKey='gateway.timeline.workspace.v1';let preferences={};try{preferences=JSON.parse(doc.defaultView.localStorage.getItem(storageKey)||'{}')||{};}catch{}
    const added=new Set(Array.isArray(preferences.tracks)?preferences.tracks.filter(id=>typeof id==='string'&&id.length<100):[]);
    const hidden=new Set(Array.isArray(preferences.hiddenTracks)?preferences.hiddenTracks.filter(id=>typeof id==='string'&&id!=='headers'&&id.length<100):[]);
    const groups=new Map(),expandedGroups={blocks:preferences.groups?.blocks!==false,txio:preferences.groups?.txio===true};
    const groupOf=id=>['headers','blocks'].includes(id)?'blocks':layers.some(([layer])=>layer===id)?'txio':null;
    const definitionFor=id=>childDefinitions(snapshot).find(d=>d.id===id)||(snapshot.definitions||[]).find(d=>d.id===id);
    function select(id,nextView){const d=definitionFor(id);selectIndex(d?.owner||id,nextView);if(d?.owner){selected=id;selection={id,kind:'track'};render();}}
    let trackHeight=Math.max(56,Math.min(220,Number(preferences.trackHeight)||80)),spaceHeld=false,spaceDragged=false,drag=null,suppressClick=false,hovering=false;
    const pointers=new Map();let pinch=null;
    let hierarchy=[],hierarchySerial=0,hierarchyRequest=null,hierarchyBusy=false,entityTimer=null,hierarchyError='',focusedResource=null,revealRequest=null,revealSerial=0,revealSnapshotKey='',revealChooser=null,revealChooserAnchor=null,observedTip='',observedStale='',tipHeight=null,validationRequest=null;const resourceAnchors=new Map();const revealCache=new Map();
    const hierarchyCache=new Map();
    const frame=()=>hierarchy.at(-1),navView=()=>frame()?.view||view,navExtent=()=>frame()?Math.max(0,(frame().count??1)-1):extent;
    function setNavView(value){if(hierarchyBusy)cancelHierarchy();if(frame())frame().view=clampView(value,navExtent());else{view=clampView(value,extent);fit=false;}}
    function save(){try{doc.defaultView.localStorage.setItem(storageKey,JSON.stringify({tracks:[...added],hiddenTracks:[...hidden],trackHeight,groups:expandedGroups}));}catch{}}
    root.style.setProperty('--timeline-track-height',trackHeight+'px');
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
    const on=makeSwitch('Indexing','on'),live=makeSwitch('Follow new blocks','live'),browse=makeSwitch('Index as you browse','browse');browse.input.id='timeline-browse-index';browse.label.title='Save this index’s verified results when you browse a block. This does not start a historical scan.';
    const focus=button('Focus selection','timeline-focus','Zoom to selected coverage',()=>focusSelection());
    const inspect=button('Open in Explorer','timeline-open-entity',null,()=>{if(selection&&selection.kind==='block'&&healthy&&!current()?.locked)openEntity?.(selected,selection.from);});
    const build=button('Build this range','timeline-build-range',null,()=>{const ranges=selectedRanges();if(ranges.length===1&&current()?.definition.buildable&&!current()?.locked&&healthy&&!busy)prepareRange(ranges[0]);});
    actions.append(focus,inspect,build);controls.append(switches,actions);
    const notice=el('p','','timeline-note');notice.id='timeline-selection-note';
    const selectionList=el('div',undefined,'timeline-selected-ranges');selectionList.id='timeline-selected-ranges';
    const advanced=el('details',undefined,'timeline-details');advanced.id='timeline-details';const summary=el('summary','Settings, jobs & records');advanced.append(summary);if(workspace)advanced.append(workspace);
    inspector.append(inspectorTop,meta,controls,selectionList,notice,advanced);
    const editor=el('section',undefined,'timeline-editor');editor.setAttribute('aria-label','Index timeline');
    const navigation=el('div',undefined,'timeline-navigation');
    const left=button('←','timeline-pan-left','Pan to earlier blocks',()=>pan(-.6)),right=button('→','timeline-pan-right','Pan to later blocks',()=>pan(.6));
    const fitButton=button('⤢','timeline-fit','Fit the whole chain',()=>{returnParent(-1);fit=true;redraw();});
    navigation.append(left,right,fitButton);
    // The shell address is the only resource address input. Timeline controls
    // live beside the ruler rather than in a separate lower controls panel.
    const rulerRow=el('div',undefined,'timeline-ruler-row'),rulerLabel=el('div',undefined,'timeline-ruler-label'),ruler=el('div',undefined,'timeline-ruler');ruler.id='timeline-ruler';
    ruler.tabIndex=0;ruler.setAttribute('role','slider');ruler.setAttribute('aria-label','Timeline playhead');ruler.setAttribute('aria-orientation','horizontal');ruler.addEventListener('keydown',positionKey);
    const addButton=button('+','timeline-add-index','Add an index track',()=>{picker.hidden=!picker.hidden;addButton.setAttribute('aria-expanded',String(!picker.hidden));if(!picker.hidden){positionPicker();picker.querySelector('button:not(:disabled)')?.focus();}});
    addButton.className='timeline-add-circle';
    addButton.setAttribute('aria-expanded','false');addButton.setAttribute('aria-controls','timeline-index-picker');
    const picker=el('div',undefined,'timeline-index-picker');picker.id='timeline-index-picker';picker.hidden=true;picker.setAttribute('role','group');picker.setAttribute('aria-label','Available index tracks');
    const refreshButton=button('↻','timeline-refresh','Refresh index status',()=>refresh?.());refreshButton.className='timeline-refresh-icon';rulerLabel.append(refreshButton,navigation);rulerRow.append(rulerLabel,ruler);
    const trackArea=el('div',undefined,'timeline-track-area'),trackViewport=el('div',undefined,'timeline-track-viewport'),tracks=el('div',undefined,'timeline-tracks'),empty=el('p','Reading local coverage…','timeline-empty');trackViewport.id='timeline-track-viewport';trackViewport.append(tracks,empty);
    const addRow=el('div',undefined,'timeline-add-track-row');addRow.append(addButton,picker);tracks.append(addRow);
    function makeScrollbar(axis) {
      const bar=el('div',undefined,'timeline-scrollbar timeline-scrollbar-'+axis);bar.id='timeline-scrollbar-'+axis;bar.dataset.axis=axis;
      const thumb=el('div',undefined,'timeline-scroll-thumb');thumb.tabIndex=0;thumb.setAttribute('role','scrollbar');thumb.setAttribute('aria-orientation',axis);thumb.setAttribute('aria-label',axis==='horizontal'?'Visible block range':'Visible index tracks');thumb.setAttribute('aria-controls',axis==='horizontal'?'timeline-ruler':'timeline-track-viewport');
      const start=button('',null,axis==='horizontal'?'Change first visible block':'Increase or decrease track height from the top',()=>{}),end=button('',null,axis==='horizontal'?'Change last visible block':'Increase or decrease track height from the bottom',()=>{});
      for(const [handle,edge] of [[start,'start'],[end,'end']]){handle.className='timeline-scroll-handle '+edge;handle.dataset.edge=edge;handle.setAttribute('role','slider');handle.setAttribute('aria-orientation',axis);thumb.append(handle);}
      bar.append(thumb);return {bar,thumb,start,end,axis};
    }
    const horizontal=makeScrollbar('horizontal'),vertical=makeScrollbar('vertical'),scrollbarRow=el('div',undefined,'timeline-scrollbar-row');scrollbarRow.append(el('div',undefined,'timeline-scrollbar-spacer'),horizontal.bar);trackArea.append(trackViewport,vertical.bar);
    const footer=el('div',undefined,'timeline-footer'),legend=el('div',undefined,'timeline-legend');
    for(const [cls,text] of [['saved','Available locally'],['potential','Potential · not stored'],['partial','Mixed coverage · zoom in'],['requested','Requested work']]){const item=el('span',undefined,'timeline-legend-item'),swatch=el('i',undefined,'timeline-swatch '+cls);swatch.setAttribute('aria-hidden','true');item.append(swatch,el('span',text));legend.append(item);}
    const viewportLabel=el('span','','timeline-viewport-label');viewportLabel.id='timeline-viewport-label';footer.append(legend,viewportLabel);
    const help=el('details',undefined,'timeline-help');help.append(el('summary','Navigation help'));
    const instructions=el('p','Click a timeline lane to move the playhead; drag the lane to pan. Drag the ruler or its playhead and pause to load the focused item. Shift + click selects an inclusive indexing range. Ctrl / Command + click adds or removes a block. Wheel, Ctrl / Command + wheel or pinch: zoom at the pointer. Horizontal scrolling or Shift + wheel: pan. Alt + wheel: resize tracks. Scroll over track labels or use the vertical scrollbar to move through tracks. Hold Space and drag to pan in both directions. Drag scrollbar edges to zoom; drag its middle to move. With a track focused, + / − zoom, arrows pan, and Home fits the chain.','timeline-instructions');instructions.id='timeline-instructions';help.append(instructions);
    const coverageDetails=el('details',undefined,'timeline-exact'),coverageSummary=el('summary','Exact coverage'),coverageList=el('div',undefined,'timeline-exact-list');coverageDetails.append(coverageSummary,coverageList);
    const tooltip=el('div',undefined,'timeline-tooltip');tooltip.hidden=true;tooltip.setAttribute('role','status');
    help.firstChild.textContent='?';help.firstChild.setAttribute('aria-label','Timeline navigation help');help.firstChild.title='Timeline navigation help';rulerLabel.append(help);inspector.append(coverageDetails);
    const collapse=button('⌄','timeline-collapse','Collapse timeline to a chain scrubber',()=>setCollapsed(true));collapse.setAttribute('aria-expanded','true');collapse.setAttribute('aria-controls','timeline-editor');rulerLabel.append(collapse);
    const compact=el('div',undefined,'timeline-compact');compact.hidden=true;
    const expand=button('⌃ Tracks','timeline-expand','Expand index tracks',()=>setCollapsed(false));
    const compactPosition=el('output','Choose a block','timeline-current-height');compactPosition.id='timeline-current-height';
    const compactLabel=el('div',undefined,'timeline-compact-label');compactLabel.append(expand,compactPosition);
    const compactScrubber=el('canvas',undefined,'timeline-compact-lane');compactScrubber.id='timeline-compact-scrubber';compactScrubber.tabIndex=0;compactScrubber.setAttribute('role','slider');compactScrubber.setAttribute('aria-label','Timeline playhead');compactScrubber.setAttribute('aria-orientation','horizontal');compactScrubber.addEventListener('keydown',positionKey);
    compactScrubber.addEventListener('click',event=>{if(suppressClick||spaceHeld)return;focusPosition(heightAt(event.clientX),true);});
    compactScrubber.addEventListener('dblclick',()=>zoomIntoFocus());
    compact.append(compactLabel,compactScrubber);
    const hierarchyBar=el('div',undefined,'timeline-hierarchy-bar'),breadcrumbs=el('nav',undefined,'timeline-breadcrumbs'),hierarchyNote=el('span','','timeline-hierarchy-note');breadcrumbs.id='timeline-breadcrumbs';breadcrumbs.setAttribute('aria-label','Timeline hierarchy');hierarchyNote.id='timeline-hierarchy-note';hierarchyNote.setAttribute('role','status');hierarchyBar.append(breadcrumbs,hierarchyNote);
    const upper=el('div',undefined,'timeline-upper-workspace'),viewport=el('section',undefined,'workspace-viewport');viewport.id='workspace-viewport';viewport.setAttribute('aria-label','Resource visualization');
    const fullscreen=button('⛶','workspace-viewport-fullscreen','Fullscreen resource visualization',()=>{});fullscreen.setAttribute('aria-pressed','false');
    const viewportBody=el('div',undefined,'workspace-viewport-body');viewportBody.id='workspace-viewport-body';viewportBody.append(el('p','Select a block or inscription to view it.','workspace-viewport-empty'));
    viewport.append(fullscreen,viewportBody);upper.append(inspector,viewport);
    editor.append(hierarchyBar,rulerRow,compact,trackArea,scrollbarRow,footer,tooltip);root.append(upper,editor);
    function setCollapsed(value,focusControl=true){closeRevealPicker();root.dataset.timelineCollapsed=String(Boolean(value));compact.hidden=!value;trackArea.hidden=footer.hidden=Boolean(value);collapse.hidden=Boolean(value);collapse.setAttribute('aria-expanded',String(!value));expand.setAttribute('aria-expanded',String(!value));doc.defaultView.dispatchEvent(new doc.defaultView.Event('gateway:timeline-layout'));draw();if(focusControl){if(value)compactScrubber.focus();else collapse.focus();}}
    function allDefinitions(){return [...(snapshot.definitions||[])].sort((a,b)=>a.id==='headers'?-1:b.id==='headers'?1:0);}
    function ordered(){return allDefinitions().filter(d=>d.id==='headers'||!d.locked&&!hidden.has(d.id)&&(['blocks','utxo-state'].includes(d.id)||added.has(d.id)||established(d,snapshot))).flatMap(d=>d.id==='txo-spender'?childDefinitions(snapshot):[d]);}
    function renderPicker(){
      const defs=allDefinitions(),visible=new Set(ordered().map(d=>d.owner||d.id)),key=JSON.stringify(defs.map(d=>[d.id,d.name,d.locked,d.lock_reason,visible.has(d.id)]));if(picker.dataset.key===key)return;picker.dataset.key=key;picker.replaceChildren();
      for(const d of defs){if(visible.has(d.id))continue;const option=button(d.name+(d.locked?' · Locked':''),null,d.locked?d.lock_reason||'Available in a later release':'Add '+d.name,()=>{
        if(d.locked)return;hidden.delete(d.id);added.add(d.id);save();picker.hidden=true;addButton.setAttribute('aria-expanded','false');render();select(d.id,d.buildable?'build':'providers');advanced.open=true;entries.get(d.id)?.head.focus();entries.get(d.id)?.row.scrollIntoView({block:'nearest'});
      });option.dataset.index=d.id;option.disabled=!!d.locked;if(d.locked)option.append(el('small',d.lock_reason||'Available in a later release.'));picker.append(option);}
      if(!picker.children.length)picker.append(el('p','All available indexes have been added.'));
    }
    function positionPicker(){const rect=addButton.getBoundingClientRect(),vw=doc.documentElement.clientWidth||800,vh=doc.documentElement.clientHeight||600,gap=8,margin=16,below=Math.max(0,vh-margin-rect.bottom-gap),above=Math.max(0,rect.top-margin-gap),down=below>=Math.min(120,picker.scrollHeight)||below>=above;picker.style.position='fixed';picker.style.maxHeight=(down?below:above)+'px';const box=picker.getBoundingClientRect();picker.style.left=clamp(rect.left,8,Math.max(8,vw-box.width-8))+'px';picker.style.top=(down?rect.bottom+gap:Math.max(margin,rect.top-gap-box.height))+'px';}
    doc.addEventListener('pointerdown',event=>{if(!addRow.contains(event.target)){picker.hidden=true;addButton.setAttribute('aria-expanded','false');}});
    picker.addEventListener('keydown',event=>{if(event.key==='Escape'){event.preventDefault();picker.hidden=true;addButton.setAttribute('aria-expanded','false');addButton.focus();}});
    function current(){const d=definitionFor(selected);return d?trackModel(d,snapshot):null;}
    function parentKey(value){return [value.kind,value.height,value.txIndex??'',value.outputIndex??''].join(':');}
    function coordinate(value){if(value.kind==='block')return value.height+'.bitcoin';if(value.kind==='transaction')return value.txIndex+'.'+value.height+'.bitcoin';if(value.kind==='input')return 'i'+value.inputIndex+'.'+value.txIndex+'.'+value.height+'.bitcoin';if(value.kind==='output')return value.outputIndex+'.'+value.txIndex+'.'+value.height+'.bitcoin';return value.satOffset+'.'+value.outputIndex+'.'+value.txIndex+'.'+value.height+'.bitcoin';}
    function childEntity(index){const p=frame();if(!p)return {kind:'block',height:index,address:index+'.bitcoin'};const value=p.kind==='block'?{kind:'transaction',height:p.height,txIndex:index}:p.kind==='transaction'?{kind:'output',height:p.height,txIndex:p.txIndex,outputIndex:index}:{kind:'satpoint',height:p.height,txIndex:p.txIndex,outputIndex:p.outputIndex,satOffset:index};return {...value,address:coordinate(value)};}
    function positionLabel(index){const p=frame();return (p?(p.kind==='block'?'Transaction ':p.kind==='transaction'?'Output ':'Output-local sat '):'Block ')+format(index);}
    function cancelHierarchy(){closeRevealPicker();hierarchySerial++;revealSerial++;revealRequest?.abort();revealRequest=null;hierarchyRequest?.abort();hierarchyRequest=null;validationRequest?.abort();validationRequest=null;hierarchyBusy=false;doc.defaultView.clearTimeout(entityTimer);entityTimer=null;}
    function returnParent(index,inspect=false){const parent=index<0?hierarchy[0]:hierarchy[index];cancelHierarchy();hierarchy=index<0?[]:hierarchy.slice(0,index+1);hierarchyError='';redraw();if(inspect&&parent){const value={kind:parent.kind,height:parent.height,txIndex:parent.txIndex,outputIndex:parent.outputIndex};onEntity?.({...value,address:coordinate(value)},{immediate:true});}}
    function invalidateAnchor(block,message='The selected block changed. Zoom into its current contents again.'){cancelHierarchy();for(const [key,item] of hierarchyCache)if(item.height===block)hierarchyCache.delete(key);revealCache.delete(block);if(hierarchy.some(p=>p.height===block)){hierarchy=[];view=clampView({from:block,to:block},extent);fit=false;hierarchyError=message;}if(focusedResource?.height===block)focusedResource=null;}
    function acceptResource(value,metadata,{preserveFocus=false}={}){
      if(metadata?.block_hash&&height(metadata.height)){const before=resourceAnchors.get(metadata.height);if(before&&before!==metadata.block_hash)invalidateAnchor(metadata.height);resourceAnchors.delete(metadata.height);resourceAnchors.set(metadata.height,metadata.block_hash);while(resourceAnchors.size>16)resourceAnchors.delete(resourceAnchors.keys().next().value);}
      if(value?.resolved&&!preserveFocus)focusedResource=value;
      if(metadata&&['block','transaction','output'].includes(metadata.kind)&&height(metadata.height)&&(metadata.count==null||height(metadata.count))){const checked={...metadata,count:height(metadata.count)?metadata.count:null};hierarchyCache.delete(parentKey(checked));hierarchyCache.set(parentKey(checked),checked);while(hierarchyCache.size>16)hierarchyCache.delete(hierarchyCache.keys().next().value);const active=hierarchy.find(p=>parentKey(p)===parentKey(checked));if(active&&checked.count!==null){if(checked.count===0){cancelHierarchy();hierarchy=hierarchy.slice(0,hierarchy.indexOf(active));hierarchyError='This verified parent has no children.';}else{active.count=checked.count;active.entries=checked.entries;active.view=clampView(active.view,checked.count-1);if(active.focused>=checked.count)active.focused=null;}}}
      const p=frame();if(!preserveFocus&&p&&value?.height===p.height){if(p.kind==='block'&&height(value.txIndex))p.focused=value.txIndex;else if(p.kind==='transaction'&&value.txIndex===p.txIndex&&height(value.outputIndex))p.focused=value.outputIndex;else if(p.kind==='output'&&value.txIndex===p.txIndex&&value.outputIndex===p.outputIndex&&height(value.satOffset))p.focused=value.satOffset;}
      draw();
    }
    function focusHeight(block,immediate=true){if(!height(block))return;dimensions();if(!fit&&(block<view.from||block>view.to)){const span=view.to-view.from+1,from=Math.max(0,block-Math.floor(span/2));view=clampView({from,to:from+span-1},extent);}focusBlock(block,immediate);}
    // A resolved response is evidence, not a fresh user navigation. It may
    // complete after a newer zoom began. Explicit navigation cancels that zoom
    // at intent time; passive echoes still update/recheck the block anchor.
    function syncResource(value,metadata){if(!height(value?.height))return;const preserveFocus=hierarchyBusy&&value.height===playhead;if(!preserveFocus)focusHeight(value.height,true);acceptResource(value,metadata,{preserveFocus});}
    function focusPosition(index,immediate=true){if(!height(index)||index>navExtent())return;if(hierarchyBusy)cancelHierarchy();const visible=navView();if(index<visible.from||index>visible.to){const span=visible.to-visible.from+1,from=Math.max(0,index-Math.floor(span/2));setNavView({from,to:from+span-1});}if(!frame()){focusBlock(index,immediate);return;}const p=frame();p.focused=index;playhead=p.height;root.dataset.playhead=String(playhead);const entity=childEntity(index);doc.defaultView.clearTimeout(entityTimer);entityTimer=null;if(immediate)onEntity?.(entity,{immediate:true});else entityTimer=doc.defaultView.setTimeout(()=>{entityTimer=null;onEntity?.(entity,{immediate:false});},1000);draw();}
    function positionKey(event){if(['+','='].includes(event.key)){event.preventDefault();zoom(.25);return;}if(event.key==='-'){event.preventDefault();zoom(2);return;}if(event.key==='Enter'){event.preventDefault();zoomIntoFocus();return;}if(event.key==='Escape'&&frame()){event.preventDefault();returnParent(hierarchy.length-2);return;}if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return;event.preventDefault();const v=navView(),focused=frame()?(frame().focused??v.from):(playhead??v.from);focusPosition(event.key==='Home'?0:event.key==='End'?navExtent():clamp(focused+(event.key==='ArrowLeft'?-1:1),0,navExtent()),true);}
    function paintHierarchy(){const p=frame();breadcrumbs.replaceChildren();const chain=button('Chain',null,'Return to the Bitcoin chain',()=>returnParent(-1,true));chain.setAttribute('aria-current',p?'false':'page');breadcrumbs.append(chain);for(const [i,parent] of hierarchy.entries()){const label=parent.kind==='block'?'Block '+format(parent.height):parent.kind==='transaction'?'TX '+format(parent.txIndex):'Output '+format(parent.outputIndex),crumb=button(label,null,'Return to '+label,()=>returnParent(i,true));crumb.setAttribute('aria-current',i===hierarchy.length-1?'page':'false');breadcrumbs.append(crumb);}root.dataset.hierarchy=p?.kind||'chain';root.dataset.hierarchyDepth=String(hierarchy.length);hierarchyNote.textContent=hierarchyBusy?'Loading selected parent…':hierarchyError||(p?(p.kind==='output'?'Output-local sat offsets':height(p.count)?format(p.count)+' '+(p.kind==='block'?'ordered transactions':'ordered outputs'):'Child count unavailable'):'Zoom into a block to explore its contents');}
    async function descend(){
      if(hierarchyBusy||frame()?.kind==='output')return;const v=navView();if(v.from!==v.to)return;const parent=childEntity(v.from);if(!['block','transaction','output'].includes(parent.kind))return;
      cancelHierarchy();const ticket=hierarchySerial;hierarchyBusy=true;hierarchyError='';paintHierarchy();const controller=doc.defaultView.AbortController?new doc.defaultView.AbortController():null;hierarchyRequest=controller;
      try{const metadata=loadHierarchy?await loadHierarchy(parent,{signal:controller?.signal}):hierarchyCache.get(parentKey(parent));if(ticket!==hierarchySerial)return;if(!metadata||metadata.kind!==parent.kind||metadata.height!==parent.height||parent.txIndex!==undefined&&metadata.txIndex!==parent.txIndex||parent.outputIndex!==undefined&&metadata.outputIndex!==parent.outputIndex||!height(metadata.count))throw Error('Child count is unavailable. The timeline stays at its known parent.');if(metadata.count===0)throw Error('This verified parent has no children.');acceptResource(parent,metadata);if(ticket!==hierarchySerial)return;hierarchy.push({...metadata,view:{from:0,to:metadata.count-1},focused:null});playhead=parent.height;hierarchyNote.textContent='';redraw();if(!focusedResource||coordinate(focusedResource)!==coordinate(parent))onEntity?.(parent,{immediate:false});}
      catch(error){if(ticket===hierarchySerial&&error.name!=='AbortError'){hierarchyError=error.message;notify?.(error.message,true);}}
      finally{if(ticket===hierarchySerial){hierarchyBusy=false;hierarchyRequest=null;paintHierarchy();}}
    }
    function zoomIntoFocus(){const v=navView(),n=frame()?(frame().focused??Math.floor((v.from+v.to)/2)):(height(playhead)?playhead:Math.floor((v.from+v.to)/2));if(n<v.from||n>v.to)return;if(v.from===n&&v.to===n){descend();return;}setNavView({from:n,to:n});redraw();}
    function drawPositionLane(canvas,entry){const p=frame(),v=navView(),span=v.to-v.from+1,w=Math.max(1,canvas.getBoundingClientRect().width),h=canvas===compactScrubber?30:trackHeight,dpr=Math.min(2,doc.defaultView.devicePixelRatio||1),dark=doc.documentElement.dataset.theme==='dark';canvas.width=Math.round(w*dpr);canvas.height=Math.round(h*dpr);canvas.dataset.kind=p?.kind||'chain';canvas.dataset.from=String(v.from);canvas.dataset.to=String(v.to);canvas.dataset.count=String(p?.count??extent+1);canvas.setAttribute('aria-valuemin','0');canvas.setAttribute('aria-valuemax',String(navExtent()));canvas.setAttribute('aria-valuenow',String(p?(p.focused??v.from):(playhead??v.from)));canvas.setAttribute('aria-valuetext',positionLabel(p?(p.focused??v.from):(playhead??v.from)));const ctx=canvas.getContext('2d');if(!ctx)return;ctx.scale(dpr,dpr);ctx.fillStyle=dark?'#26332f':'#e9ebe3';ctx.fillRect(0,Math.max(3,h*.15),w,h*.7);const bins=Math.min(512,Math.floor(w),span),focused=p?p.focused:playhead;for(let i=0;i<bins;i++){const first=v.from+Math.floor(i*span/bins),last=v.from+Math.floor((i+1)*span/bins)-1,x=i/bins*w,width=w/bins,active=focused!==null&&focused>=first&&focused<=last;ctx.fillStyle=active?(dark?'#80baff':'#235fa1'):dark?'#375044':'#d6ddd2';ctx.fillRect(x+1,Math.max(3,h*.15),Math.max(1,width-2),h*.7);if(width>48){ctx.fillStyle=active?(dark?'#152637':'#ffffff'):(dark?'#e8f0e8':'#233e30');ctx.font='10px ui-monospace,monospace';ctx.textAlign='center';ctx.fillText(String(first)+(last>first?'…':''),x+width/2,h/2+3,Math.max(1,width-5));}}if(focused!==null&&focused>=v.from&&focused<=v.to){const x=(focused-v.from+.5)/span*w;ctx.strokeStyle=dark?'#80baff':'#235fa1';ctx.lineWidth=2;ctx.beginPath();ctx.moveTo(x,0);ctx.lineTo(x,h);ctx.stroke();}if(entry){entry.markers.replaceChildren();entry.canvas.dataset.detail=p?.kind||'chain';entry.canvas.dataset.selection=JSON.stringify(selectedRanges(entry.d.id));}}
    function ensureRevealMarkers(){const p=frame();if(root.dataset.timelineCollapsed==='true'||p?.kind!=='block'||!entries.has('inscriptions')||revealCache.has(p.height)||revealRequest||!loadRevealMarkers)return;const ticket=++revealSerial,controller=doc.defaultView.AbortController?new doc.defaultView.AbortController():null;revealRequest=controller;Promise.resolve(loadRevealMarkers({kind:'block',height:p.height},{signal:controller?.signal})).then(result=>{if(ticket!==revealSerial||frame()?.height!==p.height)return;revealCache.set(p.height,{...result,markers:(result?.markers||[]).filter(m=>m.height===p.height&&height(m.txIndex)&&m.txIndex<p.count&&height(m.inscriptionIndex)).slice(0,1000)});while(revealCache.size>16)revealCache.delete(revealCache.keys().next().value);draw();}).catch(error=>{if(ticket===revealSerial&&error.name!=='AbortError'){revealCache.set(p.height,{markers:[],known:false,note:error.message});draw();}}).finally(()=>{if(ticket===revealSerial)revealRequest=null;});}
    function drawParentLane(e){const p=frame(),m=trackModel(e.d,snapshot),canvas=e.canvas,w=Math.max(1,canvas.getBoundingClientRect().width),h=trackHeight,dpr=Math.min(2,doc.defaultView.devicePixelRatio||1),dark=doc.documentElement.dataset.theme==='dark',local=m.coverage.some(r=>r.from<=p.height&&r.to>=p.height);canvas.hidden=false;e.placeholder.hidden=true;e.markers.replaceChildren();canvas.width=Math.round(w*dpr);canvas.height=Math.round(h*dpr);canvas.dataset.detail='block-context';canvas.dataset.selection=JSON.stringify(selectedRanges(e.d.id));canvas.setAttribute('aria-label',e.d.name+' containing block '+format(p.height)+(local?' available locally':' not stored locally'));const ctx=canvas.getContext('2d');if(!ctx)return;ctx.scale(dpr,dpr);ctx.fillStyle=local?(dark?'#96c9ad':'#517965'):(dark?'#26332f':'#e9ebe3');ctx.fillRect(0,h*.25,w,h*.5);ctx.font='11px ui-monospace,monospace';ctx.textAlign='center';ctx.fillStyle=local?(dark?'#182d26':'#ffffff'):(dark?'#e8f0e8':'#233e30');ctx.fillText('Block '+format(p.height),w/2,h/2+4);}
    function closeRevealPicker(restore=false){if(!revealChooser)return;revealChooser.remove();revealChooser=null;const anchor=revealChooserAnchor;revealChooserAnchor=null;if(restore)(anchor?.isConnected?anchor:entries.get('inscriptions')?.markers.querySelector('[data-tx-index="'+anchor?.dataset.txIndex+'"]'))?.focus({preventScroll:true});}
    function showRevealPicker(markers,anchor){closeRevealPicker();const box=el('div',undefined,'timeline-reveal-picker');box.id='timeline-reveal-picker';box.setAttribute('role','group');box.setAttribute('aria-label','Inscriptions revealed in transaction '+markers[0].txIndex);box.append(el('strong',markers.length+' inscriptions · TX '+format(markers[0].txIndex)));const close=button('×',null,'Close inscription choices',()=>closeRevealPicker(true));close.className='timeline-reveal-close';box.append(close);const list=el('div',undefined,'timeline-reveal-choices');for(const marker of markers){const option=button(marker.address,null,'Open inscription '+marker.address,()=>{closeRevealPicker();onInscription?.(marker);});option.dataset.coordinate=marker.address;list.append(option);}box.append(list);editor.append(box);revealChooser=box;revealChooserAnchor=anchor;const rect=anchor.getBoundingClientRect(),vw=doc.documentElement.clientWidth||800,vh=doc.documentElement.clientHeight||600;box.style.maxHeight=Math.max(80,vh-32)+'px';const measured=box.getBoundingClientRect();box.style.left=clamp(rect.left-measured.width/2,8,Math.max(8,vw-measured.width-8))+'px';box.style.top=clamp(rect.top-measured.height-6,8,Math.max(8,vh-measured.height-8))+'px';list.firstElementChild?.focus();box.addEventListener('keydown',event=>{if(event.key==='Escape'){event.preventDefault();event.stopPropagation();closeRevealPicker(true);}else if(['ArrowDown','ArrowUp','Home','End'].includes(event.key)){const choices=[...list.children],i=choices.indexOf(doc.activeElement),target=event.key==='Home'?0:event.key==='End'?choices.length-1:clamp(i+(event.key==='ArrowDown'?1:-1),0,choices.length-1);event.preventDefault();choices[target]?.focus();}});}
    doc.addEventListener('pointerdown',event=>{if(revealChooser&&!revealChooser.contains(event.target)&&event.target!==revealChooserAnchor)closeRevealPicker();});
    function drawRevealLane(e){const p=frame(),v=navView(),span=v.to-v.from+1,record=revealCache.get(p.height),canvas=e.canvas,w=Math.max(1,canvas.getBoundingClientRect().width),h=trackHeight,dpr=Math.min(2,doc.defaultView.devicePixelRatio||1),dark=doc.documentElement.dataset.theme==='dark';canvas.width=Math.round(w*dpr);canvas.height=Math.round(h*dpr);canvas.dataset.detail='inscription-reveals';canvas.dataset.revealKnown=String(record?.known===true);canvas.dataset.reveals=JSON.stringify(record?.markers||[]);canvas.setAttribute('aria-label','Known inscription reveal positions in block '+format(p.height));e.placeholder.hidden=record?.known===true;e.placeholder.textContent=record?'Reveal coverage not established':'Reading known reveal positions…';e.markers.replaceChildren();const ctx=canvas.getContext('2d');if(!ctx)return;ctx.scale(dpr,dpr);ctx.fillStyle=dark?'#26332f':'#e9ebe3';ctx.fillRect(0,h*.3,w,h*.4);const visible=(record?.markers||[]).filter(m=>m.txIndex>=v.from&&m.txIndex<=v.to),groups=new Map();for(const marker of visible){const group=groups.get(marker.txIndex)||[];group.push(marker);groups.set(marker.txIndex,group);}for(const [txIndex,markers]of [...groups].slice(0,128)){const x=(txIndex-v.from+.5)/span*w;ctx.fillStyle=dark?'#d9edb8':'#335c41';ctx.fillRect(x-1,h*.2,2,h*.6);const b=button('',null,markers.length===1?'Open inscription '+markers[0].address:'Choose '+markers.length+' inscriptions in transaction '+txIndex,()=>{if(markers.length===1)onInscription?.(markers[0]);else showRevealPicker(markers,b);});b.className='timeline-selection-marker single';b.style.left=((txIndex-v.from+.5)/span*100)+'%';b.dataset.txIndex=String(txIndex);b.dataset.inscriptionCount=String(markers.length);b.dataset.coordinate=markers[0].address;b.addEventListener('pointerdown',event=>event.stopPropagation());b.addEventListener('click',event=>event.stopPropagation());e.markers.append(b);}if(record?.truncated||groups.size>128){e.placeholder.hidden=false;e.placeholder.textContent='Showing bounded known reveal markers; zoom closer for detail';}}
    function dimensions(){const values=[snapshot.timeline?.tip_height,snapshot.timeline?.target_height];for(const d of snapshot.definitions||[])for(const r of trackModel(d,snapshot).coverage)values.push(r.to);for(const job of snapshot.jobs||[])if(!(snapshot.definitions||[]).find(d=>d.id===job.index)?.locked)values.push(job.to);extent=Math.max(0,...values.filter(height));view=fit?{from:0,to:extent}:clampView(view,extent);root.dataset.from=String(view.from);root.dataset.to=String(view.to);root.dataset.fit=String(fit);}
    function makeGroup(id){
      const row=el('div',undefined,'timeline-group');row.dataset.group=id;
      const heading=el('div',undefined,'timeline-group-heading'),toggle=button('', 'timeline-group-'+id,null,()=>{expandedGroups[id]=!expandedGroups[id];save();render();});
      toggle.className='timeline-group-toggle';toggle.setAttribute('aria-controls','timeline-group-bands-'+id);
      const name=el('strong',id==='blocks'?'Blocks':'TXIO'),labels=el('span',undefined,'timeline-group-labels');toggle.append(name,labels);heading.append(toggle);
      const bands=el('div',undefined,'timeline-group-bands');bands.id='timeline-group-bands-'+id;row.append(heading,bands);const group={id,row,toggle,name,labels,bands,canvases:new Map()};groups.set(id,group);return group;
    }
    function renderGroups(defs){
      const activeGroups=new Set(defs.map(d=>groupOf(d.id)).filter(Boolean));
      for(const [id,g]of groups)if(!activeGroups.has(id)){g.row.remove();groups.delete(id);}
      const emitted=new Set(),rows=[];for(const d of defs){const id=groupOf(d.id),e=entries.get(d.id);if(id){const g=groups.get(id)||makeGroup(id);if(!emitted.has(id)){
        emitted.add(id);rows.push(g.row);g.row.dataset.expanded=String(expandedGroups[id]);g.toggle.setAttribute('aria-expanded',String(expandedGroups[id]));g.toggle.setAttribute('aria-label',(expandedGroups[id]?'Collapse ':'Expand ')+(id==='blocks'?'Blocks':'TXIO')+' layers');const children=defs.filter(d=>groupOf(d.id)===id),key=JSON.stringify(children.map(d=>[d.id,d.name]));if(g.key!==key){g.key=key;g.labels.replaceChildren();g.bands.replaceChildren();g.canvases.clear();
        for(const child of children){const label=child.id==='headers'?'Headers':child.id==='blocks'?'Raw blocks':child.name,tag=el('span',label);tag.dataset.index=child.id;tag.style.setProperty('--layer-color',trackColor(child.id,doc.documentElement.dataset.theme==='dark'));g.labels.append(tag);
          const canvas=el('canvas',undefined,'timeline-group-band');canvas.dataset.index=child.id;canvas.tabIndex=0;canvas.setAttribute('role','button');canvas.setAttribute('aria-label',label+' coverage; select this layer');canvas.addEventListener('click',()=>select(child.id,child.buildable?'build':'providers'));canvas.addEventListener('keydown',event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();select(child.id,child.buildable?'build':'providers');}});g.bands.append(canvas);g.canvases.set(child.id,canvas);
        }}
      }e.row.dataset.group=id;e.row.hidden=!expandedGroups[id];e.name.textContent=d.id==='headers'?'Headers':d.id==='blocks'?'Raw blocks':d.name;
      }else{delete e.row.dataset.group;e.row.hidden=false;}rows.push(e.row);}rows.forEach((row,i)=>{if(tracks.children[i]!==row)tracks.insertBefore(row,tracks.children[i]||addRow);});
    }
    function drawGroups(){for(const g of groups.values()){g.bands.hidden=expandedGroups[g.id];for(const tag of g.labels.children)tag.style.setProperty('--layer-color',trackColor(tag.dataset.index,doc.documentElement.dataset.theme==='dark'));for(const [id,canvas]of g.canvases){const d=definitionFor(id),m=trackModel(d,snapshot),w=Math.max(1,canvas.getBoundingClientRect().width),h=12,dark=doc.documentElement.dataset.theme==='dark',dpr=Math.min(2,doc.defaultView.devicePixelRatio||1);canvas.width=Math.round(w*dpr);canvas.height=h*dpr;canvas.dataset.coverage=JSON.stringify(m.coverage);canvas.setAttribute('aria-label',(id==='blocks'?'Raw blocks':id==='headers'?'Headers':d.name)+' coverage: '+blockCount(m.total)+(m.complete?'':' · mapping incomplete'));const ctx=canvas.getContext('2d');if(!ctx)continue;ctx.scale(dpr,dpr);ctx.fillStyle=dark?'#26332f':'#e9ebe3';ctx.fillRect(0,2,w,8);ctx.globalAlpha=healthy?1:.45;for(const bin of coverageBins(m.coverage,frame()?{from:frame().height,to:frame().height}:view,w)){ctx.fillStyle=trackColor(id,dark);ctx.fillRect(bin.x,2,Math.max(.8,bin.width),8);if(bin.count<bin.total){ctx.clearRect(bin.x,5,Math.max(.8,bin.width),2);}}ctx.globalAlpha=1;}}}
    function makeTrack(d) {
      const row=el('div',undefined,'timeline-track');row.dataset.index=d.id;
      const head=button('',null,null,()=>select(d.id,d.buildable?'build':'providers'));head.className='timeline-track-select';
      const number=el('span','','timeline-track-number'),name=el('strong',d.name),state=el('span','','timeline-track-state'),detail=el('span','','timeline-track-count');head.append(number,name,state,detail);
      const remove=button('×',null,'Remove '+d.name+' track from workspace; keep stored data',()=>removeTrack(d.owner||d.id));remove.className='timeline-track-remove';remove.dataset.index=d.id;remove.hidden=d.id==='headers'||!!d.owner;
      const heading=el('div',undefined,'timeline-track-heading');heading.append(head,remove);
      const trackBody=el('div',undefined,'timeline-track-body'),canvas=el('canvas',undefined,'timeline-lane');canvas.dataset.index=d.id;canvas.tabIndex=0;canvas.setAttribute('role','button');canvas.setAttribute('aria-describedby','timeline-instructions');
      const placeholder=el('span','','timeline-track-placeholder'),markers=el('div',undefined,'timeline-selection-markers');trackBody.append(canvas,markers,placeholder);row.append(heading,trackBody);tracks.insertBefore(row,addRow);
      const e={row,head,remove,number,name,state,detail,canvas,markers,placeholder,d};entries.set(d.id,e);
      canvas.addEventListener('click',event=>hit(e,event));
      canvas.addEventListener('dblclick',()=>{if(frame()){zoomIntoFocus();return;}if(selection?.id===d.id){if(view.to-view.from<64&&height(playhead))zoomIntoFocus();else focusSelection();}});
      canvas.addEventListener('keydown',event=>{
        if(frame()){positionKey(event);return;}if(event.key==='+'||event.key==='='){event.preventDefault();zoom(.25);}else if(event.key==='-'){event.preventDefault();zoom(2);}else if(event.key==='Home'){event.preventDefault();fit=true;redraw();}else if(event.key==='ArrowLeft'||event.key==='ArrowRight'){event.preventDefault();if(view.to-view.from<64&&selection?.kind==='block'&&selection.id===d.id){const n=Math.max(0,Math.min(extent,selection.from+(event.key==='ArrowLeft'?-1:1)));choose({id:d.id,kind:'block',from:n,to:n});focusBlock(n,true);if(n<view.from||n>view.to){view=clampView({from:Math.max(0,n-16),to:Math.max(0,n-16)+32},extent);fit=false;redraw();}}else pan(event.key==='ArrowLeft'?-.6:.6);}else if(event.key==='Enter'){event.preventDefault();activateLane(e);}
      });
      canvas.addEventListener('pointermove',event=>{if(!drag&&!pinch&&!spaceHeld)showTooltip(e,event);});canvas.addEventListener('pointerleave',()=>{tooltip.hidden=true;});canvas.addEventListener('blur',()=>{tooltip.hidden=true;});
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
    function heightAt(clientX){const rect=ruler.getBoundingClientRect(),v=navView(),span=v.to-v.from+1;return Math.min(v.to,v.from+Math.floor(clamp((clientX-rect.left)/Math.max(1,rect.width),0,1)*span));}
    function selectedRanges(id=selected){return normalizeRanges(indexSelections.get(id)?.ranges||[]);}
    function prepareRange(r){if(selected==='utxo-state')return;advanced.open=true;buildRange?.(definitionFor(selected)?.owner||selected,r.from,r.to);}
    function focusBlock(block,immediate=true){if(!height(block))return;if(hierarchyBusy)cancelHierarchy();if(frame()&&frame().height!==block){cancelHierarchy();hierarchy=[];}if(!selected){const d=ordered()[0];if(d)select(d.id,'providers');}playhead=clamp(block,0,extent);root.dataset.playhead=String(playhead);if(selected&&selected!=='utxo-state'&&healthy&&!current()?.locked)onFocus?.({index:definitionFor(selected)?.owner||selected,height:playhead,immediate});draw();}
    function removeTrack(id){const d=(snapshot.definitions||[]).find(d=>d.id===id);if(!d||id==='headers')return;if(!doc.defaultView.confirm('Remove '+d.name+' from this workspace? Stored data and indexing jobs will be kept. You can add the track again using the plus button.'))return;hidden.add(id);added.delete(id);save();if(selected===id){select('headers','providers');}render();notify?.('Track removed from the workspace. Stored data and jobs were kept.');}
    function hit(e,event){if(suppressClick||spaceHeld)return;if(e.d.id==='utxo-state'){select(e.d.id,'build');return;}if(trackModel(e.d,snapshot).locked){select(e.d.id,'catalog');return;}if(frame()){if(e.d.id!==selected)select(e.d.id,'providers');if(['headers','blocks'].includes(e.d.id)){focusBlock(frame().height,true);return;}if(e.d.id==='tx-locator'&&frame().kind!=='output')focusPosition(heightAt(event.clientX),true);if(e.d.id==='inscriptions'&&frame().kind==='block'){const markers=revealCache.get(frame().height)?.markers.filter(m=>m.txIndex===heightAt(event.clientX))||[];if(markers.length===1)onInscription?.(markers[0]);else if(markers.length>1)showRevealPicker(markers,e.markers.querySelector('[data-tx-index="'+markers[0].txIndex+'"]')||e.canvas);}return;}const block=heightAt(event.clientX),prior=indexSelections.get(e.d.id)||{ranges:[],anchor:null};if(event.shiftKey||event.ctrlKey||event.metaKey){const next=selectBlock(prior.ranges,prior.anchor,block,{extend:event.shiftKey,toggle:event.ctrlKey||event.metaKey});chooseSet(e.d.id,next.ranges,next.anchor);}else{if(e.d.id!==selected)select(e.d.id,e.d.buildable?'build':'providers');focusBlock(block,true);}}
    function activateLane(e){if(!e)return;if(view.to-view.from<64){const n=Math.floor((view.from+view.to)/2);choose({id:e.d.id,kind:'block',from:n,to:n});focusBlock(n,true);}else select(e.d.id,e.d.buildable?'build':'providers');}
    function chooseSet(id,ranges,anchor){if(!id)return;if(selected!==id){selectionChanging=true;try{select(id,(snapshot.definitions||[]).find(d=>d.id===id)?.buildable?'build':'providers');}finally{selectionChanging=false;}}selected=id;const rs=normalizeRanges(ranges);indexSelections.set(id,{ranges:rs,anchor:height(anchor)?anchor:null});selection=rs.length===0?{id,kind:'track'}:rs.length===1?{id,kind:rs[0].from===rs[0].to?'block':'range',...rs[0]}:{id,kind:'multi',from:rs[0].from,to:rs.at(-1).to};renderInspector();draw();}
    function choose(value){if(!value.id)return;chooseSet(value.id,[{from:value.from,to:value.to}],value.from);}
    function showTooltip(e,event){if(frame()){tooltip.hidden=true;return;}const s=projectHit(e,event),m=trackModel(e.d,snapshot),present=count(intersect(m.coverage,s));tooltip.textContent=m.locked?'Locked · '+(e.d.lock_reason||'Available in a later release'):s.kind==='block'?'Block '+format(s.from)+' · '+(present?'available locally':m.complete?'not stored locally':'not established locally'):'Blocks '+labelRange(s)+' · '+format(present)+' available';const rect=editor.getBoundingClientRect();tooltip.style.left=Math.max(8,Math.min(rect.width-280,event.clientX-rect.left+12))+'px';tooltip.style.top=Math.max(8,event.clientY-rect.top-42)+'px';tooltip.hidden=false;}
    const intersect=(ranges,r)=>ranges.filter(x=>x.from<=r.to&&x.to>=r.from).map(x=>({from:Math.max(r.from,x.from),to:Math.min(r.to,x.to)}));
    function zoom(factor,anchor=.5){dimensions();const v=navView(),span=v.to-v.from+1,total=navExtent()+1;if(span===1&&factor<1){descend();return;}if(frame()&&factor>1&&span===total){returnParent(hierarchy.length-2);redraw();return;}const target=Math.max(1,Math.min(total,Math.round(span*factor))),center=v.from+span*anchor;setNavView({from:Math.max(0,Math.round(center-target*anchor)),to:Math.max(0,Math.round(center-target*anchor))+target-1});redraw();}
    function pan(fraction){const v=navView(),span=v.to-v.from+1,from=Math.max(0,v.from+Math.round(span*fraction));setNavView({from,to:from+span-1});redraw();}
    const clamp=(n,min,max)=>Math.max(min,Math.min(max,n));
    function pointerAnchor(x){const rect=ruler.getBoundingClientRect();return clamp((x-rect.left)/Math.max(1,rect.width),0,1);}
    function setTrackHeight(value,anchor=.5){const old=trackHeight,pixels=trackViewport.clientHeight*anchor,point=(trackViewport.scrollTop+pixels)/old;trackHeight=clamp(value,56,220);root.style.setProperty('--timeline-track-height',trackHeight+'px');trackViewport.scrollTop=Math.max(0,point*trackHeight-pixels);save();redraw();}
    function updateScrollbars(){
      const v=navView(),max=navExtent(),span=v.to-v.from+1,total=max+1,length=horizontal.bar.clientWidth||1,minThumb=Math.min(36,length),size=Math.max(minThumb,span/total*length),travel=Math.max(0,length-size);
      horizontal.thumb.style.width=size+'px';horizontal.thumb.style.left=(total>span?v.from/(total-span)*travel:0)+'px';
      horizontal.thumb.setAttribute('aria-valuemin','0');horizontal.thumb.setAttribute('aria-valuemax',String(Math.max(0,total-span)));horizontal.thumb.setAttribute('aria-valuenow',String(v.from));horizontal.thumb.setAttribute('aria-valuetext',positionLabel(v.from)+'–'+format(v.to));
      for(const [handle,value,min,last] of [[horizontal.start,v.from,0,v.to],[horizontal.end,v.to,v.from,max]]){handle.setAttribute('aria-valuemin',String(min));handle.setAttribute('aria-valuemax',String(last));handle.setAttribute('aria-valuenow',String(value));handle.setAttribute('aria-valuetext',positionLabel(value));}
      const vh=vertical.bar.clientHeight||1,visible=trackViewport.clientHeight,totalPixels=Math.max(visible,trackViewport.scrollHeight),vsize=Math.max(Math.min(32,vh),visible/Math.max(1,totalPixels)*vh),maxScroll=Math.max(0,totalPixels-visible);
      vertical.thumb.style.height=vsize+'px';vertical.thumb.style.top=(maxScroll?trackViewport.scrollTop/maxScroll*Math.max(0,vh-vsize):0)+'px';
      vertical.thumb.setAttribute('aria-valuemin','0');vertical.thumb.setAttribute('aria-valuemax',String(maxScroll));vertical.thumb.setAttribute('aria-valuenow',String(Math.round(trackViewport.scrollTop)));vertical.thumb.setAttribute('aria-valuetext','Track height '+Math.round(trackHeight)+' pixels');
      for(const handle of [vertical.start,vertical.end]){handle.setAttribute('aria-valuemin','56');handle.setAttribute('aria-valuemax','220');handle.setAttribute('aria-valuenow',String(Math.round(trackHeight)));}
      root.dataset.trackHeight=String(Math.round(trackHeight));root.dataset.compactTracks=String(trackHeight<76);
    }
    trackViewport.addEventListener('scroll',()=>{tooltip.hidden=true;updateScrollbars();if(!picker.hidden)positionPicker();},{passive:true});
    function hideDragClick(){suppressClick=true;}
    // A fresh pointer press is a new intention, even immediately after a pinch.
    // Suppress only the compatibility click emitted by the preceding release.
    doc.addEventListener('pointerdown',()=>{if(!drag&&!pinch)suppressClick=false;},{capture:true});
    function upperScroller(){return [...inspector.querySelectorAll('.timeline-explorer-panel,.timeline-inscription-panel,.timeline-inspector-panel')].find(n=>!n.hidden)||inspector;}
    function beginDrag(event,data){if(event.button!==0)return;event.preventDefault();tooltip.hidden=true;const target=data.captureTarget||event.currentTarget;drag={...data,pointer:event.pointerId,x:event.clientX,y:event.clientY,view:{...navView()},scroll:trackViewport.scrollTop,upper:upperScroller(),upperScroll:upperScroller().scrollTop,height:trackHeight,moved:false,target};try{target.setPointerCapture(event.pointerId);}catch{};root.dataset.dragging=data.kind;}
    function endDrag(){if(drag?.moved)hideDragClick();const prior=drag;drag=null;delete root.dataset.dragging;if(prior)try{prior.target.releasePointerCapture(prior.pointer);}catch{};}
    for(const scroll of [horizontal,vertical]){
      scroll.bar.addEventListener('pointerdown',event=>{
        const edge=event.target.closest('[data-edge]')?.dataset.edge;
        if(!scroll.thumb.contains(event.target)){if(scroll.axis==='horizontal')pan(event.clientX<scroll.thumb.getBoundingClientRect().left?-.8:.8);else trackViewport.scrollTop+=(event.clientY<scroll.thumb.getBoundingClientRect().top?-1:1)*trackViewport.clientHeight*.8;return;}
        beginDrag(event,{kind:scroll.axis,edge,bar:scroll.bar.getBoundingClientRect(),thumb:scroll.thumb.getBoundingClientRect(),scrollHeight:trackViewport.scrollHeight,viewportHeight:trackViewport.clientHeight});
      });
      scroll.thumb.addEventListener('keydown',event=>{
        const edge=event.target.dataset.edge,back=['ArrowLeft','ArrowUp'].includes(event.key),forward=['ArrowRight','ArrowDown'].includes(event.key);if(!back&&!forward&&event.key!=='Home'&&event.key!=='End')return;event.preventDefault();
        if(scroll.axis==='horizontal'){
          const v=navView(),max=navExtent(),step=Math.max(1,Math.round((v.to-v.from+1)/20)),delta=(back?-1:1)*step;
          if(edge){const from=edge==='start'?clamp(event.key==='Home'?0:event.key==='End'?v.to:v.from+delta,0,v.to):v.from,to=edge==='end'?clamp(event.key==='Home'?v.from:event.key==='End'?max:v.to+delta,v.from,max):v.to;setNavView({from,to});redraw();}
          else if(event.key==='Home'||event.key==='End'){const span=v.to-v.from+1;setNavView(event.key==='Home'?{from:0,to:span-1}:{from:max-span+1,to:max});redraw();}else pan(back?-.1:.1);
        }else if(edge)setTrackHeight(event.key==='Home'?56:event.key==='End'?220:trackHeight+(back?-8:8),edge==='start'?1:0);
        else trackViewport.scrollTop=event.key==='Home'?0:event.key==='End'?trackViewport.scrollHeight:trackViewport.scrollTop+(back?-1:1)*trackHeight;
      });
    }
    const nativeControl=target=>target?.closest?.('input,textarea,select,button,a,summary,[contenteditable="true"],[role="slider"],[role="scrollbar"]');
    editor.addEventListener('pointerenter',()=>{hovering=true;});editor.addEventListener('pointerleave',()=>{hovering=false;});
    doc.addEventListener('keydown',event=>{
      if(event.key==='Escape'){if(!picker.hidden){event.preventDefault();picker.hidden=true;addButton.setAttribute('aria-expanded','false');addButton.focus({preventScroll:true});}spaceHeld=false;spaceDragged=true;endDrag();pinch=null;pointers.clear();delete root.dataset.hand;return;}
      if(event.code!=='Space'||nativeControl(event.target)&&event.target!==ruler&&event.target!==compactScrubber||!(editor.contains(event.target)||hovering&&(event.target===doc.body||event.target===doc.documentElement)))return;event.preventDefault();if(!event.repeat){spaceHeld=true;spaceDragged=false;root.dataset.hand='true';tooltip.hidden=true;}
    });
    doc.addEventListener('keyup',event=>{if(event.code!=='Space'||!spaceHeld)return;event.preventDefault();const use=!spaceDragged&&!drag?.moved;spaceHeld=false;endDrag();delete root.dataset.hand;if(use&&event.target?.matches('.timeline-lane'))activateLane(entries.get(event.target.dataset.index));});
    doc.defaultView.addEventListener('blur',()=>{spaceHeld=false;spaceDragged=true;endDrag();pinch=null;pointers.clear();delete root.dataset.hand;});
    editor.addEventListener('click',event=>{if(suppressClick){event.preventDefault();event.stopImmediatePropagation();suppressClick=false;}},{capture:true});
    function panePointerDown(event){
      if(picker.contains(event.target))return;
      const isRuler=ruler.contains(event.target),isCompact=compactScrubber===event.target;
      if(nativeControl(event.target)&&!isRuler&&!isCompact)return;
      if(isRuler&&!spaceHeld&&event.pointerType!=='touch'){if(!selected){const d=ordered()[0];if(d)select(d.id,'providers');}event.currentTarget.focus({preventScroll:true});beginDrag(event,{kind:'scrub'});focusPosition(heightAt(event.clientX),false);return;}
      if(event.pointerType==='touch'){
        pointers.set(event.pointerId,{x:event.clientX,y:event.clientY});try{event.currentTarget.setPointerCapture(event.pointerId);}catch{}
        if(pointers.size===1){beginDrag(event,{kind:isRuler?'scrub':'touch-pan',laneWidth:ruler.clientWidth||1});if(isRuler){event.currentTarget.focus({preventScroll:true});focusPosition(heightAt(event.clientX),false);}}
        if(pointers.size===2){event.preventDefault();endDrag();const [a,b]=[...pointers.values()],mid=(a.x+b.x)/2,v=navView();pinch={distance:Math.max(1,Math.hypot(a.x-b.x,a.y-b.y)),anchor:v.from+(v.to-v.from+1)*pointerAnchor(mid),span:v.to-v.from+1,midY:(a.y+b.y)/2,scroll:trackViewport.scrollTop};hideDragClick();}
      }else if(spaceHeld){beginDrag(event,{kind:'hand',laneWidth:ruler.clientWidth||1});}
      else if(!event.shiftKey&&!event.ctrlKey&&!event.metaKey&&(isCompact||event.target.closest('.timeline-track-body')||event.target===trackViewport||event.target===tracks)){
        const entry=entries.get(event.target.closest('.timeline-track')?.dataset.index);(entry?.canvas||compactScrubber).focus({preventScroll:true});beginDrag(event,{kind:'hand',plain:true,entry,laneWidth:ruler.clientWidth||1});
      }
    }
    trackArea.addEventListener('pointerdown',panePointerDown);ruler.addEventListener('pointerdown',panePointerDown);compactScrubber.addEventListener('pointerdown',panePointerDown);
    doc.addEventListener('pointermove',event=>{
      if(pointers.has(event.pointerId)){pointers.set(event.pointerId,{x:event.clientX,y:event.clientY});if(pinch&&pointers.size===2){event.preventDefault();const [a,b]=[...pointers.values()],distance=Math.max(1,Math.hypot(a.x-b.x,a.y-b.y)),rawSpan=pinch.span*pinch.distance/distance,span=clamp(Math.round(rawSpan),1,navExtent()+1),from=Math.max(0,Math.round(pinch.anchor-span*pointerAnchor((a.x+b.x)/2)));if(pinch.span===1&&rawSpan<.65&&!hierarchyBusy){pinch=null;pointers.clear();descend();hideDragClick();return;}setNavView({from,to:from+span-1});trackViewport.scrollTop=pinch.scroll-((a.y+b.y)/2-pinch.midY);redraw();hideDragClick();return;}}
      if(!drag||drag.pointer!==event.pointerId)return;event.preventDefault();const dx=event.clientX-drag.x,dy=event.clientY-drag.y;if(Math.hypot(dx,dy)>(drag.plain?4:2))drag.moved=true;
      if(drag.kind==='scrub'){focusPosition(heightAt(event.clientX),false);return;}
      if(drag.kind==='selection'){const block=heightAt(event.clientX),picks=drag.ranges.map(r=>({...r})),r=picks[drag.rangeIndex],original=drag.ranges[drag.rangeIndex];if(drag.edge==='single')r.from=r.to=block;else if(drag.edge==='in')r.from=Math.min(block,r.to);else r.to=Math.max(block,r.from);drag.moved=drag.moved||r.from!==original.from||r.to!==original.to;chooseSet(drag.id,picks,indexSelections.get(drag.id)?.anchor);return;}
      if(drag.kind==='hand'||drag.kind==='touch-pan'){
        if(drag.plain&&!drag.moved)return;
        spaceDragged=spaceDragged||drag.moved;const span=drag.view.to-drag.view.from+1,from=Math.max(0,drag.view.from-Math.round(dx/drag.laneWidth*span));setNavView({from,to:from+span-1});if(root.dataset.timelineCollapsed==='true')drag.upper.scrollTop=drag.upperScroll-dy;else trackViewport.scrollTop=drag.scroll-dy;
      }else if(drag.kind==='horizontal'){
        if(drag.edge){const delta=Math.round(dx/Math.max(1,drag.bar.width)*(navExtent()+1));setNavView(drag.edge==='start'?{from:clamp(drag.view.from+delta,0,drag.view.to),to:drag.view.to}:{from:drag.view.from,to:clamp(drag.view.to+delta,drag.view.from,navExtent())});}
        else {const span=drag.view.to-drag.view.from+1,delta=Math.round(dx/Math.max(1,drag.bar.width-drag.thumb.width)*(navExtent()+1-span)),from=Math.max(0,drag.view.from+delta);setNavView({from,to:from+span-1});}
      }else if(drag.kind==='vertical'){
        if(drag.edge){const first=drag.scroll/drag.height,last=(drag.scroll+drag.viewportHeight)/drag.height,delta=dy/Math.max(1,drag.bar.height)*Math.max(entries.size,drag.viewportHeight/drag.height),visible=drag.edge==='start'?last-clamp(first+delta,0,last-.2):Math.max(.2,last+delta-first);trackHeight=clamp(drag.viewportHeight/visible,56,220);root.style.setProperty('--timeline-track-height',trackHeight+'px');trackViewport.scrollTop=drag.edge==='start'?Math.max(0,last*trackHeight-drag.viewportHeight):first*trackHeight;save();}
        else trackViewport.scrollTop=drag.scroll+dy/Math.max(1,drag.bar.height-drag.thumb.height)*Math.max(0,drag.scrollHeight-drag.viewportHeight);
      }redraw();
    },{passive:false});
    function finishPointer(event){const released=event.type==='pointerup'&&drag?.pointer===event.pointerId?drag:null,markerClick=released?.kind==='selection'&&!released.moved?released:null,plainClick=released?.plain&&!released.moved?released:null;if(pointers.has(event.pointerId)){pointers.delete(event.pointerId);if(pinch){pinch=null;hideDragClick();}}if(drag?.pointer===event.pointerId)endDrag();if(plainClick){if(plainClick.entry)hit(plainClick.entry,event);else focusPosition(heightAt(event.clientX),true);hideDragClick();}if(markerClick){const pick=markerClick.ranges[markerClick.rangeIndex];if(pick){hideDragClick();if(selected!==markerClick.id)select(markerClick.id,(snapshot.definitions||[]).find(d=>d.id===markerClick.id)?.buildable?'build':'providers');focusBlock(markerClick.edge==='out'?pick.to:pick.from,true);}}}
    doc.addEventListener('pointerup',finishPointer);doc.addEventListener('pointercancel',finishPointer);
    editor.addEventListener('lostpointercapture',event=>{if(drag?.pointer===event.pointerId)finishPointer(event);});
    function wheel(event){
      if(picker.contains(event.target)){if(event.ctrlKey||event.metaKey)event.preventDefault();return;}
      if(event.target.closest?.('input,textarea,select,[contenteditable="true"]'))return;event.preventDefault();tooltip.hidden=true;if(rulerLabel.contains(event.target))return;const unit=event.deltaMode===1?16:event.deltaMode===2?Math.max(1,trackViewport.clientHeight):1,dx=event.deltaX*unit,dy=event.deltaY*unit;
      if(event.ctrlKey||event.metaKey)zoom(Math.exp(clamp(dy,-300,300)*.006),pointerAnchor(event.clientX));
      else if(event.altKey){const rect=trackViewport.getBoundingClientRect();setTrackHeight(trackHeight*Math.exp(clamp(dy,-300,300)*-.004),clamp((event.clientY-rect.top)/Math.max(1,rect.height),0,1));}
      else if(event.shiftKey||dx)pan((event.shiftKey?(dy||dx):dx)/Math.max(1,ruler.clientWidth));
      else if(event.target.closest('.timeline-track-heading')||vertical.bar.contains(event.target)){trackViewport.scrollTop+=dy;updateScrollbars();}
      else if(dy)zoom(Math.exp(clamp(dy,-300,300)*.006),pointerAnchor(event.clientX));
    }
    trackArea.addEventListener('wheel',wheel,{passive:false});rulerRow.addEventListener('wheel',wheel,{passive:false});compactScrubber.addEventListener('wheel',wheel,{passive:false});horizontal.bar.addEventListener('wheel',wheel,{passive:false});
    function focusSelection(){returnParent(-1);const m=current();if(!m)return;const rs=selectedRanges(),r=rs.length?{from:rs[0].from,to:rs.at(-1).to}:m.coverage.length?{from:m.coverage[0].from,to:m.coverage.at(-1).to}:null;if(!r)return;const padding=r.to===r.from?4:Math.max(1,Math.round((r.to-r.from+1)*.04));view=clampView({from:Math.max(0,r.from-padding),to:Math.min(extent,r.to+padding)},extent);fit=false;redraw();}
    function setMeta(values){meta.replaceChildren();for(const [label,value] of values){const cell=el('div',undefined,'timeline-stat');cell.append(el('span',label),el('strong',value));meta.append(cell);}}
    function changeMarker(id,rangeIndex,edge,block){const picks=selectedRanges(id),r=picks[rangeIndex];if(!r)return;block=clamp(block,0,extent);if(edge==='single')r.from=r.to=block;else if(edge==='in')block=r.from=Math.min(block,r.to);else block=r.to=Math.max(block,r.from);if(block<view.from||block>view.to){const span=view.to-view.from+1;view=clampView({from:Math.max(0,block-Math.floor(span/2)),to:Math.max(0,block-Math.floor(span/2))+span-1},extent);fit=false;}pendingMarkerFocus={id,height:block,edge};chooseSet(id,picks,indexSelections.get(id)?.anchor);}
    function selectionMarker(e,rangeIndex,pick,edge,value,span){
      const marker=button('',null,(edge==='single'?'Selected block':edge==='in'?'In point':'Out point')+' · block '+format(value),()=>{});marker.className='timeline-selection-marker '+edge;marker.dataset.height=String(value);marker.dataset.range=String(rangeIndex);marker.dataset.edge=edge;marker.style.left=((value-view.from+(edge==='out'?1:.5))/span*100)+'%';marker.setAttribute('role','slider');marker.setAttribute('aria-orientation','horizontal');marker.setAttribute('aria-valuemin',String(edge==='out'?pick.from:0));marker.setAttribute('aria-valuemax',String(edge==='in'?pick.to:extent));marker.setAttribute('aria-valuenow',String(value));
      marker.addEventListener('pointerdown',event=>{event.stopPropagation();marker.focus({preventScroll:true});if(event.shiftKey||event.ctrlKey||event.metaKey)return;beginDrag(event,{kind:'selection',id:e.d.id,rangeIndex,edge,ranges:selectedRanges(e.d.id),captureTarget:trackArea});});
      marker.addEventListener('click',event=>{event.preventDefault();event.stopPropagation();if(event.shiftKey||event.ctrlKey||event.metaKey){const prior=indexSelections.get(e.d.id)||{ranges:[],anchor:null},next=selectBlock(prior.ranges,prior.anchor,value,{extend:event.shiftKey,toggle:event.ctrlKey||event.metaKey});chooseSet(e.d.id,next.ranges,next.anchor);}else{if(selected!==e.d.id)select(e.d.id,e.d.buildable?'build':'providers');focusBlock(value,true);}});
      marker.addEventListener('keydown',event=>{if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return;event.preventDefault();event.stopPropagation();changeMarker(e.d.id,rangeIndex,edge,event.key==='Home'?0:event.key==='End'?extent:value+(event.key==='ArrowLeft'?-1:1));});return marker;
    }
    function renderInspector() {
      const m=current();if(!m){title.textContent='Select an index';advanced.hidden=true;controls.hidden=true;selectionList.hidden=true;return;}
      const s=selection||{id:selected,kind:'track'},d=m.definition,isTrack=s.kind==='track';
      inspector.dataset.index=selected;inspector.dataset.kind=s.kind;eyebrow.textContent=isTrack?'TRACK INSPECTOR':s.kind==='block'?'BLOCK INSPECTOR':'RANGE INSPECTOR';
      const picks=selectedRanges();title.textContent=isTrack?d.name:s.kind==='multi'?format(picks.length)+' selected ranges':s.kind==='block'?'Block '+format(s.from):'Blocks '+labelRange(s);
      status.textContent=!healthy?'Status unavailable':m.locked?'Locked':d.id==='headers'?(snapshot.timeline?.syncing?'Syncing headers':m.total?'Headers available':'Waiting for headers'):m.state;
      status.dataset.locked=String(m.locked);subtitle.textContent=isTrack?d.theory||'Local index coverage':d.name+' · '+(m.locked?'Locked':s.kind==='block'?'Inspect this position in the chain.':'An exact block-height selection.');
      const coverageRanges=isTrack?m.coverage:normalizeRanges(picks.flatMap(r=>intersect(m.coverage,r))),available=count(coverageRanges),length=isTrack?extent+1:count(picks);
      setMeta(isTrack?[['Available locally',blockCount(m.total)+(m.complete?'':' · mapped so far')],['Separate ranges',format(m.coverage.length)],['Local header tip',format(snapshot.timeline?.tip_height)]]:[['Selection',blockCount(length)],['Available locally',format(available)+' / '+format(length)],['Coverage',available===length?'Complete':available?'Partial':m.complete?'Not stored':'Not established']]);
      const stateTrack=d.id==='utxo-state';controls.hidden=stateTrack;switches.hidden=!isTrack||!d.buildable||m.locked||stateTrack;
      on.input.checked=m.on;live.input.checked=m.live;on.input.disabled=live.input.disabled=!healthy||busy||m.locked||!d.buildable;
      const browsePreference=(snapshot.browse||[]).find(p=>p.index===d.id),browseSupported=['tx-locator','inscriptions'].includes(d.id)&&browsePreference?.available===true;
      browse.label.hidden=!browseSupported;browse.input.checked=Boolean(browsePreference?.enabled);browse.input.disabled=!healthy||busy||m.locked||!browseSupported;browse.input.setAttribute('aria-label',d.id+' Index as you browse');
      on.input.setAttribute('aria-label',d.id+' On');live.input.setAttribute('aria-label',d.id+' Live');
      on.label.title='Allow indexing work. Turning off keeps saved records.';live.label.title='Catch up from saved progress and follow new blocks. This may fetch many blocks.';
      focus.disabled=m.locked||isTrack&&!m.coverage.length&&!picks.length;focus.hidden=m.locked;inspect.hidden=!!onFocus||s.kind!=='block'||m.locked;inspect.disabled=!healthy;
      build.hidden=stateTrack||isTrack||!d.buildable||m.locked;build.disabled=!healthy||busy||picks.length!==1;build.textContent=picks.length>1?'Choose a range below':'Build this range';
      const selectionKey=JSON.stringify([selected,picks,healthy,busy,d.buildable,m.locked]);selectionList.hidden=stateTrack||!picks.length;if(selectionList.dataset.key!==selectionKey){selectionList.dataset.key=selectionKey;selectionList.replaceChildren();selectionList.append(el('p','Selected indexing ranges','timeline-selected-ranges-label'));for(const [i,r] of picks.entries()){const item=el('div',undefined,'timeline-selected-range');item.append(el('span',r.from===r.to?'Block '+format(r.from):'Blocks '+labelRange(r)));if(d.buildable&&!m.locked&&!stateTrack){const prepare=button('Prepare this range',null,'Prepare blocks '+labelRange(r)+' for review',()=>prepareRange(r));prepare.dataset.range=String(i);prepare.disabled=!healthy||busy;item.append(prepare);}selectionList.append(item);}}
      const blockDefinition=(snapshot.definitions||[]).find(d=>d.id==='blocks');
      const bodyAvailable=s.kind==='block'&&blockDefinition&&trackModel(blockDefinition,snapshot).coverage.some(r=>r.from<=s.from&&r.to>=s.from);
      if(s.kind==='block'&&!bodyAvailable)inspect.textContent='Fetch & open in Explorer';else inspect.textContent='Open in Explorer';
      advanced.hidden=m.locked;summary.textContent=stateTrack?'UTXO build & state':d.buildable?'Settings, jobs & records':'Header data sources & details';if(stateTrack)advanced.open=true;if(workspace)workspace.hidden=m.locked;
      notice.textContent=m.locked?d.lock_reason||'This index is locked while Headers and Blocks are tested.':!healthy?'Showing the last known coverage. Controls are paused until local status is available.':d.id==='headers'?'Headers link the chain together. They do not contain the transactions inside each block.':d.id==='blocks'?'Filled ranges show block files available on this computer. Processing a block does not always keep its source file. Files are checked again when opened.':m.note||'Filled ranges show committed local results. They do not imply that source block files are retained.';
      if(!m.locked&&!m.complete)notice.textContent+=' Coverage is partial: blank areas may contain data that has not been mapped yet.';
      if(isTrack&&d.buildable&&!m.locked)notice.textContent+=m.retained?' Following new blocks continues from saved progress.':' Follow new blocks catches up all applicable history from block '+format(m.initialFrom)+'. It may download many source blocks.';
      if(m.stale)notice.textContent+=' Chain verification needs attention; inspect the source details before relying on saved results.';
      if(s.kind==='block'&&!bodyAvailable&&!m.locked)notice.textContent+=' Viewing this block in Explorer may fetch it from your configured sources.';
      if(picks.length>1)notice.textContent+=' These ranges remain separate. Prepare and review each range individually before starting indexing.';
      const errors=snapshot.timeline?.errors||[];if(errors.length)notice.textContent+=' '+errors.join(' ');
      const signature=JSON.stringify([selected,m.coverage,view]);if(coverageList.dataset.key!==signature){coverageList.dataset.key=signature;coverageList.replaceChildren();coverageSummary.textContent='Exact coverage · '+format(m.coverage.length)+' range'+(m.coverage.length===1?'':'s');const rs=intersect(m.coverage,view);for(const r of rs.slice(0,100)){const b=button(labelRange(r),null,'Select blocks '+labelRange(r),()=>choose({id:selected,kind:r.from===r.to?'block':'range',...r}));b.disabled=m.locked;coverageList.append(b);}if(!rs.length)coverageList.append(el('p',m.complete?'No local coverage in this view.':'No established coverage in this view.'));if(rs.length>100)coverageList.append(el('p','Showing the first 100 visible ranges. Zoom in to inspect the rest.'));}
    }
    function draw() {
      const active=doc.activeElement,restore=pendingMarkerFocus||active?.matches?.('.timeline-selection-marker')&&{id:active.closest('.timeline-track')?.dataset.index,height:Number(active.dataset.height),edge:active.dataset.edge};pendingMarkerFocus=null;
      dimensions();paintHierarchy();ruler.replaceChildren();const p=frame(),v=navView(),span=v.to-v.from+1,position=p?p.focused:playhead;
      const marks=p?rulerMarks(v,ruler.clientWidth||800).filter(m=>m.kind==='regular'):rulerMarks(v,ruler.clientWidth||800),ticks=new Set(marks.filter(m=>m.kind==='regular').map(m=>m.height));
      for(const mark of marks){const tick=el('span',mark.label,'timeline-ruler-tick '+mark.kind);tick.dataset.height=String(mark.height);tick.title=p?positionLabel(mark.height):mark.detail||'Block '+format(mark.height);tick.style.left=((mark.height-v.from+.5)/span*100)+'%';ruler.append(tick);}
      ruler.setAttribute('aria-valuemin','0');ruler.setAttribute('aria-valuemax',String(navExtent()));ruler.setAttribute('aria-valuenow',String(position??v.from));ruler.setAttribute('aria-valuetext',position===null?'Choose a position':positionLabel(position));
      compactPosition.textContent=position===null?'Choose a position':positionLabel(position);drawPositionLane(compactScrubber);
      if(position!==null&&position>=v.from&&position<=v.to){const marker=el('span',undefined,'timeline-playhead-marker');marker.dataset.height=String(position);marker.title='Playhead · '+positionLabel(position);marker.style.left=((position-v.from+.5)/span*100)+'%';marker.setAttribute('aria-hidden','true');ruler.append(marker);}
      const tip=snapshot.timeline?.tip_height;viewportLabel.textContent=format(v.from)+'–'+format(v.to)+' · '+(p?format(span)+' positions':blockCount(span))+(fit&&!p?' · Fit chain':' · Focused view');
      fitButton.setAttribute('aria-pressed',String(fit&&!p));left.disabled=v.from===0;right.disabled=v.to===navExtent();
      root.dataset.from=String(v.from);root.dataset.to=String(v.to);root.dataset.fit=String(fit&&!p);
      for(const e of entries.values()) {
        if(p){
          const containing=['headers','blocks'].includes(e.d.id),supported=e.d.id==='tx-locator'&&p.kind!=='output',reveals=e.d.id==='inscriptions'&&p.kind==='block';
          e.row.dataset.hierarchySupported=String(supported||reveals);e.canvas.hidden=!(supported||reveals||containing);e.placeholder.hidden=supported||reveals||containing;e.placeholder.textContent=supported||reveals||containing?'':'Block '+format(p.height)+' · This track has no detail at this depth';e.markers.replaceChildren();
          if(containing)drawParentLane(e);else if(reveals)drawRevealLane(e);else if(supported){drawPositionLane(e.canvas,e);e.canvas.setAttribute('aria-label',e.d.name+' · '+positionLabel(v.from)+' to '+positionLabel(v.to));}continue;
        }
        delete e.row.dataset.hierarchySupported;e.canvas.hidden=false;e.placeholder.textContent=e.d.locked?(e.d.lock_reason||'Available in a later release'):'';e.placeholder.hidden=!e.d.locked;
        const m=trackModel(e.d,snapshot),canvas=e.canvas,rect=canvas.getBoundingClientRect(),w=Math.max(1,rect.width),h=trackHeight,cy=h/2,band=Math.min(42,h*.4),top=cy-band/2,dpr=Math.min(2,doc.defaultView?.devicePixelRatio||1);
        canvas.width=Math.round(w*dpr);canvas.height=h*dpr;canvas.dataset.from=String(view.from);canvas.dataset.to=String(view.to);canvas.dataset.coverage=JSON.stringify(m.coverage);canvas.setAttribute('aria-label',e.d.name+' timeline, blocks '+labelRange(view)+', '+format(m.total)+' locally available'+(m.locked?', locked':''));
        const ctx=canvas.getContext('2d');if(!ctx)continue;ctx.scale(dpr,dpr);
        const dark=doc.documentElement.dataset.theme==='dark',colors={grid:dark?'#284139':'#dfdfd5',ghost:dark?'#26332f':'#e9ebe3',fill:trackColor(e.d.id,dark),partial:dark?'#dcb97c':'#86682d',ink:dark?'#e8f0e8':'#233e30',selection:dark?'#d9edb8':'#335c41',playhead:dark?'#80baff':'#235fa1',job:dark?'#c8ac74':'#9a7435'};
        const potential=m.potentialFrom===null||extent<m.potentialFrom?[]:intersect([{from:m.potentialFrom,to:extent}],view);
        e.canvas.dataset.potential=JSON.stringify(potential);
        ctx.fillStyle=colors.ghost;for(const range of potential)ctx.fillRect((range.from-view.from)/span*w,top,(range.to-range.from+1)/span*w,band);
        ctx.fillStyle=colors.grid;for(const tick of ticks){const x=(tick-view.from+.5)/span*w;ctx.fillRect(Math.floor(x),0,1,h);}
        for(const mark of marks.filter(m=>m.kind!=='regular')){ctx.globalAlpha=mark.kind==='halving'?.65:.35;ctx.fillStyle=colors.ink;ctx.fillRect(Math.floor((mark.height-view.from+.5)/span*w),0,mark.kind==='halving'?2:1,h);}ctx.globalAlpha=1;
        if(!healthy)ctx.globalAlpha=.45;
        for(const bin of coverageBins(m.coverage,view,w)) {
          if(m.locked)break;
          const full=bin.count===bin.total;ctx.fillStyle=full?colors.fill:colors.partial;
          ctx.fillRect(bin.x,top,Math.max(.8,bin.width),band);
          if(!full){ctx.fillStyle=dark?'#182d26':'#fffdf8';ctx.fillRect(bin.x,top+band*.2,Math.max(.8,bin.width),4);ctx.fillRect(bin.x,top+band*.7,Math.max(.8,bin.width),4);}
        }
        const job=m.job;if(!m.locked&&job&&['running','waiting','queued','pausing','paused','catching_up'].includes(job.state)&&height(job.from)&&height(job.to)&&job.from<=view.to&&job.to>=view.from){const x=(Math.max(view.from,job.from)-view.from)/span*w,end=(Math.min(view.to,job.to)+1-view.from)/span*w;ctx.strokeStyle=colors.job;ctx.setLineDash([4,4]);ctx.strokeRect(x+.5,top-8,Math.max(1,end-x-1),band+16);ctx.setLineDash([]);}
        if(span<=64&&!m.locked){for(let i=0;i<span;i++){const x=i/span*w,bw=w/span;ctx.fillStyle=colors.grid;ctx.fillRect(x,top-2,1,band+4);if(bw>42){const block=view.from+i;ctx.fillStyle=m.coverage.some(r=>r.from<=block&&r.to>=block)?(dark?'#182d26':'#ffffff'):colors.ink;ctx.font='10px ui-monospace, monospace';ctx.textAlign='center';ctx.fillText(String(block),x+bw/2,cy+3,Math.max(1,bw-6));}}}
        ctx.globalAlpha=1;
        if(height(tip)&&tip>=view.from&&tip<=view.to){const x=(tip+1-view.from)/span*w;ctx.strokeStyle=colors.fill;ctx.setLineDash([2,3]);ctx.beginPath();ctx.moveTo(Math.min(w-1,x),4);ctx.lineTo(Math.min(w-1,x),h-4);ctx.stroke();ctx.setLineDash([]);}
        const picks=selectedRanges(e.d.id);canvas.dataset.selection=JSON.stringify(picks);e.markers.replaceChildren();
        for(const [rangeIndex,pick] of picks.entries()){if(pick.from>view.to||pick.to<view.from)continue;const x=(Math.max(view.from,pick.from)-view.from)/span*w,end=(Math.min(view.to,pick.to)+1-view.from)/span*w;ctx.strokeStyle=colors.selection;ctx.lineWidth=e.d.id===selected?2:1;ctx.globalAlpha=e.d.id===selected?1:.6;ctx.strokeRect(x+1,8,Math.max(1,end-x-2),h-16);ctx.globalAlpha=1;for(const [edge,value] of pick.from===pick.to?[['single',pick.from]]:[['in',pick.from],['out',pick.to]]){if(value<view.from||value>view.to)continue;e.markers.append(selectionMarker(e,rangeIndex,pick,edge,value,span));}}
        if(playhead!==null&&playhead>=view.from&&playhead<=view.to){const x=(playhead-view.from+.5)/span*w;ctx.strokeStyle=colors.playhead;ctx.lineWidth=1.5;ctx.beginPath();ctx.moveTo(x,0);ctx.lineTo(x,h);ctx.stroke();}
      }
      drawGroups();ensureRevealMarkers();renderInspector();updateScrollbars();if(!picker.hidden)positionPicker();
      if(restore){const entry=entries.get(restore.id),reference=nearestMarker(selectedRanges(restore.id),restore),next=reference&&entry?.markers.querySelector('[data-height="'+reference.height+'"][data-edge="'+reference.edge+'"]');(next||entry?.canvas)?.focus({preventScroll:true});}
    }
    function redraw(){tooltip.hidden=true;draw();}
    function cancelPendingNavigation(){cancelHierarchy();endDrag();pinch=null;pointers.clear();spaceHeld=false;delete root.dataset.hand;hierarchyError='';paintHierarchy();}
    function validateFocusedAnchor(){const p=frame();if(!p||!loadHierarchy||hierarchyBusy)return;validationRequest?.abort();const controller=doc.defaultView.AbortController?new doc.defaultView.AbortController():null,ticket=hierarchySerial;validationRequest=controller;Promise.resolve(loadHierarchy(p,{signal:controller?.signal})).then(metadata=>{if(ticket===hierarchySerial&&frame()===p)acceptResource({kind:p.kind,height:p.height},metadata);}).catch(error=>{if(ticket===hierarchySerial&&error.name!=='AbortError'){invalidateAnchor(p.height,'Current block evidence is unavailable. Child positions need revalidation.');redraw();}}).finally(()=>{if(validationRequest===controller)validationRequest=null;});}
    function render(value=snapshot) {
      const tip=value.timeline?.tip_hash,nextHeight=value.timeline?.tip_height,changed=Boolean(tip&&observedTip&&tip!==observedTip),stale=JSON.stringify((value.instances||[]).filter(i=>/stale_reorg|anchor_unavailable/.test(i.verification||'')).map(i=>[i.definition,i.verification,i.checkpoint]));if(stale!=='[]'&&stale!==observedStale||height(tipHeight)&&height(nextHeight)&&nextHeight<tipHeight){const currentHeight=frame()?.height;if(height(currentHeight))invalidateAnchor(currentHeight);hierarchyCache.clear();revealCache.clear();resourceAnchors.clear();}else if(changed)validateFocusedAnchor();observedStale=stale;if(tip)observedTip=tip;if(height(nextHeight))tipHeight=nextHeight;
      const revealKey=JSON.stringify((value.instances||[]).filter(i=>i.definition==='inscriptions').map(i=>[i.checkpoint,i.coverage,i.verification]));if(revealKey!==revealSnapshotKey){revealSnapshotKey=revealKey;closeRevealPicker();revealCache.clear();revealSerial++;revealRequest?.abort();revealRequest=null;}snapshot=value;dimensions();const defs=ordered(),ids=new Set(defs.map(d=>d.id));
      for(const [id,e] of entries)if(!ids.has(id)){e.row.remove();entries.delete(id);}
      for(const [i,d] of defs.entries()){const e=entries.get(d.id)||makeTrack(d),m=trackModel(d,snapshot);e.d=d;e.number.textContent=String(i+1).padStart(2,'0');e.name.textContent=d.name;e.head.setAttribute('aria-pressed',String(d.id===selected));e.head.setAttribute('aria-label','Select '+d.name+(m.locked?' (locked)':''));e.row.dataset.selected=String(d.id===selected);e.row.dataset.locked=String(m.locked);e.state.textContent=!healthy?'Status unavailable':m.locked?'Locked':d.id==='headers'?(snapshot.timeline?.syncing?'Syncing':m.total?'Foundation':'Waiting'):m.state;e.detail.textContent=m.locked?'Available in a later release':blockCount(m.total)+(m.complete?'':' mapped')+' · '+format(m.coverage.length)+' range'+(m.coverage.length===1?'':'s');e.placeholder.textContent=m.locked?(d.lock_reason||'Locked · Available in a later release'):'';e.placeholder.hidden=!m.locked;e.head.title=m.locked?d.lock_reason||'Locked':d.theory||d.name;}
      if(tracks.lastElementChild!==addRow)tracks.append(addRow);renderGroups(defs);
      empty.hidden=defs.length>0;root.dataset.healthy=String(healthy);renderPicker();draw();
    }
    async function change(control,checked) {
      const m=current();if(!m||!healthy||busy||m.locked){renderInspector();return;}
      if(control==='browse'){const id=m.definition.id;if(!['tx-locator','inscriptions'].includes(id))return;busy=true;renderInspector();try{const result=await api('browse',{index:id,enabled:checked});snapshot={...snapshot,browse:[...(snapshot.browse||[]).filter(p=>p.index!==id),result]};notify(checked?'Verified '+m.definition.name+' results will be saved as you browse.':'Browsing no longer saves '+m.definition.name+' results. Existing records remain.');await refresh();}catch(error){notify(error.message,true);}finally{busy=false;render();}return;}
      const id=m.definition.owner||m.definition.id;busy=true;renderInspector();try{const action=control==='on'?(checked?'start':'stop'):(checked?'enable':'disable'),result=await api('live',{index:id,action});snapshot={...snapshot,live:[...(snapshot.live||[]).filter(p=>p.index!==id),result]};notify(control==='live'?(checked?'Following new blocks. Gateway will catch up from saved progress.':'Following new blocks is off. Saved data remains available.'):(checked?'Indexing is on. Choose a range, or follow new blocks.':'Indexing stopped. Saved data remains available.'));await refresh();}catch(error){notify(error.message,true);}finally{busy=false;render();}
    }
    const Resize=doc.defaultView?.ResizeObserver;if(Resize){const observer=new Resize(()=>{if(!drawing){drawing=true;doc.defaultView.requestAnimationFrame(()=>{drawing=false;draw();});}});observer.observe(ruler);observer.observe(trackViewport);}
    const Mutation=doc.defaultView?.MutationObserver;if(Mutation)new Mutation(()=>draw()).observe(doc.documentElement,{attributes:true,attributeFilter:['data-theme']});
    setCollapsed(true,false);
    return {cancelPendingNavigation,acceptResource,syncResource,zoomIntoFocus,returnParent,setCollapsed,getHierarchy:()=>hierarchy.map(p=>({kind:p.kind,height:p.height,txIndex:p.txIndex,outputIndex:p.outputIndex,count:p.count,view:{...p.view},focused:p.focused})),render,health:ok=>{healthy=ok;render();},select:id=>{id=id==='txo-spender'?'txio-links':id;const changed=selected!==id;selected=id;selection=id?{id,kind:'track'}:null;if(changed)advanced.open=false;render();if(changed&&id==='utxo-state')doc.getElementById('timeline-inspector-panel')?.scrollTo({top:0});if(id&&id!=='utxo-state'&&changed&&!frame()&&!selectionChanging&&playhead!==null&&healthy&&!current()?.locked)onFocus?.({index:definitionFor(id)?.owner||id,height:playhead,immediate:false});},model:id=>trackModel(definitionFor(id)||{id},snapshot),getView:()=>({...navView(),fit:fit&&!frame()}),getSelection:id=>selectedRanges(id).map(r=>({...r})),getPlayhead:()=>playhead,focus:focusHeight,removeTrack};
  }
  return {normalizeRanges,subtractRanges,toggleBlock,selectBlock,selectionMarkers,nearestMarker,clampView,coverageBins,trackModel,established,rulerMarks,mount};
})();
