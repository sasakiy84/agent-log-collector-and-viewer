package app

import (
	"database/sql/driver"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"

	"modernc.org/sqlite"
)

func init() {
	// Queued batches were accepted by encoding/json, whose nesting limit exceeds
	// SQLite JSON's. Count with that same decoder before replaying those batches.
	sqlite.MustRegisterDeterministicScalarFunction("agentlog_inbox_records", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		var data []byte
		switch value := args[0].(type) {
		case []byte:
			data = value
		case string:
			data = []byte(value)
		default:
			return nil, errors.New("queued batch is not JSON text")
		}
		var records []json.RawMessage
		if err := json.Unmarshal(data, &records); err != nil {
			return nil, err
		}
		return int64(len(records)), nil
	})
}

func inboxMigrationSQL(m migration) string {
	if m.version == 2 && m.name == "0002_inbox_record_counts" {
		// Keep migration 2's checksum valid on installations that applied it.
		return strings.Replace(m.sql, "json_array_length(CAST(data AS TEXT))", "agentlog_inbox_records(data)", 1)
	}
	return m.sql
}

//go:embed inbox_record_counts.sql
var inboxRecordCountsSchema string

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
`}, {2, "0002_inbox_record_counts", inboxRecordCountsSchema}}
