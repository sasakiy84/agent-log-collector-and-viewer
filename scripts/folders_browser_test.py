#!/usr/bin/env python3
"""Folder overview, scoped session pages and navigation using synthetic data."""
import json,os,pathlib,socket,subprocess,tempfile,time,urllib.parse,urllib.request
from playwright.sync_api import sync_playwright,expect
binary=os.getenv('AGENTLOG_BIN','./agentlog');token='synthetic-folder-browser-token'
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}'
 server=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:urllib.request.urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  cwd='/workspace/検索プロジェクト';other='/srv/research/検索プロジェクト';attack='/work/<img src=x onerror=window.xss=true>'
  def send(source,session,index,path,prompt):
   attrs={'session.id':session,'prompt':prompt}
   if path:attrs['cwd']=path
   return {'resource':{'attributes':[{'key':'service.name','value':{'stringValue':source}}]},'scopeLogs':[{'logRecords':[{'timeUnixNano':str(1783750446508000000+index*1000000000),'eventName':'gen_ai.user.message','attributes':[{'key':k,'value':{'stringValue':v}} for k,v in attrs.items()]}]}]}
  logs=[send('codex' if i%2==0 else 'claude',f'session-{i:03d}',i,cwd,('認証の調査 ' if i<10 else 'Run analysis ')+str(i)) for i in range(305)]
  logs += [send('copilot','other-'+str(i),400+i,other,'Other folder') for i in range(2)]
  logs += [send('codex','unknown',1,'','Unknown folder metadata'),send('codex','hostile',2,attack,'Plain text directory')]
  req=urllib.request.Request(base+'/v1/logs',json.dumps({'resourceLogs':logs}).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
  with urllib.request.urlopen(req) as response:assert response.status==200
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox']);page=browser.new_page(viewport={'width':1280,'height':800});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   page.goto(base);page.locator('#token').fill(token);page.locator('#loginform button').click();expect(page.locator('.folder')).to_have_count(4);expect(page.locator('.session')).to_have_count(0)
   def row(path):return page.locator('.folder').filter(has=page.locator('small').filter(has_text=path))
   expect(row(cwd).locator('.count')).to_have_text('305');expect(row(other).locator('.count')).to_have_text('2');expect(page.locator('.folder').first).to_have_attribute('data-folder',other)
   assert page.locator('.folder img').count()==0 and not page.evaluate('window.xss')
   if os.getenv('AGENTLOG_SCREENSHOT_DIR'):
    output=pathlib.Path(os.environ['AGENTLOG_SCREENSHOT_DIR']);output.mkdir(parents=True,exist_ok=True);page.screenshot(path=str(output/'ui-folders.png'),full_page=True)
   row(cwd).click();expect(page.locator('.session')).to_have_count(200);expect(page.locator('#folderpath')).to_have_text(cwd);expect(page.locator('.session').first).to_have_attribute('data-session','codex:session-304')
   pager=page.locator('#sessionpagination');expect(pager.locator('.pagination-range')).to_have_text('1–200 / 305 sessions')
   pager.get_by_role('button',name='Page 2',exact=True).click();expect(page.locator('.session')).to_have_count(105);page.reload();expect(page.locator('.session')).to_have_count(105)
   back_url=page.url;page.locator('.session').first.click();page.locator('.event').first.wait_for();page.locator('#detailpage > .back').click();expect(page.locator('.session')).to_have_count(105);assert page.url==back_url
   page.locator('#search').fill('認証');expect(page.locator('.session')).to_have_count(10);page.locator('#foldersback').click();expect(page.locator('.folder')).to_have_count(1);expect(row(cwd).locator('.count')).to_have_text('10');assert 'q=' in page.url
   page.locator('#search').fill('');expect(page.locator('.folder')).to_have_count(4)
   page.locator('[data-source="claude"]').click();expect(page.locator('.folder')).to_have_count(1);expect(row(cwd).locator('.count')).to_have_text('152');expect(row(cwd).locator('.source')).to_have_text('Claude Code')
   page.locator('[data-source=""]').click();expect(page.locator('.folder')).to_have_count(4)
   # Failed overview filtering keeps the confirmed groups, URL and filter.
   saved_url=page.url;page.route('**/api/folders?source=codex*',lambda route:route.fulfill(status=500,body='synthetic failure'))
   page.locator('[data-source="codex"]').click();expect(page.locator('#error')).to_contain_text('HTTP 500');expect(page.locator('.folder')).to_have_count(4);assert page.url==saved_url;assert page.locator('[data-source=""]').get_attribute('class')=='active';page.unroute('**/api/folders?source=codex*')
   page.set_viewport_size({'width':390,'height':844});assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   if os.getenv('AGENTLOG_SCREENSHOT_DIR'):page.screenshot(path=str(output/'ui-folders-mobile.png'),full_page=True)
   page.locator('[data-folder=""]').click();expect(page.locator('.session')).to_have_count(1);expect(page.locator('.session')).to_have_attribute('data-session','codex:unknown')
   expect(page.locator('#listtitle')).to_have_text('Folder unavailable');assert not errors,errors;browser.close()
 finally:server.terminate();server.wait(timeout=20)
print('PASS: global folder grouping, full paths/same names/unknown, counts across pages, recent ordering, source/Japanese search, direct reload/back links, failure rollback, XSS and mobile')
