-- Explicit UPSERT handling remains effective inside an outer UPSERT update.
-- INSERT OR IGNORE inherits the outer conflict policy and can abort ingestion.

DROP TRIGGER skill_read_key_added;

CREATE TRIGGER skill_read_key_added AFTER INSERT ON record_keys WHEN NEW.namespace='tool.call' BEGIN
 INSERT INTO work_items(kind,scope,key) SELECT 'skill_reads',r.event_id,'' FROM records e CROSS JOIN skill_read_inputs r
 WHERE e.id=NEW.event_id AND r.session=e.session AND r.source=e.source
 AND r.start_key=NEW.key
 UNION ALL
 SELECT 'skill_reads',r.event_id,'' FROM records e CROSS JOIN skill_read_inputs r
 WHERE e.id=NEW.event_id AND r.session=e.session AND r.source=e.source AND r.result_key=NEW.key
 ON CONFLICT(kind,scope,key) DO NOTHING;
END;

DROP TRIGGER skill_read_key_removed;

CREATE TRIGGER skill_read_key_removed BEFORE DELETE ON record_keys WHEN OLD.namespace='tool.call' BEGIN
 INSERT INTO work_items(kind,scope,key) SELECT 'skill_reads',r.event_id,'' FROM records e CROSS JOIN skill_read_inputs r
 WHERE e.id=OLD.event_id AND r.session=e.session AND r.source=e.source
 AND r.start_key=OLD.key
 UNION ALL
 SELECT 'skill_reads',r.event_id,'' FROM records e CROSS JOIN skill_read_inputs r
 WHERE e.id=OLD.event_id AND r.session=e.session AND r.source=e.source AND r.result_key=OLD.key
 ON CONFLICT(kind,scope,key) DO NOTHING;
END;

DROP TRIGGER skill_read_event_removed;

CREATE TRIGGER skill_read_event_removed BEFORE DELETE ON records BEGIN
 INSERT INTO work_items(kind,scope,key) SELECT 'skill_reads',r.event_id,'' FROM record_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.start_key=k.key
 UNION ALL
 SELECT 'skill_reads',r.event_id,'' FROM record_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.result_key=k.key
 ON CONFLICT(kind,scope,key) DO NOTHING;
END;

DROP TRIGGER skill_read_event_changing;

CREATE TRIGGER skill_read_event_changing BEFORE UPDATE ON records BEGIN
 INSERT INTO work_items(kind,scope,key) SELECT 'skill_reads',r.event_id,'' FROM record_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.start_key=k.key
 UNION ALL
 SELECT 'skill_reads',r.event_id,'' FROM record_keys k CROSS JOIN skill_read_inputs r
 WHERE k.event_id=OLD.id AND k.namespace='tool.call' AND r.session=OLD.session AND r.source=OLD.source
 AND r.result_key=k.key
 ON CONFLICT(kind,scope,key) DO NOTHING;
END;
