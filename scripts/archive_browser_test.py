#!/usr/bin/env python3
"""Synthetic end-to-end archive download and explicit-confirmation deletion."""
import hashlib,json,os,pathlib,socket,sqlite3,subprocess,tempfile,time,urllib.request,zipfile
from playwright.sync_api import sync_playwright,expect
binary=os.environ.get('AGENTLOG_BIN',str(pathlib.Path(__file__).resolve().parents[1]/'agentlog'))
token='synthetic-archive-test-token'
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:
  sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}'
 if os.getenv('AGENTLOG_TEST_LEGACY'):
  with sqlite3.connect(folder+'/test.db') as database:database.execute('CREATE TABLE legacy_fixture(id INTEGER)')
 server=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL)
 try:
  def api(path,body=None):
   request=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
   with urllib.request.urlopen(request) as response:return json.load(response)
  for _ in range(100):
   try:api('/api/stats');break
   except OSError:time.sleep(.05)
  logs=[]
  for i,source in enumerate(['codex','claude','copilot']):
   logs.append({'resource':{'attributes':[{'key':'service.name','value':{'stringValue':source}}]},'scopeLogs':[{'logRecords':[{'timeUnixNano':'1735689600000000000','eventName':'user_prompt','attributes':[{'key':'session.id','value':{'stringValue':f'archive-example-{i}'}},{'key':'prompt','value':{'stringValue':'Synthetic old session'}}]}]}]})
  api('/v1/logs',{'resourceLogs':logs})
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox'])
   page=browser.new_page(viewport={'width':1280,'height':800});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   page.goto(base+'/archive');page.locator('#token').fill(token);page.locator('#loginform button').click();expect(page.locator('.archive-row')).to_have_count(3)
   assert page.locator('#listpage').is_hidden()
   page.locator('[data-archive-session]').first.check();assert page.locator('#archiveselected').inner_text()=='1 selected'
   page.locator('#archiveall').click();assert page.locator('#archiveselected').inner_text()=='3 selected';page.locator('#archivecreate').click()
   page.locator('#archivedownload').wait_for(timeout=15000)
   assert page.locator('#archivedelete').is_disabled()
   assert api('/api/stats')['events']==3
   with page.expect_download() as capture:page.locator('#archivedownload').click()
   download=capture.value;archive=pathlib.Path(folder)/'download.zip';download.save_as(archive)
   job=api('/api/archive')['job'];assert hashlib.sha256(archive.read_bytes()).hexdigest()==job['sha256']
   with zipfile.ZipFile(archive) as zipped:
    assert zipped.testzip() is None
    assert len([n for n in zipped.namelist() if n.startswith('sessions/')])==3
   page.locator('#archiveconfirmation').fill('DOWNLOADED')
   # Polling must preserve the typed confirmation and focused input.
   page.wait_for_timeout(5500);assert page.locator('#archiveconfirmation').input_value()=='DOWNLOADED'
   assert api('/api/stats')['events']==3
   if os.getenv('AGENTLOG_SCREENSHOT'):page.screenshot(path=os.environ['AGENTLOG_SCREENSHOT'],full_page=True)
   page.set_viewport_size({'width':390,'height':844});assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   page.locator('#archivedelete').click();page.get_by_text('Archive complete',exact=True).wait_for(timeout=15000)
   assert api('/api/stats')['events']==0
   if os.getenv('AGENTLOG_TEST_LEGACY'):
    assert api('/api/archive')['job']['rebuild_required']
    page.locator('#archivecompact').click();expect(page.locator('#archivecompact')).to_have_count(0)
    page.get_by_text('Archive complete',exact=True).wait_for(timeout=15000)
    assert not api('/api/archive')['job']['rebuild_required']
   api('/v1/logs',{'resourceLogs':logs});assert api('/api/stats')['events']==0
   assert not errors,errors
   browser.close()
 finally:
  server.terminate();server.wait(timeout=20)
print('PASS: inactive list, selection, native ZIP download/CRC/SHA, polling preserves input, mobile, explicit deletion, replay suppression')
