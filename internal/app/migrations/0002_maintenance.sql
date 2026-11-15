-- Adopt resumable jobs from the pre-migration releases. user_version was only
-- the old folder-index flag, never a database schema version.
CREATE TABLE maintenance_jobs(
 name TEXT PRIMARY KEY,cursor TEXT NOT NULL DEFAULT '',complete INTEGER NOT NULL DEFAULT 0 CHECK(complete IN(0,1)),
 processed INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '');
INSERT INTO maintenance_jobs(name,cursor,complete) SELECT 'usage.v1',cursor,complete FROM usage_backfill;
INSERT INTO maintenance_jobs(name,cursor,complete) SELECT 'agents.v1',cursor,complete FROM agent_index_progress;
INSERT INTO maintenance_jobs(name,cursor,complete) SELECT 'skills.v1',cursor,complete FROM skill_index_progress;
INSERT INTO maintenance_jobs(name,complete) SELECT 'directories.v1',CASE WHEN user_version>=1 THEN 1 ELSE 0 END FROM pragma_user_version;
DROP TABLE usage_backfill;
DROP TABLE agent_index_progress;
DROP TABLE skill_index_progress;

-- Missing derived rows are discovered once during adoption. Subsequent writes
-- maintain durable dirty queues in the same transaction as their original.
INSERT INTO cards_dirty(id) SELECT e.id FROM events e LEFT JOIN event_cards c ON c.id=e.id WHERE c.id IS NULL ON CONFLICT DO NOTHING;
INSERT INTO read_model_dirty(session,id) SELECT e.session,e.id FROM events e JOIN cards_dirty d ON d.id=e.id ON CONFLICT DO NOTHING;
INSERT INTO search_dirty(event_id) SELECT e.id FROM events e LEFT JOIN search_documents d ON d.event_id=e.id WHERE d.event_id IS NULL ON CONFLICT DO NOTHING;
