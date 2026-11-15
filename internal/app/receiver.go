package app

import (
	"compress/gzip"
	"context"
	"encoding/json"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	traces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"io"
	"mime"
	"net/http"
	"strings"
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
		if e = decodeJSON(b, &obj); e == nil {
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
	if e = a.Save(r.Context(), events); e != nil {
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

// IngestLogs uses the same normalization and storage as the OTLP receiver.
func (a *App) IngestLogs(ctx context.Context, resources []any) error {
	events, err := normalize(map[string]any{"resourceLogs": resources}, "logs")
	if err != nil {
		return err
	}
	return a.Save(ctx, events)
}
