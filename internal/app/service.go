package app

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"
)

// Start opens the durable inbox before preparing the view database. HTTP can
// acknowledge committed batches even while migrations or historical jobs run.
func Start(ctx context.Context, path, token string) (*App, error) {
	a, err := newApp(ctx, token)
	if err != nil {
		return nil, err
	}
	a.initialized = make(chan struct{})
	a.maintained = make(chan struct{})
	a.maintenancePause = 10 * time.Millisecond
	if a.inbox, err = openIngestQueue(a.ctx, path); err != nil {
		a.Close()
		return nil, err
	}
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		err := a.openDatabase(a.ctx, path)
		if err == nil {
			err = a.initReaders(path)
		}
		a.startupErr = err
		close(a.initialized)
		if err == nil {
			err = a.prepareDatabase(a.ctx)
		}
		if err == nil {
			err = a.initArchives(path)
		}
		if err == nil {
			err = a.startSkillIndexer()
		}
		if err == nil {
			err = a.startProcessingIndexer()
		}
		a.maintenanceErr = err
		close(a.maintained)
		if err != nil {
			if a.ctx.Err() == nil {
				log.Printf("database preparation paused: %v; incoming telemetry remains queued", err)
			}
			return
		}
		log.Print("Database preparation complete; processing durable inbox")
		a.inbox.process(a.Save)
	}()
	return a, nil
}

// Embedded callers save synchronously. The server acknowledges the queue's
// durable commit and projects events independently of the HTTP request.
func (a *App) accept(ctx context.Context, events []Event, wait bool) error {
	if a.inbox == nil {
		return a.Save(ctx, events)
	}
	return a.inbox.accept(ctx, events, wait)
}

func closed(channel <-chan struct{}) bool {
	if channel == nil {
		return true
	}
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

func (a *App) serviceAvailable(w http.ResponseWriter, r *http.Request) bool {
	// Let the first page request wait briefly for the small schema/read-pool
	// initialization, without waiting for historical preparation or client polling.
	if !closed(a.initialized) {
		select {
		case <-a.initialized:
		case <-r.Context().Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
	if !closed(a.initialized) || a.startupErr != nil {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "Database is preparing; telemetry reception is available", 503)
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/archive") && (!closed(a.maintained) || a.maintenanceErr != nil) {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "Archive operations are waiting for database preparation", 503)
		return false
	}
	return true
}

func (a *App) status(w http.ResponseWriter, r *http.Request) {
	state := "ready"
	if !closed(a.initialized) {
		state = "migrating"
	} else if a.startupErr != nil {
		state = "failed"
	} else if !closed(a.maintained) {
		state = "preparing"
	} else if a.maintenanceErr != nil {
		state = "failed"
	}
	var batches, bytes int64
	if a.inbox != nil {
		var err error
		batches, bytes, err = a.inbox.size(r.Context())
		if err != nil {
			databaseError(w, r, err)
			return
		}
	}
	write(w, map[string]any{"state": state, "pending_batches": batches, "pending_bytes": bytes, "queue_capacity_bytes": maxInboxBytes, "import_capacity_bytes": maxImportBytes})
}
