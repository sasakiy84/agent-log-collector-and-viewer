package app

import (
	"context"
	"database/sql"
	"log"
	"os"
	"strconv"
)

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
	defer a.notifyArchive()
	var mode int
	err := a.mergeSearchIndex(ctx)
	if err == nil {
		err = a.DB.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode)
	}
	var requested bool
	if err == nil {
		err = a.DB.QueryRowContext(ctx, "SELECT rebuild_requested FROM archive_jobs WHERE id=?", job.ID).Scan(&requested)
	}
	if err == nil && mode != 2 && !requested {
		_, err = a.DB.ExecContext(ctx, "UPDATE archive_jobs SET status='deleted',rebuild_required=1,compaction_error=? WHERE id=?", "Records deleted; freed pages are reusable. Rebuilding this existing database releases disk space, pauses ingestion, and can require up to twice the database size in free space.", job.ID)
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
	// Reclaim the pages available when this job starts. Concurrent ingestion
	// can free more pages; following a moving freelist would keep the job active
	// indefinitely. New free pages remain reusable by subsequent writes/jobs.
	if err == nil {
		err = a.reclaimPages(ctx)
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
	if e := a.DB.QueryRowContext(ctx, "SELECT database_bytes_before FROM archive_jobs WHERE id=?", job.ID).Scan(&before); e != nil {
		return e
	}
	if e := a.DB.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); e != nil {
		return e
	}
	_, err = a.DB.ExecContext(ctx, "UPDATE archive_jobs SET status='deleted',reclaimed_bytes=?,compaction_error=?,rebuild_required=? WHERE id=?", max(int64(0), before-a.diskBytes()), warning, mode != 2, job.ID)
	return err
}

// Share the normal writer pool so ingestion runs between bounded batches.
func (a *App) reclaimPages(ctx context.Context) error {
	var remaining int
	if err := a.DB.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&remaining); err != nil {
		return err
	}
	for remaining > 0 {
		pages, err := a.vacuumBatch(ctx, min(256, remaining))
		if err != nil {
			return err
		}
		if pages == 0 {
			break
		}
		remaining -= pages
		if _, err = a.DB.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
			return err
		}
	}
	return nil
}

// incremental_vacuum yields a row after every reclaimed page. Exec stops after
// the first SQLITE_ROW in this driver; drain the result to finish the batch.
func (a *App) vacuumBatch(ctx context.Context, limit int) (int, error) {
	rows, err := a.DB.QueryContext(ctx, "PRAGMA incremental_vacuum("+strconv.Itoa(limit)+")")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	pages := 0
	for rows.Next() {
		pages++
	}
	return pages, rows.Err()
}
