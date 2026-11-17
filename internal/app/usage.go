package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"modernc.org/sqlite"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("agentlog_add", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		a, ok := new(big.Rat).SetString(fmt.Sprint(args[0]))
		if !ok {
			return nil, errors.New("invalid stored decimal")
		}
		b, ok := new(big.Rat).SetString(fmt.Sprint(args[1]))
		if !ok {
			return nil, errors.New("invalid stored decimal")
		}
		return decimalText(a.Add(a, b)), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("agentlog_neg", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		a, ok := new(big.Rat).SetString(fmt.Sprint(args[0]))
		if !ok {
			return nil, errors.New("invalid stored decimal")
		}
		return decimalText(a.Neg(a)), nil
	})
}

func decimalText(n *big.Rat) string {
	if n.IsInt() {
		return n.Num().String()
	}
	return strings.TrimRight(strings.TrimRight(n.FloatString(18), "0"), ".")
}
func measurement(v any, integer bool) (string, bool) {
	text := str(v)
	if len(text) == 0 || len(text) > 128 {
		return "", false
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1e30 {
		return "", false
	}
	n, ok := new(big.Rat).SetString(text)
	if !ok || n.Sign() < 0 || integer && !n.IsInt() {
		return "", false
	}
	// Bound precision and exponent before doing exact arithmetic on telemetry.
	denominator := new(big.Int).Set(n.Denom())
	scale := 0
	for _, factor := range []int64{2, 5} {
		count := 0
		for new(big.Int).Mod(denominator, big.NewInt(factor)).Sign() == 0 {
			denominator.Div(denominator, big.NewInt(factor))
			count++
			if count > 18 {
				return "", false
			}
		}
		if count > scale {
			scale = count
		}
	}
	if len(n.Num().String()) > 64 || denominator.Cmp(big.NewInt(1)) != 0 || scale > 18 {
		return "", false
	}
	return decimalText(n), true
}
func usageHash(v any) string {
	data, _ := json.Marshal(v)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
func nanoKey(v any) string {
	s := str(v)
	if len(s) > 20 {
		return ""
	}
	if _, err := strconv.ParseUint(s, 10, 64); err != nil {
		return ""
	}
	return strings.Repeat("0", 20-len(s)) + s
}

type usagePoint struct {
	Basis, Key, Series, Start, End string
	Values                         map[string]string
}

func usageMeasurement(e Event, doc document, p Detail) *usagePoint {
	a, r := doc.attributes(), doc.Record
	cliImport := str(doc.Scope["name"]) == "agentlog.import" && present(a["agentlog.import.id"])
	u := &usagePoint{Basis: "logs", Key: e.ID, Values: map[string]string{}}
	if e.Signal == "traces" {
		u.Basis = "traces"
	}
	put := func(component string, integer bool, keys ...string) {
		v := firstValue(a, keys...)
		if v == nil {
			return
		}
		if text, ok := measurement(v, integer); ok {
			u.Values[component] = text
		} else {
			u.Values["excluded"] = "1"
		}
	}
	if e.Signal == "metrics" {
		name := str(r["name"])
		component := ""
		switch name {
		case "gen_ai.client.token.usage", "claude_code.token.usage":
			typ := str(firstValue(a, "gen_ai.token.type", "type", "token.type"))
			component = map[string]string{"input": "input_tokens", "output": "output_tokens", "cacheRead": "cache_read_tokens", "cache_read": "cache_read_tokens", "cacheCreation": "cache_write_tokens", "cache_write": "cache_write_tokens", "reasoning": "reasoning_tokens"}[typ]
		case "gen_ai.client.inference.usage.input_tokens":
			component = "input_tokens"
		case "gen_ai.client.inference.usage.output_tokens":
			component = "output_tokens"
		case "gen_ai.client.inference.usage.cache_read.input_tokens":
			component = "cache_read_tokens"
		case "gen_ai.client.inference.usage.cache_write.input_tokens":
			component = "cache_write_tokens"
		case "gen_ai.client.inference.usage.reasoning.output_tokens":
			component = "reasoning_tokens"
		case "claude_code.cost.usage":
			if strings.EqualFold(str(r["unit"]), "USD") {
				component = "cost_usd"
			}
		case "gen_ai.client.operation.duration", "gen_ai.client.inference.duration":
			if e.Source == "copilot" && a["gen_ai.operation.name"] != nil && a["gen_ai.operation.name"] != "chat" {
				return nil
			}
			if str(r["unit"]) == "s" {
				component = "duration_ms"
			}
		default:
			return nil
		}
		if component == "" {
			u.Values["excluded"] = "1"
			return u
		}
		u.Start = nanoKey(r["startTimeUnixNano"])
		u.End = nanoKey(r["timeUnixNano"])
		temporality := str(r["aggregationTemporality"])
		switch temporality {
		case "1", "AGGREGATION_TEMPORALITY_DELTA":
			u.Basis = "metric-delta"
		case "2", "AGGREGATION_TEMPORALITY_CUMULATIVE":
			u.Basis = "metric-cumulative"
		default:
			u.Values["excluded"] = "1"
			return u
		}
		kind := str(r["metricType"])
		flags, _ := strconv.Atoi(str(r["flags"]))
		if u.Start == "" || u.Start == strings.Repeat("0", 20) || u.End == "" || u.End <= u.Start || flags&1 != 0 || (kind != "sum" && kind != "histogram" && kind != "exponentialHistogram") || (kind == "sum" && str(r["isMonotonic"]) != "true") {
			u.Values["excluded"] = "1"
			return u
		}
		v := firstValue(r, "asInt", "asDouble")
		if kind != "sum" {
			v = r["sum"]
		}
		value, ok := measurement(v, component != "cost_usd" && component != "duration_ms")
		if !ok {
			u.Values["excluded"] = "1"
			return u
		}
		if component == "duration_ms" {
			n, _ := new(big.Rat).SetString(value)
			value = decimalText(n.Mul(n, big.NewRat(1000, 1)))
			count, ok := measurement(r["count"], true)
			if !ok {
				u.Values["excluded"] = "1"
				return u
			}
			u.Values["duration_samples"] = count
		}
		u.Values[component] = value
		if name == "claude_code.token.usage" && (component == "cache_read_tokens" || component == "cache_write_tokens") {
			u.Values["input_tokens"] = value
		}
		u.Series = usageHash([]any{e.Source, name, decodedAttributes(doc.Resource["attributes"]), []any{doc.Scope["name"], doc.Scope["version"], decodedAttributes(doc.Scope["attributes"])}, a})
		u.Key = usageHash([]string{u.Series, u.Start})
		if u.Basis == "metric-delta" {
			u.Key = usageHash([]string{u.Series, u.Start, u.End})
		}
		return u
	}
	// Copilot invocation spans aggregate the child chat spans. Count model usage
	// once at inference level; the invocation's AI units have their own family.
	if e.Source == "copilot" && e.Signal == "traces" && a["gen_ai.operation.name"] == "invoke_agent" {
		if e.ParentSpan == "" || present(a["server.address"]) {
			put("nano_aiu", true, "github.copilot.nano_aiu")
		}
		if len(u.Values) == 0 {
			return nil
		}
		return u
	}
	for component, keys := range map[string][]string{
		"input_tokens": {"gen_ai.usage.input_tokens", "input_tokens", "input_token_count"}, "output_tokens": {"gen_ai.usage.output_tokens", "output_tokens", "output_token_count"},
		"cache_read_tokens": {"gen_ai.usage.cache_read.input_tokens", "cache_read_tokens", "cached_token_count"}, "cache_write_tokens": {"gen_ai.usage.cache_write.input_tokens", "cache_creation_tokens", "cache_write_token_count"},
		"reasoning_tokens": {"gen_ai.usage.reasoning.output_tokens", "reasoning_token_count"},
	} {
		put(component, true, keys...)
	}
	put("cost_usd", false, "cost_usd")
	// Anthropic's native input_tokens excludes cache reads/writes. OTEL's
	// gen_ai.usage.input_tokens includes them; keep a common inclusive definition.
	if e.Source == "claude" && a["gen_ai.usage.input_tokens"] == nil {
		if input, ok := u.Values["input_tokens"]; ok {
			n, _ := new(big.Rat).SetString(input)
			for _, k := range []string{"cache_read_tokens", "cache_write_tokens"} {
				if v, ok := u.Values[k]; ok {
					x, _ := new(big.Rat).SetString(v)
					n.Add(n, x)
				}
			}
			u.Values["input_tokens"] = decimalText(n)
		}
	}
	if e.Source == "claude" && e.Origin == "import" && cliImport && a["gen_ai.usage.input_tokens"] != nil { // Older CLI imports used the raw Anthropic input count.
		if input, ok := u.Values["input_tokens"]; ok && str(a["agentlog.usage.input_includes_cache"]) != "true" {
			n, _ := new(big.Rat).SetString(input)
			for _, k := range []string{"cache_read_tokens", "cache_write_tokens"} {
				if v, ok := u.Values[k]; ok {
					x, _ := new(big.Rat).SetString(v)
					n.Add(n, x)
				}
			}
			u.Values["input_tokens"] = decimalText(n)
		}
	}
	if e.Source == "codex" && e.Origin == "import" && p.Kind == "model" && (cliImport || str(a["agentlog.usage.counter"]) == "true") {
		if str(a["agentlog.usage.counter"]) == "true" {
			u.Basis = "codex-total"
			u.Key = "session-total"
			u.Values = map[string]string{}
			for _, k := range []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens"} {
				put(k, true, "agentlog.usage.total."+k)
			}
			if str(a["agentlog.usage.counter_incomplete"]) == "true" || len(u.Values) == 0 {
				u.Values = map[string]string{"excluded": "1"}
			}
		} else {
			u.Values = map[string]string{"legacy": "1"}
		}
	} else if id, ok := firstValue(a, "gen_ai.response.id", "response.id", "response_id", "request_id", "gen_ai.message.id", "message.id", "message_id").(string); ok && id != "" && len(id) <= 512 {
		u.Key = usageHash([]string{"response", id})
	} else if e.Trace != "" && e.Span != "" {
		u.Key = "span:" + e.Trace + ":" + e.Span
	}
	if p.Kind == "model" && u.Basis != "codex-total" && u.Values["legacy"] == "" {
		completed := containsAny(p.Name, "api_request", "llm_request") || str(firstValue(a, "event.kind", "event_type")) == "response.completed"
		if v := firstValue(a, "duration_ms", "interaction.duration_ms"); completed && v != nil {
			if duration, ok := measurement(v, false); ok {
				u.Values["duration_ms"] = duration
				u.Values["duration_samples"] = "1"
			}
		}
		if e.Signal == "traces" {
			start, end := nanoKey(r["startTimeUnixNano"]), nanoKey(r["endTimeUnixNano"])
			if start != "" && end > start {
				x, _ := new(big.Int).SetString(end, 10)
				y, _ := new(big.Int).SetString(start, 10)
				u.Values["duration_ms"] = decimalText(new(big.Rat).SetFrac(x.Sub(x, y), big.NewInt(1000000)))
				u.Values["duration_samples"] = "1"
			}
		}
	}
	if len(u.Values) == 0 {
		return nil
	}
	return u
}

func storeUsage(ctx context.Context, tx *sql.Tx, e Event, doc document, p Detail) error {
	if _, err := execRecord(ctx, tx, "DELETE FROM usage_samples WHERE id=?", e.ID); err != nil {
		return err
	}
	if u := usageMeasurement(e, doc, p); u != nil {
		values, err := json.Marshal(u.Values)
		if err != nil {
			return err
		}
		if _, err = execRecord(ctx, tx, "INSERT INTO usage_samples VALUES(?,?,?,?,?,?,?,?,?,?,?)", e.ID, e.Session, e.Origin, u.Basis, u.Key, u.Series, u.Start, u.End, e.Time, e.Sequence, string(values)); err != nil {
			return err
		}
		if err = storeInterval(ctx, tx, e, u); err != nil {
			return err
		}
	}
	result, err := execRecord(ctx, tx, "DELETE FROM event_errors WHERE id=?", e.ID)
	if err != nil {
		return err
	}
	removed, _ := result.RowsAffected()
	if p.Error {
		if _, err = execRecord(ctx, tx, "INSERT INTO event_errors VALUES(?,?,?)", e.ID, e.Session, e.Origin); err != nil {
			return err
		}
	}
	if !p.Error && removed == 0 {
		return nil
	}
	// Late imports may alter error membership of an existing logical event.
	if _, err = execRecord(ctx, tx, `DELETE FROM timeline_errors WHERE id IN (SELECT id FROM logical_events WHERE id=? UNION ALL SELECT id FROM logical_events WHERE otel_id=? AND import_id<>'' AND otel_id<>'')`, e.ID, e.ID); err != nil {
		return err
	}
	_, err = execRecord(ctx, tx, `INSERT OR IGNORE INTO timeline_errors SELECT id,session FROM logical_events WHERE (id=? OR id IN (SELECT id FROM logical_events WHERE otel_id=? AND import_id<>'' AND otel_id<>''))
 AND EXISTS(SELECT 1 FROM event_errors WHERE id IN (import_id,otel_id))`, e.ID, e.ID)
	return err
}

func refreshUsage(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT session,origin,basis,group_key FROM pending_usage")
	if err != nil {
		return err
	}
	type key struct{ session, origin, basis, key string }
	keys := []key{}
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.session, &k.origin, &k.basis, &k.key); err != nil {
			break
		}
		keys = append(keys, k)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return errors.Join(err, rowErr)
	}
	for _, k := range keys {
		if _, err = execRecord(ctx, tx, "DELETE FROM usage_groups WHERE session=? AND origin=? AND basis=? AND group_key=?", k.session, k.origin, k.basis, k.key); err != nil {
			return err
		}
		var values, series, eventID, latestTime string
		err = queryRecord(ctx, tx, "SELECT values_json,series,id,time FROM usage_samples WHERE session=? AND origin=? AND basis=? AND group_key=? ORDER BY time DESC,sequence DESC,id DESC LIMIT 1", k.session, k.origin, k.basis, k.key).Scan(&values, &series, &eventID, &latestTime)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if k.basis == "logs" || k.basis == "traces" {
			reports, e := tx.QueryContext(ctx, "SELECT values_json,time,sequence FROM usage_samples WHERE session=? AND origin=? AND basis=? AND group_key=? ORDER BY time DESC,sequence DESC,id DESC", k.session, k.origin, k.basis, k.key)
			if e != nil {
				return e
			}
			merged := map[string]string{}
			type order struct {
				time     string
				sequence int
			}
			orders := map[string]order{}
			ambiguous := map[string]bool{}
			for reports.Next() {
				var encoded string
				var current order
				if e = reports.Scan(&encoded, &current.time, &current.sequence); e != nil {
					break
				}
				var v map[string]string
				if e = json.Unmarshal([]byte(encoded), &v); e != nil {
					break
				}
				for component, value := range v {
					if ambiguous[component] {
						continue
					}
					if _, exists := merged[component]; !exists {
						merged[component] = value
						orders[component] = current
					} else if component != "excluded" && orders[component] == current && merged[component] != value {
						delete(merged, component)
						ambiguous[component] = true
						merged["excluded"] = "1"
					}
				}
			}
			rowErr := reports.Err()
			reports.Close()
			if e != nil || rowErr != nil {
				return errors.Join(e, rowErr)
			}
			encoded, e := json.Marshal(merged)
			if e != nil {
				return e
			}
			values = string(encoded)
		}
		if k.basis == "metric-delta" && series != "" {
			overlap, e := overlappingUsage(ctx, tx, eventID, k.key)
			if e != nil {
				return e
			}
			if overlap {
				values = `{"excluded":"1"}`
			}
		}
		if (k.basis == "metric-delta" || k.basis == "metric-cumulative") && series != "" {
			var conflict bool
			if err = queryRecord(ctx, tx, "SELECT EXISTS(SELECT 1 FROM usage_samples WHERE session=? AND origin=? AND basis=? AND group_key=? AND time=? AND values_json<>?)", k.session, k.origin, k.basis, k.key, latestTime, values).Scan(&conflict); err != nil {
				return err
			}
			if conflict {
				values = `{"excluded":"1"}`
			}
		}
		if _, err = execRecord(ctx, tx, "INSERT INTO usage_groups VALUES(?,?,?,?,?)", k.session, k.origin, k.basis, k.key, values); err != nil {
			return err
		}
	}
	if _, err = execRecord(ctx, tx, "DELETE FROM work_items WHERE kind='usage'"); err != nil {
		return err
	}
	return refreshUsageSummaries(ctx, tx)
}
