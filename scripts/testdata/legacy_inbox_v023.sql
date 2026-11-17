
 CREATE TABLE ingest_batches(id INTEGER PRIMARY KEY AUTOINCREMENT,data BLOB NOT NULL);
 CREATE TABLE ingest_sessions(batch INTEGER NOT NULL REFERENCES ingest_batches(id) ON DELETE CASCADE,
 session TEXT NOT NULL,PRIMARY KEY(batch,session)) WITHOUT ROWID;
 CREATE INDEX ingest_session_pending ON ingest_sessions(session,batch);
 CREATE TABLE ingest_size(id INTEGER PRIMARY KEY CHECK(id=1),bytes INTEGER NOT NULL,batches INTEGER NOT NULL);
 INSERT INTO ingest_size VALUES(1,0,0);
 CREATE TRIGGER ingest_added AFTER INSERT ON ingest_batches BEGIN
 UPDATE ingest_size SET bytes=bytes+length(NEW.data),batches=batches+1 WHERE id=1; END;
 CREATE TRIGGER ingest_removed AFTER DELETE ON ingest_batches BEGIN
 UPDATE ingest_size SET bytes=bytes-length(OLD.data),batches=batches-1 WHERE id=1; END;

CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,name TEXT NOT NULL,checksum TEXT NOT NULL,applied_at TEXT NOT NULL);
INSERT INTO schema_migrations VALUES(1,'0001_durable_inbox','3dbed5d4ce655e162192c0073caf4b7c66f09c83e79cc853d28070767aa636ca','2026-11-17T00:00:00.000000000Z');
CREATE TABLE watch_files(key TEXT PRIMARY KEY,value BLOB NOT NULL) WITHOUT ROWID;
CREATE TABLE watch_records(key TEXT NOT NULL,id TEXT NOT NULL,hash BLOB NOT NULL,PRIMARY KEY(key,id)) WITHOUT ROWID;
