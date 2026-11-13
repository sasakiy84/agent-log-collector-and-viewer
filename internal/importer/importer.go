// Package importer converts saved CLI transcripts into OTLP log records.
package importer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Options struct {
	Dirs, Files, Tags                            []string
	All, DryRun                                  bool
	Source, Endpoint, Token, Project, WorkingDir string
	Client                                       *http.Client
}
type Stats struct{ Files, Records, Skipped, Failed int }
type Sender func(map[string]any) error

func m(v any) map[string]any { x, _ := v.(map[string]any); return x }
func a(v any) []any          { x, _ := v.([]any); return x }
func s(v any) string {
	if v == nil {
		return ""
	}
	if x, ok := v.(string); ok {
		return x
	}
	b, _ := json.Marshal(v)
	return string(b)
}
func first(v ...any) string {
	for _, x := range v {
		if s(x) != "" {
			return s(x)
		}
	}
	return ""
}
func attrs(v map[string]any) []any {
	keys := []string{}
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []any{}
	for _, k := range keys {
		if v[k] == nil || s(v[k]) == "" {
			continue
		}
		out = append(out, map[string]any{"key": k, "value": value(v[k])})
	}
	return out
}
func value(v any) map[string]any {
	switch x := v.(type) {
	case bool:
		return map[string]any{"boolValue": x}
	case json.Number:
		if _, e := x.Int64(); e == nil {
			return map[string]any{"intValue": x.String()}
		}
		if n, e := x.Float64(); e == nil {
			return map[string]any{"doubleValue": n}
		}
		return map[string]any{"stringValue": x.String()}
	case map[string]any:
		return map[string]any{"kvlistValue": map[string]any{"values": attrs(x)}}
	case []any:
		vs := []any{}
		for _, v := range x {
			vs = append(vs, value(v))
		}
		return map[string]any{"arrayValue": map[string]any{"values": vs}}
	case int:
		return map[string]any{"intValue": fmt.Sprint(x)}
	case []string:
		vs := []any{}
		for _, v := range x {
			vs = append(vs, value(v))
		}
		return map[string]any{"arrayValue": map[string]any{"values": vs}}
	default:
		return map[string]any{"stringValue": s(v)}
	}
}
func endpoint(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return "", errors.New("endpoint must be an HTTP(S) URL without userinfo or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/v1/logs") {
		u.Path += "/v1/logs"
	}
	return u.String(), nil
}
func roots() []string {
	home, _ := os.UserHomeDir()
	pick := func(key, def string) string {
		if x := os.Getenv(key); x != "" {
			return x
		}
		return filepath.Join(home, def)
	}
	return []string{filepath.Join(pick("CODEX_HOME", ".codex"), "sessions"), filepath.Join(pick("CODEX_HOME", ".codex"), "archived_sessions"), filepath.Join(pick("CLAUDE_CONFIG_DIR", ".claude"), "projects"), filepath.Join(pick("COPILOT_HOME", ".copilot"), "session-state")}
}
func Run(ctx context.Context, o Options, progress io.Writer) (Stats, error) {
	var stats Stats
	if o.Source == "" {
		o.Source = "auto"
	}
	if o.Source != "auto" && o.Source != "codex" && o.Source != "claude" && o.Source != "copilot" {
		return stats, errors.New("source must be auto, codex, claude or copilot")
	}
	if len(o.Dirs)+len(o.Files) == 0 && !o.All {
		return stats, errors.New("specify --dir, --file or --all")
	}
	dest, e := endpoint(o.Endpoint)
	if e != nil && !o.DryRun {
		return stats, e
	}
	if !o.DryRun && len(o.Token) < 16 {
		return stats, errors.New("AGENTLOG_TOKEN must contain at least 16 characters")
	}
	paths := map[string]bool{}
	for _, f := range o.Files {
		abs, e := filepath.Abs(f)
		if e != nil {
			return stats, e
		}
		paths[abs] = true
	}
	dirs := append([]string{}, o.Dirs...)
	if o.All {
		for _, d := range roots() {
			if info, e := os.Stat(d); e == nil && info.IsDir() {
				dirs = append(dirs, d)
			}
		}
	}
	for _, dir := range dirs {
		e := filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
				abs, e := filepath.Abs(p)
				if e != nil {
					return e
				}
				paths[abs] = true
			}
			return nil
		})
		if e != nil {
			return stats, fmt.Errorf("scan %s: %w", dir, e)
		}
	}
	files := []string{}
	for f := range paths {
		files = append(files, f)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return stats, errors.New("no JSONL session files found")
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	send := func(records []any) error {
		if len(records) == 0 || o.DryRun {
			return nil
		}
		body, _ := json.Marshal(map[string]any{"resourceLogs": records})
		req, e := http.NewRequestWithContext(ctx, "POST", dest, bytes.NewReader(body))
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer "+o.Token)
		req.Header.Set("Content-Type", "application/json")
		res, e := client.Do(req)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return fmt.Errorf("collector returned HTTP %d", res.StatusCode)
		}
		var reply struct {
			Partial map[string]any `json:"partialSuccess"`
		}
		b, e := io.ReadAll(io.LimitReader(res.Body, 65537))
		if e != nil {
			return e
		}
		if len(b) > 65536 || json.Unmarshal(b, &reply) != nil {
			return errors.New("invalid OTLP response")
		}
		if len(reply.Partial) > 0 {
			return errors.New("collector reported partial success")
		}
		return nil
	}
	var failures []error
	for _, file := range files {
		if e := ctx.Err(); e != nil {
			return stats, e
		}
		// Rotated Copilot segments are linked from events.jsonl, not independent sessions.
		base := filepath.Base(file)
		if strings.HasPrefix(base, "events.") && base != "events.jsonl" {
			continue
		}
		stats.Files++
		batch := []any{}
		size := 0
		flush := func() error {
			if e := send(batch); e != nil {
				return e
			}
			stats.Records += len(batch)
			batch = nil
			size = 0
			return nil
		}
		emit := func(resource map[string]any, record map[string]any) error {
			entry := map[string]any{"resource": map[string]any{"attributes": attrs(resource)}, "scopeLogs": []any{map[string]any{"scope": map[string]any{"name": "agentlog.import"}, "logRecords": []any{record}}}}
			b, _ := json.Marshal(entry)
			if len(b) > 8<<20 {
				return errors.New("converted record exceeds 8 MiB")
			}
			if len(batch) >= 200 || size+len(b) > 8<<20 {
				if e := flush(); e != nil {
					return e
				}
			}
			batch = append(batch, entry)
			size += len(b)
			return nil
		}
		state := newState(o, file, emit)
		e := state.parse(ctx, file)
		stats.Skipped += state.skipped
		if e == nil {
			e = flush()
		}
		if e != nil {
			stats.Failed++
			failures = append(failures, fmt.Errorf("%s: %w", file, e))
			fmt.Fprintf(progress, "Failed: %s: %v\n", file, e)
		} else {
			fmt.Fprintf(progress, "Read: %s\n", file)
		}
	}
	return stats, errors.Join(failures...)
}

type state struct {
	options                                                 Options
	source, session, parent, agent, cwd, model, stamp, file string
	order, skipped                                          int
	emit                                                    func(map[string]any, map[string]any) error
	tools                                                   map[string]string
	inferredTime                                            bool
	canonicalUser, canonicalAssistant                       bool
	visiting                                                map[string]bool
}

func newState(o Options, file string, emit func(map[string]any, map[string]any) error) *state {
	return &state{options: o, source: o.Source, file: file, emit: emit, tools: map[string]string{}, visiting: map[string]bool{}}
}
func (p *state) output(event string, fields map[string]any, id string) error {
	if filter := p.options.WorkingDir; filter != "" {
		clean := func(v string) string { return strings.TrimSuffix(path.Clean(strings.ReplaceAll(v, "\\", "/")), "/") }
		want, got := clean(filter), clean(p.cwd)
		if got != want && !strings.HasPrefix(got, want+"/") {
			p.skipped++
			return nil
		}
	}
	if p.session == "" {
		p.skipped++
		return nil
	}
	if p.source == "auto" {
		return nil
	}
	p.order++
	if id == "" {
		b, _ := json.Marshal(fields)
		h := sha256.Sum256(append([]byte(event+"|"+p.stamp), b...))
		id = hex.EncodeToString(h[:])
	}
	key := p.source + "|" + p.session + "|" + p.agent + "|" + event + "|" + id
	fields["event.name"] = event
	fields["session.id"] = p.session
	fields["agentlog.import.id"] = key
	fields["agentlog.import.sequence"] = p.order
	fields["agentlog.origin"] = "import"
	if p.agent != "" {
		fields["agent_id"] = p.agent
	}
	if p.parent != "" {
		fields["parent.session.id"] = p.parent
	}
	if p.model != "" {
		fields["gen_ai.request.model"] = p.model
	}
	ns := "0"
	if t, e := time.Parse(time.RFC3339Nano, p.stamp); e == nil {
		ns = fmt.Sprint(t.UnixNano())
		if p.inferredTime {
			fields["agentlog.import.timestamp_inferred"] = true
		}
	} else {
		fields["agentlog.import.timestamp_missing"] = true
	}
	resource := map[string]any{"service.name": p.source, "agentlog.origin": "import", "cwd": p.cwd}
	project := p.options.Project
	if project == "" && p.cwd != "" {
		project = path.Base(strings.ReplaceAll(p.cwd, "\\", "/"))
	}
	if project != "" {
		resource["service.namespace"] = project
	}
	if len(p.options.Tags) > 0 {
		tags := append([]string{}, p.options.Tags...)
		sort.Strings(tags)
		resource["project.tags"] = tags
	}
	return p.emit(resource, map[string]any{"timeUnixNano": ns, "eventName": event, "attributes": attrs(fields)})
}
func (p *state) message(role, content, id string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	event, key := "gen_ai.user.message", "gen_ai.input.messages"
	if role == "assistant" {
		event, key = "gen_ai.assistant.message", "gen_ai.output.messages"
	}
	return p.output(event, map[string]any{key: []any{map[string]any{"role": role, "parts": []any{map[string]any{"type": "text", "content": content}}}}}, id)
}
func (p *state) tool(id, name string, input, output any, failed bool, result bool) error {
	if name != "" {
		p.tools[id] = name
	} else {
		name = p.tools[id]
	}
	fields := map[string]any{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": name, "gen_ai.tool.call.id": id}
	if id == "" {
		id = fmt.Sprintf("record-%d", p.order+1)
	}
	phase := "start"
	if result {
		phase = "result"
		fields["gen_ai.tool.call.result"] = output
		if p.source != "codex" || failed {
			fields["success"] = !failed
		}
	} else {
		fields["gen_ai.tool.call.arguments"] = input
	}
	if failed {
		fields["error"] = s(output)
	}
	return p.output("gen_ai.tool."+phase, fields, id+"|"+phase)
}
func content(v any) string {
	if x, ok := v.(string); ok {
		return x
	}
	parts := []string{}
	for _, b := range a(v) {
		x := m(b)
		if text := first(x["text"], x["content"]); text != "" && (x["type"] == "text" || x["type"] == "input_text" || x["type"] == "output_text" || x["type"] == nil) {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
func (p *state) parse(ctx context.Context, file string) error {
	if p.visiting[file] || len(p.visiting) > 128 {
		return errors.New("cyclic or excessively deep journal chain")
	}
	p.visiting[file] = true
	defer delete(p.visiting, file)
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	limit := info.Size()
	scan := bufio.NewReaderSize(io.NewSectionReader(f, 0, limit), 64<<10)
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		b, e := readLine(scan)
		if e != nil && e != io.EOF {
			return e
		}
		var row map[string]any
		if decodeObject(b, &row) == nil && s(row["type"]) == "response_item" && s(m(row["payload"])["type"]) == "message" {
			role := s(m(row["payload"])["role"])
			if role == "user" {
				p.canonicalUser = true
			}
			if role == "assistant" {
				p.canonicalAssistant = true
			}
		}
		if e == io.EOF {
			break
		}
	}
	reader := bufio.NewReaderSize(io.NewSectionReader(f, 0, limit), 64<<10)
	line := 0
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		b, readErr := readLine(reader)
		if len(b) == 0 && readErr == io.EOF {
			break
		}
		line++
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		if len(bytes.TrimSpace(b)) == 0 {
			if readErr == io.EOF {
				break
			}
			continue
		}
		var v map[string]any
		if e = decodeObject(b, &v); e != nil {
			if readErr == io.EOF {
				p.skipped++
				break
			}
			return fmt.Errorf("line %d: invalid JSON", line)
		}
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		t := s(v["type"])
		if p.source == "auto" {
			switch {
			case t == "session_meta" || t == "response_item" || t == "turn_context" || t == "event_msg":
				p.source = "codex"
			case (t == "user" || t == "assistant") && v["message"] != nil:
				p.source = "claude"
			case strings.HasPrefix(t, "session.") || strings.HasPrefix(t, "tool.execution_") || strings.HasPrefix(t, "subagent.") || t == "assistant.usage" || t == "user.message" || t == "assistant.message":
				p.source = "copilot"
			default:
				p.skipped++
				continue
			}
		}
		if st := s(v["timestamp"]); st != "" {
			p.stamp = st
			p.inferredTime = false
		} else {
			p.inferredTime = true
		}
		if t == "session.journal_link" && p.source == "copilot" {
			if e = p.journal(ctx, file, m(v["data"])); e != nil {
				return e
			}
			continue
		}
		switch p.source {
		case "codex":
			e = p.codex(v, line)
		case "claude":
			e = p.claude(v, line)
		case "copilot":
			e = p.copilot(v, line)
		}
		if e != nil {
			return fmt.Errorf("line %d: %w", line, e)
		}
		if readErr == io.EOF {
			break
		}
	}
	if p.source == "auto" {
		return errors.New("unrecognized session format")
	}
	return nil
}
func readLine(r *bufio.Reader) ([]byte, error) {
	out := []byte{}
	for {
		part, e := r.ReadSlice('\n')
		out = append(out, part...)
		if len(out) > 16<<20 {
			return nil, errors.New("JSONL line exceeds 16 MiB")
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		return out, e
	}
}
