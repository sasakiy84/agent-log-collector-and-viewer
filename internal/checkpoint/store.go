// Package checkpoint persists successful file imports independently of the view database.
package checkpoint

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db    *sql.DB
	owned bool
}

func New(ctx context.Context, db *sql.DB) (*Store, error) {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS watch_files(key TEXT PRIMARY KEY,value BLOB NOT NULL) WITHOUT ROWID`); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}
func Open(ctx context.Context, path string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && uriPath[0] != '/' {
		uriPath = "/" + uriPath
	}
	uri := (&url.URL{Scheme: "file", Path: uriPath}).String()
	db, err := sql.Open("sqlite", uri+"?_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	store, err := New(ctx, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	store.owned = true
	return store, nil
}
func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx, "SELECT value FROM watch_files WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return value, err
}
func (s *Store) Put(ctx context.Context, key string, value []byte) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO watch_files VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}
func (s *Store) Close() error {
	if s.owned {
		return s.db.Close()
	}
	return nil
}
