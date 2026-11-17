package app

import (
	"context"
	"database/sql"
)

// Project navigation already maintains exact visible-root counts atomically
// with session/lock changes. Reuse them for list paging; only searches or event
// facets need to count catalog matches. Pending external edits use that fallback.
func cachedRootSessionCount(ctx context.Context, tx *sql.Tx, f sessionFilter) (int, bool, error) {
	if f.IncludeLocked || f.Folder != nil || f.Search != "" || f.Exclude != "" || f.Skill != "" || f.IDKey != "" || f.IDValue != "" || f.Facets.hasEvents() {
		return 0, false, nil
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM folder_dirty)").Scan(&pending); err != nil {
		return 0, false, err
	}
	if pending {
		return 0, false, nil
	}
	projects, sources := f.Facets.Projects, f.Facets.Sources
	if len(projects) == 0 && f.Project != nil {
		projects = []string{*f.Project}
	}
	if len(sources) == 0 && f.Source != "" {
		sources = []string{f.Source}
	}
	query, args := sqlSelections("project", projects)
	source, more := sqlSelections("source", sources)
	query += source
	args = append(args, more...)
	if !f.Diagnostics {
		query += " AND diagnostic=0"
	}
	var count int
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(root_count),0) FROM project_totals WHERE 1"+query, args...).Scan(&count)
	return count, true, err
}
