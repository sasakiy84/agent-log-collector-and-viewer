package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"log"
)

//go:embed schema.sql
var storageSchema string

//go:embed performance.sql
var performanceSchema string

//go:embed skill_attempts.sql
var skillAttemptsSchema string

//go:embed work_conflict.sql
var workConflictSchema string

const storageApplicationID = 1095518002 // ALG2

// Generation 2 starts with one complete baseline. Future changes append migrations;
// released baselines remain immutable and independently verifiable by Python.
func migrations() ([]migration, error) {
	return []migration{{1, "0001_storage_v2", storageSchema}, {2, "0002_storage_performance", performanceSchema}, {3, "0003_skill_attempts", skillAttemptsSchema}, {4, "0004_work_conflict", workConflictSchema}}, nil
}

type migration struct {
	version int
	name    string
	sql     string
}

// Check identity before writing any DDL or changing journal mode. The offline
// Python tool owns conversion of old databases; the service never guesses.
func validateStorage(ctx context.Context, db *sql.DB) error {
	var applicationID, objects int
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); err != nil {
		return err
	}
	if applicationID == storageApplicationID {
		var generation int
		if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&generation); err != nil {
			return err
		}
		if generation != 1 {
			return fmt.Errorf("unsupported storage generation %d", generation)
		}
		return nil
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&objects); err != nil {
		return err
	}
	if applicationID == 0 && objects == 0 {
		return nil
	}
	return fmt.Errorf("unsupported database format: migrate the stopped v0.23.0 database with uv run scripts/migrate_db.py before starting this version")
}

// DDL and its history entry commit together. SQL files are immutable: an edited,
// missing or future migration is an error, never an invitation to rebuild data.
func migrate(ctx context.Context, db *sql.DB, plan []migration) error {
	for i, m := range plan {
		if m.version != i+1 {
			return fmt.Errorf("invalid migration plan at version %d", m.version)
		}
	}
	return inTransaction(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(
 version INTEGER PRIMARY KEY,name TEXT NOT NULL,checksum TEXT NOT NULL,applied_at TEXT NOT NULL)`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT version,name,checksum FROM schema_migrations ORDER BY version")
		if err != nil {
			return err
		}
		applied := 0
		for rows.Next() {
			var version int
			var name, checksum string
			if err = rows.Scan(&version, &name, &checksum); err != nil {
				break
			}
			if version != applied+1 || version > len(plan) {
				err = fmt.Errorf("unsupported migration history at version %d (binary supports %d)", version, len(plan))
				break
			}
			m := plan[applied]
			if name != m.name || checksum != fmt.Sprintf("%x", sha256.Sum256([]byte(m.sql))) {
				err = fmt.Errorf("migration %d differs from applied history", version)
				break
			}
			applied++
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if rowErr != nil {
			return rowErr
		}
		for _, m := range plan[applied:] {
			log.Printf("Database migration %04d: %s", m.version, m.name)
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("migration %04d %s: %w", m.version, m.name, err)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,name,checksum,applied_at) VALUES(?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))", m.version, m.name, fmt.Sprintf("%x", sha256.Sum256([]byte(m.sql)))); err != nil {
				return err
			}
		}
		return nil
	})
}

func inTransaction(ctx context.Context, db *sql.DB, run func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = run(tx); err != nil {
		return err
	}
	return tx.Commit()
}
