#!/usr/bin/env python3
"""Test a locally built Compose image using an isolated, disposable project/volume."""
import json,os,pathlib,secrets,sqlite3,subprocess,tempfile,time,urllib.error,urllib.request
project='agentlog-smoke-'+secrets.token_hex(4)
token=secrets.token_hex(24)
env={**os.environ,'AGENTLOG_TOKEN':token,'AGENTLOG_PORT':'0'}
compose=['docker','--host=unix:///var/run/docker.sock','compose','-p',project,'-f','compose.yaml']
def run(*args):return subprocess.check_output([*compose,*args],env=env,text=True).strip()
def base():return 'http://'+run('port','agentlog','4318')
def request(path,body=None,auth=token,timeout=5):
 b=None if body is None else json.dumps(body).encode()
 req=urllib.request.Request(base()+path,b,headers={'Content-Type':'application/json','Authorization':'Bearer '+auth})
 with urllib.request.urlopen(req,timeout=timeout) as response:return json.load(response)
def ready():
 for _ in range(100):
  try:return request('/api/stats')
  except (OSError,subprocess.CalledProcessError):time.sleep(.2)
 raise RuntimeError('Compose service did not become ready')
def large_history(cid):
 # Seed only synthetic rows. A long history must not exhaust the 64 MiB /tmp
 # mount while SQLite materializes timeline groups and preview selection.
 run('stop','agentlog')
 docker=['docker','--host=unix:///var/run/docker.sock']
 with tempfile.TemporaryDirectory() as tmp:
  database=pathlib.Path(tmp)/'agentlog.db'
  subprocess.run([*docker,'cp',cid+':/data/agentlog.db',str(database)],check=True)
  with sqlite3.connect(database) as db:
   db.execute('DELETE FROM event_keys')
   db.execute('DELETE FROM events')
   def rows():
    for session in range(500):
     sid='codex:scale-'+f'{session:060d}'
     raw=json.dumps({'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'record':{'eventName':'gen_ai.user.message','attributes':[{'key':'session.id','value':{'stringValue':sid}},{'key':'agentlog.origin','value':{'stringValue':'import'}},{'key':'prompt','value':{'stringValue':'Synthetic scale fixture'}}]}}).encode()
     for sequence in range(300):
      yield ('import:'+f'{session*300+sequence:064x}',sid,'','codex','logs','2026-11-14T00:00:00.000000000Z','gen_ai.user.message','','','',raw,sequence,'import','user','')
   db.executemany('INSERT INTO events(id,session,parent,source,signal,time,name,trace,span,parent_span,raw,sequence,origin,kind,tool_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)',rows())
  db.close()
  # docker cp writes as root; this disposable fixture must remain writable by
  # the image's unprivileged user inside the private test volume.
  database.chmod(0o666)
  subprocess.run([*docker,'cp',str(database),cid+':/data/agentlog.db'],check=True)
 run('start','agentlog')
 assert ready()=={'events':150000,'sessions':500}
 summaries=request('/api/sessions',timeout=30)
 assert len(summaries)==201 and all(s['count']==300 for s in summaries)
 page=request('/api/session?id='+summaries[0]['id'],timeout=30)
 assert page['count']==300 and len(page['events'])==201
 print('PASS: 150,000 records with a read-only root and 64 MiB /tmp; SQLite temporary files use the data volume.')

try:
 run('up','-d','--no-build')
 assert ready()=={'events':0,'sessions':0}
 cid=run('ps','-q','agentlog')
 config=json.loads(subprocess.check_output(['docker','--host=unix:///var/run/docker.sock','inspect',cid],text=True))[0]
 assert config['Config']['User']=='65532:65532'
 assert config['HostConfig']['ReadonlyRootfs']
 assert 'SQLITE_TMPDIR=/data' in config['Config']['Env']
 record={'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeLogs':[{'logRecords':[{'timeUnixNano':'1794500000000000000','body':{'stringValue':'compose persistence test'},'attributes':[{'key':'conversation.id','value':{'stringValue':'compose-test'}}]}]}]}]}
 request('/v1/logs',record)
 assert request('/api/stats')['events']==1
 try:request('/api/stats',auth='incorrect');raise AssertionError('Unauthenticated access accepted')
 except urllib.error.HTTPError as e:assert e.code==401
 run('up','-d','--no-build','--force-recreate')
 assert ready()['events']==1,'Data lost on recreation'
 request('/v1/logs',record)
 assert request('/api/stats')['events']==1,'Replay was not deduplicated'
 assert request('/api/session?id=codex:compose-test')['events'][0]['records'][0]['name']=='compose persistence test'
 print('PASS: Compose non-root/read-only startup, authenticated ingestion, recreation persistence, replay deduplication.')
 large_history(run('ps','-q','agentlog'))
finally:
 run('down','--volumes','--remove-orphans')
