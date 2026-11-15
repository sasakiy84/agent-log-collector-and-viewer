package app

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Every replacement connection must retain foreign keys and the busy timeout.
// Writer transactions acquire their reservation before reading a checkpoint.
func databaseURI(path string, readOnly bool) (string, error) {
	address := ":memory:"
	query := url.Values{"_pragma": {"busy_timeout(5000)"}}
	if readOnly {
		query.Set("mode", "ro")
		query.Add("_pragma", "query_only(1)")
	} else {
		query.Set("_txlock", "immediate")
		query.Add("_pragma", "foreign_keys(1)")
	}
	if path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		uriPath := filepath.ToSlash(absolute)
		if filepath.VolumeName(absolute) != "" && uriPath[0] != '/' {
			uriPath = "/" + uriPath
		}
		address = (&url.URL{Scheme: "file", Path: uriPath}).String()
	}
	return address + "?" + query.Encode(), nil
}

func Open(path, token string) (*App, error) {
	if len(token) < 16 {
		return nil, fmt.Errorf("AGENTLOG_TOKEN must contain at least 16 characters")
	}
	if path != ":memory:" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
	}
	uri, err := databaseURI(path, false)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	a := &App{DB: db, token: token}
	ready := false
	defer func() {
		if !ready {
			if a.cancel != nil {
				a.cancel()
				a.workers.Wait()
			}
			if a.readers != nil && a.readers != db {
				a.readers.Close()
			}
			db.Close()
		}
	}()
	if _, err = db.Exec("PRAGMA auto_vacuum=INCREMENTAL; PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	plan, err := migrations()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err = migrate(ctx, db, plan); err != nil {
		return nil, err
	}
	// Only pending work runs here. Schema changes and legacy discovery are
	// migrations; normal ingestion maintains these queues transactionally.
	steps := []struct {
		name string
		run  func() error
	}{
		{"trace ownership", func() error { return a.drainBatches(ctx, "trace ownership", reconcileTraceBatch) }},
		{"session views", func() error { return a.prepareReadModel(ctx) }},
		{"event presentation", func() error { return a.runMaintenance(ctx, "presentation.v1", backfillPresentation, 0) }},
		{"folder summaries", func() error { return a.runMaintenance(ctx, directoriesJob, backfillDirectories, 0) }},
		{"session titles", func() error { return a.runMaintenance(ctx, titlesJob, backfillTitles, 0) }},
		{"usage summaries", func() error { return a.runMaintenance(ctx, usageJob, backfillUsage, 0) }},
		{"folder overview", func() error { return a.drainBatches(ctx, "folder overview", refreshFolderBatch) }},
		{"subagent links", func() error { return a.runMaintenance(ctx, agentsJob, backfillAgents, 0) }},
		{"full-text search", func() error { return a.prepareSearch(ctx) }},
	}
	for _, step := range steps {
		log.Printf("Database preparation: %s", step.name)
		if err = step.run(); err != nil {
			return nil, fmt.Errorf("%s: %w", step.name, err)
		}
	}
	if err = a.initReaders(path); err != nil {
		return nil, err
	}
	if err = a.initArchives(path); err != nil {
		return nil, err
	}
	if err = a.startSkillIndexer(); err != nil {
		return nil, err
	}
	ready = true
	return a, nil
}
