package app

import (
	"context"
	"database/sql"
	"net/http"
)

const childrenPageSize = 20

type childrenPage struct {
	Children []string `json:"children"`
	Next     string   `json:"next"`
}

func readChildren(ctx context.Context, tx *sql.Tx, session, after string) (childrenPage, error) {
	page := childrenPage{Children: []string{}}
	rows, err := tx.QueryContext(ctx, "SELECT session FROM session_catalog WHERE parent=? AND session>? ORDER BY session LIMIT ?", session, after, childrenPageSize+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return page, err
		}
		page.Children = append(page.Children, id)
	}
	if len(page.Children) > childrenPageSize {
		page.Children = page.Children[:childrenPageSize]
		page.Next = page.Children[len(page.Children)-1]
	}
	return page, rows.Err()
}

func (a *App) children(w http.ResponseWriter, r *http.Request) {
	tx, err := a.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		databaseError(w, r, err)
		return
	}
	defer tx.Rollback()
	page, err := readChildren(r.Context(), tx, r.URL.Query().Get("session"), r.URL.Query().Get("after"))
	if err != nil {
		databaseError(w, r, err)
		return
	}
	if err = tx.Commit(); err != nil {
		databaseError(w, r, err)
		return
	}
	write(w, page)
}
