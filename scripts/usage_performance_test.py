#!/usr/bin/env python3
"""Synthetic, out-of-order delta batches verify indexed interval accounting."""
import json,os,socket,subprocess,tempfile,time,urllib.request
binary=os.getenv('AGENTLOG_BIN','./agentlog');token='synthetic-usage-performance-token'
with tempfile.TemporaryDirectory() as folder:
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}'
 proc=subprocess.Popen([binary,'--listen',f'127.0.0.1:{port}','--db',folder+'/test.db'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 try:
  for _ in range(100):
   try:urllib.request.urlopen(base,timeout=1);break
   except OSError:time.sleep(.1)
  def fetch(path,data=None):
   request=urllib.request.Request(base+path,None if data is None else json.dumps(data).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
   with urllib.request.urlopen(request,timeout=120) as response:return json.load(response)
  for count in [1000,2000,4000,10000]:
   stamp=1780000000000000000
   points=[{'startTimeUnixNano':str(stamp+i*1000000000),'timeUnixNano':str(stamp+(i+1)*1000000000),'asInt':'1','attributes':[{'key':'type','value':{'stringValue':'output'}}]} for i in reversed(range(count))]
   metrics={'resourceMetrics':[{'resource':{'attributes':[{'key':'service.name','value':{'stringValue':'claude'}},{'key':'session.id','value':{'stringValue':f'batch-{count}'}}]},'scopeMetrics':[{'metrics':[{'name':'claude_code.token.usage','unit':'tokens','sum':{'aggregationTemporality':1,'isMonotonic':True,'dataPoints':points}}]}]}]}
   started=time.monotonic();fetch('/v1/metrics',metrics);elapsed=time.monotonic()-started
   for _ in range(2):assert fetch(f'/api/session?id=claude:batch-{count}')['usage']['values']['output_tokens']==str(count)
   print(f'delta batch {count}: {elapsed:.3f}s; exact total {count}',flush=True)
 finally:proc.terminate();proc.wait(timeout=20)
print('PASS: reverse-order delta batches, exact totals and cached detail reads')
