package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/importer"
	"os"
	"os/signal"
	"syscall"
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
	cwd := fs.String("cwd", "", "Only sessions from this working directory (including descendants)")
	project := fs.String("project", "", "Project namespace; defaults to session working-directory name")
	endpoint := fs.String("endpoint", "http://127.0.0.1:4318", "Collector base URL or /v1/logs URL")
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
	stats, e := importer.Run(ctx, importer.Options{Dirs: dirs, Files: files, Tags: tags, All: *all, Source: *source, Project: *project, WorkingDir: *cwd, Endpoint: *endpoint, Token: os.Getenv("AGENTLOG_TOKEN"), DryRun: *dry}, os.Stderr)
	fmt.Fprintf(os.Stderr, "Files: %d; records: %d; skipped: %d; failed: %d\n", stats.Files, stats.Records, stats.Skipped, stats.Failed)
	return e
}
