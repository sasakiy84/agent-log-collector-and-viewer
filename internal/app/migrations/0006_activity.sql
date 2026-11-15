ALTER TABLE event_cards ADD COLUMN activity INTEGER NOT NULL DEFAULT 1;
ALTER TABLE event_cards ADD COLUMN kind TEXT NOT NULL DEFAULT 'event';
ALTER TABLE event_cards ADD COLUMN is_error INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_cards ADD COLUMN turn TEXT NOT NULL DEFAULT '';
ALTER TABLE timeline_entries ADD COLUMN activity INTEGER NOT NULL DEFAULT 1;
ALTER TABLE timeline_entries ADD COLUMN kind TEXT NOT NULL DEFAULT 'event';
ALTER TABLE timeline_entries ADD COLUMN is_error INTEGER NOT NULL DEFAULT 0;
CREATE INDEX cards_activity_page ON event_cards(session,origin,activity,time,sequence,id);
CREATE INDEX cards_kind_page ON event_cards(session,origin,kind,time,sequence,id);
CREATE INDEX cards_error_page ON event_cards(session,origin,time,sequence,id) WHERE is_error=1;
CREATE INDEX cards_turn_answer ON event_cards(session,turn,kind,origin,time DESC,sequence DESC,id DESC);
CREATE INDEX timeline_activity_page ON timeline_entries(session,activity,time,sequence,id);
CREATE INDEX timeline_kind_page ON timeline_entries(session,kind,time,sequence,id);
CREATE INDEX timeline_error_page ON timeline_entries(session,time,sequence,id) WHERE is_error=1;
CREATE TABLE presentation_dirty(id TEXT PRIMARY KEY) WITHOUT ROWID;
CREATE TRIGGER presentation_card_insert AFTER INSERT ON event_cards BEGIN
 INSERT INTO presentation_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER presentation_card_update AFTER UPDATE ON event_cards BEGIN
 INSERT INTO presentation_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;
 INSERT INTO presentation_dirty SELECT id FROM timeline_entries WHERE otel_id=NEW.id AND import_id<>'' AND otel_id<>'' ON CONFLICT DO NOTHING; END;
CREATE TRIGGER presentation_timeline_insert AFTER INSERT ON timeline_entries BEGIN
 INSERT INTO presentation_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING; END;
INSERT INTO maintenance_jobs(name) VALUES('presentation.v1');

CREATE TABLE model_facts(
 id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,source TEXT NOT NULL,origin TEXT NOT NULL,trace TEXT NOT NULL,
 span TEXT NOT NULL,parent_span TEXT NOT NULL,turn TEXT NOT NULL,role TEXT NOT NULL,
 root TEXT NOT NULL,time TEXT NOT NULL,info TEXT NOT NULL);
CREATE INDEX model_fact_pending ON model_facts(source,trace,root,role);
CREATE INDEX model_fact_root ON model_facts(root,time,id);
CREATE TABLE model_dirty(id TEXT PRIMARY KEY) WITHOUT ROWID;
CREATE TRIGGER model_fact_insert AFTER INSERT ON model_facts BEGIN
 INSERT INTO model_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING; END;
CREATE TRIGGER model_fact_update AFTER UPDATE ON model_facts BEGIN
 INSERT INTO model_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;
 INSERT INTO model_dirty SELECT OLD.root WHERE OLD.root<>'' ON CONFLICT DO NOTHING; END;
CREATE TRIGGER model_fact_delete BEFORE DELETE ON model_facts BEGIN
 INSERT INTO model_dirty SELECT OLD.root WHERE OLD.root<>'' AND OLD.root<>OLD.id ON CONFLICT DO NOTHING;
 UPDATE model_facts SET root='' WHERE root=OLD.id AND id<>OLD.id; END;
CREATE TRIGGER model_parent_insert AFTER INSERT ON events WHEN NEW.trace<>'' BEGIN
 INSERT INTO model_dirty SELECT id FROM model_facts WHERE source=NEW.source AND trace=NEW.trace AND root='' AND role='usage' ON CONFLICT DO NOTHING; END;
CREATE TABLE model_calls(
 id TEXT PRIMARY KEY REFERENCES model_facts(id) ON DELETE CASCADE,
 session TEXT NOT NULL,origin TEXT NOT NULL,turn TEXT NOT NULL,time TEXT NOT NULL,info TEXT NOT NULL);
CREATE INDEX model_calls_turn ON model_calls(session,turn,origin,time,id);
