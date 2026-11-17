// Package checkpoint persists successful file imports independently of the view database.
package checkpoint

import (
	"context"
	"database/sql"
	"encoding/json"
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
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS watch_records(key TEXT NOT NULL,id TEXT NOT NULL,hash BLOB NOT NULL,PRIMARY KEY(key,id)) WITHOUT ROWID`); err != nil {
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

// Record hashes describe acknowledged normalized records, never source text.
type Record struct {
	ID   string
	Hash [32]byte
}

func (s *Store) Records(ctx context.Context, key string, records []Record) (map[string][32]byte, error) {
	ids := make([]string, len(records))
	for i, r := range records {
		ids[i] = r.ID
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,hash FROM watch_records WHERE key=? AND id IN(SELECT value FROM json_each(?))", key, string(encoded))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][32]byte{}
	for rows.Next() {
		var id string
		var value []byte
		if err := rows.Scan(&id, &value); err != nil {
			return nil, err
		}
		var hash [32]byte
		copy(hash[:], value)
		out[id] = hash
	}
	return out, rows.Err()
}

func (s *Store) PutRecords(ctx context.Context, key string, records []Record) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO watch_records VALUES(?,?,?) ON CONFLICT(key,id) DO UPDATE SET hash=excluded.hash")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range records {
		if _, err = stmt.ExecContext(ctx, key, r.ID, r.Hash[:]); err != nil {
			return err
		}
	}
	return tx.Commit()
}
