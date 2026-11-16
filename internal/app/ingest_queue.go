package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/checkpoint"
)

// The durable queue owns acceptance, capacity, restart replay and watch
// checkpoints. It does not know how view storage projects or presents events.
type ingestQueue struct {
	db          *sql.DB
	ctx         context.Context
	checkpoints *checkpoint.Store
	wake, space chan struct{}
}

const maxInboxBytes = 256 << 20
const maxImportBytes = maxInboxBytes / 2

var errInboxFull = errors.New("ingestion queue is full")

func openIngestQueue(ctx context.Context, path string) (*ingestQueue, error) {
	inboxPath := path + ".ingest.db"
	if path == ":memory:" {
		inboxPath = path
	} else {
		file, err := os.OpenFile(inboxPath, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = file.Close(); err != nil {
			return nil, err
		}
	}
	uri, err := databaseURI(inboxPath, false)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", uri+"&_pragma=synchronous(FULL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ready := false
	defer func() {
		if !ready {
			db.Close()
		}
	}()
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	if err = migrate(ctx, db, inboxMigrations); err != nil {
		return nil, err
	}
	checkpoints, err := checkpoint.New(ctx, db)
	if err != nil {
		return nil, err
	}
	ready = true
	return &ingestQueue{db: db, ctx: ctx, checkpoints: checkpoints, wake: make(chan struct{}, 1), space: make(chan struct{}, 1)}, nil
}

func (q *ingestQueue) accept(ctx context.Context, events []Event, wait bool) error {
	if len(events) == 0 {
		return nil
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		return err
	}
	limit := maxInboxBytes
	imported := true
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
		err = inTransaction(ctx, q.db, func(tx *sql.Tx) error {
			var size int
			if err := tx.QueryRowContext(ctx, "SELECT bytes FROM ingest_size WHERE id=1").Scan(&size); err != nil {
				return err
			}
			if size+len(encoded) > limit {
				return errInboxFull
			}
			result, err := tx.ExecContext(ctx, "INSERT INTO ingest_batches(data) VALUES(?)", encoded)
			if err != nil {
				return err
			}
			id, err := result.LastInsertId()
			if err != nil {
				return err
			}
			sessions := map[string]bool{}
			for _, event := range events {
				sessions[event.Session] = true
			}
			for session := range sessions {
				if _, err = tx.ExecContext(ctx, "INSERT INTO ingest_sessions VALUES(?,?)", id, session); err != nil {
					return err
				}
			}
			return nil
		})
		if !errors.Is(err, errInboxFull) || !wait {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-q.ctx.Done():
			return q.ctx.Err()
		case <-q.space:
		case <-time.After(time.Second):
		}
	}
	if err == nil {
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
	return err
}

func (q *ingestQueue) process(save func(context.Context, []Event) error) {
	for q.ctx.Err() == nil {
		var id int64
		var data []byte
		err := q.db.QueryRowContext(q.ctx, "SELECT id,data FROM ingest_batches ORDER BY id LIMIT 1").Scan(&id, &data)
		if errors.Is(err, sql.ErrNoRows) {
			select {
			case <-q.ctx.Done():
				return
			case <-q.wake:
			}
			continue
		}
		if err == nil {
			var events []Event
			err = json.Unmarshal(data, &events)
			if err == nil {
				err = save(q.ctx, events)
			}
			// A crash after Save but before DELETE replays the same stable IDs.
			if err == nil {
				_, err = q.db.ExecContext(q.ctx, "DELETE FROM ingest_batches WHERE id=?", id)
			}
		}
		if err != nil {
			if q.ctx.Err() != nil {
				return
			}
			log.Printf("durable inbox batch %d retained for retry: %v", id, err)
			select {
			case <-q.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		} else {
			select {
			case q.space <- struct{}{}:
			default:
			}
		}
	}
}

func (q *ingestQueue) close() error { return q.db.Close() }

func (q *ingestQueue) size(ctx context.Context) (batches, bytes int64, err error) {
	err = q.db.QueryRowContext(ctx, "SELECT batches,bytes FROM ingest_size WHERE id=1").Scan(&batches, &bytes)
	return
}

func (q *ingestQueue) hasSessions(ctx context.Context, sessions []string) (bool, error) {
	if len(sessions) == 0 {
		return false, nil
	}
	args := make([]any, len(sessions))
	for i, session := range sessions {
		args[i] = session
	}
	var pending bool
	err := q.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM ingest_sessions WHERE session IN ("+strings.TrimRight(strings.Repeat("?,", len(args)), ",")+"))", args...).Scan(&pending)
	return pending, err
}
