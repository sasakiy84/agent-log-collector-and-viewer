package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

type agentTarget struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Session bool   `json:"session,omitempty"`
}
type SubagentLink struct {
	Session string `json:"session"`
	Agent   string `json:"agent,omitempty"`
	Label   string `json:"label"`
}

func launchTool(name string) bool {
	name = strings.ToLower(name)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	switch name {
	case "spawn_agent", "task", "agent", "runsubagent", "run_subagent":
		return true
	}
	return false
}
func agentID(v any) string {
	s, ok := v.(string)
	if !ok || len(s) > 512 || strings.ContainsAny(s, "\r\n\x00\t ") {
		return ""
	}
	return s
}

var claudeAgentID = regexp.MustCompile(`(?m)(?:^|\n)agentId:\s*([A-Za-z0-9_-]+)(?:\s|$)`)

// Parse identifiers from known launch results, never from prompts or arbitrary
// tool output. Request/result correlation is scoped to the executing session.
func agentTargets(e Event, d Detail) (bool, []agentTarget) {
	at := d.Attributes
	launch := launchTool(str(firstValue(at, "gen_ai.tool.name", "tool_name", "tool_name_safe"))) || at["gen_ai.operation.name"] == "invoke_agent"
	targets := []agentTarget{}
	add := func(id any, label any, session bool) {
		if id := agentID(id); id != "" && len(targets) < 16 {
			targets = append(targets, agentTarget{id, clip(str(label), 160), session})
		}
	}
	add(at["child.session.id"], at["gen_ai.agent.name"], true)
	add(at["agentlog.subagent.id"], at["gen_ai.agent.name"], false)
	if at["gen_ai.operation.name"] == "invoke_agent" && (e.Source != "copilot" || d.Name == "gen_ai.agent.started" || d.Name == "gen_ai.agent.completed") {
		add(at["gen_ai.agent.id"], at["gen_ai.agent.name"], false)
	}
	if e.Source == "copilot" && at["gen_ai.operation.name"] == "invoke_agent" && e.Signal == "traces" && e.Trace != "" && e.Span != "" && e.ParentSpan != "" && !present(at["server.address"]) {
		add("trace:"+e.Trace+":"+e.Span, at["gen_ai.agent.name"], false)
	}
	if d.Kind != "tool" {
		return launch, targets
	}
	if !launch && len(targets) == 0 && present(firstValue(at, "gen_ai.tool.name", "tool_name", "tool_name_safe")) {
		return false, nil
	}
	output := firstValue(at, "gen_ai.tool.call.result", "tool_result", "output", "result", "tool.output")
	if s, ok := output.(string); ok && len(s) > 1<<20 {
		return launch, targets
	}
	// A result can arrive before its named request. It is accepted as a link
	// only after attachSubagents finds a matching known launch in the index.
	var visit func(any, int)
	visit = func(value any, depth int) {
		if depth > 3 || len(targets) >= 16 {
			return
		}
		if text, ok := value.(string); ok {
			if len(text) > 1<<20 {
				return
			}
			if e.Source == "claude" {
				if m := claudeAgentID.FindStringSubmatch(text); len(m) > 1 {
					add(m[1], nil, false)
				}
			}
			value = decoded(text)
		}
		switch out := value.(type) {
		case map[string]any:
			add(firstValue(out, "agent_id", "agentId"), firstValue(out, "nickname", "agentName"), false)
			add(firstValue(out, "session_id", "sessionId", "thread_id", "threadId"), out["nickname"], true)
			for _, key := range []string{"content", "result", "output", "text"} {
				if v := out[key]; v != nil {
					visit(v, depth+1)
				}
			}
		case []any:
			for _, v := range out[:min(8, len(out))] {
				visit(v, depth+1)
			}
		}
	}
	visit(output, 0)
	return launch, targets
}
func storeAgentData(ctx context.Context, tx *sql.Tx, e Event, d Detail) error {
	owner, valid := agentIdentity(e.Source, d.Attributes)
	if !valid && d.Attributes["gen_ai.operation.name"] != "invoke_agent" {
		return nil
	}
	if owner != "" && agentID(owner) == "" {
		return nil
	}
	owner = agentID(owner)
	if owner != "" && e.Source == "copilot" {
		if _, err := execRecord(ctx, tx, "INSERT INTO agent_activity VALUES(?,?,?) ON CONFLICT(event_id) DO UPDATE SET session=excluded.session,agent=excluded.agent", e.ID, e.Session, owner); err != nil {
			return err
		}
	}
	if d.Kind != "tool" && d.Kind != "agent" {
		return nil
	}
	launch, targets := agentTargets(e, d)
	if !launch && len(targets) == 0 {
		return nil
	}
	call := agentID(firstValue(d.Attributes, "gen_ai.tool.call.id", "tool_call_id", "call_id", "tool_use_id"))
	if call == "" && e.Trace != "" && e.Span != "" {
		call = e.Trace + "/" + e.Span
	}
	encoded, err := json.Marshal(targets)
	if err != nil {
		return err
	}
	_, err = execRecord(ctx, tx, "INSERT INTO agent_calls VALUES(?,?,?,?,?,?) ON CONFLICT(event_id) DO UPDATE SET session=excluded.session,owner=excluded.owner,call_id=excluded.call_id,launch=excluded.launch,targets=excluded.targets", e.ID, e.Session, owner, call, launch, string(encoded))
	return err
}
func subagentLink(parent, source string, t agentTarget) SubagentLink {
	label := t.Label
	if label == "" {
		label = t.ID
	}
	if t.Session {
		id := t.ID
		if !strings.HasPrefix(id, source+":") {
			id = source + ":" + id
		}
		return SubagentLink{Session: id, Label: label}
	}
	if source == "claude" {
		return SubagentLink{Session: strings.Split(parent, "/agent:")[0] + "/agent:" + t.ID, Label: label}
	}
	if source == "copilot" {
		return SubagentLink{Session: parent, Agent: t.ID, Label: label}
	}
	id := t.ID
	if !strings.HasPrefix(id, source+":") {
		id = source + ":" + id
	}
	return SubagentLink{Session: id, Label: label}
}
func attachSubagents(ctx context.Context, tx *sql.Tx, events []TimelineEvent) error {
	ids := []string{}
	positions := map[string]int{}
	for i, e := range events {
		if e.Detail.Kind != "tool" && e.Detail.Kind != "agent" {
			continue
		}
		for _, record := range e.Records {
			ids = append(ids, record.ID)
			positions[record.ID] = i
		}
	}
	if len(ids) == 0 {
		return nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT event_id,session,owner,call_id,launch,targets FROM agent_calls WHERE event_id IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return err
	}
	type call struct {
		id, session, owner, key, targets string
		launch                           bool
	}
	calls := []call{}
	for rows.Next() {
		var c call
		if err = rows.Scan(&c.id, &c.session, &c.owner, &c.key, &c.launch, &c.targets); err != nil {
			break
		}
		calls = append(calls, c)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return errors.Join(err, rowErr)
	}
	related := map[[3]string][]agentTarget{}
	seen := make([]map[[2]string]bool, len(events))
	for _, c := range calls {
		targets := []agentTarget{}
		if c.launch {
			if err = json.Unmarshal([]byte(c.targets), &targets); err != nil {
				return err
			}
		}
		if c.key != "" {
			key := [3]string{c.session, c.owner, c.key}
			refs, ok := related[key]
			if !ok {
				rows, err = tx.QueryContext(ctx, `SELECT targets FROM agent_calls WHERE session=? AND owner=? AND call_id=?
 AND EXISTS(SELECT 1 FROM agent_calls l WHERE l.session=? AND l.owner=? AND l.call_id=? AND l.launch=1) LIMIT 32`, c.session, c.owner, c.key, c.session, c.owner, c.key)
				if err != nil {
					return err
				}
				for rows.Next() {
					var value string
					if err = rows.Scan(&value); err != nil {
						break
					}
					var part []agentTarget
					if err = json.Unmarshal([]byte(value), &part); err != nil {
						break
					}
					refs = append(refs, part...)
				}
				rowErr = rows.Err()
				rows.Close()
				if err != nil || rowErr != nil {
					return errors.Join(err, rowErr)
				}
				related[key] = refs
			}
			targets = append(targets, refs...)
		}
		i := positions[c.id]
		e := &events[i]
		if seen[i] == nil {
			seen[i] = map[[2]string]bool{}
		}
		for _, target := range targets {
			if len(e.Subagents) == 16 {
				break
			}
			link := subagentLink(e.Session, e.Source, target)
			key := [2]string{link.Session, link.Agent}
			if !seen[i][key] && link.Session != "" && (link.Session != e.Session || link.Agent != "") {
				e.Subagents = append(e.Subagents, link)
				seen[i][key] = true
			}
		}
	}
	return nil
}

// Copilot keeps runtime agents inside one session. Read their indexed logical
// cards without loading original JSON or changing the collector's session IDs.
const agentTimelineIDs = `SELECT DISTINCT COALESCE((SELECT id FROM logical_events t WHERE t.otel_id=a.event_id AND t.import_id<>'' AND t.otel_id<>''),a.event_id) FROM agent_activity a WHERE a.session=? AND a.agent=?`

func (a *App) subagent(w http.ResponseWriter, r *http.Request) {
	session, agent := r.URL.Query().Get("session"), r.URL.Query().Get("agent")
	if agentID(agent) == "" {
		http.Error(w, "Invalid agent", 400)
		return
	}
	if position := r.URL.Query().Get("position"); position != "" && position != "latest" {
		http.Error(w, "Invalid position", 400)
		return
	}
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	var locked bool
	if err = tx.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM archive_locks WHERE session=?)", session).Scan(&locked); err != nil {
		databaseError(w, r, err)
		return
	}
	if locked {
		http.Error(w, "Session is being archived", 409)
		return
	}
	var source string
	err = tx.QueryRowContext(r.Context(), "SELECT source FROM sessions WHERE session=?", session).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	if source != "copilot" {
		http.Error(w, "Invalid agent session", 400)
		return
	}
	idsQuery, idsArgs := agentTimelineIDs, []any{session, agent}
	if strings.HasPrefix(agent, "trace:") {
		parts := strings.Split(agent, ":")
		if len(parts) != 3 || len(parts[1]) != 32 || len(parts[2]) != 16 {
			http.Error(w, "Invalid trace agent", 400)
			return
		}
		idsQuery = traceAgentTimelineIDs
		idsArgs = []any{session, parts[1], parts[2]}
	}
	page := SessionPage{SessionSummary: SessionSummary{ID: session, Source: "copilot"}, Limit: 40, Children: []string{}, Events: []TimelineEvent{}}
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM ("+idsQuery+")", idsArgs...).Scan(&page.Count); err != nil {
		databaseError(w, r, err)
		return
	}
	page.Offset = pageOffset(offset(r), page.Count, page.Limit)
	if r.URL.Query().Get("position") == "latest" {
		page.Offset = max(0, page.Count-1) / page.Limit * page.Limit
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT p.id,`+qualifiedCardColumns("c")+` FROM
 (SELECT id,time,sequence,import_id,otel_id FROM logical_events WHERE id IN (`+idsQuery+`) ORDER BY time,sequence,id LIMIT ? OFFSET ?) p
 JOIN record_facts c ON c.id=p.import_id OR c.id=p.otel_id ORDER BY p.time,p.sequence,p.id,c.origin,c.id`, append(append([]any{}, idsArgs...), page.Limit, page.Offset)...)
	if err == nil {
		page.Events, err = decodeTimeline(rows)
	}
	if err == nil {
		err = attachSubagents(r.Context(), tx, page.Events)
		for i := range page.Events {
			links := page.Events[i].Subagents
			page.Events[i].Subagents = nil
			for _, link := range links {
				if link.Session != session || link.Agent != agent {
					page.Events[i].Subagents = append(page.Events[i].Subagents, link)
				}
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, page)
}

// gen_ai.agent.id names an agent definition, not an invocation instance. Span
// ancestry identifies each invocation exactly, including repeated same-name
// agents, and never crosses a session or trace. This path runs only on preview.
const traceAgentTimelineIDs = `WITH RECURSIVE trace_events AS MATERIALIZED (
 SELECT e.id,e.span,e.parent_span,c.kind FROM records e JOIN record_facts c ON c.id=e.id
 WHERE e.source='copilot' AND e.session=? AND e.trace=? AND e.span<>''
), branch(span) AS (
 SELECT span FROM trace_events WHERE span=? AND parent_span<>'' AND kind='agent'
 UNION SELECT e.span FROM trace_events e JOIN branch b ON e.parent_span=b.span
) SELECT DISTINCT COALESCE((SELECT id FROM logical_events t WHERE t.otel_id=e.id AND t.import_id<>'' AND t.otel_id<>''),e.id)
 FROM trace_events e JOIN branch b ON e.span=b.span`
