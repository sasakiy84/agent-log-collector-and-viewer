-- Classification uses explicit metadata from multiple events, never prompts
-- or model names. Historical extraction runs as checkpointed background work.
ALTER TABLE session_catalog ADD COLUMN internal INTEGER NOT NULL DEFAULT 0;
CREATE TABLE codex_session_facts(
 event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,ephemeral INTEGER NOT NULL,persistent INTEGER NOT NULL,temporary INTEGER NOT NULL);
CREATE INDEX codex_session_facts_session ON codex_session_facts(session);
CREATE TABLE session_visibility_dirty(session TEXT PRIMARY KEY) WITHOUT ROWID;
CREATE TRIGGER session_fact_insert AFTER INSERT ON codex_session_facts BEGIN
 INSERT INTO session_visibility_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER session_fact_update AFTER UPDATE ON codex_session_facts BEGIN
 INSERT INTO session_visibility_dirty VALUES(OLD.session) ON CONFLICT DO NOTHING;
 INSERT INTO session_visibility_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER session_fact_delete AFTER DELETE ON codex_session_facts BEGIN
 INSERT INTO session_visibility_dirty VALUES(OLD.session) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER session_visibility_insert AFTER INSERT ON session_catalog BEGIN
 INSERT INTO session_visibility_dirty VALUES(NEW.session) ON CONFLICT DO NOTHING; END;
INSERT INTO maintenance_jobs(name) VALUES('codex_visibility.v1');
