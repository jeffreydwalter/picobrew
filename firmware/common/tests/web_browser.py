#!/usr/bin/env python3
"""Exercise real embedded browser assets against authenticated local API fixtures.

Requires Playwright and CHROMIUM_PATH. No device or cloud service is contacted.
"""
import functools
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import re
from pathlib import Path
import threading
from playwright.sync_api import sync_playwright, expect

ROOT = Path(__file__).resolve().parents[1]
KEY = '0123456789abcdef' * 4
FIELDS = ['elapsed_ms','wort_f','thermoblock_f','target_f','step_seconds','phase','controller_status','compartment','current_step','flags','device_state','temperature_f','pressure_kpa','battery_v','sensor_pressure_native']

class Quiet(SimpleHTTPRequestHandler):
    def log_message(self, *_args): pass

def main():
    output = Path(os.environ.get('WEB_TEST_OUTPUT', '/tmp/pico-common-browser'))
    output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(Quiet, directory=str(ROOT / 'web')))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    checks = []
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=os.environ.get('CHROMIUM_PATH'), args=['--no-sandbox'])
            for model in ['picoferm','picostill','z-series']:
                for width in [1280,390]:
                    context = browser.new_context(viewport={'width':width,'height':900})
                    page = context.new_page()
                    errors, requests, commands = [], [], []
                    page.on('pageerror', lambda error: errors.append(str(error)))
                    brewing = model == 'z-series'
                    allowed = ['brew','clean','drain','chill','stop','pause','resume','encoder','resume-interrupted','discard-interrupted'] if brewing else ['start','stop','calibrate','sample','set-sampling-interval'] if model == 'picoferm' else ['start','essential-oils','clean','stop','set-pump','set-fan']
                    descriptors = {name:{'allowed':[],'required':[]} for name in allowed}
                    for name,fields in {'brew':['recipe_id'],'encoder':['delta','click'],'calibrate':['confirm'],'resume-interrupted':['confirm'],'set-sampling-interval':['value'],'set-pump':['enabled'],'set-fan':['channel','level','enabled']}.items():
                        if name in descriptors: descriptors[name]={'allowed':fields,'required':[] if name=='encoder' else fields[:2] if name=='set-fan' else fields}
                    info = {'api_version':1,'mode':'standalone','model':model,'model_name':{'z-series':'Pico Z','picoferm':'PicoFerm','picostill':'PicoStill'}[model],'model_family':model,'firmware_version':'1.2.0-fixture','commands':allowed,'command_fields':descriptors,'settings_fields':['temperature_unit','language','detailed_status'] if brewing else ['temperature_unit'],'recipe_capacity':16 if brewing else 0,'step_capacity':15 if brewing else 0,'front_panel':brewing,'telemetry_forwarding':True,'capabilities':{'recipes':brewing,'brewing':brewing,'heating':brewing,'front_panel':brewing,'temperature':True,'pressure':not brewing,'battery':model=='picoferm','fermentation':model=='picoferm','pump':model!='picoferm','fans':model=='picostill'}}
                    info['z_program_maker']=brewing
                    info['zero_minute_steps']=brewing
                    info['history_sample_interval_ms']=720000 if model=='picoferm' else 15000
                    saved_recipes={}
                    sensors = [{'id':'temperature','label':'Temperature','kind':'temperature','unit':'F','value':68.5},{'id':'pressure_native','label':'Pressure','kind':'pressure','unit':'raw','value':532.125},{'id':'battery','label':'Battery','kind':'voltage','unit':'V','value':3.8}] if model=='picoferm' else [{'id':'inlet','label':'Inlet','kind':'temperature','unit':'F','value':152.1},{'id':'outlet','label':'Outlet','kind':'temperature','unit':'F','value':145.2},{'id':'pot','label':'Pot','kind':'temperature','unit':'F','value':170.4},{'id':'ambient','label':'Ambient','kind':'temperature','unit':'F','value':68.1},{'id':'pressure','label':'Pressure','kind':'pressure','unit':'kPa','value':6.89}]
                    actuators=[] if model!='picostill' else [{'id':'pump','label':'Pump','kind':'pump','enabled':False,'level':None,'rpm':None},{'id':'fan1','label':'Fan 1','kind':'fan','enabled':True,'level':25,'rpm':1200},{'id':'fan2','label':'Fan 2','kind':'fan','enabled':False,'level':0,'rpm':0}]
                    status={'model':model,'model_name':info['model_name'],'model_family':model,'ready':True,'busy':False,'stopped':False,'state':'idle','device_uid':'fixture-device','command':'idle','device_state':0,'phase':4,'current_step':-1,'step_count':0,'step_seconds':0,'wort_f':72 if brewing else None,'thermoblock_f':73 if brewing else None,'target_f':0 if brewing else None,'temperature_f':68.5,'pressure_kpa':None if model=='picoferm' else 6.89,'battery_v':3.8 if model=='picoferm' else None,'filter_inserted':None,'sensors':[] if brewing else sensors,'actuators':actuators}
                    history={'available':True,'id':1,'state':'completed','command':'start','record_count':3,'persisted_record_count':3,'duration_ms':30000,'first_elapsed_ms':0,'gap_count':0,'truncated':False,'fields':FIELDS[:-1]+['sensor_current_schema']}
                    newer=dict(history,id=2,sample_interval_ms=300000)
                    older=dict(history,id=1,sample_interval_ms=60000)
                    history.update(id=2,sample_interval_ms=300000,sessions=[newer,older])
                    status['recovery']={'available':True,'valid':True,'recipe_name':'Saved pale ale','step_name':'','current_step':-1,'step_seconds':0,'elapsed_seconds':125} if brewing else None
                    rows=[[i*15000,None,None,None,0,0,0,0,255,3,2,68.5+i,None,3.8,532.125+i] for i in range(3)]
                    hub_launch=width==390
                    ticket="43"*32 if hub_launch else None
                    claims=0
                    def route_api(route):
                        nonlocal ticket,claims
                        request=route.request;path=request.url.split('/api/v1',1)[1];requests.append((request.method,path))
                        public=path in ('/overview','/pairing/request','/pairing/claim')
                        assert request.headers.get('authorization')==(None if public else 'Bearer '+KEY)
                        code=200
                        if path=='/overview':
                            result={'model':model,'model_name':info['model_name'],'ready':True,'busy':False}
                            if brewing:result['pairing_v1']=True
                        elif path=='/pairing/request':
                            assert brewing and not hub_launch and request.method=='POST'
                            ticket=request.post_data_json['nonce']
                            assert re.fullmatch('[a-f0-9]{64}',ticket)
                            result={'state':'pending','code':ticket[:8]};code=202
                        elif path=='/pairing/claim':
                            assert ticket and request.post_data_json=={'nonce':ticket}
                            claims+=1
                            if brewing and not hub_launch and claims==1:result={'state':'pending'};code=202
                            else:result={'api_key':KEY};ticket=None
                        elif path=='': result=info
                        elif path=='/status':result=status
                        elif path=='/settings':result={'temperature_unit':'F','language':'en','detailed_status':False} if brewing else {'temperature_unit':'F'}
                        elif path=='/recipes':
                            result={'capacity':16,'recipes':[{'id':key,'name':program['Name'],'steps':len(program['Steps'])} for key,program in saved_recipes.items()]}
                        elif path.startswith('/recipes/'):
                            key=path.rsplit('/',1)[1]
                            if request.method=='PUT':
                                payload=request.post_data_json
                                assert payload.get('create_only') == (True if key not in saved_recipes else None)
                                saved_recipes[key]=payload['z_program']
                            result={'id':key,'z_program':saved_recipes[key]}
                        elif path=='/telemetry':result={'enabled':False,'host':'','port':8080,'status':{'state':'disabled'}}
                        elif path=='/commands':
                            body=request.post_data_json;commands.append(body);result={'accepted':True,'command':body['command']};code=202
                            if body['command'] in ('resume-interrupted','discard-interrupted'):status['recovery'].update(available=False,valid=True)
                        elif path=='/history':result=history
                        elif path.startswith('/history/samples/'):
                            offset=int(path.split('/samples/')[1].split('?')[0]);sample=rows[offset:offset+2]
                            requested_id=int(path.split('?id=')[1])
                            result={'id':requested_id,'offset':offset,'next_offset':offset+len(sample) if offset+len(sample)<len(rows) else None,'fields':FIELDS,'field_metadata':{'sensor_pressure_native':{'label':'Native pressure','unit':'raw','kind':'pressure'}},'samples':sample}
                        elif path.startswith('/display'):
                            offset=0 if path=='/display' else int(path.split('/')[2].split('?')[0]);assert offset==0 or path.endswith('?revision=7')
                            result={'width':128,'height':64,'rotation':0,'format':'row-gray4-msb-hex','revision':7,'total_bytes':4096,'offset':offset,'next_offset':offset+1024 if offset<3072 else None,'data':'0123456789abcdef'*128}
                        else:raise AssertionError('Unexpected API request '+path)
                        route.fulfill(status=code,content_type='application/json',body=json.dumps(result))
                    page.route('**/api/v1**',route_api)
                    url=f'http://127.0.0.1:{server.server_port}/'
                    page.goto(url+('#pair='+ticket if hub_launch else ''))
                    if hub_launch:
                        expect(page).to_have_url(url)
                    else:
                        expect(page.locator('#publicState')).to_have_text('Idle')
                        expect(page.get_by_label('Device API key',exact=True)).to_be_hidden()
                        page.locator('#connectButton').click()
                        if brewing:
                            page.locator('#pairButton').click()
                            expect(page.locator('#connectError')).to_contain_text('Settings → About')
                        else:
                            expect(page.locator('#pairButton')).to_be_hidden()
                            page.get_by_label('Device API key',exact=True).fill(KEY)
                            page.locator('#submitConnect').click()
                    expect(page.locator('#connectionText')).to_have_text('Connected')
                    care={'z-series':'every three brews','picoferm':'gray fermentation seal','picostill':'condenser column'}
                    expect(page.locator('#careAdvice')).to_contain_text(care[model])
                    expect(page.locator('#careManual')).to_have_attribute('href',re.compile(r'^https://picobrewcontent\.blob\.core\.windows\.net/content/.+\.pdf#page=\d+$'))
                    assert commands==[], 'Connecting caused a mutation'
                    if brewing:
                        expect(page.locator('#displayHint')).to_have_text('LIVE OLED · 128 × 64')
                        pixels=page.locator('#display').evaluate('(canvas)=>Array.from(canvas.getContext("2d").getImageData(0,0,16,1).data)')
                        assert pixels[:4]==[18,38,32,255] and pixels[-4:]==[199,225,180,255]
                        assert len([path for _,path in requests if path.startswith('/display')])>=4
                        assert set(page.locator('#program option').all_text_contents())=={'Clean','Drain','Chill'}
                        expect(page.get_by_role('button',name='Pause session',exact=True)).to_be_visible()
                        expect(page.get_by_role('button',name='Resume session',exact=True)).to_be_visible()
                        expect(page.get_by_role('button',name='Pause session',exact=True)).to_be_disabled()
                        expect(page.locator('#runProgram')).to_be_disabled()
                        expect(page.locator('#recoveryDetail')).to_have_text('Saved pale ale · Preparation')
                        expect(page.locator('#operationTitle')).to_have_text('An interrupted brew is waiting.')
                        assert page.evaluate('document.documentElement.scrollWidth<=innerWidth+1'), 'Recovery panel overflow'
                        page.screenshot(path=str(output/f'recovery-{width}.png'),full_page=True)
                        for key,value in [('ready',False),('filter_inserted',False)]:
                            previous=status[key];status[key]=value
                            expect(page.locator('#resumeInterrupted')).to_be_disabled()
                            status[key]=previous
                            expect(page.locator('#resumeInterrupted')).to_be_enabled()
                        assert commands==[], 'Recovery checks caused a mutation'
                        dialogs=[]
                        def cancel_recovery(dialog):dialogs.append(dialog.message);dialog.dismiss()
                        page.once('dialog',cancel_recovery);page.locator('#resumeInterrupted').click()
                        assert commands==[] and all(word in dialogs[0] for word in ['water','vessel','filter','30 seconds','saved remaining'])
                        page.once('dialog',lambda dialog:dialog.accept());page.locator('#resumeInterrupted').click()
                        expect(page.locator('#recoveryPanel')).to_be_hidden()
                        assert commands[-1]=={'command':'resume-interrupted','confirm':True}
                        status['recovery'].update(available=False,valid=False)
                        expect(page.locator('#recoveryHeading')).to_have_text('The checkpoint could not be verified.')
                        expect(page.locator('#operationTitle')).to_have_text('The saved checkpoint needs attention.')
                        expect(page.locator('#resumeInterrupted')).to_be_hidden()
                        expect(page.locator('#runProgram')).to_be_disabled()
                        page.once('dialog',lambda dialog:dialog.dismiss());page.locator('#discardInterrupted').click();assert len(commands)==1
                        page.once('dialog',lambda dialog:dialog.accept());page.locator('#discardInterrupted').click()
                        expect(page.locator('#recoveryPanel')).to_be_hidden()
                        assert commands[-1]=={'command':'discard-interrupted'}
                        expect(page.locator('#runProgram')).to_be_enabled()
                        page.locator('[data-tab="recipes"]').click()
                        page.locator('#newRecipe').click()
                        expect(page.locator('#zProgramOptions')).to_be_visible()
                        for field, invalid, valid in [
                            (page.locator('#recipeName'),'Café','Cafe'),
                            (page.get_by_label('Step name',exact=True).first,'Mäsh','Mash'),
                            (page.locator('#recipeId'),'bad/id','valid_id-1')
                        ]:
                            field.fill(invalid)
                            assert field.evaluate('(input)=>input.validity.patternMismatch')
                            field.fill(valid)
                            assert field.evaluate('(input)=>input.checkValidity()')
                        page.locator('#zUnits').select_option('C')
                        with page.expect_download() as created:
                            page.locator('#exportRecipe').click()
                        assert json.loads(created.value.path().read_text())['IsMetric'] is True
                        page.once('dialog',lambda dialog:dialog.accept())
                        page.locator('#zSample').click()
                        expect(page.locator('#steps tr')).to_have_count(7)
                        original={'Name':'Original hops','IsBeer':False,'UsePicoPakMode':True,'IsMetric':True,
                                  'Steps':[{'Name':'Fourth compartment','StepType':1,'Location':6,'Temp':67.1,'Time':5401,'Drain':61},
                                           {'Name':'Add berries','StepType':2},{'Name':'Cold','StepType':1,'Location':1,'Temp':None,'Time':86400,'Drain':7200}]}
                        page.once('dialog',lambda d:d.accept())
                        page.locator('#importFile').set_input_files({'name':'program.json','mimeType':'application/json','buffer':json.dumps(original).encode()})
                        expect(page.locator('#steps tr')).to_have_count(3)
                        expect(page.get_by_label('Duration in minutes and seconds').first).to_have_value('90:01')
                        expect(page.get_by_label('Flow path').nth(1)).to_be_disabled()
                        page.locator('#zUnits').select_option('F')
                        expect(page.locator('#stepTimeHeading')).to_have_text('Hold (min:sec)')
                        expect(page.get_by_label('Target temperature').first).to_have_value('152.78')
                        page.locator('#zUnits').select_option('C')
                        with page.expect_download() as downloaded:
                            page.locator('#exportRecipe').click()
                        assert json.loads(downloaded.value.path().read_text()) == original
                        def import_wire(wire):
                            page.locator('#importFile').set_input_files({'name':'recipe.json','mimeType':'application/json','buffer':wire.encode()})
                        base_step={'name':'Mash','location':2,'target_f':152,'minutes':60}
                        def generic(step):return json.dumps({'id':'test','name':'Test','steps':[step]})
                        for bad in [
                            generic(dict(base_step,minutes=None)),
                            generic(dict(base_step,heating_ramp=10)),
                            generic({k:v for k,v in base_step.items() if k!='target_f'}),
                            generic(base_step).replace('"minutes": 60','"minutes": 1,"minutes": 60'),
                            json.dumps({'id':'wrong','z_program':original,'steps':[base_step]})]:
                            import_wire(bad)
                            expect(page.locator('#notice')).to_contain_text(re.compile('unsupported|repeated|conflicting'))
                            expect(page.locator('#recipeName')).to_have_value(original['Name'])
                        page.once('dialog',lambda d:d.accept())
                        import_wire(json.dumps({'id':'native-id','z_program':original}))
                        expect(page.locator('#recipeId')).to_have_value('native-id')
                        page.locator('#saveRecipe').click()
                        expect(page.locator('#notice')).to_contain_text('Recipe saved')
                        assert saved_recipes['native-id']==original
                        page.locator('#recipeList button').first.click()
                        expect(page.locator('#editorHeading')).to_have_text('Edit recipe')
                        expect(page.locator('#notice')).to_be_hidden()
                        import_wire(generic(dict(base_step,target_f=0,minutes=0)))
                        expect(page.locator('#notice')).to_contain_text('Recipe imported')
                        with page.expect_download() as native_download:
                            page.locator('#exportRecipe').click()
                        exported=json.loads(native_download.value.path().read_text())
                        assert exported['steps']==[dict(base_step,target_f=0,minutes=0,drain_minutes=0)]
                        page.locator('[data-tab="overview"]').click()
                        status['ready']=False
                        expect(page.locator('#runProgram')).to_be_disabled()
                        expect(page.locator('#stopButton')).to_be_enabled()
                        expect(page.locator('#operationTitle')).to_have_text('Waiting for the brewer.')
                        pause_button=page.locator('#pauseSession')
                        resume_button=page.locator('#resumeSession')
                        status.update(ready=True,busy=True,state='Starting',phase=3)
                        expect(page.locator('#phaseName')).to_have_text('Holding temperature')
                        expect(pause_button).to_be_disabled()
                        expect(resume_button).to_be_disabled()
                        status['state']='Running'
                        expect(pause_button).to_be_enabled()
                        expect(resume_button).to_be_enabled()
                        status['state']='Stopping'
                        expect(pause_button).to_be_disabled()
                        expect(resume_button).to_be_disabled()
                    else:
                        expect(page.locator('#recoveryPanel')).to_be_hidden()
                        expect(page.locator('[data-tab="recipes"]')).to_be_hidden()
                        expect(page.locator('[data-feature="front_panel"]')).to_be_hidden()
                        expect(page.get_by_text('Thermoblock',exact=True)).to_be_hidden()
                        assert not any(path.startswith(('/display','/recipes')) for _,path in requests)
                        if model=='picoferm':
                            expect(page.get_by_label('Sampling interval (seconds)')).to_have_value('720')
                            dialogs=[]
                            def cancel(dialog):dialogs.append(dialog.message);dialog.dismiss()
                            page.once('dialog',cancel);page.get_by_role('button',name='Calibrate pressure',exact=True).click();assert commands==[]
                            assert 'GRAY' in dialogs[0] and 'depressurized' in dialogs[0]
                            page.once('dialog',lambda dialog:dialog.accept());page.get_by_role('button',name='Calibrate pressure',exact=True).click()
                            expect(page.locator('#notice')).to_contain_text('accepted')
                            assert commands[-1]=={'command':'calibrate','confirm':True}
                            page.locator('[name="value"]').fill('900');page.get_by_role('button',name='Set sampling interval',exact=True).click()
                            expect(page.locator('#notice')).to_contain_text('accepted');assert commands[-1]=={'command':'set-sampling-interval','value':900}
                            status.update(ready=False,busy=False,state='sensor-fault')
                            expect(page.locator('#modeBadge')).to_have_text('NOT READY')
                            expect(page.get_by_role('button',name='Take a reading',exact=True)).to_be_enabled()
                            expect(page.get_by_role('button',name='Start recording',exact=True)).to_be_disabled()
                            status.update(ready=True,busy=True,state='recording')
                            expect(page.get_by_role('button',name='Set sampling interval',exact=True)).to_be_disabled()
                        else:
                            expect(page.get_by_role('button',name='Set pump',exact=True)).to_be_disabled()
                            status.update(busy=True,state='running')
                            expect(page.get_by_role('button',name='Set pump',exact=True)).to_be_enabled()
                            expect(page.get_by_label('Fan level (%)',exact=True)).to_have_value('25')
                            expect(page.get_by_label('Fan enabled',exact=True)).to_be_checked()
                            page.get_by_role('button',name='Set pump',exact=True).click();expect(page.locator('#notice')).to_contain_text('accepted');assert commands[-1]=={'command':'set-pump','enabled':False}
                            page.get_by_label('Fan channel',exact=True).fill('2')
                            expect(page.get_by_label('Fan level (%)',exact=True)).to_have_value('0')
                            expect(page.get_by_label('Fan enabled',exact=True)).not_to_be_checked()
                            page.get_by_label('Fan level (%)',exact=True).fill('35')
                            expect(page.get_by_label('Fan enabled',exact=True)).to_be_checked()
                            page.get_by_role('button',name='Set fan',exact=True).click()
                            expect(page.locator('#notice')).to_contain_text('accepted');assert commands[-1]=={'command':'set-fan','channel':2,'level':35,'enabled':True}
                            actuators[0]['enabled']=True
                            expect(page.locator('#fanCoolingHint')).to_be_visible()
                            fan_level=page.get_by_label('Fan level (%)',exact=True)
                            fan_enabled=page.get_by_label('Fan enabled',exact=True)
                            apply_fan=page.get_by_role('button',name='Set fan',exact=True)
                            fan_level.fill('60');expect(apply_fan).to_be_disabled()
                            fan_level.fill('61');expect(apply_fan).to_be_enabled()
                            fan_enabled.uncheck();expect(apply_fan).to_be_disabled()
                            fan_enabled.check();apply_fan.click()
                            expect(page.locator('#notice')).to_contain_text('accepted');assert commands[-1]=={'command':'set-fan','channel':2,'level':61,'enabled':True}
                            page.get_by_label('Fan channel',exact=True).fill('1')
                            expect(page.locator('#fanCoolingHint')).to_be_hidden()
                            expect(apply_fan).to_be_enabled()
                    page.screenshot(path=str(output/f'{model}-{width}.png'),full_page=True)
                    assert page.evaluate('document.documentElement.scrollWidth<=innerWidth+1'), 'Horizontal page overflow'
                    page.locator('[data-tab="history"]').click()
                    expect(page.locator('#historySummary')).to_contain_text('3 readings')
                    page.locator('#historySession').select_option('1')
                    with page.expect_download() as download:page.locator('#historyExportJson').click()
                    exported=json.loads(Path(download.value.path()).read_text())
                    assert exported['metadata']['id']==1 and exported['metadata']['sample_interval_ms']==60000
                    if brewing:expect(page.locator('#historyReadout')).to_contain_text('Paused')
                    page.locator('#historyMetric').select_option('sensor_pressure_native')
                    expect(page.locator('#historyReadout')).to_contain_text('Native pressure')
                    assert page.locator('#historyMetric option').all_text_contents().count('Native pressure')==1
                    assert 'sensor_pressure_native' in page.locator('#historyValues').text_content()
                    page.locator('[data-tab="settings"]').click()
                    if not brewing:expect(page.get_by_label('Front-panel language',exact=True)).to_be_hidden()
                    page.locator('[data-tab="api"]').click()
                    options=page.locator('#apiEndpoint option').all_text_contents()
                    assert any('/display/{offset}' in option for option in options)==brewing
                    if not brewing:assert not any('/recipes' in option or '/display' in option for option in options)
                    assert not errors, errors
                    checks.append({'model':model,'viewport':width,'requests':len(requests),'commands':commands,'errors':errors})
                    context.close()
            browser.close()
    finally:
        server.shutdown();server.server_close()
    (output/'results.json').write_text(json.dumps({'cases':checks,'hardware_contacted':False},indent=2)+'\n')
    print(f'{len(checks)} embedded website browser cases passed; results in {output}')

if __name__=='__main__':main()
