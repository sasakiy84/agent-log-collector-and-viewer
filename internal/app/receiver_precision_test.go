package app

import (
	"context"
	"strings"
	"testing"
)

func TestJSONReceiverPreservesNumericInt64(t *testing.T) {
	a := setup(t)
	// OTLP JSON permits either numbers or strings for int64. These values exceed
	// float64's exact-integer range; rounding changes timestamps and accounting.
	for _, tc := range []struct{ signal, resource, scope, record, payload string }{
		{"logs", "resourceLogs", "scopeLogs", "logRecords", `"timeUnixNano":1790000000000000001,"attributes":[{"key":"input_tokens","value":{"intValue":9007199254740993}}]`},
		{"traces", "resourceSpans", "scopeSpans", "spans", `"name":"request","startTimeUnixNano":1790000000000000001,"endTimeUnixNano":1790000000000000003,"attributes":[{"key":"input_tokens","value":{"intValue":9007199254740993}}]`},
		{"metrics", "resourceMetrics", "scopeMetrics", "metrics", `"name":"input_tokens","sum":{"dataPoints":[{"timeUnixNano":1790000000000000001,"asInt":9007199254740993}]}`},
	} {
		t.Run(tc.signal, func(t *testing.T) {
			body := `{"` + tc.resource + `":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex"}}]},"` + tc.scope + `":[{"` + tc.record + `":[{` + tc.payload + `}]}]}]}`
			w := request(a, "/v1/"+tc.signal, body, token)
			if w.Code != 200 {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
			var raw []byte
			if err := a.DB.QueryRowContext(context.Background(), "SELECT raw FROM records WHERE signal=?", tc.signal).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"1790000000000000001", "9007199254740993"} {
				if !strings.Contains(string(raw), value) {
					t.Fatalf("numeric int64 changed (%s): %s", value, raw)
				}
			}
		})
	}
	// Decoder consolidation must not accept a valid prefix of an invalid request.
	if w := request(a, "/v1/logs", `{} {}`, token); w.Code != 400 {
		t.Fatal("accepted trailing JSON", w.Code)
	}
}
