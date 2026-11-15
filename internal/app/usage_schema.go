package app

// Points retain only normalized measurements. Groups collapse repeated reports;
// exact decimal delta updates keep reads independent of history length.
const usageSchema = `
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
`
