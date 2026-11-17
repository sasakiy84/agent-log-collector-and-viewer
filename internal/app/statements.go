package app

import (
	"context"
	"database/sql"
	"strings"
)

type statementContextKey struct{}

// Only ingestion's fixed per-record SQL uses this cache. Its lifetime and size
// are bounded by one transaction, never by user query strings or connection age.
type recordStatements struct {
	tx         *sql.Tx
	statements map[string]*sql.Stmt
}

func (s *recordStatements) close() {
	for _, stmt := range s.statements {
		stmt.Close()
	}
}
func (s *recordStatements) prepare(ctx context.Context, query string) (*sql.Stmt, error) {
	query = strings.TrimSpace(query)
	if stmt := s.statements[query]; stmt != nil {
		return stmt, nil
	}
	stmt, err := s.tx.PrepareContext(ctx, query)
	if err == nil {
		s.statements[query] = stmt
	}
	return stmt, err
}
func recordStatement(ctx context.Context, tx *sql.Tx, query string) (*sql.Stmt, error) {
	if cache, ok := ctx.Value(statementContextKey{}).(*recordStatements); ok && cache.tx == tx {
		return cache.prepare(ctx, query)
	}
	return nil, nil
}
func execRecord(ctx context.Context, tx *sql.Tx, query string, args ...any) (sql.Result, error) {
	stmt, err := recordStatement(ctx, tx, query)
	if err != nil {
		return nil, err
	}
	if stmt != nil {
		return stmt.ExecContext(ctx, args...)
	}
	return tx.ExecContext(ctx, query, args...)
}

type failedScan struct{ err error }

func (f failedScan) Scan(...any) error { return f.err }
func queryRecord(ctx context.Context, tx *sql.Tx, query string, args ...any) scanner {
	stmt, err := recordStatement(ctx, tx, query)
	if err != nil {
		return failedScan{err}
	}
	if stmt != nil {
		return stmt.QueryRowContext(ctx, args...)
	}
	return tx.QueryRowContext(ctx, query, args...)
}
