'use strict';

const $ = id => document.getElementById(id);
const state = {key:'', connected:false, generation:0, status:null, recipes:[],
  settings:{temperature_unit:'F', language:'en', detailed_status:false}, capabilities:[],
  editorId:null, dirty:false, tab:'overview', polling:false, restoreDefaults:false,
  telemetry:null, telemetryAt:0, info:{}, pending:false};
let requestQueue = Promise.resolve();
let pollTimer;
let apiExplorer;
let statusCapture;
let brewHistory;
const hasFeature = name => PicoDevice.feature(state.info,name);
const allowedCommand = name => state.capabilities.includes(name);
const programLabels = {'sous-vide':'Sous vide', 'sous-vide-no-keg':'Sous vide without keg', 'cold-brew':'Cold brew', coffee:'Coffee menu', 'picostill-manual':'PicoStill manual', 'picostill-boil-point':'PicoStill boiling point', manual:'Manual brewing',
  rinse:'Rinse', 'first-rinse':'First rinse', descale:'Descale', circulate:'Circulate',
  'drain-reservoir':'Drain reservoir', 'drain-filter':'Drain step filter', 'rack-beer':'Rack beer',
  clean:'Clean',drain:'Drain',chill:'Chill',utilities:'Utilities menu', settings:'Settings menu'};
const careGuides = {
  'z-series': ['Rinse after every brew. Run Clean after every three brews or before leaving the brewer unused for more than a couple of weeks.', 'z/Z_Manual.pdf#page=36'],
  picostill: ['After each use, clean the condenser column and removable parts. Soak the copper parts in the recommended cleaning solution, then rinse thoroughly. Keep the upper electronics dry.', 'picostill/PicoStill_Manual.pdf#page=58'],
  picoferm: ['Before attaching PicoFerm to a Pico C keg, sanitize the underside of the gray fermentation seal and let it air-dry. Depressurize the keg before removing or replacing PicoFerm.', 'picoferm/PicoFerm_Manual.pdf#page=5']
};

const careTasks = {
  rinse: ['Rinse the brewer after use.', 'rinse', 'Open Rinse'],
  clean: ['The brewer is due for a cleaning cycle.', 'clean', 'Open Clean'],
  clean_condenser: ['Clean the condenser column and removable parts after this use.', 'ack-clean-condenser', 'Mark condenser cleaned'],
  sanitize_seal: ['Before the next attachment, sanitize the gray seal and let it air-dry.', 'ack-sanitize-seal', 'Mark seal sanitized']
};
function renderCare(status) {
  const due=(status.maintenance?.due || []).filter(task=>Object.hasOwn(careTasks,task));
  const notice=$('careDue'), action=$('careAction');
  notice.hidden=due.length===0 && status.maintenance?.available!==false;
  notice.textContent=status.maintenance?.available===false ? 'The device could not read its maintenance record.' : due.map(task=>careTasks[task][0]).join(' ');
  // Clean includes a rinse cycle. Offer the full routine when both are due.
  const task=careTasks[due.includes('clean')?'clean':due[0]];
  action.hidden=!task || !allowedCommand(task[1]);
  action.textContent=task?.[2] || '';
  action.dataset.careCommand=task?.[1] || '';
}
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
      catch (_) { throw new Error('The device returned an unreadable response.'); }
      if (!response.ok) {
        const error = new Error(data.message || data.error || 'Request failed ('+response.status+').');
        error.status = response.status;
        error.code = data.error;
        throw error;
      }
      if (generation !== state.generation) throw new Error('Connection changed.');
      return data;
    } catch (error) {
      if (error.name === 'AbortError') throw new Error('The device did not respond. Check its connection.');
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
  if(!value)statusCapture?.stop('Connection lost');else statusCapture?.render();
  const appearance = $('appearanceRow');
  if (value) $('settingsForm').before(appearance);
  else $('publicOverview').append(appearance);
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

let groupInvite=null, groupResponding=false, groupChecked=0;
function displayGroup(invite) {
  groupInvite=invite?.state==='pending' ? invite : null;
  $('groupInvite').hidden=!groupInvite;
  if(!groupInvite)return;
  const recipe=state.recipes.find(r=>r.id===invite.recipe_id);
  const program=invite.program_name || recipe?.name || programLabels[invite.action] || invite.recipe_id || 'a program';
  const coffee=invite.action==='z_program' && [invite.water_l,invite.temperature_f,invite.intensity,invite.time_seconds].every(Number.isFinite);
  const dose=[];
  if(invite.action==='brew'){
    if(Number.isInteger(invite.selected_abv))dose.push('Strength '+invite.selected_abv+'/10');
    if(Number.isInteger(invite.selected_ibu))dose.push('Bitterness '+invite.selected_ibu+'/10');
  }
  if(coffee)dose.push(Number(invite.water_l.toFixed(2))+' L',temperature(invite.temperature_f),invite.time_seconds/60+' min','Intensity '+invite.intensity+'/15');
  $('groupDetail').textContent=invite.from+' invited this brewer to run '+program+'.'+(dose.length?' '+dose.join(' · '):'');
  $('joinGroup').textContent=coffee?'Review on brewer':'Accept and start';
  $('joinGroup').disabled=groupResponding || state.status?.filter_inserted===false;
  $('declineGroup').disabled=groupResponding;
}
async function respondGroup(op) {
  if(!groupInvite || groupResponding || !state.connected)return;
  groupResponding=true;displayGroup(groupInvite);
  try {const reply=await api('/group-brew','POST',{op,id:groupInvite.id});displayGroup(reply.invite);}
  catch(error){notify(error.message,true);displayGroup(null);}
  finally {groupResponding=false;groupChecked=0;}
}
let stillLink=null, stillPending=false, stillChecked=0;
function renderStillLink() {
  const idle=state.connected && state.status?.ready && !state.status?.busy && !stillPending && !stillLink?.checking;
  const stock=stillLink?.stock_supported && $('stillMode').value==='stock';
  $('stillModeRow').hidden=!stillLink?.stock_supported;
  $('stillStockHelp').hidden=!stock && stillLink?.protocol!=='stock';
  $('stillKeyRow').hidden=$('stillKey').disabled=!!stock;
  $('stillPortRow').hidden=$('stillPort').disabled=!!stock;
  $('stillPair').disabled=!idle || !stillLink;
  $('stillUnpair').hidden=!stillLink?.paired;
  $('stillUnpair').disabled=!idle;
  $('stillLogRow').hidden=!stillLink?.paired;
  $('stillLogging').disabled=!idle;
  $('stillLogging').checked=!!stillLink?.session_logging;
  $('stillLinkStatus').textContent=stillLink?.checking ? 'Verifying the PicoStill connection…' :
    stillLink?.error || (stillLink?.paired ? 'Paired with '+stillLink.host+'.' : 'No paired PicoStill.');
  const sample=stillLink?.sample;
  $('stillSample').hidden=!sample;
  if(sample) {
    const temperatures=['Inlet','Outlet','Pot','Ambient'].map((label,i)=>label+' '+temperature(sample.temperature_c[i]*9/5+32));
    $('stillSample').textContent=[...temperatures,'Pressure '+sample.pressure_psi+' psi',
      'Pump '+(sample.pump_running?'on':'off'),'Cooling fan '+(sample.fan_2_running?'on':'off')].join(' · ');
  }
}
async function readStillLink() {
  stillChecked=Date.now();
  try {stillLink=await api('/picostill');renderStillLink()} catch(error){stillLink=null;renderStillLink();$('stillLinkStatus').textContent=error.message;}
}
async function changeStillLink(method,body) {
  if(stillPending)return;
  stillPending=true;renderStillLink();
  try {stillLink=await api('/picostill',method,body);}
  catch(error){notify(error.message,true);await readStillLink();}
  finally {stillPending=false;renderStillLink();}
}
function updateControls() {
  if(!$('stillLinkSection').hidden)renderStillLink();
  const ready = state.connected && state.status?.ready === true && !state.pending;
  const busy = !!state.status?.busy;
  const stopped = !!state.status?.stopped;
  const recovery=state.status?.recovery;
  const interrupted=!!recovery && (recovery.available===true || recovery.valid===false);
  document.querySelectorAll('[data-encoder]').forEach(button => button.disabled = !ready);
  const careAction=$('careAction');
  careAction.disabled=!ready || busy || (stopped && !careAction.dataset.careCommand?.startsWith('ack-'));
  $('encoderClick').disabled = !ready;
  $('brewRecipe').disabled = !ready || busy || stopped || interrupted;
  $('startRecipe').disabled = !ready || busy || stopped || interrupted || !$('brewRecipe').value;
  $('program').disabled = !ready || busy || stopped || interrupted;
  $('runProgram').disabled = !ready || busy || stopped || interrupted || !$('program').value;
  $('stopButton').disabled = !state.connected || state.pending || !allowedCommand('stop');
  $('clearStop').hidden = !stopped;
  $('clearStop').disabled = !ready || busy;
  $('sessionButtons').hidden=!allowedCommand('pause')&&!allowedCommand('resume');
  for(const command of ['pause','resume']) {
    $(command+'Session').hidden=!allowedCommand(command);
    $(command+'Session').disabled=!ready || stopped || state.status?.['can_'+command]!==true;
  }
  $('recoveryPanel').hidden=!state.connected || !interrupted;
  $('resumeInterrupted').hidden=!allowedCommand('resume-interrupted') || recovery?.valid!==true;
  const resumeBlocked=state.info.model==='z-series' && (!ready || state.status?.filter_inserted===false);
  $('resumeInterrupted').disabled=!state.connected || state.pending || busy || stopped || resumeBlocked || recovery?.available!==true || recovery?.valid!==true;
  $('discardInterrupted').hidden=!allowedCommand('discard-interrupted');
  $('discardInterrupted').disabled=!state.connected || state.pending || busy || !interrupted;
  $('newRecipe').disabled = !ready || busy;
  $('restoreRecipes').disabled = !ready || busy || !state.restoreDefaults;
  $('saveRecipe').disabled = !ready || busy;
  $('deleteRecipe').disabled = !ready || busy || !state.editorId;
  $('saveSettings').disabled = !ready || busy;
  document.querySelectorAll('[data-action]').forEach(button=>button.disabled=!state.connected || state.pending || !allowedCommand(button.dataset.action) || !PicoDevice.accessoryActionAllowed(button.dataset.action,state.status) || !!button.form?.matches(':invalid'));
  $('accessoryStop').disabled=!state.connected || state.pending || !allowedCommand('stop');
  apiExplorer?.update();
  brewHistory?.update();
}



function renderIdentity(info) {
  $('modelName').textContent=PicoDevice.label(info)+' console';
  document.title=PicoDevice.label(info)+' · Pico Local';
}
function applyCapabilities(info) {
  state.info=info;
  const care=Object.hasOwn(careGuides,info.model) ? careGuides[info.model] : null;
  $('careGuide').hidden=!care;
  if(care) {
    $('careAdvice').textContent=care[0];
    $('careManual').href='https://picobrewcontent.blob.core.windows.net/content/'+care[1];
  }
  document.body.classList.toggle("accessory",!hasFeature("brewing"));
  $("recipeRoutingHeader").textContent="Flow path";
  document.querySelectorAll('[data-feature]').forEach(node=>node.hidden=!hasFeature(node.dataset.feature));
  document.querySelectorAll('[data-accessory]').forEach(node=>node.hidden=hasFeature('brewing') ||
    (node.dataset.feature && !hasFeature(node.dataset.feature)));
  const fields=Array.isArray(info.settings_fields)?info.settings_fields:['temperature_unit','language','detailed_status'];
  document.querySelectorAll('[data-setting]').forEach(node=>node.hidden=!fields.includes(node.dataset.setting));
  $('temperatureUnit').closest('.settings-row').hidden=!fields.includes('temperature_unit');
  $('settingsForm').hidden=!fields.length;
  $('restoreRecipes').hidden=info.restore_default_recipes!==true;
  if(state.tab==='recipes'&&!hasFeature('recipes'))selectTab('overview');
  $('stillLinkSection').hidden=info.model!=='z-series'||info.picostill_brewer_link_v1!==true;
  renderActions();apiExplorer?.configure(info);
}
function renderActions() {
  const actions=$('deviceActions'),parameters=$('parameterControls');actions.replaceChildren();parameters.replaceChildren();
  const labels={...PicoDevice.commandLabels,start:hasFeature('fermentation')?'Start recording':'Start manual session'};
  $('controlsHeading').textContent=hasFeature('fermentation')?'Monitor fermentation.':'Run your PicoStill.';
  $('controlHint').textContent=hasFeature('fermentation')?'Readings and session history are recorded on the device.':
    'Prepare the device before starting and review its live sensor and output readings.';
  for(const name of state.capabilities) {
    if(name==='stop'||!labels[name])continue;
    const descriptor=state.info.command_fields?.[name] || {allowed:[],required:[]};
    if(['set-pump','set-fan','set-sampling-interval'].includes(name)) {
      const form=document.createElement('form');form.className='control-form';form.dataset.command=name;
      const heading=document.createElement('h3');heading.textContent=labels[name];form.append(heading);
      const add=(field,label,type,minimum,maximum,value)=>{
        const wrapper=document.createElement('label'),input=document.createElement('input');wrapper.textContent=label;
        input.name=field;input.type=type;input.setAttribute('aria-label',label);input.required=type!=='checkbox';
        if(type==='checkbox')input.checked=!!value;else{input.min=minimum;input.max=maximum;input.step='1';input.value=value;}
        wrapper.append(input);form.append(wrapper);return input;
      };
      if(name==='set-fan') {
        add('channel','Fan channel','number',1,2,1);add('level','Fan level (%)','number',0,100,0);add('enabled','Fan enabled','checkbox',0,0,false);
        const hint=document.createElement('p');hint.id='fanCoolingHint';hint.className='hint';hint.hidden=true;
        hint.textContent='While the pump is running, keep fan 2 on at 61% or higher.';form.append(hint);
        form.elements.level.setAttribute('aria-describedby',hint.id);form.elements.enabled.setAttribute('aria-describedby',hint.id);
      }
      else if(name==='set-pump')add('enabled','Pump enabled','checkbox',0,0,false);
      else add('value','Sampling interval (seconds)','number',60,86400,state.info.history_sample_interval_ms/1000 || '');
      form.addEventListener('input',event=>{
        if(event.target.name==='channel') {delete form.dataset.edited;syncActuatorForm(form);}
        else {
          form.dataset.edited='true';
          if(event.target.name==='level')form.elements.enabled.checked=Number(event.target.value)>0;
        }
        updateControls();
      });
      const button=document.createElement('button');button.type='submit';button.className='button';button.dataset.action=name;button.textContent=labels[name];form.append(button);
      form.addEventListener('submit',event=>{event.preventDefault();if(!form.reportValidity())return;
        const body={command:name};form.querySelectorAll('input').forEach(input=>body[input.name]=input.type==='checkbox'?input.checked:Number(input.value));sendCommand(body).then(accepted=>{if(accepted){delete form.dataset.edited;syncActuatorForm(form);}});});
      parameters.append(form);
    } else {
      const button=document.createElement('button');button.type='button';button.className='button primary';button.dataset.action=name;button.textContent=labels[name];
      button.addEventListener('click',()=>{
        const body={command:name};
        if(name==='calibrate') {
          if(!confirm('Remove PicoFerm from any keg or keg adapter. Remove the GRAY fermentation seal and make sure the device is fully depressurized. Start pressure calibration?'))return;
          body.confirm=true;
        } else if(descriptor.required.includes('confirm')) {
          if(!confirm('Confirm the device is prepared for '+labels[name].toLowerCase()+'.'))return;
          body.confirm=true;
        }
        sendCommand(body);
      });actions.append(button);
    }
  }
  $('accessoryStopRow').hidden=!allowedCommand('stop');updateControls();
}
function syncActuatorForm(form) {
  const name=form.dataset.command;
  if(name==='set-fan') {
    const coolingRequired=form.elements.channel.value==='2' && state.status?.actuators?.some(item=>item.id==='pump' && item.enabled===true);
    form.elements.level.min=coolingRequired?61:0;
    form.elements.enabled.required=!!coolingRequired;
    $('fanCoolingHint').hidden=!coolingRequired;
  }
  if(form.dataset.edited || (name!=='set-fan' && name!=='set-pump'))return;
  const id=name==='set-fan'?'fan'+form.elements.channel.value:'pump';
  const reading=state.status?.actuators?.find(item=>item.id===id);
  if(!reading)return;
  form.elements.enabled.checked=reading.enabled===true;
  if(name==='set-fan')form.elements.level.value=Math.round(reading.level ?? 0);
}
function renderReadings(status) {
  const render=(target,items,actuator)=>{
    const container=$(target);container.replaceChildren();
    for(const item of items) {
      const card=document.createElement('article');card.className='metric';const label=document.createElement('span'),reading=document.createElement('strong'),hint=document.createElement('small');
      label.textContent=item.label || item.id;
      if(actuator) {
        reading.textContent=item.enabled==null?'—':item.enabled?(PicoDevice.finite(item.level)?item.level.toFixed(0)+'%':'On'):'Off';
        hint.textContent=PicoDevice.finite(item.rpm)?item.rpm.toFixed(0)+' rpm':item.kind;
      } else {
        reading.textContent=item.unit==='F'?temperature(item.value):PicoDevice.finite(item.value)?item.value.toFixed(item.unit==='raw'?0:2)+(item.unit==='raw'?'': ' '+item.unit):'—';
        hint.textContent=item.unit==='raw'?'Raw device reading':item.kind;
      }
      card.append(label,reading,hint);container.append(card);
    }
  };
  const sensors=Array.isArray(status.sensors)?status.sensors.slice(0,8):[],actuators=Array.isArray(status.actuators)?status.actuators.slice(0,4):[];
  render('sensorReadings',sensors,false);render('actuatorReadings',actuators,true);
  for(const id of ['sensorTemperature','sensorPressure','sensorBattery'])if(sensors.length)$(id).closest('.metric').hidden=true;
  for(const id of ['pumpOutput','fanOutput'])if(actuators.length)$(id).closest('.metric').hidden=true;
}

function renderStatus(status) {
  state.status = status;
  if (status.model) renderIdentity(status);
  renderReadings(status);
  renderCare(status);
  $('parameterControls').querySelectorAll('form').forEach(syncActuatorForm);
  const recovery=status.recovery;
  $('recoveryHeading').textContent=recovery?.valid===false?'The checkpoint could not be verified.':'Review the saved checkpoint.';
  $('recoveryDetail').textContent=recovery?.valid===false?'Discard the invalid checkpoint before starting another recipe.':
    recovery?.available?(recovery.recipe_name+' · '+(recovery.step_name || 'Preparation')):'';
  $('recoveryTime').textContent=recovery?.available && recovery.valid===true?
    (recovery.step_seconds>0?duration(recovery.step_seconds)+' remaining in the saved step · ':'Preparation or untimed phase · ')+duration(recovery.elapsed_seconds)+' saved elapsed time':'';
  $('sensorTemperature').textContent=temperature(status.temperature_f);
  $('sensorPressure').textContent=PicoDevice.finite(status.pressure_kpa)?status.pressure_kpa.toFixed(2)+' kPa':'—';
  $('sensorBattery').textContent=PicoDevice.finite(status.battery_v)?status.battery_v.toFixed(2)+' V':'—';
  $('wortTemperature').textContent = temperature(status.wort_f);
  $('blockTemperature').textContent = temperature(status.thermoblock_f);
  $('targetTemperature').textContent = status.target_f === 0 ? 'Unheated' : temperature(status.target_f);
  $('stepTime').textContent = duration(status.step_seconds);
  $('phaseName').textContent = state.info.model==='z-series' && !status.busy ? (status.ready?'Idle':'—') : PicoDevice.phaseLabel(state.info.model,status.phase);
  $('operationTitle').textContent = status.stopped ? 'Outputs stopped.' : status.busy ? (status.recipe_name || 'Operation in progress.') : 'Ready for your next brew.';
  $('operationDetail').textContent = status.stopped ? (status.stop_reason === 'filter-removed' ? 'The step filter was removed during pumping. This operation is cancelled. Refit the filter and wait for idle before clearing the stop.' : 'The stop latch is active. Wait for the operation to end before clearing it.') : status.busy ? (status.step_name || 'Follow the brewer’s prompts on the remote front panel.') : 'Choose a saved recipe, open a program, or use the front panel.';
  $('modeBadge').textContent = status.stopped ? 'STOPPED' : !status.ready ? 'NOT READY' : status.busy ? 'IN PROGRESS' : 'READY';
  $('modeBadge').classList.toggle('stopped', !!status.stopped);
  if(hasFeature('brewing') && !status.ready && !status.busy && !status.stopped){
    $('operationTitle').textContent='Waiting for the brewer.';
    $('operationDetail').textContent='Check the front panel for instructions.';
  }
  const reviewRecovery=!!recovery && (recovery.available===true || recovery.valid===false) && !status.busy && !status.stopped;
  $('modeBadge').classList.toggle('warning',reviewRecovery);
  if(reviewRecovery){
    $('operationTitle').textContent=recovery.valid?'An interrupted brew is waiting.':'The saved checkpoint needs attention.';
    $('operationDetail').textContent=recovery.valid?'Review its checkpoint before starting another program.':'Discard the invalid checkpoint before starting another recipe.';
    $('modeBadge').textContent='REVIEW CHECKPOINT';
  }
  $('filterBadge').textContent = status.filter_inserted === null ? 'FILTER UNKNOWN' : status.filter_inserted ? 'FILTER INSERTED' : 'FILTER REMOVED';
  $('filterBadge').classList.toggle('warning', status.filter_inserted === false);
  if(!hasFeature('brewing')) {
    $('operationTitle').textContent=status.stopped?'Operation stopped.':status.state?.replace(/[_-]/g,' ').replace(/^./,letter=>letter.toUpperCase()) || (status.busy?'Session in progress.':'Ready.');
    $('operationDetail').textContent=status.stop_reason && status.stop_reason!=='none'?'Stop reason: '+status.stop_reason:status.busy?(status.state==='sampling'?'Taking a sensor reading.':status.state==='calibrating'?'Pressure calibration in progress.':'The device is running this session.'):'Choose an available device control below.';
  }
  $('filterBadge').hidden=status.filter_inserted==null;
  $('deviceId').textContent = status.device_uid ? 'Device '+status.device_uid : '';
  $('lastUpdated').textContent = 'Updated '+new Date().toLocaleTimeString();
  updateControls();
}

function renderDisplay(frame) {
  const pixels=PicoDevice.decodeDisplay(frame),canvas=$('display');canvas.width=frame.width;canvas.height=frame.height;
  const context=canvas.getContext('2d'),image=context.createImageData(frame.width,frame.height);
  pixels.forEach((brightness,index)=>{
    const light=brightness/255;image.data[index*4]=Math.round(18+181*light);image.data[index*4+1]=Math.round(38+187*light);image.data[index*4+2]=Math.round(32+148*light);image.data[index*4+3]=255;
  });context.putImageData(image,0,0);$('displayHint').textContent='LIVE OLED · '+frame.width+' × '+frame.height;
}

function renderPrograms() {
  $('program').replaceChildren();
  for (const command of state.capabilities) if (programLabels[command]) {
    $('program').add(new Option(command==='sous-vide'&&state.capabilities.includes('sous-vide-no-keg')?'Sous vide with keg':programLabels[command],command));
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

const flowPaths = ['Keg circulation', 'Mash', 'Hop addition 1', 'Hop addition 2', 'Hop addition 3', 'Hop addition 4'];

function programId(name) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'').slice(0,20);
}

function hasOnlyFields(object, allowed) {
  return object && typeof object==='object' && !Array.isArray(object) && Object.keys(object).every(key=>allowed.includes(key));
}

function zProgramStepLimit() {
  return state.info.z_program_step_capacity ?? state.info.step_capacity ?? 15;
}

function validZProgram(program) {
  const name=value=>typeof value==='string' && /^[ -~]{1,20}$/.test(value) && value.trim().length>0;
  const seconds=(value,max)=>Number.isInteger(value) && value>=0 && value<=max;
  return hasOnlyFields(program,['Name','IsBeer','UsePicoPakMode','Steps','IsMetric']) && name(program.Name) &&
    typeof program.IsBeer==='boolean' && typeof program.UsePicoPakMode==='boolean' &&
    (program.IsMetric===undefined || typeof program.IsMetric==='boolean') &&
    Array.isArray(program.Steps) && program.Steps.length>=1 && program.Steps.length<=zProgramStepLimit() &&
    program.Steps.every(s=>s && name(s.Name) && (s.StepType===2
      ? hasOnlyFields(s,['Name','StepType','Time']) && (s.Time===undefined || seconds(s.Time,86400))
      : s.StepType===1 && hasOnlyFields(s,['Name','StepType','Location','Temp','Time','Drain']) &&
        Number.isInteger(s.Location) && s.Location>=1 && s.Location<=6 &&
        (s.Temp===null || Number.isFinite(s.Temp) && s.Temp>=-29 && s.Temp<=98) &&
        seconds(s.Time,86400) && seconds(s.Drain,7200)));
}

function addZStep(step={Name:'Mash',StepType:1,Location:2,Temp:65,Time:3600,Drain:0}) {
  const limit=zProgramStepLimit();
  if ($('steps').children.length>=limit) {notify('This brewer can save up to '+limit+' Program Maker steps.',true);return;}
  const row=$('steps').insertRow();
  function field(tag,label) {
    const control=document.createElement(tag);
    control.setAttribute('aria-label',label);
    row.insertCell().append(control);
    return control;
  }
  const name=field('input','Step name');
  name.maxLength=20;name.pattern='[ -~]{1,20}';name.title='Use printable ASCII characters.';name.required=true;name.value=step.Name;
  const type=field('select','Step type');
  type.add(new Option('Brew step','1'));type.add(new Option('Pause for operator','2'));
  type.value=String(step.StepType);
  const route=field('select','Flow path');
  flowPaths.forEach((label,i)=>route.add(new Option(label,String(i+1))));
  route.value=String(step.Location ?? 1);
  const target=field('input','Target temperature');
  target.type='number';target.step='any';target.placeholder='Unheated';
  let celsius=step.Temp ?? null;
  const fahrenheit=()=>$('zUnits').value==='F';
  row.renderTemperature=()=>{
    target.min=fahrenheit() ? -20.2 : -29;
    target.max=fahrenheit() ? 208.4 : 98;
    target.value=celsius===null ? '' : Number((fahrenheit() ? celsius*1.8+32 : celsius).toFixed(8));
  };
  row.renderTemperature();
  target.addEventListener('input',()=>{
    celsius=target.value==='' ? null : fahrenheit() ? Number(((Number(target.value)-32)/1.8).toFixed(8)) : Number(target.value);
  });
  function time(label,value,limit) {
    const input=field('input',label);
    input.value=value===undefined ? '' : duration(value);
    input.placeholder='min:sec';
    input.pattern='[0-9]+(:[0-5][0-9])?';
    input.seconds=()=>{
      if(input.value==='')return undefined;
      const [minutes,sec='0']=input.value.split(':');
      return Number(minutes)*60+Number(sec);
    };
    input.addEventListener('input',()=>input.setCustomValidity(
      input.seconds()>limit ? 'Maximum '+duration(limit)+' (minutes:seconds).' : ''));
    return input;
  }
  const originalPauseTime=step.StepType===2 ? step.Time : undefined;
  const hold=time('Duration in minutes and seconds',step.StepType===2 ? 0 : step.Time,86400);
  const drain=time('Drain in minutes and seconds',step.Drain ?? 0,7200);
  function updateType() {
    const pause=type.value==='2';
    route.disabled=target.disabled=drain.disabled=hold.disabled=pause;
    hold.hidden=pause;
    pauseNote.hidden=!pause;
    hold.required=!pause;drain.required=!pause;
  }
  const pauseNote=document.createElement('span');
  pauseNote.textContent='Until confirmed';hold.after(pauseNote);
  type.addEventListener('change',updateType);updateType();
  row.readStep=()=>{
    const value={Name:name.value,StepType:Number(type.value)};
    if(value.StepType===2 && originalPauseTime!==undefined)value.Time=originalPauseTime;
    if(value.StepType===1 && hold.value!=='')value.Time=hold.seconds();
    if(value.StepType===1)Object.assign(value,{Location:Number(route.value),Temp:celsius,Drain:drain.seconds()});
    return value;
  };
  const remove=field('button','Remove step');
  remove.type='button';remove.textContent='×';remove.className='remove';
  remove.addEventListener('click',()=>{row.remove();state.dirty=true;});
}

function newZProgram() {
  return {Name:'',IsBeer:true,UsePicoPakMode:false,Steps:[{Name:'Mash',StepType:1,Location:2,Temp:65,Time:3600,Drain:0}]};
}

function zSampleProgram() {
  const active=(Name,Location,Temp,Time,Drain=0)=>({Name,StepType:1,Location,Temp,Time,Drain});
  // Match the seven-stage example in the original Z Program Maker.
  return {Name:'My Sample Recipe',IsMetric:true,IsBeer:true,UsePicoPakMode:false,Steps:[
    active('Heat Water',1,67.1,0),active('Mash',2,67,5400,480),active('Heat to Boil',1,97,0),
    active('Boil Adjunct 1',3,97,2700),active('Boil Adjunct 2',4,97,300),
    active('Boil Adjunct 3',5,97,300),active('Boil Adjunct 4',6,97,300,300)
  ]};
}

function addStep(step={name:'New step',location:1,target_f:152,minutes:20,drain_minutes:0}) {
  if ($('steps').children.length>=15) {notify('A recipe can contain up to 15 steps.',true);return;}
  const row=document.createElement('tr');
  const definitions=[['name','text',null,null],['location','select',1,6],['target_f','number',0,212],['minutes','number',0,599],['drain_minutes','number',0,120]];
  for (const [field,type,min,max] of definitions) {
    const cell=document.createElement('td');let input;
    if(type==='select') {
      input=document.createElement('select');
      flowPaths.forEach((label,index)=>input.add(new Option(label,String(state.zCloud?index:index+1))));
      if(state.zCloud)input.add(new Option('Pause for operator','6'));
    }
    else {input=document.createElement('input');input.type=type;if(type==='text'){input.maxLength=21;input.pattern='[ -~]{1,21}';input.title='Use printable ASCII characters.';}else{input.min=min;input.max=max;input.step=1;}}
    input.dataset.field=field;input.required=true;input.value=step[field] ?? (field==='drain_minutes' ? 0 : '');
    input.setAttribute('aria-label',field.replaceAll('_',' '));cell.append(input);row.append(cell);
  }
  const cell=document.createElement('td');const remove=document.createElement('button');remove.type='button';remove.className='remove';remove.textContent='×';remove.setAttribute('aria-label','Remove step');
  remove.addEventListener('click',()=>{row.remove();state.dirty=true;});cell.append(remove);row.append(cell);$('steps').append(row);
  if(state.zCloud){
    const route=row.querySelector('[data-field=location]');
    const sync=()=>{for(const key of ['target_f','minutes','drain_minutes'])row.querySelector('[data-field='+key+']').disabled=route.value==='6';};
    route.addEventListener('change',sync);sync();
  }
}

function editRecipe(recipe, existing=false) {
  const program=recipe.z_program || (state.info.z_program_maker===true && !recipe.steps ? newZProgram() : null);
  state.zProgram=!!program;
  state.zCloud=!!recipe.z_cloud;
  state.zKit=recipe.z_pak || null;
  $('zKitOptions').hidden=$('zKitOptions').disabled=!state.zKit;
  $('addStep').hidden=!!state.zKit;
  if(state.zKit) {
    for(const [id,key] of [['kitStrength','abv'],['kitBitterness','ibu']]){
      const select=$(id), baseline=state.zKit[key];
      const adjustable=baseline>=1 && baseline<=9 && state.zKit.step_options.some((o,i)=>
        o.action===0 && o.min_time!=null && o.max_time!=null && (key==='abv'?recipe.steps[i].location===2:recipe.steps[i].location>=3));
      $(id+'Row').hidden=!adjustable;
      select.replaceChildren(new Option('Original ('+baseline+' of 10)',''));
      for(let i=0;i<=10;i++)if(i!==baseline)select.add(new Option(i+' of 10',String(i)));
      select.value=state.zKit['selected_'+key]===baseline?'':String(state.zKit['selected_'+key] ?? '');
    }
  }
  $('zCloudOptions').hidden=$('zCloudOptions').disabled=!state.zCloud;
  $('cloudWater').value=state.zCloud ? recipe.z_cloud.start_water_ml/1000 : '';
  $('cloudBeer').checked=recipe.z_cloud?.is_beer===true;
  state.zHasUnit=program?.IsMetric!==undefined;
  $('zStepType').hidden=$('zProgramOptions').hidden=!program;
  $('zUnits').value=state.zHasUnit ? (program.IsMetric ? 'C' : 'F') : state.settings.temperature_unit;
  $('zPak').checked=program?.UsePicoPakMode===true;
  $('zBeer').checked=program?.IsBeer===true;
  $('recipeName').maxLength=program ? 20 : 31;
  $('stepTarget').textContent='Target °'+(program ? $('zUnits').value : 'F');
  $('stepTimeHeading').textContent=program ? 'Hold (min:sec)' : 'Minutes';
  $('stepDrain').textContent=program ? 'Drain (min:sec)' : 'Drain min';
  $('recipeLimits').textContent=program ? 'Up to '+zProgramStepLimit()+' steps. A pause waits for the operator to continue.' : 'Up to 15 steps. A target of 0 means unheated circulation.';
  $('recipeRoutingHint').textContent=program ? 'Import or export Z Program Maker JSON. Use minutes:seconds for exact timing.' : 'Choose the flow path for each step.';
  if(state.zCloud){
    $('recipeRoutingHint').textContent='The brewer uses the saved water quantity during setup. Pauses wait for you to continue.';
    $('recipeLimits').textContent='A target of 0 keeps the previous heat setting. The Z adjusts boiling steps to its local boiling point.';
  }
  if(state.zKit){
    $('recipeRoutingHint').textContent='The Z uses the original PicoPak schedule and adjusts only steps with kit-specific timing ranges.';
    $('recipeLimits').textContent='A target of 0 keeps the previous heat setting.';
  }
  state.editorId=existing ? recipe.id : null;
  state.editorRevision=existing ? recipe.revision : undefined;
  state.dirty=false;
  $('recipeName').value=program?.Name || recipe.name || '';$('recipeId').value=recipe.id || programId($('recipeName').value);$('recipeId').readOnly=existing;
  $('recipeId').dataset.auto=existing ? 'false' : 'true';
  $('editorHeading').textContent=existing ? 'Edit recipe' : 'New recipe';$('steps').replaceChildren();
  if(program)program.Steps.forEach(addZStep);
  else for(const step of recipe.steps || [{name:'Mash',location:2,target_f:152,minutes:60,drain_minutes:0}])addStep(step);
  if(state.zKit)Array.from($('steps').children).forEach((row,i)=>{
    const pause=state.zKit.step_options[i].action===1;
    for(const input of row.querySelectorAll('input,select,button'))input.disabled=true;
    row.querySelector('button').hidden=true;
    if(pause){
      row.querySelector('select').hidden=true;
      row.cells[1].append(document.createTextNode('Pause for operator'));
    }
  });
  updateControls();
}

async function loadRecipe(id) {
  if(state.dirty && !confirm('Discard the unsaved recipe changes?'))return;
  try {const recipe=await api('/recipes/'+encodeURIComponent(id));editRecipe({...recipe,id},true);notify('');}
  catch(error){notify(error.message,true);}
}

function recipeFromForm() {
  if(!$('recipeForm').reportValidity())return null;
  if(state.zProgram) {
    const z_program={Name:$('recipeName').value,IsBeer:$('zBeer').checked,UsePicoPakMode:$('zPak').checked,
      Steps:Array.from($('steps').children,row=>row.readStep())};
    if(state.zHasUnit)z_program.IsMetric=$('zUnits').value==='C';
    if(!validZProgram(z_program)){notify('Check the program names, step count, temperatures and durations.',true);return null;}
    return {id:$('recipeId').value,z_program};
  }
  const steps=Array.from($('steps').children).map(row=>{
    const step={};row.querySelectorAll('[data-field]').forEach(input=>step[input.dataset.field]=input.dataset.field==='name'?input.value:Number(input.value));return step;
  });
  if(!steps.length){notify('Add at least one brewing step.',true);return null;}
  if(state.info.zero_minute_steps!==true && steps.some(step=>step.minutes===0)){notify('This firmware requires at least one minute per step.',true);return null;}
  const minimum=state.zCloud || state.zKit ? 32 : 60;
  if(steps.some(step=>step.target_f!==0 && step.target_f<minimum)){notify('Use 0 or a target between '+minimum+' and 212 °F.',true);return null;}
  const result={id:$('recipeId').value,name:$('recipeName').value,steps};
  if(steps.some((s,i)=>s.target_f>0 && s.target_f<60 &&
    ((state.zCloud && s.location!==6 && steps[i-1]?.location!==6) ||
     (state.zKit && state.zKit.step_options[i].action===0 && state.zKit.step_options[i-1]?.action!==1)))){
    notify('Place an operator pause before a chilling step below 60 °F.',true);return null;
  }
  if(state.zKit){
    result.z_pak={...state.zKit};
    for(const [id,key] of [['kitStrength','selected_abv'],['kitBitterness','selected_ibu']]){
      if($(id).value==='')delete result.z_pak[key];else result.z_pak[key]=Number($(id).value);
    }
  }
  if(state.zCloud)result.z_cloud={start_water_ml:Math.round(Number($('cloudWater').value)*1000),is_beer:$('cloudBeer').checked};
  return result;
}

async function sendCommand(body) {
  if(state.pending || !state.connected || !allowedCommand(body.command))return;
  state.pending=true;updateControls();
  try {
    const reply=await api('/commands','POST',body);
    if(!reply?.accepted || reply.command!==body.command)throw new Error('Command acceptance was not confirmed.');
    notify(body.command==='stop'?'Stop requested.':'Command accepted.');renderStatus(await api('/status'));return true;
  } catch(error){notify(error.message+(!error.status?' Check the device status before repeating the command.':''),true);}
  finally{state.pending=false;updateControls();}
}

function download(name,data) {
  const blob=new Blob([JSON.stringify(data,null,2)+'\n'],{type:'application/json'});
  const url=URL.createObjectURL(blob);const link=document.createElement('a');link.href=url;link.download=name;link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
}

function renderSettings(settings) {
  state.settings={temperature_unit:'F',...settings};$('temperatureUnit').value=state.settings.temperature_unit;$('language').value=settings.language || 'en';$('detailedStatus').checked=!!settings.detailed_status;$('autoCycle').checked=!!settings.auto_cycle;
}

function renderTelemetry(config) {
  state.telemetry=config;
  $('hubDestination').textContent=config.enabled && config.host ?
    'Sending live readings to '+config.host+':'+config.port+'.' : 'No Hub destination configured.';
  const status=config.status || {};
  const labels={disabled:'Add this device in PicoBrew Hub to connect automatically.',offline:'Waiting for the Pico’s network connection.',
    'waiting-idle':'Waiting for the device to be idle to connect.',ready:'Ready to send readings.',
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
    if(!document.hidden || statusCapture?.active()) {
      const status=await api('/status');
      if(generation!==state.generation)return;
      const capture=statusCapture?.due();
      if(capture) {
        try{statusCapture.sample(capture,status,await api('/z/state'));}
        catch(error){if(capture===statusCapture.due())statusCapture.stop('Capture interrupted: '+error.message);}
      }
      if(generation!==state.generation)return;
      renderStatus(status);connected(true);
      if(document.hidden)return;
      if(state.tab==='overview' && hasFeature('front_panel')) {try{renderDisplay(await PicoDevice.readDisplay(api));}catch(error){$('displayHint').textContent=error.message;}}
      if(state.tab==='overview' && state.info.group_brewing_v1 && !groupResponding && Date.now()-groupChecked>=4000){
        groupChecked=Date.now();
        try{displayGroup((await api('/group-brew')).invite);}catch(_){displayGroup(null);}
      }
      if(state.tab==='settings' && !$('stillLinkSection').hidden && !stillPending && Date.now()-stillChecked>=2500)await readStillLink();
      if(state.tab==='settings' && state.info.telemetry_forwarding && Date.now()-state.telemetryAt>=5000)await refreshTelemetry();
    }
  } catch(error) {
    if(generation===state.generation){connected(false);notify(error.message,true);if(error.status===401){state.key='';try{sessionStorage.removeItem('pico-api-key');}catch(_){}}}
  } finally {state.polling=false;if(state.key)pollTimer=setTimeout(poll,state.connected?1200:3000);}
}

function selectTab(tab) {
  if(tab==='recipes'&&!hasFeature('recipes'))return;
  state.tab=tab;document.querySelectorAll('.tab-panel').forEach(panel=>panel.hidden=panel.id!==tab);
  document.querySelectorAll('[data-tab]').forEach(button=>{const active=button.dataset.tab===tab;button.classList.toggle('selected',active);if(active)button.setAttribute('aria-current','page');else button.removeAttribute('aria-current');});
  if(state.connected && tab==='recipes')refreshRecipes().catch(error=>notify(error.message,true));
  if(state.connected && tab==='settings' && !$('stillLinkSection').hidden)void readStillLink();
  if(state.connected && tab==='settings' && state.info.telemetry_forwarding)refreshTelemetry();
  if(tab==='history')brewHistory?.activate();
}

async function connect(key) {
  statusCapture?.stop('Connection changed');
  state.key=key;state.generation++;clearTimeout(pollTimer);
  state.telemetry=null;state.telemetryAt=0;
  const info=await api('');state.capabilities=PicoDevice.commands(info);applyCapabilities(info);renderPrograms();
  renderIdentity(info);
  state.restoreDefaults=info.restore_default_recipes === true;
  $('firmwareVersion').textContent=info.firmware_version ? 'Firmware '+info.firmware_version : 'Standalone firmware';
  renderSettings(await api('/settings'));if(!state.dirty && !state.editorId)editRecipe({});renderStatus(await api('/status'));if(hasFeature('recipes'))await refreshRecipes();
  try{sessionStorage.setItem('pico-api-key',key);}catch(_){}
  connected(true);notify('');$('connectDialog').close();$('apiKey').value='';poll();
}

function disconnect() {
  displayGroup(null);groupChecked=0;stillLink=null;$('stillLinkForm').reset();
  state.generation++;state.key='';state.status=null;clearTimeout(pollTimer);connected(false);
  state.telemetry=null;$('telemetryStatus').textContent='Connect to see Hub connection status.';
  $('hubDestination').textContent='No Hub connection loaded.';
  try{sessionStorage.removeItem('pico-api-key');}catch(_){}
  $('operationTitle').textContent='Disconnected from your device.';$('operationDetail').textContent='A running session continues on the device. Reconnect to see its status.';
  $('apiKey').value='';notify('Device key removed from this tab.');
  publicOverview();
}

document.querySelectorAll('[data-tab]').forEach(button=>button.addEventListener('click',()=>selectTab(button.dataset.tab)));
document.querySelectorAll('[data-encoder]').forEach(button=>button.addEventListener('click',()=>sendCommand({command:'encoder',delta:Number(button.dataset.encoder),click:false})));
$('encoderClick').addEventListener('click',()=>sendCommand({command:'encoder',delta:0,click:true}));
$('brewRecipe').addEventListener('change',updateControls);
$('startRecipe').addEventListener('click',()=>sendCommand({command:'brew',recipe_id:$('brewRecipe').value}));
$('runProgram').addEventListener('click',()=>sendCommand({command:$('program').value}));
$('careAction').addEventListener('click',()=>{const command=$('careAction').dataset.careCommand;if(command)sendCommand({command});});
$('stopButton').addEventListener('click',()=>sendCommand({command:'stop'}));
$('accessoryStop').addEventListener('click',()=>sendCommand({command:'stop'}));
$('clearStop').addEventListener('click',()=>sendCommand({command:'clear-stop'}));
$('pauseSession').addEventListener('click',()=>sendCommand({command:'pause'}));
$('resumeSession').addEventListener('click',()=>sendCommand({command:'resume'}));
$('resumeInterrupted').addEventListener('click',()=>{
  const recovery=state.status?.recovery;
  if(!recovery?.available || recovery.valid!==true)return;
  if(confirm('Verify the water, vessel and filter before resuming. Saved progress can be up to 30 seconds behind the interruption. The brewer resumes with its saved remaining step time and may return to preparation prompts. Resume '+recovery.recipe_name+'?'))
    sendCommand({command:'resume-interrupted',confirm:true});
});
$('discardInterrupted').addEventListener('click',()=>{
  if(confirm('Discard the interrupted-brew checkpoint? This saved operation cannot be resumed afterward.'))sendCommand({command:'discard-interrupted'});
});
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
$('zUnits').addEventListener('change',()=>{
  state.zHasUnit=true;
  $('stepTarget').textContent='Target °'+$('zUnits').value;
  for(const row of $('steps').children)row.renderTemperature();
});
$('zSample').addEventListener('click',()=>{
  if(state.dirty && !confirm('Discard the unsaved recipe changes?'))return;
  editRecipe({z_program:zSampleProgram()});state.dirty=true;
});
$('addStep').addEventListener('click',()=>{if(state.zProgram)addZStep();else addStep();state.dirty=true;});
$('recipeForm').addEventListener('input',()=>{state.dirty=true;});
$('recipeName').addEventListener('input',()=>{if($('recipeId').dataset.auto==='true')$('recipeId').value=programId($('recipeName').value);});
$('recipeId').addEventListener('input',()=>{$('recipeId').dataset.auto='false';});
$('recipeForm').addEventListener('submit',async event=>{
  event.preventDefault();const recipe=recipeFromForm();if(!recipe)return;
  const {id,...body}=recipe;
  try{const saved=await api('/recipes/'+encodeURIComponent(id),'PUT',{...body,...(state.editorId ? {if_revision:state.editorRevision} : {create_only:true})});editRecipe(saved,true);await refreshRecipes();notify('Recipe saved on the Pico.');}
  catch(error){notify(error.message,true);}
});
$('deleteRecipe').addEventListener('click',async()=>{
  if(!state.editorId || !confirm('Delete this recipe from the Pico?'))return;
  try{await api('/recipes/'+encodeURIComponent(state.editorId),'DELETE',state.editorRevision ? {if_revision:state.editorRevision} : undefined);editRecipe({});await refreshRecipes();notify('Recipe deleted.');}
  catch(error){notify(error.message,true);}
});
$('importRecipe').addEventListener('click',()=>$('importFile').click());
function readRecipeFile(text) {
  const recipe=JSON.parse(text);
  // JSON.parse keeps the last repeated property. The firmware rejects ambiguous
  // objects. Track keys in each object after the browser has checked the syntax.
  const keys=[];
  for(const match of text.matchAll(/("(?:\\.|[^"\\])*")\s*:|"(?:\\.|[^"\\])*"|[{}]/g)) {
    if(match[0]==='{')keys.push(new Set());
    else if(match[0]==='}')keys.pop();
    else if(match[1]) {
      const key=JSON.parse(match[1]), current=keys[keys.length-1];
      if(current.has(key))throw new Error('The recipe contains a repeated field: '+key+'.');
      current.add(key);
    }
  }
  // A revision belongs to the source brewer. An imported copy gets a new one.
  if(recipe && Object.hasOwn(recipe,'id') && Number.isInteger(recipe.revision) &&
     recipe.revision>0 && recipe.revision<=0xffffffff)delete recipe.revision;
  const validId=id=>typeof id==='string' && id.length>=1 && id.length<=20 && !/[^a-zA-Z0-9_-]/.test(id);
  if(recipe && Object.hasOwn(recipe,'PicoRecipeID')){
    if(!hasOnlyFields(recipe,['Name','Abv','Ibu','PicoRecipeID','PicoBeerID','Steps']) || !Array.isArray(recipe.Steps) ||
      recipe.Steps.some(s=>!hasOnlyFields(s,['Name','Location','Temp','Time','Drain','Action','MinTime','MaxTime']) ||
        ['Name','Location','Temp','Time','Drain','Action'].some(key=>s[key]==null)))
      throw new Error('Missing or unsupported fields in the original PicoPak.');
    const program={id:'pico-pak-'+recipe.PicoRecipeID,name:recipe.Name,
      steps:recipe.Steps.map(s=>({name:s.Name,location:s.Location,target_f:s.Temp,minutes:s.Time,drain_minutes:s.Drain})),
      z_pak:{recipe_id:recipe.PicoRecipeID,beer_id:recipe.PicoBeerID,abv:recipe.Abv,ibu:recipe.Ibu,
        step_options:recipe.Steps.map(s=>({action:s.Action,...(s.MinTime==null?{}:{min_time:s.MinTime}),...(s.MaxTime==null?{}:{max_time:s.MaxTime})}))}};
    return readRecipeFile(JSON.stringify(program));
  }
  if(recipe && (Object.hasOwn(recipe,'Steps') || Object.hasOwn(recipe,'z_program'))) {
    if(state.info.z_program_maker!==true)throw new Error('This brewer does not support Z Program Maker files.');
    const wrapped=Object.hasOwn(recipe,'z_program');
    const program=wrapped ? recipe.z_program : recipe;
    if(!validZProgram(program))throw new Error('This Z program contains unsupported fields or values.');
    if(wrapped && (!hasOnlyFields(recipe,['id','z_program','name','steps']) || !validId(recipe.id) ||
      ('name' in recipe && recipe.name!==program.Name) || ('steps' in recipe && recipe.steps!==null && !(Array.isArray(recipe.steps) && recipe.steps.length===0)))) {
      throw new Error('The Z program wrapper has conflicting or unsupported fields.');
    }
    return {id:wrapped ? recipe.id : programId(program.Name),z_program:program};
  }
  const label=(value,max)=>typeof value==='string' && value.length>=1 && value.length<=max && !/[^ -~]/.test(value);
  const integer=(value,min,max)=>Number.isInteger(value) && value>=min && value<=max;
  const cloud=recipe && Object.hasOwn(recipe,'z_cloud');
  const kit=recipe && Object.hasOwn(recipe,'z_pak');
  if(kit){
    const p=recipe.z_pak;
    if(cloud || state.info.z_picopak_v1!==true || !hasOnlyFields(p,['recipe_id','beer_id','abv','ibu','selected_abv','selected_ibu','step_options']) ||
      !integer(p.recipe_id,1,0xffffffff) || !integer(p.beer_id,1,0xffffffff) || !integer(p.abv,-1,127) || !integer(p.ibu,-1,127) ||
      !Array.isArray(recipe.steps) || !Array.isArray(p.step_options) || p.step_options.length!==recipe.steps.length ||
      p.step_options.some(o=>!hasOnlyFields(o,['action','min_time','max_time']) || !integer(o.action,0,1) ||
        ['min_time','max_time'].some(k=>k in o && !integer(o[k],0,599)))) throw new Error('This brewer or file does not support the supplied PicoPak schedule.');
    for(const key of ['abv','ibu']){
      if(!('selected_'+key in p))continue;
      const selected=p['selected_'+key];
      if(!integer(selected,0,10) || (selected!==p[key] && (!integer(p[key],1,9) || !p.step_options.some((o,i)=>
        o.action===0 && o.min_time!=null && o.max_time!=null && (key==='abv'?recipe.steps[i]?.location===2:recipe.steps[i]?.location>=3)))))
        throw new Error('This PicoPak has no matching authored adjustment for the selected strength.');
    }
  }
  if(cloud && (state.info.z_cloud_program_v1!==true || !hasOnlyFields(recipe.z_cloud,['start_water_ml','is_beer']) ||
    !integer(recipe.z_cloud.start_water_ml,1,16163) || typeof recipe.z_cloud.is_beer!=='boolean'))
    throw new Error('This Z schedule requires a compatible brewer and a valid starting-water quantity.');
  if(kit && !label(recipe.name,31))throw new Error('Give this kit a device name of up to 31 printable ASCII characters in the Hub, then transfer it.');
  if(!hasOnlyFields(recipe,['id','name','steps','z_cloud','z_pak']) || !validId(recipe.id) || !label(recipe.name,31) ||
     !Array.isArray(recipe.steps) || !integer(recipe.steps.length,1,15))throw new Error('Choose a Pico Local recipe with a valid ID, name, and 1–15 steps.');
  for(const [i,step] of recipe.steps.entries()) {
    if(!hasOnlyFields(step,['name','location','target_f','minutes','drain_minutes']) || !label(step.name,21) ||
       !integer(step.location,cloud?0:1,6) || (step.target_f!==0 && !integer(step.target_f,cloud || kit?32:60,212)) ||
       !integer(step.minutes,state.info.zero_minute_steps===true ? 0 : 1,599) ||
       (cloud && step.location!==6 && step.target_f>0 && step.target_f<60 && recipe.steps[i-1]?.location!==6) ||
       (kit && recipe.z_pak.step_options[i].action===0 && step.target_f>0 && step.target_f<60 && recipe.z_pak.step_options[i-1]?.action!==1) ||
       ('drain_minutes' in step && !integer(step.drain_minutes,0,120)))throw new Error('Step '+(i+1)+' contains unsupported fields or values.');
  }
  return recipe;
}

async function importRecipeFile(file) {
  if(!file)return;
  try{
    if(file.size>16384)throw new Error('Recipe file is too large.');
    const recipe=readRecipeFile(await file.text());
    if(state.dirty && !confirm('Discard the unsaved recipe changes?'))return;
    editRecipe(recipe,false);state.dirty=true;notify('Recipe imported into the editor. Review it, then save to the Pico.');
  }catch(error){notify(error instanceof SyntaxError?'The file is not valid JSON.':error.message,true);}
}
$('importFile').addEventListener('change',event=>{
  const file=event.target.files[0];event.target.value='';importRecipeFile(file);
});
$('importRecipe').addEventListener('dragover',event=>event.preventDefault());
$('importRecipe').addEventListener('drop',event=>{
  event.preventDefault();importRecipeFile(event.dataTransfer.files[0]);
});
$('exportRecipe').addEventListener('click',()=>{const recipe=recipeFromForm();if(recipe)download(recipe.id+'.json',recipe.z_program || recipe);});
$('stillMode').addEventListener('change',renderStillLink);
$('stillLinkForm').addEventListener('submit',event=>{
  event.preventDefault();
  const body={host:$('stillHost').value.trim(),port:Number($('stillPort').value),uid:$('stillUID').value.toLowerCase(),key:$('stillKey').value.toLowerCase()};
  if(stillLink?.stock_supported && $('stillMode').value==='stock'){body.protocol='stock';body.port=80;delete body.key;}
  $('stillKey').value='';
  void changeStillLink('PUT',body);
});
$('stillUnpair').addEventListener('click',()=>void changeStillLink('DELETE'));
$('joinGroup').addEventListener('click',()=>void respondGroup('accept'));
$('declineGroup').addEventListener('click',()=>void respondGroup('decline'));
$('stillLogging').addEventListener('change',event=>void changeStillLink('PATCH',{session_logging:event.target.checked}));
$('settingsForm').addEventListener('submit',async event=>{
  event.preventDefault();try{const available={temperature_unit:$('temperatureUnit').value,language:$('language').value,detailed_status:$('detailedStatus').checked,auto_cycle:$('autoCycle').checked};const keys=state.info.settings_fields || ['temperature_unit','language','detailed_status'];const patch=Object.fromEntries(keys.filter(key=>key in available).map(key=>[key,available[key]]));const settings=await api('/settings','PATCH',patch);renderSettings(settings);if(state.status)renderStatus(state.status);notify('Preferences saved on the Pico.');}catch(error){notify(error.message,true);}
});
document.addEventListener('visibilitychange',()=>{if(!document.hidden && state.key)poll();});
statusCapture=setupStatusCapture({session:()=>({supported:state.info.z_state_v1===true,uid:state.status?.device_uid,connected:state.connected}),download,refresh:poll});
apiExplorer=setupApiExplorer({session:()=>({key:state.key,connected:state.connected,generation:state.generation}),enqueue:task=>{
  const result=requestQueue.catch(()=>{}).then(task);requestQueue=result;return result;
}});
brewHistory=setupHistory({session:()=>({key:state.key,connected:state.connected,generation:state.generation,temperature_unit:state.settings.temperature_unit,brewing:hasFeature('brewing'),model:state.info.model}),enqueue:task=>{
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
    $('publicState').textContent = view.busy ? 'Operation in progress' : view.ready ? 'Idle' : 'Starting up';
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
