'use strict';
// Original Gateway implementation of a proportional Bitcoin bowtie diagram.
// Numeric reference: mempool/mempool tx-bowtie-graph at
// adde1343c8ab3e2258175197a7b618bec1589933. No upstream source is bundled.
(function (scope) {
  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
  function amount(v) {
    if (v == null) return null;
    if (!Number.isSafeInteger(v) || v < 0) throw Error('Flow values must be non-negative integer sats.');
    return v;
  }
  function dimension(value, fallback, minimum, maximum) {
    return Number.isFinite(value) ? clamp(value, minimum, maximum) : fallback;
  }
  function nodes(data, limit) {
    if (!Array.isArray(data.inputs) || !Array.isArray(data.outputs)) throw Error('Transaction inputs and outputs are required.');
    if (data.inputs.length > 100000 || data.outputs.length > 100000) throw Error('Transaction flow exceeds the bounded renderer limit.');
    const inputs = data.inputs.map((source, index) => ({type:'input',index:Number.isSafeInteger(source.index)?source.index:index,value:amount(source.value),source,coinbase:source.coinbase===true||source.outpoint==='coinbase'}));
    const outputs = data.outputs.map((source, index) => ({type:'output',index:Number.isSafeInteger(source.n)?source.n:index,value:amount(source.value_sats),source}));
    if (inputs.some((node,index)=>node.index!==index)||outputs.some((node,index)=>node.index!==index)) throw Error('Flow positions must retain original transaction order.');
    if (!Number.isSafeInteger(inputs.reduce((sum,node)=>sum+(node.value||0),0))) throw Error('Transaction input total exceeds exact integer sats.');
    const fee = amount(data.fee);
    const total = outputs.reduce((sum, node) => sum + (node.value || 0), 0) + (fee || 0);
    if (!Number.isSafeInteger(total)) throw Error('Transaction flow total exceeds exact integer sats.');
    if (fee) outputs.unshift({type:'fee',value:fee,source:{}});
    for (const node of inputs) if (node.coinbase && !total) node.value = 0;
    function bounded(list, side) {
      if (list.length <= limit) return list;
      const tail=list.slice(limit),unknown=tail.filter(node=>node.value===null).length;
      return list.slice(0,limit).concat({type:side,value:tail.reduce((sum,node)=>sum+(node.value||0),0),rest:tail.length,unknown,source:{}});
    }
    return {inputs:bounded(inputs,'input'),outputs:bounded(outputs,'output'),total,fee,inputCount:inputs.length,outputCount:data.outputs.length};
  }
  function layout(data, options={}) {
    const width=dimension(options.width,1200,240,10000),height=dimension(options.height,600,40,10000);
    const connectors=options.connectors===true,limit=Math.floor(dimension(options.lineLimit,250,1,250));
    const visible=Math.floor(dimension(options.maxStrands,24,1,250)),minimum=dimension(options.minWeight,2,1,20);
    const bridge=Math.min(10,Math.ceil(width/100));
    const bandArea=connectors?Math.max(width-200,width*.8):width-20;
    const mass=Math.min(dimension(options.maxCombinedWeight,100,1,200),Math.floor((bandArea-2*bridge)/6));
    const edge=(width-bandArea)/2,zeroLength=clamp(bandArea/2-bridge-110,20,60);
    const prepared=nodes(data,limit),center=height/2;
    let bottom=height+10;
    function curve(side,node) {
      const reflect=x=>side==='in'?x:width-x;
      if(node.zeroValue){const x=edge/2+20;return 'M '+reflect(x)+' '+node.outerY+' L '+reflect(x+zeroLength)+' '+node.outerY;}
      const y=Math.round(node.outerY)===Math.round(node.innerY)?node.outerY-1:node.outerY;
      const start=edge+node.thickness/2,end=width/2-bridge*.9+1;
      const first=Math.max(start+5,node.padding+edge-node.offset),last=end-node.offset-10;
      const control=(first+last)/2;
      return 'M '+reflect(start)+' '+y+' L '+reflect(first)+' '+y+' C '+reflect(control)+' '+y+', '+reflect(control)+' '+node.innerY+', '+reflect(last)+' '+node.innerY+' L '+reflect(end)+' '+node.innerY;
    }
    function polygon(side,node,connector) {
      if(node.zeroValue)return null;
      const y=Math.round(node.outerY)===Math.round(node.innerY)?node.outerY-1:node.outerY,h=node.thickness/2;
      const pts=side==='in'?[[edge-10,y-h],[h+edge-10,y],[edge-10,y+h],[connector?-10:node.thickness+edge,y+h],[connector?-10:node.thickness+edge,y-h]]:[[width-h-edge+10,y-h],[width-edge+10,y],[width-h-edge+10,y+h],[connector?width+10:width-h-edge,y+h],[connector?width+10:width-h-edge,y-h]];
      return pts.map((point,i)=>(i?'L ':'M ')+point.join(' ')).join(' ');
    }
    function side(list,which) {
      let unresolved=0,remaining=prepared.total;
      for(const node of list){if(node.value===null)unresolved++;else remaining-=node.value;}
      const residual=unresolved?Math.max(0,remaining)/unresolved:0;
      const rows=list.map(node=>{
        const weight=prepared.total?mass*(node.value===null?residual:node.value)/prepared.total:mass/(list.length||1);
        return {...node,weight,thickness:node.value===0?20:Math.min(mass+.5,Math.max(minimum-1,weight)+1),zeroValue:node.value===0,outerY:0,innerY:0,offset:0,padding:0};
      });
      const visibleRows=rows.slice(0,visible),occupied=visibleRows.reduce((sum,row)=>sum+row.thickness,0);
      const gap=visibleRows.length>1?Math.max(4,(height-occupied)/(visibleRows.length-1)):0;
      const low=center-mass/2,high=low+mass+.5;
      let outer=0,inner=low,shift=0,minShift=0,maxShift=0,previous=0,padding=0;
      for(const row of rows){
        row.outerY=list.length===1?center:outer+row.thickness/2;
        outer+=row.thickness+gap;bottom=Math.max(bottom,row.outerY+row.thickness/2+10);
        if(row.zeroValue)continue;
        row.innerY=Math.min(high-row.thickness/2,Math.max(low+row.thickness/2,inner+row.weight/2));
        inner+=row.weight;
        if(options.tooltip===true||!row.rest){
          const reach=(bandArea-Math.max(previous,row.weight)-2*edge)/2;
          const bend=Math.atan2(1.5*(row.innerY-row.outerY),.75*reach),separation=(previous+row.weight)/2;
          if(Math.sin(bend)!==0)shift+=clamp(separation*(1-Math.cos(bend))/Math.sin(bend),-separation,separation);
          row.offset=shift;minShift=Math.min(minShift,shift);maxShift=Math.max(maxShift,shift);padding=Math.max(padding,row.thickness/2);previous=row.weight;
        }
      }
      for(const row of rows){row.offset-=minShift;row.padding=padding+maxShift-minShift;row.path=curve(which,row);row.markerPath=polygon(which,row,false);row.connectorPath=connectors?polygon(which,row,true):null;}
      return rows;
    }
    const inputs=side(prepared.inputs,'in'),outputs=side(prepared.outputs,'out');
    return {width,height,total:prepared.total,inputCount:prepared.inputCount,outputCount:prepared.outputCount,inputs,outputs,hasLine:inputs.some(node=>!node.zeroValue)&&outputs.some(node=>!node.zeroValue),middle:{path:'M '+(width/2-bridge)+' '+(center+.25)+' L '+(width/2+bridge)+' '+(center+.25),thickness:mass+.5},bounds:{x:0,y:0,width,height:bottom},unknownInputs:inputs.some(node=>node.value===null||node.unknown>0)};
  }
  let nextId=0;
  function render(doc,data,options={}) {
    const result=layout(data,options),ns='http://www.w3.org/2000/svg',prefix='gateway-flow-'+(++nextId)+'-';
    const make=(tag,attributes={})=>{const node=doc.createElementNS(ns,tag);for(const [key,value]of Object.entries(attributes))if(value!==null&&value!==undefined)node.setAttribute(key,String(value));return node;};
    const svg=make('svg',{class:'transaction-flow-svg',viewBox:'0 0 '+result.width+' '+(result.height+10),role:'img','aria-label':'Transaction input and output value flow','data-reference':'mempool-bowtie','data-input-count':result.inputCount,'data-output-count':result.outputCount});
    const defs=make('defs');svg.append(defs);let closePicker=null;
    svg.disposeFlow=()=>closePicker?.();
    const colors=['#9339f4','#007cfa'];
    for(const side of ['input','output','fee'])for(const active of [false,true]){
      const gradient=make('linearGradient',{id:prefix+side+(active?'-active':''),x1:'0%',y1:'0%',x2:'100%',y2:'0%'});
      let stops=side==='input'?[[0,colors[0]],[100,colors[1]]]:side==='output'?[[0,colors[1]],[100,colors[0]]]:[[0,colors[1]],[50,colors[1]],[100,'transparent']];
      if(active)stops=side==='input'?[[0,colors[0]],[2,colors[0]],[30,'#fff'],[100,colors[1]]]:side==='output'?[[0,colors[1]],[70,'#fff'],[98,colors[0]],[100,colors[0]]]:[[0,colors[1]],[100,'#fff']];
      for(const [offset,color]of stops)gradient.append(make('stop',{offset:offset+'%','stop-color':color}));defs.append(gradient);
      if(!active&&side!=='fee'){
        const marker=make('marker',{id:prefix+side+'-arrow',viewBox:'-5 -5 10 10',refX:0,refY:0,markerUnits:'strokeWidth',markerWidth:1.5,markerHeight:1,orient:'auto'});
        const points=side==='input'?[[-5,-5],[0,0],[-5,5],[1,5],[1,-5]]:[[1,-5],[0,-5],[-5,0],[0,5],[1,5]];
        marker.append(make('polygon',{points:points.map(point=>point.join(',')).join(' '),fill:colors[0]}));defs.append(marker);
      }
    }
    if(result.hasLine)svg.append(make('path',{d:result.middle.path,class:'flow-middle',fill:'none',stroke:colors[1],'stroke-width':result.middle.thickness}));
    const number=value=>Number(value).toLocaleString('en-US');
    function add(node,side){
      const index=node.index,type=node.type,unknown=node.value===null||node.unknown>0;
      const label=node.rest?node.rest+' more '+(side==='input'?'inputs':'outputs')+(unknown?' · values partly unknown':' · '+number(node.value)+' sats'):(type==='fee'?'Fee':side==='input'?(node.coinbase?'Coinbase':'Input '+index):'Output '+index)+' · '+(unknown?'value unknown':number(node.value)+' sats');
      const group=make('g',{class:'flow-strand '+type+(node.zeroValue?' zero-value':'')+(unknown?' unknown-value':''),'data-flow-side':side,'data-flow-index':index,'data-flow-rest':node.rest,tabindex:node.rest||type==='fee'?-1:0,role:node.rest||type==='fee'?'img':'button','aria-label':label});
      const title=make('title');title.textContent=label+(node.source?.outpoint?' · '+node.source.outpoint:'');group.append(title);
      const path=make('path',{d:node.path,class:'flow-band',fill:'none',stroke:node.zeroValue?colors[0]:'url(#'+prefix+type+')','stroke-width':node.thickness,'stroke-linecap':node.zeroValue?'round':null,'marker-start':node.zeroValue||type==='fee'?null:'url(#'+prefix+type+'-arrow)'});
      if(node.markerPath)group.append(make('path',{d:node.markerPath,class:'flow-marker-target',fill:'transparent'}));group.append(path);
      if(node.connectorPath&&side==='input'&&!node.coinbase)group.append(make('path',{d:node.connectorPath,class:'flow-connector',fill:colors[0],opacity:.75}));
      const payload={index,n:index,datum:node.source,unknown,rest:node.rest};
      const activate=event=>{if(event.defaultPrevented||node.rest||type==='fee')return;(side==='input'?options.onInput:options.onOutput)?.(payload,event);};
      group.addEventListener('click',activate);group.addEventListener('keydown',event=>{if(!node.rest&&type!=='fee'&&(event.key==='Enter'||event.key===' ')){event.preventDefault();(side==='input'?options.onInput:options.onOutput)?.(payload,event);}});
      for(const name of ['pointerenter','focus'])group.addEventListener(name,event=>{path.setAttribute('stroke',node.zeroValue?'#fff':'url(#'+prefix+type+'-active)');options.onHover?.({...payload,side},event);});
      for(const name of ['pointerleave','blur'])group.addEventListener(name,()=>path.setAttribute('stroke',node.zeroValue?colors[0]:'url(#'+prefix+type+')'));
      svg.append(group);
      if(side==='output'&&type==='output'&&!node.rest){
        const entries=(options.placements||data.inscription_context?.entries||[]).filter(entry=>entry.state==='verified_reveal_output'&&Number.isSafeInteger(entry.output_index)&&entry.output_index===index);
        if(entries.length){
          const x=result.width-22,y=node.outerY,badge=make('g',{class:'flow-inscription-badge',role:'button',tabindex:0,'aria-label':entries.length+' verified reveal inscription'+(entries.length===1?'':'s')+' in output '+index,'data-flow-inscription-output':index});
          badge.append(make('circle',{cx:x,cy:y,r:9}));const text=make('text',{x,y:y+3,'text-anchor':'middle'});text.textContent=entries.length>1?String(entries.length):'◈';badge.append(text);
          let menu=null,chosen=0;
          const close=()=>{menu?.remove();menu=null;badge.setAttribute('aria-expanded','false');doc.removeEventListener?.('pointerdown',outside,true);doc.removeEventListener?.('keydown',escape,true);doc.defaultView?.removeEventListener?.('blur',close);if(closePicker===close)closePicker=null;};
          const outside=event=>{const path=event.composedPath?.()||[];if(!path.includes(menu)&&!path.includes(badge))close();};
          const escape=event=>{if(menu&&event.key==='Escape'){event.preventDefault();event.stopPropagation?.();close();badge.focus?.();}};
          const choose=(entry,event)=>{event.stopPropagation?.();close();options.onInscription?.(entry,event);};
          function paintMenu(){
            const pageSize=8,start=Math.floor(chosen/pageSize)*pageSize,rows=entries.slice(start,start+pageSize),line=28,menuWidth=Math.min(310,result.width-20);
            if(!menu){menu=make('g',{class:'flow-inscription-menu',role:'menu','aria-label':'Verified inscriptions in output '+index,'data-flow-inscription-menu':index});svg.append(menu);}
            menu.replaceChildren();const top=clamp(y-16,10,Math.max(10,result.bounds.height-(rows.length+1)*line)),left=result.width-menuWidth-10;
            menu.append(make('rect',{x:left,y:top,width:menuWidth,height:(rows.length+1)*line,rx:5,class:'flow-picker-background'}));
            const caption=make('text',{x:left+10,y:top+19,class:'flow-picker-caption'});caption.textContent=(start+1)+'–'+(start+rows.length)+' of '+entries.length+' · arrows / Page Up / Page Down';menu.append(caption);
            rows.forEach((entry,row)=>{
              const position=start+row,item=make('g',{role:'menuitem',tabindex:position===chosen?0:-1,'data-flow-inscription-choice':position,'aria-label':String(entry.coordinate||entry.id||'Inscription '+position)});
              item.append(make('rect',{x:left+3,y:top+(row+1)*line,width:menuWidth-6,height:line-2,rx:3,class:'flow-picker-choice'}));
              const label=make('text',{x:left+10,y:top+(row+1)*line+19});label.textContent=String(entry.coordinate||entry.id||'Inscription '+position).slice(0,40);item.append(label);
              const title=make('title');title.textContent=String(entry.coordinate||entry.id||'Inscription '+position);item.append(title);
              item.addEventListener('click',event=>choose(entry,event));item.addEventListener('keydown',event=>{
                if(event.key==='Enter'||event.key===' '){event.preventDefault();choose(entry,event);return;}
                if(event.key==='Escape'){event.preventDefault();event.stopPropagation?.();close();badge.focus?.();return;}
                if(!['ArrowUp','ArrowDown','Home','End','PageUp','PageDown'].includes(event.key))return;
                event.preventDefault();event.stopPropagation?.();chosen=clamp(event.key==='Home'?0:event.key==='End'?entries.length-1:chosen+(event.key==='ArrowUp'?-1:event.key==='ArrowDown'?1:event.key==='PageUp'?-pageSize:pageSize),0,entries.length-1);paintMenu();
              });menu.append(item);if(position===chosen)item.focus?.();
            });
          }
          const open=event=>{event.stopPropagation?.();if(entries.length===1){choose(entries[0],event);return;}if(menu){close();return;}closePicker?.();closePicker=close;badge.setAttribute('aria-expanded','true');chosen=0;paintMenu();doc.addEventListener?.('pointerdown',outside,true);doc.addEventListener?.('keydown',escape,true);doc.defaultView?.addEventListener?.('blur',close);};
          if(entries.length>1){badge.setAttribute('aria-haspopup','menu');badge.setAttribute('aria-expanded','false');svg.addEventListener('click',close);}
          badge.addEventListener('click',event=>{if(!event.defaultPrevented)open(event);});badge.addEventListener('keydown',event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();open(event);}});svg.append(badge);
        }
      }
    }
    for(const node of result.inputs)add(node,'input');for(const node of result.outputs)add(node,'output');
    svg.flowLayout=result;
    return svg;
  }
  scope.GatewayTransactionFlow={layout,render,reference:'mempool/mempool@adde1343c8ab3e2258175197a7b618bec1589933'};
})(typeof window==='object'?window:globalThis);
