-- A span may report multiple invocations of the same skill. Preserve each
-- invocation group without copying or changing any original event payload.
DROP TRIGGER skill_event_update;
CREATE TABLE skill_events_next(event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL,origin TEXT NOT NULL,
 name TEXT NOT NULL,plugin TEXT NOT NULL,scope TEXT NOT NULL,mode TEXT NOT NULL,evidence TEXT NOT NULL,group_key TEXT NOT NULL,time TEXT NOT NULL,path TEXT NOT NULL,search TEXT NOT NULL,
 PRIMARY KEY(event_id,name,plugin,evidence,group_key));
INSERT INTO skill_events_next SELECT * FROM skill_events;
DROP TABLE skill_events;
ALTER TABLE skill_events_next RENAME TO skill_events;
CREATE INDEX skill_sessions ON skill_events(name,plugin,session,evidence,origin);
CREATE INDEX skills_by_session ON skill_events(session,name,plugin,time);
CREATE TRIGGER skill_event_update AFTER UPDATE ON events BEGIN DELETE FROM skill_events WHERE event_id=OLD.id; END;
INSERT INTO maintenance_jobs(name) VALUES('providers.v1');
