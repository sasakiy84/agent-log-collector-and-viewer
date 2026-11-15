package app

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
)

const (
	usageJob       = "usage.v1"
	agentsJob      = "agents.v1"
	skillsJob      = "skills.v1"
	directoriesJob = "directories.v1"
	titlesJob      = "titles.v1"
)

type maintenanceBatch func(context.Context, *sql.Tx, string) (cursor string, processed int, err error)

// Dirty queues are their own checkpoints: derived writes and queue removal
// commit together. Bound both row count (in SQL) and original payload memory.
func loadEventBatch(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]Event, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	batch, size := []Event{}, 0
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		batch = append(batch, event)
		size += len(event.Raw)
		if size >= 8<<20 {
			break
		}
	}
	return batch, rows.Err()
}

func (a *App) drainBatches(ctx context.Context, name string, batch func(context.Context, *sql.Tx) (int, error)) error {
	processed, lastReport := 0, time.Now()
	for {
		count := 0
		err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
			var err error
			count, err = batch(ctx, tx)
			return err
		})
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if count == 0 {
			if processed > 0 {
				log.Printf("Prepared %s: %d records", name, processed)
			}
			return nil
		}
		processed += count
		if err := a.pauseMaintenance(ctx); err != nil {
			return err
		}
		if time.Since(lastReport) >= 5*time.Second {
			log.Printf("Preparing %s: %d records", name, processed)
			lastReport = time.Now()
		}
	}
}

// Each batch and its checkpoint share a transaction. Versioned names identify
// the extraction logic, so future changes schedule a new job rather than
// silently reusing a completed checkpoint. Empty batches mark completion.
func (a *App) runMaintenance(ctx context.Context, name string, batch maintenanceBatch, pause time.Duration) error {
	lastReport := time.Now()
	started := false
	for {
		complete, processed := false, 0
		err := inTransaction(ctx, a.DB, func(tx *sql.Tx) error {
			var cursor string
			if err := tx.QueryRowContext(ctx, "SELECT cursor,complete,processed FROM maintenance_jobs WHERE name=?", name).Scan(&cursor, &complete, &processed); err != nil {
				return err
			}
			if complete {
				return nil
			}
			if !started {
				log.Printf("Maintenance %s started (checkpoint: %d records)", name, processed)
				started = true
			}
			next, count, err := batch(ctx, tx, cursor)
			if err != nil {
				return err
			}
			if count < 0 || count > 0 && next <= cursor {
				return fmt.Errorf("batch made no forward progress")
			}
			complete = count == 0
			processed += count
			if complete {
				next = cursor
			}
			_, err = tx.ExecContext(ctx, "UPDATE maintenance_jobs SET cursor=?,complete=?,processed=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),error='' WHERE name=?", next, complete, processed, name)
			return err
		})
		if err != nil {
			if ctx.Err() == nil {
				// Keep the last good checkpoint, and record failure for operators.
				_, recordErr := a.DB.ExecContext(ctx, "UPDATE maintenance_jobs SET error=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE name=?", err.Error(), name)
				if recordErr != nil {
					log.Printf("Maintenance %s could not record failure: %v", name, recordErr)
				}
			}
			return fmt.Errorf("maintenance %s: %w", name, err)
		}
		if complete {
			if started {
				log.Printf("Maintenance %s complete (%d records)", name, processed)
			}
			return nil
		}
		if time.Since(lastReport) >= 5*time.Second {
			log.Printf("Maintenance %s: %d records", name, processed)
			lastReport = time.Now()
		}
		pause = max(pause, a.maintenancePause)
		if pause > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pause):
			}
		}
	}
}

func (a *App) pauseMaintenance(ctx context.Context) error {
	if a.maintenancePause == 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(a.maintenancePause):
		return nil
	}
}
