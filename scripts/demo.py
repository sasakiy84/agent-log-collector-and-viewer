#!/usr/bin/env python3
"""Send synthetic OTLP fixtures over HTTP; never reads real CLI sessions."""
import datetime, json, os, urllib.request
BASE=os.getenv('AGENTLOG_URL','http://127.0.0.1:4318').rstrip('/')
TOKEN=os.environ['AGENTLOG_TOKEN']
now=datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0)
def attrs(**v): return [{'key':k,'value':{'stringValue':str(x)}} for k,x in v.items()]
def send(signal,payload):
 r=urllib.request.Request(BASE+'/v1/'+signal,json.dumps(payload).encode(),headers={'Content-Type':'application/json','Authorization':'Bearer '+TOKEN})
 with urllib.request.urlopen(r) as response: assert response.status==200
sessions=[('codex','refactor-auth-middleware',0),('claude','review-api-boundaries',12),('copilot','implement-session-search',25),('codex','add-sqlite-migrations',40),('claude','document-otel-setup',55),('copilot','fix-pagination-state',70)]
for source,sid,minutes in sessions:
 start=now-datetime.timedelta(minutes=minutes+4)
 messages=['Session started · workspace /projects/observatory','User request: Review the authentication flow and add focused tests.','Planning · inspecting routes and existing test coverage','Tool call · rg --files internal/','Tool result · found 12 relevant files','Applying changes to authentication middleware','Verification · go test ./... passed','Session completed · changes ready for review']
 records=[]
 for i,msg in enumerate(messages):
  records.append({'timeUnixNano':str(int((start+datetime.timedelta(seconds=i*24)).timestamp())*10**9),'severityText':'INFO','attributes':attrs(**{'session.id':sid}),'body':{'stringValue':msg}})
 send('logs',{'resourceLogs':[{'resource':{'attributes':attrs(**{'service.name':source+'-cli','host.name':'demo-workstation','demo.synthetic':'true'})},'scopeLogs':[{'scope':{'name':source+'.telemetry'},'logRecords':records}]}]})
 if source=='claude':
  for agent,parent in [('reviewer',''),('tester','reviewer')]:
   send('logs',{'resourceLogs':[{'resource':{'attributes':attrs(**{'service.name':'claude-code','demo.synthetic':'true'})},'scopeLogs':[{'logRecords':[{'timeUnixNano':str(int((start+datetime.timedelta(seconds=90)).timestamp())*10**9),'attributes':attrs(**{'session.id':sid,'agent_id':agent,'parent_agent_id':parent}),'body':{'stringValue':'Subagent '+agent+' · focused review completed'}}]}]}]})
trace='a'*32
send('traces',{'resourceSpans':[{'resource':{'attributes':attrs(**{'service.name':'github-copilot','demo.synthetic':'true'})},'scopeSpans':[{'spans':[{'traceId':trace,'spanId':'b'*16,'name':'invoke_agent explore','parentSpanId':'c'*16,'startTimeUnixNano':str(int(now.timestamp())*10**9),'endTimeUnixNano':str(int(now.timestamp()+2)*10**9)},{'traceId':trace,'spanId':'c'*16,'name':'invoke_agent copilot','startTimeUnixNano':str(int(now.timestamp()-2)*10**9),'endTimeUnixNano':str(int(now.timestamp()+3)*10**9),'attributes':attrs(**{'gen_ai.conversation.id':'implement-session-search'})}]}]}]})
send('metrics',{'resourceMetrics':[{'resource':{'attributes':attrs(**{'service.name':'codex-cli','demo.synthetic':'true'})},'scopeMetrics':[{'metrics':[{'name':'agent.token.usage','unit':'{token}','sum':{'aggregationTemporality':2,'isMonotonic':True,'dataPoints':[{'timeUnixNano':str(int(now.timestamp())*10**9),'asInt':'14280','attributes':attrs(**{'conversation.id':'refactor-auth-middleware'})}]}}]}]}]})
print('Synthetic demo: 10 sessions, 55 events sent over OTLP/HTTP.')
