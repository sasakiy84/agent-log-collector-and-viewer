#!/usr/bin/env python3
"""Reported usage: scoped totals, missing/zero values, latest access and imports."""
import json,os,pathlib,socket,subprocess,tempfile,time,urllib.request
from playwright.sync_api import sync_playwright,expect
binary=os.getenv('AGENTLOG_BIN','./agentlog');token='synthetic-usage-browser-token'
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}'
 proc=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:urllib.request.urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  def api(path,data=None):
   request=urllib.request.Request(base+path,None if data is None else json.dumps(data).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
   with urllib.request.urlopen(request) as response:return json.load(response)
  def record(session,index,attrs,name='gen_ai.user.message'):
   return {'timeUnixNano':str(1780000000000000000+index*1000000000),'eventName':name,'attributes':[{'key':k,'value':{'stringValue':str(v)}} for k,v in {'session.id':session,**attrs}.items()]}
  records=[record('main',i,{'prompt':f'Continue analysis {i}'}) for i in range(205)]
  records += [record('main',210,{'input_tokens':'1000','output_tokens':'100','cache_read_tokens':'200','cost_usd':'0.0123','duration_ms':'2000'},'api_request'),record('main',211,{'input_tokens':'3000','output_tokens':'200','cache_read_tokens':'500','cost_usd':'0.0877','duration_ms':'1000'},'api_request'),record('main',212,{'tool_name':'Bash','success':'false','error':'Synthetic test failure'},'tool_result'),record('missing',0,{'prompt':'Usage was not exported'})]
  api('/v1/logs',{'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'claude'}},{'key':'cwd','value':{'stringValue':'/work/analysis'}}]},'scopeLogs':[{'logRecords':records}]}]})
  # Repeated token_count records carry the same cumulative report twice.
  path=pathlib.Path(folder)/'codex.jsonl';rows=[{'type':'session_meta','payload':{'id':'counter','cwd':'/work/codex'}}]
  for i,(input,output,cache) in enumerate([(100,20,50),(100,20,50),(250,40,100)]):
   rows.append({'timestamp':f'2026-05-29T00:00:0{i}Z','type':'event_msg','payload':{'type':'token_count','info':{'last_token_usage':{'input_tokens':100,'output_tokens':20,'cached_input_tokens':50},'total_token_usage':{'input_tokens':input,'output_tokens':output,'cached_input_tokens':cache,'total_tokens':input+output}}}})
  path.write_text(''.join(json.dumps(r)+'\n' for r in rows))
  subprocess.run([binary,'import','--source','codex','--file',str(path),'--endpoint',base],env={**os.environ,'AGENTLOG_TOKEN':token},check=True,capture_output=True)
  assert api('/api/session?id=codex:counter')['usage']['values']['input_tokens']=='250'
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox']);page=browser.new_page(viewport={'width':1440,'height':900});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   page.goto(base);page.locator('#token').fill(token);page.locator('#loginform button').click();expect(page.locator('.folder')).to_have_count(2)
   row=page.locator('[data-folder="/work/analysis"]');expect(row.locator('.usage-row')).to_contain_text('4.7K in');expect(row.locator('.usage-row')).to_contain_text('$0.1');expect(row.locator('.usage-partial')).to_be_visible();assert '1/2 sessions reporting' in row.locator('.usage-row').get_attribute('title')
   if os.getenv('AGENTLOG_SCREENSHOT_DIR'):
    output=pathlib.Path(os.environ['AGENTLOG_SCREENSHOT_DIR']);output.mkdir(exist_ok=True,parents=True);page.screenshot(path=str(output/'ui-usage-folders.png'),full_page=True)
   row.click();expect(page.locator('.session')).to_have_count(2);page.locator('[data-session="claude:main"]').click()
   page.wait_for_function('() => Math.abs(scrollY+innerHeight-document.documentElement.scrollHeight)<2')
   expect(page.locator('.usage-shortcut')).to_have_text('5K tokens · $0.1')
   page.locator('.usage-shortcut').click();expect(page.locator('#sessionusage')).to_be_focused();expect(page.locator('.usage-heading')).to_be_in_viewport()
   def total(label):return page.locator('.usage-totals > div').filter(has=page.get_by_text(label,exact=True)).locator('dd')
   expect(total('Input')).to_have_text('4,700');expect(total('Output')).to_have_text('300');expect(total('Cache read')).to_have_text('700');expect(total('Cached input')).to_have_text('14.9%');expect(total('USD cost')).to_have_text('$0.1');expect(total('Error events')).to_have_text('1')
   page.locator('.usage-more summary').click();expect(page.locator('.usage-more')).to_contain_text('1.50 s')
   page.locator('[data-kind="tool"]').click();expect(total('Input')).to_have_text('4,700')
   if os.getenv('AGENTLOG_SCREENSHOT_DIR'):page.screenshot(path=str(output/'ui-usage-session.png'))
   page.locator('#origin').select_option('import');expect(page.locator('.usage-unreported')).to_have_text('Usage not reported')
   page.locator('#origin').select_option('otel');expect(total('Input')).to_have_text('4,700')
   page.set_viewport_size({'width':390,'height':844});page.reload();page.locator('.usage-shortcut').click();assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   if os.getenv('AGENTLOG_SCREENSHOT_DIR'):page.screenshot(path=str(output/'ui-usage-mobile.png'))
   page.goto(base+'/sessions/codex%3Acounter');expect(page.locator('.event')).to_have_count(3);expect(page.locator('.event h2').first).to_have_text('Token usage');expect(total('Input')).to_have_text('250');expect(total('Output')).to_have_text('40')
   # Browser integer formatting retains values beyond Number.MAX_SAFE_INTEGER.
   huge=record('precision',500,{'gen_ai.usage.input_tokens':'9007199254740993','gen_ai.usage.output_tokens':'0','cost_usd':'0'},'api_request')
   api('/v1/logs',{'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'claude'}}]},'scopeLogs':[{'logRecords':[huge]}]}]})
   page.goto(base+'/sessions/claude%3Aprecision');expect(total('Input')).to_have_text('9,007,199,254,740,993');expect(total('Output')).to_have_text('0');expect(total('USD cost')).to_have_text('$0');assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   assert not errors,errors;browser.close()
 finally:proc.terminate();proc.wait(timeout=20)
print('PASS: folder/session usage, full-history and origin scopes, latest shortcut, missing/zero, cache/USD/latency/errors, Codex counter replay and label, exact BigInt formatting and mobile')
