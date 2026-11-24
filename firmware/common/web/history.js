'use strict';

function setupHistory({session,enqueue}) {
  const el=id=>document.getElementById(id), canvas=el('historyChart'), selector=el('historySession');
  let series=[],metric='temperature';
  const lightColors=['#1e5947','#b45e34','#765ba0','#227b9a','#a37b25','#ba4e74','#557e46','#6e6a9d'];
  const darkColors=['#9ed6ad','#ffac82','#ccb0ef','#86d2ed','#ebc47f','#f7a8c9','#bbd88e','#b9b8ee'];
  const fieldInfo=name=>metadata?.field_metadata?.[name] || ({wort_f:{label:'Wort',unit:'F'},thermoblock_f:{label:'Thermoblock',unit:'F'},target_f:{label:'Target',unit:'F'},temperature_f:{label:'Temperature',unit:'F'},pressure_kpa:{label:'Pressure',unit:'kPa'},battery_v:{label:'Battery',unit:'V'}}[name]) || {label:name.replaceAll('_',' '),unit:'raw'};
  const axisUnit=()=>metric==='temperature'?unit:fieldInfo(metric).unit;
  const configureMetrics=()=>{
    const selector=el('historyMetric'),old=selector.value;selector.replaceChildren();
    const visible=fields.filter(name=>session().brewing || !['wort_f','thermoblock_f','target_f'].includes(name));
    const temperatures=visible.filter(name=>fieldInfo(name).unit==='F' && rows.some(row=>Number.isFinite(value(row,name))));
    if(temperatures.length)selector.add(new Option('Temperature','temperature'));
    for(const name of visible)if(!['elapsed_ms','phase','controller_status','compartment','current_step','flags','device_state','step_seconds'].includes(name)&&fieldInfo(name).unit!=='F')selector.add(new Option(fieldInfo(name).label,name));
    selector.value=Array.from(selector.options).some(option=>option.value===old)?old:selector.options[0]?.value || '';metric=selector.value;
    series=(metric==='temperature'?temperatures:[metric]).filter(Boolean).map((name,index)=>[name,fieldInfo(name).label,index]);
  };
  let metadata=null, rows=[], fields=[], selected=0, wantedId=null, loading=false, run=0, generation=session().generation;
  let unit=session().temperature_unit==='C'?'C':'F';
  let geometry=null;
  const duration=ms=>{
    const seconds=Math.max(0,Math.floor(Number(ms)/1000));
    return Math.floor(seconds/3600)+':'+String(Math.floor(seconds/60)%60).padStart(2,'0')+':'+String(seconds%60).padStart(2,'0');
  };
  const value=(row,name)=>row[fields.indexOf(name)];
  const temperature=(row,name)=>{
    const number=value(row,name);
    return !Number.isFinite(number) || (name==='target_f' && number===0)?null:fieldInfo(name).unit==='F' && unit==='C'?(number-32)*5/9:number;
  };
  const phase=row=>session().brewing?PicoDevice.phaseLabel(session().model,value(row,'phase')):'State '+value(row,'device_state');
  const discardRecording=()=>{
    metadata=null;rows=[];fields=[];selected=0;geometry=null;
    el('historyReadout').textContent='';el('historyValues').textContent='';
    el('historyPhases').replaceChildren();canvas.getContext('2d').clearRect(0,0,canvas.width,canvas.height);
  };
  const clear=()=>{
    discardRecording();wantedId=null;run++;selector.replaceChildren();
    el('historySummary').textContent='Connect to load recorded sessions.';
  };
  const update=()=>{
    const connection=session();
    if (!connection.key || connection.generation!==generation) {clear();generation=connection.generation;}
    const nextUnit=connection.temperature_unit==='C'?'C':'F';
    if (nextUnit!==unit) {unit=nextUnit;if (rows.length) choose(selected);}
    el('historyRefresh').disabled=loading || !connection.connected;
    selector.disabled=loading || !connection.connected || !selector.options.length;
    for (const id of ['historyExportJson','historyExportCsv','historySample']) el(id).disabled=loading || !rows.length;
  };
  const request=async(path,connection,token)=>enqueue(async()=>{
    if (token!==run || connection.generation!==session().generation || !session().key) throw new Error('Connection changed.');
    const controller=new AbortController(), timer=setTimeout(()=>controller.abort(),6000);
    try {
      const reply=await fetch('/api/v1/history'+path,{headers:{Accept:'application/json',Authorization:'Bearer '+connection.key},
        cache:'no-store',credentials:'omit',redirect:'error',signal:controller.signal});
      const data=await reply.json();
      if (!reply.ok) {const error=new Error(data.message || data.error || 'History request failed.');error.status=reply.status;throw error;}
      return data;
    } finally {clearTimeout(timer);}
  });
  const draw=()=>{
    const context=canvas.getContext('2d'), width=Math.max(320,canvas.clientWidth || 1000), height=340;
    const ratio=window.devicePixelRatio || 1;
    if (canvas.width!==Math.round(width*ratio) || canvas.height!==Math.round(height*ratio)) {
      canvas.width=Math.round(width*ratio);canvas.height=Math.round(height*ratio);
    }
    context.setTransform(ratio,0,0,ratio,0,0);
    context.clearRect(0,0,width,height);geometry=null;
    if (!rows.length) return;
    const palette=document.documentElement.dataset.theme==='dark'?darkColors:lightColors;
    const style=getComputedStyle(canvas);
    const color=name=>style.getPropertyValue('--'+name).trim();
    const left=65, right=width-20, columns=Math.max(1,Math.floor((width-70)/155)), top=20+Math.ceil(series.length/columns)*18, bottom=height-55;
    const first=Number(value(rows[0],'elapsed_ms')), last=Number(value(rows[rows.length-1],'elapsed_ms'));
    const span=Math.max(1000,last-first), readings=[];
    for (const row of rows) for (const [field] of series) if (temperature(row,field)!==null) readings.push(temperature(row,field));
    let low=0,high=212;
    if(readings.length){const minimum=Math.min(...readings),maximum=Math.max(...readings),padding=Math.max(metric==='temperature'?5:0.5,(maximum-minimum)*0.05);low=minimum-padding;high=maximum+padding;}
    const x=time=>left+(time-first)/span*(right-left), y=temp=>bottom-(temp-low)/(high-low)*(bottom-top);
    context.font='12px system-ui';context.lineWidth=1;
    series.forEach(([,label,index])=>{context.fillStyle=palette[index%palette.length];context.textAlign='left';context.fillText(label,left+(index%columns)*155,14+Math.floor(index/columns)*18);});
    for (let tick=0;tick<=4;tick++) {
      const temp=low+(high-low)*tick/4, yy=y(temp);
      context.strokeStyle=color('border');context.beginPath();context.moveTo(left,yy);context.lineTo(right,yy);context.stroke();
      context.fillStyle=color('muted');context.textAlign='right';context.fillText(temp.toFixed(metric==='temperature'||axisUnit()==='raw'?0:1)+(metric==='temperature'?'°':' ')+axisUnit(),left-8,yy+4);
      const time=first+span*tick/4;context.textAlign='center';context.fillText(duration(time),x(time),height-12);
    }
    let previous=rows[0], start=first;
    const band=(end,row)=>{
      const xx=x(start), ww=x(end)-xx;
      context.fillStyle=Number(value(row,'phase'))%2?color('selection'):color('neutral');context.fillRect(xx,bottom+8,ww,15);
      if (ww>75) {context.fillStyle=color('ink');context.textAlign='left';context.fillText(phase(row),xx+4,bottom+20,ww-8);}
    };
    for (const row of rows.slice(1)) if (phase(row)!==phase(previous)) {
      band(value(row,'elapsed_ms'),previous);start=value(row,'elapsed_ms');previous=row;
    }
    band(last,previous);
    rows.forEach((row,index)=>{
      if (index && (value(row,'flags')&16)) {
        const from=x(value(rows[index-1],'elapsed_ms')), to=x(value(row,'elapsed_ms'));
        context.fillStyle=color('chart-gap');context.fillRect(from,top,to-from,bottom-top+23);
      }
    });
    for (const [field,,index] of series) {
      context.strokeStyle=palette[index%palette.length];context.lineWidth=2;context.setLineDash(field==='target_f'?[5,4]:[]);context.beginPath();
      let line=false;
      for (const row of rows) {
        const temp=temperature(row,field);
        if (temp===null) {line=false;continue;}
        const xx=x(value(row,'elapsed_ms')), yy=y(temp);
        if (!line || (value(row,'flags')&16)) context.moveTo(xx,yy);else context.lineTo(xx,yy);
        line=true;
      }
      context.stroke();
    }
    context.setLineDash([]);context.strokeStyle=color('muted');context.lineWidth=1;
    const cursor=x(value(rows[selected],'elapsed_ms'));
    context.beginPath();context.moveTo(cursor,top);context.lineTo(cursor,bottom);context.stroke();
    geometry={left,right,first,span,width};
  };
  const choose=index=>{
    if (!rows.length) return;
    selected=Math.max(0,Math.min(rows.length-1,index));const row=rows[selected], flags=value(row,'flags');
    el('historySample').value=selected;
    const temperatures=series.map(([field,label])=>label+': '+(field==='target_f' && value(row,field)===0?'Unheated':temperature(row,field)!==null?temperature(row,field).toFixed(fieldInfo(field).unit==='raw'?0:2)+(fieldInfo(field).unit==='F'?'°'+unit:fieldInfo(field).unit==='raw'?' raw':' '+fieldInfo(field).unit):'unavailable'));
    el('historyReadout').textContent=duration(value(row,'elapsed_ms'))+' · '+temperatures.join(' · ')+' · '+phase(row);
    el('historyValues').textContent=JSON.stringify({...Object.fromEntries(fields.map((name,i)=>[name,row[i]])),
      ready:!!(flags&1),busy:!!(flags&2),stopped:!!(flags&4),...(session().brewing?{filter_inserted:!!(flags&8)}:{}),sampling_gap:!!(flags&16)},null,2);
    draw();
  };
  const render=()=>{
    const saved=metadata.persisted_record_count ?? rows.length;
    el('historySummary').textContent=(metadata.recipe_name || metadata.command || 'Session')+' · '+metadata.state+' · '+
      duration(metadata.duration_ms)+' · '+rows.length+' readings ('+saved+' saved) · '+(metadata.gap_count || 0)+' gaps'+
      (metadata.truncated?' · Recording limit reached; later readings were not saved.':'')+
      (metadata.storage_error?' · Storage error.':'')+
      (metadata.recovered_after_restart?' · Interrupted by a restart; showing the saved recording.':'');
    el('historySample').max=Math.max(0,rows.length-1);
    const body=el('historyPhases');body.replaceChildren();let previous='';
    for (const row of rows) {
      const state=[phase(row),value(row,'current_step'),value(row,'compartment'),value(row,'flags')&28].join(':');
      if (state===previous) continue;previous=state;
      const tr=document.createElement('tr');
      const step=value(row,'current_step');
      for (const text of [duration(value(row,'elapsed_ms')),phase(row)+((value(row,'flags')&16)?' · gap':''),
        session().brewing?(step===255?'—':String(step+1))+' / compartment '+value(row,'compartment'):(value(row,'flags')&2?'Session active':'Idle')]) {
        const td=document.createElement('td');td.textContent=text;tr.append(td);
      }
      body.append(tr);
    }
    if (rows.length) choose(Math.min(selected,rows.length-1));
    else {el('historyReadout').textContent='No readings yet.';el('historyValues').textContent='';draw();}
    update();
  };
  const load=async(retry=false)=>{
    if (loading || !session().connected) {update();return;}
    loading=true;const connection=session(), token=++run;update();
    const current=()=>token===run && connection.generation===session().generation && !!session().key;
    try {
      const inventory=await request('',connection,token);
      if (!current()) return;
      if (!inventory.available) {clear();el('historySummary').textContent='No session has been recorded yet.';return;}
      const sessions=inventory.sessions || [inventory];
      if (!sessions.length) throw new Error('No recorded session is available.');
      const chosen=sessions.find(item=>item.id===wantedId) || sessions[0];wantedId=chosen.id;
      if (metadata && metadata.id!==chosen.id) discardRecording();
      selector.replaceChildren();
      for (const item of sessions) selector.add(new Option('#'+item.id+' · '+(item.recipe_name || item.command || 'Session')+' · '+item.state,item.id));
      selector.value=String(wantedId);
      const next={...chosen,fields:inventory.fields,field_metadata:inventory.field_metadata || {},sample_interval_ms:chosen.sample_interval_ms ?? inventory.sample_interval_ms,
        checkpoint_interval_ms:inventory.checkpoint_interval_ms};
      if (!Number.isSafeInteger(next.record_count) || next.record_count<0 || next.record_count>10000) throw new Error('Unrecognized history length.');
      const reuse=metadata?.id===next.id && metadata.first_elapsed_ms===next.first_elapsed_ms && rows.length<=next.record_count;
      const collected=reuse?rows.slice():[];
      let names=reuse?metadata.fields:next.fields,fieldMetadata=reuse?metadata.field_metadata:next.field_metadata;
      if (!Array.isArray(names) || !names.includes('elapsed_ms') || !names.includes('flags')) throw new Error('Unrecognized history format.');
      while (collected.length<next.record_count) {
        const offset=collected.length;
        const page=await request('/samples/'+offset+'?id='+encodeURIComponent(next.id),connection,token);
        if (!current()) return;
        if(Array.isArray(page.fields)) {
          if(offset && (JSON.stringify(page.fields)!==JSON.stringify(names) || JSON.stringify(page.field_metadata || {})!==JSON.stringify(fieldMetadata || {})))throw new Error('History channel schema changed during download. Refresh to try again.');
          names=page.fields;fieldMetadata=page.field_metadata || {};
        }
        if (page.id!==next.id || page.offset!==offset || !Array.isArray(page.samples) || !page.samples.length ||
            page.samples.some(row=>!Array.isArray(row) || row.length!==names.length)) throw new Error('Incomplete history page.');
        collected.push(...page.samples.slice(0,next.record_count-offset));
        el('historySummary').textContent='Loading recorded session: '+collected.length+' / '+next.record_count+' readings';
      }
      if (!current()) return;
      metadata={...next,fields:names,field_metadata:fieldMetadata};rows=collected;fields=names;configureMetrics();render();
    } catch (error) {
      if (current()) {
        if (error.status===409 && !retry) {loading=false;return await load(true);}
        el('historySummary').textContent=error.name==='AbortError'?'History request timed out. Refresh to try again.':error.message;
      }
    } finally {loading=false;update();}
  };
  const download=format=>{
    if (!metadata || !rows.length || loading) return;
    const quote=value=>'"'+String(value ?? '').replaceAll('"','""')+'"';
    const content=format==='json'?JSON.stringify({metadata,samples:rows},null,2):
      [fields.map(quote).join(','),...rows.map(row=>row.map(quote).join(','))].join('\r\n');
    const url=URL.createObjectURL(new Blob([content],{type:format==='json'?'application/json':'text/csv;charset=utf-8'}));
    const link=document.createElement('a');link.href=url;link.download='pico-session-'+metadata.id+'.'+format;
    link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
  };
  el('historyMetric').addEventListener('change',()=>{metric=el('historyMetric').value;configureMetrics();choose(selected);});
  el('historyRefresh').addEventListener('click',()=>load());
  selector.addEventListener('change',()=>{wantedId=Number(selector.value);discardRecording();load();});
  el('historySample').addEventListener('input',event=>choose(Number(event.target.value)));
  el('historyExportJson').addEventListener('click',()=>download('json'));
  el('historyExportCsv').addEventListener('click',()=>download('csv'));
  canvas.addEventListener('pointermove',event=>{
    if (!geometry || loading) return;
    const bounds=canvas.getBoundingClientRect(), px=(event.clientX-bounds.left)*geometry.width/bounds.width;
    const time=geometry.first+(px-geometry.left)/(geometry.right-geometry.left)*geometry.span;
    let nearest=0;
    for (let i=1;i<rows.length;i++) if (Math.abs(value(rows[i],'elapsed_ms')-time)<Math.abs(value(rows[nearest],'elapsed_ms')-time)) nearest=i;
    if (nearest!==selected) choose(nearest);
  });
  window.addEventListener('pico-theme-change',()=>{if(rows.length) draw();});
  new ResizeObserver(()=>{if (rows.length) draw();}).observe(canvas);
  setInterval(()=>{if (!document.hidden && !el('history').hidden && session().connected) load();},15000);
  clear();update();
  return {activate:()=>load(),update};
}

// Measurement formatting shared by the overview and session views.
function temperature(value) {
  const number = Number(value);
  if (value === null || value === undefined || !Number.isFinite(number)) return '—';
  const unit = state.settings.temperature_unit;
  return (unit === 'C' ? (number-32)*5/9 : number).toFixed(1)+'°'+unit;
}
function duration(seconds) {
  if (!Number.isFinite(Number(seconds))) return '—';
  const value = Math.max(0, Math.floor(Number(seconds)));
  const minutes = Math.floor(value/60);
  return minutes+':'+String(value%60).padStart(2,'0');
}
