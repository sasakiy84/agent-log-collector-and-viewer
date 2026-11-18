package app

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestConversationGroupCountsUseLogicalPositionsAcrossPages(t *testing.T) {
	a := setup(t)
	var events []Event
	for i := 0; i < 12; i++ {
		for _, origin := range []string{"import", "otel"} {
			e := recorded(t, fmt.Sprintf("question-%s-%02d", origin, i), origin, "codex", "bounds", "gen_ai.user.message", map[string]any{"event.id": fmt.Sprint("question-", i), "prompt": "Question"})
			e.Sequence = i * 10
			if origin == "otel" {
				e.Sequence += 3 // The merged position differs from the imported card.
			}
			events = append(events, e)
		}
		for _, sequence := range []int{1, 4, 8} {
			e := recorded(t, fmt.Sprintf("processing-%02d-%d", i, sequence), "otel", "codex", "bounds", "codex.tool_result", map[string]any{"output": "Processing", "success": i%3 != 0})
			e.Sequence = i*10 + sequence
			events = append(events, e)
		}
		e := recorded(t, fmt.Sprintf("answer-%02d", i), "import", "codex", "bounds", "gen_ai.assistant.message", map[string]any{"response": "Answer"})
		e.Sequence = i*10 + 9
		events = append(events, e)
	}
	save(t, a, events...)
	for _, origin := range []string{"", "import", "otel"} {
		// An independent chronological walk computes the reference groups;
		// equal timestamps and merged sequence numbers must not lose logs.
		type entry struct {
			id                             string
			conversation, activity, errors int
		}
		query, args := timelineScope("codex:bounds", origin)
		rows, err := a.DB.Query("SELECT id,conversation,activity,is_error FROM "+timelineTable(origin)+" WHERE "+query+" ORDER BY time,sequence,id", args...)
		if err != nil {
			t.Fatal(err)
		}
		var entries []entry
		for rows.Next() {
			var e entry
			if err = rows.Scan(&e.id, &e.conversation, &e.activity, &e.errors); err != nil {
				t.Fatal(err)
			}
			entries = append(entries, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		var all []ProcessingGroup
		g := ProcessingGroup{}
		zeroSeconds := int64(0)
		for _, e := range entries {
			if e.conversation == 1 {
				g.Before = e.id
				if g.Count > 0 {
					all = append(all, g)
				}
				g = ProcessingGroup{After: e.id}
			} else {
				g.Count++
				g.DurationSeconds = &zeroSeconds
				g.Activity += e.activity
				g.Errors += e.errors
			}
		}
		if g.Count > 0 {
			all = append(all, g)
		}
		for _, paging := range []string{"offset=0", "offset=7", "position=latest"} {
			var page SessionPage
			getJSON(t, a, "/api/session?id=codex:bounds&view=conversation&origin="+origin+"&limit=7&"+paging, &page)
			visible := map[string]bool{}
			for _, e := range page.Events {
				visible[e.ID] = true
			}
			want := []ProcessingGroup{}
			for _, group := range all {
				if visible[group.Before] || group.Before == "" && page.Offset+len(page.Events) >= page.Count {
					want = append(want, group)
				}
			}
			if len(page.Processing) != len(want) || len(want) > 0 && !reflect.DeepEqual(page.Processing, want) {
				t.Fatalf("origin=%q %s: got %+v, want %+v", origin, paging, page.Processing, want)
			}
		}
	}
}

func TestProcessingDurationUsesKnownLogTimesWithinOrigin(t *testing.T) {
	a := setup(t)
	add := func(id, origin, name, stamp string, attrs map[string]any) Event {
		e := recorded(t, id, origin, "codex", "timing", name, attrs)
		e.Time = stamp
		return e
	}
	save(t, a,
		add("unknown", "otel", "prepare_tools", "1970-01-01T00:00:00.000000000Z", nil),
		add("question", "otel", "codex.user_prompt", "2026-11-15T00:00:00.000000000Z", map[string]any{"prompt": "Start"}),
		add("first", "otel", "prepare_tools", "2026-11-15T00:01:00.000000000Z", nil),
		add("second", "otel", "prepare_tools", "2026-11-15T00:02:00.000000000Z", nil),
		add("imported", "import", "prepare_tools", "2026-11-16T02:06:00.000000000Z", nil),
		add("answer", "otel", "codex.agent_response", "2026-11-16T02:10:00.000000000Z", map[string]any{"response": "Done"}),
	)
	for _, test := range []struct {
		origin  string
		seconds int64
	}{{"", 26*3600 + 5*60}, {"otel", 60}} {
		var page SessionPage
		getJSON(t, a, "/api/session?id=codex:timing&view=conversation&limit=1&position=latest&origin="+test.origin, &page)
		if len(page.Processing) != 1 || page.Processing[0].DurationSeconds == nil || *page.Processing[0].DurationSeconds != test.seconds {
			t.Fatalf("origin %q: %+v", test.origin, page.Processing)
		}
		page = SessionPage{}
		getJSON(t, a, "/api/session?id=codex:timing&view=conversation&limit=1&origin="+test.origin, &page)
		if len(page.Processing) != 1 || page.Processing[0].DurationSeconds != nil {
			t.Fatalf("unknown timestamps must not have a duration: %+v", page.Processing)
		}
	}
}

func TestConversationRangesAndLazyPagination(t *testing.T) {
	a := setup(t)
	batch := []Event{}
	add := func(id, name string, at map[string]any, sequence int) {
		e := recorded(t, id, "otel", "codex", "chat", name, at)
		e.Sequence = sequence // Equal timestamps must still form disjoint ranges.
		batch = append(batch, e)
	}
	add("leading", "prepare_tools", nil, -1)
	add("user", "codex.user_prompt", map[string]any{"prompt": "hello"}, 0)
	for i := 1; i <= 105; i++ {
		add(fmt.Sprintf("tool-%03d", i), "codex.tool_result", map[string]any{"tool_name": "exec_command", "output": "large processing body"}, i)
	}
	add("failure", "internal_failure", map[string]any{"error": "important failure"}, 106)
	add("answer", "codex.agent_response", map[string]any{"response": "answer"}, 107)
	add("trailing", "prepare_tools", nil, 108)
	save(t, a, batch...)
	var page SessionPage
	getJSON(t, a, "/api/session?id=codex:chat&view=conversation", &page)
	if page.Count != 2 || len(page.Events) != 2 || page.TotalCount != 110 || len(page.Processing) != 3 {
		t.Fatalf("conversation page: %+v", page)
	}
	if g := page.Processing[1]; g.After != "user" || g.Before != "answer" || g.Count != 106 || g.Activity != 106 || g.Errors != 1 {
		t.Fatal(g)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "large processing body") || strings.Contains(string(encoded), "important failure") {
		t.Fatal("collapsed log bodies loaded with the conversation")
	}
	seen := map[string]bool{}
	cursor := ""
	for request := 0; request < 3; request++ {
		var logs processingPage
		getJSON(t, a, "/api/processing?session=codex:chat&after=user&before=answer&cursor="+url.QueryEscape(cursor), &logs)
		if len(logs.Events) > 50 {
			t.Fatal("unbounded expansion", len(logs.Events))
		}
		for _, e := range logs.Events {
			if seen[e.ID] || e.ID == "user" || e.ID == "answer" || e.ID == "trailing" {
				t.Fatal("duplicate or out-of-range processing event", e.ID)
			}
			seen[e.ID] = true
		}
		cursor = logs.Next
	}
	if len(seen) != 106 || cursor != "" {
		t.Fatal("lost logs during cursor pagination", len(seen), cursor)
	}
	getJSON(t, a, "/api/session?id=codex:chat&view=conversation&limit=1&position=latest", &page)
	if page.Offset != 1 || len(page.Events) != 1 || page.Events[0].ID != "answer" || len(page.Processing) != 2 || page.Processing[0].After != "user" {
		t.Fatal("conversation-page boundaries lost processing", page)
	}
}

func TestConversationSearchAndSourceIsolation(t *testing.T) {
	a := setup(t)
	save(t, a,
		recorded(t, "question", "import", "codex", "chat", "gen_ai.user.message", map[string]any{"prompt": "hello"}),
		recorded(t, "imported", "import", "codex", "chat", "gen_ai.tool.result", map[string]any{"call_id": "same", "tool_name": "exec_command", "output": "日本語の認証エラー"}),
		recorded(t, "native", "otel", "codex", "chat", "codex.tool_result", map[string]any{"call_id": "same", "tool_name": "exec_command", "output": "日本語の認証エラー"}),
		recorded(t, "other", "otel", "codex", "other", "codex.user_prompt", map[string]any{"prompt": "other session"}),
	)
	match := url.QueryEscape("q=" + url.QueryEscape("認証"))
	var page SessionPage
	getJSON(t, a, "/api/session?id=codex:chat&view=conversation&match="+match, &page)
	if len(page.Processing) != 1 || page.Processing[0].Count != 1 || page.Processing[0].Matches != 1 {
		t.Fatal("merged search evidence counted twice", page.Processing)
	}
	var logs processingPage
	getJSON(t, a, "/api/processing?session=codex:chat&matches=1&diagnostics=1&match="+match, &logs)
	if len(logs.Events) != 1 || len(logs.Events[0].Records) != 2 {
		t.Fatal("processing search lost the merged record", logs)
	}
	getJSON(t, a, "/api/processing?session=codex:chat&origin=otel", &logs)
	if len(logs.Events) != 1 || logs.Events[0].ID != "native" || len(logs.Events[0].Records) != 1 {
		t.Fatal("import leaked into OTEL-only processing", logs)
	}
	// A log matching an old page must be reachable directly from search,
	// rather than landing on the latest 200 unrelated conversation entries.
	answers := []Event{}
	for i := 0; i < 205; i++ {
		e := recorded(t, fmt.Sprintf("later-%03d", i), "otel", "codex", "chat", "codex.agent_response", map[string]any{"response": "later explanation"})
		e.Sequence = i + 1
		answers = append(answers, e)
	}
	save(t, a, answers...)
	getJSON(t, a, "/api/session?id=codex:chat&view=conversation&position=latest&match="+match, &page)
	if page.Offset != 0 || page.MatchID != "import:imported" || len(page.Events) != 200 || page.Processing[0].Matches != 1 {
		t.Fatal("search did not locate the old conversation page", page.Offset, page.MatchID, len(page.Events), page.Processing)
	}
	for _, path := range []string{
		"/api/processing?session=codex:chat&after=other",
		"/api/processing?session=codex:chat&cursor=other",
		"/api/processing?session=codex:chat&after=import:imported",
	} {
		if w := archiveReq(a, "GET", path, ""); w.Code != 409 {
			t.Fatal("accepted cross-session or non-conversation boundary", path, w.Code)
		}
	}
	if _, err := a.DB.Exec("INSERT INTO archive_jobs(id,status,created_at,cutoff) VALUES('fixture','ready','',''); INSERT INTO archive_locks VALUES('codex:chat','fixture')"); err != nil {
		t.Fatal(err)
	}
	if w := archiveReq(a, "GET", "/api/processing?session=codex:chat", ""); w.Code != 409 {
		t.Fatal("processing bypassed archive lock", w.Code)
	}
}

func TestConversationOmitsInjectedContextAndKeepsEmbeddedMessages(t *testing.T) {
	a := setup(t)
	save(t, a,
		recorded(t, "context", "import", "codex", "context", "gen_ai.user.message", map[string]any{"prompt": "<external_codex_apps_selected_page_part_1_of_1>snapshot</external_codex_apps_selected_page_part_1_of_1>"}),
		recorded(t, "request", "import", "codex", "context", "gen_ai.user.message", map[string]any{"prompt": "<environment_context>injected</environment_context>\nhello"}),
		recorded(t, "tools-only", "otel", "codex", "no-text", "codex.tool_result", map[string]any{"tool_name": "exec_command", "output": "still inspectable"}),
		recorded(t, "embedded", "otel", "copilot", "embedded", "chat", map[string]any{"gen_ai.input.messages": `[{"role":"user","content":"question"}]`, "gen_ai.output.messages": `[{"role":"assistant","content":"answer"}]`}),
	)
	var page SessionPage
	getJSON(t, a, "/api/session?id=codex:context&view=conversation", &page)
	if page.Count != 1 || page.Events[0].Detail.Blocks[0].Text != "hello" || len(page.Processing) != 1 {
		t.Fatal("injected context shown as human input", page)
	}
	getJSON(t, a, "/api/session?id=codex:no-text&view=conversation", &page)
	if page.Count != 0 || len(page.Events) != 0 || len(page.Processing) != 1 {
		t.Fatal("tools-only session became inaccessible", page)
	}
	getJSON(t, a, "/api/session?id=copilot:embedded&view=conversation", &page)
	if page.Count != 1 || len(page.Events[0].Detail.Blocks) != 2 {
		t.Fatal("embedded conversation lost", page)
	}
}

func TestGuardianClassificationKeepsToolPairsIndependent(t *testing.T) {
	a := setup(t)
	start := recorded(t, "start", "import", "codex", "guardian", "gen_ai.tool.start", map[string]any{"call_id": "call", "tool_name": "write_stdin", "arguments": "input"})
	result := recorded(t, "result", "import", "codex", "guardian", "gen_ai.tool.result", map[string]any{"call_id": "call", "tool_name": "write_stdin", "output": "real output"})
	guardian := modelSpan(t, "guardian", "guardian", "observation", "", "guardian_scoring_observation", map[string]any{"call_id": "call", "tool_name": "write_stdin", "code.module.name": "codex_guardian_v2::async_scorer::observation"})
	save(t, a, start, result, guardian)
	check := func() {
		t.Helper()
		if pair := inspected(t, a, "codex:guardian", start.ID, "").ToolPair; pair.State != "paired" || pair.Counterpart.ID != result.ID {
			t.Fatal("Guardian polluted real tool pairing", pair)
		}
		e := inspected(t, a, "codex:guardian", guardian.ID, "")
		if e.Detail.Kind != "event" || !e.Detail.Diagnostic || e.ToolPair != nil || e.Detail.ToolPhase != "" || e.Detail.Title != "Guardian internal observation" {
			t.Fatal(e)
		}
		var p SessionPage
		getJSON(t, a, "/api/session?id=codex:guardian&view=activity&kind=tool", &p)
		if p.Count != 2 {
			t.Fatal("Guardian counted as tool execution", p.Count)
		}
	}
	check()
}
