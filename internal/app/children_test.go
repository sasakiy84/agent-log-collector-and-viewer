package app

import (
	"fmt"
	"net/url"
	"testing"
)

func TestChildSessionPagesAreBoundedAndComplete(t *testing.T) {
	a := setup(t)
	events := []Event{recorded(t, "root", "otel", "codex", "root", "codex.user_prompt", map[string]any{"prompt": "hello"})}
	for i := 0; i < 47; i++ {
		e := recorded(t, fmt.Sprint(i), "otel", "codex", fmt.Sprintf("child-%02d", i), "codex.user_prompt", map[string]any{"prompt": "child"})
		e.Parent = "codex:root"
		events = append(events, e)
	}
	save(t, a, events...)
	var initial SessionPage
	getJSON(t, a, "/api/session?id=codex:root", &initial)
	if len(initial.Children) != 20 || initial.ChildrenNext == "" {
		t.Fatal("unbounded initial children", initial.Children)
	}
	all := append([]string{}, initial.Children...)
	next := initial.ChildrenNext
	for next != "" {
		var page childrenPage
		getJSON(t, a, "/api/session/children?session=codex:root&after="+url.QueryEscape(next), &page)
		if len(page.Children) > 20 {
			t.Fatal("unbounded child page")
		}
		all = append(all, page.Children...)
		next = page.Next
	}
	if len(all) != 47 {
		t.Fatal("missing children", len(all))
	}
	for i, id := range all {
		if id != fmt.Sprintf("codex:child-%02d", i) {
			t.Fatal("duplicate or reordered child", i, id)
		}
	}
}
