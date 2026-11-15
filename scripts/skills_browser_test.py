#!/usr/bin/env python3
"""Reported Codex skill use versus historical references, search and session navigation."""
from test_support import settled_urlopen
import json,os,pathlib,socket,subprocess,tempfile,time,urllib.request
from playwright.sync_api import sync_playwright,expect
binary=os.getenv('AGENTLOG_BIN','./agentlog');token='synthetic-skills-browser-token'
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}';proc=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:settled_urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  def record(session,i,name,attrs):return {'eventName':name,'timeUnixNano':str(1780000000000000000+i*1000000000),'attributes':[{'key':k,'value':{'stringValue':str(v)}} for k,v in {'session.id':session,**attrs}.items()]}
  rows=[record('main',0,'gen_ai.user.message',{'cwd':'/work/shop','prompt':'Fix pagination'})]
  for i,turn in enumerate(['turn-1','turn-1','turn-2']):rows.append(record('main',i+1,'codex.skill_invocation',{'skill.name':'review-core','turn.id':turn,'skill.invocation_type':'explicit' if i<2 else 'implicit','skill.scope':'repo'}))
  rows += [record('legacy',5,'gen_ai.tool.start',{'cwd':'/work/legacy','tool_name':'exec_command','arguments':"{\"cmd\":\"cat /work/.codex/skills/review-core/SKILL.md\"}",'agentlog.origin':'import'}),record('legacy',6,'gen_ai.tool.start',{'cwd':'/work/legacy','tool_name':'Read','arguments':'{"file_path":"/work/skills/legacy-only/SKILL.md"}','agentlog.origin':'import'}),record('legacy',7,'gen_ai.tool.start',{'cwd':'/work/legacy','tool_name':'exec_command','arguments':'{"cmd":"cat /home/demo/.codex/skills/.system/skill-creator/SKILL.md"}','agentlog.origin':'import'}),record('mentioned',8,'gen_ai.user.message',{'cwd':'/work/mentions','prompt':'Available: review-core. Also /work/skills/not-used/SKILL.md'}),record('main',9,'gen_ai.tool.start',{'tool_name':'apply_patch','arguments':'{"input":"*** Add File: /work/skills/written-only/SKILL.md"}'}),record('jp',10,'codex.skill_invocation',{'cwd':'/work/docs','skill.name':'日本語ドキュメント','skill.plugin_id':'writing-tools','skill.scope':'user','turn.id':'jp-turn','skill.invocation_type':'implicit'})]
  req=urllib.request.Request(base+'/v1/logs',json.dumps({'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeLogs':[{'logRecords':rows}]}]}).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
  with settled_urlopen(req) as response:assert response.status==200
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox']);page=browser.new_page(viewport={'width':1440,'height':1000});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   page.goto(base+'/skills');page.locator('#token').fill(token);page.locator('#loginform button').click();expect(page.locator('.skill-row')).to_have_count(4);expect(page.locator('#skillcount')).to_have_text('4 skills · ≈6 uses');expect(page.locator('.skill-row h2').first).to_have_text('review-core');expect(page.locator('#projectsidebar')).to_be_visible()
   row=page.locator('.skill-row').filter(has=page.get_by_role('heading',name='review-core',exact=True));expect(row.locator('span').nth(0)).to_have_text('≈3');expect(row.locator('span').nth(1)).to_have_text('2');expect(row.locator('span').nth(2)).to_have_text('1')
   legacy=page.locator('.skill-row').filter(has=page.get_by_role('heading',name='legacy-only',exact=True));expect(legacy.locator('span').nth(0)).to_have_text('≈1');expect(legacy.locator('.skill-uses')).to_have_attribute('title','0 reported; 1 inferred from sessions with skill reads or injections. Repeat reads count once per session.')
   if os.getenv('AGENTLOG_SCREENSHOT_DIR'):
    output=pathlib.Path(os.environ['AGENTLOG_SCREENSHOT_DIR']);output.mkdir(parents=True,exist_ok=True);page.screenshot(path=str(output/'ui-skills.png'))
   page.locator('[data-project="name:shop"]').click();expect(page.locator('.skill-row')).to_have_count(1);expect(page.locator('#skillcount')).to_have_text('1 skill · 2 uses');assert 'project=' in page.url;page.locator('.skill-row').click();expect(page.locator('.session')).to_have_count(1);assert 'project=' in page.url;page.locator('#skillsnav').click();expect(page.locator('#skillcount')).to_have_text('1 skill · 2 uses');page.reload();expect(page.locator('#skillcount')).to_have_text('1 skill · 2 uses')
   page.locator('#allprojects').click();expect(page.locator('.skill-row')).to_have_count(4);expect(page.locator('#skillcount')).to_have_text('4 skills · ≈6 uses')
   page.locator('#skillsort').select_option('recent');expect(page.locator('.skill-row h2').first).to_have_text('日本語ドキュメント');assert 'sort=recent' in page.url;page.reload();expect(page.locator('#skillsort')).to_have_value('recent')
   page.locator('#skillsort').select_option('uses');expect(page.locator('.skill-row h2').first).to_have_text('review-core')
   page.locator('#skillsearch').fill('ﾄﾞｷｭﾒﾝﾄ');expect(page.locator('.skill-row')).to_have_count(1);expect(page.locator('.skill-row h2')).to_have_text('日本語ドキュメント');page.reload();expect(page.locator('#skillsearch')).to_have_value('ﾄﾞｷｭﾒﾝﾄ');expect(page.locator('.skill-row')).to_have_count(1)
   page.locator('#skillsearch').fill('writing-tools');expect(page.locator('.skill-row')).to_have_count(1);page.locator('#skillsearch').fill('');expect(page.locator('.skill-row')).to_have_count(4)
   page.locator('.skill-row').filter(has=page.get_by_role('heading',name='review-core',exact=True)).click();expect(page.locator('.session')).to_have_count(2);expect(page.locator('#skillfilterlabel')).to_have_text('Skill: review-core');assert '/work/mentions' not in page.locator('#sessions').inner_text()
   page.locator('[data-project="name:shop"]').click();expect(page.locator('.session')).to_have_count(1);page.locator('.session').click();expect(page.locator('#sessionskills')).to_contain_text('review-core');expect(page.locator('#sessionskills')).to_contain_text('×2');page.locator('#sessionskills .session-skill').click();expect(page.locator('#inspectstatus')).to_have_text('');expect(page.locator('#inspectortitle')).to_have_text('review-core');expect(page.locator('#inspectdetails')).to_contain_text('implicit');page.locator('#jsontab').click();expect(page.locator('#eventjson')).to_contain_text('codex.skill_invocation');page.keyboard.press('Escape');page.locator('button[data-kind="skill"]').click();expect(page.locator('#timeline .event')).to_have_count(3)
   page.locator('#origin').select_option('import');expect(page.locator('#sessionskills')).to_be_empty();page.locator('#origin').select_option('otel');expect(page.locator('#sessionskills')).to_contain_text('×2')
   page.locator('#detailpage>.back').click();expect(page.locator('#skillfilterlabel')).to_have_text('Skill: review-core');page.locator('#clearskillfilter').click();expect(page.locator('#skillfilter')).to_be_hidden();assert 'skill=' not in page.url
   page.goto(base+'/sessions/codex%3Alegacy');expect(page.locator('#sessionskills')).to_contain_text('legacy-only ≈1');expect(page.locator('#sessionskills .session-skill').filter(has_text='legacy-only')).to_have_attribute('title','0 reported; 1 inferred from sessions with skill reads or injections. Repeat reads count once per session. · View latest skill event')
   page.goto(base+'/skills?project=path%3A%2Fwork%2Fshop');expect(page.locator('.skill-row')).to_have_count(1);saved_url=page.url
   page.route('**/api/skills?*sort=recent*',lambda route:route.fulfill(status=500,body='synthetic failure'));page.locator('#skillsort').select_option('recent');expect(page.locator('#error')).to_contain_text('HTTP 500');expect(page.locator('#skillsort')).to_have_value('uses');expect(page.locator('[data-project="name:shop"]')).to_have_attribute('aria-current','true');assert page.url==saved_url;page.unroute('**/api/skills?*sort=recent*')
   page.set_viewport_size({'width':390,'height':844});page.goto(base+'/skills');expect(page.locator('.skill-row')).to_have_count(4);assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   if os.getenv('AGENTLOG_SCREENSHOT_DIR'):page.screenshot(path=str(output/'ui-skills-mobile.png'))
   assert not errors,errors;browser.close()
 finally:proc.terminate();proc.wait(timeout=20)
print('PASS: skill counts/turn dedup, reported plus inferred use, Japanese/NFKC/plugin search, exact session/folder filtering, latest event/JSON, timeline skill filter, origins, clear/back and mobile')
