package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net/url"
	"testing"
)

func TestCompactIdentifiersSharedLifetime(t *testing.T) {
	a := setup(t)
	first := recorded(t, "first", "import", "codex", "ids", "gen_ai.user.message", map[string]any{"message_id": "shared-JP-日本語", "prompt": "one"})
	second := recorded(t, "second", "import", "codex", "ids", "gen_ai.user.message", map[string]any{"message_id": "shared-JP-日本語", "prompt": "two"})
	save(t, a, first, second)
	check := func(want int) {
		t.Helper()
		var count int
		if err := a.DB.QueryRow("SELECT COUNT(*) FROM search_identifiers WHERE value=? AND key='message.id'", "shared-JP-日本語").Scan(&count); err != nil || count != want {
			t.Fatal(count, err)
		}
	}
	check(2)
	if _, err := a.DB.Exec("DELETE FROM records WHERE id=?", first.ID); err != nil {
		t.Fatal(err)
	}
	check(1)
	if _, err := a.DB.Exec("DELETE FROM records WHERE id=?", second.ID); err != nil {
		t.Fatal(err)
	}
	check(0)
	var count int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM identifier_values WHERE value=?", "shared-JP-日本語").Scan(&count); err != nil || count != 0 {
		t.Fatal("orphan identifiers", count, err)
	}
	save(t, a, first)
	check(1)
	if _, err := a.DB.Exec("PRAGMA incremental_vacuum"); err != nil {
		t.Fatal(err)
	}
	check(1)
}

func TestReplayPreflightRechecksEarlierImportInBatch(t *testing.T) {
	a := setup(t)
	first := recorded(t, "same", "import", "codex", "replay", "gen_ai.user.message", map[string]any{"prompt": "first"})
	changed := recorded(t, "same", "import", "codex", "replay", "gen_ai.user.message", map[string]any{"prompt": "changed"})
	save(t, a, first)
	// The second event appears unchanged to preflight, but not after the first
	// event in this batch updates the same import ID under the writer lock.
	save(t, a, changed, first)
	var raw, hash []byte
	if err := a.DB.QueryRow("SELECT raw,raw_hash FROM records WHERE id=?", first.ID).Scan(&raw, &hash); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(first.Raw)
	if string(raw) != string(first.Raw) || string(hash) != string(digest[:]) {
		t.Fatal("preflight skipped a changed import")
	}
	// A later malformed record must roll back the entire batch.
	bad := changed
	bad.ID = "import:bad"
	bad.Raw = []byte("{")
	if err := a.Save(context.Background(), []Event{changed, bad}); err == nil {
		t.Fatal("malformed batch accepted")
	}
	if err := a.DB.QueryRow("SELECT raw FROM records WHERE id=?", first.ID).Scan(&raw); err != nil || string(raw) != string(first.Raw) {
		t.Fatal("partial batch published", err)
	}
}

func TestSessionSkillCacheRevisionAndOrigin(t *testing.T) {
	a := setup(t)
	first := skillReport(t, "first", "import", "cache", "review", "one", "")
	save(t, a, first)
	read := func(origin string) []SkillSummary {
		t.Helper()
		tx, err := a.readers.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		out, err := a.cachedSessionSkills(context.Background(), tx, "codex:cache", origin)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := read("import"); len(got) != 1 || got[0].Uses != 1 {
		t.Fatal(got)
	}
	if got := read("otel"); len(got) != 0 {
		t.Fatal("origin leaked", got)
	}
	save(t, a, skillReport(t, "second", "import", "cache", "review", "two", ""))
	if got := read("import"); len(got) != 1 || got[0].Uses != 2 {
		t.Fatal("stale cache", got)
	}
	if _, err := a.DB.Exec("DELETE FROM records WHERE id=?", first.ID); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	if got := read("import"); len(got) != 1 || got[0].Uses != 1 {
		t.Fatal("delete reused stale cache", got)
	}
}

func TestInspectorDetailsCanOmitOriginal(t *testing.T) {
	a := setup(t)
	event := recorded(t, "inspect", "import", "codex", "inspect", "gen_ai.user.message", map[string]any{"prompt": "日本語のリクエスト"})
	save(t, a, event)
	path := "/api/event?session=" + url.QueryEscape(event.Session) + "&id=" + url.QueryEscape(event.ID)
	var details, full TimelineEvent
	getJSON(t, a, path+"&raw=0", &details)
	getJSON(t, a, path, &full)
	var original TimelineEvent
	getJSON(t, a, path+"&view=raw", &original)
	if len(original.Records) != 1 || string(original.Records[0].Raw) != string(event.Raw) {
		t.Fatal("raw-only fetch changed original")
	}

	if len(details.Records) != 1 || len(details.Records[0].Raw) != 0 || len(full.Records) != 1 || string(full.Records[0].Raw) != string(event.Raw) || details.Detail.Title != full.Detail.Title {
		t.Fatal("inspector changed detail or original")
	}
}
