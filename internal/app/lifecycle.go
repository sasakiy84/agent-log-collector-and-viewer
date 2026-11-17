package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/checkpoint"
	"sync"
	"sync/atomic"
	"time"
)

type App struct {
	Version            string
	startedAt          time.Time
	inbox              *ingestQueue
	initialized        chan struct{}
	maintained         chan struct{}
	startupErr         error
	maintenanceErr     error
	DB                 *sql.DB
	readers            *sql.DB
	exportReaders      *sql.DB
	exportSlots        chan struct{}
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
	if a.exportReaders != nil && a.exportReaders != a.DB {
		err = errors.Join(err, a.exportReaders.Close())
	}
	if a.readers != nil && a.readers != a.DB {
		err = a.readers.Close()
	}
	if a.DB != nil {
		err = errors.Join(err, a.DB.Close())
	}
	if a.inbox != nil {
		err = errors.Join(err, a.inbox.close())
	}
	return err
}

func newApp(ctx context.Context, token string) (*App, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if token != "" && len(token) < 16 {
		return nil, fmt.Errorf("AGENTLOG_TOKEN must be empty or contain at least 16 characters")
	}
	ctx, cancel := context.WithCancel(ctx)
	return &App{Version: "dev", startedAt: time.Now(), ctx: ctx, cancel: cancel, token: token, exportSlots: make(chan struct{}, maxConcurrentExports)}, nil
}
func (a *App) WatchState() *checkpoint.Store {
	if a.inbox == nil {
		return nil
	}
	return a.inbox.checkpoints
}
