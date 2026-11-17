package app

import (
	"context"
	"database/sql"
	"encoding/json"
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
	case "message_id", "messageId", "gen_ai.message.id", "message.uuid":
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

func storeIdentifiers(ctx context.Context, tx *sql.Tx, event Event, documentID int64, identifiers map[searchIdentifier]bool) error {
	// Keep shared dictionary entries while an updated record's postings are rebuilt.
	// Insert the new set first, then remove only obsolete postings in one operation.
	ids := make([]int64, 0, len(identifiers))
	for id := range identifiers {
		if _, err := execRecord(ctx, tx, "INSERT INTO identifier_values(key,value) VALUES(?,?) ON CONFLICT(value,key) DO NOTHING", id.Key, id.Value); err != nil {
			return err
		}
		var key int64
		if err := queryRecord(ctx, tx, "SELECT id FROM identifier_values WHERE value=? AND key=?", id.Value, id.Key).Scan(&key); err != nil {
			return err
		}
		ids = append(ids, key)
		if _, err := execRecord(ctx, tx, "INSERT INTO identifier_postings(document_id,identifier_id,session) VALUES(?,?,?) ON CONFLICT(document_id,identifier_id) DO UPDATE SET session=excluded.session WHERE session<>excluded.session", documentID, key, event.Session); err != nil {
			return err
		}
	}
	encoded, _ := json.Marshal(ids)
	_, err := execRecord(ctx, tx, "DELETE FROM identifier_postings WHERE document_id=? AND identifier_id NOT IN(SELECT value FROM json_each(?))", documentID, string(encoded))
	return err
}

func identifierFilter(key, value string) (string, []any, error) {
	if key == "" && value == "" {
		return "", nil, nil
	}
	if value == "" || utf8.RuneCountInString(value) > 4096 || utf8.RuneCountInString(key) > 256 || key != "" && !identifierKey(key) {
		return "", nil, fmt.Errorf("Specify an ID value (up to 4096 characters) and an ID field, or leave the field empty to search all IDs")
	}
	query := " AND s.session IN (SELECT p.session FROM identifier_values v JOIN identifier_postings p ON p.identifier_id=v.id WHERE v.value=?"
	args := []any{value}
	if key != "" {
		query += " AND v.key=?"
		args = append(args, key)
	}
	return query + " GROUP BY p.session)", args, nil
}
