-- Re-evaluate resolved as well as unresolved usage when explicit ancestry
-- changes. Coalesce a batch's changes by trace instead of re-scanning usage
-- after every inserted span. Normal reads never traverse original histories.
CREATE TABLE model_trace_dirty(source TEXT NOT NULL,trace TEXT NOT NULL,
 PRIMARY KEY(source,trace)) WITHOUT ROWID;

-- Pending-only lookups are gone. This prefix supports both usage requeueing
-- and exact request-span lookups while walking ancestry.
DROP INDEX model_fact_pending;
CREATE INDEX model_fact_trace ON model_facts(source,trace,role,span);

DROP TRIGGER model_parent_insert;
CREATE TRIGGER model_trace_insert AFTER INSERT ON events
 WHEN NEW.signal='traces' AND NEW.trace<>'' BEGIN
 INSERT INTO model_trace_dirty VALUES(NEW.source,NEW.trace) ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER model_trace_update AFTER UPDATE OF session,source,origin,signal,trace,span,parent_span ON events BEGIN
 INSERT INTO model_trace_dirty SELECT OLD.source,OLD.trace WHERE OLD.signal='traces' AND OLD.trace<>'' ON CONFLICT DO NOTHING;
 INSERT INTO model_trace_dirty SELECT NEW.source,NEW.trace WHERE NEW.signal='traces' AND NEW.trace<>'' ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER model_trace_delete AFTER DELETE ON events
 WHEN OLD.signal='traces' AND OLD.trace<>'' BEGIN
 INSERT INTO model_trace_dirty VALUES(OLD.source,OLD.trace) ON CONFLICT DO NOTHING;
END;

-- Presentation backfills can change request facts without changing events.
CREATE TRIGGER model_request_insert AFTER INSERT ON model_facts WHEN NEW.role='request' BEGIN
 INSERT INTO model_trace_dirty VALUES(NEW.source,NEW.trace) ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER model_request_update AFTER UPDATE OF session,source,origin,trace,span,role ON model_facts
 WHEN OLD.role='request' OR NEW.role='request' BEGIN
 INSERT INTO model_trace_dirty VALUES(OLD.source,OLD.trace) ON CONFLICT DO NOTHING;
 INSERT INTO model_trace_dirty VALUES(NEW.source,NEW.trace) ON CONFLICT DO NOTHING;
END;
CREATE TRIGGER model_request_delete AFTER DELETE ON model_facts WHEN OLD.role='request' BEGIN
 INSERT INTO model_trace_dirty VALUES(OLD.source,OLD.trace) ON CONFLICT DO NOTHING;
END;

-- Discover historical repairs once. Subsequent startups drain only queued work.
INSERT INTO model_trace_dirty SELECT DISTINCT source,trace FROM model_facts;
