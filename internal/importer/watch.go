package importer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func validate(o Options) error {
	if o.Source != "" && o.Source != "auto" && o.Source != "codex" && o.Source != "claude" && o.Source != "copilot" {
		return errors.New("source must be auto, codex, claude or copilot")
	}
	if len(o.Dirs)+len(o.Files) == 0 && !o.All {
		return errors.New("specify --dir, --file or --all")
	}
	if !o.DryRun && o.Sink == nil {
		if _, err := endpoint(o.Endpoint); err != nil {
			return err
		}
		if o.Token != "" && len(o.Token) < 16 {
			return errors.New("AGENTLOG_TOKEN must be empty or contain at least 16 characters")
		}
	}
	return nil
}

func sessionFiles(ctx context.Context, o Options, missingOK bool) ([]string, error) {
	paths := map[string]bool{}
	for _, f := range o.Files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return nil, err
		}
		paths[abs] = true
	}
	dirs := append([]string{}, o.Dirs...)
	if o.All {
		for _, d := range roots() {
			if missingOK {
				dirs = append(dirs, d)
			} else if info, err := os.Stat(d); err == nil && info.IsDir() {
				dirs = append(dirs, d)
			}
		}
	}
	var failures []error
	for _, dir := range dirs {
		// Follow a root the user explicitly selected, including common links to
		// synced session directories. Links encountered inside it stay excluded
		// so they cannot pull unrelated trees or cycles into recursive discovery.
		root, err := filepath.EvalSymlinks(dir)
		if err != nil {
			if missingOK && errors.Is(err, os.ErrNotExist) {
				continue
			}
			failures = append(failures, fmt.Errorf("scan %s: %w", dir, err))
			continue
		}
		err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			if err != nil {
				if missingOK && errors.Is(err, os.ErrNotExist) {
					return nil
				}
				if missingOK {
					failures = append(failures, fmt.Errorf("scan %s: %w", p, err))
					return nil
				}
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
				abs, e := filepath.Abs(p)
				if e != nil {
					return e
				}
				paths[abs] = true
			}
			return nil
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("scan %s: %w", dir, err))
		}
	}
	files := make([]string, 0, len(paths))
	for f := range paths {
		files = append(files, f)
	}
	sort.Strings(files)
	return files, errors.Join(failures...)
}

func journalSegment(file string) bool {
	base := filepath.Base(file)
	return strings.HasPrefix(base, "events.") && base != "events.jsonl"
}

type watchedFile struct {
	info        os.FileInfo
	fingerprint []byte
	pending     bool
	retry       time.Time
	lastError   string
}
type watchState struct {
	options   Options
	files     map[string]*watchedFile
	scanError string
}

// Watch imports existing files, then polls recursively for changes. Failed imports
// retry without requiring another file change. Stored event identities make replay safe.
func Watch(ctx context.Context, options []Options, interval time.Duration, progress io.Writer) error {
	if interval <= 0 {
		return errors.New("watch interval must be positive")
	}
	if len(options) == 0 {
		return errors.New("no watch folders configured")
	}
	if progress == nil {
		progress = io.Discard
	}
	states := make([]watchState, len(options))
	for i, o := range options {
		if err := validate(o); err != nil {
			return err
		}
		if o.DryRun {
			return errors.New("--watch cannot be combined with --dry-run")
		}
		states[i] = watchState{options: o, files: map[string]*watchedFile{}}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		for i := range states {
			if ctx.Err() != nil {
				return nil
			}
			w := &states[i]
			files, err := sessionFiles(ctx, w.options, true)
			if ctx.Err() != nil {
				return nil
			}
			message := ""
			if err != nil {
				message = err.Error()
			}
			if message != "" && message != w.scanError {
				fmt.Fprintln(progress, message)
			}
			w.scanError = message
			seen := map[string]bool{}
			dirtyHeads := map[string]bool{}
			for _, file := range files {
				info, e := os.Lstat(file)
				if e != nil || !info.Mode().IsRegular() {
					continue
				}
				seen[file] = true
				old := w.files[file]
				if old == nil || info.Size() != old.info.Size() || !info.ModTime().Equal(old.info.ModTime()) || !os.SameFile(info, old.info) || fileIdentity(info) != fileIdentity(old.info) {
					w.files[file] = &watchedFile{info: info, pending: true}
					if journalSegment(file) {
						dirtyHeads[filepath.Join(filepath.Dir(file), "events.jsonl")] = true
					}
				}
				if old != nil && !old.pending && w.options.Checkpoint != nil && filepath.Base(file) == "events.jsonl" {
					fingerprint, e := watchFingerprint(ctx, file)
					if e != nil || !bytes.Equal(fingerprint, old.fingerprint) {
						old.pending = true
						old.retry = time.Time{}
					}
				}

			}
			// Keep unseen entries if a scan failed; a temporary permission error is not deletion.
			if err == nil {
				for file := range w.files {
					if !seen[file] {
						delete(w.files, file)
						if journalSegment(file) {
							dirtyHeads[filepath.Join(filepath.Dir(file), "events.jsonl")] = true
						}
					}
				}
			}
			for head := range dirtyHeads {
				if f := w.files[head]; f != nil {
					f.pending = true
					f.retry = time.Time{}
				}
			}
			for _, file := range files {
				f := w.files[file]
				if f == nil || !f.pending || journalSegment(file) || time.Now().Before(f.retry) {
					continue
				}
				o := w.options
				o.All = false
				o.Dirs = nil
				o.Files = []string{file}
				var fingerprint []byte
				var e error
				key := watchKey(o, file)
				if o.Checkpoint != nil {
					fingerprint, e = watchFingerprint(ctx, file)
					if e == nil {
						var previous []byte
						previous, e = o.Checkpoint.Get(ctx, key)
						if e == nil && bytes.Equal(previous, fingerprint) {
							f.pending = false
							f.fingerprint = fingerprint
							continue
						}
					}
				}
				var stats Stats
				if e == nil {
					stats, e = Run(ctx, o, io.Discard)
				}
				if e == nil && o.Checkpoint != nil {
					var after []byte
					after, e = watchFingerprint(ctx, file)
					if e == nil && !bytes.Equal(after, fingerprint) {
						continue
					}
					if e == nil {
						e = o.Checkpoint.Put(ctx, key, fingerprint)
					}
				}
				if ctx.Err() != nil {
					return nil
				}
				if e != nil {
					if e.Error() != f.lastError {
						fmt.Fprintf(progress, "watch %s: %v (will retry)\n", file, e)
					}
					f.lastError = e.Error()
					f.retry = time.Now().Add(max(interval, time.Second))
					continue
				}
				f.pending = false
				f.fingerprint = fingerprint
				f.lastError = ""
				fmt.Fprintf(progress, "watch %s: %d records, %d skipped\n", file, stats.Records, stats.Skipped)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
