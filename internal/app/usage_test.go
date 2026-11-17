package app

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"
)

func sessionUsage(t *testing.T, a *App, id, origin string) UsageSummary {
	t.Helper()
	var p SessionPage
	getJSON(t, a, "/api/session?id="+url.QueryEscape(id)+"&origin="+origin, &p)
	return p.Usage
}
func usageWant(t *testing.T, s UsageSummary, key, want string) {
	t.Helper()
	if s.Values[key] != want {
		t.Fatalf("%s: want %q got %q: %+v", key, want, s.Values[key], s)
	}
}
func cliUsage(t *testing.T, e Event) Event {
	t.Helper()
	doc, err := decodeDocument(e.Raw)
	if err != nil {
		t.Fatal(err)
	}
	doc.Scope = map[string]any{"name": "agentlog.import"}
	doc.Record["attributes"] = append(arr(doc.Record["attributes"]), map[string]any{"key": "agentlog.import.id", "value": map[string]any{"stringValue": e.ID}})
	e.Raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func usageMetric(t *testing.T, id, name, typ, value string, start, end int64, temporality int) Event {
	t.Helper()
	e := recorded(t, id, "otel", "claude", "metrics", name, map[string]any{"type": typ})
	doc, err := decodeDocument(e.Raw)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{"name": name, "metricType": "sum", "aggregationTemporality": fmt.Sprint(temporality), "isMonotonic": true, "unit": "tokens", "startTimeUnixNano": fmt.Sprint(start), "timeUnixNano": fmt.Sprint(end), "asInt": value} {
		doc.Record[k] = v
	}
	e.Signal = "metrics"
	e.Time = timestamp(time.Unix(0, end))
	e.Raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestUsageExactArithmeticAndRepeatedResponseReports(t *testing.T) {
	a := setup(t)
	first := recorded(t, "first", "otel", "claude", "one", "api_request", map[string]any{"gen_ai.response.id": "response-1", "gen_ai.usage.input_tokens": "9007199254740993", "gen_ai.usage.output_tokens": "3", "cost_usd": "0.1", "duration_ms": "100"})
	last := recorded(t, "last", "otel", "claude", "one", "api_request", map[string]any{"gen_ai.response.id": "response-1", "gen_ai.usage.output_tokens": "0", "cost_usd": "0.2", "duration_ms": "120"})
	last.Sequence = 1
	second := recorded(t, "second", "otel", "claude", "one", "api_request", map[string]any{"gen_ai.usage.input_tokens": "1", "gen_ai.usage.output_tokens": "2", "cost_usd": "0.00000123", "duration_ms": "80"})
	second.Sequence = 2
	save(t, a, first, last, second)
	s := sessionUsage(t, a, "claude:one", "")
	usageWant(t, s, "input_tokens", "9007199254740994")
	usageWant(t, s, "output_tokens", "2")
	usageWant(t, s, "cost_usd", "0.20000123")
	usageWant(t, s, "duration_ms", "200")
	usageWant(t, s, "duration_samples", "2")
	// Replay cannot change aggregates, including exact zero and fractional USD.
	save(t, a, first, last, second)
	usageWant(t, sessionUsage(t, a, "claude:one", ""), "cost_usd", "0.20000123")
}
func TestUsageCumulativeEpochsAndOutOfOrderSamples(t *testing.T) {
	a := setup(t)
	s := int64(time.Second)
	save(t, a, usageMetric(t, "a", "claude_code.token.usage", "input", "10", s, 2*s, 2), usageMetric(t, "b", "claude_code.token.usage", "input", "15", s, 4*s, 2))
	usageWant(t, sessionUsage(t, a, "claude:metrics", ""), "input_tokens", "15")
	save(t, a, usageMetric(t, "older", "claude_code.token.usage", "input", "12", s, 3*s, 2), usageMetric(t, "restart", "claude_code.token.usage", "input", "3", 5*s, 6*s, 2))
	usageWant(t, sessionUsage(t, a, "claude:metrics", ""), "input_tokens", "18")
	save(t, a, usageMetric(t, "cache", "claude_code.token.usage", "cacheRead", "5", s, 4*s, 2))
	u := sessionUsage(t, a, "claude:metrics", "")
	usageWant(t, u, "input_tokens", "23")
	usageWant(t, u, "cache_read_tokens", "5")
}
func TestUsageDeltaIntervalsReplayAndOverlapRecovery(t *testing.T) {
	a := setup(t)
	s := int64(time.Second)
	first := usageMetric(t, "a", "claude_code.token.usage", "output", "10", s, 2*s, 1)
	last := usageMetric(t, "b", "claude_code.token.usage", "output", "20", 2*s, 3*s, 1)
	save(t, a, first, last)
	usageWant(t, sessionUsage(t, a, "claude:metrics", ""), "output_tokens", "30")
	duplicate := last
	duplicate.ID = "retry"
	save(t, a, duplicate)
	usageWant(t, sessionUsage(t, a, "claude:metrics", ""), "output_tokens", "30")
	overlap := usageMetric(t, "overlap", "claude_code.token.usage", "output", "100", s+s/2, 2*s+s/2, 1)
	save(t, a, overlap)
	u := sessionUsage(t, a, "claude:metrics", "")
	if _, known := u.Values["output_tokens"]; known || u.Excluded != 3 {
		t.Fatal("overlapping ranges were added", u)
	}
	if _, err := a.DB.Exec("DELETE FROM records WHERE id=?", overlap.ID); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	usageWant(t, sessionUsage(t, a, "claude:metrics", ""), "output_tokens", "30")
}
func TestUsageOriginsAndSignalsAreAlternatives(t *testing.T) {
	a := setup(t)
	imported := recorded(t, "imported", "import", "claude", "one", "gen_ai.client.operation", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": "100", "gen_ai.usage.output_tokens": "10", "gen_ai.usage.cache_read.input_tokens": "20"})
	imported = cliUsage(t, imported)
	native := recorded(t, "native", "otel", "claude", "one", "api_request", map[string]any{"input_tokens": "200", "output_tokens": "30", "cache_read_tokens": "40", "github.copilot.cost": "2"})
	save(t, a, imported, native)
	u := sessionUsage(t, a, "claude:one", "")
	usageWant(t, u, "input_tokens", "240")
	if !u.Overlap || len(u.Variants) != 2 {
		t.Fatal(u)
	}
	usageWant(t, sessionUsage(t, a, "claude:one", "import"), "input_tokens", "120")
	if _, known := u.Values["cost_usd"]; known {
		t.Fatal("a billing multiplier became USD", u)
	}
}
func TestUsageErrorsFollowLogicalTimelineAndDeletion(t *testing.T) {
	a := setup(t)
	imported := recorded(t, "call", "import", "codex", "one", "gen_ai.tool.result", map[string]any{"gen_ai.tool.call.id": "call", "success": false, "error": "failed"})
	native := recorded(t, "native", "otel", "codex", "one", "tool_result", map[string]any{"call_id": "call", "success": false, "error": "failed"})
	save(t, a, imported, native)
	usageWant(t, sessionUsage(t, a, "codex:one", ""), "errors", "1")
	usageWant(t, sessionUsage(t, a, "codex:one", "otel"), "errors", "1")
	changed := recorded(t, "call", "import", "codex", "one", "gen_ai.tool.result", map[string]any{"gen_ai.tool.call.id": "call", "success": true})
	save(t, a, changed)
	usageWant(t, sessionUsage(t, a, "codex:one", ""), "errors", "1")
	if _, err := a.DB.Exec("DELETE FROM records WHERE id=?", native.ID); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	usageWant(t, sessionUsage(t, a, "codex:one", ""), "errors", "0")
	if _, err := a.DB.Exec("DELETE FROM records"); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	for _, table := range []string{"usage_samples", "usage_groups", "usage_totals", "session_usage", "timeline_errors", "event_errors"} {
		var n int
		if err := a.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
}
func TestUsageCodexCountersAndLegacyImports(t *testing.T) {
	a := setup(t)
	for i, n := range []string{"100", "100", "250"} {
		e := recorded(t, fmt.Sprint(i), "import", "codex", "one", "gen_ai.client.operation", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": "100", "agentlog.usage.counter": true, "agentlog.usage.total.input_tokens": n, "agentlog.usage.total.output_tokens": "20"})
		e.Sequence = i
		save(t, a, e)
	}
	usageWant(t, sessionUsage(t, a, "codex:one", ""), "input_tokens", "250")
	legacy := recorded(t, "old", "import", "codex", "legacy", "gen_ai.client.operation", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": "100"})
	legacy = cliUsage(t, legacy)
	save(t, a, legacy)
	u := sessionUsage(t, a, "codex:legacy", "")
	if _, known := u.Values["input_tokens"]; known || u.Legacy != 1 {
		t.Fatal("legacy last-token samples were summed", u)
	}
}
func TestUsageFolderTotalsRespectSearchAndCoverage(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "one", "otel", "claude", "one", "api_request", map[string]any{"cwd": "/work/app", "input_tokens": "100", "output_tokens": "20", "cost_usd": "0", "prompt": "needle"}), recorded(t, "two", "otel", "codex", "two", "user_prompt", map[string]any{"cwd": "/work/app", "prompt": "other"}))
	var p folderPage
	getJSON(t, a, "/api/folders", &p)
	u := p.Folders[0].Usage
	usageWant(t, u, "input_tokens", "100")
	usageWant(t, u, "cost_usd", "0")
	if u.Sessions != 2 || u.Coverage["input_tokens"] != 1 || u.Coverage["cost_usd"] != 1 {
		t.Fatal(u)
	}
	getJSON(t, a, "/api/folders?q=needle", &p)
	if p.Folders[0].Usage.Sessions != 1 {
		t.Fatal(p)
	}
}

func TestUsageDecimalPrecisionAndUSD(t *testing.T) {
	a := setup(t)
	for i, value := range []string{"0.0000000000000000005", "0.00000000000000000025"} {
		e := recorded(t, fmt.Sprint(i), "otel", "codex", "precision", "api_request", map[string]any{"cost_usd": value})
		save(t, a, e)
	}
	u := sessionUsage(t, a, "codex:precision", "")
	if _, known := u.Values["cost_usd"]; known || u.Excluded != 2 {
		t.Fatal("subprecision values were rounded into money", u)
	}
	e := recorded(t, "alias", "otel", "codex", "alias", "custom_event", map[string]any{"cost_usd": "0.000000000000000001"})
	save(t, a, e)
	usageWant(t, sessionUsage(t, a, "codex:alias", ""), "cost_usd", "0.000000000000000001")
}

func TestUsageNanosecondIntervalsAcrossDayBoundaries(t *testing.T) {
	a := setup(t)
	base := time.Date(2026, 11, 15, 23, 59, 59, 0, time.UTC).UnixNano()
	for i, interval := range [][2]int64{{999999999, 1000000001}, {999999998, 999999999}, {1000000001, 1000000002}, {0, 999999998}} {
		save(t, a, usageMetric(t, fmt.Sprint(i), "claude_code.token.usage", "output", "1", base+interval[0], base+interval[1], 1))
	}
	usageWant(t, sessionUsage(t, a, "claude:metrics", ""), "output_tokens", "4")
	var count int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM usage_interval_index").Scan(&count); err != nil || count != 4 {
		t.Fatal(count, err)
	}
	if _, err := a.DB.Exec("DELETE FROM records"); err != nil {
		t.Fatal(err)
	}
	save(t, a)
	for _, table := range []string{"usage_interval_index", "usage_interval_data", "usage_streams"} {
		if err := a.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
}

func TestUsageConflictingUnorderedReportsAreUnknown(t *testing.T) {
	a := setup(t)
	for i, value := range []string{"10", "20"} {
		e := recorded(t, fmt.Sprint(i), "otel", "claude", "conflict", "api_request", map[string]any{"gen_ai.response.id": "same-response", "gen_ai.usage.input_tokens": "100", "gen_ai.usage.output_tokens": value, "duration_ms": value})
		save(t, a, e)
	}
	u := sessionUsage(t, a, "claude:conflict", "")
	usageWant(t, u, "input_tokens", "100")
	if _, known := u.Values["output_tokens"]; known || u.Excluded != 1 {
		t.Fatal(u)
	}
	if _, known := u.Values["duration_ms"]; known {
		t.Fatal(u)
	}
}

func TestUsageUSDCumulativeCounterIsAlternativeToEventCost(t *testing.T) {
	a := setup(t)
	s := int64(time.Second)
	for i, amount := range []string{"0.1", "0.3"} {
		e := usageMetric(t, fmt.Sprint(i), "claude_code.cost.usage", "", "0", s, int64(i+2)*s, 2)
		doc, err := decodeDocument(e.Raw)
		if err != nil {
			t.Fatal(err)
		}
		delete(doc.Record, "asInt")
		doc.Record["asDouble"] = json.Number(amount)
		doc.Record["unit"] = "USD"
		e.Raw, err = json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		save(t, a, e)
	}
	save(t, a, recorded(t, "log", "otel", "claude", "metrics", "api_request", map[string]any{"cost_usd": "0.1"}))
	u := sessionUsage(t, a, "claude:metrics", "")
	usageWant(t, u, "cost_usd", "0.3")
	if !u.Overlap {
		t.Fatal(u)
	}
}

func TestUsageSSEProgressIsNotRequestLatency(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "progress", "otel", "codex", "timing", "codex.sse_event", map[string]any{"event.kind": "response.output_text.delta", "duration_ms": "5"}), recorded(t, "completed", "otel", "codex", "timing", "codex.sse_event", map[string]any{"event.kind": "response.completed", "duration_ms": "100", "output_token_count": "10"}))
	u := sessionUsage(t, a, "codex:timing", "")
	usageWant(t, u, "duration_ms", "100")
	usageWant(t, u, "duration_samples", "1")
}

func TestUsageStandardOTELImportsKeepInclusiveInput(t *testing.T) {
	a := setup(t)
	for _, source := range []string{"claude", "codex"} {
		save(t, a, recorded(t, source, "import", source, "standard", "gen_ai.client.operation", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.input_tokens": "100", "gen_ai.usage.output_tokens": "20", "gen_ai.usage.cache_read.input_tokens": "30"}))
		u := sessionUsage(t, a, source+":standard", "")
		usageWant(t, u, "input_tokens", "100")
		if u.Legacy != 0 {
			t.Fatal(u)
		}
	}
}

func TestUsagePartialCacheReportsDoNotInventInput(t *testing.T) {
	a := setup(t)
	save(t, a, recorded(t, "partial", "otel", "codex", "partial", "gen_ai.client.operation", map[string]any{"gen_ai.operation.name": "chat", "gen_ai.usage.cache_read.input_tokens": "30"}))
	u := sessionUsage(t, a, "codex:partial", "")
	usageWant(t, u, "cache_read_tokens", "30")
	if _, known := u.Values["input_tokens"]; known {
		t.Fatal(u)
	}
}
