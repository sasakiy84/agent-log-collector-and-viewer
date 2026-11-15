-- Additive folder summaries keep overview reads independent of session count.
CREATE TABLE folder_members(
 session TEXT PRIMARY KEY,path TEXT NOT NULL,source TEXT NOT NULL,
 diagnostic INTEGER NOT NULL,end TEXT NOT NULL,usage TEXT NOT NULL);
CREATE INDEX folder_members_recent ON folder_members(path,source,diagnostic,end DESC,session);
CREATE TABLE folder_totals(
 path TEXT NOT NULL,source TEXT NOT NULL,diagnostic INTEGER NOT NULL,
 count INTEGER NOT NULL,end TEXT NOT NULL,totals TEXT NOT NULL,
 PRIMARY KEY(path,source,diagnostic)) WITHOUT ROWID;
CREATE TABLE folder_dirty(session TEXT PRIMARY KEY) WITHOUT ROWID;
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
INSERT INTO folder_dirty SELECT session FROM session_catalog;
