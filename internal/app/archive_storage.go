package app

import (
	"errors"
	"io/fs"
	"path/filepath"
)

func (a *App) archiveAvailableBytes() *uint64 {
	if a.dbPath == ":memory:" {
		return nil
	}
	available, err := filesystemAvailableBytes(a.archiveDir)
	if errors.Is(err, fs.ErrNotExist) {
		// The first archive creates this directory beside the database.
		available, err = filesystemAvailableBytes(filepath.Dir(a.dbPath))
	}
	if err != nil {
		return nil
	}
	return &available
}

func (a *App) databaseAvailableBytes() *uint64 {
	if a.dbPath == ":memory:" {
		return nil
	}
	available, err := filesystemAvailableBytes(filepath.Dir(a.dbPath))
	if err != nil {
		return nil
	}
	return &available
}
