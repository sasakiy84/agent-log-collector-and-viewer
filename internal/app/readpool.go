package app

import (
	"database/sql"
	"net/url"
	"path/filepath"
)

// WAL readers use their own connections, so a write batch cannot monopolize
// navigation or the archive status endpoint. The database enforces read-only.
func (a *App) initReaders(path string) error {
	if path == ":memory:" {
		a.readers = a.DB
		return nil
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && uriPath[0] != '/' {
		uriPath = "/" + uriPath
	}
	address := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "query_only(1)"}}
	address.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", address.String())
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err = db.Ping(); err != nil {
		db.Close()
		return err
	}
	a.readers = db
	return nil
}
