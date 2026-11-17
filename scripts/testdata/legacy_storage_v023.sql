-- Frozen v0.23.0 schema fixture for the standalone Python migrator.
CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,checksum TEXT NOT NULL,applied_at TEXT NOT NULL);
CREATE TABLE events(id TEXT PRIMARY KEY, session TEXT NOT NULL, parent TEXT NOT NULL, source TEXT NOT NULL, signal TEXT NOT NULL, time TEXT NOT NULL, trace TEXT NOT NULL, span TEXT NOT NULL, parent_span TEXT NOT NULL, sequence INTEGER NOT NULL DEFAULT 0, origin TEXT NOT NULL, kind TEXT NOT NULL, tool_name TEXT NOT NULL, name TEXT NOT NULL, raw BLOB NOT NULL);
CREATE TABLE event_keys(event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE, namespace TEXT NOT NULL, key TEXT NOT NULL, PRIMARY KEY(event_id,namespace,key));
CREATE TABLE archived_usage_records(id_hash BLOB NOT NULL,raw_hash BLOB NOT NULL,PRIMARY KEY(id_hash,raw_hash),
 FOREIGN KEY(id_hash,raw_hash) REFERENCES archived_records(id_hash,raw_hash) ON DELETE CASCADE) WITHOUT ROWID;
CREATE TABLE session_revisions(session TEXT PRIMARY KEY, revision INTEGER NOT NULL);
CREATE TABLE archives(
 id TEXT PRIMARY KEY,status TEXT NOT NULL,created_at TEXT NOT NULL,cutoff TEXT NOT NULL,
 session_count INTEGER NOT NULL DEFAULT 0,record_count INTEGER NOT NULL DEFAULT 0,raw_bytes INTEGER NOT NULL DEFAULT 0,
 processed_records INTEGER NOT NULL DEFAULT 0,archive_bytes INTEGER NOT NULL DEFAULT 0,sha256 TEXT NOT NULL DEFAULT '',
 verified_at TEXT NOT NULL DEFAULT '',download_at TEXT NOT NULL DEFAULT '',confirmed_at TEXT NOT NULL DEFAULT '',deleted_records INTEGER NOT NULL DEFAULT 0,
 reclaimed_bytes INTEGER NOT NULL DEFAULT 0,database_bytes_before INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',compaction_error TEXT NOT NULL DEFAULT '',rebuild_required INTEGER NOT NULL DEFAULT 0,rebuild_requested INTEGER NOT NULL DEFAULT 0);
CREATE TABLE archive_sessions(
 archive_id TEXT NOT NULL REFERENCES archives(id),session TEXT NOT NULL,source TEXT NOT NULL,
 revision INTEGER NOT NULL,records INTEGER NOT NULL,raw_bytes INTEGER NOT NULL,last_activity TEXT NOT NULL, folder TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(archive_id,session)) WITHOUT ROWID;
CREATE TABLE archive_locks(session TEXT PRIMARY KEY,archive_id TEXT NOT NULL REFERENCES archives(id));
CREATE TABLE archived_records(id_hash BLOB NOT NULL,raw_hash BLOB NOT NULL,PRIMARY KEY(id_hash,raw_hash)) WITHOUT ROWID;
CREATE VIRTUAL TABLE event_search USING fts5(
 text,content='',contentless_delete=1,tokenize='unicode61 remove_diacritics 2',prefix='2 3');
CREATE TABLE search_documents(
 rowid INTEGER PRIMARY KEY,event_id TEXT NOT NULL UNIQUE REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL);
CREATE TABLE search_dirty(event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE);
CREATE TABLE event_cards(
 id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 source TEXT NOT NULL,parent TEXT NOT NULL,time TEXT NOT NULL,sequence INTEGER NOT NULL,is_user INTEGER NOT NULL,
 signal TEXT NOT NULL,trace TEXT NOT NULL,span TEXT NOT NULL,parent_span TEXT NOT NULL,detail BLOB NOT NULL, prompt_title TEXT NOT NULL DEFAULT '', presentation_ready INTEGER NOT NULL DEFAULT 0, activity INTEGER NOT NULL DEFAULT 1, kind TEXT NOT NULL DEFAULT 'event', is_error INTEGER NOT NULL DEFAULT 0, has_skill INTEGER NOT NULL DEFAULT 0, conversation INTEGER NOT NULL DEFAULT 0, turn TEXT NOT NULL DEFAULT '');
CREATE TABLE timeline_entries(
 id TEXT PRIMARY KEY,session TEXT NOT NULL,source TEXT NOT NULL,parent TEXT NOT NULL,time TEXT NOT NULL,
 end TEXT NOT NULL,sequence INTEGER NOT NULL,is_user INTEGER NOT NULL,import_id TEXT NOT NULL,otel_id TEXT NOT NULL, activity INTEGER NOT NULL DEFAULT 1, kind TEXT NOT NULL DEFAULT 'event', is_error INTEGER NOT NULL DEFAULT 0, has_skill INTEGER NOT NULL DEFAULT 0, conversation INTEGER NOT NULL DEFAULT 0);
CREATE TABLE card_counts(session TEXT NOT NULL,origin TEXT NOT NULL,source TEXT NOT NULL,parent TEXT NOT NULL,count INTEGER NOT NULL,PRIMARY KEY(session,origin,source,parent)) WITHOUT ROWID;
CREATE TABLE timeline_counts(session TEXT PRIMARY KEY,count INTEGER NOT NULL);
CREATE TABLE session_catalog(
 session TEXT PRIMARY KEY,source TEXT NOT NULL,parent TEXT NOT NULL,count INTEGER NOT NULL,start TEXT NOT NULL,end TEXT NOT NULL,
 import_count INTEGER NOT NULL,import_start TEXT NOT NULL,import_end TEXT NOT NULL,
 otel_count INTEGER NOT NULL,otel_start TEXT NOT NULL,otel_end TEXT NOT NULL,preview BLOB NOT NULL, archive_start TEXT GENERATED ALWAYS AS (
 CASE WHEN import_count=0 THEN otel_start WHEN otel_count=0 THEN import_start
 ELSE MIN(import_start,otel_start) END) VIRTUAL, internal INTEGER NOT NULL DEFAULT 0, project_directory TEXT GENERATED ALWAYS AS (
 CASE WHEN json_type(preview,'$.cwd')='text'
 THEN replace(rtrim(json_extract(preview,'$.cwd'),'/\'),'\','/') ELSE '' END) VIRTUAL, project_key TEXT GENERATED ALWAYS AS (
 CASE WHEN json_type(preview,'$.project')='text' AND json_extract(preview,'$.project')<>''
 THEN 'name:'||json_extract(preview,'$.project')
 WHEN json_type(preview,'$.cwd')='text' AND json_extract(preview,'$.cwd')<>''
 THEN 'name:'||COALESCE(NULLIF(substr(project_directory,
 length(rtrim(project_directory,replace(project_directory,'/','')))+1),''),json_extract(preview,'$.cwd'))
 ELSE '' END) VIRTUAL);
CREATE TABLE read_model_dirty(session TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(session,id)) WITHOUT ROWID;
CREATE TABLE cards_dirty(id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE);
CREATE TABLE usage_points(
 id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 basis TEXT NOT NULL,group_key TEXT NOT NULL,series TEXT NOT NULL,start TEXT NOT NULL,end TEXT NOT NULL,
 time TEXT NOT NULL,sequence INTEGER NOT NULL,values_json TEXT NOT NULL);
CREATE TABLE usage_streams(id INTEGER PRIMARY KEY,session TEXT NOT NULL,origin TEXT NOT NULL,series TEXT NOT NULL,UNIQUE(session,origin,series));
CREATE TABLE usage_interval_data(id INTEGER PRIMARY KEY,event_id TEXT NOT NULL UNIQUE REFERENCES usage_points(id) ON DELETE CASCADE,
 stream INTEGER NOT NULL REFERENCES usage_streams(id),start TEXT NOT NULL,end TEXT NOT NULL,
 min_day INTEGER NOT NULL,max_day INTEGER NOT NULL,min_second INTEGER NOT NULL,max_second INTEGER NOT NULL,min_nano INTEGER NOT NULL,max_nano INTEGER NOT NULL);
CREATE VIRTUAL TABLE usage_interval_index USING rtree_i32(id,min_stream,max_stream,min_day,max_day,min_second,max_second,min_nano,max_nano);
CREATE TABLE usage_dirty(session TEXT NOT NULL,origin TEXT NOT NULL,basis TEXT NOT NULL,group_key TEXT NOT NULL,
 PRIMARY KEY(session,origin,basis,group_key)) WITHOUT ROWID;
CREATE TABLE usage_groups(session TEXT NOT NULL,origin TEXT NOT NULL,basis TEXT NOT NULL,group_key TEXT NOT NULL,values_json TEXT NOT NULL,
 PRIMARY KEY(session,origin,basis,group_key)) WITHOUT ROWID;
CREATE TABLE usage_totals(session TEXT NOT NULL,origin TEXT NOT NULL,basis TEXT NOT NULL,component TEXT NOT NULL,value TEXT NOT NULL,samples INTEGER NOT NULL,
 PRIMARY KEY(session,origin,basis,component)) WITHOUT ROWID;
CREATE TABLE usage_sessions_dirty(session TEXT PRIMARY KEY);
CREATE TABLE usage_summary(session TEXT NOT NULL REFERENCES session_catalog(session) ON DELETE CASCADE,origin TEXT NOT NULL,summary TEXT NOT NULL,
 PRIMARY KEY(session,origin)) WITHOUT ROWID;
CREATE TABLE event_errors(id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL);
CREATE TABLE timeline_errors(id TEXT PRIMARY KEY REFERENCES timeline_entries(id) ON DELETE CASCADE,session TEXT NOT NULL);
CREATE TABLE agent_calls(event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,owner TEXT NOT NULL,call_id TEXT NOT NULL,launch INTEGER NOT NULL,targets TEXT NOT NULL);
CREATE TABLE agent_activity(event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,agent TEXT NOT NULL);
CREATE TABLE maintenance_jobs(
 name TEXT PRIMARY KEY,cursor TEXT NOT NULL DEFAULT '',complete INTEGER NOT NULL DEFAULT 0 CHECK(complete IN(0,1)),
 processed INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '');
CREATE TABLE trace_dirty(source TEXT NOT NULL,trace TEXT NOT NULL,PRIMARY KEY(source,trace)) WITHOUT ROWID;
CREATE TABLE folder_members(
 session TEXT PRIMARY KEY,path TEXT NOT NULL,source TEXT NOT NULL,
 diagnostic INTEGER NOT NULL,end TEXT NOT NULL,usage TEXT NOT NULL, project TEXT, child INTEGER NOT NULL DEFAULT 0);
CREATE TABLE folder_totals(
 path TEXT NOT NULL,source TEXT NOT NULL,diagnostic INTEGER NOT NULL,
 count INTEGER NOT NULL,end TEXT NOT NULL,totals TEXT NOT NULL,
 PRIMARY KEY(path,source,diagnostic)) WITHOUT ROWID;
CREATE TABLE folder_dirty(session TEXT PRIMARY KEY) WITHOUT ROWID;
CREATE TABLE presentation_dirty(id TEXT PRIMARY KEY) WITHOUT ROWID;
CREATE TABLE model_facts(
 id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,source TEXT NOT NULL,origin TEXT NOT NULL,trace TEXT NOT NULL,
 span TEXT NOT NULL,parent_span TEXT NOT NULL,turn TEXT NOT NULL,role TEXT NOT NULL,
 root TEXT NOT NULL,time TEXT NOT NULL,info TEXT NOT NULL);
CREATE TABLE model_dirty(id TEXT PRIMARY KEY) WITHOUT ROWID;
CREATE TABLE model_calls(
 id TEXT PRIMARY KEY REFERENCES model_facts(id) ON DELETE CASCADE,
 session TEXT NOT NULL,origin TEXT NOT NULL,turn TEXT NOT NULL,time TEXT NOT NULL,info TEXT NOT NULL);
CREATE TABLE project_totals(
 project TEXT NOT NULL,source TEXT NOT NULL,diagnostic INTEGER NOT NULL,
 count INTEGER NOT NULL,end TEXT NOT NULL, root_count INTEGER NOT NULL DEFAULT 0, root_end TEXT NOT NULL DEFAULT '',PRIMARY KEY(project,source,diagnostic)) WITHOUT ROWID;
CREATE TABLE archive_stats(session TEXT PRIMARY KEY,records INTEGER NOT NULL,raw_bytes INTEGER NOT NULL) WITHOUT ROWID;
CREATE TABLE archive_stats_pending(session TEXT PRIMARY KEY) WITHOUT ROWID;
CREATE TABLE model_trace_dirty(source TEXT NOT NULL,trace TEXT NOT NULL,
 PRIMARY KEY(source,trace)) WITHOUT ROWID;
CREATE TABLE codex_session_facts(
 event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,ephemeral INTEGER NOT NULL,persistent INTEGER NOT NULL,temporary INTEGER NOT NULL, automatic INTEGER NOT NULL DEFAULT 0, human INTEGER NOT NULL DEFAULT 0);
CREATE TABLE session_visibility_dirty(session TEXT PRIMARY KEY) WITHOUT ROWID;
CREATE TABLE search_identifiers(
 event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,key TEXT NOT NULL,value TEXT NOT NULL,
 PRIMARY KEY(event_id,key,value)) WITHOUT ROWID;
CREATE TABLE "skill_events"(event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 name TEXT NOT NULL,plugin TEXT NOT NULL,scope TEXT NOT NULL,mode TEXT NOT NULL,evidence TEXT NOT NULL,group_key TEXT NOT NULL,time TEXT NOT NULL,path TEXT NOT NULL,search TEXT NOT NULL,
 PRIMARY KEY(event_id,name,plugin,evidence,group_key));
CREATE TABLE skill_read_inputs(
 event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,source TEXT NOT NULL,start_key TEXT NOT NULL,result_key TEXT NOT NULL,
 facts BLOB NOT NULL);
CREATE TABLE skill_reads_dirty(event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE) WITHOUT ROWID;
CREATE TABLE processing_buckets(
 session TEXT NOT NULL,origin TEXT NOT NULL,minute TEXT NOT NULL,
 count INTEGER NOT NULL,activity INTEGER NOT NULL,errors INTEGER NOT NULL,
 PRIMARY KEY(session,origin,minute)) WITHOUT ROWID;
CREATE TABLE api_keys(
 id TEXT PRIMARY KEY,name TEXT NOT NULL,key TEXT NOT NULL,
 key_hash BLOB NOT NULL UNIQUE,created_at TEXT NOT NULL);
CREATE INDEX event_keys_key ON event_keys(key,event_id);
CREATE INDEX events_session ON events(session,origin,time);
CREATE INDEX events_trace ON events(source,trace,span);
CREATE INDEX events_pending_trace ON events(source,trace,parent_span) WHERE trace<>'' AND session GLOB 'trace:*';
CREATE UNIQUE INDEX one_active_archive ON archives((1)) WHERE status IN ('preparing','ready','deleting','compacting');
CREATE INDEX cards_preview ON event_cards(session,origin,is_user DESC,time,sequence,id);
CREATE INDEX cards_directory ON event_cards(session,is_user DESC,time,sequence,id) WHERE json_type(detail,'$.cwd')='text' AND json_extract(detail,'$.cwd')<>'';
CREATE INDEX timeline_preview ON timeline_entries(session,is_user DESC,time,sequence,id);
CREATE UNIQUE INDEX timeline_pair_otel ON timeline_entries(otel_id) WHERE import_id<>'' AND otel_id<>'';
CREATE INDEX timeline_end ON timeline_entries(session,end DESC);
CREATE INDEX catalog_recent ON session_catalog(end DESC,session);
CREATE INDEX catalog_source_recent ON session_catalog(source,end DESC,session);
CREATE INDEX catalog_children ON session_catalog(parent,session);
CREATE INDEX catalog_directory_recent ON session_catalog(COALESCE(json_extract(preview,'$.cwd'),''),end DESC,session);
CREATE INDEX usage_group_points ON usage_points(session,origin,basis,group_key,time DESC,sequence DESC,id DESC);
CREATE INDEX usage_interval_stream ON usage_interval_data(stream);
CREATE INDEX cards_agent_candidates ON event_cards(id) WHERE source='copilot' OR json_extract(detail,'$.kind') IN ('tool','agent');
CREATE INDEX agent_call_lookup ON agent_calls(session,owner,call_id);
CREATE INDEX agent_activity_lookup ON agent_activity(session,agent,event_id);
CREATE INDEX cards_prompt_title ON event_cards(session,time,sequence,id) WHERE prompt_title<>'';
CREATE INDEX cards_origin_prompt_title ON event_cards(session,origin,time,sequence,id) WHERE prompt_title<>'';
CREATE INDEX cards_title_backfill ON event_cards(id) WHERE is_user=1;
CREATE INDEX folder_members_recent ON folder_members(path,source,diagnostic,end DESC,session);
CREATE INDEX cards_presentation_pending ON event_cards(id) WHERE presentation_ready=0;
CREATE INDEX cards_skill_page ON event_cards(session,origin,time,sequence,id) WHERE has_skill=1;
CREATE INDEX cards_conversation_page ON event_cards(session,origin,time,sequence,id) WHERE conversation=1;
CREATE INDEX timeline_skill_page ON timeline_entries(session,time,sequence,id) WHERE has_skill=1;
CREATE INDEX timeline_conversation_page ON timeline_entries(session,time,sequence,id) WHERE conversation=1;
CREATE INDEX cards_activity_page ON event_cards(session,origin,activity,time,sequence,id);
CREATE INDEX cards_kind_page ON event_cards(session,origin,kind,time,sequence,id);
CREATE INDEX cards_error_page ON event_cards(session,origin,time,sequence,id) WHERE is_error=1;
CREATE INDEX cards_turn_answer ON event_cards(session,turn,kind,origin,time DESC,sequence DESC,id DESC);
CREATE INDEX timeline_activity_page ON timeline_entries(session,activity,time,sequence,id);
CREATE INDEX timeline_kind_page ON timeline_entries(session,kind,time,sequence,id);
CREATE INDEX timeline_error_page ON timeline_entries(session,time,sequence,id) WHERE is_error=1;
CREATE INDEX model_fact_root ON model_facts(root,time,id);
CREATE INDEX model_calls_turn ON model_calls(session,turn,origin,time,id);
CREATE INDEX project_members_recent ON folder_members(project,source,diagnostic,end DESC,session);
CREATE INDEX catalog_archive_candidates ON session_catalog(end,archive_start,session);
CREATE INDEX archives_latest ON archives(created_at DESC,id DESC);
CREATE INDEX archive_snapshot_folder ON archive_sessions(archive_id,folder,last_activity,session);
CREATE INDEX model_fact_trace ON model_facts(source,trace,role,span);
CREATE INDEX codex_session_metadata ON codex_session_facts(session)
 WHERE ephemeral<>0 OR persistent<>0 OR temporary<>0;
CREATE INDEX codex_session_automatic ON codex_session_facts(session) WHERE automatic<>0;
CREATE INDEX codex_session_human ON codex_session_facts(session) WHERE human<>0;
CREATE INDEX search_identifiers_value ON search_identifiers(value,key,session);
CREATE INDEX catalog_project_recent ON session_catalog(project_key,end DESC,session);
CREATE INDEX project_members_roots_recent ON folder_members(project,source,diagnostic,end DESC,session) WHERE child=0;
CREATE INDEX skill_sessions ON skill_events(name,plugin,session,evidence,origin);
CREATE INDEX skills_by_session ON skill_events(session,name,plugin,time);
CREATE INDEX timeline_page ON timeline_entries(session,time,sequence,id,conversation,activity,is_error);
CREATE INDEX cards_page ON event_cards(session,origin,time,sequence,id,conversation,activity,is_error);
CREATE INDEX skill_read_start ON skill_read_inputs(session,source,start_key);
CREATE INDEX skill_read_result ON skill_read_inputs(session,source,result_key);
CREATE INDEX skill_evidence_groups ON skill_events(group_key,evidence);
CREATE INDEX catalog_roots_recent ON session_catalog(end DESC,session,source)
 WHERE internal=0 AND length(parent)=0;
CREATE TRIGGER archive_insert_guard BEFORE INSERT ON events WHEN EXISTS(SELECT 1 FROM archive_locks WHERE session=NEW.session)
 BEGIN SELECT RAISE(ABORT,'session archive deletion is in progress'); END;
CREATE TRIGGER archive_update_guard BEFORE UPDATE ON events WHEN EXISTS(SELECT 1 FROM archive_locks WHERE session IN (OLD.session,NEW.session))
 BEGIN SELECT RAISE(ABORT,'session archive deletion is in progress'); END;
CREATE TRIGGER revision_insert AFTER INSERT ON events BEGIN
 INSERT INTO session_revisions VALUES(NEW.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;
CREATE TRIGGER revision_update AFTER UPDATE ON events BEGIN
 INSERT INTO session_revisions VALUES(OLD.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1;
 INSERT INTO session_revisions SELECT NEW.session,1 WHERE NEW.session<>OLD.session ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;
CREATE TRIGGER revision_delete AFTER DELETE ON events BEGIN
 INSERT INTO session_revisions VALUES(OLD.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;
CREATE TRIGGER search_document_delete AFTER DELETE ON search_documents BEGIN
 DELETE FROM event_search WHERE rowid=OLD.rowid;END;
CREATE TRIGGER search_event_insert AFTER INSERT ON events BEGIN
 INSERT INTO search_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER search_event_update AFTER UPDATE ON events
 WHEN OLD.raw IS NOT NEW.raw OR OLD.session<>NEW.session OR OLD.name<>NEW.name OR OLD.source<>NEW.source BEGIN
 INSERT INTO search_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER card_count_insert AFTER INSERT ON event_cards BEGIN
 INSERT INTO card_counts VALUES(NEW.session,NEW.origin,NEW.source,NEW.parent,1) ON CONFLICT(session,origin,source,parent) DO UPDATE SET count=count+1;END;
CREATE TRIGGER card_count_delete AFTER DELETE ON event_cards BEGIN
 UPDATE card_counts SET count=count-1 WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent;
 DELETE FROM card_counts WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent AND count=0;END;
CREATE TRIGGER card_count_update AFTER UPDATE ON event_cards BEGIN
 UPDATE card_counts SET count=count-1 WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent;
 DELETE FROM card_counts WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent AND count=0;
 INSERT INTO card_counts VALUES(NEW.session,NEW.origin,NEW.source,NEW.parent,1) ON CONFLICT(session,origin,source,parent) DO UPDATE SET count=count+1;END;
CREATE TRIGGER timeline_count_insert AFTER INSERT ON timeline_entries BEGIN
 INSERT INTO timeline_counts VALUES(NEW.session,1) ON CONFLICT(session) DO UPDATE SET count=count+1;END;
CREATE TRIGGER timeline_count_delete AFTER DELETE ON timeline_entries BEGIN
 UPDATE timeline_counts SET count=count-1 WHERE session=OLD.session;
 DELETE FROM timeline_counts WHERE session=OLD.session AND count=0;END;
CREATE TRIGGER read_model_neighbors_UPDATE BEFORE UPDATE ON events WHEN EXISTS(SELECT 1 FROM event_keys WHERE event_id=OLD.id) BEGIN
 INSERT INTO read_model_dirty SELECT e.session,e.id FROM event_keys old JOIN event_keys neighbor ON neighbor.key=old.key
 JOIN events e ON e.id=neighbor.event_id WHERE old.event_id=OLD.id AND e.session=OLD.session AND e.source=OLD.source
 ON CONFLICT DO NOTHING;END;
CREATE TRIGGER read_model_neighbors_DELETE BEFORE DELETE ON events WHEN EXISTS(SELECT 1 FROM event_keys WHERE event_id=OLD.id) BEGIN
 INSERT INTO read_model_dirty SELECT e.session,e.id FROM event_keys old JOIN event_keys neighbor ON neighbor.key=old.key
 JOIN events e ON e.id=neighbor.event_id WHERE old.event_id=OLD.id AND e.session=OLD.session AND e.source=OLD.source
 ON CONFLICT DO NOTHING;END;
CREATE TRIGGER read_model_insert AFTER INSERT ON events BEGIN
 INSERT INTO read_model_dirty VALUES(NEW.session,NEW.id) ON CONFLICT DO NOTHING;
 INSERT INTO cards_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER read_model_update AFTER UPDATE ON events BEGIN
 INSERT INTO read_model_dirty VALUES(OLD.session,OLD.id) ON CONFLICT DO NOTHING;
 INSERT INTO read_model_dirty VALUES(NEW.session,NEW.id) ON CONFLICT DO NOTHING;
 INSERT INTO cards_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER read_model_delete AFTER DELETE ON events BEGIN
 INSERT INTO read_model_dirty VALUES(OLD.session,OLD.id) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER usage_point_insert AFTER INSERT ON usage_points BEGIN
 INSERT INTO usage_dirty VALUES(NEW.session,NEW.origin,NEW.basis,NEW.group_key) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER usage_point_delete BEFORE DELETE ON usage_points BEGIN
 INSERT INTO usage_dirty VALUES(OLD.session,OLD.origin,OLD.basis,OLD.group_key) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER usage_group_insert AFTER INSERT ON usage_groups BEGIN
 INSERT INTO usage_totals SELECT NEW.session,NEW.origin,NEW.basis,key,value,1 FROM json_each(NEW.values_json) WHERE true
 ON CONFLICT(session,origin,basis,component) DO UPDATE SET value=agentlog_add(value,excluded.value),samples=samples+1;END;
CREATE TRIGGER usage_group_delete AFTER DELETE ON usage_groups BEGIN
 UPDATE usage_totals SET value=agentlog_add(value,agentlog_neg((SELECT value FROM json_each(OLD.values_json) WHERE key=component))),samples=samples-1
 WHERE session=OLD.session AND origin=OLD.origin AND basis=OLD.basis AND component IN (SELECT key FROM json_each(OLD.values_json));
 DELETE FROM usage_totals WHERE session=OLD.session AND origin=OLD.origin AND basis=OLD.basis AND samples=0;END;
CREATE TRIGGER usage_total_insert AFTER INSERT ON usage_totals BEGIN INSERT INTO usage_sessions_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER usage_total_update AFTER UPDATE ON usage_totals BEGIN INSERT INTO usage_sessions_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER usage_total_delete AFTER DELETE ON usage_totals BEGIN INSERT INTO usage_sessions_dirty VALUES(OLD.session) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER usage_event_error_insert AFTER INSERT ON event_errors BEGIN
 INSERT INTO usage_totals VALUES(NEW.session,NEW.origin,'events','errors','1',1)
 ON CONFLICT(session,origin,basis,component) DO UPDATE SET value=agentlog_add(value,'1'),samples=samples+1;END;
CREATE TRIGGER usage_event_error_delete AFTER DELETE ON event_errors BEGIN
 UPDATE usage_totals SET value=agentlog_add(value,'-1'),samples=samples-1 WHERE session=OLD.session AND origin=OLD.origin AND basis='events' AND component='errors';
 DELETE FROM usage_totals WHERE session=OLD.session AND origin=OLD.origin AND basis='events' AND component='errors' AND samples=0;END;
CREATE TRIGGER usage_timeline_error_insert AFTER INSERT ON timeline_errors BEGIN
 INSERT INTO usage_totals VALUES(NEW.session,'','events','errors','1',1)
 ON CONFLICT(session,origin,basis,component) DO UPDATE SET value=agentlog_add(value,'1'),samples=samples+1;END;
CREATE TRIGGER usage_timeline_error_delete AFTER DELETE ON timeline_errors BEGIN
 UPDATE usage_totals SET value=agentlog_add(value,'-1'),samples=samples-1 WHERE session=OLD.session AND origin='' AND basis='events' AND component='errors';
 DELETE FROM usage_totals WHERE session=OLD.session AND origin='' AND basis='events' AND component='errors' AND samples=0;END;
CREATE TRIGGER agent_index_update AFTER UPDATE ON events BEGIN
 DELETE FROM agent_calls WHERE event_id=OLD.id;
 DELETE FROM agent_activity WHERE event_id=OLD.id; END;
CREATE TRIGGER usage_interval_insert AFTER INSERT ON usage_interval_data BEGIN
INSERT INTO usage_interval_index VALUES(NEW.id,NEW.stream,NEW.stream,NEW.min_day,NEW.max_day,NEW.min_second,NEW.max_second,NEW.min_nano,NEW.max_nano);
INSERT INTO usage_dirty SELECT p.session,p.origin,p.basis,p.group_key FROM usage_interval_index i CROSS JOIN usage_points p ON p.rowid=i.id
 WHERE i.min_stream<=NEW.stream AND i.max_stream>=NEW.stream AND i.min_day<=NEW.max_day AND i.max_day>=NEW.min_day
 AND i.min_second<=NEW.max_second AND i.max_second>=NEW.min_second AND i.min_nano<=NEW.max_nano AND i.max_nano>=NEW.min_nano
 AND p.start<NEW.end AND p.end>NEW.start ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER usage_interval_delete BEFORE DELETE ON usage_interval_data BEGIN
INSERT INTO usage_dirty SELECT p.session,p.origin,p.basis,p.group_key FROM usage_interval_index i CROSS JOIN usage_points p ON p.rowid=i.id
 WHERE i.min_stream<=OLD.stream AND i.max_stream>=OLD.stream AND i.min_day<=OLD.max_day AND i.max_day>=OLD.min_day
 AND i.min_second<=OLD.max_second AND i.max_second>=OLD.min_second AND i.min_nano<=OLD.max_nano AND i.max_nano>=OLD.min_nano
 AND p.start<OLD.end AND p.end>OLD.start ON CONFLICT DO NOTHING;
DELETE FROM usage_interval_index WHERE id=OLD.id;
END;
CREATE TRIGGER usage_interval_stream_delete AFTER DELETE ON usage_interval_data BEGIN
 DELETE FROM usage_streams WHERE id=OLD.stream AND NOT EXISTS(SELECT 1 FROM usage_interval_data WHERE stream=OLD.stream);END;
CREATE TRIGGER trace_work_insert AFTER INSERT ON events WHEN NEW.trace<>'' BEGIN
 INSERT INTO trace_dirty(source,trace) VALUES(NEW.source,NEW.trace) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER trace_work_delete AFTER DELETE ON events WHEN OLD.trace<>'' BEGIN
 INSERT INTO trace_dirty(source,trace) VALUES(OLD.source,OLD.trace) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER trace_work_update AFTER UPDATE ON events
 WHEN OLD.source<>NEW.source OR OLD.trace<>NEW.trace OR OLD.session<>NEW.session OR OLD.span<>NEW.span OR OLD.parent_span<>NEW.parent_span BEGIN
 INSERT INTO trace_dirty(source,trace) SELECT OLD.source,OLD.trace WHERE OLD.trace<>'' ON CONFLICT DO NOTHING;
 INSERT INTO trace_dirty(source,trace) SELECT NEW.source,NEW.trace WHERE NEW.trace<>'' ON CONFLICT DO NOTHING;END;
CREATE TRIGGER folder_session_insert AFTER INSERT ON session_catalog BEGIN
 INSERT INTO folder_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER folder_session_update AFTER UPDATE ON session_catalog BEGIN
 INSERT INTO folder_dirty VALUES(OLD.session) ON CONFLICT DO NOTHING;
 INSERT INTO folder_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER folder_session_delete AFTER DELETE ON session_catalog BEGIN
 INSERT INTO folder_dirty VALUES(OLD.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER folder_usage_insert AFTER INSERT ON usage_summary WHEN NEW.origin='' BEGIN
 INSERT INTO folder_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER folder_usage_update AFTER UPDATE ON usage_summary BEGIN
 INSERT INTO folder_dirty SELECT OLD.session WHERE OLD.origin='' ON CONFLICT DO NOTHING;
 INSERT INTO folder_dirty SELECT NEW.session WHERE NEW.origin='' ON CONFLICT DO NOTHING; END;
CREATE TRIGGER folder_usage_delete AFTER DELETE ON usage_summary WHEN OLD.origin='' BEGIN
 INSERT INTO folder_dirty VALUES(OLD.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER folder_lock_insert AFTER INSERT ON archive_locks BEGIN
 INSERT INTO folder_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER folder_lock_delete AFTER DELETE ON archive_locks BEGIN
 INSERT INTO folder_dirty VALUES(OLD.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER presentation_card_insert AFTER INSERT ON event_cards BEGIN
 INSERT INTO presentation_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER presentation_card_update AFTER UPDATE ON event_cards BEGIN
 INSERT INTO presentation_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;
 INSERT INTO presentation_dirty SELECT id FROM timeline_entries WHERE otel_id=NEW.id AND import_id<>'' AND otel_id<>'' ON CONFLICT DO NOTHING; END;
CREATE TRIGGER presentation_timeline_insert AFTER INSERT ON timeline_entries BEGIN
 INSERT INTO presentation_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER model_fact_insert AFTER INSERT ON model_facts BEGIN
 INSERT INTO model_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER model_fact_update AFTER UPDATE ON model_facts BEGIN
 INSERT INTO model_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;
 INSERT INTO model_dirty SELECT OLD.root WHERE OLD.root<>'' ON CONFLICT DO NOTHING; END;
CREATE TRIGGER model_fact_delete BEFORE DELETE ON model_facts BEGIN
 INSERT INTO model_dirty SELECT OLD.root WHERE OLD.root<>'' AND OLD.root<>OLD.id ON CONFLICT DO NOTHING;
 UPDATE model_facts SET root='' WHERE root=OLD.id AND id<>OLD.id; END;
CREATE TRIGGER archive_stats_insert AFTER INSERT ON events BEGIN
 INSERT INTO archive_stats VALUES(NEW.session,1,length(NEW.raw))
 ON CONFLICT(session) DO UPDATE SET records=records+1,raw_bytes=raw_bytes+excluded.raw_bytes; END;
CREATE TRIGGER archive_stats_delete AFTER DELETE ON events BEGIN
 UPDATE archive_stats SET records=records-1,raw_bytes=raw_bytes-length(OLD.raw) WHERE session=OLD.session;
 DELETE FROM archive_stats WHERE session=OLD.session AND records=0; END;
CREATE TRIGGER archive_stats_update AFTER UPDATE OF session,raw ON events
 WHEN OLD.session<>NEW.session OR length(OLD.raw)<>length(NEW.raw) BEGIN
 UPDATE archive_stats SET records=records-1,raw_bytes=raw_bytes-length(OLD.raw) WHERE session=OLD.session;
 DELETE FROM archive_stats WHERE session=OLD.session AND records=0;
 INSERT INTO archive_stats VALUES(NEW.session,1,length(NEW.raw))
 ON CONFLICT(session) DO UPDATE SET records=records+1,raw_bytes=raw_bytes+excluded.raw_bytes; END;
CREATE TRIGGER model_trace_insert AFTER INSERT ON events
 WHEN NEW.signal='traces' AND NEW.trace<>'' BEGIN
 INSERT INTO model_trace_dirty VALUES(NEW.source,NEW.trace) ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER model_trace_update AFTER UPDATE OF session,source,origin,signal,trace,span,parent_span ON events BEGIN
 INSERT INTO model_trace_dirty SELECT OLD.source,OLD.trace WHERE OLD.signal='traces' AND OLD.trace<>'' ON CONFLICT DO NOTHING;
 INSERT INTO model_trace_dirty SELECT NEW.source,NEW.trace WHERE NEW.signal='traces' AND NEW.trace<>'' ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER model_trace_delete AFTER DELETE ON events
 WHEN OLD.signal='traces' AND OLD.trace<>'' BEGIN
 INSERT INTO model_trace_dirty VALUES(OLD.source,OLD.trace) ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER model_request_insert AFTER INSERT ON model_facts WHEN NEW.role='request' BEGIN
 INSERT INTO model_trace_dirty VALUES(NEW.source,NEW.trace) ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER model_request_update AFTER UPDATE OF session,source,origin,trace,span,role ON model_facts
 WHEN OLD.role='request' OR NEW.role='request' BEGIN
 INSERT INTO model_trace_dirty VALUES(OLD.source,OLD.trace) ON CONFLICT DO NOTHING;
 INSERT INTO model_trace_dirty VALUES(NEW.source,NEW.trace) ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER model_request_delete AFTER DELETE ON model_facts WHEN OLD.role='request' BEGIN
 INSERT INTO model_trace_dirty VALUES(OLD.source,OLD.trace) ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER session_fact_insert AFTER INSERT ON codex_session_facts BEGIN
 INSERT INTO session_visibility_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER session_fact_update AFTER UPDATE ON codex_session_facts BEGIN
 INSERT INTO session_visibility_dirty VALUES(OLD.session) ON CONFLICT DO NOTHING;
 INSERT INTO session_visibility_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER session_fact_delete AFTER DELETE ON codex_session_facts BEGIN
 INSERT INTO session_visibility_dirty VALUES(OLD.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER session_visibility_insert AFTER INSERT ON session_catalog BEGIN
 INSERT INTO session_visibility_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER skill_event_update AFTER UPDATE ON events BEGIN DELETE FROM skill_events WHERE event_id=OLD.id; END;
CREATE TRIGGER skill_read_key_added AFTER INSERT ON event_keys WHEN NEW.namespace='tool.call' BEGIN
 INSERT OR IGNORE INTO skill_reads_dirty
 SELECT r.event_id FROM events e CROSS JOIN skill_read_inputs r
 WHERE e.id=NEW.event_id AND r.session=e.session AND r.source=e.source
 AND r.start_key=NEW.key
 UNION ALL
 SELECT r.event_id FROM events e CROSS JOIN skill_read_inputs r
 WHERE e.id=NEW.event_id AND r.session=e.session AND r.source=e.source AND r.result_key=NEW.key;
END;
CREATE TRIGGER skill_read_key_removed BEFORE DELETE ON event_keys WHEN OLD.namespace='tool.call' BEGIN
 INSERT OR IGNORE INTO skill_reads_dirty
 SELECT r.event_id FROM events e CROSS JOIN skill_read_inputs r
 WHERE e.id=OLD.event_id AND r.session=e.session AND r.source=e.source
 AND r.start_key=OLD.key
 UNION ALL
 SELECT r.event_id FROM events e CROSS JOIN skill_read_inputs r
 WHERE e.id=OLD.event_id AND r.session=e.session AND r.source=e.source AND r.result_key=OLD.key;
END;
CREATE TRIGGER skill_read_event_removed BEFORE DELETE ON events BEGIN
 INSERT OR IGNORE INTO skill_reads_dirty
 SELECT r.event_id FROM event_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.start_key=k.key
 UNION ALL
 SELECT r.event_id FROM event_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.result_key=k.key;
END;
CREATE TRIGGER skill_read_event_changing BEFORE UPDATE ON events BEGIN
 INSERT OR IGNORE INTO skill_reads_dirty
 SELECT r.event_id FROM event_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.start_key=k.key
 UNION ALL
 SELECT r.event_id FROM event_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.result_key=k.key;
END;
CREATE TRIGGER skill_read_input_changed AFTER UPDATE ON events BEGIN
 DELETE FROM skill_read_inputs WHERE event_id=OLD.id;
END;
CREATE TRIGGER skill_read_input_removed AFTER DELETE ON skill_read_inputs BEGIN
 DELETE FROM skill_reads_dirty WHERE event_id=OLD.event_id;
END;
CREATE TRIGGER processing_cards_insert AFTER INSERT ON event_cards
 WHEN NEW.conversation=0 AND (SELECT complete OR NEW.id<=cursor FROM maintenance_jobs WHERE name='processing.cards.v1') BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,NEW.origin,substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;
CREATE TRIGGER processing_cards_delete AFTER DELETE ON event_cards
 WHEN OLD.conversation=0 AND (SELECT complete OR OLD.id<=cursor FROM maintenance_jobs WHERE name='processing.cards.v1') BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16) AND count=0;
END;
CREATE TRIGGER processing_cards_update_remove AFTER UPDATE ON event_cards
 WHEN OLD.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.origin IS NOT NEW.origin OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) AND (SELECT complete OR OLD.id<=cursor FROM maintenance_jobs WHERE name='processing.cards.v1') BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16) AND count=0;
END;
CREATE TRIGGER processing_cards_update_add AFTER UPDATE ON event_cards
 WHEN NEW.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.origin IS NOT NEW.origin OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) AND (SELECT complete OR NEW.id<=cursor FROM maintenance_jobs WHERE name='processing.cards.v1') BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,NEW.origin,substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;
CREATE TRIGGER processing_timeline_insert AFTER INSERT ON timeline_entries
 WHEN NEW.conversation=0 AND (SELECT complete OR NEW.id<=cursor FROM maintenance_jobs WHERE name='processing.timeline.v1') BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,'',substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;
CREATE TRIGGER processing_timeline_delete AFTER DELETE ON timeline_entries
 WHEN OLD.conversation=0 AND (SELECT complete OR OLD.id<=cursor FROM maintenance_jobs WHERE name='processing.timeline.v1') BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16) AND count=0;
END;
CREATE TRIGGER processing_timeline_update_remove AFTER UPDATE ON timeline_entries
 WHEN OLD.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) AND (SELECT complete OR OLD.id<=cursor FROM maintenance_jobs WHERE name='processing.timeline.v1') BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16) AND count=0;
END;
CREATE TRIGGER processing_timeline_update_add AFTER UPDATE ON timeline_entries
 WHEN NEW.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) AND (SELECT complete OR NEW.id<=cursor FROM maintenance_jobs WHERE name='processing.timeline.v1') BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,'',substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;
INSERT INTO schema_migrations VALUES(1,'0001_baseline.sql','b71fbee6b62074e7d8143e6e6a6d15040c1f5818dd90f77fa048efd8088c923f','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(2,'0002_maintenance.sql','24786b828c97672cd8f88b564296290c17301fea78447e1dea6091dd700de355','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(3,'0003_trace_work.sql','3404bd88a26a5df5ad8def8691664d62a99c145e35837da87b4f347bb5b27e3d','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(4,'0004_session_titles.sql','ad05de12ad1e5b146b4403fb43ce584dd4c80df2edf2e738afbd86bd07ce3535','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(5,'0005_folder_overview.sql','17b241b419d10ed99244626282b46c0186ba931b95fb66e2677eb63bb3867293','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(6,'0006_activity.sql','9bb9ed497dc262d8de4440e29b75440ed106884eadca2d11c44b6232fdde1d5d','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(7,'0007_projects.sql','00c65924270907541bdcffad89eab5e82a0ef644e80ed75cc32f1224f545924a','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(8,'0008_archive_metadata.sql','92d6257ed4ded9ace4cf9b0fc3b92f37bbd9e3520fa06ce5894ad9ba1754940a','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(9,'0009_model_ancestry.sql','d4f754b395547a7a7fa3023152323f2b03a96b9d8a2f61e6085db28b8db7b93d','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(10,'0010_codex_visibility.sql','b005dc04b88ee4eac3cb1049fcd184e43de23a56fb0683b1ebbf819cdabae44e','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(11,'0011_codex_app_titles.sql','88c26d0f6e6fbad8ea7831affb116f1dc01d58df02f8c49d07843bf7a4f8165c','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(12,'0012_codex_automatic_sessions.sql','10f0439c737200a2ffa3690d4b3cf1d5cd90418ba72eaec0255fab5a6aad93b3','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(13,'0013_string_search.sql','d45f8fd4d34ad64facccf9f768c2629707d4fd24b401c9bfc0940146b83379c6','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(14,'0014_project_names.sql','e6492198c9b121299275cb6414794b18a8078810b53c9c9eec14a737447f37b2','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(15,'0015_visible_project_counts.sql','a6bd3636176a2c6056624e89a80c6f602f014ef45d55df7780ca700c4222eac8','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(16,'0016_provider_presentations.sql','535479308fd81b276ed8294788516076064b7fe9333286107067cc878cc9357c','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(17,'0017_codex_page_messages.sql','0ced3b7ba021da1736370006567b7460b7f7cb6582354a977026edacdcd08e58','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(18,'0018_conversation.sql','5f865bf6cd856882b0d5c4dcbcb30512d90075f19834271233e4f7e67c9f389f','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(19,'0019_codex_tool_timing.sql','b97b30e5dc6aa97c237a90425111c5bc5d733d05d52460c5217805ffbdf0b11f','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(20,'0020_processing_summary_indexes.sql','5dbd8594d6c25dc02c8c56ebd5844a67e1d6c8e3f2e6dfed1ae5c70190ea22b9','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(21,'0021_skill_reads.sql','90c3ccc5e0bc738f7a6f934e4da9fb580d49789695756296313cde64b29eef38','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(22,'0022_navigation_summaries.sql','58007ee1fb687fac12683faf76eb42fc2f135243d86f4f3da1dde11ec01c8ac8','2026-11-17T00:00:00.000000000Z');
INSERT INTO schema_migrations VALUES(23,'0023_api_keys.sql','550087876822b658b5b5c14553e2e3b2d4e36f2d8bf30c0b6f715b37d3311332','2026-11-17T00:00:00.000000000Z');
INSERT INTO maintenance_jobs VALUES('usage.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('agents.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('skills.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('directories.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('titles.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('presentation.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('codex_visibility.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('codex_app_titles.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('codex_automatic_sessions.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('search_strings.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('providers.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('codex_page_messages.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('conversation.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('codex_tool_timing.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('skills.reads.v2','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('processing.cards.v1','',1,0,'','');
INSERT INTO maintenance_jobs VALUES('processing.timeline.v1','',1,0,'','');
