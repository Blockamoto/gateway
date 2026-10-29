'use strict';
// These cards explain controls. They never select a track or issue API requests.
window.GatewayIndexHelp=(()=>{
  const managers=new WeakMap();let serial=0;
  function manager(doc){
    if(managers.has(doc))return managers.get(doc);
    const win=doc.defaultView,panel=doc.createElement('aside'),heading=doc.createElement('strong'),closeButton=doc.createElement('button'),body=doc.createElement('div'),header=doc.createElement('div');
    panel.id='index-help-card-'+(++serial);panel.className='index-help-popover';panel.hidden=true;panel.tabIndex=-1;panel.setAttribute('role','tooltip');heading.id=panel.id+'-title';panel.setAttribute('aria-labelledby',heading.id);header.className='index-help-header';body.className='index-help-body';closeButton.type='button';closeButton.className='index-help-close';closeButton.textContent='×';closeButton.setAttribute('aria-label','Close index help');header.append(heading,closeButton);panel.append(header,body);doc.body.append(panel);
    let anchor=null,pinned=false,hideTimer=null,suppressFocus=false;
    const clearTimer=()=>{if(hideTimer!==null){win.clearTimeout(hideTimer);hideTimer=null;}};
    function position(){
      if(panel.hidden||!anchor?.isConnected)return;
      const gap=8,rect=anchor.getBoundingClientRect(),vw=doc.documentElement.clientWidth||win.innerWidth,vh=win.innerHeight;
      panel.style.maxWidth=Math.max(100,vw-2*gap)+'px';panel.style.maxHeight=Math.max(1,vh-2*gap)+'px';panel.style.left='0px';panel.style.top='0px';
      const natural=panel.getBoundingClientRect(),below=Math.max(0,vh-rect.bottom-2*gap),above=Math.max(0,rect.top-2*gap),useBelow=natural.height<=below||natural.height>above&&below>=above;
      // Long cards scroll on the roomier side; they must never cover their trigger.
      panel.style.maxHeight=Math.max(1,useBelow?below:above)+'px';
      const box=panel.getBoundingClientRect(),top=useBelow?rect.bottom+gap:rect.top-gap-box.height;
      panel.style.left=Math.max(gap,Math.min(vw-gap-box.width,rect.left))+'px';panel.style.top=Math.min(Math.max(gap,top),Math.max(gap,vh-gap-box.height))+'px';
    }
    function close(restore=false){clearTimer();const before=anchor;panel.hidden=true;panel.removeAttribute('data-pinned');panel.setAttribute('role','tooltip');if(before){before.setAttribute('aria-expanded','false');before.removeAttribute('aria-describedby');}anchor=null;pinned=false;if(restore&&before?.isConnected){suppressFocus=true;before.focus({preventScroll:true});suppressFocus=false;}}
    function content(button){const definition=typeof button.helpDefinition==='function'?button.helpDefinition():button.helpDefinition,source=win.GatewayIndexHelpContent;if(button.dataset.helpKind==='dependencies')return source?.dependencies?.(button.dataset.helpId,definition);return source?.get?.(button.dataset.helpId,definition)||{title:definition?.name||button.dataset.helpId,paragraphs:[definition?.theory||'Inspect the saved coverage and current state for this track.']};}
    function open(button,pin=false){if(suppressFocus)return;clearTimer();if(pinned&&anchor!==button&&!pin)return;const card=content(button);if(!card)return;if(anchor&&anchor!==button){anchor.setAttribute('aria-expanded','false');anchor.removeAttribute('aria-describedby');}anchor=button;pinned=pin||pinned;heading.textContent=card.title;body.replaceChildren();for(const paragraph of card.paragraphs||[]){const p=doc.createElement('p');p.textContent=paragraph;body.append(p);}if(card.dependencies?.length){const list=doc.createElement('dl');for(const row of card.dependencies){const term=doc.createElement('dt'),description=doc.createElement('dd');term.textContent=row.name+' · '+({hard:'Required',conditional:'Conditional',optional:'Optional',input:'Source input'}[row.requirement]||row.requirement);description.textContent=row.why;list.append(term,description);}body.append(list);}panel.hidden=false;panel.dataset.pinned=String(pinned);panel.setAttribute('role',pinned?'dialog':'tooltip');closeButton.hidden=!pinned;button.setAttribute('aria-expanded',String(pinned));button.setAttribute('aria-describedby',panel.id);position();}
    function deferClose(){clearTimer();hideTimer=win.setTimeout(()=>{if(!pinned&&anchor&&!anchor.matches(':hover')&&doc.activeElement!==anchor&&!panel.matches(':hover'))close();},100);}
    closeButton.onclick=event=>{event.preventDefault();event.stopPropagation();close(true);};
    for(const name of ['pointerdown','mousedown','click','dblclick','contextmenu','wheel'])panel.addEventListener(name,event=>event.stopPropagation());
    panel.addEventListener('dragstart',event=>{event.preventDefault();event.stopPropagation();});panel.addEventListener('pointerenter',clearTimer);panel.addEventListener('pointerleave',deferClose);
    doc.addEventListener('pointerdown',event=>{if(anchor&&!panel.contains(event.target)&&event.target!==anchor&&!anchor.contains(event.target))close();},true);
    doc.addEventListener('keydown',event=>{if(!anchor)return;if(event.key==='Escape'){event.preventDefault();event.stopImmediatePropagation();close(true);}else if(panel.contains(event.target))event.stopPropagation();},true);
    doc.addEventListener('focusin',event=>{if(pinned&&anchor&&event.target!==anchor&&!panel.contains(event.target))close();});
    win.addEventListener('resize',position);doc.addEventListener('scroll',position,true);
    const observer=win.MutationObserver&&new win.MutationObserver(()=>{if(anchor&&!anchor.isConnected)close();});observer?.observe(doc.body,{childList:true,subtree:true});
    const api={bind(button){button.addEventListener('pointerenter',()=>open(button));button.addEventListener('pointerleave',deferClose);button.addEventListener('focus',()=>open(button));button.addEventListener('blur',deferClose);button.addEventListener('click',event=>{event.preventDefault();event.stopPropagation();if(anchor===button&&pinned)close();else{open(button,true);if(event.detail===0)panel.focus({preventScroll:true});}});for(const name of ['pointerdown','mousedown','dblclick','contextmenu'])button.addEventListener(name,event=>event.stopPropagation());button.addEventListener('dragstart',event=>{event.preventDefault();event.stopPropagation();});button.addEventListener('keydown',event=>{event.stopPropagation();if(!['Tab','Enter',' '].includes(event.key))event.preventDefault();});},close};managers.set(doc,api);return api;
  }
  function controls({document:doc,id,definition}){
    const box=doc.createElement('span');box.className='index-help-controls';const provider=doc.defaultView.GatewayIndexHelpContent,resolved=typeof definition==='function'?definition():definition;
    const cards=[['purpose',provider?.get?.(id,resolved)],['dependencies',provider?.dependencies?.(id,resolved)]];
    for(const[kind,card]of cards){if(kind==='dependencies'&&!card?.dependencies?.some(row=>row.otherTrack===true&&['hard','conditional'].includes(row.requirement)))continue;const button=doc.createElement('button');button.type='button';button.className='index-help-trigger index-help-'+kind;button.dataset.helpId=id;button.dataset.helpKind=kind;button.helpDefinition=definition;button.setAttribute('aria-label',(kind==='purpose'?'Help: ':'Dependencies: ')+(card?.title||resolved?.name||id));button.setAttribute('aria-haspopup',kind==='dependencies'?'dialog':'true');button.setAttribute('aria-expanded','false');button.draggable=false;if(kind==='purpose')button.textContent='?';else button.innerHTML='<svg viewBox="0 0 18 18" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true"><rect x="6" y="1.5" width="6" height="4" rx="1"/><rect x="1" y="12" width="6" height="4" rx="1"/><rect x="11" y="12" width="6" height="4" rx="1"/><path d="M9 5.5v4H4v2.5m5-2.5h5v2.5"/></svg>';manager(doc).bind(button);box.append(button);}
    return box;
  }
  return {controls};
})();
