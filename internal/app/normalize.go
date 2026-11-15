package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func obj(v any) map[string]any { x, _ := v.(map[string]any); return x }
func arr(v any) []any          { x, _ := v.([]any); return x }
func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func attrs(v any) map[string]string {
	out := map[string]string{}
	for k, v := range decodedAttributes(v) {
		out[k] = str(v)
	}
	return out
}
func normalize(root map[string]any, signal string) ([]Event, error) {
	out := []Event{}
	normalizedBytes := 0
	resourceKey, scopeKey, recordKey := "resourceLogs", "scopeLogs", "logRecords"
	if signal == "traces" {
		resourceKey, scopeKey, recordKey = "resourceSpans", "scopeSpans", "spans"
	}
	if signal == "metrics" {
		resourceKey, scopeKey, recordKey = "resourceMetrics", "scopeMetrics", "metrics"
	}
	for _, rv := range arr(root[resourceKey]) {
		res := obj(rv)
		for _, sv := range arr(res[scopeKey]) {
			scope := obj(sv)
			for _, iv := range arr(scope[recordKey]) {
				record := obj(iv)
				records := []map[string]any{record}
				if signal == "metrics" {
					records = nil
					for _, kind := range []string{"gauge", "sum", "histogram", "exponentialHistogram", "summary"} {
						for _, point := range arr(obj(record[kind])["dataPoints"]) {
							p := obj(point)
							copy := map[string]any{}
							for k, v := range p {
								copy[k] = v
							}
							copy["name"] = record["name"]
							copy["metricType"] = kind
							copy["aggregationTemporality"] = obj(record[kind])["aggregationTemporality"]
							copy["isMonotonic"] = obj(record[kind])["isMonotonic"]
							copy["unit"] = record["unit"]
							copy["description"] = record["description"]
							copy["metadata"] = record["metadata"]
							records = append(records, copy)
						}
					}
				}
				for _, rec := range records {
					at := attrs(obj(res["resource"])["attributes"])
					for k, v := range attrs(obj(scope["scope"])["attributes"]) {
						at[k] = v
					}
					for k, v := range attrs(rec["attributes"]) {
						at[k] = v
					}
					source := "other"
					hint := strings.ToLower(at["service.name"] + " " + str(obj(scope["scope"])["name"]))
					for _, s := range []string{"codex", "claude", "copilot"} {
						if strings.Contains(hint, s) {
							source = s
						}
					}
					sid := ""
					for _, k := range []string{"session.id", "conversation.id", "gen_ai.conversation.id", "session_id"} {
						if at[k] != "" {
							sid = at[k]
							break
						}
					}
					trace, span := str(rec["traceId"]), str(rec["spanId"])
					parent := ""
					if at["parent.session.id"] != "" {
						parent = source + ":" + at["parent.session.id"]
					}
					if sid != "" {
						sid = source + ":" + sid
						agent := at["agent_id"]
						if agent == "" && at["gen_ai.operation.name"] != "invoke_agent" {
							agent = at["gen_ai.agent.id"]
						}
						if source == "claude" && agent != "" {
							base := sid
							parent = base
							sid += "/agent:" + agent
							if at["parent_agent_id"] != "" {
								parent = base + "/agent:" + at["parent_agent_id"]
							}
						}
					} else if trace != "" {
						sid = "trace:" + source + ":" + trace
					} else {
						sid = "unassigned:" + source
					}
					ns := str(rec["timeUnixNano"])
					if ns == "" {
						ns = str(rec["startTimeUnixNano"])
					}
					if ns == "" {
						ns = str(rec["observedTimeUnixNano"])
					}
					n, _ := strconv.ParseInt(ns, 10, 64)
					tm := time.Unix(0, n).UTC().Format("2006-01-02T15:04:05.000000000Z")
					name := str(rec["name"])
					if name == "" {
						name = str(rec["eventName"])
					}
					if name == "" {
						body := obj(rec["body"])
						if s, ok := body["stringValue"].(string); ok {
							name = s
						} else if len(body) > 0 {
							b, _ := json.Marshal(body)
							name = string(b)
						}
					}
					if name == "" {
						name = signal
					}
					raw, _ := json.Marshal(map[string]any{"resource": res["resource"], "scope": scope["scope"], "record": rec})
					normalizedBytes += len(raw)
					if normalizedBytes > maxBody {
						return nil, fmt.Errorf("normalized batch exceeds 16 MiB")
					}
					ev := Event{Session: sid, Parent: parent, Source: source, Signal: signal, Time: tm, Name: name, Trace: trace, Span: span, ParentSpan: str(rec["parentSpanId"]), Raw: raw}
					if importID := at["agentlog.import.id"]; importID != "" {
						sum := sha256.Sum256([]byte(source + "|" + importID))
						ev.ID = "import:" + hex.EncodeToString(sum[:])
						ev.Sequence, _ = strconv.Atoi(at["agentlog.import.sequence"])
					}
					out = append(out, ev)
					if len(out) > 10000 {
						return nil, fmt.Errorf("maximum 10000 records per request")
					}
				}
			}
		}
	}
	return out, nil
}
