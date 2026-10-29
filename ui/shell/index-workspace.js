'use strict';
// Workspace geometry and draft range controls. Neither starts work or fetches data.
window.GatewayIndexWorkspace = (() => {
  const clamp = (value, min, max) => Math.max(min, Math.min(max, value));
  const height = value => Number.isSafeInteger(value) && value >= 0;
  const number = input => input.value.trim() !== '' && height(Number(input.value)) ? Number(input.value) : null;
  const format = value => value.toLocaleString('en-GB');

  function mountLayout(root, message) {
    const doc = root.ownerDocument, win = doc.defaultView;
    const inspector = root.querySelector('.timeline-inspector'), editor = root.querySelector('.timeline-editor'), upper = root.querySelector('.timeline-upper-workspace'), viewport = root.querySelector('#workspace-viewport');
    if (!inspector || !editor) return;
    if (message) inspector.prepend(message);
    editor.id = 'timeline-editor';
    const divider = doc.createElement('div');
    divider.id = 'timeline-divider'; divider.className = 'timeline-divider'; divider.tabIndex = 0;
    divider.setAttribute('role', 'separator'); divider.setAttribute('aria-orientation', 'horizontal');
    divider.setAttribute('aria-label', 'Resize details and timeline'); divider.setAttribute('aria-controls', 'timeline-inspector timeline-editor');
    divider.title = 'Drag to resize. Arrow keys adjust height; Home and End choose the limits. Double-click resets.';
    root.insertBefore(divider, editor);
    let ratio = .56, drag = null, paneDrag = null, maximized = null, paneRatios = {block:.38, inscription:.38, transaction:.56};
    try { const saved = Number(win.localStorage.getItem('gateway.timeline.panel-ratio')); if (saved > 0 && saved < 1) ratio = saved; } catch {}
    try { const saved=JSON.parse(win.localStorage.getItem('gateway.workspace.viewport-ratios')||'{}');for(const kind of Object.keys(paneRatios))if(Number.isFinite(saved[kind])&&saved[kind]>.1&&saved[kind]<.9)paneRatios[kind]=saved[kind]; } catch {}
    const paneDivider=doc.createElement('div');paneDivider.id='workspace-viewport-divider';paneDivider.className='workspace-viewport-divider';paneDivider.tabIndex=0;paneDivider.setAttribute('role','separator');paneDivider.setAttribute('aria-orientation','vertical');paneDivider.setAttribute('aria-label','Resize Details and visualization');paneDivider.setAttribute('aria-controls','timeline-inspector workspace-viewport');paneDivider.title='Drag to resize Details and visualization. Arrow keys adjust width; double-click resets.';
    if(upper&&viewport)upper.insertBefore(paneDivider,viewport);
    function paneBounds(){const width=Math.max(0,(upper?.clientWidth||root.clientWidth)-10),available=Math.max(0,upper?.clientHeight||0),kind=viewport?.dataset.resource||'block';const minimum=Math.min(150,width*.32,available),maximum=Math.max(minimum,Math.min(width-Math.min(250,width*.45),kind==='transaction'?width*.72:available));return {width,minimum,maximum,kind};}
    function paintPane(value,save=false){if(!viewport||maximized)return;const b=paneBounds(),size=clamp(value??b.width*(paneRatios[b.kind]??.38),b.minimum,b.maximum);root.style.setProperty('--workspace-viewport-size',size+'px');paneDivider.setAttribute('aria-valuemin',String(Math.round(b.minimum)));paneDivider.setAttribute('aria-valuemax',String(Math.round(b.maximum)));paneDivider.setAttribute('aria-valuenow',String(Math.round(size)));paneDivider.setAttribute('aria-valuetext','Visualization '+Math.round(size)+' pixels wide');if(save&&b.width){paneRatios[b.kind]=size/b.width;try{win.localStorage.setItem('gateway.workspace.viewport-ratios',JSON.stringify(paneRatios));}catch{}}}
    function bounds() {
      const available = Math.max(0, root.clientHeight - divider.offsetHeight);
      const minimum = Math.min(220, available * .52);
      return {available, minimum, maximum:Math.max(minimum, available - Math.min(130, available * .3))};
    }
    function paint(value, save = false) {
      if(maximized)return;
      const compact = root.dataset.timelineCollapsed === 'true';
      divider.hidden = compact;
      const b = bounds(), size = clamp(value ?? b.available * ratio, b.minimum, b.maximum);
      if (!compact) root.style.setProperty('--timeline-panel-height', size + 'px');
      paintPane();
      divider.setAttribute('aria-valuemin', String(Math.round(b.minimum)));
      divider.setAttribute('aria-valuemax', String(Math.round(b.maximum)));
      divider.setAttribute('aria-valuenow', String(Math.round(size)));
      divider.setAttribute('aria-valuetext', 'Timeline ' + Math.round(b.available ? size / b.available * 100 : 0) + ' percent of workspace');
      if (save && b.available) { ratio = size / b.available; try { win.localStorage.setItem('gateway.timeline.panel-ratio', String(ratio)); } catch {} }
    }
    divider.addEventListener('pointerdown', event => {
      if (event.button !== 0) return;
      event.preventDefault(); divider.focus();
      drag = {id:event.pointerId, y:event.clientY, size:editor.getBoundingClientRect().height};
      divider.setPointerCapture(event.pointerId); root.dataset.resizing = 'true';
    });
    divider.addEventListener('pointermove', event => { if (drag?.id === event.pointerId) paint(drag.size + drag.y - event.clientY, true); });
    function release(event) { if (drag?.id !== event.pointerId) return; drag = null; delete root.dataset.resizing; }
    for (const type of ['pointerup', 'pointercancel', 'lostpointercapture']) divider.addEventListener(type, release);
    divider.addEventListener('dblclick', () => { ratio = .56; paint(null, true); });
    divider.addEventListener('keydown', event => {
      const b = bounds(), current = editor.getBoundingClientRect().height, step = event.shiftKey ? 48 : 16;
      const value = {ArrowUp:current + step, ArrowDown:current - step, Home:b.minimum, End:b.maximum}[event.key];
      if (value === undefined) return; event.preventDefault(); paint(value, true);
    });
    paneDivider.addEventListener('pointerdown',event=>{if(event.button!==0)return;event.preventDefault();paneDivider.focus();paneDrag={id:event.pointerId,x:event.clientX,size:viewport.getBoundingClientRect().width};paneDivider.setPointerCapture(event.pointerId);root.dataset.resizing='viewport';});
    paneDivider.addEventListener('pointermove',event=>{if(paneDrag?.id===event.pointerId)paintPane(paneDrag.size+paneDrag.x-event.clientX,true);});
    function releasePane(event){if(paneDrag?.id!==event.pointerId)return;paneDrag=null;delete root.dataset.resizing;}
    for(const type of ['pointerup','pointercancel','lostpointercapture'])paneDivider.addEventListener(type,releasePane);
    paneDivider.addEventListener('dblclick',()=>{paneRatios[viewport.dataset.resource||'block']=viewport.dataset.resource==='transaction'?.56:.38;paintPane(undefined,true);});
    paneDivider.addEventListener('keydown',event=>{const b=paneBounds(),current=viewport.getBoundingClientRect().width,step=event.shiftKey?48:16,value={ArrowLeft:current+step,ArrowRight:current-step,Home:b.minimum,End:b.maximum}[event.key];if(value===undefined)return;event.preventDefault();paintPane(value,true);});
    if (win.ResizeObserver) new win.ResizeObserver(() => paint()).observe(root);
    if(win.ResizeObserver&&upper)new win.ResizeObserver(()=>paintPane()).observe(upper);
    if(win.MutationObserver&&viewport)new win.MutationObserver(()=>paintPane()).observe(viewport,{attributes:true,attributeFilter:['data-resource']});
    win.addEventListener('gateway:timeline-layout', () => paint());
    const panels=new Map([['inspector',inspector],['viewport',viewport],['timeline',editor]]);
    let hovered=null,activePanel='inspector',previous=null,restoredScroll=null;
    const restore=doc.createElement('button');restore.type='button';restore.className='workspace-restore-layout';restore.textContent='Restore layout';restore.title='Restore layout (Backtick / Escape)';restore.hidden=true;root.append(restore);
    const announce=doc.createElement('span');announce.className='timeline-reorder-status';announce.setAttribute('role','status');root.append(announce);
    function publish(){win.dispatchEvent(new win.CustomEvent('gateway:panel-layout',{detail:{panel:maximized}}));if(win.parent!==win)win.parent.postMessage({type:'gateway:workspace-panel-mode',panel:maximized},win.location.origin);}
    function toggleMaximize(panel=hovered||activePanel){
      if(maximized){restoreLayout();return;}
      if(!panels.has(panel)||!panels.get(panel))return;
      if(doc.fullscreenElement){doc.exitFullscreen?.().then(()=>toggleMaximize(panel)).catch(()=>{});return;}
      viewport?.classList.remove('fullscreen-fallback');
      restoredScroll=null;previous={focus:doc.activeElement,scroll:[root,...root.querySelectorAll('*')].filter(n=>n.scrollTop||n.scrollLeft).map(n=>[n,n.scrollTop,n.scrollLeft]),inert:[...panels.values(),divider,paneDivider].filter(Boolean).map(n=>[n,n.inert])};
      maximized=panel;activePanel=panel;root.dataset.maximizedPanel=panel;panels.get(panel).classList.add('workspace-panel-maximized');for(const [name,node]of panels)if(node&&name!==panel)node.inert=true;divider.inert=paneDivider.inert=true;restore.hidden=false;announce.textContent=(panel==='inspector'?'Details':panel==='viewport'?'Visualization':'Timeline')+' maximized. Backtick or Escape restores the layout.';publish();
    }
    function settleRestoredScroll(){const saved=restoredScroll;if(!saved||maximized)return;win.requestAnimationFrame(()=>win.requestAnimationFrame(()=>{if(restoredScroll!==saved||maximized)return;paint();for(const[node,top,left]of saved)if(node.isConnected){node.scrollTop=top;node.scrollLeft=left;}}));}
    function restoreLayout(){
      if(!maximized)return;panels.get(maximized)?.classList.remove('workspace-panel-maximized');maximized=null;restoredScroll=previous?.scroll||[];delete root.dataset.maximizedPanel;restore.hidden=true;for(const [node,value]of previous?.inert||[])node.inert=value;paint();for(const [node,top,left]of previous?.scroll||[])if(node.isConnected){node.scrollTop=top;node.scrollLeft=left;}const focus=previous?.focus;previous=null;if(focus?.isConnected)focus.focus?.({preventScroll:true});announce.textContent='Previous layout restored.';publish();settleRestoredScroll();
    }
    function blockedOverlay(){return Boolean(doc.querySelector('dialog[open],[aria-modal="true"],[role="dialog"]:not([hidden]),[role="menu"]:not([hidden]),.timeline-help[open],.timeline-index-picker:not([hidden]),.timeline-reveal-picker'));}
    const editable=target=>target?.closest?.('input,textarea,select,[contenteditable]:not([contenteditable="false"])');
    function shortcut(event){if(event.defaultPrevented||event.isComposing||event.repeat||event.ctrlKey||event.metaKey||event.altKey||event.shiftKey||blockedOverlay()||editable(event.target))return;if(event.key==='Escape'&&maximized){event.preventDefault();event.stopImmediatePropagation?.();restoreLayout();return;}if(event.code!=='Backquote')return;event.preventDefault();event.stopImmediatePropagation?.();toggleMaximize();}
    for(const [name,panel]of panels)if(panel){panel.addEventListener('pointerenter',()=>hovered=name);panel.addEventListener('pointerleave',()=>{if(hovered===name)hovered=null;});panel.addEventListener('focusin',()=>activePanel=name);panel.addEventListener('pointerdown',()=>activePanel=name);}
    restore.onclick=restoreLayout;doc.addEventListener('keydown',shortcut,true);
    win.addEventListener('message',event=>{if(event.source!==win.parent||event.origin!==win.location.origin)return;if(event.data?.type==='gateway:workspace-panel-host-state'&&!event.data.panel)settleRestoredScroll();if(event.data?.type==='gateway:workspace-panel-shortcut')shortcut({code:event.data.code,key:event.data.key,target:doc.body,preventDefault(){},stopImmediatePropagation(){}});if(event.data?.type==='gateway:workspace-visibility'&&event.data.visible===false)restoreLayout();});
    root.gatewayPanelLayout={toggle:toggleMaximize,restore:restoreLayout,state:()=>maximized};
    paint();
    return {resize:() => paint(),...root.gatewayPanelLayout};
  }

  function mountRange({root, from, to, live}) {
    const doc = root.ownerDocument;
    const start = root.querySelector('#range-start'), end = root.querySelector('#range-end');
    const rail = root.querySelector('.build-range-rail'), summary = root.querySelector('#range-summary');
    let definition = null, snapshot = null, extent = 0, applyingHandle = false;
    function sync() {
      const d = definition, minimum = height(d?.start_height) ? d.start_height : 0;
      const tip = height(snapshot?.timeline?.tip_height) ? snapshot.timeline.tip_height : null;
      const a = number(from), b = number(to);
      const invalidFirst = from.value.trim() !== '' && a === null, invalidLast = !live.checked && to.value.trim() !== '' && b === null;
      // Keep a stable rail while editing, even before a header tip is available.
      extent = Math.max(extent, minimum, tip ?? 0, a ?? 0, b ?? 0);
      const maximum = extent;
      const first = clamp(a ?? minimum, minimum, maximum), last = clamp(b ?? tip ?? maximum, first, maximum);
      const locked = !d?.buildable || !!d.locked;
      from.min = String(minimum); to.min = String(Math.max(minimum, a ?? minimum));
      from.setCustomValidity(invalidFirst ? 'Enter a whole, non-negative block height.' : a !== null && a < minimum ? 'The first block must be at least ' + minimum + '.' : '');
      to.setCustomValidity(invalidLast ? 'Enter a whole, non-negative block height.' : !live.checked && b !== null && b < Math.max(minimum, a ?? minimum) ? 'The last block must not precede the first block.' : '');
      start.min = end.min = String(minimum); start.max = end.max = String(maximum);
      start.value = String(first); end.value = String(last);
      start.disabled = locked || from.readOnly || from.disabled || maximum === minimum;
      end.disabled = locked || live.checked || to.disabled || maximum === minimum;
      rail.hidden = live.checked;
      const span = Math.max(1, maximum - minimum);
      rail.style.setProperty('--range-start', ((first - minimum) / span * 100) + '%');
      rail.style.setProperty('--range-end', ((last - minimum) / span * 100) + '%');
      start.setAttribute('aria-valuetext', 'First block ' + format(first));
      end.setAttribute('aria-valuetext', 'Last block ' + format(last));
      if (invalidFirst || invalidLast) summary.textContent = 'Use whole, non-negative block heights for both endpoints.';
      else if (live.checked) summary.textContent = 'Follow the verified chain from block ' + format(first) + '. Review the plan before starting.';
      else if (a !== null && a < minimum || b !== null && b < Math.max(minimum, a ?? minimum)) summary.textContent = 'Choose an ordered range at or above block ' + format(minimum) + '.';
      else if (to.value === '') summary.textContent = tip === null ? 'Enter a last block or use the verified tip when work starts. The local header tip is not available yet.' : 'Last block follows the verified tip at start. Current tip: ' + format(tip) + '. Move the right handle to choose a fixed end.';
      else summary.textContent = format(last - first + 1) + ' blocks selected, including both endpoints.';
      root.dataset.start = String(first); root.dataset.end = String(last);
    }
    function change(input, target) {
      if (input.disabled) return;
      const minimum = Number(start.min), maximum = Number(start.max);
      target.value = String(input === start ? clamp(Number(input.value), minimum, number(to) ?? maximum) : clamp(Number(input.value), number(from) ?? minimum, maximum));
      applyingHandle = true;
      try { target.dispatchEvent(new doc.defaultView.Event('input', {bubbles:true})); } finally { applyingHandle = false; }
      sync();
    }
    start.addEventListener('input', () => change(start, from));
    end.addEventListener('input', () => change(end, to));
    function numberChanged() { if (!applyingHandle) extent = 0; sync(); }
    from.addEventListener('input', numberChanged); to.addEventListener('input', numberChanged); live.addEventListener('change', sync);
    return {update(d, s) { if (d?.id !== definition?.id) extent = 0; definition = d; snapshot = s; sync(); }, sync};
  }
  return {mountLayout, mountRange};
})();
