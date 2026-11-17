#!/usr/bin/env python3
"""Measure ready overview summaries with many sessions and rich usage reports. Synthetic data only."""
from test_support import settled_urlopen
import atexit,json,os,pathlib,shutil,socket,sqlite3,statistics,subprocess,tempfile,time,urllib.request
root=pathlib.Path(tempfile.mkdtemp(prefix='agentlog-overview-perf-'));atexit.register(shutil.rmtree,root)
token='synthetic-overview-benchmark-token'
def start(binary,db):
 with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
 base=f'http://127.0.0.1:{port}';proc=subprocess.Popen([binary,'--db',str(db),'--listen',f'127.0.0.1:{port}'],env={**os.environ,'AGENTLOG_TOKEN':token},stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 for _ in range(6000):
  try:settled_urlopen(base,timeout=1);return proc,base
  except OSError:
   if proc.poll() is not None:raise RuntimeError('Server exited')
   time.sleep(.01)
 raise RuntimeError('Startup timed out')
def api(base,path):
 req=urllib.request.Request(base+path,headers={'Authorization':'Bearer '+token})
 with settled_urlopen(req,timeout=120) as r:return r.read()
if __name__=='__main__':
 import argparse
 p=argparse.ArgumentParser();p.add_argument('--binary',default=os.getenv('AGENTLOG_BIN','./agentlog'));p.add_argument('--baseline');p.add_argument('--browser',action='store_true');p.add_argument('--sessions',type=int,default=20000);args=p.parse_args();assert args.sessions>0;fixture=root/f'fixture-{args.sessions}.db'
 if not fixture.exists():
  proc,base=start(args.baseline or args.binary,fixture);api(base,'/api/skills');time.sleep(.1);proc.terminate();proc.wait(timeout=20)
  with sqlite3.connect(fixture) as c:
   def rows():
    for i in range(args.sessions):
     sid=f'codex:synthetic-{i:08d}';folder=f'/work/project-{i%20:02d}';stamp=f'2026-11-{i%28+1:02d}T12:{i%60:02d}:00Z';detail=json.dumps({'name':'codex.user_prompt','kind':'user','title':'User','blocks':[{'label':'Request','text':'Synthetic investigation '+str(i)}],'fields':[],'attributes':{},'cwd':folder})
     yield(sid,'codex','',100,stamp,stamp,100,stamp,stamp,0,'','',detail,folder,'name:'+folder.rsplit('/',1)[-1])
   c.executemany('INSERT INTO sessions(session,source,parent,count,start,end,import_count,import_start,import_end,otel_count,otel_start,otel_end,preview,project_directory,project_key) VALUES('+','.join('?'*15)+')',rows())
   usage={'sessions':1,'values':{'input_tokens':'9007199254740993','output_tokens':'500','cache_read_tokens':'300','cost_usd':'0.012345678901234567','errors':'0','duration_ms':'1000','duration_samples':'1'},'coverage':{'input_tokens':1,'output_tokens':1,'cache_read_tokens':1,'cost_usd':1,'errors':1},'basis':{'tokens':'otel/traces','cost':'otel/logs','latency':'otel/traces'},'overlap':True,'excluded':0,'legacy':0}
   usage['variants']={'otel':{k:v for k,v in usage.items() if k!='variants'},'import':{k:v for k,v in usage.items() if k!='variants'}}
   c.executemany('INSERT INTO session_usage VALUES(?,?,?)',((f'codex:synthetic-{i:08d}','',json.dumps(usage)) for i in range(args.sessions)))
   c.commit()
 expected={}
 for name,binary in ([('baseline',args.baseline)] if args.baseline else [])+[('current',args.binary)]:
  db=root/f'{name}-{args.sessions}.db'
  with sqlite3.connect(fixture) as src,sqlite3.connect(db) as dst:src.backup(dst)
  start_time=time.perf_counter();proc,base=start(binary,db);print('STARTUP',round(time.perf_counter()-start_time,3),flush=True)
  try:
   for path in ['/api/folders','/api/projects','/api/sessions?folder=/work/project-00','/api/sessions']:
    if path=='/api/projects' and name=='baseline':continue
    samples=[]
    for i in range(4):
     begin=time.perf_counter();body=api(base,path);samples.append(round((time.perf_counter()-begin)*1000,2))
    print(name,args.sessions,path,'first_ms',samples[0],'median_ms',statistics.median(samples[1:]),'bytes',len(body),flush=True)
    result=json.loads(body)
    if path=='/api/projects':
     assert result['total']==min(20,args.sessions) and result['sessions']==args.sessions
    if name=='baseline':expected[path]=result
    elif path in expected:assert result==expected[path],path
    if path=='/api/folders':
     assert result['total']==min(20,args.sessions) and sum(f['count'] for f in result['folders'])==args.sessions
     for f in result['folders']:assert f['usage']['values']['input_tokens']==str(9007199254740993*f['count'])
   if args.browser:
    from playwright.sync_api import sync_playwright
    with sync_playwright() as p:
     browser=p.chromium.launch(executable_path='/usr/bin/chromium',headless=True,args=['--no-sandbox']);page=browser.new_page(viewport={'width':1440,'height':1000});page.goto(base);page.locator('#token').fill(token);page.locator('#loginform button').click();page.wait_for_selector('.session')
     response=json.loads(api(base,'/api/sessions?folder=/work/project-00'))
     t=page.evaluate('rows=>{const start=performance.now();renderSessions(rows,1000);return performance.now()-start}',response)
     print('BROWSER render',min(200,len(response)),'sessions ms',round(t,2),flush=True);browser.close()
  finally:proc.terminate();proc.wait(timeout=20)
