'use strict';

const $ = id => document.getElementById(id);
const state = {key:'', connected:false, generation:0, status:null, recipes:[],
  pendingCommands:0, stopPending:false, restartSupported:false,
  settings:{temperature_unit:'F', language:'en', detailed_status:false}, capabilities:[],
  editorId:null, dirty:false, tab:'overview', polling:false, restoreDefaults:false,
  telemetry:null, telemetryAt:0};
let requestQueue = Promise.resolve();
let pollTimer;
let apiExplorer;
let brewHistory;

function setupAppearance() {
  const select = $('appearance'), system = matchMedia('(prefers-color-scheme: dark)');
  const apply = () => {
    document.documentElement.style.colorScheme = select.value === 'system' ? 'light dark' : select.value;
    brewHistory?.redraw();
  };
  try {
    const saved = localStorage.getItem('picobrew-theme');
    if (saved === 'light' || saved === 'dark') select.value = saved;
  } catch (_) {}
  select.addEventListener('change', () => {
    try { localStorage.setItem('picobrew-theme', select.value); } catch (_) {}
    apply();
  });
  system.addEventListener('change', apply);
  apply();
}
setupAppearance();

const programLabels = {'sous-vide':'Sous vide', 'cold-brew':'Cold brew',
  'deep-clean':'Deep Clean', rinse:'Rinse', 'first-rinse':'First rinse', descale:'Descale', circulate:'Circulate',
  'drain-reservoir':'Drain reservoir', 'drain-filter':'Drain step filter', 'rack-beer':'Rack beer',
  utilities:'Utilities menu', settings:'Settings menu'};

function notify(message, error=false) {
  $('notice').textContent = message;
  $('notice').classList.toggle('error', error);
  $('notice').hidden = !message;
}

function api(path, method='GET', body) {
  const generation = state.generation;
  const key = state.key;
  const task = async () => {
    if (generation !== state.generation) throw new Error('Connection changed.');
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 6000);
    try {
      const headers = {Accept:'application/json'};
      if (key && path !== '/overview' && !path.startsWith('/pairing/')) headers.Authorization = 'Bearer '+key;
      if (body !== undefined || ['POST','PUT','PATCH'].includes(method)) headers['Content-Type'] = 'application/json';
      const response = await fetch('/api/v1'+path, {method, headers,
        body:body === undefined ? undefined : JSON.stringify(body),
        signal:controller.signal, cache:'no-store', credentials:'omit', redirect:'error'});
      let data;
      try { data = response.status === 204 ? null : await response.json(); }
      catch (_) { throw new Error('The brewer returned an unreadable response.'); }
      if (!response.ok) {
        const error = new Error(data.message || data.error || 'Request failed ('+response.status+').');
        error.status = response.status;
        throw error;
      }
      if (generation !== state.generation) throw new Error('Connection changed.');
      return data;
    } catch (error) {
      if (error.name === 'AbortError') throw new Error('The brewer did not respond. Check its connection.');
      throw error;
    } finally { clearTimeout(timeout); }
  };
  // One connection at a time matches the Pico's bounded embedded HTTP server.
  const result = requestQueue.catch(() => {}).then(task);
  requestQueue = result;
  return result;
}

function connected(value) {
  state.connected = value;
  $('publicOverview').hidden = value;
  document.querySelector('nav').hidden = !value;
  if (!value) document.querySelectorAll('.tab-panel').forEach(panel=>panel.hidden=true);
  else if (!$('overview').parentElement.querySelector('.tab-panel:not([hidden])')) selectTab(state.tab);
  $('copyDeviceKey').hidden = !value;
  if(value)$('pairButton').hidden = true;
  $('connectionText').textContent = value ? 'Connected' : 'Disconnected';
  $('connectionDot').classList.toggle('online', value);
  $('connectButton').textContent = value ? 'Device key' : 'Connect';
  updateControls();
}

function renderIdentity(info) {
  const name={'pico-c':'Pico C'}[info.model] || 'Pico C';
  $('modelName').textContent=name+' brewing console';
  document.title=name+' · Pico Local';
}

function renderStatus(status) {
  state.status = status;
  const due = status.maintenance?.due || [];
  const care = {rinse:'Rinse after this brew',deep_clean:'Run Deep Clean',descale:'Descale if using mineral-containing reservoir water'};
  const reminders = due.filter(key=>care[key]).map(key=>{
    const button=document.createElement('button');
    button.className='button small';
    button.textContent=care[key];
    button.disabled=!status.ready || status.busy || status.stopped;
    button.onclick=()=>{ $('program').value=key.replace('_','-'); $('runProgram').click(); };
    return button;
  });
  $('careDue').replaceChildren(...reminders);
  if (status.model) renderIdentity(status);
  $('wortTemperature').textContent = temperature(status.wort_f);
  $('blockTemperature').textContent = temperature(status.thermoblock_f);
  $('targetTemperature').textContent = status.target_f === 0 ? 'Unheated' : temperature(status.target_f);
  $('stepTime').textContent = duration(status.step_seconds);
  const phases = ['Heating','Holding temperature','Cooling','Draining','Idle','Circulating','Cooling thermoblock','Pump hold','Heating'];
  $('phaseName').textContent = phases[status.phase] || 'Phase '+status.phase;
  $('operationTitle').textContent = status.stopped ? 'Outputs stopped.' : status.busy ? (status.recipe_name || 'Operation in progress.') : 'Ready for your next brew.';
  $('operationDetail').textContent = status.stopped ? (status.stop_reason === 'filter-removed' ? 'The step filter was removed during pumping. This operation is cancelled. Refit the filter and wait for idle before clearing the stop.' : 'The stop latch is active. Wait for the operation to end before clearing it.') : status.busy ? (status.step_name || 'Follow the brewer’s prompts on the remote front panel.') : 'Choose a saved recipe, open a program, or use the front panel.';
  $('modeBadge').textContent = status.stopped ? 'STOPPED' : status.busy ? 'IN PROGRESS' : 'READY';
  if (!status.ready && !status.stopped) {
    $('modeBadge').textContent = 'NOT READY';
    $('operationTitle').textContent = 'Waiting for the brewer.';
    $('operationDetail').textContent = 'Check the front panel for instructions.';
  }
  $('modeBadge').classList.toggle('stopped', !!status.stopped);
  $('filterBadge').textContent = status.filter_inserted === null ? 'FILTER UNKNOWN' : status.filter_inserted ? 'FILTER INSERTED' : 'FILTER REMOVED';
  $('filterBadge').classList.toggle('warning', status.filter_inserted === false);
  $('deviceId').textContent = status.device_uid ? 'Device '+status.device_uid : '';
  $('lastUpdated').textContent = 'Updated '+new Date().toLocaleTimeString();
  updateControls();
}

function renderDisplay(frame) {
  const {width,height}=frame, rotation=frame.rotation ?? 2;
  const logicalBytes=width*Math.ceil(height/8), storage=frame.storage_bytes ?? logicalBytes;
  if (width !== 128 || ![32,64].includes(height) || ![0,2].includes(rotation) ||
      !Number.isInteger(storage) || storage<logicalBytes || storage>1024 ||
      frame.format !== 'page-lsb-hex' || typeof frame.data !== 'string' ||
      !/^[0-9a-fA-F]+$/.test(frame.data) || frame.data.length !== storage*2) return;
  const canvas=$('display');
  if(canvas.width!==width || canvas.height!==height) {canvas.width=width; canvas.height=height;}
  const context = canvas.getContext('2d');
  const image = context.createImageData(width,height);
  for (let y=0;y<height;y++) for (let x=0;x<width;x++) {
    // Only visible pages are drawn. SSD1309 retains a larger raw buffer.
    const sourceX=rotation===2 ? width-1-x : x, sourceY=rotation===2 ? height-1-y : y;
    const address = (Math.floor(sourceY/8)*128+sourceX)*2;
    const lit = (parseInt(frame.data.slice(address,address+2),16) >> (sourceY%8)) & 1;
    const i=(y*128+x)*4;
    const color = lit ? [199,225,180] : [18,38,32];
    image.data[i]=color[0]; image.data[i+1]=color[1]; image.data[i+2]=color[2]; image.data[i+3]=255;
  }
  context.putImageData(image,0,0);
  $('displayHint').textContent = 'LIVE OLED · '+width+' × '+height;
}

function renderPrograms() {
  $('program').replaceChildren();
  for (const command of state.capabilities) if (programLabels[command]) {
    $('program').add(new Option(programLabels[command],command));
  }
  updateControls();
}

async function refreshRecipes() {
  const data = await api('/recipes');
  state.recipes = data.recipes || [];
  const previous = $('brewRecipe').value;
  $('brewRecipe').replaceChildren(new Option('Choose a recipe',''));
  $('recipeList').replaceChildren();
  for (const recipe of state.recipes) {
    $('brewRecipe').add(new Option(recipe.name,recipe.id));
    const button=document.createElement('button'); button.type='button'; button.className='recipe-item';
    button.classList.toggle('active', recipe.id === state.editorId);
    const name=document.createElement('strong');name.textContent=recipe.name;
    const detail=document.createElement('small');detail.textContent=recipe.steps+' steps · '+recipe.id;
    button.append(name,detail);button.addEventListener('click',()=>loadRecipe(recipe.id));$('recipeList').append(button);
  }
  if (!state.recipes.length) {
    const empty=document.createElement('p');empty.className='empty';empty.textContent='Your collection is empty. Create or import your first recipe.';$('recipeList').append(empty);
  }
  if (state.recipes.some(recipe=>recipe.id===previous)) $('brewRecipe').value=previous;
  else if (state.recipes.length) $('brewRecipe').value=state.recipes[0].id;
  $('recipeCount').textContent=state.recipes.length;
  $('recipeCapacity').textContent=state.recipes.length+' / '+data.capacity;
  updateControls();
}

function download(name,data) {
  const blob=new Blob([JSON.stringify(data,null,2)+'\n'],{type:'application/json'});
  const url=URL.createObjectURL(blob);const link=document.createElement('a');link.href=url;link.download=name;link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
}

function renderSettings(settings) {
  state.settings=settings;$('temperatureUnit').value=settings.temperature_unit;$('language').value=settings.language;$('detailedStatus').checked=!!settings.detailed_status;
}

function renderTelemetry(config) {
  state.telemetry=config;
  $('hubDestination').textContent=config.enabled && config.host ?
    'Sending live readings to '+config.host+':'+config.port+'.' : 'No Hub destination configured.';
  const status=config.status || {};
  const labels={disabled:'Add this brewer in PicoBrew Hub to connect automatically.',offline:'Waiting for the Pico’s network connection.',
    'waiting-idle':'Waiting for the brewer to be idle to connect.',ready:'Ready to send readings.',
    connecting:'Connecting…',sending:'Sending readings…',receiving:'Waiting for acknowledgement…',
    backoff:'Destination unavailable. The Pico will retry.'};
  $('telemetryStatus').textContent=(labels[status.state] || 'Waiting for connection status.')+
    ' Sent: '+(status.sent || 0)+' · Failed: '+(status.failed || 0)+' · Skipped: '+(status.dropped || 0)+
    (status.last_http_status?' · Last reply: HTTP '+status.last_http_status:'')+
    (status.storage_error?' · Destination settings could not be saved.':'');
  updateControls();
}

async function refreshTelemetry() {
  const generation=state.generation;
  state.telemetryAt=Date.now();
  try {renderTelemetry(await api('/telemetry'));}
  catch(error) {if(generation===state.generation)$('telemetryStatus').textContent=error.message;}
}

async function poll() {
  clearTimeout(pollTimer);
  if(!state.key || state.polling)return;
  state.polling=true;const generation=state.generation;
  try {
    if(!document.hidden) {
      renderStatus(await api('/status'));connected(true);
      if(state.tab==='overview')renderDisplay(await api('/display'));
      if(state.tab==='settings' && Date.now()-state.telemetryAt>=5000)await refreshTelemetry();
    }
  } catch(error) {
    if(generation===state.generation){connected(false);notify(error.message,true);if(error.status===401){state.key='';try{sessionStorage.removeItem('pico-api-key');}catch(_){}}}
  } finally {state.polling=false;if(state.key)pollTimer=setTimeout(poll,state.connected?1200:3000);}
}

function selectTab(tab) {
  state.tab=tab;document.querySelectorAll('.tab-panel').forEach(panel=>panel.hidden=panel.id!==tab);
  document.querySelectorAll('[data-tab]').forEach(button=>{const active=button.dataset.tab===tab;button.classList.toggle('selected',active);if(active)button.setAttribute('aria-current','page');else button.removeAttribute('aria-current');});
  if(state.connected && tab==='recipes')refreshRecipes().catch(error=>notify(error.message,true));
  if(state.connected && tab==='settings')refreshTelemetry();
  if(tab==='history')brewHistory?.activate();
}

async function connect(key) {
  state.key=key;state.generation++;state.pendingCommands=0;state.stopPending=false;clearTimeout(pollTimer);
  state.telemetry=null;state.telemetryAt=0;
  const info=await api('');state.capabilities=info.commands || [];state.restartSupported=info.system_restart===true;renderPrograms();
  renderIdentity(info);
  state.restoreDefaults=info.restore_default_recipes === true;
  $('firmwareVersion').textContent=info.firmware_version ? 'Firmware '+info.firmware_version : 'Standalone firmware';
  renderSettings(await api('/settings'));renderStatus(await api('/status'));await refreshRecipes();
  try{sessionStorage.setItem('pico-api-key',key);}catch(_){}
  connected(true);notify('');$('connectDialog').close();$('apiKey').value='';poll();
}

function disconnect() {
  state.generation++;state.pendingCommands=0;state.stopPending=false;state.key='';state.status=null;clearTimeout(pollTimer);connected(false);
  state.telemetry=null;$('telemetryStatus').textContent='Connect to see Hub connection status.';
  $('hubDestination').textContent='No Hub connection loaded.';
  try{sessionStorage.removeItem('pico-api-key');}catch(_){}
  $('operationTitle').textContent='Disconnected from your brewer.';$('operationDetail').textContent='A running program continues on the Pico. Reconnect to see its status.';
  $('apiKey').value='';notify('Device key removed from this tab.');
  publicOverview();
}

document.querySelectorAll('[data-tab]').forEach(button=>button.addEventListener('click',()=>selectTab(button.dataset.tab)));
document.querySelectorAll('[data-encoder]').forEach(button=>button.addEventListener('click',()=>sendCommand({command:'encoder',delta:Number(button.dataset.encoder),click:false})));
$('encoderClick').addEventListener('click',()=>sendCommand({command:'encoder',delta:0,click:true}));
$('brewRecipe').addEventListener('change',updateControls);
$('startRecipe').addEventListener('click',()=>sendCommand({command:'brew',recipe_id:$('brewRecipe').value}));
$('runProgram').addEventListener('click',()=>sendCommand({command:$('program').value}));
$('stopButton').addEventListener('click',()=>sendCommand({command:'stop'}));
$('clearStop').addEventListener('click',()=>sendCommand({command:'clear-stop'}));
$('restartBrewer').addEventListener('click',restartBrewer);
$('connectButton').addEventListener('click',()=>{$('connectError').textContent='';$('connectDialog').showModal();});
$('cancelConnect').addEventListener('click',()=>$('connectDialog').close());
$('disconnectButton').addEventListener('click',disconnect);
$('connectForm').addEventListener('submit',async event=>{
  event.preventDefault();$('submitConnect').disabled=true;$('connectError').textContent='';
  try{await connect($('apiKey').value.trim());}catch(error){connected(false);$('connectError').textContent=error.message;}finally{$('submitConnect').disabled=false;}
});
$('newRecipe').addEventListener('click',()=>{if(!state.dirty || confirm('Discard the unsaved recipe changes?')){editRecipe({});$('recipeName').focus();}});
$('restoreRecipes').addEventListener('click',async()=>{
  if(!confirm('Add missing starter recipes? Recipes already on the Pico, including your edits, will be preserved.'))return;
  $('restoreRecipes').disabled=true;
  try{const result=await api('/recipes/restore-defaults','POST');await refreshRecipes();notify(result.added ? result.added+' starter recipes added. Existing recipes preserved.' : 'All starter recipes are already on the Pico.');}
  catch(error){notify(error.message,true);}finally{updateControls();}
});
$('addStep').addEventListener('click',()=>{addStep();state.dirty=true;});
$('recipeForm').addEventListener('input',()=>{state.dirty=true;});
$('recipeName').addEventListener('input',()=>{if($('recipeId').dataset.auto==='true')$('recipeId').value=$('recipeName').value.toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'').slice(0,20);});
$('recipeId').addEventListener('input',()=>{$('recipeId').dataset.auto='false';});
$('recipeForm').addEventListener('submit',async event=>{
  event.preventDefault();const recipe=recipeFromForm();if(!recipe)return;
  try{await api('/recipes/'+encodeURIComponent(recipe.id),'PUT',{name:recipe.name,steps:recipe.steps,...(!state.editorId && {create_only:true})});editRecipe(recipe,true);await refreshRecipes();notify('Recipe saved on the Pico.');}
  catch(error){notify(error.message,true);}
});
$('deleteRecipe').addEventListener('click',async()=>{
  if(!state.editorId || !confirm('Delete this recipe from the Pico?'))return;
  try{await api('/recipes/'+encodeURIComponent(state.editorId),'DELETE');editRecipe({});await refreshRecipes();notify('Recipe deleted.');}
  catch(error){notify(error.message,true);}
});
$('importRecipe').addEventListener('click',()=>$('importFile').click());
$('importFile').addEventListener('change',async event=>{
  const file=event.target.files[0];event.target.value='';if(!file)return;
  try{
    if(file.size>16384)throw new Error('Recipe file is too large.');
    const recipe=readRecipeFile(await file.text());
    const issue=recipeIssue(recipe);
    if(issue)throw new Error(issue);
    if(state.dirty && !confirm('Discard the unsaved recipe changes?'))return;
    editRecipe(recipe,false);state.dirty=true;notify('Recipe imported into the editor. Review it, then save to the Pico.');
  }catch(error){notify(error instanceof SyntaxError?'The file is not valid JSON.':error.message,true);}
});
$('exportRecipe').addEventListener('click',()=>{const recipe=recipeFromForm();if(recipe)download(recipe.id+'.json',recipe);});
$('settingsForm').addEventListener('submit',async event=>{
  event.preventDefault();try{const settings=await api('/settings','PATCH',{temperature_unit:$('temperatureUnit').value,language:$('language').value,detailed_status:$('detailedStatus').checked});renderSettings(settings);if(state.status)renderStatus(state.status);notify('Preferences saved on the Pico.');}catch(error){notify(error.message,true);}
});
document.addEventListener('visibilitychange',()=>{if(!document.hidden && state.key)poll();});
apiExplorer=setupApiExplorer({session:()=>({key:state.key,connected:state.connected,generation:state.generation}),enqueue:task=>{
  const result=requestQueue.catch(()=>{}).then(task);requestQueue=result;return result;
}});
brewHistory=setupHistory({session:()=>({key:state.key,connected:state.connected,generation:state.generation,temperature_unit:state.settings.temperature_unit}),enqueue:task=>{
  const result=requestQueue.catch(()=>{}).then(task);requestQueue=result;return result;
}});
editRecipe({});
async function publicOverview() {
  if (state.connected) return;
  const generation = state.generation;
  try {
    const view = await api('/overview');
    if (state.connected || generation !== state.generation) return;
    $('pairButton').hidden = view.pairing_v1 !== true;
    $('publicModel').textContent = view.model_name || 'Machine overview';
    $('publicState').textContent = !view.ready ? 'Starting up' : view.busy ? 'Operation in progress' : 'Idle';
    $('publicTemperatures').textContent = [
      ['Wort', view.wort_f], ['Thermoblock', view.thermoblock_f], ['Temperature', view.temperature_f],
    ].filter(([, value]) => Number.isFinite(value)).map(([name, value]) => name+' '+temperature(value)).join(' · ');
  } catch (_) {
    if (generation === state.generation && !state.connected) {
      $('publicState').textContent='Machine status unavailable.';
      $('publicTemperatures').textContent='';
    }
  }
}
$('copyDeviceKey').addEventListener('click',async()=>{
  if (!state.connected) return;
  try {
    await navigator.clipboard.writeText(state.key);
    $('connectError').textContent='Device key copied.';
  } catch (_) {
    const input=$('apiKey');
    input.type='text';input.value=state.key;input.focus();input.select();
    $('connectError').textContent='Press Ctrl+C or Command+C to copy the selected key.';
  }
});
$('pairButton').addEventListener('click',()=>pairOnDevice(''));
$('connectDialog').addEventListener('close',()=>{++pairAttempt;$('pairButton').disabled=false;$('apiKey').value='';$('apiKey').type='password';});
setInterval(()=>{if(!state.connected && !document.hidden)publicOverview();},15000);
try{state.key=sessionStorage.getItem('pico-api-key') || '';}catch(_){}
if (hubTicket) {
  publicOverview();pairOnDevice(hubTicket,true);
} else if (/^[0-9a-f]{64}$/.test(state.key)) {
  connect(state.key).catch(error=>{
    disconnect();
    notify(error.message,true);
    publicOverview();
  });
} else {
  connected(false);
  publicOverview();
}
