package importer_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/importer"
)

func TestImportRetriesTheSameBatchUnderBackpressure(t *testing.T) {
	var attempts atomic.Int32
	var first []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if attempts.Add(1) == 1 {
			first = body
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			return
		}
		if string(body) != string(first) {
			t.Error("retry changed the batch")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "{}")
	}))
	defer server.Close()
	source := file(t, t.TempDir(), "session.jsonl", claudeLine("one", "retry same batch"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stats, err := importer.Run(ctx, importer.Options{Files: []string{source}, Endpoint: server.URL, Token: token}, io.Discard)
	if err != nil || stats.Records != 1 || attempts.Load() != 2 {
		t.Fatal(stats, err, attempts.Load())
	}
}

func TestImportBackpressureWaitIsCancelable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "30"); w.WriteHeader(503) }))
	defer server.Close()
	source := file(t, t.TempDir(), "session.jsonl", claudeLine("one", "cancel retry"))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := importer.Run(ctx, importer.Options{Files: []string{source}, Endpoint: server.URL, Token: token}, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("backpressure ignored cancellation", err)
	}
}
