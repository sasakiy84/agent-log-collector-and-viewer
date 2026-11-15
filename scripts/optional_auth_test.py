#!/usr/bin/env python3
"""Unset/empty/configured token modes through Compose config, CLI and browser."""
import json,os,pathlib,socket,subprocess,tempfile,time,urllib.error,urllib.request
from playwright.sync_api import sync_playwright,expect
binary=os.getenv('AGENTLOG_BIN','./agentlog');secret='synthetic-optional-auth-token'
# Compose interpolation must work with no .env and with a blank or set value.
for value in (None,'',secret):
 env={**os.environ};env.pop('AGENTLOG_TOKEN',None)
 if value is not None:env['AGENTLOG_TOKEN']=value
 result=subprocess.run(['docker','compose','--env-file','/dev/null','config','--format','json'],env=env,capture_output=True,text=True,check=True)
 config=json.loads(result.stdout)
 assert config['services']['agentlog']['environment']['AGENTLOG_TOKEN']==(value or '')
 assert config['services']['agentlog']['ports'][0]['host_ip']=='127.0.0.1'
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}';proc=None
 def request(path,body=None,auth=None):
  headers={'Content-Type':'application/json'}
  if auth is not None:headers['Authorization']='Bearer '+auth
  req=urllib.request.Request(base+path,None if body is None else json.dumps(body).encode(),headers=headers)
  return urllib.request.urlopen(req,timeout=5)
 def ready(auth):
  for _ in range(500):
   try:
    with request('/api/status',auth=auth) as response:state=json.load(response)
    if state['state']=='ready' and state['pending_batches']==0:return
   except (OSError,ValueError):pass
   if proc.poll() is not None:raise AssertionError('server exited')
   time.sleep(.02)
  raise TimeoutError('not ready')
 def start(value):
  global proc
  env={**os.environ};env.pop('AGENTLOG_TOKEN',None)
  if value is not None:env['AGENTLOG_TOKEN']=value
  proc=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  ready(value or None)
 def stop():
  global proc
  if proc is not None:proc.terminate();proc.wait(timeout=20);proc=None
 try:
  start(None)
  for signal in ('logs','traces','metrics'):
   with request('/v1/'+signal,{}) as response:assert response.status==200
  # Import CLI must omit authentication when collecting locally.
  transcript=pathlib.Path(folder)/'session.jsonl'
  transcript.write_text(json.dumps({'type':'user','uuid':'local-user','sessionId':'local-session','timestamp':'2026-11-14T00:00:00Z','message':{'content':'hello from local import'}})+'\n')
  env={**os.environ};env.pop('AGENTLOG_TOKEN',None)
  subprocess.run([binary,'import','--source','claude','--file',str(transcript),'--endpoint',base],env=env,check=True,capture_output=True)
  ready(None)
  with sync_playwright() as p:
   browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox']);page=browser.new_page();errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
   for value in (None,''):
    if value is not None:stop();start(value)
    page.goto(base);expect(page.locator('.session')).to_have_count(1);expect(page.locator('#login')).to_be_hidden();expect(page.locator('#logout')).to_be_hidden()
    page.locator('.session').click();expect(page.locator('#sessiontitle')).to_have_text('hello from local import')
    page.goto(base+'/skills');expect(page.locator('#skillpage')).to_be_visible();expect(page.locator('#logout')).to_be_hidden()
    page.goto(base+'/export');expect(page.locator('.session')).to_have_count(1)
    with page.expect_download() as download:page.locator('#downloadexport').click()
    assert pathlib.Path(download.value.path()).stat().st_size>0
   stop();start(secret)
   for path,body in (('/api/status',None),('/api/export/download',None),('/v1/logs',{}),('/v1/traces',{}),('/v1/metrics',{})):
    try:request(path,body);raise AssertionError('anonymous request accepted with configured token')
    except urllib.error.HTTPError as e:assert e.code==401
   page.goto(base);expect(page.locator('#login')).to_be_visible();page.locator('#token').fill(secret);page.locator('#loginform button').click();expect(page.locator('.session')).to_have_count(1);expect(page.locator('#logout')).to_be_visible()
   page.locator('#logout').click();expect(page.locator('#login')).to_be_visible()
   assert not errors,errors;browser.close()
 finally:stop()
print('PASS: Compose absent/empty/configured token, anonymous OTLP/CLI import/UI/ZIP, hidden login/logout, protected mode and signout after restart')
