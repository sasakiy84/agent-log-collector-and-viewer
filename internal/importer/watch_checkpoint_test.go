package importer_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/checkpoint"
	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/importer"
)

func watchOnce(t *testing.T, o importer.Options) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := importer.Watch(ctx, []importer.Options{o}, 10*time.Millisecond, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentWatchSkipsRestartAndDetectsReplacementAndConfig(t *testing.T) {
	dir := t.TempDir()
	path := file(t, dir, "session.jsonl", claudeLine("one", "initial"))
	statePath := filepath.Join(t.TempDir(), "state.db")
	open := func() *checkpoint.Store {
		store, err := checkpoint.Open(context.Background(), statePath)
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	var sends atomic.Int32
	o := importer.Options{Dirs: []string{dir}, Source: "claude", Checkpoint: open(), Sink: func(context.Context, []any) error { sends.Add(1); return nil }}
	watchOnce(t, o)
	o.Checkpoint.Close()
	if sends.Load() != 1 {
		t.Fatal("first import missing", sends.Load())
	}
	o.Checkpoint = open()
	defer o.Checkpoint.Close()
	watchOnce(t, o)
	if sends.Load() != 1 {
		t.Fatal("restart reparsed an unchanged transcript", sends.Load())
	}
	// A replaced file with identical size/mtime is different, even across restarts.
	old, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := file(t, t.TempDir(), "replacement", claudeLine("two", "initial"))
	if err = os.Chtimes(replacement, old.ModTime(), old.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	watchOnce(t, o)
	if sends.Load() != 2 {
		t.Fatal("replacement was skipped", sends.Load())
	}
	o.Project = "new-project"
	watchOnce(t, o)
	if sends.Load() != 3 {
		t.Fatal("changed import configuration was skipped", sends.Load())
	}
	appendFile(t, path, claudeLine("three", "appended"))
	watchOnce(t, o)
	if sends.Load() != 4 {
		t.Fatal("appended data was skipped", sends.Load())
	}
}

func TestPersistentWatchDoesNotCheckpointFailedDelivery(t *testing.T) {
	dir := t.TempDir()
	file(t, dir, "session.jsonl", claudeLine("one", "retry"))
	statePath := filepath.Join(t.TempDir(), "state.db")
	store, err := checkpoint.Open(context.Background(), statePath)
	if err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	fail.Store(true)
	var sent atomic.Int32
	o := importer.Options{Dirs: []string{dir}, Checkpoint: store, Sink: func(context.Context, []any) error {
		if fail.Load() {
			return errors.New("offline")
		}
		sent.Add(1)
		return nil
	}}
	watchOnce(t, o)
	store.Close()
	store, err = checkpoint.Open(context.Background(), statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	o.Checkpoint = store
	fail.Store(false)
	watchOnce(t, o)
	if sent.Load() != 1 {
		t.Fatal("failed delivery was treated as completed", sent.Load())
	}
	watchOnce(t, o)
	if sent.Load() != 1 {
		t.Fatal("successful retry was not checkpointed", sent.Load())
	}
}

func TestPersistentWatchIncludesCopilotPredecessors(t *testing.T) {
	dir := t.TempDir()
	file(t, dir, "events.jsonl", `{"type":"session.journal_link","data":{"sessionId":"journal","previousFile":"events.1.jsonl","version":1}}`+"\n")
	predecessor := file(t, dir, "events.1.jsonl", `{"type":"session.start","data":{"sessionId":"journal"}}`+"\n"+`{"type":"user.message","id":"a","data":{"content":"archive"}}`+"\n")
	store, err := checkpoint.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var sends atomic.Int32
	o := importer.Options{Files: []string{filepath.Join(dir, "events.jsonl")}, Checkpoint: store, Sink: func(context.Context, []any) error { sends.Add(1); return nil }}
	watchOnce(t, o)
	watchOnce(t, o)
	if sends.Load() != 1 {
		t.Fatal("unchanged journal replayed", sends.Load())
	}
	appendFile(t, predecessor, `{"type":"assistant.message","id":"b","data":{"content":"changed predecessor"}}`+"\n")
	watchOnce(t, o)
	if sends.Load() != 2 {
		t.Fatal("changed predecessor was skipped after restart", sends.Load())
	}
}
