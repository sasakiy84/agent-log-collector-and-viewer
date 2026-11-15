package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/checkpoint"
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
	a.queueWake = make(chan struct{}, 1)
	a.queueSpace = make(chan struct{}, 1)
	a.maintenancePause = 10 * time.Millisecond
	if err = a.openInbox(path); err != nil {
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
		a.maintenanceErr = err
		close(a.maintained)
		if err != nil {
			if a.ctx.Err() == nil {
				log.Printf("database preparation paused: %v; incoming telemetry remains queued", err)
			}
			return
		}
		log.Print("Database preparation complete; processing durable inbox")
		a.processInbox()
	}()
	return a, nil
}

const maxInboxBytes = 256 << 20
const maxImportBytes = maxInboxBytes / 2

var errInboxFull = errors.New("ingestion queue is full")

func (a *App) openInbox(path string) error {
	inboxPath := path + ".ingest.db"
	if path == ":memory:" {
		inboxPath = path
	} else {
		file, err := os.OpenFile(inboxPath, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
	}
	uri, err := databaseURI(inboxPath, false)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", uri+"&_pragma=synchronous(FULL)")
	if err != nil {
		return err
	}
	a.inbox = db
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(a.ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	if err = migrate(a.ctx, db, inboxMigrations); err != nil {
		return err
	}
	a.checkpoints, err = checkpoint.New(a.ctx, db)
	return err
}

func (a *App) accept(ctx context.Context, events []Event, wait bool) error {
	if a.inbox == nil {
		return a.Save(ctx, events)
	}
	if len(events) == 0 {
		return nil
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		return err
	}
	limit := maxInboxBytes
	imported := len(events) > 0
	for _, event := range events {
		if event.Origin != "import" {
			imported = false
			break
		}
	}
	if wait || imported {
		limit = maxImportBytes
	}
	if len(encoded) > limit {
		return fmt.Errorf("batch exceeds ingestion queue capacity")
	}

	for {
		err = inTransaction(ctx, a.inbox, func(tx *sql.Tx) error {
			var size int
			if err := tx.QueryRowContext(ctx, "SELECT bytes FROM ingest_size WHERE id=1").Scan(&size); err != nil {
				return err
			}
			if size+len(encoded) > limit {
				return errInboxFull
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO ingest_batches(data) VALUES(?)", encoded)
			return err
		})
		if !errors.Is(err, errInboxFull) || !wait {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.ctx.Done():
			return a.ctx.Err()
		case <-a.queueSpace:
		case <-time.After(time.Second):
		}
	}
	if err == nil {
		select {
		case a.queueWake <- struct{}{}:
		default:
		}
	}
	return err
}

func (a *App) processInbox() {
	for a.ctx.Err() == nil {
		var id int64
		var data []byte
		err := a.inbox.QueryRowContext(a.ctx, "SELECT id,data FROM ingest_batches ORDER BY id LIMIT 1").Scan(&id, &data)
		if errors.Is(err, sql.ErrNoRows) {
			select {
			case <-a.ctx.Done():
				return
			case <-a.queueWake:
			}
			continue
		}
		if err == nil {
			var events []Event
			err = json.Unmarshal(data, &events)
			if err == nil {
				err = a.Save(a.ctx, events)
			}
			// A crash after Save but before DELETE replays the same stable IDs.
			if err == nil {
				_, err = a.inbox.ExecContext(a.ctx, "DELETE FROM ingest_batches WHERE id=?", id)
			}
		}
		if err != nil {
			if a.ctx.Err() != nil {
				return
			}
			log.Printf("durable inbox batch %d retained for retry: %v", id, err)
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		} else {
			select {
			case a.queueSpace <- struct{}{}:
			default:
			}
		}
	}
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
	if r.Method == "POST" && r.URL.Path == "/api/archives" && a.inbox != nil {
		var pending bool
		if err := a.inbox.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM ingest_batches)").Scan(&pending); err != nil {
			databaseError(w, r, err)
			return false
		}
		if pending {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Telemetry is still being indexed; retry after the queue drains", 409)
			return false
		}
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
		if err := a.inbox.QueryRowContext(r.Context(), "SELECT batches,bytes FROM ingest_size WHERE id=1").Scan(&batches, &bytes); err != nil {
			databaseError(w, r, err)
			return
		}
	}
	write(w, map[string]any{"state": state, "pending_batches": batches, "pending_bytes": bytes, "queue_capacity_bytes": maxInboxBytes, "import_capacity_bytes": maxImportBytes})
}
