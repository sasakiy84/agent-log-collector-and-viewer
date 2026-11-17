package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

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
