package app

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

func (a *App) archiveWorker() {
	defer a.workers.Done()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.archiveWake:
		}
		for a.ctx.Err() == nil {
			job, err := scanArchive(a.DB.QueryRowContext(a.ctx, "SELECT "+archiveColumns+" FROM archives WHERE status IN ('preparing','deleting','compacting') ORDER BY created_at LIMIT 1"))
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				if a.ctx.Err() == nil {
					log.Printf("archive worker: %v", err)
				}
				break
			}
			ctx, cancel := context.WithCancel(a.ctx)
			a.taskMu.Lock()
			a.taskID = job.ID
			a.taskCancel = cancel
			a.taskMu.Unlock()
			switch job.Status {
			case "preparing":
				err = a.buildArchive(ctx, job)
			case "deleting":
				err = a.deleteArchive(ctx, job)
			case "compacting":
				err = a.compactArchive(ctx, job)
			}
			cancel()
			a.taskMu.Lock()
			a.taskID = ""
			a.taskCancel = nil
			a.taskMu.Unlock()
			if err != nil && a.ctx.Err() == nil {
				log.Printf("archive %s %s: %v", job.ID, job.Status, err)
				tx, e := a.DB.Begin()
				if e == nil {
					_, e = tx.Exec("DELETE FROM archive_locks WHERE archive_id=?", job.ID)
					if e == nil {
						_, e = tx.Exec("UPDATE archives SET status='failed',error=? WHERE id=? AND status IN ('preparing','deleting','compacting')", archiveFailure(err), job.ID)
					}
					if e == nil {
						e = tx.Commit()
					} else {
						tx.Rollback()
					}
				}
				if e != nil {
					log.Printf("archive failure state: %v", e)
					break
				}
				os.Remove(a.archivePath(job.ID, true))
				os.Remove(a.archivePath(job.ID, false))
			}
		}
	}
}
func (a *App) archiveConnection(ctx context.Context, readOnly bool) (*sql.DB, error) {
	db, err := sql.Open("sqlite", a.dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	pragma := "PRAGMA busy_timeout=5000;"
	if readOnly {
		pragma += "PRAGMA query_only=ON;"
	}
	if _, err = db.ExecContext(ctx, pragma); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type byteCounter struct{ n int64 }

func (c *byteCounter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }
func (a *App) buildArchive(ctx context.Context, job ArchiveJob) (err error) {
	current, err := a.archiveJob(ctx, job.ID)
	if err != nil {
		return err
	}
	if current.Status != "preparing" {
		return context.Canceled
	}
	reader, err := a.archiveConnection(ctx, true)
	if err != nil {
		return err
	}
	defer reader.Close()
	partial := a.archivePath(job.ID, true)
	final := a.archivePath(job.ID, false)
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		file.Close()
		if err != nil {
			os.Remove(partial)
			os.Remove(final)
		}
	}()
	digest := sha256.New()
	counter := &byteCounter{}
	archive := zip.NewWriter(io.MultiWriter(file, digest, counter))
	manifest, err := archive.Create("manifest.json")
	if err != nil {
		return err
	}
	header := struct {
		Format    string `json:"format"`
		Version   int    `json:"version"`
		ID        string `json:"id"`
		CreatedAt string `json:"created_at"`
		Cutoff    string `json:"cutoff"`
		Sessions  int64  `json:"sessions"`
		Records   int64  `json:"records"`
	}{"agentlog.session-archive", 1, job.ID, job.CreatedAt, job.Cutoff, job.SessionCount, job.RecordCount}
	if err = json.NewEncoder(manifest).Encode(header); err != nil {
		return err
	}
	manifest, err = archive.Create("sessions.jsonl")
	if err != nil {
		return err
	}
	rows, err := a.DB.QueryContext(ctx, "SELECT session,source,last_activity,records,raw_bytes FROM archive_sessions WHERE archive_id=? ORDER BY session", job.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s ArchiveSession
		if err = rows.Scan(&s.ID, &s.Source, &s.LastActivity, &s.Records, &s.RawBytes); err != nil {
			break
		}
		entry := struct {
			ArchiveSession
			File string `json:"file"`
		}{s, archiveEntry(s.ID)}
		if err = json.NewEncoder(manifest).Encode(entry); err != nil {
			break
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return errors.Join(err, rowErr)
	}
	var processed int64
	lastSession := ""
	lastProgress := time.Now()
	for {
		var session string
		var revision, expected int64
		err = a.DB.QueryRowContext(ctx, "SELECT session,revision,records FROM archive_sessions WHERE archive_id=? AND session>? ORDER BY session LIMIT 1", job.ID, lastSession).Scan(&session, &revision, &expected)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		tx, e := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return e
		}
		var actual int64
		e = tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT revision FROM session_revisions WHERE session=?),0)", session).Scan(&actual)
		if e != nil || actual != revision {
			tx.Rollback()
			if e != nil {
				return e
			}
			return errArchiveStale
		}
		destination, e := archive.CreateHeader(&zip.FileHeader{Name: archiveEntry(session), Method: zip.Deflate})
		if e != nil {
			tx.Rollback()
			return e
		}
		records, e := tx.QueryContext(ctx, "SELECT "+eventColumns+" FROM events WHERE session=? ORDER BY origin,time", session)
		if e != nil {
			tx.Rollback()
			return e
		}
		encoder := json.NewEncoder(destination)
		var count int64
		for records.Next() {
			record, e := scanEvent(records)
			if e != nil {
				err = e
				break
			}
			if e = encoder.Encode(record); e != nil {
				err = e
				break
			}
			count++
			processed++
			if processed%200 == 0 || time.Since(lastProgress) > time.Second {
				result, e := a.DB.ExecContext(ctx, "UPDATE archives SET processed_records=?,archive_bytes=? WHERE id=? AND status='preparing'", processed, counter.n, job.ID)
				if e != nil {
					err = e
					break
				}
				n, _ := result.RowsAffected()
				if n == 0 {
					err = context.Canceled
					break
				}
				lastProgress = time.Now()
			}
			if ctx.Err() != nil {
				err = ctx.Err()
				break
			}
		}
		rowErr = records.Err()
		records.Close()
		tx.Rollback()
		if err != nil || rowErr != nil {
			return errors.Join(err, rowErr)
		}
		if count != expected {
			return errArchiveStale
		}
		lastSession = session
	}
	if processed != job.RecordCount {
		return errArchiveStale
	}
	if err = archive.Close(); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(partial, final); err != nil {
		return err
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	valid, err := snapshotsCurrent(ctx, tx, job.ID)
	if err != nil {
		return err
	}
	if !valid {
		return errArchiveStale
	}
	result, err := tx.ExecContext(ctx, "UPDATE archives SET status='ready',processed_records=?,archive_bytes=?,sha256=? WHERE id=? AND status='preparing'", processed, counter.n, hex.EncodeToString(digest.Sum(nil)), job.ID)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return context.Canceled
	}
	return tx.Commit()
}
func archiveEntry(session string) string {
	hash := sha256.Sum256([]byte(session))
	return "sessions/" + hex.EncodeToString(hash[:]) + ".jsonl"
}
func (a *App) deleteArchive(ctx context.Context, job ArchiveJob) error {
	// Verification is persisted before unlinking so interrupted, confirmed deletions can resume.
	var verified string
	if err := a.DB.QueryRowContext(ctx, "SELECT verified_at FROM archives WHERE id=?", job.ID).Scan(&verified); err != nil {
		return err
	}
	if file, err := os.Open(a.archivePath(job.ID, false)); err == nil {
		digest := sha256.New()
		size, err := io.Copy(digest, archiveReader{ctx: ctx, reader: file})
		file.Close()
		if err != nil {
			return err
		}
		if size != job.ArchiveBytes || hex.EncodeToString(digest.Sum(nil)) != job.SHA256 {
			return fmt.Errorf("archive integrity verification failed")
		}
		if _, err = a.DB.ExecContext(ctx, "UPDATE archives SET verified_at=? WHERE id=?", timestamp(time.Now()), job.ID); err != nil {
			return err
		}
		if err = os.Remove(a.archivePath(job.ID, false)); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) || verified == "" {
		return fmt.Errorf("verified archive is missing: %w", err)
	}

	for {
		var session string
		err := a.DB.QueryRowContext(ctx, "SELECT l.session FROM archive_locks l WHERE l.archive_id=? AND EXISTS(SELECT 1 FROM events e WHERE e.session=l.session) LIMIT 1", job.ID).Scan(&session)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		tx, err := a.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT id,raw FROM events WHERE session=? LIMIT 200", session)
		if err != nil {
			tx.Rollback()
			return err
		}
		type fingerprint struct {
			id              string
			idHash, rawHash [32]byte
		}
		records := []fingerprint{}
		var bytes int
		for rows.Next() {
			var id string
			var raw []byte
			if err = rows.Scan(&id, &raw); err != nil {
				break
			}
			records = append(records, fingerprint{id, sha256.Sum256([]byte(id)), sha256.Sum256(raw)})
			bytes += len(raw)
			if bytes >= 8<<20 {
				break
			}
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil || rowErr != nil {
			tx.Rollback()
			return errors.Join(err, rowErr)
		}
		for _, record := range records {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO archived_records(id_hash,raw_hash) VALUES(?,?)", record.idHash[:], record.rawHash[:]); err != nil {
				break
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM events WHERE id=?", record.id); err != nil {
				break
			}
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "UPDATE archives SET deleted_records=deleted_records+? WHERE id=?", len(records), job.ID)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if _, err = a.DB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return err
		}
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM archive_locks WHERE archive_id=?", job.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE archives SET status='compacting' WHERE id=?", job.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return a.compactArchive(ctx, job)
}
func (a *App) diskBytes() int64 {
	var size int64
	for _, path := range []string{a.dbPath, a.dbPath + "-wal"} {
		if info, err := os.Stat(path); err == nil {
			size += info.Size()
		}
	}
	return size
}
func (a *App) compactArchive(ctx context.Context, job ArchiveJob) error {
	var mode int
	err := a.DB.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode)
	var requested bool
	if err == nil {
		err = a.DB.QueryRowContext(ctx, "SELECT rebuild_requested FROM archives WHERE id=?", job.ID).Scan(&requested)
	}
	if err == nil && mode != 2 && !requested {
		_, err = a.DB.ExecContext(ctx, "UPDATE archives SET status='deleted',rebuild_required=1,compaction_error=? WHERE id=?", "Records deleted; freed pages are reusable. Rebuilding this existing database releases disk space, pauses ingestion, and can require up to twice the database size in free space.", job.ID)
		return err
	}
	if err == nil && mode != 2 {
		// Only an explicitly requested, one-time rebuild pauses all ingestion.
		a.compacting.Store(true)
		defer a.compacting.Store(false)
		var db *sql.DB
		db, err = a.archiveConnection(ctx, false)
		if err == nil {
			_, err = db.ExecContext(ctx, "PRAGMA auto_vacuum=INCREMENTAL; VACUUM")
			db.Close()
		}
	}
	// Share the normal single-connection pool so queued ingestion can run between
	// small vacuum/checkpoint calls rather than being rejected during reclamation.
	for err == nil {
		var free int
		if err = a.DB.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil || free == 0 {
			break
		}
		if _, err = a.DB.ExecContext(ctx, "PRAGMA incremental_vacuum(256)"); err != nil {
			break
		}
		_, err = a.DB.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
	}
	if err == nil {
		_, err = a.DB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	warning := ""
	if err != nil {
		log.Printf("archive %s compact: %v", job.ID, err)
		warning = "Records were deleted, but database compaction failed. Free disk space and retry compaction."
	}
	var before int64
	if e := a.DB.QueryRowContext(ctx, "SELECT database_bytes_before FROM archives WHERE id=?", job.ID).Scan(&before); e != nil {
		return e
	}
	if e := a.DB.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); e != nil {
		return e
	}
	_, err = a.DB.ExecContext(ctx, "UPDATE archives SET status='deleted',reclaimed_bytes=?,compaction_error=?,rebuild_required=? WHERE id=?", max(int64(0), before-a.diskBytes()), warning, mode != 2, job.ID)
	return err
}

func archiveFailure(err error) string {
	if errors.Is(err, errArchiveStale) {
		return errArchiveStale.Error()
	}
	return "Operation failed. Check the server log and available disk space."
}

type archiveReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r archiveReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
