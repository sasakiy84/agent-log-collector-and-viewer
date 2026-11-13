'use strict';
// Decode OTLP AnyValue recursively. Keep absent values absent (including zero usage).
function anyValue(v) {
  if (!v || typeof v !== 'object') return v;
  if ('arrayValue' in v) return (v.arrayValue.values || []).map(anyValue);
  if ('kvlistValue' in v) return attributes(v.kvlistValue.values);
  for (const k of ['stringValue','intValue','doubleValue','boolValue','bytesValue']) if (k in v) return v[k];
  return v;
}
function attributes(items = []) { return Object.fromEntries(items.map(x => [x.key, anyValue(x.value)])); }
function decoded(v) { if (typeof v === 'string') { try { return JSON.parse(v); } catch {} } return v; }
function text(v) { return v == null ? '' : typeof v === 'string' ? v : JSON.stringify(v, null, 2); }
function parseEvent(e) {
  const raw=e.raw || {}, r=raw.record || {};
  const a={...attributes(raw.resource?.attributes), ...attributes(raw.scope?.attributes), ...attributes(r.attributes)};
  const get=(...keys) => { for (const k of keys) if (a[k] !== undefined && a[k] !== '') return a[k]; };
  const name=text(get('event.name') || r.eventName || r.name || e.name);
  const p={name, kind:'event', title:name || 'Event', blocks:[], fields:[], error:false, model:get('gen_ai.response.model','gen_ai.request.model','model'), host:get('host.name'), cwd:get('cwd','working_directory','workspace.path'), attributes:a};
  const field=(label,value) => { if(value !== undefined && value !== '') p.fields.push([label,text(value)]); };
  const block=(label,value,code=false) => { if(value !== undefined && value !== '') p.blocks.push({label,text:text(decoded(value)),code}); };
  const messages=(label,value) => {
    const v=decoded(value);
    if (!Array.isArray(v)) { block(label,v); return; }
    for(const m of v) {
      const parts=m.parts || m.content;
      const content=Array.isArray(parts)?parts.map(x=>x.content ?? x.text ?? (x.type==='tool_call'?text(x):'')).filter(Boolean).join('\n'):parts;
      block(m.role || label,content);
    }
  };
  if (/user_prompt|interaction$/.test(name)) {
    p.kind='user';p.title='User';block('Request',get('prompt_text','prompt','user_prompt'));
    if(!p.blocks.length) block('Request','Content not exported');
  } else if (/assistant_response/.test(name)) {
    p.kind='assistant';p.title='Assistant';block('Response',get('response','response_text','assistant_response','text'));
    if(!p.blocks.length) block('Response','Content not exported');
  } else if (/tool/.test(name) || get('gen_ai.operation.name')==='execute_tool') {
    p.kind='tool';p.title=text(get('gen_ai.tool.name','tool_name','tool_name_safe') || name);
    const input=decoded(get('gen_ai.tool.call.arguments','tool_input','arguments'));
    const parameters=decoded(get('tool_parameters'));
    block('Command',get('full_command') || parameters?.full_command || parameters?.command || input?.command || input?.cmd,true);
    if (input && (input.file_path || input.path)) { block('File', input.file_path || input.path, true); field('Offset', input.offset); field('Limit', input.limit); }
    else if(input !== undefined && !input?.command && !input?.cmd) block('Input',input,true);
    else if(parameters && !p.blocks.length) block('Input',parameters,true);
    block('Output',get('gen_ai.tool.call.result','tool_result','output','result','tool.output'),true);
    if (get('file_path') && !input?.file_path && !input?.path) block('File',get('file_path'),true);field('Truncated',get('output_truncated')); field('Decision',get('decision'));field('Exit code',get('exit_code'));
  } else if (/api_request|llm_request|response|sse_event|chat/.test(name) || get('gen_ai.operation.name')==='chat') {
    p.kind='model';p.title='Model request';
  } else if (get('gen_ai.operation.name')==='invoke_agent') {
    p.kind='agent';p.title=text(get('gen_ai.agent.name') || 'Agent');
  }
  messages('Input',get('gen_ai.input.messages'));messages('Output',get('gen_ai.output.messages'));
  block('Response',get('response.model_output'));
  if(e.signal==='metrics') {
    p.kind='metric';p.title=text(r.name || name);
    field('Value',r.asInt ?? r.asDouble);field('Unit',r.unit);field('Count',r.count);
    field('Sum',r.sum);field('Temporality',({1:'Delta',2:'Cumulative',AGGREGATION_TEMPORALITY_DELTA:'Delta',AGGREGATION_TEMPORALITY_CUMULATIVE:'Cumulative'})[r.aggregationTemporality]);
    if(r.bucketCounts) block('Buckets',{bounds:r.explicitBounds,counts:r.bucketCounts},true);
  }
  if(!p.blocks.length && p.kind==='event') { const body=anyValue(r.body); if(body) { if(text(body)===name) p.title='Log'; block('Message',body); } }
  field('Model',p.model);
  for(const [label,keys] of [
    ['Input tokens',['gen_ai.usage.input_tokens','input_tokens','input_token_count']],
    ['Output tokens',['gen_ai.usage.output_tokens','output_tokens','output_token_count']],
    ['Cache read',['gen_ai.usage.cache_read.input_tokens','cache_read_tokens','cached_token_count']],
    ['Cache write',['gen_ai.usage.cache_write.input_tokens','cache_creation_tokens','cache_write_token_count']],
    ['Reasoning',['gen_ai.usage.reasoning.output_tokens','reasoning_token_count']],
    ['Cost (USD)',['cost_usd']],['Billing multiplier',['github.copilot.cost']],['Nano AIU',['github.copilot.nano_aiu']]
  ]) field(label,get(...keys));
  let ms=get('duration_ms','interaction.duration_ms','tool_duration_ms');
  if(ms===undefined && r.startTimeUnixNano && r.endTimeUnixNano) { try {ms=Number(BigInt(r.endTimeUnixNano)-BigInt(r.startTimeUnixNano))/1e6;}catch{} }
  if(ms!==undefined) field('Duration',Number(ms)>=1000?`${(Number(ms)/1000).toFixed(2)} s`:`${Number(ms).toFixed(0)} ms`);
  const success=get('success'); if(success!==undefined) field('Result',String(success)==='true'?'Succeeded':'Failed');
  field('Finish',get('stop_reason','gen_ai.response.finish_reasons'));field('Status',get('status_code','http.response.status_code'));
  const failure=get('error','error.message','error.type','error_type') || r.status?.message;
  p.error=!!failure || get('success')===false || get('success')==='false' || r.status?.code===2 || r.status?.code==='STATUS_CODE_ERROR' || /ERROR|FATAL/.test(r.severityText || '');
  if(p.error) block('Error',failure || 'Request failed');
  if(p.kind==='tool' && !p.blocks.length) block('Details','Tool content not exported');
  // Span events can carry output and exceptions independently of span attributes.
  for(const event of r.events || []) {
    const ea=attributes(event.attributes);
    block(event.name,ea['tool.output'] ?? ea['exception.message'] ?? ea.output ?? ea.content);
  }
  return p;
}
globalThis.AgentlogParse={parseEvent,anyValue,attributes};
