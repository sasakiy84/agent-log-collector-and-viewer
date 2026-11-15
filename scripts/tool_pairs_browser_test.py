#!/usr/bin/env python3
"""Tool input/output navigation and opt-in Codex internal traces; synthetic data only."""
import json,os,pathlib,socket,subprocess,tempfile,time,urllib.request
from playwright.sync_api import sync_playwright,expect
binary=os.getenv('AGENTLOG_BIN','./agentlog');token='synthetic-tool-pair-browser-token'
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}';proc=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:urllib.request.urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  def record(i,name,values):return {'timeUnixNano':str(1780000000000000000+i*1000000000),'eventName':name,'attributes':[{'key':k,'value':{'stringValue':str(v)}} for k,v in values.items()]}
  rows=[record(0,'gen_ai.tool.start',{'session.id':'root','cwd':'/work/tool-pair','tool_name':'exec_command','call_id':'test-call','arguments':'{"cmd":"go test ./..."}'})]
  rows += [record(i+1,'gen_ai.assistant.message',{'session.id':'root','response':f'Working {i}'}) for i in range(205)]
  rows += [record(210,'gen_ai.tool.result',{'session.id':'root','tool_name':'exec_command','call_id':'test-call','output':'FAIL: expected stable ordering\n<img src=x onerror="window.xss=true">','success':'false','error':'Exit code 1','duration_ms':'1500'}),record(211,'startup',{})]
  spans=[{'name':'fs.read_file','traceId':f'{i+1:032x}','spanId':f'{i+1:016x}','startTimeUnixNano':'1780000000000000000','endTimeUnixNano':'1780000000000000100'} for i in range(3)]
  for signal,body in [('logs',{'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeLogs':[{'logRecords':rows}]}]}),('traces',{'resourceSpans':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeSpans':[{'spans':spans}]}]})]:
   req=urllib.request.Request(base+'/v1/'+signal,json.dumps(body).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
   with urllib.request.urlopen(req) as response:assert response.status==200
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox']);page=browser.new_page(viewport={'width':1440,'height':1000});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   page.goto(base);page.locator('#token').fill(token);page.locator('#loginform button').click();expect(page.locator('.session')).to_have_count(1);assert 'Folder unavailable' not in page.locator('#sessions').inner_text()
   page.locator('#showdiagnostics').check();expect(page.locator('.session')).to_have_count(5);assert 'diagnostics=1' in page.url;page.reload();expect(page.locator('#showdiagnostics')).to_be_checked();page.locator('[data-project=""]').click();expect(page.locator('.session')).to_have_count(4);page.locator('#allprojects').click();expect(page.locator('#showdiagnostics')).to_be_checked();page.locator('#showdiagnostics').uncheck();expect(page.locator('.session')).to_have_count(1)
   page.goto(base+'/sessions/codex%3Aroot?page=1');expect(page.locator('#timeline .event')).to_have_count(200);first=page.locator('#timeline .event').first;expect(first.locator('.kind')).to_have_text('Input');first.click();expect(page.locator('#inspectstatus')).to_have_text('');expect(page.locator('.toolpair')).to_contain_text('FAIL: expected stable ordering');expect(page.locator('.toolpair .failed')).to_have_text('Failed');assert page.locator('.toolpair img').count()==0
   if os.getenv('AGENTLOG_SCREENSHOT_DIR'):
    output=pathlib.Path(os.environ['AGENTLOG_SCREENSHOT_DIR']);output.mkdir(parents=True,exist_ok=True);page.screenshot(path=str(output/'ui-tool-pair.png'))
   url=page.url;page.get_by_role('button',name='Open output event',exact=True).click();expect(page.locator('#inspectstatus')).to_have_text('');expect(page.locator('.toolpair')).to_contain_text('go test ./...');assert page.url==url;expect(page.locator('#inspectdetails>.block .code').first).to_contain_text('FAIL: expected stable ordering')
   page.locator('#jsontab').click();expect(page.locator('#eventjson')).to_contain_text('FAIL: expected stable ordering');page.locator('#detailtab').click();page.get_by_role('button',name='Open input event',exact=True).click();expect(page.locator('#inspectstatus')).to_have_text('');expect(page.locator('.toolpair')).to_contain_text('FAIL: expected stable ordering');page.keyboard.press('Escape');expect(first).to_be_focused()
   page.set_viewport_size({'width':390,'height':844});first.click();expect(page.locator('.toolpair')).to_contain_text('FAIL: expected stable ordering');expect(page.locator('#inspector')).to_have_attribute('aria-modal','true');assert page.evaluate('document.documentElement.scrollWidth<=innerWidth');page.get_by_role('button',name='Open output event',exact=True).click();expect(page.locator('#inspectstatus')).to_have_text('');expect(page.locator('.toolpair')).to_contain_text('go test ./...');page.keyboard.press('Escape');expect(first).to_be_focused()
   page.goto(base+'/?diagnostics=1&folder=');expect(page.locator('.session')).to_have_count(4);page.locator('#showdiagnostics').uncheck();expect(page.get_by_text('No sessions',exact=True)).to_be_visible();page.reload();expect(page.get_by_text('No sessions',exact=True)).to_be_visible()
   assert page.evaluate('!window.xss');assert not errors,errors;browser.close()
 finally:proc.terminate();proc.wait(timeout=20)
print('PASS: cross-page input/output previews, reverse/full JSON navigation, failure/latency, focus/mobile/XSS, internal traces opt-in with folder/session counts and persisted filters')
