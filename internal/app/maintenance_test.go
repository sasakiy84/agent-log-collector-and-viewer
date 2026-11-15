package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestMaintenanceAtomicCheckpointAndResume(t *testing.T) {
	a := setup(t)
	if _, err := a.DB.Exec("CREATE TABLE backfill_fixture(id TEXT PRIMARY KEY); INSERT INTO maintenance_jobs(name) VALUES('fixture.v1')"); err != nil {
		t.Fatal(err)
	}
	broken := func(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
		next := "a"
		if cursor == "a" {
			next = "b"
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO backfill_fixture VALUES(?)", next); err != nil {
			return cursor, 0, err
		}
		if next == "b" {
			return next, 1, errors.New("injected batch failure")
		}
		return next, 1, nil
	}
	if err := a.runMaintenance(context.Background(), "fixture.v1", broken, 0); err == nil {
		t.Fatal("batch failure lost")
	}
	var cursor, failure string
	var count, processed int
	var complete bool
	if err := a.DB.QueryRow("SELECT cursor,complete,processed,error FROM maintenance_jobs WHERE name='fixture.v1'").Scan(&cursor, &complete, &processed, &failure); err != nil || cursor != "a" || complete || processed != 1 || failure == "" {
		t.Fatal("checkpoint advanced through failure", cursor, complete, processed, failure, err)
	}
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM backfill_fixture").Scan(&count); err != nil || count != 1 {
		t.Fatal("failed writes committed", count, err)
	}
	resume := func(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
		if cursor == "b" {
			return cursor, 0, nil
		}
		if cursor != "a" {
			t.Fatal("completed batch repeated", cursor)
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO backfill_fixture VALUES('b')")
		return "b", 1, err
	}
	if err := a.runMaintenance(context.Background(), "fixture.v1", resume, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow("SELECT complete,processed,error FROM maintenance_jobs WHERE name='fixture.v1'").Scan(&complete, &processed, &failure); err != nil || !complete || processed != 2 || failure != "" {
		t.Fatal("resume did not finish/clear failure", complete, processed, failure, err)
	}
	if err := a.runMaintenance(context.Background(), "fixture.v1", func(context.Context, *sql.Tx, string) (string, int, error) {
		t.Fatal("completed job was run again")
		return "", 0, nil
	}, 0); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceRejectsNonAdvancingBatch(t *testing.T) {
	a := setup(t)
	if _, err := a.DB.Exec("INSERT INTO maintenance_jobs(name) VALUES('stalled.v1')"); err != nil {
		t.Fatal(err)
	}
	err := a.runMaintenance(context.Background(), "stalled.v1", func(context.Context, *sql.Tx, string) (string, int, error) {
		return "", 1, nil
	}, 0)
	if err == nil {
		t.Fatal("stalled job would loop forever")
	}
}
