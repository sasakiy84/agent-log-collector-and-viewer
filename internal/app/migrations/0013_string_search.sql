-- Keep exact identifiers separate from the Japanese/English full-text index.
CREATE TABLE search_identifiers(
 event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
 session TEXT NOT NULL,key TEXT NOT NULL,value TEXT NOT NULL,
 PRIMARY KEY(event_id,key,value)) WITHOUT ROWID;
CREATE INDEX search_identifiers_value ON search_identifiers(value,key,session);
-- Historical extraction runs after HTTP startup in resumable batches.
INSERT INTO maintenance_jobs(name) VALUES('search_strings.v1');
