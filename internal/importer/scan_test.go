package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWatchScanContinuesAfterUnreadableDirectory(t *testing.T) {
	root := t.TempDir()
	denied := filepath.Join(root, "a-unreadable")
	readable := filepath.Join(root, "z-readable")
	for _, p := range []string{denied, readable} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(readable, "session.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(denied, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(denied, 0700) })
	if _, err := os.ReadDir(denied); err == nil {
		t.Skip("filesystem permits reading chmod-000 directories")
	}
	options := Options{Dirs: []string{root}}
	files, err := sessionFiles(context.Background(), options, true)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected permission error, got %v", err)
	}
	if len(files) != 1 || files[0] != path {
		t.Fatalf("readable files after the denied directory were lost: %v", files)
	}
	if _, err = sessionFiles(context.Background(), options, false); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("one-time import must still report failure: %v", err)
	}
}
