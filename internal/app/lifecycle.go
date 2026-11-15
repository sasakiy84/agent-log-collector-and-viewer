package app

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
)

type App struct {
	DB                 *sql.DB
	readers            *sql.DB
	token              string
	dbPath, archiveDir string
	ctx                context.Context
	cancel             context.CancelFunc
	workers            sync.WaitGroup
	archiveWake        chan struct{}
	archiveMu          sync.Mutex
	archiveChange      chan struct{}
	taskMu             sync.Mutex
	taskID             string
	taskCancel         context.CancelFunc
	compacting         atomic.Bool
}

func (a *App) Close() error {
	a.cancel()
	a.workers.Wait()
	var err error
	if a.readers != nil && a.readers != a.DB {
		err = a.readers.Close()
	}
	return errors.Join(err, a.DB.Close())
}
