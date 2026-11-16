package app

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestArchiveDeletionMakesProgressWithLongRunningReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Keep compaction an explicit follow-up so this test isolates deletion,
	// rather than the final space-reclamation checkpoint.
	_, err = seed.Exec("CREATE TABLE existing_database(id INTEGER)")
	seed.Close()
	if err != nil {
		t.Fatal(err)
	}
	a, err := Open(context.Background(), path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	events := make([]Event, 600)
	for i := range events {
		events[i] = oldEvent(t, fmt.Sprint(i), "archived")
	}
	save(t, a, events...)
	job := newArchive(t, a)
	if download := archiveReq(a, "GET", "/api/archives/"+job.ID+"/download", ""); download.Code != 200 {
		t.Fatal(download.Code)
	}
	// Run the deletion directly after the ordinary archive worker has stopped.
	a.cancel()
	a.workers.Wait()
	if _, err = a.DB.Exec("INSERT INTO archive_locks VALUES(?,?); UPDATE archives SET status='deleting' WHERE id=?", events[0].Session, job.ID, job.ID); err != nil {
		t.Fatal(err)
	}
	reader, err := a.readers.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var retained int
	if err = reader.QueryRow("SELECT COUNT(*) FROM events").Scan(&retained); err != nil || retained != len(events) {
		t.Fatal(retained, err)
	}
	// The normal writer's busy timeout is five seconds. A pinned export must
	// not consume it on each batch of 200 records while no reader is ending.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = a.deleteArchive(ctx, job); err != nil {
		t.Fatal("deletion waited for the export snapshot", err)
	}
	if countEvents(t, a) != 0 {
		t.Fatal("deletion did not finish every batch")
	}
	if err = reader.QueryRow("SELECT COUNT(*) FROM events").Scan(&retained); err != nil || retained != len(events) {
		t.Fatal("concurrent reader lost its original snapshot", retained, err)
	}
}
