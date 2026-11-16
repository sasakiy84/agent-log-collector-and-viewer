package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestSessionFilesFollowsSelectedRootButNotNestedLinks(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "sessions")
	if err := os.MkdirAll(filepath.Join(directory, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(directory, "nested", "session.jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// macOS temp roots can themselves be aliases (/var -> /private/var).
	// Discovery returns the canonical root for both configured directory forms.
	var err error
	want, err = filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "configured")
	if err := os.Symlink(directory, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.jsonl"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"outside": outside, "loop": directory, "linked.jsonl": want} {
		if err := os.Symlink(target, filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, dirs := range [][]string{{link}, {link, directory}} {
		for _, missingOK := range []bool{false, true} {
			files, err := sessionFiles(context.Background(), Options{Dirs: dirs}, missingOK)
			if err != nil || !slices.Equal(files, []string{want}) {
				t.Fatalf("dirs=%v missingOK=%v: got %v, err %v", dirs, missingOK, files, err)
			}
		}
	}
}

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
