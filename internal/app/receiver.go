package app

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	traces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxBody = 16 << 20

func (a *App) receive(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	defer r.Body.Close()
	var reader io.Reader = r.Body
	switch r.Header.Get("Content-Encoding") {
	case "":
	case "gzip":
		g, e := gzip.NewReader(reader)
		if e != nil {
			http.Error(w, "Invalid gzip", 400)
			return
		}
		defer g.Close()
		reader = g
	default:
		http.Error(w, "Unsupported content encoding", 415)
		return
	}
	b, e := io.ReadAll(io.LimitReader(reader, maxBody+1))
	if e != nil || len(b) > maxBody {
		http.Error(w, "Invalid or oversized request", 413)
		return
	}
	signal := strings.TrimPrefix(r.URL.Path, "/v1/")
	var msg, reply proto.Message
	switch signal {
	case "logs":
		msg = &logs.ExportLogsServiceRequest{}
		reply = &logs.ExportLogsServiceResponse{}
	case "traces":
		msg = &traces.ExportTraceServiceRequest{}
		reply = &traces.ExportTraceServiceResponse{}
	case "metrics":
		msg = &metrics.ExportMetricsServiceRequest{}
		reply = &metrics.ExportMetricsServiceResponse{}
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "application/json":
		var obj any
		if e = json.Unmarshal(b, &obj); e == nil {
			convertIDs(obj, true)
			b, e = json.Marshal(obj)
		}
		if e == nil {
			e = (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(b, msg)
		}
	case "application/x-protobuf":
		e = proto.Unmarshal(b, msg)
	default:
		http.Error(w, "Use application/json or application/x-protobuf", 415)
		return
	}
	if e != nil {
		http.Error(w, "Invalid OTLP payload", 400)
		return
	}
	b, e = protojson.Marshal(msg)
	if e != nil {
		http.Error(w, "Encoding error", 500)
		return
	}
	var root map[string]any
	json.Unmarshal(b, &root)
	convertIDs(root, false)
	if e = applyProjectQuery(root, signal, r.URL.Query()); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	events, e := normalize(root, signal)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	if len(events) > 10000 {
		http.Error(w, "Maximum 10000 records per request", 413)
		return
	}
	if e = a.Save(events); e != nil {
		http.Error(w, "Storage unavailable", 503)
		return
	}
	if ct == "application/json" {
		w.Header().Set("Content-Type", ct)
		b, _ = protojson.Marshal(reply)
	} else {
		w.Header().Set("Content-Type", ct)
		b, _ = proto.Marshal(reply)
	}
	w.Write(b)
}
func convertIDs(v any, toBase64 bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == "traceId" || k == "spanId" || k == "parentSpanId" {
				s, ok := v.(string)
				if !ok {
					continue
				}
				if toBase64 {
					want := 16
					if k == "traceId" {
						want = 32
					}
					if len(s) == want {
						if b, e := hex.DecodeString(s); e == nil {
							x[k] = base64.StdEncoding.EncodeToString(b)
						}
					}
				} else {
					if b, e := base64.StdEncoding.DecodeString(s); e == nil {
						x[k] = hex.EncodeToString(b)
					}
				}
			} else {
				convertIDs(v, toBase64)
			}
		}
	case []any:
		for _, v := range x {
			convertIDs(v, toBase64)
		}
	}
}
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
	for _, v := range arr(v) {
		a := obj(v)
		for _, v := range obj(a["value"]) {
			out[str(a["key"])] = str(v)
		}
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
						if source == "claude" && at["agent_id"] != "" {
							base := sid
							parent = base
							sid += "/agent:" + at["agent_id"]
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

// IngestLogs uses the same normalization and storage as the OTLP receiver.
func (a *App) IngestLogs(resources []any) error {
	events, err := normalize(map[string]any{"resourceLogs": resources}, "logs")
	if err != nil {
		return err
	}
	return a.Save(events)
}
