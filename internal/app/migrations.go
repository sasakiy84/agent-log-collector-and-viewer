package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

func migrations() ([]migration, error) {
	files, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	out := make([]migration, 0, len(files))
	for _, file := range files {
		prefix, _, _ := strings.Cut(file.Name(), "_")
		version, err := strconv.Atoi(prefix)
		if err != nil || version != len(out)+1 {
			return nil, fmt.Errorf("invalid migration sequence: %s", file.Name())
		}
		body, err := migrationFiles.ReadFile("migrations/" + file.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version, file.Name(), string(body)})
	}
	return out, nil
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
