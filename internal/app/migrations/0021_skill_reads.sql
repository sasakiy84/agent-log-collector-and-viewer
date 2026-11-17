-- Reference sessions are no longer inferred invocations. Keep legacy evidence
-- visible as unconfirmed until the resumable raw-history decoder revisits it.
UPDATE skill_events SET evidence='pending' WHERE evidence='reference';
CREATE TABLE skill_read_inputs(
 event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,source TEXT NOT NULL,start_key TEXT NOT NULL,result_key TEXT NOT NULL,
 facts BLOB NOT NULL);
CREATE INDEX skill_read_start ON skill_read_inputs(session,source,start_key);
CREATE INDEX skill_read_result ON skill_read_inputs(session,source,result_key);
CREATE TABLE skill_reads_dirty(event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE) WITHOUT ROWID;
-- Separate equality lookups keep all three index columns usable; an OR can
-- degrade to scanning every skill read in a long session.
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
INSERT INTO maintenance_jobs(name) VALUES('skills.reads.v2');

CREATE TRIGGER skill_read_input_removed AFTER DELETE ON skill_read_inputs BEGIN
 DELETE FROM skill_reads_dirty WHERE event_id=OLD.event_id;
END;
CREATE INDEX skill_evidence_groups ON skill_events(group_key,evidence);
