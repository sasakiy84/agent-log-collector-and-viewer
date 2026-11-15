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
	Folder       string `json:"folder,omitempty"`
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

const inactiveSessions = `SELECT s.session,s.source,s.end AS last_activity,a.records,a.raw_bytes,COALESCE(json_extract(s.preview,'$.cwd'),'') AS folder
 FROM session_catalog s JOIN archive_stats a ON a.session=s.session
 WHERE s.end<? AND s.archive_start>'1970-01-01T00:00:00.000000000Z'`

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
	a.archiveWake = make(chan struct{}, 1)
	rows, err := a.DB.QueryContext(a.ctx, "SELECT id FROM archives WHERE status='preparing'")
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
	if _, err = a.DB.ExecContext(a.ctx, "UPDATE archives SET status='failed',error='Archive creation was interrupted; create a new archive.' WHERE status='preparing'"); err != nil {
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
	return a.createFolderArchive(ctx, cutoff, all, ids, nil)
}
func (a *App) createFolderArchive(ctx context.Context, cutoff string, all bool, ids, folders []string) (ArchiveJob, error) {
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
	query := "INSERT INTO archive_sessions(archive_id,session,source,revision,records,raw_bytes,last_activity,folder) SELECT ?,s.session,s.source,COALESCE(r.revision,0),s.records,s.raw_bytes,s.last_activity,s.folder FROM (" + inactiveSessions + ") s LEFT JOIN session_revisions r ON r.session=s.session"
	args := []any{id, cutoff}
	if len(folders) > 0 {
		query += " WHERE s.folder IN (" + strings.TrimRight(strings.Repeat("?,", len(folders)), ",") + ")"
		for _, folder := range folders {
			args = append(args, folder)
		}
	} else if !all {
		query += " WHERE s.session IN (" + strings.TrimRight(strings.Repeat("?,", len(ids)), ",") + ")"
		for _, sid := range ids {
			args = append(args, sid)
		}
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return ArchiveJob{}, err
	}
	pending, err := a.archiveHasPending(ctx, tx, id)
	if err != nil {
		return ArchiveJob{}, err
	}
	if pending {
		return ArchiveJob{}, errArchiveStale
	}

	var count int64
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM archive_sessions WHERE archive_id=?", id).Scan(&count); err != nil {
		return ArchiveJob{}, err
	}
	if count == 0 || !all && len(folders) == 0 && count != int64(len(ids)) {
		return ArchiveJob{}, errArchiveStale
	}
	if len(folders) > 0 {
		var selected int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(DISTINCT folder) FROM archive_sessions WHERE archive_id=?", id).Scan(&selected); err != nil {
			return ArchiveJob{}, err
		}
		if selected != len(folders) {
			return ArchiveJob{}, errArchiveStale
		}
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
