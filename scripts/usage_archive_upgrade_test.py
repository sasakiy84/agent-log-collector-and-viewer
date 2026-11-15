#!/usr/bin/env python3
"""Compare a published pre-usage importer with current archive suppression."""
from test_support import settled_urlopen
import hashlib,json,os,pathlib,socket,sqlite3,subprocess,tempfile,time,urllib.request
binary=os.getenv('AGENTLOG_BIN','./agentlog');baseline=os.environ['AGENTLOG_BASELINE_BIN'];token='synthetic-archive-upgrade-token'
with tempfile.TemporaryDirectory() as folder:
 for source in ['codex','claude']:
  database=str(pathlib.Path(folder)/(source+'.db'));path=pathlib.Path(folder)/(source+'.jsonl')
  if source=='codex':
   rows=[{'type':'session_meta','payload':{'id':'archived','cwd':'/work/archive','timestamp':'2025-01-01T00:00:00Z'}},{'timestamp':'2025-01-01T00:00:01Z','type':'event_msg','payload':{'type':'token_count','info':{'last_token_usage':{'input_tokens':10,'output_tokens':2,'cached_input_tokens':5},'total_token_usage':{'input_tokens':10,'output_tokens':2,'cached_input_tokens':5,'total_tokens':12}}}}]
  else:rows=[{'type':'assistant','uuid':'message','sessionId':'archived','cwd':'/work/archive','timestamp':'2025-01-01T00:00:01Z','message':{'id':'response','content':[{'type':'text','text':'Synthetic reply'}],'usage':{'input_tokens':10,'output_tokens':2,'cache_read_input_tokens':5,'cache_creation_input_tokens':2}}}]
  def save():path.write_text(''.join(json.dumps(row)+'\n' for row in rows))
  def start(executable):
   with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
   base=f'http://127.0.0.1:{port}';proc=subprocess.Popen([executable,'--listen',f'127.0.0.1:{port}','--db',database],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
   for _ in range(200):
    try:settled_urlopen(base,timeout=1);return proc,base
    except OSError:time.sleep(.1)
   raise AssertionError('server did not start')
  def upload(executable,base):subprocess.run([executable,'import','--source',source,'--file',str(path),'--endpoint',base],env={**os.environ,'AGENTLOG_TOKEN':token},check=True,capture_output=True)
  save();old,base=start(baseline)
  try:upload(baseline,base)
  finally:old.terminate();old.wait(timeout=20)
  # The same durable tombstones produced by confirmed archive deletion.
  with sqlite3.connect(database) as db:
   db.execute('PRAGMA foreign_keys=ON');records=db.execute('SELECT id,raw FROM events').fetchall();assert records
   for id,raw in records:db.execute('INSERT INTO archived_records(id_hash,raw_hash) VALUES(?,?)',(hashlib.sha256(id.encode()).digest(),hashlib.sha256(raw).digest()))
   db.execute('DELETE FROM events')
  current,base=start(binary)
  try:
   upload(binary,base)
   request=urllib.request.Request(base+'/api/stats',headers={'Authorization':'Bearer '+token})
   with settled_urlopen(request) as response:assert json.load(response)['events']==0,source
   if source=='codex':
    rows[1]['payload']['info']['last_token_usage']['output_tokens']=3;rows[1]['payload']['info']['total_token_usage']['output_tokens']=3;rows[1]['payload']['info']['total_token_usage']['total_tokens']=13
   else:rows[0]['message']['usage']['output_tokens']=3
   save();upload(binary,base)
   with settled_urlopen(request) as response:assert json.load(response)['events']==1,source
   def api(path,data=None):
    req=urllib.request.Request(base+path,None if data is None else json.dumps(data).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
    with settled_urlopen(req) as response:return json.load(response)
   def status(job,want):
    deadline=time.monotonic()+20
    while time.monotonic()<deadline:
     state=api('/api/archives/'+job['id'])
     if state['status']==want:return
     assert state['status']!='failed',state
     time.sleep(.05)
    raise AssertionError((want,state))
   job=api('/api/archives',{'all':True,'cutoff':'2025-02-01T00:00:00Z'});status(job,'ready')
   download=urllib.request.Request(base+'/api/archives/'+job['id']+'/download',headers={'Authorization':'Bearer '+token})
   with settled_urlopen(download) as response:assert response.read().startswith(b'PK')
   api('/api/archives/'+job['id']+'/delete',{'confirmation':'DOWNLOADED'});status(job,'deleted');time.sleep(.1)
   # Both current and older clients replaying unchanged source remain suppressed.
   for client in [baseline,binary]:upload(client,base);assert api('/api/stats')['events']==0,source
   # A real change confined to newly supported fields must remain importable.
   if source=='codex':rows[1]['payload']['info']['total_token_usage']['input_tokens']=11;rows[1]['payload']['info']['total_token_usage']['total_tokens']=14
   else:rows[0]['message']['id']='response-revised'
   save();upload(binary,base);assert api('/api/stats')['events']==(1 if source=='codex' else 2),source
  finally:current.terminate();current.wait(timeout=20)
  print(source+': old/new archive replay stays deleted; last/output and counter/response-only edits remain importable',flush=True)
print('PASS: archive tombstones survive Codex/Claude usage-normalization upgrades')
