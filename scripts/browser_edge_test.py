#!/usr/bin/env python3
"""Isolated browser tests for pagination, long/XSS content and live ingestion.
Run after go build -o agentlog ./cmd/agentlog; needs Playwright and Chromium.
"""
import json, os, socket, subprocess, tempfile, time, urllib.request
from playwright.sync_api import sync_playwright
with tempfile.TemporaryDirectory() as temp:
 sock=socket.socket();sock.bind(('127.0.0.1',0));port=sock.getsockname()[1];sock.close()
 base=f'http://127.0.0.1:{port}';token='edge-test-token-only'
 proc=subprocess.Popen([os.getenv('AGENTLOG_BIN','./agentlog'),'--listen',f'127.0.0.1:{port}','--db',temp+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:urllib.request.urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  def record(sid,text,i):return {'timeUnixNano':str(1780000000000000000+i*100000000),'attributes':[{'key':'session.id','value':{'stringValue':sid}}],'body':{'stringValue':text}}
  def send(records):
   b={'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeLogs':[{'logRecords':records}]}]}
   req=urllib.request.Request(base+'/v1/logs',json.dumps(b).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
   with urllib.request.urlopen(req) as r:assert r.status==200
  attack='<img src=x onerror="window.xss=true"><script>window.xss=true</script>'+'x'*5000
  send([record('long-session',attack if i==0 else 'event '+str(i),i) for i in range(205)])
  send([record('extra-'+str(i),'other session',i) for i in range(205)])
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox'])
   page=browser.new_page(viewport={'width':390,'height':844});errors=[]
   page.on('pageerror',lambda e:errors.append(str(e)))
   page.goto(base);page.locator('#token').fill(token);page.locator('#loginform button').click()
   page.wait_for_function("() => document.querySelectorAll('.session').length===200")
   page.locator('#nextsessions').click()
   page.wait_for_function("() => document.querySelectorAll('.session').length===6")
   page.locator('#prevsessions').click()
   page.wait_for_function("() => document.querySelectorAll('.session').length===200")
   page.locator('#search').fill('long-session')
   page.wait_for_function("() => document.querySelectorAll('.session').length===1")
   page.locator('.session').click()
   page.wait_for_function("() => document.querySelectorAll('.event').length===200")
   assert page.locator('.event .prose').first.inner_text()==attack
   assert page.evaluate('() => !window.xss')
   assert page.evaluate('() => document.documentElement.scrollWidth <= innerWidth'),'long content overflow'
   page.locator('#nextevents').click()
   page.wait_for_function("() => document.querySelectorAll('.event').length===5")
   send([record('long-session','live update arrived',999)])
   page.wait_for_function("() => document.querySelectorAll('.event').length===6",timeout=10000)
   assert page.get_by_text('live update arrived',exact=True).count()==1
   page.locator('#prevevents').click()
   page.wait_for_function("() => document.querySelectorAll('.event').length===200")
   assert not errors,errors
   browser.close()
  print('PASS: 206 sessions, session/event pagination, 5KB hostile text escaped, 390px layout, live ingestion on a paginated timeline.')
 finally:
  proc.terminate();proc.wait(timeout=15)
