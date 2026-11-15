CREATE TABLE archive_stats(session TEXT PRIMARY KEY,records INTEGER NOT NULL,raw_bytes INTEGER NOT NULL) WITHOUT ROWID;
CREATE TABLE archive_stats_pending(session TEXT PRIMARY KEY) WITHOUT ROWID;
INSERT INTO archive_stats_pending SELECT DISTINCT session FROM events;
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
-- Logical imported/OTEL pairs can hide an unknown timestamp. Archive eligibility
-- uses the earliest original timestamp from either origin, not the card time.
ALTER TABLE session_catalog ADD COLUMN archive_start TEXT GENERATED ALWAYS AS (
 CASE WHEN import_count=0 THEN otel_start WHEN otel_count=0 THEN import_start
 ELSE MIN(import_start,otel_start) END) VIRTUAL;
CREATE INDEX catalog_archive_candidates ON session_catalog(end,archive_start,session);
CREATE INDEX archives_latest ON archives(created_at DESC,id DESC);
ALTER TABLE archive_sessions ADD COLUMN folder TEXT NOT NULL DEFAULT '';
UPDATE archive_sessions SET folder=COALESCE((SELECT json_extract(preview,'$.cwd') FROM session_catalog WHERE session=archive_sessions.session),'');
CREATE INDEX archive_snapshot_folder ON archive_sessions(archive_id,folder,last_activity,session);
