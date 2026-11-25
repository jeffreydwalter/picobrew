'use strict';

// BEGIN GENERATED API CATALOG
const apiOperations=[["GET","/api/v1/health","Check readiness and API-key provisioning"],["GET","/api/v1","Discover implemented local capabilities"],["GET","/api/v1/status","Read controller, workflow and front-panel state"],["GET","/api/v1/display","Read the 128×64 appliance display"],["GET","/api/v1/recipes","List persisted local recipes"],["GET","/api/v1/recipes/{id}","Read a complete local recipe",null,[["id","path",true,{"type":"string","minLength":1,"maxLength":20,"pattern":"^[A-Za-z0-9_-]+$"}]]],["PUT","/api/v1/recipes/{id}","Create or replace a persisted recipe while idle",[["Example",{"name":"example","steps":[{"name":"example","location":1,"target_f":0,"minutes":0}]}]],[["id","path",true,{"type":"string","minLength":1,"maxLength":20,"pattern":"^[A-Za-z0-9_-]+$"}]]],["DELETE","/api/v1/recipes/{id}","Remove a recipe while idle",null,[["id","path",true,{"type":"string","minLength":1,"maxLength":20,"pattern":"^[A-Za-z0-9_-]+$"}]]],["POST","/api/v1/commands","Queue a workflow or operate the virtual front panel",[["stop",{"command":"stop"}],["brew",{"command":"brew","recipe_id":"example"}],["circulate",{"command":"circulate"}],["clear-stop",{"command":"clear-stop"}],["cold-brew",{"command":"cold-brew"}],["descale",{"command":"descale"}],["drain-filter",{"command":"drain-filter"}],["drain-reservoir",{"command":"drain-reservoir"}],["encoder",{"command":"encoder","delta":1}],["first-rinse",{"command":"first-rinse"}],["manual",{"command":"manual"}],["rack-beer",{"command":"rack-beer"}],["rinse",{"command":"rinse"}],["settings",{"command":"settings"}],["sous-vide",{"command":"sous-vide"}],["utilities",{"command":"utilities"}]]],["GET","/api/v1/settings","Read nonsecret display preferences"],["PATCH","/api/v1/settings","Persist supported preferences while idle",[["Example",{"temperature_unit":"F"}]]],["GET","/api/v1/events","Read the recent volatile event ring"],["GET","/api/v1/history","List retained brews and recording capacity"],["GET","/api/v1/history/samples/{offset}","Read a page of recorded brew samples",null,[["offset","path",true,{"type":"integer","minimum":0,"maximum":4224}],["id","query",true,{"type":"integer","minimum":1,"maximum":4294967295}]]],["GET","/api/v1/telemetry","Read Hub connection and delivery status"],["POST","/api/v1/recipes/restore-defaults","Add missing starter PicoPak recipes while idle"]];
// END GENERATED API CATALOG

function setupApiExplorer({session, enqueue}) {
  const el=id=>document.getElementById(id);
  const endpoint=el('apiEndpoint');
  const example=el('apiExample'), body=el('apiBody');
  const status=el('apiStatus'), response=el('apiResponse');
  let busy=false, parameters=[], operations=apiOperations;
  for (const [method,path] of apiOperations)
    endpoint.add(new Option(method+' '+path,method+' '+path));
  const operation=()=>operations[endpoint.selectedIndex];
  const update=()=>{
    el('apiSend').disabled=busy || !session().connected;
    for (const input of [endpoint,example,body,...parameters.map(item=>item.input)]) input.disabled=busy;
    if (!session().connected) {status.textContent='Connect to send a request.';response.textContent='';}
  };
  const loadExample=()=>{body.value=JSON.stringify(operation()[3][example.selectedIndex][1],null,2);};
  const select=()=>{
    const [method,path,summary,examples,specs=[]]=operation();
    el('apiDescription').textContent=summary;
    const row=el('apiParameterRow');row.hidden=!specs.length;row.replaceChildren();
    parameters=specs.map(([name,place,required,schema],index)=>{
      const input=document.createElement('input'), label=document.createElement('label');
      input.id='apiParameter'+(index || '');input.required=required;input.autocomplete='off';
      if (schema.type==='integer') input.inputMode='numeric';
      if (schema.maxLength) input.maxLength=schema.maxLength;
      input.value=schema.default ?? '';
      label.htmlFor=input.id;label.textContent=name+' ('+place+')';row.append(label,input);
      return {name,place,schema,input};
    });
    el('apiBodyRow').hidden=!examples;
    el('apiExampleRow').hidden=!examples || examples.length<2;
    body.required=!!examples;
    example.replaceChildren();body.value='';
    if (examples) {
      for (const [name] of examples) example.add(new Option(name));
      loadExample();
    }
    status.textContent=method==='GET'?'Read-only request.':'This request changes the device or saved data.';
    response.textContent='';update();
  };
  endpoint.addEventListener('change',select);
  example.addEventListener('change',loadExample);
  el('apiForm').addEventListener('submit',async event=>{
    event.preventDefault();
    const connection=session();
    if (busy || !connection.connected) return;
    const current=()=>session().connected && session().generation===connection.generation;
    const [method,template,,examples]=operation();
    let path=template, payload;
    try {
      const query=new URLSearchParams();
      for (const {name,place,schema,input} of parameters) {
        let value=input.value.trim();const number=Number(value);
        if (!value && !input.required) continue;
        if (!value || (schema.pattern && !new RegExp(schema.pattern).test(value)) ||
            value.length<(schema.minLength || 0) || value.length>(schema.maxLength || Infinity) ||
            (schema.type==='integer' && (!/^-?\d+$/.test(value) || !Number.isSafeInteger(number) ||
              number<(schema.minimum ?? -Infinity) || number>(schema.maximum ?? Infinity))))
          throw new Error('Invalid '+name+'. Check the parameter value.');
        if (schema.type==='integer') value=String(number);
        if (place==='path') path=path.replace('{'+name+'}',encodeURIComponent(value));
        else query.set(name,value);
      }
      if (query.size) path+='?'+query;
      if (examples) {
        JSON.parse(body.value);
        payload=body.value;
        if (new TextEncoder().encode(payload).length>4095) throw new Error('JSON body exceeds 4095 bytes.');
      }
      busy=true;update();status.textContent='Sending '+method+' '+path;response.textContent='';
      const result=await enqueue(async()=>{
        if (!current()) throw new Error('Connection changed.');
        const controller=new AbortController(), timer=setTimeout(()=>controller.abort(),6000);
        try {
          const headers={Accept:'application/json',Authorization:'Bearer '+connection.key};
          if (payload!==undefined || ['POST','PUT','PATCH'].includes(method)) headers['Content-Type']='application/json';
          const reply=await fetch(path,{method,headers,body:payload,signal:controller.signal,
            cache:'no-store',credentials:'omit',redirect:'error'});
          return [reply.status,await reply.text()];
        } finally {clearTimeout(timer);}
      });
      if (current()) {
        status.textContent=method+' '+path+' · HTTP '+result[0];
        try {response.textContent=JSON.stringify(JSON.parse(result[1]),null,2);}
        catch (_) {response.textContent=result[1] || '(empty response)';}
      }
    } catch (error) {
      if (current()) status.textContent=error.name==='AbortError'?'Request timed out.':error.message;
    } finally {busy=false;update();}
  });
  select();
  const configure=info=>{
    const original=endpoint.value;
    operations=apiOperations.filter(row=>{
      const path=row[1];
      if(path.includes('/recipes'))return PicoDevice.feature(info,'recipes') && (!path.endsWith('restore-defaults') || info.restore_default_recipes===true);
      if(path==='/api/v1/display')return PicoDevice.feature(info,'front_panel');
      if(path==='/api/v1/telemetry')return info.telemetry_forwarding===true;
      if(path==='/api/v1/commands')return PicoDevice.commands(info).length>0;
      if(path==='/api/v1/settings' && row[0]==='PATCH')return !Array.isArray(info.settings_fields) || info.settings_fields.length>0;
      return true;
    }).map(row=>{
      if(row[1]==='/api/v1/settings' && row[0]==='PATCH' && Array.isArray(info.settings_fields)) {
        const defaults={temperature_unit:'F',language:'en',detailed_status:false,auto_cycle:true};
        return [row[0],row[1],row[2],[['Supported preferences',Object.fromEntries(info.settings_fields.filter(name=>name in defaults).map(name=>[name,defaults[name]]))]]];
      }
      if(row[1]!=='/api/v1/commands')return row;
      const examples=PicoDevice.commands(info).map(name=>{
        const payload={command:name},required=info.command_fields?.[name]?.required || [];
        for(const field of required)payload[field]=field==='recipe_id'?'example':field==='enabled'||field==='confirm'||field==='click'?false:field==='value'?300:field==='channel'?1:0;
        return [PicoDevice.commandLabels[name] || name,payload];
      });return [row[0],row[1],row[2],examples];
    });
    if(info.z_state_v1===true)operations.push(['GET','/api/v1/z/state','Read the Z Program Maker sensor and controller fields.']);
    if(info.model==='z-series' && PicoDevice.feature(info,'front_panel'))operations.push(['GET','/api/v1/display/{offset}','Read a page of the same grayscale display revision returned by /display.',null,[['offset','path',true,{type:'integer',minimum:1024,maximum:3072,default:1024}],['revision','query',true,{type:'integer',minimum:0,maximum:4294967295,default:0}]]]);
    endpoint.replaceChildren();for(const [method,path] of operations)endpoint.add(new Option(method+' '+path,method+' '+path));
    if(Array.from(endpoint.options).some(option=>option.value===original))endpoint.value=original;
    select();
  };
  return {update,configure};
}

// Hub pairing uses the installed API client asset. Its five flash slots also serve older recovery images.
const ticketPattern=/^[0-9a-f]{64}$/;
function launchTicket() {
  if (!location.hash.startsWith('#pair=')) return '';
  const ticket=location.hash.slice(6);
  history.replaceState(null,'',location.pathname+location.search);
  return ticketPattern.test(ticket) ? ticket : '';
}
const hubTicket=launchTicket();
let pairAttempt=0;
async function pairOnDevice(ticket,fromHub=false){
  const attempt=++pairAttempt;
  const deadline=Date.now()+(fromHub?30000:90000);
  const button=$('pairButton'), message=$('connectError');
  button.disabled=true;
  try{
    if (!fromHub) {
      const random=crypto.getRandomValues(new Uint8Array(32));
      ticket=Array.from(random,b=>b.toString(16).padStart(2,'0')).join('');
      const request=await api('/pairing/request','POST',{nonce:ticket});
      if(attempt!==pairAttempt)return;
      if(request.state!=='pending'||request.code!==ticket.slice(0,8))throw new Error('Unexpected pairing response.');
      message.textContent='On the brewer, open Settings → About → Pair with Hub. Confirm code '+request.code+'.';
    }
    while(Date.now()<deadline && attempt===pairAttempt){
      const result=await api('/pairing/claim','POST',{nonce:ticket});
      if(attempt!==pairAttempt)return;
      if(ticketPattern.test(result.api_key||'')){await connect(result.api_key);return}
      if(result.state!=='pending')throw new Error('Pairing request unavailable.');
      await new Promise(resolve=>setTimeout(resolve,1000));
    }
    if(attempt===pairAttempt)throw new Error('Pairing expired. Start again.');
  }catch(error){
    if(attempt===pairAttempt){
      message.textContent=error.status===409?'Another pairing request is in progress.':error.message;
      if(fromHub)notify('Pairing link unavailable. Use Connect to pair on the brewer.',true);
    }
  }finally{if(attempt===pairAttempt)button.disabled=false}
}
