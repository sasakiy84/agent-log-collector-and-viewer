-- Storage generation 2. This is the complete schema, shared with the Python offline migrator.

-- Record IDs and raw JSON are preserved. API names are independent of storage names.

PRAGMA application_id=1095518002;

PRAGMA user_version=1;

CREATE TABLE work_items(kind TEXT NOT NULL,scope TEXT NOT NULL,key TEXT NOT NULL,PRIMARY KEY(kind,scope,key)) WITHOUT ROWID;

CREATE VIEW pending_records AS SELECT scope AS id FROM work_items WHERE kind='records';

CREATE VIEW pending_search AS SELECT scope AS event_id FROM work_items WHERE kind='search';

CREATE VIEW pending_presentation AS SELECT scope AS id FROM work_items WHERE kind='presentation';

CREATE VIEW pending_timeline AS SELECT scope AS session,key AS id FROM work_items WHERE kind='timeline';

CREATE VIEW pending_projects AS SELECT scope AS session FROM work_items WHERE kind='projects';

CREATE VIEW pending_trace AS SELECT scope AS source,key AS trace FROM work_items WHERE kind='trace';

CREATE VIEW pending_models AS SELECT scope AS id FROM work_items WHERE kind='models';

CREATE VIEW pending_model_trace AS SELECT scope AS source,key AS trace FROM work_items WHERE kind='model_trace';

CREATE VIEW pending_visibility AS SELECT scope AS session FROM work_items WHERE kind='visibility';

CREATE VIEW pending_skill_reads AS SELECT scope AS event_id FROM work_items WHERE kind='skill_reads';

CREATE VIEW pending_usage_sessions AS SELECT scope AS session FROM work_items WHERE kind='usage_sessions';

CREATE VIEW pending_usage AS SELECT scope AS session,json_extract(key,'$[0]') AS origin,json_extract(key,'$[1]') AS basis,json_extract(key,'$[2]') AS group_key FROM work_items WHERE kind='usage';

CREATE VIEW pending_storage AS SELECT scope AS session FROM work_items WHERE kind='storage';

CREATE TABLE records(id TEXT PRIMARY KEY, session TEXT NOT NULL, parent TEXT NOT NULL, source TEXT NOT NULL, signal TEXT NOT NULL, time TEXT NOT NULL, trace TEXT NOT NULL, span TEXT NOT NULL, parent_span TEXT NOT NULL, sequence INTEGER NOT NULL DEFAULT 0, origin TEXT NOT NULL, kind TEXT NOT NULL, tool_name TEXT NOT NULL, name TEXT NOT NULL, raw BLOB NOT NULL);

CREATE TABLE record_keys(event_id TEXT NOT NULL REFERENCES records(id) ON DELETE CASCADE, namespace TEXT NOT NULL, key TEXT NOT NULL, PRIMARY KEY(event_id,namespace,key));

CREATE INDEX event_keys_key ON record_keys(key,event_id);

CREATE INDEX events_session ON records(session,origin,time);

CREATE INDEX events_trace ON records(source,trace,span);

CREATE INDEX events_pending_trace ON records(source,trace,parent_span) WHERE trace<>'' AND session GLOB 'trace:*';

CREATE TABLE usage_tombstones(id_hash BLOB NOT NULL,raw_hash BLOB NOT NULL,PRIMARY KEY(id_hash,raw_hash),
 FOREIGN KEY(id_hash,raw_hash) REFERENCES record_tombstones(id_hash,raw_hash) ON DELETE CASCADE) WITHOUT ROWID;

CREATE TABLE session_versions(session TEXT PRIMARY KEY, revision INTEGER NOT NULL);

CREATE TABLE archive_jobs(
 id TEXT PRIMARY KEY,status TEXT NOT NULL,created_at TEXT NOT NULL,cutoff TEXT NOT NULL,
 session_count INTEGER NOT NULL DEFAULT 0,record_count INTEGER NOT NULL DEFAULT 0,raw_bytes INTEGER NOT NULL DEFAULT 0,
 processed_records INTEGER NOT NULL DEFAULT 0,archive_bytes INTEGER NOT NULL DEFAULT 0,sha256 TEXT NOT NULL DEFAULT '',
 verified_at TEXT NOT NULL DEFAULT '',download_at TEXT NOT NULL DEFAULT '',confirmed_at TEXT NOT NULL DEFAULT '',deleted_records INTEGER NOT NULL DEFAULT 0,
 reclaimed_bytes INTEGER NOT NULL DEFAULT 0,database_bytes_before INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',compaction_error TEXT NOT NULL DEFAULT '',rebuild_required INTEGER NOT NULL DEFAULT 0,rebuild_requested INTEGER NOT NULL DEFAULT 0);

CREATE UNIQUE INDEX one_active_archive ON archive_jobs((1)) WHERE status IN ('preparing','ready','deleting','compacting');

CREATE TABLE archive_sessions(
 archive_id TEXT NOT NULL REFERENCES archive_jobs(id),session TEXT NOT NULL,source TEXT NOT NULL,
 revision INTEGER NOT NULL,records INTEGER NOT NULL,raw_bytes INTEGER NOT NULL,last_activity TEXT NOT NULL, folder TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(archive_id,session)) WITHOUT ROWID;

CREATE TABLE archive_locks(session TEXT PRIMARY KEY,archive_id TEXT NOT NULL REFERENCES archive_jobs(id));

CREATE TABLE record_tombstones(id_hash BLOB NOT NULL,raw_hash BLOB NOT NULL,PRIMARY KEY(id_hash,raw_hash)) WITHOUT ROWID;

CREATE TRIGGER archive_insert_guard BEFORE INSERT ON records WHEN EXISTS(SELECT 1 FROM archive_locks WHERE session=NEW.session)
 BEGIN SELECT RAISE(ABORT,'session archive deletion is in progress'); END;

CREATE TRIGGER archive_update_guard BEFORE UPDATE ON records WHEN EXISTS(SELECT 1 FROM archive_locks WHERE session IN (OLD.session,NEW.session))
 BEGIN SELECT RAISE(ABORT,'session archive deletion is in progress'); END;

CREATE TRIGGER revision_insert AFTER INSERT ON records BEGIN
 INSERT INTO session_versions VALUES(NEW.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;

CREATE TRIGGER revision_update AFTER UPDATE ON records BEGIN
 INSERT INTO session_versions VALUES(OLD.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1;
 INSERT INTO session_versions SELECT NEW.session,1 WHERE NEW.session<>OLD.session ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;

CREATE TRIGGER revision_delete AFTER DELETE ON records BEGIN
 INSERT INTO session_versions VALUES(OLD.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;

CREATE VIRTUAL TABLE event_search USING fts5(
 text,content='',contentless_delete=1,tokenize='unicode61 remove_diacritics 2',prefix='2 3');

CREATE TABLE search_documents(
 rowid INTEGER PRIMARY KEY,event_id TEXT NOT NULL UNIQUE REFERENCES records(id) ON DELETE CASCADE,session TEXT NOT NULL);

CREATE TRIGGER search_document_delete AFTER DELETE ON search_documents BEGIN
 DELETE FROM event_search WHERE rowid=OLD.rowid;END;

CREATE TRIGGER search_event_insert AFTER INSERT ON records BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('search',NEW.id,'') ON CONFLICT DO NOTHING;END;

CREATE TRIGGER search_event_update AFTER UPDATE ON records
 WHEN OLD.raw IS NOT NEW.raw OR OLD.session<>NEW.session OR OLD.name<>NEW.name OR OLD.source<>NEW.source BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('search',NEW.id,'') ON CONFLICT DO NOTHING;END;

CREATE TABLE record_facts(
 id TEXT PRIMARY KEY REFERENCES records(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 source TEXT NOT NULL,parent TEXT NOT NULL,time TEXT NOT NULL,sequence INTEGER NOT NULL,is_user INTEGER NOT NULL,
 signal TEXT NOT NULL,trace TEXT NOT NULL,span TEXT NOT NULL,parent_span TEXT NOT NULL,detail BLOB NOT NULL, prompt_title TEXT NOT NULL DEFAULT '', presentation_ready INTEGER NOT NULL DEFAULT 0, activity INTEGER NOT NULL DEFAULT 1, kind TEXT NOT NULL DEFAULT 'event', is_error INTEGER NOT NULL DEFAULT 0, has_skill INTEGER NOT NULL DEFAULT 0, conversation INTEGER NOT NULL DEFAULT 0, turn TEXT NOT NULL DEFAULT '');

CREATE INDEX cards_preview ON record_facts(session,origin,is_user DESC,time,sequence,id);

CREATE INDEX cards_directory ON record_facts(session,is_user DESC,time,sequence,id) WHERE json_type(detail,'$.cwd')='text' AND json_extract(detail,'$.cwd')<>'';

CREATE TABLE logical_events(
 id TEXT PRIMARY KEY,session TEXT NOT NULL,source TEXT NOT NULL,parent TEXT NOT NULL,time TEXT NOT NULL,
 end TEXT NOT NULL,sequence INTEGER NOT NULL,is_user INTEGER NOT NULL,import_id TEXT NOT NULL,otel_id TEXT NOT NULL, activity INTEGER NOT NULL DEFAULT 1, kind TEXT NOT NULL DEFAULT 'event', is_error INTEGER NOT NULL DEFAULT 0, has_skill INTEGER NOT NULL DEFAULT 0, conversation INTEGER NOT NULL DEFAULT 0);

CREATE INDEX timeline_preview ON logical_events(session,is_user DESC,time,sequence,id);

CREATE UNIQUE INDEX timeline_pair_otel ON logical_events(otel_id) WHERE import_id<>'' AND otel_id<>'';

CREATE INDEX timeline_end ON logical_events(session,end DESC);

CREATE TABLE card_counts(session TEXT NOT NULL,origin TEXT NOT NULL,source TEXT NOT NULL,parent TEXT NOT NULL,count INTEGER NOT NULL,PRIMARY KEY(session,origin,source,parent)) WITHOUT ROWID;

CREATE TABLE timeline_counts(session TEXT PRIMARY KEY,count INTEGER NOT NULL);

CREATE TRIGGER card_count_insert AFTER INSERT ON record_facts BEGIN
 INSERT INTO card_counts VALUES(NEW.session,NEW.origin,NEW.source,NEW.parent,1) ON CONFLICT(session,origin,source,parent) DO UPDATE SET count=count+1;END;

CREATE TRIGGER card_count_delete AFTER DELETE ON record_facts BEGIN
 UPDATE card_counts SET count=count-1 WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent;
 DELETE FROM card_counts WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent AND count=0;END;

CREATE TRIGGER card_count_update AFTER UPDATE ON record_facts
 WHEN OLD.session IS NOT NEW.session OR OLD.origin IS NOT NEW.origin
 OR OLD.source IS NOT NEW.source OR OLD.parent IS NOT NEW.parent BEGIN
 UPDATE card_counts SET count=count-1 WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent;
 DELETE FROM card_counts WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent AND count=0;
 INSERT INTO card_counts VALUES(NEW.session,NEW.origin,NEW.source,NEW.parent,1) ON CONFLICT(session,origin,source,parent) DO UPDATE SET count=count+1;END;

CREATE TRIGGER timeline_count_insert AFTER INSERT ON logical_events BEGIN
 INSERT INTO timeline_counts VALUES(NEW.session,1) ON CONFLICT(session) DO UPDATE SET count=count+1;END;

CREATE TRIGGER timeline_count_delete AFTER DELETE ON logical_events BEGIN
 UPDATE timeline_counts SET count=count-1 WHERE session=OLD.session;
 DELETE FROM timeline_counts WHERE session=OLD.session AND count=0;END;

CREATE TABLE sessions(
 session TEXT PRIMARY KEY,source TEXT NOT NULL,parent TEXT NOT NULL,count INTEGER NOT NULL,start TEXT NOT NULL,end TEXT NOT NULL,
 import_count INTEGER NOT NULL,import_start TEXT NOT NULL,import_end TEXT NOT NULL,
 otel_count INTEGER NOT NULL,otel_start TEXT NOT NULL,otel_end TEXT NOT NULL,preview BLOB NOT NULL, archive_start TEXT GENERATED ALWAYS AS (
 CASE WHEN import_count=0 THEN otel_start WHEN otel_count=0 THEN import_start
 ELSE MIN(import_start,otel_start) END) VIRTUAL, internal INTEGER NOT NULL DEFAULT 0, project_directory TEXT NOT NULL DEFAULT '', project_key TEXT NOT NULL DEFAULT '');

CREATE INDEX catalog_recent ON sessions(end DESC,session);

CREATE INDEX catalog_source_recent ON sessions(source,end DESC,session);

CREATE INDEX catalog_children ON sessions(parent,session);

CREATE INDEX catalog_directory_recent ON sessions(COALESCE(json_extract(preview,'$.cwd'),''),end DESC,session);

CREATE TRIGGER read_model_neighbors_UPDATE BEFORE UPDATE ON records WHEN EXISTS(SELECT 1 FROM record_keys WHERE event_id=OLD.id) BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'timeline',e.session,e.id FROM record_keys old JOIN record_keys neighbor ON neighbor.key=old.key
 JOIN records e ON e.id=neighbor.event_id WHERE old.event_id=OLD.id AND e.session=OLD.session AND e.source=OLD.source
 ON CONFLICT DO NOTHING;END;

CREATE TRIGGER read_model_neighbors_DELETE BEFORE DELETE ON records WHEN EXISTS(SELECT 1 FROM record_keys WHERE event_id=OLD.id) BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'timeline',e.session,e.id FROM record_keys old JOIN record_keys neighbor ON neighbor.key=old.key
 JOIN records e ON e.id=neighbor.event_id WHERE old.event_id=OLD.id AND e.session=OLD.session AND e.source=OLD.source
 ON CONFLICT DO NOTHING;END;

CREATE TRIGGER read_model_insert AFTER INSERT ON records BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('timeline',NEW.session,NEW.id) ON CONFLICT DO NOTHING;
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('records',NEW.id,'') ON CONFLICT DO NOTHING;END;

CREATE TRIGGER read_model_update AFTER UPDATE ON records BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('timeline',OLD.session,OLD.id) ON CONFLICT DO NOTHING;
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('timeline',NEW.session,NEW.id) ON CONFLICT DO NOTHING;
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('records',NEW.id,'') ON CONFLICT DO NOTHING;END;

CREATE TRIGGER read_model_delete AFTER DELETE ON records BEGIN
 DELETE FROM work_items WHERE kind IN('records','search','skill_reads') AND scope=OLD.id;
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('timeline',OLD.session,OLD.id) ON CONFLICT DO NOTHING;END;

CREATE TABLE usage_samples(
 id TEXT PRIMARY KEY REFERENCES records(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 basis TEXT NOT NULL,group_key TEXT NOT NULL,series TEXT NOT NULL,start TEXT NOT NULL,end TEXT NOT NULL,
 time TEXT NOT NULL,sequence INTEGER NOT NULL,values_json TEXT NOT NULL);

CREATE INDEX usage_group_points ON usage_samples(session,origin,basis,group_key,time DESC,sequence DESC,id DESC);

CREATE TABLE usage_streams(id INTEGER PRIMARY KEY,session TEXT NOT NULL,origin TEXT NOT NULL,series TEXT NOT NULL,UNIQUE(session,origin,series));

CREATE TABLE usage_interval_data(id INTEGER PRIMARY KEY,event_id TEXT NOT NULL UNIQUE REFERENCES usage_samples(id) ON DELETE CASCADE,
 stream INTEGER NOT NULL REFERENCES usage_streams(id),start TEXT NOT NULL,end TEXT NOT NULL,
 min_day INTEGER NOT NULL,max_day INTEGER NOT NULL,min_second INTEGER NOT NULL,max_second INTEGER NOT NULL,min_nano INTEGER NOT NULL,max_nano INTEGER NOT NULL);

CREATE INDEX usage_interval_stream ON usage_interval_data(stream);

CREATE VIRTUAL TABLE usage_interval_index USING rtree_i32(id,min_stream,max_stream,min_day,max_day,min_second,max_second,min_nano,max_nano);

CREATE TABLE usage_groups(session TEXT NOT NULL,origin TEXT NOT NULL,basis TEXT NOT NULL,group_key TEXT NOT NULL,values_json TEXT NOT NULL,
 PRIMARY KEY(session,origin,basis,group_key)) WITHOUT ROWID;

CREATE TABLE usage_totals(session TEXT NOT NULL,origin TEXT NOT NULL,basis TEXT NOT NULL,component TEXT NOT NULL,value TEXT NOT NULL,samples INTEGER NOT NULL,
 PRIMARY KEY(session,origin,basis,component)) WITHOUT ROWID;

CREATE TABLE session_usage(session TEXT NOT NULL REFERENCES sessions(session) ON DELETE CASCADE,origin TEXT NOT NULL,summary TEXT NOT NULL,
 PRIMARY KEY(session,origin)) WITHOUT ROWID;

CREATE TABLE event_errors(id TEXT PRIMARY KEY REFERENCES records(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL);

CREATE TABLE timeline_errors(id TEXT PRIMARY KEY REFERENCES logical_events(id) ON DELETE CASCADE,session TEXT NOT NULL);

CREATE TRIGGER usage_point_insert AFTER INSERT ON usage_samples BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('usage',NEW.session,json_array(NEW.origin,NEW.basis,NEW.group_key)) ON CONFLICT DO NOTHING;END;

CREATE TRIGGER usage_point_delete BEFORE DELETE ON usage_samples BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('usage',OLD.session,json_array(OLD.origin,OLD.basis,OLD.group_key)) ON CONFLICT DO NOTHING;END;

CREATE TRIGGER usage_group_insert AFTER INSERT ON usage_groups BEGIN
 INSERT INTO usage_totals SELECT NEW.session,NEW.origin,NEW.basis,key,value,1 FROM json_each(NEW.values_json) WHERE true
 ON CONFLICT(session,origin,basis,component) DO UPDATE SET value=agentlog_add(value,excluded.value),samples=samples+1;END;

CREATE TRIGGER usage_group_delete AFTER DELETE ON usage_groups BEGIN
 UPDATE usage_totals SET value=agentlog_add(value,agentlog_neg((SELECT value FROM json_each(OLD.values_json) WHERE key=component))),samples=samples-1
 WHERE session=OLD.session AND origin=OLD.origin AND basis=OLD.basis AND component IN (SELECT key FROM json_each(OLD.values_json));
 DELETE FROM usage_totals WHERE session=OLD.session AND origin=OLD.origin AND basis=OLD.basis AND samples=0;END;

CREATE TRIGGER usage_total_insert AFTER INSERT ON usage_totals BEGIN INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('usage_sessions',NEW.session,'') ON CONFLICT DO NOTHING;END;

CREATE TRIGGER usage_total_update AFTER UPDATE ON usage_totals BEGIN INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('usage_sessions',NEW.session,'') ON CONFLICT DO NOTHING;END;

CREATE TRIGGER usage_total_delete AFTER DELETE ON usage_totals BEGIN INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('usage_sessions',OLD.session,'') ON CONFLICT DO NOTHING;END;

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

CREATE INDEX cards_agent_candidates ON record_facts(id) WHERE source='copilot' OR json_extract(detail,'$.kind') IN ('tool','agent');

CREATE TABLE agent_calls(event_id TEXT PRIMARY KEY REFERENCES records(id) ON DELETE CASCADE,
 session TEXT NOT NULL,owner TEXT NOT NULL,call_id TEXT NOT NULL,launch INTEGER NOT NULL,targets TEXT NOT NULL);

CREATE INDEX agent_call_lookup ON agent_calls(session,owner,call_id);

CREATE TABLE agent_activity(event_id TEXT PRIMARY KEY REFERENCES records(id) ON DELETE CASCADE,
 session TEXT NOT NULL,agent TEXT NOT NULL);

CREATE INDEX agent_activity_lookup ON agent_activity(session,agent,event_id);

CREATE TRIGGER agent_index_update AFTER UPDATE ON records BEGIN
 DELETE FROM agent_calls WHERE event_id=OLD.id;
 DELETE FROM agent_activity WHERE event_id=OLD.id; END;

CREATE TRIGGER usage_interval_insert AFTER INSERT ON usage_interval_data BEGIN
INSERT INTO usage_interval_index VALUES(NEW.id,NEW.stream,NEW.stream,NEW.min_day,NEW.max_day,NEW.min_second,NEW.max_second,NEW.min_nano,NEW.max_nano);
INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'usage',p.session,json_array(p.origin,p.basis,p.group_key) FROM usage_interval_index i CROSS JOIN usage_samples p ON p.rowid=i.id
 WHERE i.min_stream<=NEW.stream AND i.max_stream>=NEW.stream AND i.min_day<=NEW.max_day AND i.max_day>=NEW.min_day
 AND i.min_second<=NEW.max_second AND i.max_second>=NEW.min_second AND i.min_nano<=NEW.max_nano AND i.max_nano>=NEW.min_nano
 AND p.start<NEW.end AND p.end>NEW.start ON CONFLICT DO NOTHING;
END;

CREATE TRIGGER usage_interval_delete BEFORE DELETE ON usage_interval_data BEGIN
INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'usage',p.session,json_array(p.origin,p.basis,p.group_key) FROM usage_interval_index i CROSS JOIN usage_samples p ON p.rowid=i.id
 WHERE i.min_stream<=OLD.stream AND i.max_stream>=OLD.stream AND i.min_day<=OLD.max_day AND i.max_day>=OLD.min_day
 AND i.min_second<=OLD.max_second AND i.max_second>=OLD.min_second AND i.min_nano<=OLD.max_nano AND i.max_nano>=OLD.min_nano
 AND p.start<OLD.end AND p.end>OLD.start ON CONFLICT DO NOTHING;
DELETE FROM usage_interval_index WHERE id=OLD.id;
END;

CREATE TRIGGER usage_interval_stream_delete AFTER DELETE ON usage_interval_data BEGIN
 DELETE FROM usage_streams WHERE id=OLD.stream AND NOT EXISTS(SELECT 1 FROM usage_interval_data WHERE stream=OLD.stream);END;

CREATE TRIGGER trace_work_insert AFTER INSERT ON records WHEN NEW.trace<>'' BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('trace',NEW.source,NEW.trace) ON CONFLICT DO NOTHING;END;

CREATE TRIGGER trace_work_delete AFTER DELETE ON records WHEN OLD.trace<>'' BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('trace',OLD.source,OLD.trace) ON CONFLICT DO NOTHING;END;

CREATE TRIGGER trace_work_update AFTER UPDATE ON records
 WHEN OLD.source<>NEW.source OR OLD.trace<>NEW.trace OR OLD.session<>NEW.session OR OLD.span<>NEW.span OR OLD.parent_span<>NEW.parent_span BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'trace',OLD.source,OLD.trace WHERE OLD.trace<>'' ON CONFLICT DO NOTHING;
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'trace',NEW.source,NEW.trace WHERE NEW.trace<>'' ON CONFLICT DO NOTHING;END;

CREATE INDEX cards_prompt_title ON record_facts(session,time,sequence,id) WHERE prompt_title<>'';

CREATE INDEX cards_origin_prompt_title ON record_facts(session,origin,time,sequence,id) WHERE prompt_title<>'';

CREATE INDEX cards_title_backfill ON record_facts(id) WHERE is_user=1;

CREATE TABLE session_membership(
 session TEXT PRIMARY KEY,path TEXT NOT NULL,source TEXT NOT NULL,
 diagnostic INTEGER NOT NULL,end TEXT NOT NULL,usage TEXT NOT NULL, project TEXT, child INTEGER NOT NULL DEFAULT 0);

CREATE INDEX folder_members_recent ON session_membership(path,source,diagnostic,end DESC,session);

CREATE TABLE directory_summaries(
 path TEXT NOT NULL,source TEXT NOT NULL,diagnostic INTEGER NOT NULL,
 count INTEGER NOT NULL,end TEXT NOT NULL,totals TEXT NOT NULL,
 PRIMARY KEY(path,source,diagnostic)) WITHOUT ROWID;

CREATE TRIGGER folder_session_insert AFTER INSERT ON sessions BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('projects',NEW.session,'') ON CONFLICT DO NOTHING; END;

CREATE TRIGGER folder_session_update AFTER UPDATE ON sessions BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('projects',OLD.session,'') ON CONFLICT DO NOTHING;
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('projects',NEW.session,'') ON CONFLICT DO NOTHING; END;

CREATE TRIGGER folder_session_delete AFTER DELETE ON sessions BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('projects',OLD.session,'') ON CONFLICT DO NOTHING; END;

CREATE TRIGGER folder_usage_insert AFTER INSERT ON session_usage WHEN NEW.origin='' BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('projects',NEW.session,'') ON CONFLICT DO NOTHING; END;

CREATE TRIGGER folder_usage_update AFTER UPDATE ON session_usage BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'projects',OLD.session,'' WHERE OLD.origin='' ON CONFLICT DO NOTHING;
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'projects',NEW.session,'' WHERE NEW.origin='' ON CONFLICT DO NOTHING; END;

CREATE TRIGGER folder_usage_delete AFTER DELETE ON session_usage WHEN OLD.origin='' BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('projects',OLD.session,'') ON CONFLICT DO NOTHING; END;

CREATE TRIGGER folder_lock_insert AFTER INSERT ON archive_locks BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('projects',NEW.session,'') ON CONFLICT DO NOTHING; END;

CREATE TRIGGER folder_lock_delete AFTER DELETE ON archive_locks BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('projects',OLD.session,'') ON CONFLICT DO NOTHING; END;

CREATE INDEX cards_presentation_pending ON record_facts(id) WHERE presentation_ready=0;

CREATE INDEX cards_skill_page ON record_facts(session,origin,time,sequence,id) WHERE has_skill=1;

CREATE INDEX cards_conversation_page ON record_facts(session,origin,time,sequence,id) WHERE conversation=1;

CREATE INDEX timeline_skill_page ON logical_events(session,time,sequence,id) WHERE has_skill=1;

CREATE INDEX timeline_conversation_page ON logical_events(session,time,sequence,id) WHERE conversation=1;

CREATE INDEX cards_activity_page ON record_facts(session,origin,activity,time,sequence,id);

CREATE INDEX cards_kind_page ON record_facts(session,origin,kind,time,sequence,id);

CREATE INDEX cards_error_page ON record_facts(session,origin,time,sequence,id) WHERE is_error=1;

CREATE INDEX cards_turn_answer ON record_facts(session,turn,kind,origin,time DESC,sequence DESC,id DESC);

CREATE INDEX timeline_activity_page ON logical_events(session,activity,time,sequence,id);

CREATE INDEX timeline_kind_page ON logical_events(session,kind,time,sequence,id);

CREATE INDEX timeline_error_page ON logical_events(session,time,sequence,id) WHERE is_error=1;

CREATE TRIGGER presentation_card_insert AFTER INSERT ON record_facts BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('presentation',NEW.id,'') ON CONFLICT DO NOTHING; END;

CREATE TRIGGER presentation_card_update AFTER UPDATE ON record_facts
 WHEN OLD.activity IS NOT NEW.activity OR OLD.kind IS NOT NEW.kind
 OR OLD.is_error IS NOT NEW.is_error OR OLD.has_skill IS NOT NEW.has_skill
 OR OLD.conversation IS NOT NEW.conversation BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('presentation',NEW.id,'') ON CONFLICT DO NOTHING;
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'presentation',id,'' FROM logical_events WHERE otel_id=NEW.id AND import_id<>'' AND otel_id<>'' ON CONFLICT DO NOTHING; END;

CREATE TRIGGER presentation_timeline_insert AFTER INSERT ON logical_events BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('presentation',NEW.id,'') ON CONFLICT DO NOTHING; END;

CREATE TABLE model_facts(
 id TEXT PRIMARY KEY REFERENCES records(id) ON DELETE CASCADE,
 session TEXT NOT NULL,source TEXT NOT NULL,origin TEXT NOT NULL,trace TEXT NOT NULL,
 span TEXT NOT NULL,parent_span TEXT NOT NULL,turn TEXT NOT NULL,role TEXT NOT NULL,
 root TEXT NOT NULL,time TEXT NOT NULL,info TEXT NOT NULL);

CREATE INDEX model_fact_root ON model_facts(root,time,id);

CREATE TRIGGER model_fact_insert AFTER INSERT ON model_facts BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('models',NEW.id,'') ON CONFLICT DO NOTHING; END;

CREATE TRIGGER model_fact_update AFTER UPDATE ON model_facts BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('models',NEW.id,'') ON CONFLICT DO NOTHING;
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'models',OLD.root,'' WHERE OLD.root<>'' ON CONFLICT DO NOTHING; END;

CREATE TRIGGER model_fact_delete BEFORE DELETE ON model_facts BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'models',OLD.root,'' WHERE OLD.root<>'' AND OLD.root<>OLD.id ON CONFLICT DO NOTHING;
 UPDATE model_facts SET root='' WHERE root=OLD.id AND id<>OLD.id; END;

CREATE TABLE model_calls(
 id TEXT PRIMARY KEY REFERENCES model_facts(id) ON DELETE CASCADE,
 session TEXT NOT NULL,origin TEXT NOT NULL,turn TEXT NOT NULL,time TEXT NOT NULL,info TEXT NOT NULL);

CREATE INDEX model_calls_turn ON model_calls(session,turn,origin,time,id);

CREATE INDEX project_members_recent ON session_membership(project,source,diagnostic,end DESC,session);

CREATE TABLE project_summaries(
 project TEXT NOT NULL,source TEXT NOT NULL,diagnostic INTEGER NOT NULL,
 count INTEGER NOT NULL,end TEXT NOT NULL, root_count INTEGER NOT NULL DEFAULT 0, root_end TEXT NOT NULL DEFAULT '',PRIMARY KEY(project,source,diagnostic)) WITHOUT ROWID;

CREATE TABLE record_totals(session TEXT PRIMARY KEY,records INTEGER NOT NULL,raw_bytes INTEGER NOT NULL) WITHOUT ROWID;

CREATE TRIGGER archive_stats_insert AFTER INSERT ON records BEGIN
 INSERT INTO record_totals VALUES(NEW.session,1,length(NEW.raw))
 ON CONFLICT(session) DO UPDATE SET records=records+1,raw_bytes=raw_bytes+excluded.raw_bytes; END;

CREATE TRIGGER archive_stats_delete AFTER DELETE ON records BEGIN
 UPDATE record_totals SET records=records-1,raw_bytes=raw_bytes-length(OLD.raw) WHERE session=OLD.session;
 DELETE FROM record_totals WHERE session=OLD.session AND records=0; END;

CREATE TRIGGER archive_stats_update AFTER UPDATE OF session,raw ON records
 WHEN OLD.session<>NEW.session OR length(OLD.raw)<>length(NEW.raw) BEGIN
 UPDATE record_totals SET records=records-1,raw_bytes=raw_bytes-length(OLD.raw) WHERE session=OLD.session;
 DELETE FROM record_totals WHERE session=OLD.session AND records=0;
 INSERT INTO record_totals VALUES(NEW.session,1,length(NEW.raw))
 ON CONFLICT(session) DO UPDATE SET records=records+1,raw_bytes=raw_bytes+excluded.raw_bytes; END;

CREATE INDEX catalog_archive_candidates ON sessions(end,archive_start,session);

CREATE INDEX archives_latest ON archive_jobs(created_at DESC,id DESC);

CREATE INDEX archive_snapshot_folder ON archive_sessions(archive_id,folder,last_activity,session);

CREATE INDEX model_fact_trace ON model_facts(source,trace,role,span);

CREATE TRIGGER model_trace_insert AFTER INSERT ON records
 WHEN NEW.signal='traces' AND NEW.trace<>'' BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('model_trace',NEW.source,NEW.trace) ON CONFLICT DO NOTHING;
END;

CREATE TRIGGER model_trace_update AFTER UPDATE OF session,source,origin,signal,trace,span,parent_span ON records
 WHEN OLD.session IS NOT NEW.session OR OLD.source IS NOT NEW.source
 OR OLD.origin IS NOT NEW.origin OR OLD.signal IS NOT NEW.signal
 OR OLD.trace IS NOT NEW.trace OR OLD.span IS NOT NEW.span
 OR OLD.parent_span IS NOT NEW.parent_span BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'model_trace',OLD.source,OLD.trace WHERE OLD.signal='traces' AND OLD.trace<>'' ON CONFLICT DO NOTHING;
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'model_trace',NEW.source,NEW.trace WHERE NEW.signal='traces' AND NEW.trace<>'' ON CONFLICT DO NOTHING;
END;

CREATE TRIGGER model_trace_delete AFTER DELETE ON records
 WHEN OLD.signal='traces' AND OLD.trace<>'' BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('model_trace',OLD.source,OLD.trace) ON CONFLICT DO NOTHING;
END;

CREATE TRIGGER model_request_insert AFTER INSERT ON model_facts WHEN NEW.role='request' BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('model_trace',NEW.source,NEW.trace) ON CONFLICT DO NOTHING;
END;

CREATE TRIGGER model_request_update AFTER UPDATE OF session,source,origin,trace,span,role ON model_facts
 WHEN (OLD.role='request' OR NEW.role='request')
 AND (OLD.session IS NOT NEW.session OR OLD.source IS NOT NEW.source
 OR OLD.origin IS NOT NEW.origin OR OLD.trace IS NOT NEW.trace
 OR OLD.span IS NOT NEW.span OR OLD.role IS NOT NEW.role) BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('model_trace',OLD.source,OLD.trace) ON CONFLICT DO NOTHING;
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('model_trace',NEW.source,NEW.trace) ON CONFLICT DO NOTHING;
END;

CREATE TRIGGER model_request_delete AFTER DELETE ON model_facts WHEN OLD.role='request' BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('model_trace',OLD.source,OLD.trace) ON CONFLICT DO NOTHING;
END;

CREATE TABLE session_evidence(
 event_id TEXT PRIMARY KEY REFERENCES records(id) ON DELETE CASCADE,
 session TEXT NOT NULL,ephemeral INTEGER NOT NULL,persistent INTEGER NOT NULL,temporary INTEGER NOT NULL, automatic INTEGER NOT NULL DEFAULT 0, human INTEGER NOT NULL DEFAULT 0);

CREATE TRIGGER session_fact_insert AFTER INSERT ON session_evidence BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('visibility',NEW.session,'') ON CONFLICT DO NOTHING; END;

CREATE TRIGGER session_fact_update AFTER UPDATE ON session_evidence BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('visibility',OLD.session,'') ON CONFLICT DO NOTHING;
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('visibility',NEW.session,'') ON CONFLICT DO NOTHING; END;

CREATE TRIGGER session_fact_delete AFTER DELETE ON session_evidence BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('visibility',OLD.session,'') ON CONFLICT DO NOTHING; END;

CREATE TRIGGER session_visibility_insert AFTER INSERT ON sessions BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) VALUES('visibility',NEW.session,'') ON CONFLICT DO NOTHING; END;

CREATE INDEX codex_session_metadata ON session_evidence(session)
 WHERE ephemeral<>0 OR persistent<>0 OR temporary<>0;

CREATE INDEX codex_session_automatic ON session_evidence(session) WHERE automatic<>0;

CREATE INDEX codex_session_human ON session_evidence(session) WHERE human<>0;

CREATE TABLE search_identifiers(
 event_id TEXT NOT NULL REFERENCES records(id) ON DELETE CASCADE,
 session TEXT NOT NULL,key TEXT NOT NULL,value TEXT NOT NULL,
 PRIMARY KEY(event_id,key,value)) WITHOUT ROWID;

CREATE INDEX search_identifiers_value ON search_identifiers(value,key,session);

CREATE INDEX catalog_project_recent ON sessions(project_key,end DESC,session);

CREATE INDEX project_members_roots_recent ON session_membership(project,source,diagnostic,end DESC,session) WHERE child=0;

CREATE TABLE "skill_events"(event_id TEXT NOT NULL REFERENCES records(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 name TEXT NOT NULL,plugin TEXT NOT NULL,scope TEXT NOT NULL,mode TEXT NOT NULL,evidence TEXT NOT NULL,group_key TEXT NOT NULL,time TEXT NOT NULL,path TEXT NOT NULL,search TEXT NOT NULL,
 PRIMARY KEY(event_id,name,plugin,evidence,group_key));

CREATE INDEX skill_sessions ON skill_events(name,plugin,session,evidence,origin);

CREATE INDEX skills_by_session ON skill_events(session,name,plugin,time);

CREATE TRIGGER skill_event_update AFTER UPDATE ON records BEGIN DELETE FROM skill_events WHERE event_id=OLD.id; END;

CREATE INDEX timeline_page ON logical_events(session,time,sequence,id,conversation,activity,is_error);

CREATE INDEX cards_page ON record_facts(session,origin,time,sequence,id,conversation,activity,is_error);

CREATE TABLE skill_read_inputs(
 event_id TEXT PRIMARY KEY REFERENCES records(id) ON DELETE CASCADE,
 session TEXT NOT NULL,source TEXT NOT NULL,start_key TEXT NOT NULL,result_key TEXT NOT NULL,
 facts BLOB NOT NULL);

CREATE INDEX skill_read_start ON skill_read_inputs(session,source,start_key);

CREATE INDEX skill_read_result ON skill_read_inputs(session,source,result_key);

CREATE TRIGGER skill_read_key_added AFTER INSERT ON record_keys WHEN NEW.namespace='tool.call' BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'skill_reads',r.event_id,'' FROM records e CROSS JOIN skill_read_inputs r
 WHERE e.id=NEW.event_id AND r.session=e.session AND r.source=e.source
 AND r.start_key=NEW.key
 UNION ALL
 SELECT 'skill_reads',r.event_id,'' FROM records e CROSS JOIN skill_read_inputs r
 WHERE e.id=NEW.event_id AND r.session=e.session AND r.source=e.source AND r.result_key=NEW.key;
END;

CREATE TRIGGER skill_read_key_removed BEFORE DELETE ON record_keys WHEN OLD.namespace='tool.call' BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'skill_reads',r.event_id,'' FROM records e CROSS JOIN skill_read_inputs r
 WHERE e.id=OLD.event_id AND r.session=e.session AND r.source=e.source
 AND r.start_key=OLD.key
 UNION ALL
 SELECT 'skill_reads',r.event_id,'' FROM records e CROSS JOIN skill_read_inputs r
 WHERE e.id=OLD.event_id AND r.session=e.session AND r.source=e.source AND r.result_key=OLD.key;
END;

CREATE TRIGGER skill_read_event_removed BEFORE DELETE ON records BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'skill_reads',r.event_id,'' FROM record_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.start_key=k.key
 UNION ALL
 SELECT 'skill_reads',r.event_id,'' FROM record_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.result_key=k.key;
END;

CREATE TRIGGER skill_read_event_changing BEFORE UPDATE ON records BEGIN
 INSERT OR IGNORE INTO work_items(kind,scope,key) SELECT 'skill_reads',r.event_id,'' FROM record_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.start_key=k.key
 UNION ALL
 SELECT 'skill_reads',r.event_id,'' FROM record_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.result_key=k.key;
END;

CREATE TRIGGER skill_read_input_changed AFTER UPDATE ON records BEGIN
 DELETE FROM skill_read_inputs WHERE event_id=OLD.id;
END;

CREATE TRIGGER skill_read_input_removed AFTER DELETE ON skill_read_inputs BEGIN
 DELETE FROM work_items WHERE kind='skill_reads' AND (scope,key) IN(SELECT event_id,'' FROM pending_skill_reads WHERE event_id=OLD.event_id);
END;

CREATE INDEX skill_evidence_groups ON skill_events(group_key,evidence);

CREATE INDEX catalog_roots_recent ON sessions(end DESC,session,source)
 WHERE internal=0 AND length(parent)=0;

CREATE TABLE processing_buckets(
 session TEXT NOT NULL,origin TEXT NOT NULL,minute TEXT NOT NULL,
 count INTEGER NOT NULL,activity INTEGER NOT NULL,errors INTEGER NOT NULL,
 PRIMARY KEY(session,origin,minute)) WITHOUT ROWID;

CREATE TRIGGER processing_cards_insert AFTER INSERT ON record_facts
 WHEN NEW.conversation=0 BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,NEW.origin,substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;

CREATE TRIGGER processing_cards_delete AFTER DELETE ON record_facts
 WHEN OLD.conversation=0 BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16) AND count=0;
END;

CREATE TRIGGER processing_cards_update_remove AFTER UPDATE ON record_facts
 WHEN OLD.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.origin IS NOT NEW.origin OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin=OLD.origin AND minute=substr(OLD.time,1,16) AND count=0;
END;

CREATE TRIGGER processing_cards_update_add AFTER UPDATE ON record_facts
 WHEN NEW.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.origin IS NOT NEW.origin OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,NEW.origin,substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;

CREATE TRIGGER processing_timeline_insert AFTER INSERT ON logical_events
 WHEN NEW.conversation=0 BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,'',substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;

CREATE TRIGGER processing_timeline_delete AFTER DELETE ON logical_events
 WHEN OLD.conversation=0 BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16) AND count=0;
END;

CREATE TRIGGER processing_timeline_update_remove AFTER UPDATE ON logical_events
 WHEN OLD.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) BEGIN
 UPDATE processing_buckets SET count=count-1,activity=activity-OLD.activity,errors=errors-OLD.is_error
 WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16);
 DELETE FROM processing_buckets WHERE session=OLD.session AND origin='' AND minute=substr(OLD.time,1,16) AND count=0;
END;

CREATE TRIGGER processing_timeline_update_add AFTER UPDATE ON logical_events
 WHEN NEW.conversation=0 AND (OLD.session IS NOT NEW.session OR OLD.time IS NOT NEW.time OR OLD.conversation IS NOT NEW.conversation OR OLD.activity IS NOT NEW.activity OR OLD.is_error IS NOT NEW.is_error) BEGIN
 INSERT INTO processing_buckets VALUES(NEW.session,'',substr(NEW.time,1,16),1,NEW.activity,NEW.is_error)
 ON CONFLICT(session,origin,minute) DO UPDATE SET count=count+1,activity=activity+excluded.activity,errors=errors+excluded.errors;
END;

CREATE TABLE api_keys(
 id TEXT PRIMARY KEY,name TEXT NOT NULL,key TEXT NOT NULL,
 key_hash BLOB NOT NULL UNIQUE,created_at TEXT NOT NULL);
