#!/usr/bin/env python3
"""Reproducible synthetic API benchmark; optional --baseline compares binaries."""
from test_support import settled_urlopen
import argparse,json,os,pathlib,socket,sqlite3,statistics,subprocess,tempfile,time,urllib.parse,urllib.request
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary',default=os.getenv('AGENTLOG_BIN','./agentlog'))
parser.add_argument('--baseline')
parser.add_argument('--records',type=int,default=150000)
parser.add_argument('--sessions',type=int,default=500)
parser.add_argument('--payload-bytes',type=int,default=2048)
parser.add_argument('--japanese',action='store_true',help='Measure Japanese common/rare keyword searches as well')
parser.add_argument('--folders',action='store_true',help='Include 20 working directories and measure the folder overview')
args=parser.parse_args()
assert args.records>0 and args.sessions>0 and args.payload_bytes>=0
schema='''CREATE TABLE events(id TEXT PRIMARY KEY,session TEXT NOT NULL,parent TEXT NOT NULL,source TEXT NOT NULL,signal TEXT NOT NULL,time TEXT NOT NULL,trace TEXT NOT NULL,span TEXT NOT NULL,parent_span TEXT NOT NULL,sequence INTEGER NOT NULL,origin TEXT NOT NULL,kind TEXT NOT NULL,tool_name TEXT NOT NULL,name TEXT NOT NULL,raw BLOB NOT NULL);
CREATE TABLE event_keys(event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,namespace TEXT NOT NULL,key TEXT NOT NULL,PRIMARY KEY(event_id,namespace,key));'''
def measure(binary,fixture,folder,name):
 database=folder/(name+'.db')
 with sqlite3.connect(fixture) as source,sqlite3.connect(database) as destination:source.backup(destination)
 with socket.socket() as s:s.bind(('127.0.0.1',0));port=s.getsockname()[1]
 token='synthetic-performance-token';base=f'http://127.0.0.1:{port}';started=time.monotonic()
 proc=subprocess.Popen([str(pathlib.Path(binary).resolve()),'--listen',f'127.0.0.1:{port}','--db',str(database)],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 try:
  while time.monotonic()-started<600:
   if proc.poll() is not None:raise RuntimeError('Collector exited during preparation')
   try:
    with settled_urlopen(base,timeout=1) as response:response.read()
    break
   except OSError:time.sleep(.1)
  else:raise TimeoutError('Collector preparation exceeded 10 minutes')
  print(name,'initial_upgrade_seconds',round(time.monotonic()-started,3),flush=True)
  signatures={}
  endpoints=[('list','/api/sessions'),('detail','/api/session?id=codex:fixture-0000')]
  if args.folders and name=='current':endpoints.append(('folders','/api/folders'))
  if args.japanese:endpoints += [('search_common','/api/sessions?q='+urllib.parse.quote('認証')),('search_rare','/api/sessions?q='+urllib.parse.quote('一意診断'))]
  for endpoint,path in endpoints:
   samples=[]
   for _ in range(4):
    request=urllib.request.Request(base+path,headers={'Authorization':'Bearer '+token});start=time.monotonic()
    with settled_urlopen(request,timeout=120) as response:body=response.read()
    samples.append(time.monotonic()-start)
   result=json.loads(body)
   if endpoint=='search_common':assert len(result)==min(201,args.sessions,args.records)
   if endpoint=='search_rare':assert len(result)==1 and result[0]['id']=='codex:fixture-0000'
   if endpoint=='folders':
    assert result['total']==min(20,args.sessions,args.records)
    assert sum(f['count'] for f in result['folders'])==min(args.sessions,args.records)
   else:signatures[endpoint]=[(s['id'],s['count'],s['start'],s['end']) for s in result] if endpoint!='detail' else (result['count'],[(e['id'],e['time'],e['origins']) for e in result['events']])
   print(name,endpoint,'first_ms',round(samples[0]*1000,2),'median_ms',round(statistics.median(samples[1:])*1000,2),'response_bytes',len(body),flush=True)
  return signatures
 finally:proc.terminate();proc.wait(timeout=30)
with tempfile.TemporaryDirectory(prefix='agentlog-perf-') as temporary:
 folder=pathlib.Path(temporary);fixture=folder/'fixture.db'
 with sqlite3.connect(fixture) as database:
  database.executescript(schema)
  def rows():
   for session in range(min(args.sessions,args.records)):
    sid=f'codex:fixture-{session:04d}'
    prompt=('日本語の検索と認証を確認。'+('一意診断。' if session==0 else '') if args.japanese else 'Synthetic ')+('x'*args.payload_bytes)
    attributes=[{'key':'service.name','value':{'stringValue':'codex'}}]
    if args.folders:attributes.append({'key':'cwd','value':{'stringValue':f'/work/project-{session%20:02d}'}})
    raw=json.dumps({'resource':{'attributes':attributes},'record':{'eventName':'gen_ai.user.message','attributes':[{'key':'prompt','value':{'stringValue':prompt}}]}},ensure_ascii=False).encode()
    for number in range(session,args.records,args.sessions):yield(f'import:{number:064x}',sid,'','codex','logs','2026-01-01T00:00:00.000000000Z','','','',number,'import','user','','gen_ai.user.message',raw)
  database.executemany('INSERT INTO events VALUES('+','.join('?'*15)+')',rows())
 previous=measure(args.baseline,fixture,folder,'baseline') if args.baseline else None
 current=measure(args.binary,fixture,folder,'current')
 if previous is not None:assert current==previous,'Session counts, ordering, or event identities changed'
 print('PASS: synthetic performance fixture; counts/order/identities match baseline' if previous is not None else 'PASS: synthetic performance fixture')
