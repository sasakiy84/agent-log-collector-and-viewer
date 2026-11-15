#!/usr/bin/env python3
"""Test a locally built Compose image using an isolated, disposable project/volume."""
import argparse,hashlib,json,os,pathlib,secrets,shutil,sqlite3,subprocess,tempfile,time,urllib.error,urllib.request,urllib.parse,zipfile
parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--skip-scale',action='store_true',help='Run startup, persistence and lazy-inspector checks only');options=parser.parse_args()
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
 deadline=time.monotonic()+180
 while time.monotonic()<deadline:
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
  # Seed a fresh raw-history database. Derived accounting triggers use
  # collector functions, so offline fixture creation starts with storage DDL.
  with sqlite3.connect(database) as db:
   storage=[db.execute('SELECT sql FROM sqlite_master WHERE type=? AND name=?',('table',name)).fetchone()[0] for name in ['events','event_keys']]
  database.unlink()
  for suffix in ['-wal','-shm']:pathlib.Path(str(database)+suffix).unlink(missing_ok=True)
  with sqlite3.connect(database) as db:
   db.execute('PRAGMA foreign_keys=ON')
   for schema in storage:db.execute(schema)
   def rows():
    for session in range(500):
     sid='codex:scale-'+f'{session:060d}'
     raw=json.dumps({'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'record':{'eventName':'gen_ai.user.message','attributes':[{'key':'session.id','value':{'stringValue':sid}},{'key':'agentlog.origin','value':{'stringValue':'import'}},{'key':'prompt','value':{'stringValue':'Synthetic scale fixture'}}]}}).encode()
     for sequence in range(300):
      yield ('import:'+f'{session*300+sequence:064x}',sid,'','codex','logs','2025-01-01T00:00:00.000000000Z','gen_ai.user.message','','','',raw,sequence,'import','user','')
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
 # Exercise the same archive worker under the production read-only image.
 archive=request('/api/archives',{'all':True,'cutoff':'2025-02-01T00:00:00Z'},timeout=30)
 def wait(status):
  deadline=time.monotonic()+180
  while time.monotonic()<deadline:
   job=request('/api/archives/'+archive['id'])
   if job['status']==status:return job
   assert job['status']!='failed',job
   time.sleep(.2)
  raise RuntimeError('Archive timed out: '+str(job))
 job=wait('ready');assert request('/api/stats')['events']==150000
 with tempfile.TemporaryDirectory() as tmp:
  output=pathlib.Path(tmp)/'archive.zip'
  req=urllib.request.Request(base()+'/api/archives/'+archive['id']+'/download',headers={'Authorization':'Bearer '+token})
  with urllib.request.urlopen(req,timeout=60) as response,output.open('wb') as destination:shutil.copyfileobj(response,destination,128*1024)
  digest=hashlib.sha256()
  with output.open('rb') as data:
   for chunk in iter(lambda:data.read(128*1024),b''):digest.update(chunk)
  assert digest.hexdigest()==job['sha256']
  with zipfile.ZipFile(output) as zipped:
   assert zipped.testzip() is None
   assert len([name for name in zipped.namelist() if name.startswith('sessions/')])==500
  request('/api/archives/'+archive['id']+'/delete',{'confirmation':'DOWNLOADED'})
  done=wait('deleted');assert done['deleted_records']==150000 and not done['compaction_error'],done
 assert request('/api/stats')=={'events':0,'sessions':0}
 print('PASS: ZIP streaming, SHA/CRC, explicit deletion, reclamation for 150,000 synthetic records in Docker.')


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
 card=request('/api/session?id=codex:compose-test')['events'][0]
 assert not card['records'][0].get('raw'),'Page eagerly returned original JSON'
 original=request('/api/event?session=codex:compose-test&id='+urllib.parse.quote(card['id'],safe=''))
 assert original['records'][0]['name']=='compose persistence test'
 print('PASS: Compose non-root/read-only startup, authenticated ingestion, recreation persistence, replay deduplication.')
 # The originally displayed native card must remain inspectable after merging.
 native={'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeLogs':[{'logRecords':[{'eventName':'gen_ai.user.message','attributes':[{'key':'session.id','value':{'stringValue':'late-merge'}},{'key':'event.id','value':{'stringValue':'shared'}},{'key':'prompt','value':{'stringValue':'native message'}}]}]}]}]}
 request('/v1/logs',native)
 old_id=request('/api/session?id=codex:late-merge')['events'][0]['id']
 imported=json.loads(json.dumps(native));at=imported['resourceLogs'][0]['scopeLogs'][0]['logRecords'][0]['attributes'];at.append({'key':'agentlog.origin','value':{'stringValue':'import'}})
 request('/v1/logs',imported)
 merged=request('/api/event?session=codex:late-merge&id='+urllib.parse.quote(old_id,safe=''))
 assert len(merged['records'])==2 and merged['id']!=old_id
 print('PASS: lazy original JSON and old-card resolution after a late import/OTEL merge.')
 # A bounded analyzer must handle multi-MiB Japanese without a giant lattice.
 japanese={'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeLogs':[{'logRecords':[{'eventName':'user_prompt','attributes':[{'key':'session.id','value':{'stringValue':'japanese-large'}},{'key':'prompt','value':{'stringValue':'東京'*500000+'認証エラーとパスワードの確認'}}]}]}]}]}
 request('/v1/logs',japanese,timeout=90)
 for term in ['認証','エラー','ﾊﾟｽﾜｰﾄﾞ']:
  hits=request('/api/sessions?q='+urllib.parse.quote(term))
  assert len(hits)==1 and hits[0]['id']=='codex:japanese-large',term
 print('PASS: built-in Japanese FTS, two-character words and width normalization; 3 MiB unpunctuated Japanese in the read-only Docker image.')

 if not options.skip_scale:large_history(run('ps','-q','agentlog'))
finally:
 run('down','--volumes','--remove-orphans')
