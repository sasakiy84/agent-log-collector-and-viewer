#!/usr/bin/env python3
"""Folder archive selection, pushed progress, ZIP and explicit deletion. Synthetic data."""
from test_support import settled_urlopen
import datetime,hashlib,json,os,pathlib,socket,sqlite3,subprocess,tempfile,time,urllib.request,zipfile
from playwright.sync_api import sync_playwright,expect
binary=os.environ.get('AGENTLOG_BIN',str(pathlib.Path(__file__).resolve().parents[1]/'agentlog'))
token='synthetic-archive-test-token'
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}'
 if os.getenv('AGENTLOG_TEST_LEGACY'):
  with sqlite3.connect(folder+'/test.db') as database:database.execute('CREATE TABLE legacy_fixture(id INTEGER)')
 server=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 try:
  def api(path,body=None):
   request=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
   with settled_urlopen(request) as response:return json.load(response)
  for _ in range(200):
   try:api('/api/stats');break
   except OSError:time.sleep(.05)
  logs=[]
  for source,sid,path,recent in [('codex','one','/work/alpha',False),('claude','two','/work/alpha',False),('copilot','three','/work/beta',False),('codex','live','/work/alpha',True),('other','unknown','',False)]:
   values={'session.id':sid,'prompt':'Synthetic old session' if not recent else 'Current work'}
   if path:values['cwd']=path
   record={'timeUnixNano':str(time.time_ns()) if recent else '1735689600000000000','eventName':'user_prompt','attributes':[{'key':k,'value':{'stringValue':v}} for k,v in values.items()]}
   logs.append({'resource':{'attributes':[{'key':'service.name','value':{'stringValue':source}}]},'scopeLogs':[{'logRecords':[record]}]})
  api('/v1/logs',{'resourceLogs':logs})
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox'])
   page=browser.new_page(viewport={'width':1280,'height':800});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   page.goto(base);archive_requests=[];page.on('request',lambda r:archive_requests.append(r.url) if '/api/archive' in r.url else None);page.locator('#token').fill(token);page.locator('#loginform button').click();expect(page.locator('.session')).to_have_count(5);assert not archive_requests;page.locator('#dataoptions summary').click()
   if os.getenv('AGENTLOG_SCREENSHOT'):page.screenshot(path=str(pathlib.Path(os.environ['AGENTLOG_SCREENSHOT']).with_name('ui-data-actions.png')),full_page=True)
   page.locator('#openarchive').click();expect(page.locator('#archivepanel')).to_be_visible();expect(page.locator('.archive-row')).to_have_count(3)
   assert page.locator('#refresh,#subagentrefresh,#archiveall,[data-source]').count()==0
   assert 'DB data only' not in page.locator('body').inner_text()
   expect(page.locator('.archive-row').filter(has=page.locator('[data-archive-folder="/work/alpha"]'))).to_contain_text('2 sessions')
   if os.getenv('AGENTLOG_SCREENSHOT'):
    page.screenshot(path=os.environ['AGENTLOG_SCREENSHOT'])
   page.locator('[data-archive-folder="/work/alpha"]').check();expect(page.locator('#archiveselected')).to_have_text('1 folder selected');page.locator('#archivecreate').click()
   expect(page.locator('#archivedownload')).to_be_visible(timeout=30000)
   assert page.locator('#archivedelete').is_disabled();assert api('/api/stats')['events']==5
   with page.expect_download() as capture:page.locator('#archivedownload').click()
   archive=pathlib.Path(folder)/'download.zip';capture.value.save_as(archive)
   job=api('/api/archive')['job'];assert job['session_count']==2;assert hashlib.sha256(archive.read_bytes()).hexdigest()==job['sha256']
   with zipfile.ZipFile(archive) as zipped:
    assert zipped.testzip() is None
    assert len([n for n in zipped.namelist() if n.startswith('sessions/')])==2
   if os.getenv('AGENTLOG_SCREENSHOT'):page.screenshot(path=str(pathlib.Path(os.environ['AGENTLOG_SCREENSHOT']).with_name('ui-data-download-confirmation.png')))
   page.locator('#closearchive').click();expect(page.locator('#archivepanel')).not_to_be_visible();page.locator('#dataoptions summary').click();page.locator('#openarchive').click();expect(page.locator('#archivedownload')).to_be_visible()
   page.locator('#archiveconfirmation').fill('DOWNLOADED')
   requests=[];page.on('request',lambda r:requests.append(r.url) if '/api/' in r.url else None)
   page.wait_for_timeout(5500);assert not requests,requests;assert page.locator('#archiveconfirmation').input_value()=='DOWNLOADED'
   page.set_viewport_size({'width':390,'height':844});assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   if os.getenv('AGENTLOG_SCREENSHOT'):page.screenshot(path=str(pathlib.Path(os.environ['AGENTLOG_SCREENSHOT']).with_name('ui-archive-mobile.png')))
   page.locator('#archivedelete').click();expect(page.locator('#archivejob h2')).to_have_text('Archive complete',timeout=30000)
   expect(page.locator('.archive-row')).to_have_count(2);assert api('/api/stats')['events']==3
   if os.getenv('AGENTLOG_TEST_LEGACY'):
    assert api('/api/archive')['job']['rebuild_required'];page.locator('#archivecompact').click()
    expect(page.locator('#archivecompact')).to_have_count(0);expect(page.locator('#archivejob h2')).to_have_text('Archive complete',timeout=30000)
    assert not api('/api/archive')['job']['rebuild_required']
   page.locator('[data-archive-folder="/work/beta"]').check();page.locator('[data-archive-folder=""]').check();expect(page.locator('#archiveselected')).to_have_text('2 folders selected');page.locator('#archivecreate').click()
   expect(page.locator('#archivedownload')).to_be_visible(timeout=30000)
   with page.expect_download() as capture:page.locator('#archivedownload').click()
   capture.value.save_as(pathlib.Path(folder)/'second.zip');page.locator('#archiveconfirmation').fill('DOWNLOADED');page.locator('#archivedelete').click()
   expect(page.locator('#archivejob h2')).to_have_text('Archive complete',timeout=30000);expect(page.get_by_text('No inactive folders',exact=True)).to_be_visible()
   assert api('/api/stats')['events']==1
   api('/v1/logs',{'resourceLogs':logs});assert api('/api/stats')['events']==1
   page.keyboard.press('Escape');expect(page.locator('#archivepanel')).not_to_be_visible();page.goto(base+'/archive');expect(page.locator('#archivepanel')).to_be_visible();page.keyboard.press('Escape');page.goto(base);expect(page.locator('.session')).to_have_count(1);assert page.locator('[data-source]').count()==0;assert page.locator('#refresh').count()==0
   assert not errors,errors;browser.close()
 finally:server.terminate();server.wait(timeout=20)
print('PASS: folder/mixed-agent/multiple/unassigned selection, active-session protection, pushed progress, no refresh/polling, ZIP SHA/CRC, explicit deletion, replay suppression and mobile')
