package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/checkpoint"
	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/importer"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type listFlag []string

func (v *listFlag) String() string     { return fmt.Sprint([]string(*v)) }
func (v *listFlag) Set(s string) error { *v = append(*v, s); return nil }
func importMain(args []string) error {
	fs := flag.NewFlagSet("agentlog import", flag.ContinueOnError)
	var dirs, files, tags listFlag
	fs.Var(&dirs, "dir", "Session folder, scanned recursively (repeatable)")
	fs.Var(&files, "file", "Session JSONL file (repeatable)")
	fs.Var(&tags, "tag", "Project tag (repeatable)")
	all := fs.Bool("all", false, "Scan default session folders for all three CLIs")
	source := fs.String("source", "auto", "auto, codex, claude or copilot")
	commentary := fs.Bool("codex-commentary-only", false, "Import only Codex assistant messages marked commentary (requires --source codex and --dir or --file)")
	cwd := fs.String("cwd", "", "Only sessions from this working directory (including descendants)")
	project := fs.String("project", "", "Project namespace; defaults to session working-directory name")
	endpoint := fs.String("endpoint", "http://127.0.0.1:4318", "Collector base URL or /v1/logs URL")
	watchState := fs.String("watch-state", "", "Watch checkpoint database (default: user config directory/agentlog/watch.db)")
	watch := fs.Bool("watch", false, "Import existing sessions and watch for changes")
	interval := fs.Duration("interval", time.Second, "Watch polling interval")
	dry := fs.Bool("dry-run", false, "Parse and report without sending")
	if e := fs.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return nil
		}
		return e
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments; use --dir or --file")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	o := importer.Options{Dirs: dirs, Files: files, Tags: tags, All: *all, Source: *source, Project: *project, WorkingDir: *cwd, Endpoint: *endpoint, Token: os.Getenv("AGENTLOG_TOKEN"), DryRun: *dry, CodexCommentaryOnly: *commentary}
	if *watch {
		if *dry {
			return fmt.Errorf("--watch cannot be combined with --dry-run")
		}
		statePath := *watchState
		if statePath == "" {
			dir, err := os.UserConfigDir()
			if err != nil {
				return err
			}
			statePath = filepath.Join(dir, "agentlog", "watch.db")
		}
		store, err := checkpoint.Open(ctx, statePath)
		if err != nil {
			return err
		}
		defer store.Close()
		o.Checkpoint = store
		return importer.Watch(ctx, []importer.Options{o}, *interval, os.Stderr)
	}
	stats, e := importer.Run(ctx, o, os.Stderr)
	fmt.Fprintf(os.Stderr, "Files: %d; records: %d; skipped: %d; failed: %d\n", stats.Files, stats.Records, stats.Skipped, stats.Failed)
	return e
}
