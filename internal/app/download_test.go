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
	"testing"
	"time"
)

type delayedDownloadWriter struct {
	http.ResponseWriter
	delayed bool
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
