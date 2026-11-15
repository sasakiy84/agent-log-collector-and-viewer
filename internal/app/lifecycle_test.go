package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCanceledOpenDoesNotTouchDatabase(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "canceled.db")
	if _, err := Open(ctx, path, token); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled startup created a database: %v", err)
	}
}

func TestCanceledReceiverDoesNotWaitForWriter(t *testing.T) {
	a := setup(t)
	// Pin the only writer connection, as a maintenance/ingestion batch would.
	conn, err := a.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/v1/logs", strings.NewReader(logBody("codex", "canceled", "", ""))).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { a.Handler().ServeHTTP(w, r); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		// Release before failing, so a broken implementation cannot leak a worker.
		conn.Close()
		<-done
		t.Fatal("canceled receiver kept waiting for the writer")
	}
	if w.Code != 503 {
		t.Fatalf("canceled receiver acknowledged a write: %d", w.Code)
	}
	conn.Close()
	if countEvents(t, a) != 0 {
		t.Fatal("canceled receiver published records")
	}
}

func TestParentCancellationStopsApplicationWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := Open(ctx, filepath.Join(t.TempDir(), "lifetime.db"), token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cancel()
	select {
	case <-a.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("application lifetime was detached from its caller")
	}
	done := make(chan struct{})
	go func() { a.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("application workers did not stop")
	}
}
