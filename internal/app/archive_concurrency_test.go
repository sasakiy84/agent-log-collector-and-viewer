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
	a, err := Open(context.Background(), path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// Isolate deletion from the final truncating checkpoint held by our reader.
	if _, err = a.DB.Exec("PRAGMA auto_vacuum=NONE; VACUUM"); err != nil {
		t.Fatal(err)
	}
	events := make([]Event, 401)
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
	if _, err = a.DB.Exec("INSERT INTO archive_locks VALUES(?,?); UPDATE archive_jobs SET status='deleting' WHERE id=?", events[0].Session, job.ID, job.ID); err != nil {
		t.Fatal(err)
	}
	reader, err := a.readers.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var retained int
	if err = reader.QueryRow("SELECT COUNT(*) FROM records").Scan(&retained); err != nil || retained != len(events) {
		t.Fatal(retained, err)
	}
	// Keep the busy timeout longer than the test deadline. A blocking TRUNCATE
	// cannot pass, while race-instrumented deletion has time to execute its SQL.
	if _, err = a.DB.Exec("PRAGMA busy_timeout=60000"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = a.deleteArchive(ctx, job); err != nil {
		t.Fatal("deletion waited for the export snapshot", err)
	}
	if countEvents(t, a) != 0 {
		t.Fatal("deletion did not finish every batch")
	}
	if err = reader.QueryRow("SELECT COUNT(*) FROM records").Scan(&retained); err != nil || retained != len(events) {
		t.Fatal("concurrent reader lost its original snapshot", retained, err)
	}
}
