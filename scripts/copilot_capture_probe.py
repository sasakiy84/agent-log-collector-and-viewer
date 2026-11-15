#!/usr/bin/env python3
"""Run an installed Copilot CLI against a synthetic local OpenAI API, without GitHub credentials."""
import http.server,threading,json,subprocess,os,pathlib,time,argparse,shutil
parser=argparse.ArgumentParser();parser.add_argument('--output',required=True);args=parser.parse_args()
root=pathlib.Path(args.output).resolve();root.mkdir(parents=True,exist_ok=True);
cli=os.getenv('COPILOT_BIN') or shutil.which('copilot')
if not cli:raise SystemExit('Set COPILOT_BIN to an installed Copilot CLI executable')
work=root/'work';work.mkdir(exist_ok=True);(work/'fixture.txt').write_text('Synthetic file contents for Agentlog display test.\n')
skills=root/'skills'/'review';skills.mkdir(parents=True,exist_ok=True);(skills/'SKILL.md').write_text('---\nname: review\ndescription: Synthetic review fixture\n---\nRead fixture.txt.\n')
requests=[]
class Provider(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  body=json.dumps({'object':'list','data':[{'id':'gpt-4.1','object':'model','owned_by':'local-fixture'}]}).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(body)
 def do_POST(self):
  r=json.loads(self.rfile.read(int(self.headers.get('Content-Length',0))));tools=[t['function']['name'] for t in r.get('tools',[])];requests.append({'path':self.path,'tools':tools,'roles':[m['role'] for m in r.get('messages',[])]});(root/'request-shapes.json').write_text(json.dumps(requests,indent=2))
  used={c.get('function',{}).get('name') for m in r.get('messages',[]) for c in m.get('tool_calls',[])}
  name='skill' if 'skill' in tools and 'skill' not in used else 'view'
  use=name in tools and name not in used
  message={'role':'assistant','content':None if use else 'Fixture read complete.'}
  if use:message['tool_calls']=[{'id':'call-agentlog-view','type':'function','function':{'name':name,'arguments':json.dumps({'skill':'review'} if name=='skill' else {'path':str(work/'fixture.txt')})}}]
  reason='tool_calls' if use else 'stop';usage={'prompt_tokens':100,'completion_tokens':10,'total_tokens':110,'prompt_tokens_details':{'cached_tokens':20}}
  if r.get('stream'):
   self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
   delta={**message}
   if use:delta['tool_calls']=[{'index':0,**message['tool_calls'][0]}]
   for item in [{'id':'chatcmpl-agentlog-'+str(len(requests)),'object':'chat.completion.chunk','created':int(time.time()),'model':'gpt-4.1','choices':[{'index':0,'delta':delta,'finish_reason':None}]},{'id':'chatcmpl-agentlog-'+str(len(requests)),'object':'chat.completion.chunk','created':int(time.time()),'model':'gpt-4.1','choices':[{'index':0,'delta':{},'finish_reason':reason}],'usage':usage}]:self.wfile.write(('data: '+json.dumps(item)+'\n\n').encode())
   self.wfile.write(b'data: [DONE]\n\n')
  else:
   body=json.dumps({'id':'chatcmpl-agentlog-'+str(len(requests)),'object':'chat.completion','created':int(time.time()),'model':'gpt-4.1','choices':[{'index':0,'message':message,'finish_reason':reason}],'usage':usage}).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(body)
server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Provider);threading.Thread(target=server.serve_forever,daemon=True).start()
env={'PATH':os.environ['PATH'],'LANG':'C.UTF-8','XDG_CACHE_HOME':os.getenv('XDG_CACHE_HOME',str(root/'cache')),'COPILOT_HOME':str(root/'state'),'COPILOT_PROVIDER_BASE_URL':f'http://127.0.0.1:{server.server_port}/v1','COPILOT_PROVIDER_TYPE':'openai','COPILOT_PROVIDER_API_KEY':'synthetic-local-only','COPILOT_MODEL':'gpt-4.1','COPILOT_OTEL_FILE_EXPORTER_PATH':str(root/'otel.jsonl'),'COPILOT_OTEL_ENABLED':'true','COPILOT_SKILLS_DIRS':str(root/'skills'),'OTEL_INSTRUMENTATION_GENAI_CAPTURE_CONTENT':'SPAN_AND_EVENT','OTEL_RESOURCE_ATTRIBUTES':'service.namespace=fixture-project'}
cmd=[cli,'--disable-client-telemetry','--disable-builtin-mcps','--no-auto-update','--no-custom-instructions','--no-ask-user','--allow-tool=view','--allow-tool=skill','--available-tools','view','skill','--trust-folder','-p','Invoke the review skill, use view to read fixture.txt, then say Fixture read complete.']
try:
 result=subprocess.run(cmd,cwd=work,env=env,capture_output=True,text=True,timeout=45)
 (root/'stdout.txt').write_text(result.stdout);(root/'stderr.txt').write_text(result.stderr)
 print('exit',result.returncode,'requests',len(requests),'stdout',result.stdout[-1000:],'stderr',result.stderr[-1500:])
except subprocess.TimeoutExpired as e:raise SystemExit('Copilot probe timed out; check the installed CLI version') from e
finally:server.shutdown()
print('files',[(str(p.relative_to(root)),p.stat().st_size) for p in root.rglob('*') if p.is_file()][:25])

if result.returncode!=0:raise SystemExit(result.returncode)
spans=[json.loads(line) for line in (root/'otel.jsonl').read_text().splitlines() if json.loads(line).get('type')=='span']
assert any(x['name'].startswith('execute_tool view') for x in spans)
assert any(e['name']=='github.copilot.skill.invoked' for x in spans for e in x.get('events',[]))
print('PASS: actual Copilot tool/skill execution and file-exported OTEL captured; model responses were synthetic and local only')
