#!/usr/bin/env python3
"""Latest-entry landing, chronological windows and history scroll preservation."""
import json,os,pathlib,socket,subprocess,tempfile,time,urllib.request
from playwright.sync_api import sync_playwright,expect
binary=os.environ.get('AGENTLOG_BIN',str(pathlib.Path(__file__).resolve().parents[1]/'agentlog'))
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}';token='synthetic-timeline-position-token'
 server=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:urllib.request.urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  def send(session,indexes):
   data={'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeLogs':[{'logRecords':[{'timeUnixNano':str(1780000000000000000+i*100000000),'eventName':'gen_ai.user.message','attributes':[{'key':'session.id','value':{'stringValue':session}},{'key':'prompt','value':{'stringValue':f'message {i:04d}'}}]} for i in indexes]}]}]}
   request=urllib.request.Request(base+'/v1/logs',json.dumps(data).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
   with urllib.request.urlopen(request) as response:assert response.status==200
  send('long',range(405));send('newest',[500])
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox']);page=browser.new_page(viewport={'width':1280,'height':800});errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   def bottom():page.wait_for_function('Math.abs(scrollY+innerHeight-document.documentElement.scrollHeight)<2')
   def first(text):expect(page.locator('.event .prose').first).to_have_text(text)
   page.goto(base);page.locator('#token').fill(token);page.locator('#loginform button').click();expect(page.locator('.session').first).to_have_attribute('data-session','codex:newest')
   page.locator('[data-session="codex:long"]').click();first('message 0205');expect(page.locator('.event .prose').last).to_have_text('message 0404');expect(page.locator('.event')).to_have_count(200);bottom();assert page.locator('#nextevents').is_disabled()
   page.reload();first('message 0205');bottom()
   if os.getenv('AGENTLOG_SCREENSHOT'):page.screenshot(path=os.environ['AGENTLOG_SCREENSHOT'])
   page.locator('#prevevents').click();first('message 0005');expect(page.locator('.event .prose').last).to_have_text('message 0204')
   page.locator('.event').nth(60).evaluate("el=>el.scrollIntoView({block:'start'})");anchor=page.locator('.event').nth(60);anchor_id=anchor.get_attribute('data-event');top=anchor.bounding_box()['y']
   send('long',[999]);page.evaluate("document.querySelector('#refresh').click()")
   expect(page.locator('#pageinfo')).to_contain_text('/ 406');assert page.locator('.event').nth(60).get_attribute('data-event')==anchor_id
   page.wait_for_function('(args)=>Math.abs(document.querySelector(`[data-event="${args.id}"]`).getBoundingClientRect().top-args.top)<2',arg={'id':anchor_id,'top':top})
   first('message 0005')
   # Local type filters keep rendering the confirmed page while a new window
   # is pending, including when that request fails.
   failed=[]
   page.route('**/api/session?**offset=0&limit=5',lambda route:failed.append(route))
   page.locator('#prevevents').click();page.wait_for_timeout(30);assert len(failed)==1
   page.locator('[data-kind="conversation"]').click();expect(page.locator('.event')).to_have_count(200)
   failed[0].fulfill(status=500,body='synthetic failure');expect(page.locator('#error')).to_contain_text('HTTP 500')
   first('message 0005');expect(page.locator('.event')).to_have_count(200)
   page.unroute('**/api/session?**offset=0&limit=5');page.locator('[data-kind=""]').click()
   # A delayed Latest request must disable navigation rather than send limit=0.
   held=[]
   def delayed(route):held.append(route)
   page.route('**/api/session?**position=latest',delayed)
   page.locator('#latestevents').click();assert page.locator('#prevevents').is_disabled();assert page.locator('#nextevents').is_disabled();assert page.locator('#origin').is_disabled()
   page.wait_for_timeout(30);assert len(held)==1
   page.evaluate("document.querySelector('#prevevents').click()");assert len(held)==1
   held[0].continue_()
   first('message 0206');expect(page.locator('.event .prose').last).to_have_text('message 0999');bottom();page.unroute('**/api/session?**position=latest')
   page.locator('#origin').select_option('import');page.get_by_text('0 events',exact=True).wait_for()
   page.locator('#origin').select_option('otel');first('message 0206');bottom()
   page.set_viewport_size({'width':390,'height':844});page.reload();first('message 0206');bottom();assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
   page.goto(base);expect(page.locator('.session').first).to_have_attribute('data-session','codex:long')
   assert not errors,errors;browser.close()
 finally:server.terminate();server.wait(timeout=20)
print('PASS: recent-session ordering, indexed latest tail, oldest-to-newest cards, bottom landing/reload/mobile, history anchors, request navigation, origin reset and Latest')
