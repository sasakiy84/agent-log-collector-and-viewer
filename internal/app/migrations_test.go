package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func migrationDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	uri, err := databaseURI(path, false)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrationAtomicHistoryAndRetry(t *testing.T) {
	db := migrationDB(t, filepath.Join(t.TempDir(), "schema.db"))
	ctx := context.Background()
	one := migration{1, "one", "CREATE TABLE original(value TEXT); INSERT INTO original VALUES('retained');"}
	if err := migrate(ctx, db, []migration{one}); err != nil {
		t.Fatal(err)
	}
	two := migration{2, "two", "ALTER TABLE original ADD COLUMN extra TEXT; CREATE TABLE derived(value TEXT);"}
	bad := migration{3, "three", "INSERT INTO missing_table VALUES(1)"}
	if err := migrate(ctx, db, []migration{one, two, bad}); err == nil {
		t.Fatal("failed migration accepted")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil || count != 1 {
		t.Fatal("partial migration history committed", count, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='derived'").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial DDL committed", count, err)
	}
	three := migration{3, "three", "INSERT INTO derived VALUES('complete')"}
	plan := []migration{one, two, three}
	if err := migrate(ctx, db, plan); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, plan); err != nil {
		t.Fatal("repeat application", err)
	}
	var value string
	if err := db.QueryRow("SELECT value FROM original").Scan(&value); err != nil || value != "retained" {
		t.Fatal("original changed", value, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM derived").Scan(&count); err != nil || count != 1 {
		t.Fatal("migration ran twice", count, err)
	}
	changed := append([]migration(nil), plan...)
	changed[0].sql += " -- edited"
	if err := migrate(ctx, db, changed); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatal("edited migration accepted", err)
	}
	if err := migrate(ctx, db, plan[:2]); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal("future schema accepted", err)
	}
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version=2"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, plan); err == nil {
		t.Fatal("gapped history accepted")
	}
}

func TestConcurrentMigrationApplication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	one, two := migrationDB(t, path), migrationDB(t, path)
	plan := []migration{{1, "once", "CREATE TABLE once_only(id INTEGER); INSERT INTO once_only VALUES(1)"}}
	var group sync.WaitGroup
	for _, db := range []*sql.DB{one, two} {
		group.Add(1)
		go func(db *sql.DB) {
			defer group.Done()
			if err := migrate(context.Background(), db, plan); err != nil {
				t.Error(err)
			}
		}(db)
	}
	group.Wait()
	var count int
	if err := one.QueryRow("SELECT COUNT(*) FROM once_only").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestLegacyCheckpointAdoption(t *testing.T) {
	db := migrationDB(t, filepath.Join(t.TempDir(), "legacy.db"))
	plan, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	// Released databases had feature DDL and three independent checkpoints,
	// with user_version reserved only for the folder backfill.
	if _, err := db.Exec(plan[0].sql + `
 UPDATE usage_backfill SET cursor='usage-cursor',complete=0;
 UPDATE agent_index_progress SET cursor='agent-cursor',complete=1;
 UPDATE skill_index_progress SET cursor='skill-cursor',complete=0;
 PRAGMA user_version=1;`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(context.Background(), db, plan); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		name, cursor string
		complete     bool
	}{{usageJob, "usage-cursor", false}, {agentsJob, "agent-cursor", true}, {skillsJob, "skill-cursor", false}, {directoriesJob, "", true}} {
		var cursor string
		var complete bool
		if err := db.QueryRow("SELECT cursor,complete FROM maintenance_jobs WHERE name=?", want.name).Scan(&cursor, &complete); err != nil || cursor != want.cursor || complete != want.complete {
			t.Fatal(want.name, cursor, complete, err)
		}
	}
}

func TestDatabaseConnectionSettingsSurviveReplacement(t *testing.T) {
	// URI punctuation in filenames must not become connection parameters.
	a, err := Open(filepath.Join(t.TempDir(), "db?query#fragment.sqlite"), token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.DB.SetMaxIdleConns(0)
	var foreignKeys, timeout int
	if err := a.DB.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("replacement connection lost foreign keys", foreignKeys, err)
	}
	if err := a.DB.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatal("replacement connection lost timeout", timeout, err)
	}
	if _, err := a.DB.Exec("INSERT INTO event_keys(event_id,namespace,key) VALUES('absent','test','test')"); err == nil {
		t.Fatal("orphan allowed on replacement connection")
	}
}
