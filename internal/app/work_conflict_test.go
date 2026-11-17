package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRepeatedImportedToolResultUpdatesDoNotBlockBatch(t *testing.T) {
	a := setup(t)
	input, output := skillReadEvents(t, "same-call", "import", "codex", "root", "Read", `{"file_path":"/work/skills/review/SKILL.md"}`, nil)
	save(t, a, input, output)
	first := recorded(t, "same-call-out", "import", "codex", "root", "gen_ai.tool.result", map[string]any{"call_id": "same-call", "success": false, "output": "first changed result"})
	second := recorded(t, "same-call-out", "import", "codex", "root", "gen_ai.tool.result", map[string]any{"call_id": "same-call", "success": true, "output": "second changed result"})
	following := recorded(t, "following", "import", "codex", "root", "gen_ai.user.message", map[string]any{"prompt": "The next record must also be stored"})
	if err := a.Save(context.Background(), []Event{first, second, following}); err != nil {
		t.Fatal("repeated result update blocked the entire batch:", err)
	}
	var raw []byte
	if err := a.DB.QueryRow("SELECT raw FROM records WHERE id=?", second.ID).Scan(&raw); err != nil || string(raw) != string(second.Raw) {
		t.Fatal("latest result not committed", err)
	}
	if err := a.DB.QueryRow("SELECT raw FROM records WHERE id=?", following.ID).Scan(&raw); err != nil || string(raw) != string(following.Raw) {
		t.Fatal("following record not committed", err)
	}
	var pending int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM work_items").Scan(&pending); err != nil || pending != 0 {
		t.Fatal("pending projections", pending, err)
	}
	if summary := skillCounts(t, a, ""); summary.Attempts != 1 {
		t.Fatal(summary)
	}
}

func TestUpgradeDrainsRetainedBatchAndItsFollowingBatch(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/retained.db"
	db := migrationDB(t, path)
	plan, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, plan[:3]); err != nil {
		t.Fatal(err)
	}
	legacy, err := newApp(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	legacy.DB = db
	if err = legacy.initReaders(path); err != nil {
		t.Fatal(err)
	}
	input, output := skillReadEvents(t, "retained-call", "import", "codex", "root", "Read", `{"file_path":"/work/skills/review/SKILL.md"}`, nil)
	save(t, legacy, input, output)
	changed := func(text string) Event {
		return recorded(t, "retained-call-out", "import", "codex", "root", "gen_ai.tool.result", map[string]any{"call_id": "retained-call", "success": true, "output": text})
	}
	updates := []Event{changed("first revision"), changed("second revision")}
	if err = legacy.Save(ctx, updates); err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed: work_items") {
		t.Fatal("old schema did not reproduce the retained-batch failure", err)
	}
	following := recorded(t, "following-batch", "import", "codex", "root", "gen_ai.user.message", map[string]any{"prompt": "Stored after the retained batch"})
	queue, err := openIngestQueue(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = queue.accept(ctx, updates, false); err != nil {
		t.Fatal(err)
	}
	if err = queue.accept(ctx, []Event{following}, false); err != nil {
		t.Fatal(err)
	}
	if batches, _, _, err := queue.size(ctx); err != nil || batches != 2 {
		t.Fatal(batches, err)
	}
	queue.close()
	legacy.Close()
	upgraded, err := Start(ctx, path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	select {
	case <-upgraded.maintained:
	case <-time.After(10 * time.Second):
		t.Fatal("upgrade did not finish")
	}
	if upgraded.maintenanceErr != nil || upgraded.startupErr != nil {
		t.Fatal(upgraded.startupErr, upgraded.maintenanceErr)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		batches, _, _, err := upgraded.inbox.size(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if batches == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retained batches did not drain", batches)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, event := range []Event{input, updates[1], following} {
		var raw []byte
		if err = upgraded.DB.QueryRow("SELECT raw FROM records WHERE id=?", event.ID).Scan(&raw); err != nil || string(raw) != string(event.Raw) {
			t.Fatal("record missing or changed after upgrade", event.ID, err)
		}
	}
	if summary := skillCounts(t, upgraded, ""); summary.Attempts != 1 {
		t.Fatal(summary)
	}
}
