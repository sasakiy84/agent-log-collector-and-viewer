package app

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
)

func (a *App) export(w http.ResponseWriter, r *http.Request) {
	origin, ok := eventOrigin(r)
	if !ok {
		http.Error(w, "Invalid event origin", http.StatusBadRequest)
		return
	}
	query := "SELECT id FROM events WHERE session=?"
	args := []any{r.URL.Query().Get("session")}
	if origin != "" {
		query += " AND origin=?"
		args = append(args, origin)
	}
	// Sort only narrow identities. LIMIT prevents flattening this ordered
	// coroutine, and CROSS JOIN keeps it as the outer loop while originals are
	// fetched by primary key. Raw payloads never enter SQLite's sort buffer.
	query = "SELECT " + qualifiedColumns("e") + " FROM (" + query +
		" ORDER BY time,sequence,id LIMIT -1) ordered CROSS JOIN events e ON e.id=ordered.id"
	rows, err := a.readers.QueryContext(r.Context(), query, args...)
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer rows.Close()
	prepareDownload(w, "application/json", "session.json")
	if err = writeSessionJSON(w, rows); err != nil {
		abortDownload(err)
	}
}

// Preserve the single-session JSON array format, holding only one original
// event at a time instead of materializing a potentially multi-GB session.
func writeSessionJSON(w io.Writer, rows *sql.Rows) error {
	if _, err := io.WriteString(w, "["); err != nil {
		return err
	}
	encoder := json.NewEncoder(w)
	first := true
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return err
		}
		if !first {
			if _, err = io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if err = encoder.Encode(event); err != nil {
			return err
		}
		first = false
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err := io.WriteString(w, "]\n")
	return err
}
