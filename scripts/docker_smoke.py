#!/usr/bin/env python3
"""Test a locally built Compose image using an isolated, disposable project/volume."""
import json,os,secrets,subprocess,time,urllib.error,urllib.request
project='agentlog-smoke-'+secrets.token_hex(4)
token=secrets.token_hex(24)
env={**os.environ,'AGENTLOG_TOKEN':token,'AGENTLOG_PORT':'0'}
compose=['docker','--host=unix:///var/run/docker.sock','compose','-p',project,'-f','compose.yaml']
def run(*args):return subprocess.check_output([*compose,*args],env=env,text=True).strip()
def base():return 'http://'+run('port','agentlog','4318')
def request(path,body=None,auth=token):
 b=None if body is None else json.dumps(body).encode()
 req=urllib.request.Request(base()+path,b,headers={'Content-Type':'application/json','Authorization':'Bearer '+auth})
 with urllib.request.urlopen(req,timeout=5) as response:return json.load(response)
def ready():
 for _ in range(100):
  try:return request('/api/stats')
  except (OSError,subprocess.CalledProcessError):time.sleep(.2)
 raise RuntimeError('Compose service did not become ready')
try:
 run('up','-d','--no-build')
 assert ready()=={'events':0,'sessions':0}
 cid=run('ps','-q','agentlog')
 config=json.loads(subprocess.check_output(['docker','--host=unix:///var/run/docker.sock','inspect',cid],text=True))[0]
 assert config['Config']['User']=='65532:65532'
 assert config['HostConfig']['ReadonlyRootfs']
 record={'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeLogs':[{'logRecords':[{'timeUnixNano':'1794500000000000000','body':{'stringValue':'compose persistence test'},'attributes':[{'key':'conversation.id','value':{'stringValue':'compose-test'}}]}]}]}]}
 request('/v1/logs',record)
 assert request('/api/stats')['events']==1
 try:request('/api/stats',auth='incorrect');raise AssertionError('Unauthenticated access accepted')
 except urllib.error.HTTPError as e:assert e.code==401
 run('up','-d','--no-build','--force-recreate')
 assert ready()['events']==1,'Data lost on recreation'
 request('/v1/logs',record)
 assert request('/api/stats')['events']==1,'Replay was not deduplicated'
 assert request('/api/events?session=codex:compose-test')[0]['name']=='compose persistence test'
 print('PASS: Compose non-root/read-only startup, authenticated ingestion, recreation persistence, replay deduplication.')
finally:
 run('down','--volumes','--remove-orphans')
