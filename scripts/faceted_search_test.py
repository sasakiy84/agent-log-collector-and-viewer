#!/usr/bin/env python3
"""Combined session/skill facets, fixed time ranges, analysis and scoped export."""
import io,json,os,pathlib,socket,subprocess,tempfile,time,urllib.request,zipfile
from playwright.sync_api import sync_playwright,expect
from test_support import settled_urlopen
binary=os.getenv('AGENTLOG_BIN','./agentlog');token='synthetic-faceted-search-token'
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}'
 proc=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:urllib.request.urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  def record(source,session,project,when,name,attributes,origin='otel'):
   values={'service.name':source,'service.namespace':project,'session.id':session,**attributes}
   if origin=='import':values['agentlog.origin']='import'
   # Synthetic whole seconds; production timestamps retain nanosecond precision.
   from datetime import datetime
   nano=str(int(datetime.fromisoformat(when.replace('Z','+00:00')).timestamp())*1000000000)
   return {'resource':{'attributes':[{'key':'service.name','value':{'stringValue':source}}]},'scopeLogs':[{'logRecords':[{'timeUnixNano':nano,'eventName':name,'attributes':[{'key':k,'value':{'stringValue':str(v)}} for k,v in values.items()]}]}]}
  rows=[record('codex','a','A','2026-11-14T00:00:00Z','codex.user_prompt',{'prompt':'hello alpha 認証'}),record('codex','a','A','2026-11-14T12:00:00Z','gen_ai.tool.result',{'tool_name':'exec_command','output':'alpha beta failure','error':'failure'}),record('codex','a','A','2026-11-14T13:00:00Z','codex.skill_invocation',{'skill.name':'review','turn.id':'a'}),record('claude','b','B','2026-11-14T02:00:00Z','gen_ai.user.message',{'prompt':'hello beta 認証'}),record('codex','old','A','2026-11-01T00:00:00Z','codex.user_prompt',{'prompt':'old alpha'}),record('codex','old','A','2026-11-01T01:00:00Z','codex.skill_invocation',{'skill.name':'review','turn.id':'old'}),record('codex','d','B','2026-11-14T03:00:00Z','codex.user_prompt',{'prompt':'hello delta'}),record('codex','d','B','2026-11-14T04:00:00Z','gen_ai.tool.start',{'tool_name':'Read','arguments':'{"file_path":"/work/skills/review/SKILL.md"}'},'import'),record('copilot','e','C','2026-11-14T05:00:00Z','gen_ai.user.message',{'prompt':'hello alpha'})]
  req=urllib.request.Request(base+'/v1/logs',json.dumps({'resourceLogs':rows}).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
  with settled_urlopen(req) as response:assert response.status==200
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox'])
   page=browser.new_page(viewport={'width':1440,'height':1000},timezone_id='Asia/Tokyo');errors=[];analysis=[]
   page.on('pageerror',lambda e:errors.append(str(e)));page.on('request',lambda r:analysis.append(r.url) if '/api/search/summary' in r.url else None)
   def reveal(selector):
    if not page.locator(selector).evaluate('(e)=>e.open'):page.locator(selector+'>summary').click()
   def time_picker():reveal('#searchtime')
   def text_options():reveal('#searchoptions')
   def facets():reveal('#facetoptions')
   page.goto(base);page.locator('#token').fill(token);page.locator('#loginform button').click();expect(page.locator('.session')).to_have_count(5);assert not analysis
   time_picker();page.locator('#searchrange').select_option('custom');page.locator('#searchfrom').fill('2026-11-14T09:00');page.locator('#searchto').fill('2026-11-15T09:00');page.locator('#applysearchfilters').click();expect(page.locator('.session')).to_have_count(4);assert 'from=2026-11-14T00' in page.url
   # From is inclusive, To is exclusive, and the UI can distinguish adjacent seconds.
   time_picker();page.locator('#searchto').fill('2026-11-14T11:00');page.locator('#applysearchfilters').click();expect(page.locator('.session')).to_have_count(1)
   time_picker();page.locator('#searchfrom').fill('2026-11-14T09:00:01');page.locator('#applysearchfilters').click();expect(page.locator('.session')).to_have_count(0);assert '00%3A00%3A01Z' in page.url
   time_picker();page.locator('#searchfrom').fill('2026-11-14T09:00');page.locator('#searchto').fill('2026-11-15T09:00');page.locator('#applysearchfilters').click();expect(page.locator('.session')).to_have_count(4)
   initial_output=os.getenv('AGENTLOG_SCREENSHOT_DIR')
   if initial_output:pathlib.Path(initial_output).mkdir(parents=True,exist_ok=True);page.screenshot(path=initial_output+'/ui-search-overview.png',full_page=True)
   saved=page.url;page.reload();expect(page.locator('.session')).to_have_count(4);time_picker();expect(page.locator('#searchtimezone')).to_have_text('Asia/Tokyo');assert page.url==saved;page.keyboard.press('Escape')
   page.locator('[data-project-check="name:A"]').check();expect(page.locator('.session')).to_have_count(1)
   page.locator('[data-project-check="name:B"]').check();expect(page.locator('.session')).to_have_count(3);expect(page.locator('#listtitle')).to_have_text('2 projects');assert page.url.count('project=')==2
   facets();page.locator('[name=searchsource][value=codex]').check();expect(page.locator('.session')).to_have_count(2)
   page.locator('[name=searchsource][value=claude]').check();expect(page.locator('.session')).to_have_count(3)
   page.locator('[name=searchorigin][value=import]').check();expect(page.locator('.session')).to_have_count(1)
   page.locator('[name=searchorigin][value=otel]').check();expect(page.locator('.session')).to_have_count(3)
   page.locator('[name=searchkind][value=error]').check();expect(page.locator('.session')).to_have_count(1)
   page.locator('[name=searchkind][value=conversation]').check();expect(page.locator('.session')).to_have_count(3)
   page.locator('[name=searchkind][value=error]').uncheck();page.locator('[name=searchkind][value=conversation]').uncheck();expect(page.locator('.session')).to_have_count(3)
   page.locator('#search').fill('alpha beta');expect(page.locator('.session')).to_have_count(1)
   text_options();page.locator('#textmatch').select_option('any');page.locator('#searchoptionsform button[type=submit]').click();expect(page.locator('.session')).to_have_count(2)
   text_options();page.locator('#textmatch').select_option('all');page.locator('#searchoptionsform button[type=submit]').click();expect(page.locator('.session')).to_have_count(1)
   text_options();page.locator('#searchexclude').fill('failure')
   if initial_output:page.screenshot(path=initial_output+'/ui-search-text-options.png',full_page=True)
   page.locator('#searchoptionsform button[type=submit]').click();expect(page.locator('.session')).to_have_count(0)
   text_options();page.locator('#searchexclude').fill('');page.locator('#searchoptionsform button[type=submit]').click();expect(page.locator('.session')).to_have_count(1)
   page.locator('#searchanalysis summary').click();expect(page.locator('#analysisrows tr')).to_have_count(1);expect(page.locator('#analysisrows tr td').nth(0)).to_have_text('1');expect(page.locator('#analysisrows tr td').nth(2)).to_have_text('1')
   page.locator('#analysisgroup').select_option('project');expect(page.locator('#analysisrows th')).to_have_text('A');expect(page.locator('#analysisstatus')).to_have_text('')
   requests=len(analysis);page.wait_for_timeout(350);assert len(analysis)==requests,'Unexpected auto refresh'
   assert '/export' not in page.url;expect(page.locator('.session')).to_have_count(1)
   with page.expect_download() as download:page.locator('#downloadexport').click()
   with zipfile.ZipFile(download.value.path()) as archive:
    manifest=json.loads(archive.read('manifest.json'));assert manifest['sessions']==1 and manifest['records']==3,manifest;assert manifest['filters']['project']==['name:A','name:B'],manifest;assert manifest['filters']['source']==['codex','claude'],manifest
   # Skill navigation retains the range and project/source selections, but not
   # the session body query or event-type facet.
   page.locator('#skillsnav').click();expect(page.locator('.skill-row')).to_have_count(1);expect(page.locator('#skillcount')).to_have_text('1 skill · 1 reported · 0 reads');expect(page.locator('#skilltitle')).to_have_text('2 projects · Skills');assert 'from=' in page.url
   page.reload();expect(page.locator('#skillcount')).to_have_text('1 skill · 1 reported · 0 reads');facets();page.locator('[name=searchorigin][value=otel]').uncheck();expect(page.locator('#skillcount')).to_have_text('1 skill · 0 reported · 0 reads')
   page.locator('.skill-row').click();expect(page.locator('.session')).to_have_count(1);expect(page.locator('.session')).to_have_attribute('data-session','codex:d');assert 'from=' in page.url and page.url.count('project=')==2
   page.locator('.session').click();expect(page.locator('#detailpage')).to_be_visible();page.locator('#detailpage>.back').click();expect(page.locator('.session')).to_have_count(1)
   # Invalid local input keeps the last valid results and URL.
   saved=page.url;time_picker();page.locator('#searchto').fill('2026-11-13T09:00');page.locator('#applysearchfilters').click();expect(page.locator('#searchfiltererror')).to_have_text('From must be earlier than To.');assert page.url==saved
   page.keyboard.press('Escape');page.locator('#resetsearchfilters').click();expect(page.locator('.session')).to_have_count(5);assert 'from=' not in page.url and 'project=' not in page.url
   time_picker();page.locator('#searchrange').select_option('86400');expect(page.locator('.session')).to_have_count(0);assert 'from=' in page.url;assert '.000' not in page.url;page.locator('#resetsearchfilters').click();expect(page.locator('.session')).to_have_count(5)
   time_picker();page.locator('#searchrange').select_option('custom');page.locator('#searchfrom').fill('2026-11-14T09:00');page.locator('#searchto').fill('2026-11-15T09:00');page.locator('#applysearchfilters').click();expect(page.locator('.session')).to_have_count(4);facets();page.locator('#searchanalysis summary').click();expect(page.locator('#analysisrows tr')).to_have_count(1)
   page.locator('[data-project-check="name:A"]').check();page.locator('[data-project-check="name:B"]').check();page.locator('[name=searchsource][value=codex]').check();expect(page.locator('.session')).to_have_count(2);expect(page.locator('#analysisrows tr')).to_have_count(1)
   output=os.getenv('AGENTLOG_SCREENSHOT_DIR')
   if output:pathlib.Path(output).mkdir(parents=True,exist_ok=True);page.evaluate('document.activeElement?.blur();window.scrollTo(0,0)');page.screenshot(path=output+'/ui-search-facets.png',full_page=True)
   time_picker();expect(page.locator('#searchfrom')).to_have_attribute('step','1')
   if output:page.screenshot(path=output+'/ui-search-time.png',full_page=True)
   page.keyboard.press('Escape')
   page.set_viewport_size({'width':1024,'height':768});assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   if output:page.evaluate('document.activeElement?.blur();window.scrollTo(0,0)');page.screenshot(path=output+'/ui-search-laptop.png',full_page=True)
   page.set_viewport_size({'width':390,'height':844});expect(page.locator('#facetoptions')).not_to_have_attribute('open','');assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   if output:page.evaluate('document.activeElement?.blur();window.scrollTo(0,0)');page.screenshot(path=output+'/ui-search-facets-mobile.png',full_page=True)
   time_picker()
   assert page.locator('.time-picker').bounding_box()['x']>=0
   assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   if output:page.screenshot(path=output+'/ui-search-time-mobile.png',full_page=True)
   page.keyboard.press('Escape')
   page.set_viewport_size({'width':1440,'height':1000});page.locator('#skillsnav').click();expect(page.locator('#skillcount')).to_have_text('1 skill · 1 reported · 0 reads')
   if output:page.screenshot(path=output+'/ui-search-skills.png',full_page=True)
   assert not errors,errors;browser.close()
 finally:proc.terminate();proc.wait(timeout=20)
print('PASS: native time/project/agent/origin/type controls, text AND/OR/exclusion, URL reload, analysis without polling, ZIP scope, Skills counts and mobile')
