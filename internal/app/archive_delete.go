package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

func (a *App) deleteArchive(ctx context.Context, job ArchiveJob) error {
	defer a.notifyArchive()
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
		rows, err := tx.QueryContext(ctx, "SELECT id,raw,name,origin,source FROM events WHERE session=? LIMIT 200", session)
		if err != nil {
			tx.Rollback()
			return err
		}
		type fingerprint struct {
			id              string
			idHash, rawHash [32]byte
			legacyHash      []byte
		}
		records := []fingerprint{}
		var bytes int
		for rows.Next() {
			var id string
			var name, origin, source string
			var raw []byte
			if err = rows.Scan(&id, &raw, &name, &origin, &source); err != nil {
				break
			}
			record := fingerprint{id: id, idHash: sha256.Sum256([]byte(id)), rawHash: sha256.Sum256(raw)}
			if origin == "import" && name == "gen_ai.client.operation" && (source == "codex" || source == "claude") {
				if doc, e := decodeDocument(raw); e == nil {
					if legacy, ok := legacyUsageRaw(doc); ok {
						hash := sha256.Sum256(legacy)
						record.legacyHash = append([]byte(nil), hash[:]...)
					}
				}
			}
			records = append(records, record)
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
			if record.legacyHash != nil {
				if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO archived_records(id_hash,raw_hash) VALUES(?,?)", record.idHash[:], record.legacyHash); err != nil {
					break
				}
				// Legacy projections from usage-aware archives only suppress an
				// exact old-format replay. New-format counter/response-ID edits
				// are compared against their complete current-format hash.
				if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO archived_usage_records VALUES(?,?),(?,?)", record.idHash[:], record.rawHash[:], record.idHash[:], record.legacyHash); err != nil {
					break
				}
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
		a.notifyArchive()
		// A concurrent export can retain an older WAL snapshot for minutes.
		// Do not spend the writer's busy timeout waiting for it after every
		// deletion batch; compaction handles final truncation separately.
		if _, err = a.DB.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
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
	if err = refreshProjections(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE archives SET status='compacting' WHERE id=?", job.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.notifyArchive()
	return a.compactArchive(ctx, job)
}
