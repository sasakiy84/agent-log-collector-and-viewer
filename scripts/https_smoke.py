#!/usr/bin/env python3
"""Verify Compose HTTPS using Caddy's local CA, without public ACME requests."""
import json,os,pathlib,secrets,ssl,subprocess,sys,tempfile,time,urllib.error,urllib.request
project='agentlog-https-'+secrets.token_hex(4);token=secrets.token_hex(24)
env={**os.environ,'AGENTLOG_TOKEN':token,'AGENTLOG_DOMAIN':'localhost','AGENTLOG_PORT':'0'}
with tempfile.TemporaryDirectory() as tmp:
 override=pathlib.Path(tmp)/'ports.yaml';override.write_text('services:\n  caddy:\n    ports: !override\n      - "127.0.0.1:0:80"\n      - "127.0.0.1:0:443"\n')
 compose=['docker','--host=unix:///var/run/docker.sock','compose','-p',project,'-f','compose.yaml','-f','compose.https.yaml','-f',str(override)]
 def run(*args):return subprocess.check_output([*compose,*args],env=env,text=True,stderr=subprocess.DEVNULL).strip()
 try:
  run('up','-d','--no-build')
  ca=pathlib.Path(tmp)/'root.crt'
  for _ in range(120):
   try:run('cp','caddy:/data/caddy/pki/authorities/local/root.crt',str(ca));break
   except subprocess.CalledProcessError:time.sleep(.25)
  if not ca.exists():raise RuntimeError('Caddy CA was not created')
  context=ssl.create_default_context(cafile=str(ca))
  def base():return 'https://localhost:'+run('port','caddy','443').rsplit(':',1)[1]
  def request(path,body=None,auth=True):
   headers={'Content-Type':'application/json'}
   if auth:headers['Authorization']='Bearer '+token
   req=urllib.request.Request(base()+path,None if body is None else json.dumps(body).encode(),headers=headers)
   return urllib.request.urlopen(req,context=context,timeout=5)
  for _ in range(120):
   try:
    with request('/api/stats') as res:assert json.load(res)['events']==0
    break
   except OSError as e:last_error=e;time.sleep(.25)
  else:raise RuntimeError('HTTPS proxy did not become ready: '+str(last_error))
  try:request('/api/stats',auth=False);raise AssertionError('Authentication bypassed')
  except urllib.error.HTTPError as e:assert e.code==401
  with request('/api/login',{'token':token},False) as res:assert 'Secure' in res.headers['Set-Cookie'],'Forwarded HTTPS did not produce secure cookie'
  body={'resourceLogs':[{'scopeLogs':[{'logRecords':[{'body':{'stringValue':'TLS verified'}}]}]}]}
  with request('/v1/logs',body) as res:assert res.status==200
  with request('/api/stats') as res:assert json.load(res)['events']==1
  original=ca.read_bytes()
  run('up','-d','--no-build','--force-recreate')
  run('cp','caddy:/data/caddy/pki/authorities/local/root.crt',str(ca));assert ca.read_bytes()==original,'CA changed on recreation'
  for _ in range(120):
   try:
    with request('/api/stats') as res:assert json.load(res)['events']==1
    break
   except OSError as e:last_error=e;time.sleep(.25)
  else:raise RuntimeError('HTTPS did not recover after recreation')
  print('PASS: verified HTTPS, authenticated OTLP, secure cookie, persistent CA and SQLite across Compose recreation.')
 except Exception:
  print(run('logs','--no-color','--tail','25'),file=sys.stderr);raise
 finally:run('down','--volumes','--remove-orphans')
