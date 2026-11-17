package app

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"
)

// Start opens the durable inbox before validating the view database. HTTP can
// acknowledge committed batches while schema initialization is in progress.
func Start(ctx context.Context, path, token string) (*App, error) {
	a, err := newApp(ctx, token)
	if err != nil {
		return nil, err
	}
	a.initialized = make(chan struct{})
	a.maintained = make(chan struct{})
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
		a.maintenanceErr = err
		close(a.maintained)
		if err != nil {
			if a.ctx.Err() == nil {
				log.Printf("database preparation paused: %v; incoming telemetry remains queued", err)
			}
			return
		}
		log.Print("Database ready; processing durable inbox")
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

type serviceStatus struct {
	State               string `json:"state"`
	PendingBatches      int64  `json:"pending_batches"`
	PendingBytes        int64  `json:"pending_bytes"`
	QueueCapacityBytes  int64  `json:"queue_capacity_bytes"`
	ImportCapacityBytes int64  `json:"import_capacity_bytes"`
}

func (a *App) statusSnapshot(ctx context.Context) (serviceStatus, error) {
	s := serviceStatus{State: "ready", QueueCapacityBytes: maxInboxBytes, ImportCapacityBytes: maxImportBytes}
	if !closed(a.initialized) {
		s.State = "migrating"
	} else if a.startupErr != nil {
		s.State = "failed"
	} else if !closed(a.maintained) {
		s.State = "preparing"
	} else if a.maintenanceErr != nil {
		s.State = "failed"
	}
	if a.inbox != nil {
		var err error
		s.PendingBatches, s.PendingBytes, err = a.inbox.size(ctx)
		if err != nil {
			return s, err
		}
	}
	return s, nil
}
func (a *App) status(w http.ResponseWriter, r *http.Request) {
	s, err := a.statusSnapshot(r.Context())
	if err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, s)
}
