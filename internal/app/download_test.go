package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type delayedDownloadWriter struct {
	http.ResponseWriter
	delayed bool
}

type pausedDownloadWriter struct {
	*httptest.ResponseRecorder
	entered chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (w *pausedDownloadWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { w.entered <- struct{}{}; <-w.release })
	return w.ResponseRecorder.Write(p)
}

func TestSlowExportsLeaveNavigationAvailableAndLimitConcurrency(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "download", "otel", "codex", "download", "codex.user_prompt", map[string]any{"prompt": "hello"}))
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var workers sync.WaitGroup
	for _, path := range []string{"/api/export?session=codex:download", "/api/export/download"} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			a.Handler().ServeHTTP(&pausedDownloadWriter{ResponseRecorder: httptest.NewRecorder(), entered: entered, release: release}, r)
		}()
	}
	defer workers.Wait()
	defer close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("download did not start")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r := httptest.NewRequest("GET", "/api/sessions", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("exports blocked navigation", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/api/export?session=codex:download", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	a.Handler().ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatal("export concurrency is not bounded", w.Code)
	}
}

type deadlineDownloadWriter struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineDownloadWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}
func TestDownloadWriteDeadlineTracksProgress(t *testing.T) {
	w := &deadlineDownloadWriter{ResponseRecorder: httptest.NewRecorder()}
	stream := prepareDownload(w, "application/json", "test.json")
	stream.Write([]byte("first"))
	stream.Write([]byte("second"))
	if len(w.deadlines) != 3 {
		t.Fatal("deadline must refresh for each write", w.deadlines)
	}
	for _, d := range w.deadlines {
		if d.IsZero() || time.Until(d) <= 0 || time.Until(d) > downloadIdleTimeout {
			t.Fatal("invalid idle deadline", d)
		}
	}
}

func (w *delayedDownloadWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *delayedDownloadWriter) Write(p []byte) (int, error) {
	if !w.delayed {
		w.delayed = true
		// Model compression or a large row delaying the first response body
		// beyond an ordinary API request's total write deadline.
		time.Sleep(100 * time.Millisecond)
	}
	return w.ResponseWriter.Write(p)
}

func TestExportsOutliveOrdinaryServerWriteTimeout(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "first", "otel", "codex", "download", "user_prompt", map[string]any{"prompt": "Export this session", "service.namespace": "project"}))
	handler := a.Handler()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(&delayedDownloadWriter{ResponseWriter: w}, r)
	}))
	server.Config.WriteTimeout = 25 * time.Millisecond
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	for _, path := range []string{"/api/export?session=codex:download", "/api/export/download?project=name:project"} {
		t.Run(path, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+token)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal("export could not outlive the ordinary write deadline", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != http.StatusOK {
				t.Fatal("download was interrupted", response.StatusCode, err)
			}
			if response.Header.Get("Content-Type") == "application/zip" {
				_, events := decodeExport(t, body)
				if len(events) != 1 || events[0].ID != "first" {
					t.Fatal("incomplete ZIP", events)
				}
			} else {
				var events []Event
				if err = json.Unmarshal(body, &events); err != nil || len(events) != 1 || events[0].ID != "first" {
					t.Fatal("incomplete JSON", err, events)
				}
			}
		})
	}
}

type cancelledSessionWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *cancelledSessionWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if w.cancel != nil && bytes.Contains(p, []byte(`"id":`)) {
		w.cancel()
		w.cancel = nil
	}
	return n, err
}

func TestSessionExportStreamsBeyondOnePageAndStopsOnDisconnect(t *testing.T) {
	a := setup(t)
	a.readers.SetMaxOpenConns(1)
	events := make([]Event, 205)
	for i := range events {
		origin := "otel"
		if i%2 == 0 {
			origin = "import"
		}
		events[i] = recorded(t, fmt.Sprintf("%03d", i), origin, "codex", "large", "gen_ai.assistant.message", map[string]any{"response": strings.Repeat("Export content ", 256) + "complete tail"})
		events[i].Origin = origin
		events[i].Time = fmt.Sprintf("2026-11-14T00:00:%02d.000000000Z", i%3)
		events[i].Sequence = i % 5
	}
	save(t, a, events...)
	sort.Slice(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.Time != b.Time {
			return a.Time < b.Time
		}
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		return a.ID < b.ID
	})
	var exported []Event
	for _, origin := range []string{"", "import", "otel"} {
		getJSON(t, a, "/api/export?session=codex:large&origin="+origin, &exported)
		want := []Event{}
		for _, event := range events {
			if origin == "" || event.Origin == origin {
				want = append(want, event)
			}
		}
		if len(exported) != len(want) {
			t.Fatalf("origin %q: exported %d of %d records", origin, len(exported), len(want))
		}
		for i, event := range exported {
			if event.ID != want[i].ID || !bytes.Equal(event.Raw, want[i].Raw) {
				t.Fatalf("origin %q: export was reordered or truncated at %d", origin, i)
			}
		}
	}
	getJSON(t, a, "/api/export?session=missing", &exported)
	if len(exported) != 0 {
		t.Fatal("missing session must retain the empty-array format")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &cancelledSessionWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	r := httptest.NewRequest(http.MethodGet, "/api/export?session=codex:large", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+token)
	var aborted any
	func() { defer func() { aborted = recover() }(); a.Handler().ServeHTTP(w, r) }()
	if aborted != http.ErrAbortHandler {
		t.Fatal("disconnected download was finalized", aborted)
	}
	if n := bytes.Count(w.Body.Bytes(), []byte(`"id":`)); n == 0 || n >= len(events) {
		t.Fatal("session was buffered as one response instead of streamed", n)
	}
	if json.Valid(w.Body.Bytes()) {
		t.Fatal("interrupted download was closed as a complete partial array")
	}
	readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	var one int
	if err := a.readers.QueryRowContext(readCtx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatal("disconnected export retained the only reader", one, err)
	}
}
