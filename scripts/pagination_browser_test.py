#!/usr/bin/env python3
"""Numbered pagination on all three screens with isolated synthetic data."""
from test_support import settled_urlopen
import json,os,pathlib,socket,subprocess,tempfile,time,urllib.request
from playwright.sync_api import sync_playwright,expect
binary=os.environ.get('AGENTLOG_BIN','./agentlog')
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}';token='synthetic-pagination-token'
 server=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:settled_urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  def record(session,index,prompt):return {'timeUnixNano':str(1735689600000000000+index*1000000000),'eventName':'gen_ai.user.message','attributes':[{'key':'session.id','value':{'stringValue':session}},{'key':'prompt','value':{'stringValue':prompt}}]}
  records=[record(f'project-{i:04d}',i,f'Review project {i:04d}') for i in range(1001)]
  records += [record('history',i,f'Message {i:04d}') for i in range(1805)]
  data={'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeLogs':[{'logRecords':records}]}]}
  req=urllib.request.Request(base+'/v1/logs',json.dumps(data).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
  with settled_urlopen(req) as r:assert r.status==200
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox']);page=browser.new_page(viewport={'width':1280,'height':800});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   page.goto(base+'/?folder=');page.locator('#token').fill(token);page.locator('#loginform button').click()
   pager=page.locator('#sessionpagination')
   def current(pager,number):expect(pager.locator('[aria-current="page"]')).to_have_text(str(number));expect(pager).to_have_attribute('aria-busy','false')
   def jump(pager,number):pager.get_by_role('spinbutton',name='Page number').fill(str(number));pager.get_by_role('spinbutton',name='Page number').press('Enter')
   current(pager,1);expect(pager.locator('.pagination-range')).to_have_text('1–200 / 1,002 sessions');expect(pager.get_by_role('button',name='Previous page',exact=True)).to_be_disabled()
   pager.get_by_role('button',name='Page 6',exact=True).click();current(pager,6);expect(page.locator('.session')).to_have_count(2);expect(pager.get_by_role('button',name='Next page',exact=True)).to_be_disabled();assert 'offset=1000' in page.url
   page.reload();current(pager,6)
   jump(pager,3);current(pager,3);expect(page.locator('.session')).to_have_count(200)
   expect(pager.get_by_role('spinbutton')).to_be_focused()
   assert pager.locator('.pagination-gap').count()==1
   # Failed navigation keeps the confirmed rows, page number and URL.
   before=page.locator('.session').first.get_attribute('data-session');url=page.url;held=[]
   page.route('**/api/sessions?**offset=600*',lambda route:held.append(route))
   pager.get_by_role('button',name='Page 4',exact=True).click();page.wait_for_timeout(50);assert len(held)==1
   assert pager.get_by_role('spinbutton').is_disabled();assert pager.locator('button:not(:disabled)').count()==0
   held[0].fulfill(status=500,body='synthetic failure');expect(page.locator('#error')).to_contain_text('HTTP 500');current(pager,3);assert page.url==url;assert page.locator('.session').first.get_attribute('data-session')==before
   page.unroute('**/api/sessions?**offset=600*')
   # Search resets pagination and reports the filtered total, including empty.
   page.locator('#search').fill('project');current(pager,1);expect(pager.locator('.pagination-range')).to_have_text('1–200 / 1,001 sessions')
   page.locator('#search').fill('missing-query');expect(page.locator('#sessions .empty')).to_have_text('No sessions');expect(pager.locator('.pagination-range')).to_have_text('0 sessions');assert pager.locator('button:not(:disabled)').count()==0
   page.locator('#search').fill('');expect(pager.locator('.pagination-range')).to_have_text('1–200 / 1,002 sessions')
   # Number input validity blocks out-of-range/fractional/empty page requests.
   requests=[];page.on('request',lambda r:requests.append(r.url) if '/api/' in r.url else None)
   for invalid in ['0','7','1.5','']:
    pager.get_by_role('spinbutton').fill(invalid);pager.get_by_role('spinbutton').press('Enter');assert not pager.get_by_role('spinbutton').evaluate('el=>el.checkValidity()')
   page.wait_for_timeout(100);assert not requests,requests
   jump(pager,4);current(pager,4)
   if os.getenv('AGENTLOG_SCREENSHOT_DIR'):
    output=pathlib.Path(os.environ['AGENTLOG_SCREENSHOT_DIR']);output.mkdir(parents=True,exist_ok=True);page.screenshot(path=str(output/'ui-pagination.png'))
   # A direct historical page URL survives reload; the unqualified URL opens latest.
   page.goto(base+'/sessions/codex%3Ahistory');events=page.locator('#eventpagination');current(events,10);expect(page.locator('.event .prose').first).to_have_text('Message 1800');expect(page.locator('.event')).to_have_count(5)
   page.wait_for_function('Math.abs(scrollY+innerHeight-document.documentElement.scrollHeight)<2')
   jump(events,5);current(events,5);expect(page.locator('.event .prose').first).to_have_text('Message 0800');assert 'page=5' in page.url
   expect(events.get_by_role('spinbutton')).to_be_focused()
   page.reload();current(events,5);expect(page.locator('.event .prose').first).to_have_text('Message 0800')
   expect(events.get_by_role('button',name='Page 1',exact=True)).to_be_visible();expect(events.get_by_role('button',name='Page 10',exact=True)).to_be_visible();assert events.locator('.pagination-gap').count()==2
   # The same controls fit small screens and remain visible at mid-page.
   for width in [390,320]:
    page.set_viewport_size({'width':width,'height':844});page.locator('.event').nth(80).evaluate("el=>el.scrollIntoView({block:'start'})")
    assert page.evaluate('document.documentElement.scrollWidth<=innerWidth'),width
    rect=events.bounding_box();assert rect['y']>=0 and rect['y']+rect['height']<=844,width
    if width==390 and os.getenv('AGENTLOG_SCREENSHOT_DIR'):page.screenshot(path=str(output/'ui-pagination-mobile.png'))
   events.get_by_role('button',name='Show latest events',exact=True).click();current(events,10);assert 'page=' not in page.url
   # Archive pagination shares totals, direct jumps and cross-page selections.
   page.goto(base+'/archive');archive=page.locator('#archivepagination');current(archive,1);expect(archive.locator('.pagination-range')).to_have_text('1–200 / 1,002 sessions')
   checkbox=page.locator('[data-archive-session]').first;selected=checkbox.get_attribute('data-archive-session');checkbox.check()
   jump(archive,6);current(archive,6);expect(page.locator('.archive-row')).to_have_count(2);expect(page.locator('#archiveselected')).to_have_text('1 selected')
   archive.get_by_role('button',name='Page 1',exact=True).click();current(archive,1);expect(page.locator(f'[data-archive-session="{selected}"]')).to_be_checked()
   # Exercise large page labels without constructing a million-record fixture.
   # This is the same embedded component and stylesheet used by all screens.
   page.evaluate("() => {window.stressPager=new Pagination(document.querySelector('#archivepagination'),{noun:'events',onChange:()=>{},onLatest:()=>{}})}")
   for total in [150000,1000000,100000000]:
    page.evaluate('(total)=>stressPager.update({total,offset:Math.floor(total/400)*200,size:200})',total)
    assert page.evaluate('document.documentElement.scrollWidth<=innerWidth'),total
   assert not errors,errors;browser.close()
 finally:server.terminate();server.wait(timeout=20)
print('PASS: shared numbered pagination, totals, endpoints/ellipsis, jump/validation, pending/failure state, filtering/empty, URL reload, latest landing, mobile sticky layout, archive selection')
