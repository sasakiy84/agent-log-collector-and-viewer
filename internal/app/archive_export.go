package app

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

type byteCounter struct{ n int64 }

func (c *byteCounter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }
func (a *App) buildArchive(ctx context.Context, job ArchiveJob) (err error) {
	defer a.notifyArchive()
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
	rows, err := a.DB.QueryContext(ctx, "SELECT session,source,last_activity,records,raw_bytes,folder FROM archive_sessions WHERE archive_id=? ORDER BY session", job.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s ArchiveSession
		if err = rows.Scan(&s.ID, &s.Source, &s.LastActivity, &s.Records, &s.RawBytes, &s.Folder); err != nil {
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
				a.notifyArchive()
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
