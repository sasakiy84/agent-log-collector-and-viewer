package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func searchSessions(t *testing.T, a *App, query string) []SessionSummary {
	t.Helper()
	var out []SessionSummary
	getJSON(t, a, "/api/sessions?q="+url.QueryEscape(query), &out)
	return out
}
func TestJapaneseFullTextSearch(t *testing.T) {
	a := setup(t)
	e := recorded(t, "ja", "import", "codex", "japanese", "gen_ai.user.message", map[string]any{"prompt": "日本語検索の認証エラーを調査しました。猫が走った。パスワードとｶﾀｶﾅを確認。ＣＯＤＥＸ café", "service.namespace": "決済基盤", "project.tags": "運用監視"})
	save(t, a, e)
	for _, query := range []string{"検索", "認証", "エラー", "調査する", "猫", "走る", "ﾊﾟｽﾜｰﾄﾞ", "パスワード", "カタカナ", "ｶﾀｶﾅ", "codex", "ＣＯＤＥＸ", "cafe", "決済", "監視", "認証 エラー", "認"} {
		t.Run(query, func(t *testing.T) {
			rows := searchSessions(t, a, query)
			if len(rows) != 1 || rows[0].ID != e.Session {
				t.Fatalf("%q: %+v", query, rows)
			}
		})
	}
	for _, query := range []string{"未登録単語", "認証 バナナ", "の", "' OR 1=1; --", `"`, "()"} {
		if rows := searchSessions(t, a, query); len(rows) != 0 {
			t.Fatalf("unexpected match for %q: %+v", query, rows)
		}
	}
	if rows := searchSessions(t, a, "   "); len(rows) != 1 {
		t.Fatal("blank query should list sessions")
	}
	var rows []SessionSummary
	getJSON(t, a, "/api/sessions?source=claude&q="+url.QueryEscape("認証"), &rows)
	if len(rows) != 0 {
		t.Fatal("agent filter ignored")
	}
}
func TestSearchIndexesFullDecodedContent(t *testing.T) {
	a := setup(t)
	payload := strings.Repeat("uninteresting output ", 10000) + "最後の診断結果"
	e := recorded(t, "large", "import", "claude", "large", "gen_ai.tool.result", map[string]any{"output": payload, "nested": `{"report":"\u8a8d\u8a3c\u5931\u6557","README.md":"updated","設定項目":"有効"}`})
	save(t, a, e)
	for _, query := range []string{"診断", "認証", "失敗", "README", "設定項目"} {
		if len(searchSessions(t, a, query)) != 1 {
			t.Fatalf("full/decoded text not found: %s", query)
		}
	}
	p := page(t, a, e.Session, "", 0)
	for _, block := range p.Events[0].Detail.Blocks {
		if strings.Contains(block.Text, "診断結果") {
			t.Fatal("fixture did not exceed card preview")
		}
	}
	// The FTS table stores postings only, never another copy of the raw log.
	var content sql.NullString
	if err := a.DB.QueryRow("SELECT text FROM event_search LIMIT 1").Scan(&content); err != nil || content.Valid {
		t.Fatal("full-text content duplicated", err)
	}
}
func TestSearchUpdatesMovesAndDeletesAtomically(t *testing.T) {
	a := setup(t)
	event := recorded(t, "update", "import", "claude", "original", "gen_ai.user.message", map[string]any{"prompt": "認証失敗"})
	save(t, a, event)
	var rowid int64
	a.DB.QueryRow("SELECT rowid FROM search_documents WHERE event_id=?", event.ID).Scan(&rowid)
	event = recorded(t, "update", "import", "claude", "moved", "gen_ai.user.message", map[string]any{"prompt": "接続成功"})
	save(t, a, event)
	if len(searchSessions(t, a, "認証")) != 0 {
		t.Fatal("stale postings survived update")
	}
	found := searchSessions(t, a, "接続")
	if len(found) != 1 || found[0].ID != event.Session {
		t.Fatal(found)
	}
	var after int64
	a.DB.QueryRow("SELECT rowid FROM search_documents WHERE event_id=?", event.ID).Scan(&after)
	if rowid != after {
		t.Fatal("update unnecessarily changed FTS document identity")
	}
	tx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("DELETE FROM events WHERE id=?", event.ID); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	if len(searchSessions(t, a, "接続")) != 1 {
		t.Fatal("rollback removed search data")
	}
	tx, err = a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("DELETE FROM events WHERE id=?", event.ID); err == nil {
		err = refreshTimelines(context.Background(), tx)
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(searchSessions(t, a, "接続")) != 0 {
		t.Fatal("deleted event remained searchable")
	}
	var count int
	a.DB.QueryRow("SELECT COUNT(*) FROM event_search").Scan(&count)
	if count != 0 {
		t.Fatal("orphan search document", count)
	}
}
func TestSearchUpgradeAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	a, err := Open(path, token)
	if err != nil {
		t.Fatal(err)
	}
	event := oldEvent(t, "existing", "existing")
	event = recorded(t, "existing", "import", "codex", "existing", "gen_ai.user.message", map[string]any{"prompt": "既存データを検索"})
	save(t, a, event)
	a.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`DROP TRIGGER search_document_delete;DROP TRIGGER search_event_insert;DROP TRIGGER search_event_update;DROP TABLE search_dirty;DROP TABLE search_documents;DROP TABLE event_search`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	a, err = Open(path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if len(searchSessions(t, a, "既存")) != 1 {
		t.Fatal("existing records were not backfilled")
	}
	var rowid int64
	a.DB.QueryRow("SELECT rowid FROM search_documents WHERE event_id=?", event.ID).Scan(&rowid)
	a.DB.Exec("DELETE FROM search_documents WHERE event_id=?", event.ID)
	a.DB.Exec("INSERT INTO search_dirty VALUES(?)", event.ID)
	a.Close()
	b, err := Open(path, token)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if len(searchSessions(t, b, "検索")) != 1 {
		t.Fatal("interrupted index build did not resume")
	}
}
func TestSearchArchiveAndReplay(t *testing.T) {
	a := setup(t)
	event := oldEvent(t, "archive", "old")
	event.Raw = []byte(strings.ReplaceAll(string(event.Raw), "synthetic archive fixture", "認証情報の確認"))
	save(t, a, event)
	if len(searchSessions(t, a, "認証")) != 1 {
		t.Fatal("missing fixture")
	}
	job := newArchive(t, a)
	path := "/api/archives/" + job.ID
	archiveReq(a, "GET", path+"/download", "")
	w := archiveReq(a, "POST", path+"/delete", `{"confirmation":"DOWNLOADED"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	waitArchive(t, a, job.ID, "deleted")
	if len(searchSessions(t, a, "認証")) != 0 {
		t.Fatal("archived event still searchable")
	}
	save(t, a, event)
	if len(searchSessions(t, a, "認証")) != 0 {
		t.Fatal("archive replay resurrected index")
	}
}
func TestFTSQueryUsesPostingAndPrimaryKeyLookups(t *testing.T) {
	a := setup(t)
	save(t, a, oldEvent(t, "one", "old"))
	rows, err := a.DB.Query(`EXPLAIN QUERY PLAN SELECT d.session FROM event_search CROSS JOIN search_documents d ON d.rowid=event_search.rowid WHERE event_search MATCH ? GROUP BY d.session`, `"fixture"*`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plans []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, detail)
	}
	plan := strings.Join(plans, "\n")
	if !strings.Contains(plan, "VIRTUAL TABLE INDEX") || !strings.Contains(plan, "INTEGER PRIMARY KEY") {
		t.Fatal("index search plan changed", plan)
	}
}
func TestSearchRejectsExcessiveExpressions(t *testing.T) {
	a := setup(t)
	for _, query := range []string{strings.Repeat("検", 513), func() string {
		var terms []string
		for i := 0; i < 65; i++ {
			terms = append(terms, fmt.Sprint("word", i))
		}
		return strings.Join(terms, " ")
	}()} {
		w := archiveReq(a, "GET", "/api/sessions?q="+url.QueryEscape(query), "")
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
}

func TestJapaneseWordsAcrossAnalysisWindows(t *testing.T) {
	a := setup(t)
	for i, boundary := range []int{1023, 1024, 2047, 2048, 4095} {
		text := strings.Repeat("東京", boundary/2)
		if boundary%2 != 0 {
			text += "新"
		}
		text += "認証情報" + strings.Repeat("東京", 2500)
		e := recorded(t, fmt.Sprint(i), "import", "codex", fmt.Sprint("long", i), "gen_ai.user.message", map[string]any{"prompt": text})
		save(t, a, e)
	}
	if rows := searchSessions(t, a, "認証 情報"); len(rows) != 5 {
		t.Fatal("word lost at analysis boundary", len(rows))
	}
}
func BenchmarkJapaneseSearchAnalysis(b *testing.B) {
	loadJapanese()
	text := strings.Repeat("東京", 25000)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		count := 0
		if err := searchTokens(context.Background(), text, func(string) { count++ }); err != nil {
			b.Fatal(err)
		}
		if count == 0 {
			b.Fatal("no tokens")
		}
	}
}

func TestSearchMergeReclaimsDeletedPostings(t *testing.T) {
	a := setup(t)
	events := []Event{}
	for i := 0; i < 80; i++ {
		var text strings.Builder
		for word := 0; word < 400; word++ {
			fmt.Fprintf(&text, "vocabularytoken%06d ", i*400+word)
		}
		events = append(events, recorded(t, fmt.Sprint(i), "import", "codex", "merge", "gen_ai.user.message", map[string]any{"prompt": text.String()}))
	}
	save(t, a, events...)
	indexBytes := func() int64 {
		t.Helper()
		var size int64
		if err := a.DB.QueryRow("SELECT COALESCE(SUM(length(block)),0) FROM event_search_data").Scan(&size); err != nil {
			t.Fatal(err)
		}
		return size
	}
	before := indexBytes()
	tx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("DELETE FROM events"); err == nil {
		err = refreshTimelines(context.Background(), tx)
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = a.mergeSearchIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := indexBytes(); after >= before/4 {
		t.Fatalf("deleted posting storage retained: before=%d after=%d", before, after)
	}
	if _, err = a.DB.Exec("INSERT INTO event_search(event_search) VALUES('integrity-check')"); err != nil {
		t.Fatal(err)
	}
}
