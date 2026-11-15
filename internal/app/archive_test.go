package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func oldEvent(t *testing.T, id, session string) Event {
	e := recorded(t, id, "import", "codex", session, "user_prompt", map[string]any{"prompt": "synthetic archive fixture"})
	e.Time = "2025-01-01T00:00:00.000000000Z"
	return e
}
func archiveReq(a *App, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func waitArchive(t *testing.T, a *App, id, status string) ArchiveJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := a.archiveJob(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == status {
			return j
		}
		if j.Status == "failed" {
			t.Fatalf("archive failed: %+v", j)
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, _ := a.archiveJob(context.Background(), id)
	t.Fatalf("timed out waiting for %s: %+v", status, j)
	return j
}
func newArchive(t *testing.T, a *App) ArchiveJob {
	t.Helper()
	body := `{"all":true,"cutoff":"2025-02-01T00:00:00Z"}`
	w := archiveReq(a, "POST", "/api/archives", body)
	if w.Code != 202 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var j ArchiveJob
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	return waitArchive(t, a, j.ID, "ready")
}
func countEvents(t *testing.T, a *App) int {
	t.Helper()
	var n int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM events").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestArchiveDownloadConfirmDeleteAndReplay(t *testing.T) {
	a := setup(t)
	old := oldEvent(t, "one", "old")
	recent := oldEvent(t, "two", "recent")
	recent.Time = timestamp(time.Now())
	unknown := oldEvent(t, "three", "unknown")
	unknown.Time = "1970-01-01T00:00:00.000000000Z"
	save(t, a, old, recent, unknown)
	list := archiveReq(a, "GET", "/api/archive", "")
	var data struct {
		Total int `json:"total_sessions"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &data); err != nil || data.Total != 1 {
		t.Fatalf("list: %s %v", list.Body, err)
	}
	job := newArchive(t, a)
	url := "/api/archives/" + job.ID
	if countEvents(t, a) != 3 {
		t.Fatal("creation deleted records")
	}
	for _, confirmation := range []string{"", `{"confirmation":"DOWNLOADED"}`} {
		w := archiveReq(a, "POST", url+"/delete", confirmation)
		if w.Code == 202 {
			t.Fatal("deleted before download")
		}
	}
	head := archiveReq(a, "HEAD", url+"/download", "")
	if head.Code != 200 {
		t.Fatal(head.Code)
	}
	if w := archiveReq(a, "POST", url+"/delete", `{"confirmation":"DOWNLOADED"}`); w.Code != 409 {
		t.Fatal("HEAD counted as download", w.Code)
	}
	download := archiveReq(a, "GET", url+"/download", "")
	if download.Code != 200 {
		t.Fatal(download.Code, download.Body)
	}
	digest := sha256.Sum256(download.Body.Bytes())
	if hex.EncodeToString(digest[:]) != job.SHA256 {
		t.Fatal("checksum mismatch")
	}
	zipped, err := zip.NewReader(bytes.NewReader(download.Body.Bytes()), int64(download.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range zipped.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(file.Name, "sessions/") {
			var e Event
			if err = json.Unmarshal(contents, &e); err != nil {
				t.Fatal(err)
			}
			if e.ID != old.ID || !bytes.Equal(e.Raw, old.Raw) {
				t.Fatalf("lost original: %s", contents)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing records")
	}
	if w := archiveReq(a, "POST", url+"/delete", `{"confirmation":"yes"}`); w.Code != 400 {
		t.Fatal("accepted wrong confirmation")
	}
	if countEvents(t, a) != 3 {
		t.Fatal("download deleted records")
	}
	if w := archiveReq(a, "POST", url+"/delete", `{"confirmation":"DOWNLOADED"}`); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	done := waitArchive(t, a, job.ID, "deleted")
	if done.DeletedRecords != 1 || countEvents(t, a) != 2 {
		t.Fatalf("wrong deletion: %+v", done)
	}
	if _, err = os.Stat(a.archivePath(job.ID, false)); !os.IsNotExist(err) {
		t.Fatal("retained downloaded server ZIP")
	}
	save(t, a, old)
	if countEvents(t, a) != 2 {
		t.Fatal("replayed archived record")
	}
	changed := old
	changed.Raw = bytes.Replace(old.Raw, []byte("synthetic archive fixture"), []byte("changed fixture"), 1)
	save(t, a, changed)
	if countEvents(t, a) != 3 {
		t.Fatal("new content suppressed")
	}
}
func TestArchiveStaleCancelAndCorrupt(t *testing.T) {
	for _, scenario := range []string{"updated", "canceled", "corrupt", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			a := setup(t)
			e := oldEvent(t, "one", "old")
			save(t, a, e)
			j := newArchive(t, a)
			url := "/api/archives/" + j.ID
			archiveReq(a, "GET", url+"/download", "")
			switch scenario {
			case "updated":
				e.Raw = bytes.Replace(e.Raw, []byte("synthetic archive fixture"), []byte("updated"), 1)
				save(t, a, e)
				if w := archiveReq(a, "POST", url+"/delete", `{"confirmation":"DOWNLOADED"}`); w.Code != 409 {
					t.Fatal(w.Code, w.Body)
				}
			case "canceled":
				if w := archiveReq(a, "POST", url+"/cancel", `{}`); w.Code != 200 {
					t.Fatal(w.Code)
				}
				if w := archiveReq(a, "POST", url+"/delete", `{"confirmation":"DOWNLOADED"}`); w.Code != 409 {
					t.Fatal(w.Code)
				}
			case "missing":
				os.Remove(a.archivePath(j.ID, false))
				if w := archiveReq(a, "POST", url+"/delete", `{"confirmation":"DOWNLOADED"}`); w.Code != 409 {
					t.Fatal(w.Code)
				}
			case "corrupt":
				content, err := os.ReadFile(a.archivePath(j.ID, false))
				if err != nil {
					t.Fatal(err)
				}
				content[0] ^= 1
				if err = os.WriteFile(a.archivePath(j.ID, false), content, 0600); err != nil {
					t.Fatal(err)
				}
				if w := archiveReq(a, "POST", url+"/delete", `{"confirmation":"DOWNLOADED"}`); w.Code != 202 {
					t.Fatal(w.Code)
				}
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					current, _ := a.archiveJob(context.Background(), j.ID)
					if current.Status == "failed" {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				current, _ := a.archiveJob(context.Background(), j.ID)
				if current.Status != "failed" {
					t.Fatal(current)
				}
			}
			if countEvents(t, a) != 1 {
				t.Fatal("unsafe deletion")
			}
		})
	}
}
func TestArchiveLocksAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	a, err := Open(context.Background(), path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	e := oldEvent(t, "one", "old")
	save(t, a, e)
	j := newArchive(t, a)
	// Persist a confirmed deletion, as if the process stopped before the worker ran.
	a.cancel()
	a.workers.Wait()
	if _, err = a.DB.Exec("INSERT INTO archive_locks VALUES(?,?)", e.Session, j.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.Save(context.Background(), []Event{oldEvent(t, "new", "old")}); err == nil {
		t.Fatal("locked session accepted new event")
	}
	if _, err = a.DB.Exec("UPDATE archives SET status='deleting',confirmed_at=?,database_bytes_before=? WHERE id=?", timestamp(time.Now()), a.diskBytes(), j.ID); err != nil {
		t.Fatal(err)
	}
	a.Close()
	b, err := Open(context.Background(), path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	waitArchive(t, b, j.ID, "deleted")
	if countEvents(t, b) != 0 {
		t.Fatal("did not resume")
	}
	save(t, b, e)
	if countEvents(t, b) != 0 {
		t.Fatal("tombstone not persisted")
	}
}
func TestArchiveReclaimsDatabase(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			var a *App
			if legacy {
				path := filepath.Join(t.TempDir(), "legacy.db")
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec("CREATE TABLE legacy_fixture(id INTEGER)")
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
				a, err = Open(context.Background(), path, token)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
			} else {
				a = setup(t)
			}
			events := []Event{}
			for i := 0; i < 25; i++ {
				e := oldEvent(t, strings.Repeat("x", i+1), "large")
				e.Raw = bytes.Replace(e.Raw, []byte("synthetic archive fixture"), bytes.Repeat([]byte("abcdefgh"), 16384), 1)
				events = append(events, e)
			}
			save(t, a, events...)
			a.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
			before := a.diskBytes()
			j := newArchive(t, a)
			url := "/api/archives/" + j.ID
			archiveReq(a, "GET", url+"/download", "")
			w := archiveReq(a, "POST", url+"/delete", `{"confirmation":"DOWNLOADED"}`)
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body)
			}
			done := waitArchive(t, a, j.ID, "deleted")
			if legacy {
				if !done.RebuildRequired {
					t.Fatal("legacy rebuild must require explicit action")
				}
				if w := archiveReq(a, "POST", url+"/compact", `{}`); w.Code != 409 {
					t.Fatal("implicit rebuild accepted", w.Code)
				}
				if w := archiveReq(a, "POST", url+"/compact", `{"rebuild":true}`); w.Code != 202 {
					t.Fatal(w.Code, w.Body)
				}
				done = waitArchive(t, a, j.ID, "deleted")
			}
			if done.CompactionError != "" || a.diskBytes() > before/2 {
				t.Fatalf("space not reclaimed: before=%d after=%d job=%+v", before, a.diskBytes(), done)
			}
			var mode int
			if err := a.DB.QueryRow("PRAGMA auto_vacuum").Scan(&mode); err != nil || mode != 2 {
				t.Fatal("incremental reclaim not enabled", mode, err)
			}
		})
	}
}
func TestArchiveCutoffClampsMonth(t *testing.T) {
	now := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
	if got := archiveCutoff(now); got.Day() != 28 || got.Month() != time.February {
		t.Fatal(got)
	}
}

func TestArchiveSelectionAndDownloadRanges(t *testing.T) {
	a := setup(t)
	save(t, a, oldEvent(t, "one", "a"), oldEvent(t, "two", "b"))
	w := archiveReq(a, "POST", "/api/archives", `{"cutoff":"2025-02-01T00:00:00Z","sessions":["codex:a"]}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	var j ArchiveJob
	json.Unmarshal(w.Body.Bytes(), &j)
	j = waitArchive(t, a, j.ID, "ready")
	if j.SessionCount != 1 {
		t.Fatal(j)
	}
	if w = archiveReq(a, "POST", "/api/archives", `{"all":true,"cutoff":"2025-02-01T00:00:00Z"}`); w.Code != 409 {
		t.Fatal("allowed concurrent archive", w.Code)
	}
	rangeRequest := func(value string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/archives/"+j.ID+"/download", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Range", value)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	if w = rangeRequest("bytes=999999999999-"); w.Code != 416 {
		t.Fatal(w.Code)
	}
	current, _ := a.archiveJob(context.Background(), j.ID)
	if current.DownloadAt != "" {
		t.Fatal("failed range counted as download")
	}
	if w = rangeRequest("bytes=0-9"); w.Code != 206 || w.Body.Len() != 10 {
		t.Fatal(w.Code, w.Body.Len())
	}
	r := httptest.NewRequest("POST", "/api/archives/"+j.ID+"/delete", strings.NewReader(`{"confirmation":"DOWNLOADED"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal("accepted non-JSON mutation")
	}
}
func TestArchiveChangedBeforeBuild(t *testing.T) {
	a := setup(t)
	a.cancel()
	a.workers.Wait()
	e := oldEvent(t, "one", "old")
	save(t, a, e)
	j, err := a.createArchive(context.Background(), "2025-02-01T00:00:00.000000000Z", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.Raw = bytes.Replace(e.Raw, []byte("synthetic archive fixture"), []byte("changed"), 1)
	save(t, a, e)
	if err = a.buildArchive(context.Background(), j); err != errArchiveStale {
		t.Fatal("archived stale snapshot", err)
	}
	if _, err = os.Stat(a.archivePath(j.ID, true)); !os.IsNotExist(err) {
		t.Fatal("partial file retained")
	}
	if countEvents(t, a) != 1 {
		t.Fatal("changed record lost")
	}
}

func TestArchiveRestartMissingVerification(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(fmt.Sprint("verified=", verified), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.db")
			a, err := Open(context.Background(), path, token)
			if err != nil {
				t.Fatal(err)
			}
			e := oldEvent(t, "one", "old")
			save(t, a, e)
			j := newArchive(t, a)
			a.cancel()
			a.workers.Wait()
			verifiedAt := ""
			if verified {
				verifiedAt = timestamp(time.Now())
			}
			if _, err = a.DB.Exec("INSERT INTO archive_locks VALUES(?,?)", e.Session, j.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = a.DB.Exec("UPDATE archives SET status='deleting',confirmed_at=?,verified_at=? WHERE id=?", timestamp(time.Now()), verifiedAt, j.ID); err != nil {
				t.Fatal(err)
			}
			os.Remove(a.archivePath(j.ID, false))
			a.Close()
			b, err := Open(context.Background(), path, token)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if verified {
				waitArchive(t, b, j.ID, "deleted")
				if countEvents(t, b) != 0 {
					t.Fatal("verified deletion did not resume")
				}
			} else {
				deadline := time.Now().Add(5 * time.Second)
				var current ArchiveJob
				for time.Now().Before(deadline) {
					current, _ = b.archiveJob(context.Background(), j.ID)
					if current.Status == "failed" {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				if current.Status != "failed" || countEvents(t, b) != 1 {
					t.Fatal("missing unverified archive deleted data", current)
				}
			}
		})
	}
}

func TestVacuumBatchConsumesAllPageResults(t *testing.T) {
	a := setup(t)
	if _, err := a.DB.Exec(`CREATE TABLE vacuum_fixture(data BLOB);WITH RECURSIVE items(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM items WHERE n<1024) INSERT INTO vacuum_fixture SELECT zeroblob(4096) FROM items;DELETE FROM vacuum_fixture`); err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err := a.DB.QueryRow("PRAGMA freelist_count").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before < 512 {
		t.Fatal("fixture too small", before)
	}
	if pages, err := a.vacuumBatch(context.Background(), 256); err != nil || pages != 256 {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow("PRAGMA freelist_count").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before-after != 256 {
		t.Fatalf("batch stopped early: before=%d after=%d", before, after)
	}
}
