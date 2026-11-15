package app

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
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
				e := inTransaction(a.ctx, a.DB, func(tx *sql.Tx) error {
					if _, err := tx.ExecContext(a.ctx, "DELETE FROM archive_locks WHERE archive_id=?", job.ID); err != nil {
						return err
					}
					if _, err := tx.ExecContext(a.ctx, "UPDATE archives SET status='failed',error=? WHERE id=? AND status IN ('preparing','deleting','compacting')", archiveFailure(err), job.ID); err != nil {
						return err
					}
					return refreshProjections(a.ctx, tx)
				})
				if e != nil {
					log.Printf("archive failure state: %v", e)
					break
				}
				os.Remove(a.archivePath(job.ID, true))
				os.Remove(a.archivePath(job.ID, false))
			}
			a.notifyArchive()
		}
	}
}
func (a *App) archiveConnection(ctx context.Context, readOnly bool) (*sql.DB, error) {
	uri, err := databaseURI(a.dbPath, readOnly)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func archiveFailure(err error) string {
	if errors.Is(err, errArchiveStale) {
		return errArchiveStale.Error()
	}
	return "Operation failed. Check the server log and available disk space."
}
