#!/usr/bin/env python3
"""End-to-end CLI import + live OTEL project labels + inspector/source filtering."""
import json,os,pathlib,socket,subprocess,tempfile,time,urllib.request
from playwright.sync_api import sync_playwright, expect
binary=os.getenv('AGENTLOG_BIN','./agentlog');token='browser-import-token-local'
with tempfile.TemporaryDirectory() as tmp:
 sock=socket.socket();sock.bind(('127.0.0.1',0));port=sock.getsockname()[1];sock.close();base=f'http://127.0.0.1:{port}'
 env={**os.environ,'AGENTLOG_TOKEN':token}
 proc=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',tmp+'/events.db'],env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:urllib.request.urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  folder=pathlib.Path(tmp)/'transcripts';folder.mkdir()
  rows=[{'type':'user','sessionId':'browser-import','uuid':'u','cwd':'/work/shop','timestamp':'2026-11-01T00:00:00Z','message':{'content':'Run the tests.'}}, {'type':'assistant','sessionId':'browser-import','uuid':'a','timestamp':'2026-11-01T00:00:01Z','message':{'model':'claude-model','content':[{'type':'tool_use','id':'call','name':'Bash','input':{'command':'go test ./...'}}]}}, {'type':'user','sessionId':'browser-import','uuid':'r','timestamp':'2026-11-01T00:00:02Z','message':{'content':[{'type':'tool_result','tool_use_id':'call','content':'PASS','is_error':False}]}}, {'type':'assistant','sessionId':'browser-import','uuid':'done','timestamp':'2026-11-01T00:00:03Z','message':{'content':[{'type':'text','text':'All tests passed.'}],'usage':{'input_tokens':2300,'output_tokens':89}}}]
  (folder/'session.jsonl').write_text(''.join(json.dumps(row)+'\n' for row in rows))
  for _ in range(2):subprocess.run([binary,'import','--dir',str(folder),'--endpoint',base,'--project','shop','--tag','backend','--tag','go'],env=env,check=True,capture_output=True)
  def fetch(path,body=None):
   req=urllib.request.Request(base+path,None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
   with urllib.request.urlopen(req) as r:return json.load(r)
  assert fetch('/api/stats')['events']==5
  native={'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'claude'}}]},'scopeLogs':[{'logRecords':[{'timeUnixNano':'1793577610000000000','attributes':[{'key':'session.id','value':{'stringValue':'browser-import'}},{'key':'event.name','value':{'stringValue':'claude_code.user_prompt'}},{'key':'prompt','value':{'stringValue':'Live OTEL message'}}]}]}]}]}
  fetch('/v1/logs?project=shop&tag=backend',native)
  assert fetch('/api/stats')['events']==6
  duplicates={'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'claude'}}]},'scopeLogs':[{'logRecords':[
   {'timeUnixNano':'1793577600000000000','eventName':'claude_code.user_prompt','attributes':[{'key':'session.id','value':{'stringValue':'browser-import'}},{'key':'event_id','value':{'stringValue':'u'}},{'key':'prompt','value':{'stringValue':'Run the tests.'}}]},
   {'timeUnixNano':'1793577602000000000','eventName':'claude_code.tool_result','attributes':[{'key':'session.id','value':{'stringValue':'browser-import'}},{'key':'tool_use_id','value':{'stringValue':'call'}},{'key':'tool_name','value':{'stringValue':'Bash'}},{'key':'duration_ms','value':{'intValue':'1200'}},{'key':'success','value':{'boolValue':False}},{'key':'error','value':{'stringValue':'OTEL failure'}}]}
  ]}]}]}
  fetch('/v1/logs?project=shop&tag=backend',duplicates)
  assert fetch('/api/stats')['events']==8
  assert fetch('/api/session?id=claude:browser-import')['count']==6
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox'])
   page=browser.new_page(viewport={'width':1440,'height':1060});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   page.goto(base);page.locator('#token').fill(token);page.locator('#loginform button').click();page.locator('.session').wait_for()
   assert 'shop' in page.locator('.session small').inner_text()
   page.locator('#search').fill('backend');page.locator('.session').click();page.locator('.event').first.wait_for()
   expect(page.locator('.event')).to_have_count(6)
   expect(page.get_by_text('Imported + OTEL',exact=True)).to_have_count(2)
   page.locator('.event.error').click()
   expect(page.locator('#inspectdetails')).to_contain_text('1.20 s')
   expect(page.locator('#inspectdetails')).to_contain_text('PASS')
   expect(page.locator('#inspectdetails')).to_contain_text('Source differences')
   page.locator('#jsontab').click();page.wait_for_function("() => document.querySelector('#inspectstatus').textContent===''")
   data=json.loads(page.locator('#eventjson').inner_text())
   assert len(data['records'])==2
   page.keyboard.press('Escape')
   assert 'Project: shop' in page.locator('#sessionmeta').inner_text()
   page.locator('#origin').select_option('import');expect(page.locator('.event')).to_have_count(5)
   expect(page.locator('.event.error')).to_have_count(0)
   assert page.locator('.event.assistant .prose').inner_text()=='All tests passed.'
   page.locator('.event.model').click();page.locator('#jsontab').click();page.wait_for_function("() => document.querySelector('#inspectstatus').textContent===''")
   assert '"intValue": "2300"' in page.locator('#eventjson').inner_text()
   assert 'project.tags' in page.locator('#eventjson').inner_text()
   page.locator('#detailtab').click()
   if os.getenv('AGENTLOG_SCREENSHOT'):page.screenshot(path=os.environ['AGENTLOG_SCREENSHOT'],full_page=True)
   page.keyboard.press('Escape');page.locator('#origin').select_option('otel');expect(page.locator('.event')).to_have_count(3)
   expect(page.locator('.event.user').filter(has_text='Live OTEL message')).to_have_count(1)
   assert 'PASS' not in page.locator('#timeline').inner_text()
   page.set_viewport_size({'width':390,'height':844});assert page.evaluate('() => document.documentElement.scrollWidth<=innerWidth')
   page.locator('.event.user').filter(has_text='Live OTEL message').click();assert page.locator('#inspector').get_attribute('aria-modal')=='true';page.keyboard.press('Escape')
   # New ingestion stays invisible until the user explicitly refreshes.
   late={'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'claude'}}]},'scopeLogs':[{'logRecords':[{'eventName':'claude_code.tool_result','attributes':[{'key':'session.id','value':{'stringValue':'late-import'}},{'key':'tool_use_id','value':{'stringValue':'late'}},{'key':'tool_name','value':{'stringValue':'Bash'}},{'key':'duration_ms','value':{'intValue':'300'}}]}]}]}]}
   fetch('/v1/logs',late)
   page.goto(base+'/sessions/claude%3Alate-import')
   page.locator('.event').wait_for()
   row={'type':'user','uuid':'late-result','sessionId':'late-import','message':{'content':[{'type':'tool_result','tool_use_id':'late','content':'Late output','is_error':False}]}}
   (folder/'late.jsonl').write_text(json.dumps(row)+'\n')
   subprocess.run([binary,'import','--dir',str(folder),'--endpoint',base],env=env,check=True,capture_output=True)
   assert page.locator('.event .kind').filter(has_text='Imported + OTEL').count()==0
   page.locator('.event').click();page.locator('#jsontab').click();expect(page.locator('#eventjson')).to_contain_text('Late output')
   page.keyboard.press('Escape');page.locator('#refresh').click()
   expect(page.locator('.event .kind').filter(has_text='Imported + OTEL')).to_have_count(1)
   page.locator('.event').click()
   assert page.locator('#jsontab').get_attribute('aria-selected')=='true'
   assert page.locator('.event.selected').count()==1
   expect(page.locator('#eventjson')).to_contain_text('Late output')
   assert page.locator('.event h2').inner_text()=='Bash'
   assert not errors,errors
   browser.close()
  print('PASS: real import/OTEL grouping, original JSON, complement/conflicts, origin isolation, native-first merge after manual refresh, labels, usage, search and mobile.')
 finally:proc.terminate();proc.wait(timeout=15)
