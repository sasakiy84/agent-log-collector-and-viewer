package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"
)

func identifierKey(key string) bool {
	if key == "id" || key == "ids" || key == "uuid" || key == "guid" || key == "uid" {
		return true
	}
	for _, suffix := range []string{".id", "_id", "-id", ".ids", "_ids", "-ids", "Id", "ID", "Ids", "IDs", ".uuid", "_uuid", ".guid", "_guid", ".uid", "_uid", "Uuid", "UUID", "Guid", "GUID"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func identifierAlias(key string) string {
	switch key {
	case "session_id", "sessionId", "conversation.id", "conversation_id", "conversationId", "gen_ai.conversation.id":
		return "session.id"
	case "id", "event_id", "eventId":
		return "event.id"
	case "traceId", "trace_id":
		return "trace.id"
	case "spanId", "span_id":
		return "span.id"
	case "parentSpanId", "parent_span_id":
		return "parent.span.id"
	case "parent_session_id", "parentSessionId":
		return "parent.session.id"
	case "call_id", "callId", "tool_call_id", "toolCallId", "gen_ai.tool.call.id":
		return "call.id"
	case "message_id", "messageId", "gen_ai.message.id":
		return "message.id"
	case "agent_id", "agentId", "gen_ai.agent.id":
		return "agent.id"
	}
	return key
}

// Identifiers retain their original bytes, punctuation and case. No JSON
// decoding, Unicode normalization or morphology is applied to their strings.
func identifierStrings(value any, emit func(string)) {
	switch v := value.(type) {
	case string:
		if v != "" {
			emit(v)
		}
	case []any:
		for _, item := range v {
			identifierStrings(item, emit)
		}
	case map[string]any:
		if s, ok := v["stringValue"].(string); ok {
			identifierStrings(s, emit)
		} else if a, ok := v["arrayValue"]; ok {
			identifierStrings(obj(a)["values"], emit)
		}
	}
}

type searchIdentifier struct{ Key, Value string }

func storeIdentifiers(ctx context.Context, tx *sql.Tx, event Event, identifiers map[searchIdentifier]bool) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM search_identifiers WHERE event_id=?", event.ID); err != nil {
		return err
	}
	for id := range identifiers {
		if _, err := tx.ExecContext(ctx, "INSERT INTO search_identifiers(event_id,session,key,value) VALUES(?,?,?,?)", event.ID, event.Session, id.Key, id.Value); err != nil {
			return err
		}
	}
	return nil
}

func identifierFilter(key, value string) (string, []any, error) {
	if key == "" && value == "" {
		return "", nil, nil
	}
	if value == "" || utf8.RuneCountInString(value) > 4096 || utf8.RuneCountInString(key) > 256 || key != "" && !identifierKey(key) {
		return "", nil, fmt.Errorf("Specify an ID value (up to 4096 characters) and an ID field, or leave the field empty to search all IDs")
	}
	query := " AND s.session IN (SELECT session FROM search_identifiers WHERE value=?"
	args := []any{value}
	if key != "" {
		query += " AND key=?"
		args = append(args, key)
	}
	return query + " GROUP BY session)", args, nil
}
