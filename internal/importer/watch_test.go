package importer_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/importer"
)

func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for watcher")
}
func startWatcher(t *testing.T, o importer.Options) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- importer.Watch(ctx, []importer.Options{o}, 10*time.Millisecond, io.Discard) }()
	stop := func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("watcher did not stop")
		}
	}
	t.Cleanup(stop)
	return cancel
}
func claudeLine(id, text string) string {
	return fmt.Sprintf(`{"type":"user","uuid":%q,"sessionId":"watched","cwd":"/work/shop","timestamp":"2026-11-01T00:00:00Z","message":{"content":%q}}`+"\n", id, text)
}
func appendFile(t *testing.T, p, text string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(text)
	closeErr := f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}
func TestWatchLocalLifecycle(t *testing.T) {
	db, _ := setup(t)
	dir := filepath.Join(t.TempDir(), "not-created-yet")
	var sends atomic.Int32
	o := importer.Options{Dirs: []string{dir}, Project: "store", Tags: []string{"backend"}, Sink: func(ctx context.Context, batch []any) error { sends.Add(1); return db.IngestLogs(ctx, batch) }}
	p := file(t, dir, "nested/session.jsonl", claudeLine("one", "initial"))
	cancel := startWatcher(t, o)
	count := func(want int) func() bool {
		return func() bool {
			var n int
			err := db.DB.QueryRow("SELECT COUNT(*) FROM records").Scan(&n)
			return err == nil && n == want
		}
	}
	eventually(t, count(1))
	var raw string
	if err := db.DB.QueryRow("SELECT raw FROM records LIMIT 1").Scan(&raw); err != nil || !strings.Contains(raw, "store") || !strings.Contains(raw, "backend") {
		t.Fatal(err, raw)
	}
	// An unchanged file is never resent across several polling cycles.
	before := sends.Load()
	time.Sleep(70 * time.Millisecond)
	if sends.Load() != before {
		t.Fatal("unchanged file was resent")
	}
	appendFile(t, p, claudeLine("two", "appended")+`{"type":"user","uuid":"three",`)
	eventually(t, count(2))
	appendFile(t, p, `"sessionId":"watched","message":{"content":"completed tail"}}`+"\n")
	eventually(t, count(3))
	file(t, dir, "new/subdir/other.jsonl", claudeLine("four", "new file"))
	eventually(t, count(4))
	// Replacement and truncation are reparsed, while saved history remains intact.
	replacement := file(t, t.TempDir(), "replacement", claudeLine("five", "replacement"))
	if err := os.Rename(replacement, p); err != nil {
		t.Fatal(err)
	}
	eventually(t, count(5))
	file(t, filepath.Dir(p), filepath.Base(p), claudeLine("six", "truncate"))
	eventually(t, count(6))
	cancel()
}
func TestWatchRetriesWithoutFileChange(t *testing.T) {
	db, _ := setup(t)
	dir := t.TempDir()
	file(t, dir, "session.jsonl", claudeLine("retry", "retry"))
	var attempts atomic.Int32
	startWatcher(t, importer.Options{Dirs: []string{dir}, Sink: func(ctx context.Context, b []any) error {
		if attempts.Add(1) == 1 {
			return errors.New("temporary failure")
		}
		return db.IngestLogs(ctx, b)
	}})
	eventually(t, func() bool {
		var n int
		db.DB.QueryRow("SELECT COUNT(*) FROM records").Scan(&n)
		return n == 1 && attempts.Load() == 2
	})
}
func TestWatchCopilotJournalDependency(t *testing.T) {
	db, _ := setup(t)
	dir := t.TempDir()
	head := `{"type":"session.journal_link","data":{"sessionId":"journal","previousFile":"events.1.jsonl","version":1}}` + "\n"
	file(t, dir, "events.jsonl", head)
	startWatcher(t, importer.Options{Dirs: []string{dir}, Sink: db.IngestLogs})
	archive := file(t, dir, "events.1.jsonl", `{"type":"session.start","data":{"sessionId":"journal"}}`+"\n"+`{"type":"user.message","id":"a","data":{"content":"archive"}}`+"\n")
	count := func(want int) func() bool {
		return func() bool { var n int; db.DB.QueryRow("SELECT COUNT(*) FROM records").Scan(&n); return n == want }
	}
	eventually(t, count(1))
	appendFile(t, archive, `{"type":"assistant.message","id":"b","data":{"content":"changed predecessor"}}`+"\n")
	eventually(t, count(2))
	// Rotation creates a new linked head; archive files themselves are never imported twice.
	if err := os.Rename(filepath.Join(dir, "events.jsonl"), filepath.Join(dir, "events.2.jsonl")); err != nil {
		t.Fatal(err)
	}
	file(t, dir, "events.jsonl", `{"type":"session.journal_link","data":{"sessionId":"journal","previousFile":"events.2.jsonl","version":1}}`+"\n"+`{"type":"user.message","id":"c","data":{"content":"after rotation"}}`+"\n")
	eventually(t, count(3))
}
func TestWatchValidation(t *testing.T) {
	valid := importer.Options{Dirs: []string{t.TempDir()}, Sink: func(context.Context, []any) error { return nil }}
	for _, tc := range []struct {
		o        importer.Options
		interval time.Duration
	}{
		{valid, 0}, {importer.Options{}, time.Second}, {importer.Options{Dirs: valid.Dirs, Source: "bad", DryRun: true}, time.Second}, {importer.Options{Dirs: valid.Dirs, DryRun: true}, time.Second}, {importer.Options{Dirs: valid.Dirs, Endpoint: "ftp://localhost", Token: token}, time.Second},
	} {
		if err := importer.Watch(context.Background(), []importer.Options{tc.o}, tc.interval, io.Discard); err == nil {
			t.Fatal("invalid watch options accepted", tc)
		}
	}
}
func TestCodexLateCanonicalMessages(t *testing.T) {
	db, _ := setup(t)
	dir := t.TempDir()
	meta := `{"type":"session_meta","payload":{"id":"late","cwd":"/work/shop"}}` + "\n"
	event := `{"type":"event_msg","timestamp":"2026-11-01T00:00:01Z","payload":{"type":"user_message","message":"hello"}}` + "\n"
	response := `{"type":"response_item","timestamp":"2026-11-01T00:00:02Z","payload":{"type":"message","role":"user","id":"native-id","content":[{"type":"input_text","text":"hello"}]}}` + "\n"
	p := file(t, dir, "codex.jsonl", meta+event)
	o := importer.Options{Files: []string{p}, Sink: db.IngestLogs}
	run := func(want int) {
		t.Helper()
		if _, err := importer.Run(context.Background(), o, io.Discard); err != nil {
			t.Fatal(err)
		}
		var n int
		db.DB.QueryRow("SELECT COUNT(*) FROM records").Scan(&n)
		if n != want {
			t.Fatalf("got %d events, want %d", n, want)
		}
	}
	run(1)
	appendFile(t, p, response)
	run(1)
	// Identical text in a later turn is a separate message. Unmatched fallback
	// messages remain visible even if an earlier canonical message exists.
	appendFile(t, p, event)
	run(2)
	appendFile(t, p, response)
	run(2)
}

func TestWatchCancelsInFlightHTTP(t *testing.T) {
	dir := t.TempDir()
	file(t, dir, "session.jsonl", claudeLine("cancel", "cancel"))
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- importer.Watch(ctx, []importer.Options{{Dirs: []string{dir}, Endpoint: server.URL, Token: token}}, time.Second, io.Discard)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP delivery did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP delivery ignored cancellation")
	}
}

func TestWatchRootCreatedAfterStart(t *testing.T) {
	db, _ := setup(t)
	dir := filepath.Join(t.TempDir(), "missing-root")
	startWatcher(t, importer.Options{Dirs: []string{dir}, Sink: db.IngestLogs})
	file(t, dir, "nested/session.jsonl", claudeLine("created", "created after watch start"))
	eventually(t, func() bool {
		var n int
		err := db.DB.QueryRow("SELECT COUNT(*) FROM records").Scan(&n)
		return err == nil && n == 1
	})
}

func TestWatchSymlinkRootTargetAppearsAndChanges(t *testing.T) {
	db, _ := setup(t)
	root := t.TempDir()
	directory := filepath.Join(root, "synced-sessions")
	link := filepath.Join(root, "sessions")
	if err := os.Symlink(directory, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		t.Fatal(err)
	}
	startWatcher(t, importer.Options{Dirs: []string{link}, Sink: db.IngestLogs})
	path := file(t, directory, "nested/session.jsonl", claudeLine("initial", "synced directory"))
	count := func(want int) func() bool {
		return func() bool {
			var n int
			err := db.DB.QueryRow("SELECT COUNT(*) FROM records").Scan(&n)
			return err == nil && n == want
		}
	}
	eventually(t, count(1))
	appendFile(t, path, claudeLine("appended", "new synced data"))
	eventually(t, count(2))
}
