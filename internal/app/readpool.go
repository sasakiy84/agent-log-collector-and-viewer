package app

import (
	"database/sql"
)

// WAL readers use their own connections, so a write batch cannot monopolize
// navigation or the archive status endpoint. The database enforces read-only.
func (a *App) initReaders(path string) error {
	if path == ":memory:" {
		a.readers = a.DB
		return nil
	}
	address, err := databaseURI(path, true)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", address)
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
