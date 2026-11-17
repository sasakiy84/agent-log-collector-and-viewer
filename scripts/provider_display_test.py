#!/usr/bin/env python3
"""Provider display, real Copilot trace fixture, restart preservation and browser flows."""
import hashlib,json,os,pathlib,socket,sqlite3,subprocess,tempfile,time,urllib.request
from playwright.sync_api import sync_playwright,expect
from test_support import wait_for_ingestion
binary=os.environ.get('AGENTLOG_BIN','./agentlog');token='synthetic-provider-display-token'
fixture=json.loads((pathlib.Path(__file__).resolve().parents[1]/'internal/app/testdata/copilot-1.0.112-traces.json').read_text())
with tempfile.TemporaryDirectory() as folder:
 db=folder+'/test.db';proc=None
 def start(exe):
  global proc,base
  with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
  base=f'http://127.0.0.1:{port}';proc=subprocess.Popen([exe,'--listen',f'127.0.0.1:{port}','--db',db],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  for _ in range(200):
   try:urllib.request.urlopen(base,timeout=1);break
   except OSError:time.sleep(.05)
  wait_for_ingestion(base,token)
 def stop():
  global proc
  if proc is not None:proc.terminate();proc.wait(timeout=20);proc=None
 def api(path,data=None):
  req=urllib.request.Request(base+path,None if data is None else json.dumps(data).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
  with urllib.request.urlopen(req,timeout=10) as r:out=json.load(r)
  if data is not None and path.startswith('/v1/'):wait_for_ingestion(base,token)
  return out
 def fingerprint():
  digest=hashlib.sha256()
  with sqlite3.connect('file:'+db+'?mode=ro',uri=True) as c:
   for id,raw in c.execute('SELECT id,raw FROM records ORDER BY id'):digest.update(id.encode());digest.update(raw)
  return digest.hexdigest()
 try:
  start(binary);api('/v1/traces',fixture);before=fingerprint()
  assert fingerprint()==before,'original records changed'
  current=api('/api/session?id=copilot:fixture-session&view=activity');assert current['count']==3 and current['usage']['values']['input_tokens']=='300',current
  assert api('/api/skills')['attempts']==1
  def attrs(values):
   return [{'key':k,'value':{'boolValue':v} if isinstance(v,bool) else {'intValue':str(v)} if isinstance(v,int) else {'stringValue':v}} for k,v in values.items()]
  def log(i,name,values):return {'eventName':'claude_code.'+name,'timeUnixNano':str(1794764000000000000+i*1000000000),'attributes':attrs({'session.id':'claude-session','event.name':name,**values})}
  logs=[log(0,'user_prompt',{'prompt_text':'Inspect the project tests','message.uuid':'claude-message'}),log(1,'skill_activated',{'skill.name':'test-guide','skill.source':'projectSettings','invocation_trigger':'user-slash'}),log(3,'assistant_response',{'response':'The fixture tests passed.'}),log(4,'api_request',{'input_tokens':100,'output_tokens':20,'cache_read_tokens':40,'cache_creation_tokens':10,'cost_usd':'0.02','request_id':'request-one'}),log(5,'subagent_completed',{'agent_id':'reviewer','agent_type':'Explore','total_tokens':99,'total_tool_uses':4,'duration_ms':1250}),log(6,'api_error',{'error':'Synthetic timeout','error_class':'api_timeout','attempt':2})]
  api('/v1/logs',{'resourceLogs':[{'resource':{'attributes':attrs({'service.name':'claude-code','service.namespace':'claude-fixture'})},'scopeLogs':[{'logRecords':logs}]}]})
  span={'traceId':'2'*32,'spanId':'2'*16,'name':'claude_code.tool','startTimeUnixNano':'1794764002000000000','endTimeUnixNano':'1794764002100000000','attributes':attrs({'session.id':'claude-session','tool_name':'Bash','tool_use_id':'claude-call','full_command':'printf "fixture passed"','success':True}),'events':[{'name':'tool.output','attributes':attrs({'output':'fixture passed'})}]}
  api('/v1/traces',{'resourceSpans':[{'resource':{'attributes':attrs({'service.name':'claude-code','service.namespace':'claude-fixture'})},'scopeSpans':[{'spans':[span]}]}]})
  # A documented nested invocation has a definition ID. Its trace/span identifies
  # the instance; the definition must never become a fabricated runtime agent ID.
  parent=fixture['resourceSpans'][0]['scopeSpans'][0]['spans'][-1]
  child={'traceId':parent['traceId'],'spanId':'f'*16,'parentSpanId':parent['spanId'],'name':'invoke_agent Explore','startTimeUnixNano':parent['startTimeUnixNano'],'endTimeUnixNano':parent['endTimeUnixNano'],'attributes':attrs({'gen_ai.operation.name':'invoke_agent','gen_ai.conversation.id':'fixture-session','gen_ai.agent.id':'explore-definition','gen_ai.agent.name':'Explore','gen_ai.input.messages':'[{"role":"user","content":"Check fixture"}]','gen_ai.output.messages':'[{"role":"assistant","content":"Only this subagent work"}]'})}
  api('/v1/traces',{'resourceSpans':[{'resource':fixture['resourceSpans'][0]['resource'],'scopeSpans':[{'spans':[child]}]}]})
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path=os.getenv('AGENTLOG_CHROMIUM', '/usr/bin/chromium'),headless=True,args=['--no-sandbox']);page=browser.new_page(viewport={'width':1440,'height':1000});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   page.goto(base);page.locator('#token').fill(token);page.locator('#loginform button').click();expect(page.locator('.session')).to_have_count(2);assert '<current_datetime>' not in page.locator('#sessions').inner_text()
   page.locator('[data-session="copilot:fixture-session"]').click();expect(page.locator('#timeline > .event')).to_have_count(1);page.locator('[data-view=activity]').click();expect(page.locator('#sessionskills')).to_contain_text('review 1 attempt');expect(page.locator('#sessionusage')).to_contain_text('300');expect(page.locator('#timeline .event')).to_have_count(4)
   root=page.locator('#timeline .event').filter(has=page.get_by_role('heading',name='Agent turn',exact=True));expect(root.locator('[data-model-call]')).to_have_count(3)
   tool=page.locator('#timeline .event').filter(has=page.get_by_role('heading',name='view',exact=True));tool.click();expect(page.locator('#inspectdetails')).to_contain_text('Synthetic file contents for Agentlog display test.');expect(page.locator('#inspectdetails > .block').filter(has=page.get_by_text('Output',exact=True)).locator('pre')).to_have_text('Synthetic file contents for Agentlog display test.');page.locator('#jsontab').click();expect(page.locator('#eventjson')).to_contain_text('gen_ai.tool.call.result');page.keyboard.press('Escape')
   page.locator('.subagent-link').filter(has_text='Explore').click();expect(page.locator('#inspectagent')).to_contain_text('Only this subagent work');expect(page.locator('#inspectagent .subagent-link')).to_have_count(0);page.keyboard.press('Escape')
   output=os.getenv('AGENTLOG_SCREENSHOT_DIR')
   if output:pathlib.Path(output).mkdir(parents=True,exist_ok=True);page.evaluate('document.activeElement?.blur();window.scrollTo(0,0)');page.screenshot(path=output+'/ui-copilot-display.png',full_page=True)
   page.locator('[data-view=diagnostics]').click();expect(page.locator('#timeline .event')).to_have_count(7)
   page.locator('#detailpage>.back').click();page.locator('[data-session="claude:claude-session"]').click();expect(page.locator('#timeline > .event')).to_have_count(2);page.locator('[data-view=activity]').click();expect(page.locator('#sessiontitle')).to_have_text('Inspect the project tests');expect(page.locator('#sessionusage')).to_contain_text('150');expect(page.locator('#timeline')).to_contain_text('Model call failed')
   tool=page.locator('#timeline .event').filter(has=page.get_by_role('heading',name='Bash',exact=True));tool.click();expect(page.locator('#inspectdetails')).to_contain_text('fixture passed');page.keyboard.press('Escape');page.locator('#relations .subagent-link').click();expect(page.locator('#inspectagent')).to_contain_text('Explore completed');expect(page.locator('#inspectagent')).to_contain_text('Final request tokens');page.keyboard.press('Escape')
   if output:page.evaluate('document.activeElement?.blur();window.scrollTo(0,0)');page.screenshot(path=output+'/ui-claude-display.png',full_page=True)
   page.locator('#skillsnav').click();expect(page.locator('.skill-row')).to_have_count(2);assert 'test-guide' in page.locator('#skillrows').inner_text();assert 'review' in page.locator('#skillrows').inner_text()
   page.set_viewport_size({'width':390,'height':844});assert page.evaluate('document.documentElement.scrollWidth<=innerWidth');assert not errors,errors;browser.close()
  with sqlite3.connect('file:'+db+'?mode=ro',uri=True) as c:checkpoint=c.execute("SELECT id,detail FROM record_facts ORDER BY id").fetchall()
  stop();start(binary)
  with sqlite3.connect('file:'+db+'?mode=ro',uri=True) as c:assert checkpoint==c.execute("SELECT id,detail FROM record_facts ORDER BY id").fetchall(),'repair repeated on restart'
 finally:stop()
print('PASS: real Copilot fixture 600→300 tokens, skill evidence, readable tool content/JSON, invocation statistics, trace-scoped subagent preview, Claude documented formats, original preservation and restart without historical reprocessing')
