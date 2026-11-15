-- Track only changed trace identities. Unrelated ingestion and duplicate
-- replays must not revisit every unresolved internal span in the database.
CREATE TABLE trace_dirty(source TEXT NOT NULL,trace TEXT NOT NULL,PRIMARY KEY(source,trace)) WITHOUT ROWID;
CREATE TRIGGER trace_work_insert AFTER INSERT ON events WHEN NEW.trace<>'' BEGIN
 INSERT INTO trace_dirty(source,trace) VALUES(NEW.source,NEW.trace) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER trace_work_delete AFTER DELETE ON events WHEN OLD.trace<>'' BEGIN
 INSERT INTO trace_dirty(source,trace) VALUES(OLD.source,OLD.trace) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER trace_work_update AFTER UPDATE ON events
 WHEN OLD.source<>NEW.source OR OLD.trace<>NEW.trace OR OLD.session<>NEW.session OR OLD.span<>NEW.span OR OLD.parent_span<>NEW.parent_span BEGIN
 INSERT INTO trace_dirty(source,trace) SELECT OLD.source,OLD.trace WHERE OLD.trace<>'' ON CONFLICT DO NOTHING;
 INSERT INTO trace_dirty(source,trace) SELECT NEW.source,NEW.trace WHERE NEW.trace<>'' ON CONFLICT DO NOTHING;END;

INSERT INTO trace_dirty(source,trace)
 SELECT DISTINCT e.source,e.trace FROM events e WHERE e.trace<>'' AND e.session GLOB 'trace:*'
 AND EXISTS(SELECT 1 FROM events p WHERE p.source=e.source AND p.trace=e.trace
 AND p.session NOT LIKE 'trace:%' AND p.session NOT LIKE 'unassigned:%');
