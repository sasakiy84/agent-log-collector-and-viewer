-- Only skill-related records need execution identities rebuilt. Original payloads
-- and the rest of the prepared search/timeline data remain unchanged.
CREATE TABLE skill_attempt_links(
 event_id TEXT NOT NULL REFERENCES records(id) ON DELETE CASCADE,
 session TEXT NOT NULL,source TEXT NOT NULL,owner TEXT NOT NULL,
 chunk TEXT NOT NULL,wrapper INTEGER NOT NULL,
 PRIMARY KEY(event_id,chunk)) WITHOUT ROWID;
CREATE INDEX skill_attempt_execution ON skill_attempt_links(session,source,owner,chunk,wrapper);
CREATE TRIGGER skill_attempt_changed AFTER UPDATE ON records BEGIN
 DELETE FROM skill_attempt_links WHERE event_id=OLD.id;
END;
INSERT OR IGNORE INTO work_items(kind,scope,key)
 SELECT 'skill_reads',event_id,'' FROM skill_read_inputs;
