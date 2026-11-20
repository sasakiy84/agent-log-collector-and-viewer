package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveDeletionMakesProgressWithLongRunningReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	a, err := Open(context.Background(), path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
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

func TestArchiveDeletionResumesAfterBatchFailure(t *testing.T) {
	a := setup(t)
	events := make([]Event, 201)
	for i := range events {
		events[i] = oldEvent(t, fmt.Sprint(i), "archive")
	}
	save(t, a, events...)
	job := newArchive(t, a)
	url := "/api/archives/" + job.ID
	if w := archiveReq(a, "GET", url+"/download", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := archiveReq(a, "POST", url+"/retry-delete", "{}"); w.Code != 409 {
		t.Fatal("retry accepted before confirmation", w.Code, w.Body)
	}
	if _, err := a.DB.Exec(`CREATE TRIGGER interrupt_archive BEFORE DELETE ON records
		WHEN (SELECT deleted_records FROM archive_jobs WHERE status='deleting') >= 200
		BEGIN SELECT RAISE(ABORT,'injected deletion failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if w := archiveReq(a, "POST", url+"/delete", `{"confirmation":"DOWNLOADED"}`); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var paused ArchiveJob
	for {
		changed := a.archiveChanges()
		var err error
		paused, err = a.archiveJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if paused.Error != "" {
			break
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatal("worker did not pause", paused)
		}
	}
	if paused.Status != "deleting" || paused.DeletedRecords != 200 || countEvents(t, a) != 1 {
		t.Fatalf("unexpected state after partial deletion: %+v", paused)
	}
	if _, err := os.Stat(a.archivePath(job.ID, false)); err != nil {
		t.Fatal("server ZIP was removed before all records were deleted", err)
	}
	if err := a.Save(ctx, []Event{oldEvent(t, "late", "archive")}); err == nil {
		t.Fatal("paused archive accepted a write")
	}
	if _, err := a.DB.Exec("DROP TRIGGER interrupt_archive"); err != nil {
		t.Fatal(err)
	}
	if w := archiveReq(a, "POST", url+"/retry-delete", "{}"); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	done := waitArchive(t, a, job.ID, "deleted")
	if done.DeletedRecords != job.RecordCount || done.Error != "" || countEvents(t, a) != 0 {
		t.Fatalf("retry did not finish the confirmed selection: %+v", done)
	}
}

func TestArchiveCompactionReportsBlockedWALAndCanRetry(t *testing.T) {
	a := setup(t)
	save(t, a, oldEvent(t, "one", "old"))
	job := newArchive(t, a)
	url := "/api/archives/" + job.ID
	if w := archiveReq(a, "GET", url+"/download", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	reader, err := a.readers.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var records int
	if err = reader.QueryRow("SELECT COUNT(*) FROM records").Scan(&records); err != nil || records != 1 {
		t.Fatal(records, err)
	}
	if _, err = a.DB.Exec("PRAGMA busy_timeout=50"); err != nil {
		t.Fatal(err)
	}
	if w := archiveReq(a, "POST", url+"/delete", `{"confirmation":"DOWNLOADED"}`); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	done := waitArchive(t, a, job.ID, "deleted")
	if !strings.Contains(done.CompactionError, "WAL truncation") || done.RebuildRequired || countEvents(t, a) != 0 {
		t.Fatalf("busy WAL was not reported: %+v", done)
	}
	if err = reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	if w := archiveReq(a, "POST", url+"/compact", "{}"); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	if retried := waitArchive(t, a, job.ID, "deleted"); retried.CompactionError != "" {
		t.Fatalf("compaction retry did not clear the warning: %+v", retried)
	}
}
