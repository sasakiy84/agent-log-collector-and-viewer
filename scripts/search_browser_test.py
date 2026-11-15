#!/usr/bin/env python3
"""Japanese FTS and IME composition against a fresh isolated collector."""
import json,os,pathlib,socket,subprocess,tempfile,time,urllib.request
from playwright.sync_api import sync_playwright,expect
binary=os.environ.get('AGENTLOG_BIN',str(pathlib.Path(__file__).resolve().parents[1]/'agentlog'))
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}';token='synthetic-japanese-search-token'
 server=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:urllib.request.urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  def attrs(values):return [{'key':key,'value':{'stringValue':value}} for key,value in values.items()]
  logs=[]
  for source,session,prompts in [('codex','japanese-auth',['日本語の認証エラーを調査しました。','猫が走った。']),('claude','japanese-password',['パスワードの変更をお願いします。']),('copilot','japanese-json',[json.dumps({'設定項目':'接続障害の診断'},ensure_ascii=True)])]:
   logs.append({'resource':{'attributes':attrs({'service.name':source,'service.namespace':'決済基盤'})},'scopeLogs':[{'logRecords':[{'eventName':'gen_ai.user.message','attributes':attrs({'session.id':session,'prompt':prompt})} for prompt in prompts]}]})
  request=urllib.request.Request(base+'/v1/logs',json.dumps({'resourceLogs':logs}).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
  with urllib.request.urlopen(request) as response:assert response.status==200
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox'])
   page=browser.new_page(viewport={'width':1280,'height':800});errors=[];page.on('pageerror',lambda error:errors.append(str(error)))
   page.goto(base+'/?folder=');page.locator('#token').fill(token);page.locator('#loginform button').click();expect(page.locator('.session')).to_have_count(3)
   requests=[];page.on('request',lambda r:requests.append(r.url) if '/api/sessions?' in r.url else None)
   # Native IME pre-edit must not issue a search until composition is committed.
   page.locator('#search').dispatch_event('compositionstart');page.locator('#search').fill('認証');page.wait_for_timeout(400);assert not requests,requests
   page.locator('#search').dispatch_event('compositionend');expect(page.locator('.session')).to_have_count(1)
   assert page.locator('.session').get_attribute('data-session')=='codex:japanese-auth'
   assert page.locator('.session .count').inner_text()=='2'
   for query,session in [('調査する','codex:japanese-auth'),('走る','codex:japanese-auth'),('ﾊﾟｽﾜｰﾄﾞ','claude:japanese-password'),('パスワード','claude:japanese-password'),('設定項目','copilot:japanese-json'),('接続','copilot:japanese-json')]:
    page.locator('#search').fill(query);expect(page.locator('.session')).to_have_count(1);expect(page.locator('.session')).to_have_attribute('data-session',session)
   page.locator('[data-source="codex"]').click();page.get_by_text('No sessions',exact=True).wait_for()
   page.locator('[data-source=""]').click();expect(page.locator('.session')).to_have_count(1)
   page.locator('#search').fill('決済');expect(page.locator('.session')).to_have_count(3)
   if os.getenv('AGENTLOG_SCREENSHOT'):page.screenshot(path=os.environ['AGENTLOG_SCREENSHOT'],full_page=True)
   page.set_viewport_size({'width':390,'height':844});assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   assert not errors,errors;browser.close()
 finally:server.terminate();server.wait(timeout=20)
print('PASS: Japanese IME, 2-character words, inflection, width/dakuten normalization, embedded JSON keys/values, project/agent filters, complete counts and mobile')
