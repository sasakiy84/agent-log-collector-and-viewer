#!/usr/bin/env python3
"""Published v0.8.0 data keeps originals, tool links and recoverable skill evidence."""
import hashlib,json,os,pathlib,socket,sqlite3,subprocess,tempfile,time,urllib.request
binary=os.getenv('AGENTLOG_BIN','./agentlog');baseline=os.environ['AGENTLOG_BASELINE_BIN'];token='synthetic-event-upgrade-token'
with tempfile.TemporaryDirectory() as folder:
 database=str(pathlib.Path(folder)/'test.db')
 def start(executable):
  with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
  base=f'http://127.0.0.1:{port}';proc=subprocess.Popen([executable,'--listen',f'127.0.0.1:{port}','--db',database],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  for _ in range(200):
   try:urllib.request.urlopen(base,timeout=1);return proc,base
   except OSError:time.sleep(.05)
  raise AssertionError('server did not start')
 def api(path,data=None):
  req=urllib.request.Request(base+path,None if data is None else json.dumps(data).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
  with urllib.request.urlopen(req) as response:return json.load(response)
 def fingerprint():
  digest=hashlib.sha256()
  with sqlite3.connect('file:'+database+'?mode=ro',uri=True) as db:
   for id,raw in db.execute('SELECT id,raw FROM events ORDER BY id'):digest.update(id.encode());digest.update(raw)
  return digest.hexdigest()
 def record(i,name,values):return {'eventName':name,'timeUnixNano':str(1780000000000000000+i*1000000000),'attributes':[{'key':k,'value':{'stringValue':v}} for k,v in {'session.id':'upgrade',**values}.items()]}
 rows=[record(0,'gen_ai.tool.start',{'tool_name':'exec_command','call_id':'read','arguments':'{"cmd":"cat /work/skills/review-core/SKILL.md"}'}),record(1,'gen_ai.tool.result',{'tool_name':'exec_command','call_id':'read','output':'Synthetic skill instructions'})]+[record(i+2,'codex.skill_invocation',{'skill.name':'review-core','turn.id':'turn','skill.invocation_type':'explicit'}) for i in range(2)]
 old,base=start(baseline)
 try:api('/v1/logs',{'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeLogs':[{'logRecords':rows}]}]});before=fingerprint()
 finally:old.terminate();old.wait(timeout=20)
 current,base=start(binary)
 try:
  deadline=time.monotonic()+10
  while time.monotonic()<deadline:
   catalog=api('/api/skills')
   if catalog['ready']:break
   time.sleep(.05)
  assert catalog['ready'] and catalog['total']==1,catalog
  skill=catalog['skills'][0];assert skill['uses']==1 and skill['references']==1 and skill['sessions']==1,skill
  page=api('/api/session?id=codex:upgrade');assert page['count']==4 and len(page['skills'])==1,page
  first=next(e for e in page['events'] if e['detail']['name']=='gen_ai.tool.start')
  event=api('/api/event?session=codex:upgrade&id='+first['id']);assert event['tool_pair']['state']=='paired',event
  assert event['tool_pair']['counterpart']['detail']['blocks'][0]['text']=='Synthetic skill instructions'
  assert fingerprint()==before,'Original event identities/payloads changed during upgrade'
  print('PASS: published v0.8.0 upgrades to tool links and reported/reference skill totals; event identities and original payloads unchanged')
 finally:current.terminate();current.wait(timeout=20)
