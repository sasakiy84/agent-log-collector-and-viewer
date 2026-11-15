#!/usr/bin/env python3
"""Isolated subagent launch links and bounded sidebar logs across all three agents."""
import json,os,pathlib,socket,subprocess,tempfile,time,urllib.parse,urllib.request
from playwright.sync_api import sync_playwright,expect
binary=os.getenv('AGENTLOG_BIN','./agentlog');token='synthetic-subagent-browser-token'
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}';proc=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:urllib.request.urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  def attrs(values):return [{'key':k,'value':{'stringValue':json.dumps(v) if isinstance(v,(dict,list)) else str(v)}} for k,v in values.items()]
  def record(sid,i,name,values):return {'eventName':name,'timeUnixNano':str(1780000000000000000+i*1000000000),'attributes':attrs({'session.id':sid,**values})}
  def send(source,records):
   req=urllib.request.Request(base+'/v1/logs',json.dumps({'resourceLogs':[{'resource':{'attributes':attrs({'service.name':source,'cwd':'/work/subagent-demo'})},'scopeLogs':[{'logRecords':records}]}]}).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
   with urllib.request.urlopen(req) as response:assert response.status==200
  parent=[record('parent',0,'gen_ai.tool.start',{'gen_ai.tool.name':'spawn_agent','gen_ai.tool.call.id':'launch-reviewer','gen_ai.tool.call.arguments':{'message':'Review session ordering and pagination.'}}),record('parent',1,'gen_ai.agent.started',{'gen_ai.operation.name':'invoke_agent','gen_ai.agent.id':'missing','gen_ai.agent.name':'Pending agent'})]
  parent += [record('parent',i+2,'gen_ai.assistant.message',{'response':f'Parent line {i}'}) for i in range(205)]
  parent += [record('parent',210,'gen_ai.tool.result',{'gen_ai.tool.call.id':'launch-reviewer','gen_ai.tool.call.result':{'agent_id':'child','nickname':'Reviewer'}})]
  send('codex',parent)
  child=[record('child',i,'gen_ai.assistant.message',{'parent.session.id':'parent','response':f'Child line {i}'}) for i in range(45)]
  child[0]['attributes']+=attrs({'response':'Child line 0 <img src=x onerror="window.xss=true">'})
  child[41]=record('child',41,'gen_ai.agent.started',{'parent.session.id':'parent','gen_ai.operation.name':'invoke_agent','gen_ai.agent.id':'grandchild','gen_ai.agent.name':'Second reviewer'})
  send('codex',child+[record('grandchild',0,'gen_ai.assistant.message',{'parent.session.id':'child','response':'Nested review\n'+'順序とページングを確認しました。\n'*60})])
  send('copilot',[record('inline',0,'gen_ai.agent.started',{'gen_ai.operation.name':'invoke_agent','gen_ai.agent.id':'inline-reviewer','gen_ai.agent.name':'Inline reviewer'})]+[record('inline',i+1,'gen_ai.assistant.message',{'agent_id':'inline-reviewer','response':f'Inline line {i}'}) for i in range(45)]+[record('inline',100,'gen_ai.assistant.message',{'agent_id':'other','response':'Other runtime agent'})])
  root=pathlib.Path(folder)/'claude';(root/'subagents').mkdir(parents=True)
  (root/'parent.jsonl').write_text(json.dumps({'type':'assistant','sessionId':'cl-root','uuid':'launch','timestamp':'2026-05-28T10:00:00Z','message':{'content':[{'type':'tool_use','id':'task-1','name':'Task','input':{'prompt':'Review the change'}}]}})+'\n'+json.dumps({'type':'user','sessionId':'cl-root','uuid':'result','timestamp':'2026-05-28T10:01:00Z','message':{'content':[{'type':'tool_result','tool_use_id':'task-1','content':'Done.\nagentId: review (for resuming this agent)'}]}})+'\n')
  (root/'subagents'/'agent-review.jsonl').write_text(json.dumps({'type':'assistant','sessionId':'cl-root','uuid':'reply','timestamp':'2026-05-28T10:00:30Z','message':{'content':[{'type':'text','text':'Claude child review complete.'}]}})+'\n')
  subprocess.run([binary,'import','--source','claude','--dir',str(root),'--endpoint',base],env={**os.environ,'AGENTLOG_TOKEN':token},check=True,capture_output=True)
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox']);page=browser.new_page(viewport={'width':1440,'height':1000});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   page.goto(base+'/sessions/codex%3Aparent?page=1');page.locator('#token').fill(token);page.locator('#loginform button').click();expect(page.locator('#timeline .event')).to_have_count(200)
   launch=page.locator('#timeline .subagent-link').filter(has_text='Reviewer');expect(launch).to_have_count(1);expect(launch).to_have_attribute('href','/sessions/codex%3Achild')
   launch.focus();launch.press('Enter');expect(page.locator('#subagentevents .preview-entry')).to_have_count(5);expect(page.locator('#subagentevents')).to_contain_text('Child line 44');assert '/codex%3Aparent' in page.url;assert page.locator('#inspectdetails').is_hidden()
   page.locator('#subagentpagination [aria-label="Previous page"]').click();expect(page.locator('#subagentevents .preview-entry')).to_have_count(40);expect(page.locator('#subagentevents')).to_contain_text('Child line 0');assert page.evaluate('!window.xss');assert page.locator('#subagentevents img').count()==0
   page.locator('#subagentpagination [data-page="latest"]').click();expect(page.locator('#subagentevents .preview-entry')).to_have_count(5)
   if os.getenv('AGENTLOG_SCREENSHOT_DIR'):
    output=pathlib.Path(os.environ['AGENTLOG_SCREENSHOT_DIR']);output.mkdir(parents=True,exist_ok=True);page.screenshot(path=str(output/'ui-subagent-preview.png'))
   page.locator('#subagentevents .subagent-link').click();expect(page.locator('#subagentevents')).to_contain_text('Nested review');expect(page.locator('#inspectortitle')).to_have_text('Subagent: Second reviewer')
   page.locator('.inspectorbody').evaluate('el=>el.scrollTop=el.scrollHeight');header=page.locator('.inspectorheader').bounding_box();assert header['y']>=0
   page.locator('#subagentback').click();expect(page.locator('#subagentevents')).to_contain_text('Child line 44');assert page.locator('#subagentrefresh').count()==0
   requests=[];page.on('request',lambda r:requests.append(r.url) if '/api/' in r.url else None)
   send('codex',[record('child',45,'gen_ai.assistant.message',{'parent.session.id':'parent','response':'Child line 45 newly ingested'})]);page.wait_for_timeout(5200);assert not requests,requests
   page.locator('#subagentpagination [data-page="latest"]').click();expect(page.locator('#subagentevents')).to_contain_text('Child line 45 newly ingested')
   page.route('**/api/session?id=codex%3Achild*',lambda r:r.fulfill(status=500,body='failure'));page.locator('#subagentpagination [data-page="latest"]').click();expect(page.locator('#inspectstatus')).to_contain_text('HTTP 500');expect(page.locator('#subagentevents')).to_contain_text('Child line 45');page.unroute('**/api/session?id=codex%3Achild*');page.locator('#subagentpagination [data-page="latest"]').click();expect(page.locator('#inspectstatus')).to_have_text('')
   page.locator('#subagentback').click();expect(page.locator('#inspectdetails')).to_be_visible();expect(page.locator('#inspectortitle')).to_have_text('spawn_agent');page.locator('#inspectdetails .subagent-link').click();expect(page.locator('#subagentevents')).to_contain_text('Child line 45');page.locator('#subagentback').click();expect(page.locator('#inspectdetails')).to_be_visible();page.locator('#jsontab').click();expect(page.locator('#eventjson')).to_contain_text('launch-reviewer');page.keyboard.press('Escape')
   missing=page.locator('#timeline .subagent-link').filter(has_text='Pending agent');missing.click();expect(page.locator('.preview-empty')).to_have_text('Logs have not been collected.');assert page.locator('#subagentfull').is_hidden();page.keyboard.press('Escape');expect(missing).to_be_focused()
   page.set_viewport_size({'width':390,'height':844});launch.click();expect(page.locator('#subagentevents')).to_contain_text('Child line 45');expect(page.locator('#inspector')).to_have_attribute('aria-modal','true');assert page.evaluate('document.querySelector("#eventlist").inert');assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   page.locator('#subagentevents .subagent-link').click();expect(page.locator('#subagentevents')).to_contain_text('Nested review');page.locator('.inspectorbody').evaluate('el=>el.scrollTop=el.scrollHeight');assert page.locator('.inspectorheader').bounding_box()['y']==0
   if os.getenv('AGENTLOG_SCREENSHOT_DIR'):page.screenshot(path=str(output/'ui-subagent-mobile.png'))
   page.keyboard.press('Escape');expect(launch).to_be_focused();assert page.evaluate('!document.querySelector("#eventlist").inert')
   page.goto(base+'/sessions/claude%3Acl-root');page.locator('#timeline .subagent-link').first.click();expect(page.locator('#subagentevents')).to_contain_text('Claude child review complete.');page.locator('#subagentfull').click();expect(page.locator('#timeline')).to_contain_text('Claude child review complete.');assert 'claude:cl-root/agent:review' in urllib.parse.unquote(page.url)
   page.goto(base+'/sessions/copilot%3Ainline');page.locator('#timeline .subagent-link').click();expect(page.locator('#subagentevents .preview-entry')).to_have_count(5);expect(page.locator('#subagentevents')).to_contain_text('Inline line 44');assert 'Other runtime agent' not in page.locator('#subagentevents').inner_text();assert '/copilot%3Ainline' in page.url
   assert not errors,errors;browser.close()
 finally:proc.terminate();proc.wait(timeout=20)
print('PASS: cross-page launch links, lazy bounded sidebar/latest/older pages, nested agents/back, explicit refresh/no polling, errors and missing logs, event JSON/full session, Claude imports, Copilot inline attribution, keyboard/focus/mobile and fixed header')
