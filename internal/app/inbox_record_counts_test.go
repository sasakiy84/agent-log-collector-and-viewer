package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestInboxRecordCountsUpgradeReplaysDeepQueuedJSON(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "views.db")
	db := migrationDB(t, path+".ingest.db")
	if err := migrate(ctx, db, inboxMigrations[:1]); err != nil {
		t.Fatal(err)
	}
	event := Event{ID: "deep", Session: "codex:deep", Source: "codex", Signal: "logs", Name: "user_prompt", Time: "2026-11-17T00:00:00Z",
		Raw: json.RawMessage(`{"resource":{},"scope":{},"record":{"body":` + strings.Repeat(`{"arrayValue":{"values":[`, 350) + `{"stringValue":"deep queued message"}` + strings.Repeat(`]}}`, 350) + `}}`)}
	encoded, err := json.Marshal([]Event{event})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO ingest_batches(data) VALUES(?)", encoded); err != nil {
		t.Fatal(err)
	}
	db.Close()
	a, err := Start(ctx, path, token)
	if err != nil {
		t.Fatal("previously accepted JSON prevented startup:", err)
	}
	defer a.Close()
	serviceSettled(t, a)
	var actual []byte
	if err = a.DB.QueryRow("SELECT raw FROM records WHERE id='deep'").Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, event.Raw) {
		t.Fatal("deep queued JSON changed during replay")
	}
	state, err := a.statusSnapshot(ctx)
	if err != nil || state.PendingBatches != 0 || state.PendingRecords != 0 || state.PendingBytes != 0 {
		t.Fatal(state, err)
	}
}

func TestInboxRecordCountsUpgradePreservesQueuedPayloads(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "views.db")
	db := migrationDB(t, path+".ingest.db")
	if err := migrate(ctx, db, inboxMigrations[:1]); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal([]Event{{ID: "one"}, {ID: "two"}, {ID: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO ingest_batches(data) VALUES(?),(?)", encoded, encoded); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	for restart := 0; restart < 2; restart++ {
		q, err := openIngestQueue(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		batches, records, size, err := q.size(ctx)
		if err != nil || batches != 2 || records != 6 || size != int64(2*len(encoded)) {
			q.close()
			t.Fatal("upgrade/restart changed pending counts", batches, records, size, err)
		}
		var payload []byte
		err = q.db.QueryRow("SELECT data FROM ingest_batches ORDER BY id LIMIT 1").Scan(&payload)
		if err != nil || !bytes.Equal(payload, encoded) {
			q.close()
			t.Fatal("upgrade changed an acknowledged payload", err)
		}
		if err = q.close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInboxRecordCountsAcceptsPreviouslyAppliedOriginalMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "views.db")
	db := migrationDB(t, path+".ingest.db")
	if err := migrate(ctx, db, inboxMigrations[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ingest_batches(data) VALUES('[{"id":"queued"}]')`); err != nil {
		t.Fatal(err)
	}
	// Simulate the original release applying SQL without the compatibility adapter.
	if err := migrate(ctx, db, inboxMigrations); err != nil {
		t.Fatal(err)
	}
	var checksum string
	if err := db.QueryRow("SELECT checksum FROM schema_migrations WHERE version=2").Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	db.Close()
	q, err := openIngestQueue(ctx, path)
	if err != nil {
		t.Fatal("previous migration history rejected:", err)
	}
	defer q.close()
	batches, records, _, err := q.size(ctx)
	if err != nil || batches != 1 || records != 1 {
		t.Fatal(batches, records, err)
	}
	var actual string
	if err := q.db.QueryRow("SELECT checksum FROM schema_migrations WHERE version=2").Scan(&actual); err != nil || actual != checksum {
		t.Fatal("previous migration checksum changed", actual, err)
	}
}

func TestPendingRecordsIncludeDuplicatesAndProcessingBatchUntilAcknowledged(t *testing.T) {
	a := setup(t)
	var err error
	a.inbox, err = openIngestQueue(a.ctx, filepath.Join(t.TempDir(), "views.db"))
	if err != nil {
		t.Fatal(err)
	}
	event := recorded(t, "pending", "otel", "codex", "root", "gen_ai.user.message", map[string]any{"prompt": "queued"})
	first := []Event{event, event, event}
	for _, batch := range [][]Event{first, {event}} {
		if err = a.inbox.accept(a.ctx, batch, false); err != nil {
			t.Fatal(err)
		}
	}
	assertPending := func(batches, records int64) {
		t.Helper()
		for _, path := range []string{"/api/status", "/api/metrics"} {
			var status serviceStatus
			getJSON(t, a, path, &status)
			if status.PendingBatches != batches || status.PendingRecords != records {
				t.Fatal(path, status)
			}
		}
	}
	assertPending(2, 4)
	// Saving can finish before the inbox ACK; counters still include that batch.
	if err = a.Save(a.ctx, first); err != nil {
		t.Fatal(err)
	}
	assertPending(2, 4)
	if _, err = a.inbox.db.Exec("DELETE FROM ingest_batches WHERE id=(SELECT MIN(id) FROM ingest_batches)"); err != nil {
		t.Fatal(err)
	}
	assertPending(1, 1)
	if _, err = a.inbox.db.Exec("DELETE FROM ingest_batches"); err != nil {
		t.Fatal(err)
	}
	assertPending(0, 0)
	var count int
	if err = a.inbox.db.QueryRow("SELECT COUNT(*) FROM ingest_record_counts").Scan(&count); err != nil || count != 0 {
		t.Fatal("acknowledged counts were not reclaimed", count, err)
	}
}
