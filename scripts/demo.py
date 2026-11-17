#!/usr/bin/env python3
"""Synthetic, schema-shaped OTLP fixtures. No real CLI data or claims of real execution."""
from test_support import settled_urlopen
import datetime,json,os,urllib.request
BASE=os.getenv('AGENTLOG_URL','http://127.0.0.1:4318').rstrip('/')
TOKEN=os.environ['AGENTLOG_TOKEN']
now=datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0)
def attrs(v):
 def value(x):
  if isinstance(x,bool):return {'boolValue':x}
  if isinstance(x,(dict,list)):return {'stringValue':json.dumps(x)}
  return {'stringValue':str(x)}
 return [{'key':k,'value':value(x)} for k,x in v.items()]
def send(signal,body):
 req=urllib.request.Request(BASE+'/v1/'+signal,json.dumps(body).encode(),headers={'Authorization':'Bearer '+TOKEN,'Content-Type':'application/json'})
 with settled_urlopen(req) as r:assert r.status==200
prompt='Add pagination to the sessions API and cover the boundary cases.'
def logs(source,sid,items,minutes=0,extra=None):
 resource={'attributes':attrs({'service.name':source+'-cli','host.name':'dev-workstation','demo.synthetic':'true','cwd':'/projects/agentlog'})}
 records=[]
 for i,(name,fields) in enumerate(items):
  records.append({'timeUnixNano':str(int((now-datetime.timedelta(minutes=minutes)+datetime.timedelta(seconds=i*8)).timestamp())*10**9),'attributes':attrs({'session.id':sid,'event.name':name,**(extra or {}),**fields})})
 send('logs',{'resourceLogs':[{'resource':resource,'scopeLogs':[{'logRecords':records}]}]})
if __name__=='__main__':
 logs('claude','7d4b3a91-pagination',[
  ('user_prompt',{'prompt':prompt,'model':'claude-sonnet-4-6'}),
  ('tool_result',{'tool_name':'Bash','tool_parameters':{'full_command':'rg -n "LIMIT|OFFSET" internal/app/'},'success':'true','duration_ms':42}),
  ('tool_result',{'tool_name':'Read','tool_input':{'file_path':'internal/app/app.go','offset':178,'limit':60},'success':'true','duration_ms':12}),
  ('api_request',{'model':'claude-sonnet-4-6','input_tokens':2841,'output_tokens':386,'cache_read_tokens':1200,'cost_usd':'0.0143','duration_ms':1840}),
  ('tool_result',{'tool_name':'Bash','tool_parameters':{'full_command':'go test ./internal/app -run TestPagination'},'success':'false','error':'TestPagination: expected 5 records on page 2, got 0','error_type':'ShellError','duration_ms':632}),
  ('assistant_response',{'response':'The offset was applied twice. I fixed the query and added coverage for empty pages and the 200-record boundary.','model':'claude-sonnet-4-6'})],extra={'cwd':'/projects/agentlog'})
 logs('claude','7d4b3a91-pagination',[('assistant_response',{'response':'Reviewed the query: ordering is stable when timestamps are equal.','model':'claude-sonnet-4-6'})],extra={'agent_id':'reviewer'})
 logs('codex','c82f16a0-auth',[
  ('codex.user_prompt',{'prompt':'Check that the browser cookie cannot authorize telemetry ingestion.','model':'gpt-5.4'}),
  ('codex.tool_result',{'tool_name':'exec_command','arguments':{'cmd':'go test ./internal/app -run TestLogsAndAuth -v'},'output':'=== RUN   TestLogsAndAuth\n--- PASS: TestLogsAndAuth (0.02s)\nPASS','success':'true','duration_ms':248}),
  ('codex.sse_event',{'input_token_count':4200,'output_token_count':518,'cached_token_count':2048,'model':'gpt-5.4'})],minutes=8)
 for i,(source,title) in enumerate([('codex','sqlite-retention'),('claude','review-error-handling'),('codex','document-proxy-setup')]):
  logs(source,title,[('user_prompt',{'prompt':title.replace('-',' ').capitalize()})],minutes=20+i*10,extra={'cwd':['/projects/sqlite-tools','/projects/agentlog','/projects/deployment'][i]})
 spans=[]
 def span(n,fields,i,parent=''):
  start=int((now-datetime.timedelta(minutes=12)+datetime.timedelta(seconds=i*10)).timestamp())*10**9
  spans.append({'name':n,'traceId':'a'*32,'spanId':f'{i+1:016x}','parentSpanId':parent,'startTimeUnixNano':str(start),'endTimeUnixNano':str(start+1450000000),'attributes':attrs({'gen_ai.conversation.id':'e95b2c40-search',**fields})})
 span('chat',{'gen_ai.operation.name':'chat','gen_ai.request.model':'gpt-4.1','gen_ai.input.messages':[{'role':'user','parts':[{'type':'text','content':'Find why session search returns partial event counts.'}]}],'gen_ai.output.messages':[{'role':'assistant','parts':[{'type':'text','content':'I will inspect the grouping query and move the search predicate into a session subquery.'}]}],'gen_ai.usage.input_tokens':3150,'gen_ai.usage.output_tokens':287,'github.copilot.cost':1},0)
 span('execute_tool',{'gen_ai.operation.name':'execute_tool','gen_ai.tool.name':'read_file','gen_ai.tool.call.arguments':{'path':'internal/app/app.go'},'gen_ai.tool.call.result':'SELECT session, COUNT(*) FROM records WHERE ... GROUP BY session'},1,'0000000000000001')
 send('traces',{'resourceSpans':[{'resource':{'attributes':attrs({'service.name':'github-copilot','demo.synthetic':'true','cwd':'/projects/agentlog'})},'scopeSpans':[{'spans':spans}]}]})
 print('Synthetic fixtures submitted for Claude Code, Codex and Copilot.')
