package app

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Fixtures that rewind migration history also undo the newer DDL.
const undoDirectDeleteMigration = "DROP INDEX maintenance_jobs_latest; ALTER TABLE archive_jobs DROP COLUMN direct_delete; "

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

func TestDatabaseConnectionSettingsSurviveReplacement(t *testing.T) {
	// URI punctuation in filenames must not become connection parameters.
	a, err := Open(context.Background(), filepath.Join(t.TempDir(), "db?query#fragment.sqlite"), token)
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
	if _, err := a.DB.Exec("INSERT INTO record_keys(event_id,namespace,key) VALUES('absent','test','test')"); err == nil {
		t.Fatal("orphan allowed on replacement connection")
	}
}

func TestStorageGenerationRejectsOldDatabaseWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db := migrationDB(t, path)
	if _, err := db.Exec("CREATE TABLE old_records(raw BLOB); INSERT INTO old_records VALUES(X'0020ff')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if app, err := Open(context.Background(), path, token); err == nil {
		app.Close()
		t.Fatal("old database was silently adopted")
	} else if !strings.Contains(err.Error(), "uv run scripts/migrate_db.py") {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected source database changed")
	}
}

func TestStorageGenerationAndMigrationHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.db")
	a, err := Open(context.Background(), path, token)
	if err != nil {
		t.Fatal(err)
	}
	var id, version, history int
	a.DB.QueryRow("PRAGMA application_id").Scan(&id)
	a.DB.QueryRow("PRAGMA user_version").Scan(&version)
	a.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&history)
	if id != storageApplicationID || version != 1 || history != 9 {
		t.Fatal(id, version, history)
	}
	a.DB.Exec("PRAGMA user_version=99")
	a.Close()
	if app, err := Open(context.Background(), path, token); err == nil {
		app.Close()
		t.Fatal("future storage generation accepted")
	}
}

func TestArchiveCleanupMigrationPreservesLiveRevisionsAndActiveSnapshots(t *testing.T) {
	db := migrationDB(t, filepath.Join(t.TempDir(), "archive.db"))
	ctx := context.Background()
	plan, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, plan[:5]); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
		INSERT INTO archive_jobs(id,status,created_at,cutoff)
		VALUES ('history','deleted','',''),('running','deleting','','');
		INSERT INTO records(id,session,parent,source,signal,time,name,trace,span,parent_span,raw,origin,kind,tool_name)
		VALUES ('record','reimported','','codex','','','','','','','{}','import','user','');
		INSERT INTO session_versions VALUES ('removed',2),('locked',3);
		INSERT INTO archive_sessions(archive_id,session,source,revision,records,raw_bytes,last_activity) VALUES
		('history','removed','codex',1,1,1,''),('history','reimported','codex',1,1,1,''),
		('history','locked','codex',1,1,1,''),('running','locked','codex',2,1,1,'');
		INSERT INTO archive_locks VALUES ('locked','running');`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, plan); err != nil {
		t.Fatal(err)
	}
	for query, expected := range map[string]int{
		"SELECT COUNT(*) FROM archive_jobs":                                2,
		"SELECT COUNT(*) FROM archive_sessions WHERE archive_id='history'": 0,
		"SELECT COUNT(*) FROM archive_sessions WHERE archive_id='running'": 1,
		"SELECT COUNT(*) FROM archive_locks WHERE archive_id='running'":    1,
		"SELECT COUNT(*) FROM session_versions WHERE session='removed'":    0,
		"SELECT revision FROM session_versions WHERE session='locked'":     3,
		"SELECT revision FROM session_versions WHERE session='reimported'": 1,
	} {
		var actual int
		if err := db.QueryRow(query).Scan(&actual); err != nil || actual != expected {
			t.Fatalf("%s: got %d, want %d, error %v", query, actual, expected, err)
		}
	}
}
