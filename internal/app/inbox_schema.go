package app

// The inbox has its own small schema history. Append migrations without editing
// released SQL, just as for the view database; queued ACKs must survive upgrades.
var inboxMigrations = []migration{{1, "0001_durable_inbox", `
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
`}}
