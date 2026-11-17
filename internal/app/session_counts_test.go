package app

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestSessionCountCacheMatchesListVisibilityAndFilters(t *testing.T) {
	a := setup(t)
	question := func(id, source, project, prompt string) Event {
		return recorded(t, id, "otel", source, id, "gen_ai.user.message", map[string]any{"prompt": prompt, "service.namespace": project})
	}
	one := question("one", "codex", "A", "hello one")
	two := question("two", "claude", "A", "hello two")
	three := question("three", "codex", "B", "hello three")
	child := question("child", "codex", "A", "child")
	child.Parent = one.Session
	internal := question("internal", "codex", "A", automaticSuggestion)
	save(t, a, one, two, three, child, internal)
	check := func(query string, want int, cached bool) {
		t.Helper()
		tx, err := a.readers.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		params, err := url.ParseQuery(query)
		if err != nil {
			t.Fatal(err)
		}
		count, used, err := cachedRootSessionCount(context.Background(), tx, parseSessionFilter(params))
		tx.Rollback()
		if err != nil || used != cached || used && count != want {
			t.Fatalf("%s cache: count=%d used=%v err=%v", query, count, used, err)
		}
		r := httptest.NewRequest("GET", "/api/sessions?"+query, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("X-Total-Count") != strconv.Itoa(want) {
			t.Fatalf("%s list: %d total=%s", query, w.Code, w.Header().Get("X-Total-Count"))
		}
	}
	for _, test := range []struct {
		query  string
		want   int
		cached bool
	}{
		{"", 3, true}, {"diagnostics=1", 4, true}, {"project=name:A", 2, true},
		{"project=name:A&source=codex", 1, true}, {"project=name:A&source=codex&source=claude", 2, true},
		{"project=name:A&project=name:B", 3, true}, {"project=", 0, true}, {"source=other", 0, true},
		{"include_children=1", 3, true}, {"q=hello", 3, false},
		{"from=2026-11-20T00:00:00Z", 0, false}, {"folder=/absent", 0, false},
	} {
		check(test.query, test.want, test.cached)
	}
	// Pending externally written lock changes must fall back until the derived
	// counts have been published, rather than serving a stale total.
	if _, err := a.DB.Exec("INSERT INTO archive_jobs(id,status,created_at,cutoff) VALUES('count-lock','ready','',''); INSERT INTO archive_locks VALUES(?,'count-lock')", one.Session); err != nil {
		t.Fatal(err)
	}
	check("", 2, false)
	save(t, a)
	check("", 2, true)
	if _, err := a.DB.Exec("DELETE FROM archive_locks WHERE session=?", one.Session); err != nil {
		t.Fatal(err)
	}
	check("", 3, false)
	save(t, a)
	check("", 3, true)
}
