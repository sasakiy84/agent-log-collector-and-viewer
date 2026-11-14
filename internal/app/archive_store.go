package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const archiveConfirmation = "DOWNLOADED"
const archiveSchema = `
CREATE TABLE IF NOT EXISTS session_revisions(session TEXT PRIMARY KEY, revision INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS archives(
 id TEXT PRIMARY KEY,status TEXT NOT NULL,created_at TEXT NOT NULL,cutoff TEXT NOT NULL,
 session_count INTEGER NOT NULL DEFAULT 0,record_count INTEGER NOT NULL DEFAULT 0,raw_bytes INTEGER NOT NULL DEFAULT 0,
 processed_records INTEGER NOT NULL DEFAULT 0,archive_bytes INTEGER NOT NULL DEFAULT 0,sha256 TEXT NOT NULL DEFAULT '',
 verified_at TEXT NOT NULL DEFAULT '',download_at TEXT NOT NULL DEFAULT '',confirmed_at TEXT NOT NULL DEFAULT '',deleted_records INTEGER NOT NULL DEFAULT 0,
 reclaimed_bytes INTEGER NOT NULL DEFAULT 0,database_bytes_before INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',compaction_error TEXT NOT NULL DEFAULT '',rebuild_required INTEGER NOT NULL DEFAULT 0,rebuild_requested INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_archive ON archives((1)) WHERE status IN ('preparing','ready','deleting','compacting');
CREATE TABLE IF NOT EXISTS archive_sessions(
 archive_id TEXT NOT NULL REFERENCES archives(id),session TEXT NOT NULL,source TEXT NOT NULL,
 revision INTEGER NOT NULL,records INTEGER NOT NULL,raw_bytes INTEGER NOT NULL,last_activity TEXT NOT NULL,
 PRIMARY KEY(archive_id,session)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS archive_locks(session TEXT PRIMARY KEY,archive_id TEXT NOT NULL REFERENCES archives(id));
CREATE TABLE IF NOT EXISTS archived_records(id_hash BLOB NOT NULL,raw_hash BLOB NOT NULL,PRIMARY KEY(id_hash,raw_hash)) WITHOUT ROWID;
CREATE TRIGGER IF NOT EXISTS archive_insert_guard BEFORE INSERT ON events WHEN EXISTS(SELECT 1 FROM archive_locks WHERE session=NEW.session)
 BEGIN SELECT RAISE(ABORT,'session archive deletion is in progress'); END;
CREATE TRIGGER IF NOT EXISTS archive_update_guard BEFORE UPDATE ON events WHEN EXISTS(SELECT 1 FROM archive_locks WHERE session IN (OLD.session,NEW.session))
 BEGIN SELECT RAISE(ABORT,'session archive deletion is in progress'); END;
CREATE TRIGGER IF NOT EXISTS revision_insert AFTER INSERT ON events BEGIN
 INSERT INTO session_revisions VALUES(NEW.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;
CREATE TRIGGER IF NOT EXISTS revision_update AFTER UPDATE ON events BEGIN
 INSERT INTO session_revisions VALUES(OLD.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1;
 INSERT INTO session_revisions SELECT NEW.session,1 WHERE NEW.session<>OLD.session ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;
CREATE TRIGGER IF NOT EXISTS revision_delete AFTER DELETE ON events BEGIN
 INSERT INTO session_revisions VALUES(OLD.session,1) ON CONFLICT(session) DO UPDATE SET revision=revision+1; END;
`

type ArchiveJob struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	CreatedAt        string `json:"created_at"`
	Cutoff           string `json:"cutoff"`
	SessionCount     int64  `json:"session_count"`
	RecordCount      int64  `json:"record_count"`
	RawBytes         int64  `json:"raw_bytes"`
	ProcessedRecords int64  `json:"processed_records"`
	ArchiveBytes     int64  `json:"archive_bytes"`
	SHA256           string `json:"sha256"`
	DownloadAt       string `json:"download_at"`
	ConfirmedAt      string `json:"confirmed_at"`
	DeletedRecords   int64  `json:"deleted_records"`
	ReclaimedBytes   int64  `json:"reclaimed_bytes"`
	Error            string `json:"error"`
	CompactionError  string `json:"compaction_error"`
	RebuildRequired  bool   `json:"rebuild_required"`
}
type ArchiveSession struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	LastActivity string `json:"last_activity"`
	Records      int64  `json:"records"`
	RawBytes     int64  `json:"raw_bytes"`
	Revision     int64  `json:"-"`
}

const archiveColumns = "id,status,created_at,cutoff,session_count,record_count,raw_bytes,processed_records,archive_bytes,sha256,download_at,confirmed_at,deleted_records,reclaimed_bytes,error,compaction_error,rebuild_required"

func scanArchive(row scanner) (ArchiveJob, error) {
	var j ArchiveJob
	err := row.Scan(&j.ID, &j.Status, &j.CreatedAt, &j.Cutoff, &j.SessionCount, &j.RecordCount, &j.RawBytes, &j.ProcessedRecords, &j.ArchiveBytes, &j.SHA256, &j.DownloadAt, &j.ConfirmedAt, &j.DeletedRecords, &j.ReclaimedBytes, &j.Error, &j.CompactionError, &j.RebuildRequired)
	return j, err
}
func (a *App) archiveJob(ctx context.Context, id string) (ArchiveJob, error) {
	return scanArchive(a.readers.QueryRowContext(ctx, "SELECT "+archiveColumns+" FROM archives WHERE id=?", id))
}
func activeArchive(status string) bool {
	return status == "preparing" || status == "ready" || status == "deleting" || status == "compacting"
}
func archiveCutoff(now time.Time) time.Time {
	now = now.UTC()
	previous := time.Date(now.Year(), now.Month(), 1, now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC).AddDate(0, -1, 0)
	lastDay := time.Date(now.Year(), now.Month(), 0, 0, 0, 0, 0, time.UTC).Day()
	day := min(now.Day(), lastDay)
	return time.Date(previous.Year(), previous.Month(), day, now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)
}
func timestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

const inactiveSessions = `SELECT session,MAX(source) AS source,MAX(time) AS last_activity,COUNT(*) AS records,SUM(length(raw)) AS raw_bytes
 FROM events GROUP BY session HAVING MAX(time)<? AND MIN(time)>'1970-01-01T00:00:00.000000000Z'`

func (a *App) archivePath(id string, partial bool) string {
	suffix := ".zip"
	if partial {
		suffix += ".partial"
	}
	return filepath.Join(a.archiveDir, id+suffix)
}
func (a *App) wakeArchive() {
	select {
	case a.archiveWake <- struct{}{}:
	default:
	}
}
func (a *App) Close() error {
	a.cancel()
	a.workers.Wait()
	if a.readers != nil && a.readers != a.DB {
		a.readers.Close()
	}
	return a.DB.Close()
}
func (a *App) initArchives(path string) error {
	if path == ":memory:" {
		a.dbPath = path
	} else {
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		a.dbPath = abs
		a.archiveDir = filepath.Join(filepath.Dir(abs), "archives")
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.archiveWake = make(chan struct{}, 1)
	rows, err := a.DB.Query("SELECT id FROM archives WHERE status='preparing'")
	if err != nil {
		return err
	}
	var interrupted []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		interrupted = append(interrupted, id)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return errors.Join(err, rowErr)
	}
	if _, err = a.DB.Exec("UPDATE archives SET status='failed',error='Archive creation was interrupted; create a new archive.' WHERE status='preparing'"); err != nil {
		return err
	}
	for _, id := range interrupted {
		os.Remove(a.archivePath(id, true))
		os.Remove(a.archivePath(id, false))
	}
	a.workers.Add(1)
	go a.archiveWorker()
	a.wakeArchive()
	return nil
}
func (a *App) createArchive(ctx context.Context, cutoff string, all bool, ids []string) (ArchiveJob, error) {
	if a.dbPath == ":memory:" {
		return ArchiveJob{}, fmt.Errorf("archiving requires a persistent database")
	}
	if err := os.MkdirAll(a.archiveDir, 0700); err != nil {
		return ArchiveJob{}, err
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return ArchiveJob{}, err
	}
	id := hex.EncodeToString(bytes)
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return ArchiveJob{}, err
	}
	defer tx.Rollback()
	var active int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM archives WHERE status IN ('preparing','ready','deleting','compacting')").Scan(&active); err != nil {
		return ArchiveJob{}, err
	}
	if active > 0 {
		return ArchiveJob{}, errArchiveConflict
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO archives(id,status,created_at,cutoff) VALUES(?,'preparing',?,?)", id, timestamp(time.Now()), cutoff)
	if err != nil {
		return ArchiveJob{}, err
	}
	query := "INSERT INTO archive_sessions SELECT ?,s.session,s.source,COALESCE(r.revision,0),s.records,s.raw_bytes,s.last_activity FROM (" + inactiveSessions + ") s LEFT JOIN session_revisions r ON r.session=s.session"
	args := []any{id, cutoff}
	if !all {
		query += " WHERE s.session IN (" + strings.TrimRight(strings.Repeat("?,", len(ids)), ",") + ")"
		for _, sid := range ids {
			args = append(args, sid)
		}
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return ArchiveJob{}, err
	}
	var count int64
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM archive_sessions WHERE archive_id=?", id).Scan(&count); err != nil {
		return ArchiveJob{}, err
	}
	if count == 0 || !all && count != int64(len(ids)) {
		return ArchiveJob{}, errArchiveStale
	}
	_, err = tx.ExecContext(ctx, `UPDATE archives SET session_count=?,record_count=(SELECT SUM(records) FROM archive_sessions WHERE archive_id=?),raw_bytes=(SELECT SUM(raw_bytes) FROM archive_sessions WHERE archive_id=?) WHERE id=?`, count, id, id, id)
	if err != nil {
		return ArchiveJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return ArchiveJob{}, err
	}
	job, err := a.archiveJob(ctx, id)
	a.wakeArchive()
	return job, err
}

var errArchiveConflict = errors.New("another archive operation is in progress")
var errArchiveStale = errors.New("selected sessions changed; refresh the list and create a new archive")

func snapshotsCurrent(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var changed int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM archive_sessions s LEFT JOIN session_revisions r ON r.session=s.session WHERE s.archive_id=? AND COALESCE(r.revision,0)<>s.revision`, id).Scan(&changed)
	return changed == 0, err
}
