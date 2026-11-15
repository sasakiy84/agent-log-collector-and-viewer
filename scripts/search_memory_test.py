#!/usr/bin/env python3
"""Check peak RSS with a multi-MiB Japanese log (Linux /proc; synthetic data)."""
from test_support import settled_urlopen
import json,os,pathlib,socket,subprocess,tempfile,time,urllib.request,urllib.parse
binary=os.environ.get('AGENTLOG_BIN',str(pathlib.Path(__file__).resolve().parents[1]/'agentlog'))
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}';token='synthetic-search-memory-token'
 server=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:settled_urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  data={'resourceLogs':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'codex'}}]},'scopeLogs':[{'logRecords':[{'eventName':'user_prompt','attributes':[{'key':'session.id','value':{'stringValue':'memory-fixture'}},{'key':'prompt','value':{'stringValue':'東京'*500000+'認証情報の最終診断'}}]}]}]}]}
  request=urllib.request.Request(base+'/v1/logs',json.dumps(data,ensure_ascii=False).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
  with settled_urlopen(request,timeout=90) as response:assert response.status==200
  for keyword in ['認証','最終診断']:
   request=urllib.request.Request(base+'/api/sessions?q='+urllib.parse.quote(keyword),headers={'Authorization':'Bearer '+token})
   with settled_urlopen(request) as response:assert len(json.load(response))==1
  status=pathlib.Path(f'/proc/{server.pid}/status').read_text();peak=int(next(line.split()[1] for line in status.splitlines() if line.startswith('VmHWM:')))
  assert peak<512*1024,f'Peak RSS exceeded 512 MiB: {peak/1024:.1f} MiB'
  print(f'PASS: 3 MiB unpunctuated Japanese indexed through the final word; peak RSS {peak/1024:.1f} MiB (default runtime settings)')
 finally:server.terminate();server.wait(timeout=20)
