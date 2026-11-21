CREATE TEMP TABLE discarded_codex_spans(id TEXT PRIMARY KEY,session TEXT NOT NULL,trace TEXT NOT NULL);

INSERT INTO discarded_codex_spans
SELECT e.id,e.session,e.trace FROM records e NOT INDEXED
WHERE e.source='codex' AND e.origin='otel' AND e.signal='traces'
 AND e.name IN ('code_mode.grpc.callback','code_mode.grpc.subscribe_to_tool_calls',
  'code_mode.grpc.open_binding','code_mode.grpc.open_session')
 AND NOT EXISTS (SELECT 1 FROM archive_locks WHERE session=e.session)
 AND json_valid(e.raw)
 AND json_extract(e.raw,'$.record.name')=e.name
 AND CAST(json_extract(e.raw,'$.record.kind') AS TEXT) IN ('SPAN_KIND_INTERNAL','1')
 AND COALESCE(CAST(json_extract(e.raw,'$.record.status.code') AS TEXT),'') IN ('','0','1','STATUS_CODE_UNSET','STATUS_CODE_OK')
 AND COALESCE(json_extract(e.raw,'$.record.status.message'),'')=''
 AND COALESCE(json_array_length(e.raw,'$.record.events'),0)=0
 AND COALESCE(json_array_length(e.raw,'$.record.links'),0)=0
 AND COALESCE(CAST(json_extract(e.raw,'$.record.droppedAttributesCount') AS TEXT),'0')='0'
 AND COALESCE(CAST(json_extract(e.raw,'$.record.droppedEventsCount') AS TEXT),'0')='0'
 AND COALESCE(CAST(json_extract(e.raw,'$.record.droppedLinksCount') AS TEXT),'0')='0'
 AND EXISTS (SELECT 1 FROM json_each(e.raw,'$.resource.attributes') a
  WHERE a.value->>'key'='service.name' AND a.value->>'$.value.stringValue'='codex-app-server')
 AND CASE WHEN e.name IN ('code_mode.grpc.open_binding','code_mode.grpc.open_session')
  THEN e.parent_span='' ELSE EXISTS (
  SELECT 1 FROM json_each(e.raw,'$.record.attributes') a
  WHERE a.value->>'key'='session.id' AND json_type(a.value,'$.value.stringValue')='text' AND a.value->>'$.value.stringValue'<>''
 ) END
 AND EXISTS (SELECT 1 FROM json_each(e.raw,'$.record.attributes') a
  WHERE a.value->>'key'='code.module.name' AND a.value->>'$.value.stringValue'=
   CASE e.name WHEN 'code_mode.grpc.callback' THEN 'codex_code_mode::grpc_session::callbacks'
   ELSE 'codex_code_mode::grpc_session' END)
 AND NOT EXISTS (
  SELECT 1 FROM json_each(json_array(
   json_extract(e.raw,'$.record.attributes'),json_extract(e.raw,'$.resource.attributes'),json_extract(e.raw,'$.scope.attributes')
  )) scope JOIN json_each(scope.value) a
  WHERE COALESCE(a.value->>'key','') NOT IN (
   'service.name','service.version','service.namespace','project.name','project.tags','env',
   'telemetry.sdk.name','telemetry.sdk.version','telemetry.sdk.language','code.file.path','code.module.name',
   'code.line.number','target','thread.id','thread.name','session.id','busy_ns','idle_ns','execution.id','cell.id','invocation.id')
 );

-- A mixed trace can use a transport span to connect records. Identify its
-- members before any DELETE so that cleanup cannot break trace ancestry.
CREATE TEMP TABLE mixed_codex_traces(trace TEXT PRIMARY KEY);
INSERT INTO mixed_codex_traces
SELECT DISTINCT c.trace FROM discarded_codex_spans c
WHERE c.trace<>'' AND EXISTS (
 SELECT 1 FROM records e WHERE e.source='codex' AND e.trace=c.trace
 AND NOT EXISTS (SELECT 1 FROM discarded_codex_spans d WHERE d.id=e.id)
);
DELETE FROM discarded_codex_spans WHERE trace IN (SELECT trace FROM mixed_codex_traces);
DROP TABLE mixed_codex_traces;

DELETE FROM records WHERE id IN (SELECT id FROM discarded_codex_spans);
DELETE FROM session_versions WHERE session IN (SELECT session FROM discarded_codex_spans)
 AND NOT EXISTS (SELECT 1 FROM records WHERE session=session_versions.session);
DROP TABLE discarded_codex_spans;
