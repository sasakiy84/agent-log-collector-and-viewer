package app

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Every replacement connection must retain foreign keys and the busy timeout.
// Writer transactions acquire their reservation before reading a checkpoint.
func databaseURI(path string, readOnly bool) (string, error) {
	address := ":memory:"
	query := url.Values{"_pragma": {"busy_timeout(5000)"}}
	if readOnly {
		query.Set("mode", "ro")
		query.Add("_pragma", "query_only(1)")
	} else {
		query.Set("_txlock", "immediate")
		query.Add("_pragma", "foreign_keys(1)")
	}
	if path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		uriPath := filepath.ToSlash(absolute)
		if filepath.VolumeName(absolute) != "" && uriPath[0] != '/' {
			uriPath = "/" + uriPath
		}
		address = (&url.URL{Scheme: "file", Path: uriPath}).String()
	}
	return address + "?" + query.Encode(), nil
}

func (a *App) openDatabase(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path != ":memory:" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
	}
	uri, err := databaseURI(path, false)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	a.DB = db
	if err = validateStorage(ctx, db); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "PRAGMA auto_vacuum=INCREMENTAL; PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	plan, err := migrations()
	if err != nil {
		return err
	}
	if err = migrate(ctx, db, plan); err != nil {
		return err
	}
	return nil
}

// prepareDatabase recovers only persisted invalidations. New-schema databases
// are fully materialized by the Python converter; normal restarts never scan
// originals or run historical format upgrades.
func (a *App) prepareDatabase(ctx context.Context) error {
	return inTransaction(ctx, a.DB, func(tx *sql.Tx) error { return refreshProjections(ctx, tx) })
}

// Open prepares a database synchronously for embedded callers and maintenance tools.
// The HTTP service uses Start, which accepts durable ingestion during preparation.
func Open(ctx context.Context, path, token string) (*App, error) {
	a, err := newApp(ctx, token)
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			a.Close()
		}
	}()
	if err = a.openDatabase(ctx, path); err != nil {
		return nil, err
	}
	if err = a.prepareDatabase(ctx); err != nil {
		return nil, err
	}
	if err = a.initReaders(path); err != nil {
		return nil, err
	}
	if err = a.initArchives(path); err != nil {
		return nil, err
	}
	ready = true
	return a, nil
}
