-- Baseline schema through v0.9.0. Append new migrations; never edit applied SQL.
CREATE TABLE IF NOT EXISTS events(id TEXT PRIMARY KEY, session TEXT NOT NULL, parent TEXT NOT NULL, source TEXT NOT NULL, signal TEXT NOT NULL, time TEXT NOT NULL, trace TEXT NOT NULL, span TEXT NOT NULL, parent_span TEXT NOT NULL, sequence INTEGER NOT NULL DEFAULT 0, origin TEXT NOT NULL, kind TEXT NOT NULL, tool_name TEXT NOT NULL, name TEXT NOT NULL, raw BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS event_keys(event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE, namespace TEXT NOT NULL, key TEXT NOT NULL, PRIMARY KEY(event_id,namespace,key));
 CREATE INDEX IF NOT EXISTS event_keys_key ON event_keys(key,event_id);
 CREATE INDEX IF NOT EXISTS events_session ON events(session,origin,time);
 CREATE INDEX IF NOT EXISTS events_trace ON events(source,trace,span);
 CREATE INDEX IF NOT EXISTS events_pending_trace ON events(source,trace,parent_span) WHERE trace<>'' AND session GLOB 'trace:*';

CREATE TABLE IF NOT EXISTS archived_usage_records(id_hash BLOB NOT NULL,raw_hash BLOB NOT NULL,PRIMARY KEY(id_hash,raw_hash),
 FOREIGN KEY(id_hash,raw_hash) REFERENCES archived_records(id_hash,raw_hash) ON DELETE CASCADE) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS session_revisions(session TEXT PRIMARY KEY, revision INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS archives(
 id TEXT PRIMARY KEY,status TEXT NOT NULL,created_at TEXT NOT NULL,cutoff TEXT NOT NULL,
 session_count INTEGER NOT NULL DEFAULT 0,record_count INTEGER NOT NULL DEFAULT 0,raw_bytes INTEGER NOT NULL DEFAULT 0,
 processed_records INTEGER NOT NULL DEFAULT 0,archive_bytes INTEGER NOT NULL DEFAULT 0,sha256 TEXT NOT NULL DEFAULT '',
 verified_at TEXT NOT NULL DEFAULT '',download_at TEXT NOT NULL DEFAULT '',confirmed_at TEXT NOT NULL DEFAULT '',deleted_records INTEGER NOT NULL DEFAULT 0,
 reclaimed_bytes INTEGER NOT NULL DEFAULT 0,database_bytes_before INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',compaction_error TEXT NOT NULL DEFAULT '',rebuild_required INTEGER NOT NULL DEFAULT 0,rebuild_requested INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_archive ON archives((1)) WHERE status IN ('preparing','ready','deleting','compacting');
CREATE TABLE IF NOT EXISTS archive_sessions(
 archive_id TEXT NOT NULL REFERENCES archives(id),session TEXT NOT NULL,source TEXT NOT NULL,
 revision INTEGER NOT NULL,records INTEGER NOT NULL,raw_bytes INTEGER NOT NULL,last_activity TEXT NOT NULL,
 PRIMARY KEY(archive_id,session)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS archive_locks(session TEXT PRIMARY KEY,archive_id TEXT NOT NULL REFERENCES archives(id));
CREATE TABLE IF NOT EXISTS archived_records(id_hash BLOB NOT NULL,raw_hash BLOB NOT NULL,PRIMARY KEY(id_hash,raw_hash)) WITHOUT ROWID;
CREATE TRIGGER IF NOT EXISTS archive_insert_guard BEFORE INSERT ON events WHEN EXISTS(SELECT 1 FROM archive_locks WHERE session=NEW.session)
 BEGIN SELECT RAISE(ABORT,'session archive deletion is in progress'); END;
CREATE TRIGGER IF NOT EXISTS archive_update_guard BEFORE UPDATE ON events WHEN EXISTS(SELECT 1 FROM archive_locks WHERE session IN (OLD.session,NEW.session))
 BEGIN SELECT RAISE(ABORT,'session archive deletion is in progress'); END;
CREATE TRIGGER IF NOT EXISTS revision_insert AFTER INSERT ON events BEGIN
 INSERT INTO session_revisions VALUES(NEW.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;
CREATE TRIGGER IF NOT EXISTS revision_update AFTER UPDATE ON events BEGIN
 INSERT INTO session_revisions VALUES(OLD.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1;
 INSERT INTO session_revisions SELECT NEW.session,1 WHERE NEW.session<>OLD.session ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;
CREATE TRIGGER IF NOT EXISTS revision_delete AFTER DELETE ON events BEGIN
 INSERT INTO session_revisions VALUES(OLD.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;

CREATE VIRTUAL TABLE IF NOT EXISTS event_search USING fts5(
 text,content='',contentless_delete=1,tokenize='unicode61 remove_diacritics 2',prefix='2 3');
CREATE TABLE IF NOT EXISTS search_documents(
 rowid INTEGER PRIMARY KEY,event_id TEXT NOT NULL UNIQUE REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS search_dirty(event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE);
CREATE TRIGGER IF NOT EXISTS search_document_delete AFTER DELETE ON search_documents BEGIN
 DELETE FROM event_search WHERE rowid=OLD.rowid;END;
CREATE TRIGGER IF NOT EXISTS search_event_insert AFTER INSERT ON events BEGIN
 INSERT INTO search_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS search_event_update AFTER UPDATE ON events
 WHEN OLD.raw IS NOT NEW.raw OR OLD.session<>NEW.session OR OLD.name<>NEW.name OR OLD.source<>NEW.source BEGIN
 INSERT INTO search_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;

CREATE TABLE IF NOT EXISTS event_cards(
 id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 source TEXT NOT NULL,parent TEXT NOT NULL,time TEXT NOT NULL,sequence INTEGER NOT NULL,is_user INTEGER NOT NULL,
 signal TEXT NOT NULL,trace TEXT NOT NULL,span TEXT NOT NULL,parent_span TEXT NOT NULL,detail BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS cards_page ON event_cards(session,origin,time,sequence,id);
CREATE INDEX IF NOT EXISTS cards_preview ON event_cards(session,origin,is_user DESC,time,sequence,id);
CREATE INDEX IF NOT EXISTS cards_directory ON event_cards(session,is_user DESC,time,sequence,id) WHERE json_type(detail,'$.cwd')='text' AND json_extract(detail,'$.cwd')<>'';
CREATE TABLE IF NOT EXISTS timeline_entries(
 id TEXT PRIMARY KEY,session TEXT NOT NULL,source TEXT NOT NULL,parent TEXT NOT NULL,time TEXT NOT NULL,
 end TEXT NOT NULL,sequence INTEGER NOT NULL,is_user INTEGER NOT NULL,import_id TEXT NOT NULL,otel_id TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS timeline_page ON timeline_entries(session,time,sequence,id);
CREATE INDEX IF NOT EXISTS timeline_preview ON timeline_entries(session,is_user DESC,time,sequence,id);
CREATE UNIQUE INDEX IF NOT EXISTS timeline_pair_otel ON timeline_entries(otel_id) WHERE import_id<>'' AND otel_id<>'';
CREATE INDEX IF NOT EXISTS timeline_end ON timeline_entries(session,end DESC);
CREATE TABLE IF NOT EXISTS card_counts(session TEXT NOT NULL,origin TEXT NOT NULL,source TEXT NOT NULL,parent TEXT NOT NULL,count INTEGER NOT NULL,PRIMARY KEY(session,origin,source,parent)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS timeline_counts(session TEXT PRIMARY KEY,count INTEGER NOT NULL);
CREATE TRIGGER IF NOT EXISTS card_count_insert AFTER INSERT ON event_cards BEGIN
 INSERT INTO card_counts VALUES(NEW.session,NEW.origin,NEW.source,NEW.parent,1) ON CONFLICT(session,origin,source,parent) DO UPDATE SET count=count+1;END;
CREATE TRIGGER IF NOT EXISTS card_count_delete AFTER DELETE ON event_cards BEGIN
 UPDATE card_counts SET count=count-1 WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent;
 DELETE FROM card_counts WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent AND count=0;END;
CREATE TRIGGER IF NOT EXISTS card_count_update AFTER UPDATE ON event_cards BEGIN
 UPDATE card_counts SET count=count-1 WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent;
 DELETE FROM card_counts WHERE session=OLD.session AND origin=OLD.origin AND source=OLD.source AND parent=OLD.parent AND count=0;
 INSERT INTO card_counts VALUES(NEW.session,NEW.origin,NEW.source,NEW.parent,1) ON CONFLICT(session,origin,source,parent) DO UPDATE SET count=count+1;END;
CREATE TRIGGER IF NOT EXISTS timeline_count_insert AFTER INSERT ON timeline_entries BEGIN
 INSERT INTO timeline_counts VALUES(NEW.session,1) ON CONFLICT(session) DO UPDATE SET count=count+1;END;
CREATE TRIGGER IF NOT EXISTS timeline_count_delete AFTER DELETE ON timeline_entries BEGIN
 UPDATE timeline_counts SET count=count-1 WHERE session=OLD.session;
 DELETE FROM timeline_counts WHERE session=OLD.session AND count=0;END;
CREATE TABLE IF NOT EXISTS session_catalog(
 session TEXT PRIMARY KEY,source TEXT NOT NULL,parent TEXT NOT NULL,count INTEGER NOT NULL,start TEXT NOT NULL,end TEXT NOT NULL,
 import_count INTEGER NOT NULL,import_start TEXT NOT NULL,import_end TEXT NOT NULL,
 otel_count INTEGER NOT NULL,otel_start TEXT NOT NULL,otel_end TEXT NOT NULL,preview BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS catalog_recent ON session_catalog(end DESC,session);
CREATE INDEX IF NOT EXISTS catalog_source_recent ON session_catalog(source,end DESC,session);
CREATE INDEX IF NOT EXISTS catalog_children ON session_catalog(parent,session);
CREATE INDEX IF NOT EXISTS catalog_directory_recent ON session_catalog(COALESCE(json_extract(preview,'$.cwd'),''),end DESC,session);
CREATE TABLE IF NOT EXISTS read_model_dirty(session TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(session,id)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS cards_dirty(id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE);
CREATE TRIGGER IF NOT EXISTS read_model_neighbors_UPDATE BEFORE UPDATE ON events WHEN EXISTS(SELECT 1 FROM event_keys WHERE event_id=OLD.id) BEGIN
 INSERT INTO read_model_dirty SELECT e.session,e.id FROM event_keys old JOIN event_keys neighbor ON neighbor.key=old.key
 JOIN events e ON e.id=neighbor.event_id WHERE old.event_id=OLD.id AND e.session=OLD.session AND e.source=OLD.source
 ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS read_model_neighbors_DELETE BEFORE DELETE ON events WHEN EXISTS(SELECT 1 FROM event_keys WHERE event_id=OLD.id) BEGIN
 INSERT INTO read_model_dirty SELECT e.session,e.id FROM event_keys old JOIN event_keys neighbor ON neighbor.key=old.key
 JOIN events e ON e.id=neighbor.event_id WHERE old.event_id=OLD.id AND e.session=OLD.session AND e.source=OLD.source
 ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS read_model_insert AFTER INSERT ON events BEGIN
 INSERT INTO read_model_dirty VALUES(NEW.session,NEW.id) ON CONFLICT DO NOTHING;
 INSERT INTO cards_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS read_model_update AFTER UPDATE ON events BEGIN
 INSERT INTO read_model_dirty VALUES(OLD.session,OLD.id) ON CONFLICT DO NOTHING;
 INSERT INTO read_model_dirty VALUES(NEW.session,NEW.id) ON CONFLICT DO NOTHING;
 INSERT INTO cards_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS read_model_delete AFTER DELETE ON events BEGIN
 INSERT INTO read_model_dirty VALUES(OLD.session,OLD.id) ON CONFLICT DO NOTHING;END;

CREATE TABLE IF NOT EXISTS usage_points(
 id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 basis TEXT NOT NULL,group_key TEXT NOT NULL,series TEXT NOT NULL,start TEXT NOT NULL,end TEXT NOT NULL,
 time TEXT NOT NULL,sequence INTEGER NOT NULL,values_json TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS usage_group_points ON usage_points(session,origin,basis,group_key,time DESC,sequence DESC,id DESC);
CREATE TABLE IF NOT EXISTS usage_streams(id INTEGER PRIMARY KEY,session TEXT NOT NULL,origin TEXT NOT NULL,series TEXT NOT NULL,UNIQUE(session,origin,series));
CREATE TABLE IF NOT EXISTS usage_interval_data(id INTEGER PRIMARY KEY,event_id TEXT NOT NULL UNIQUE REFERENCES usage_points(id) ON DELETE CASCADE,
 stream INTEGER NOT NULL REFERENCES usage_streams(id),start TEXT NOT NULL,end TEXT NOT NULL,
 min_day INTEGER NOT NULL,max_day INTEGER NOT NULL,min_second INTEGER NOT NULL,max_second INTEGER NOT NULL,min_nano INTEGER NOT NULL,max_nano INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS usage_interval_stream ON usage_interval_data(stream);
CREATE VIRTUAL TABLE IF NOT EXISTS usage_interval_index USING rtree_i32(id,min_stream,max_stream,min_day,max_day,min_second,max_second,min_nano,max_nano);
CREATE TABLE IF NOT EXISTS usage_dirty(session TEXT NOT NULL,origin TEXT NOT NULL,basis TEXT NOT NULL,group_key TEXT NOT NULL,
 PRIMARY KEY(session,origin,basis,group_key)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS usage_groups(session TEXT NOT NULL,origin TEXT NOT NULL,basis TEXT NOT NULL,group_key TEXT NOT NULL,values_json TEXT NOT NULL,
 PRIMARY KEY(session,origin,basis,group_key)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS usage_totals(session TEXT NOT NULL,origin TEXT NOT NULL,basis TEXT NOT NULL,component TEXT NOT NULL,value TEXT NOT NULL,samples INTEGER NOT NULL,
 PRIMARY KEY(session,origin,basis,component)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS usage_sessions_dirty(session TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS usage_summary(session TEXT NOT NULL REFERENCES session_catalog(session) ON DELETE CASCADE,origin TEXT NOT NULL,summary TEXT NOT NULL,
 PRIMARY KEY(session,origin)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS usage_backfill(id INTEGER PRIMARY KEY CHECK(id=1),cursor TEXT NOT NULL,complete INTEGER NOT NULL);
INSERT OR IGNORE INTO usage_backfill VALUES(1,'',0);
CREATE TABLE IF NOT EXISTS event_errors(id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS timeline_errors(id TEXT PRIMARY KEY REFERENCES timeline_entries(id) ON DELETE CASCADE,session TEXT NOT NULL);
CREATE TRIGGER IF NOT EXISTS usage_point_insert AFTER INSERT ON usage_points BEGIN
 INSERT INTO usage_dirty VALUES(NEW.session,NEW.origin,NEW.basis,NEW.group_key) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS usage_point_delete BEFORE DELETE ON usage_points BEGIN
 INSERT INTO usage_dirty VALUES(OLD.session,OLD.origin,OLD.basis,OLD.group_key) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS usage_group_insert AFTER INSERT ON usage_groups BEGIN
 INSERT INTO usage_totals SELECT NEW.session,NEW.origin,NEW.basis,key,value,1 FROM json_each(NEW.values_json) WHERE true
 ON CONFLICT(session,origin,basis,component) DO UPDATE SET value=agentlog_add(value,excluded.value),samples=samples+1;END;
CREATE TRIGGER IF NOT EXISTS usage_group_delete AFTER DELETE ON usage_groups BEGIN
 UPDATE usage_totals SET value=agentlog_add(value,agentlog_neg((SELECT value FROM json_each(OLD.values_json) WHERE key=component))),samples=samples-1
 WHERE session=OLD.session AND origin=OLD.origin AND basis=OLD.basis AND component IN (SELECT key FROM json_each(OLD.values_json));
 DELETE FROM usage_totals WHERE session=OLD.session AND origin=OLD.origin AND basis=OLD.basis AND samples=0;END;
CREATE TRIGGER IF NOT EXISTS usage_total_insert AFTER INSERT ON usage_totals BEGIN INSERT INTO usage_sessions_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS usage_total_update AFTER UPDATE ON usage_totals BEGIN INSERT INTO usage_sessions_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS usage_total_delete AFTER DELETE ON usage_totals BEGIN INSERT INTO usage_sessions_dirty VALUES(OLD.session) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS usage_event_error_insert AFTER INSERT ON event_errors BEGIN
 INSERT INTO usage_totals VALUES(NEW.session,NEW.origin,'events','errors','1',1)
 ON CONFLICT(session,origin,basis,component) DO UPDATE SET value=agentlog_add(value,'1'),samples=samples+1;END;
CREATE TRIGGER IF NOT EXISTS usage_event_error_delete AFTER DELETE ON event_errors BEGIN
 UPDATE usage_totals SET value=agentlog_add(value,'-1'),samples=samples-1 WHERE session=OLD.session AND origin=OLD.origin AND basis='events' AND component='errors';
 DELETE FROM usage_totals WHERE session=OLD.session AND origin=OLD.origin AND basis='events' AND component='errors' AND samples=0;END;
CREATE TRIGGER IF NOT EXISTS usage_timeline_error_insert AFTER INSERT ON timeline_errors BEGIN
 INSERT INTO usage_totals VALUES(NEW.session,'','events','errors','1',1)
 ON CONFLICT(session,origin,basis,component) DO UPDATE SET value=agentlog_add(value,'1'),samples=samples+1;END;
CREATE TRIGGER IF NOT EXISTS usage_timeline_error_delete AFTER DELETE ON timeline_errors BEGIN
 UPDATE usage_totals SET value=agentlog_add(value,'-1'),samples=samples-1 WHERE session=OLD.session AND origin='' AND basis='events' AND component='errors';
 DELETE FROM usage_totals WHERE session=OLD.session AND origin='' AND basis='events' AND component='errors' AND samples=0;END;

CREATE INDEX IF NOT EXISTS cards_agent_candidates ON event_cards(id) WHERE source='copilot' OR json_extract(detail,'$.kind') IN ('tool','agent');
CREATE TABLE IF NOT EXISTS agent_calls(event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,owner TEXT NOT NULL,call_id TEXT NOT NULL,launch INTEGER NOT NULL,targets TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS agent_call_lookup ON agent_calls(session,owner,call_id);
CREATE TABLE IF NOT EXISTS agent_activity(event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,agent TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS agent_activity_lookup ON agent_activity(session,agent,event_id);
CREATE TRIGGER IF NOT EXISTS agent_index_update AFTER UPDATE ON events BEGIN
 DELETE FROM agent_calls WHERE event_id=OLD.id;
 DELETE FROM agent_activity WHERE event_id=OLD.id; END;
CREATE TABLE IF NOT EXISTS agent_index_progress(id INTEGER PRIMARY KEY CHECK(id=1),cursor TEXT NOT NULL,complete INTEGER NOT NULL);
INSERT INTO agent_index_progress VALUES(1,'',0) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS skill_events(event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 name TEXT NOT NULL,plugin TEXT NOT NULL,scope TEXT NOT NULL,mode TEXT NOT NULL,evidence TEXT NOT NULL,group_key TEXT NOT NULL,time TEXT NOT NULL,path TEXT NOT NULL,search TEXT NOT NULL,
 PRIMARY KEY(event_id,name,plugin,evidence));
CREATE INDEX IF NOT EXISTS skill_sessions ON skill_events(name,plugin,session,evidence,origin);
CREATE INDEX IF NOT EXISTS skills_by_session ON skill_events(session,name,plugin,time);
CREATE TRIGGER IF NOT EXISTS skill_event_update AFTER UPDATE ON events BEGIN DELETE FROM skill_events WHERE event_id=OLD.id; END;
CREATE TABLE IF NOT EXISTS skill_index_progress(id INTEGER PRIMARY KEY CHECK(id=1),cursor TEXT NOT NULL,complete INTEGER NOT NULL);
INSERT INTO skill_index_progress VALUES(1,'',0) ON CONFLICT DO NOTHING;

CREATE TRIGGER IF NOT EXISTS usage_interval_insert AFTER INSERT ON usage_interval_data BEGIN
INSERT INTO usage_interval_index VALUES(NEW.id,NEW.stream,NEW.stream,NEW.min_day,NEW.max_day,NEW.min_second,NEW.max_second,NEW.min_nano,NEW.max_nano);
INSERT INTO usage_dirty SELECT p.session,p.origin,p.basis,p.group_key FROM usage_interval_index i CROSS JOIN usage_points p ON p.rowid=i.id
 WHERE i.min_stream<=NEW.stream AND i.max_stream>=NEW.stream AND i.min_day<=NEW.max_day AND i.max_day>=NEW.min_day
 AND i.min_second<=NEW.max_second AND i.max_second>=NEW.min_second AND i.min_nano<=NEW.max_nano AND i.max_nano>=NEW.min_nano
 AND p.start<NEW.end AND p.end>NEW.start ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER IF NOT EXISTS usage_interval_delete BEFORE DELETE ON usage_interval_data BEGIN
INSERT INTO usage_dirty SELECT p.session,p.origin,p.basis,p.group_key FROM usage_interval_index i CROSS JOIN usage_points p ON p.rowid=i.id
 WHERE i.min_stream<=OLD.stream AND i.max_stream>=OLD.stream AND i.min_day<=OLD.max_day AND i.max_day>=OLD.min_day
 AND i.min_second<=OLD.max_second AND i.max_second>=OLD.min_second AND i.min_nano<=OLD.max_nano AND i.max_nano>=OLD.min_nano
 AND p.start<OLD.end AND p.end>OLD.start ON CONFLICT DO NOTHING;
DELETE FROM usage_interval_index WHERE id=OLD.id;
END;
CREATE TRIGGER IF NOT EXISTS usage_interval_stream_delete AFTER DELETE ON usage_interval_data BEGIN
 DELETE FROM usage_streams WHERE id=OLD.stream AND NOT EXISTS(SELECT 1 FROM usage_interval_data WHERE stream=OLD.stream);END;
