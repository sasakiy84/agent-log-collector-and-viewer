#!/usr/bin/env python3
"""Folder archive selection, pushed progress, ZIP and explicit deletion. Synthetic data."""
from test_support import settled_urlopen
import datetime,hashlib,json,os,pathlib,re,socket,sqlite3,subprocess,tempfile,time,urllib.request,zipfile
from playwright.sync_api import sync_playwright,expect
binary=os.environ.get('AGENTLOG_BIN',str(pathlib.Path(__file__).resolve().parents[1]/'agentlog'))
token='synthetic-archive-test-token'
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}'
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
   storage=page.locator('#archivestorage')
   expect(storage).to_have_text(re.compile(r'^Storage available: [\d.]+ [KMGT]?i?B$'))
   assert isinstance(api('/api/archive?group=folders')['available_bytes'],int)
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
   field=page.locator('#archiveconfirmation').bounding_box();delete=page.locator('#archivedelete').bounding_box()
   assert abs(field['x']+field['width']-delete['x']-delete['width'])<1,'confirmation and delete action right edges differ'
   assert field['height']>=32,'confirmation field too short'
   if os.getenv('AGENTLOG_SCREENSHOT'):page.screenshot(path=str(pathlib.Path(os.environ['AGENTLOG_SCREENSHOT']).with_name('ui-archive-mobile.png')))
   with sqlite3.connect(folder+'/test.db') as db:
    db.execute("CREATE TRIGGER interrupt_archive BEFORE DELETE ON records BEGIN SELECT RAISE(ABORT,'test interruption'); END")
   page.locator('#archivedelete').click()
   expect(page.get_by_role('heading',name='Deletion paused')).to_be_visible()
   expect(page.locator('#archiveselection')).to_be_hidden()
   assert api('/api/stats')['events']==5
   if os.getenv('AGENTLOG_SCREENSHOT'):
    page.screenshot(path=str(pathlib.Path(os.environ['AGENTLOG_SCREENSHOT']).with_name('ui-archive-retry.png')))
   with sqlite3.connect(folder+'/test.db') as db:
    db.execute('DROP TRIGGER interrupt_archive')
   page.get_by_role('button',name='Retry deletion').click()
   expect(page.locator('#archivejob h2')).to_have_text('Archive complete',timeout=30000)
   expect(page.locator('.archive-row')).to_have_count(2);assert api('/api/stats')['events']==3
   extra_logs=[]
   for i in range(199):
    values={'session.id':f'old-{i}','cwd':f'/archive-test/{i}'}
    record={'timeUnixNano':'1735689600000000000','eventName':'user_prompt','attributes':[{'key':k,'value':{'stringValue':v}} for k,v in values.items()]}
    extra_logs.append(record)
   api('/v1/logs',{'resourceLogs':[{'resource':logs[0]['resource'],'scopeLogs':[{'logRecords':extra_logs}]}]})
   page.locator('#closearchive').click()
   page.locator('#dataoptions summary').click()
   page.locator('#openarchive').click()
   select_all=page.get_by_role('button',name='Select all',exact=True)
   expect(page.locator('.archive-row')).to_have_count(200)
   select_all.focus();page.keyboard.press('Enter')
   expect(page.locator('#archiveselected')).to_have_text('201 folders selected')
   expect(page.locator('[data-archive-folder]:checked')).to_have_count(200)
   page.get_by_role('button',name='Next page',exact=True).click()
   expect(page.locator('[data-archive-folder]:checked')).to_have_count(1)
   page.get_by_role('button',name='Clear',exact=True).click()
   expect(page.locator('[data-archive-folder]:checked')).to_have_count(0)
   expect(page.locator('#archivecreate')).to_be_disabled()
   select_all.click()
   last_folder=page.locator('[data-archive-folder]').first
   expect(last_folder).to_be_checked()
   last_folder.uncheck()
   expect(page.locator('#archiveselected')).to_have_text('200 folders selected')
   if os.getenv('AGENTLOG_SCREENSHOT'):
    page.screenshot(path=str(pathlib.Path(os.environ['AGENTLOG_SCREENSHOT']).with_name('ui-archive-select-all.png')))
   page.locator('#archivecreate').click()
   expect(page.locator('#archivedownload')).to_be_visible(timeout=30000)
   assert api('/api/archive')['job']['session_count']==200
   with page.expect_download() as capture:page.locator('#archivedownload').click()
   capture.value.save_as(pathlib.Path(folder)/'second.zip');page.locator('#archiveconfirmation').fill('DOWNLOADED');page.locator('#archivedelete').click()
   expect(page.locator('#archivejob h2')).to_have_text('Archive complete',timeout=30000);expect(page.locator('.archive-row')).to_have_count(1)
   expect(storage).to_have_text(re.compile(r'^Storage available: [\d.]+ [KMGT]?i?B$'))
   assert api('/api/stats')['events']==2
   api('/v1/logs',{'resourceLogs':logs});assert api('/api/stats')['events']==2
   page.keyboard.press('Escape');expect(page.locator('#archivepanel')).not_to_be_visible();page.goto(base+'/archive');expect(page.locator('#archivepanel')).to_be_visible();page.keyboard.press('Escape');page.goto(base);expect(page.locator('.session')).to_have_count(2);assert page.locator('[data-source]').count()==0;assert page.locator('#refresh').count()==0
   def storage_unavailable(route):
    response=route.fetch()
    payload=response.json()
    payload['available_bytes']=None
    route.fulfill(response=response,json=payload)
   page.route(re.compile(r'/api/archive\?group=folders'), storage_unavailable)
   page.locator('#dataoptions summary').click()
   page.locator('#openarchive').click()
   expect(storage).to_have_text('Storage availability unknown')
   expect(page.locator('.archive-row')).to_have_count(1)
   page.locator('#closearchive').press('Enter')
   page.locator('#dataoptions summary').press('Enter')
   page.get_by_role('button',name='Delete sessions…',exact=True).press('Enter')
   expect(page.get_by_role('heading',name='Delete sessions',exact=True)).to_be_visible()
   expect(page.locator('[data-delete-session]')).to_have_count(2)
   page.get_by_label('Days inactive').fill('30')
   page.get_by_label('Sort by').select_option('oldest')
   page.get_by_role('button',name='Apply',exact=True).press('Enter')
   expect(page.locator('[data-delete-session]')).to_have_count(1)
   expect(page.locator('#archivepagination .pagination-range')).to_contain_text('sessions')
   page.get_by_role('button',name='Select page').press('Enter')
   expect(page.locator('[data-delete-session]')).to_be_checked()
   assert page.evaluate("document.querySelector('#archivepanel').scrollWidth<=document.querySelector('#archivepanel').clientWidth")
   if os.getenv('AGENTLOG_SCREENSHOT'):
    page.screenshot(path=str(pathlib.Path(os.environ['AGENTLOG_SCREENSHOT']).with_name('ui-delete-sessions.png')))
   delete_selected=page.get_by_role('button',name='Delete selected sessions')
   page.once('dialog',lambda dialog:dialog.dismiss())
   delete_selected.press('Enter')
   assert api('/api/stats')['events']==2
   page.once('dialog',lambda dialog:dialog.accept())
   delete_selected.press('Enter')
   expect(page.get_by_role('heading',name='Deletion complete')).to_be_visible(timeout=30000)
   expect(page.get_by_text('No matching sessions')).to_be_visible()
   assert api('/api/stats')['events']==1
   assert not errors,errors;browser.close()
 finally:server.terminate();server.wait(timeout=20)
print('PASS: folder/mixed-agent/multiple/unassigned selection, active-session protection, pushed progress, no refresh/polling, ZIP SHA/CRC, explicit deletion, pause and retry, replay suppression, available storage, direct deletion and mobile')
