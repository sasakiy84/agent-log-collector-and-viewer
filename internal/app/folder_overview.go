package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

type folderKey struct {
	Path, Source string
	Diagnostic   int
}

type folderMember struct {
	folderKey
	End, Usage string
}

// Reports and overlap counts make subtraction exact when a session disappears,
// moves folders, changes measurement coverage, or becomes archive-locked.
type folderAggregate struct {
	Count    int            `json:"-"`
	Usage    UsageSummary   `json:"usage"`
	Reports  map[string]int `json:"reports"`
	Overlaps int            `json:"overlaps"`
}

func (a *folderAggregate) apply(encoded string, sign int) error {
	u, err := decodeUsage(encoded)
	if err != nil {
		return err
	}
	if a.Reports == nil {
		a.Reports = map[string]int{}
		a.Usage.Values = map[string]string{}
		a.Usage.Coverage = map[string]int{}
	}
	a.Count += sign
	a.Usage.Sessions += sign * u.Sessions
	a.Usage.Excluded += sign * u.Excluded
	a.Usage.Legacy += sign * u.Legacy
	if u.Overlap {
		a.Overlaps += sign
	}
	a.Usage.Overlap = a.Overlaps > 0
	for key, value := range u.Values {
		a.Reports[key] += sign
		if a.Reports[key] == 0 {
			delete(a.Reports, key)
			delete(a.Usage.Values, key)
			delete(a.Usage.Coverage, key)
			continue
		}
		n, ok := new(big.Rat).SetString(value)
		if !ok {
			return fmt.Errorf("invalid folder usage %s", key)
		}
		if sign < 0 {
			n.Neg(n)
		}
		if old, ok := a.Usage.Values[key]; ok {
			x, valid := new(big.Rat).SetString(old)
			if !valid {
				return fmt.Errorf("invalid folder total %s", key)
			}
			n.Add(n, x)
		}
		a.Usage.Values[key] = decimalText(n)
		a.Usage.Coverage[key] += sign * u.Coverage[key]
	}
	return nil
}

func refreshFolderBatch(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, "SELECT session FROM folder_dirty LIMIT 200")
	if err != nil {
		return 0, err
	}
	sessions := []string{}
	for rows.Next() {
		var session string
		if err = rows.Scan(&session); err != nil {
			break
		}
		sessions = append(sessions, session)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return 0, err
	}
	groups := map[folderKey]*folderAggregate{}
	change := func(member folderMember, sign int) error {
		aggregate := groups[member.folderKey]
		if aggregate == nil {
			aggregate = &folderAggregate{}
			var encoded string
			err := tx.QueryRowContext(ctx, "SELECT count,totals FROM folder_totals WHERE path=? AND source=? AND diagnostic=?", member.Path, member.Source, member.Diagnostic).Scan(&aggregate.Count, &encoded)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				if err = json.Unmarshal([]byte(encoded), aggregate); err != nil {
					return err
				}
			}
			groups[member.folderKey] = aggregate
		}
		return aggregate.apply(member.Usage, sign)
	}
	for _, session := range sessions {
		var old, next folderMember
		oldErr := tx.QueryRowContext(ctx, "SELECT path,source,diagnostic,end,usage FROM folder_members WHERE session=?", session).Scan(&old.Path, &old.Source, &old.Diagnostic, &old.End, &old.Usage)
		if oldErr != nil && !errors.Is(oldErr, sql.ErrNoRows) {
			return 0, oldErr
		}
		nextErr := tx.QueryRowContext(ctx, "SELECT "+directoryColumn+",source,end,"+usageColumn+" FROM session_catalog s WHERE s.session=? AND NOT EXISTS(SELECT 1 FROM archive_locks WHERE session=s.session)", session).Scan(&next.Path, &next.Source, &next.End, &next.Usage)
		if nextErr != nil && !errors.Is(nextErr, sql.ErrNoRows) {
			return 0, nextErr
		}
		if nextErr == nil {
			if next.Source == "codex" && (session == "unassigned:codex" || strings.HasPrefix(session, "trace:codex:")) {
				next.Diagnostic = 1
			}
			u, err := decodeUsage(next.Usage)
			if err != nil {
				return 0, err
			}
			// Folder accounting needs additive values, not duplicate origin reports.
			u.Basis, u.Variants = nil, nil
			encoded, err := json.Marshal(u)
			if err != nil {
				return 0, err
			}
			next.Usage = string(encoded)
		}
		if oldErr == nil && (nextErr != nil || old != next) {
			if err = change(old, -1); err != nil {
				return 0, err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM folder_members WHERE session=?", session); err != nil {
				return 0, err
			}
		}
		if nextErr == nil && (oldErr != nil || old != next) {
			if err = change(next, 1); err != nil {
				return 0, err
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO folder_members VALUES(?,?,?,?,?,?)", session, next.Path, next.Source, next.Diagnostic, next.End, next.Usage); err != nil {
				return 0, err
			}
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM folder_dirty WHERE session=?", session); err != nil {
			return 0, err
		}
	}
	for key, aggregate := range groups {
		if aggregate.Count == 0 {
			_, err = tx.ExecContext(ctx, "DELETE FROM folder_totals WHERE path=? AND source=? AND diagnostic=?", key.Path, key.Source, key.Diagnostic)
		} else {
			var end string
			err = tx.QueryRowContext(ctx, "SELECT end FROM folder_members WHERE path=? AND source=? AND diagnostic=? ORDER BY end DESC,session LIMIT 1", key.Path, key.Source, key.Diagnostic).Scan(&end)
			if err != nil {
				return 0, err
			}
			encoded, e := json.Marshal(aggregate)
			if e != nil {
				return 0, e
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO folder_totals VALUES(?,?,?,?,?,?) ON CONFLICT(path,source,diagnostic) DO UPDATE SET count=excluded.count,end=excluded.end,totals=excluded.totals", key.Path, key.Source, key.Diagnostic, aggregate.Count, end, string(encoded))
		}
		if err != nil {
			return 0, err
		}
	}
	return len(sessions), nil
}

func refreshFolders(ctx context.Context, tx *sql.Tx) error {
	for {
		n, err := refreshFolderBatch(ctx, tx)
		if err != nil || n == 0 {
			return err
		}
	}
}

func cachedFolders(ctx context.Context, tx *sql.Tx, source string, diagnostics bool, path *string, offset int) ([]FolderSummary, int, int, error) {
	filter, args := " FROM folder_totals WHERE 1=1", []any{}
	if source != "" {
		filter += " AND source=?"
		args = append(args, source)
	}
	if !diagnostics {
		filter += " AND diagnostic=0"
	}
	if path != nil {
		filter += " AND path=?"
		args = append(args, *path)
	}
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(DISTINCT path)"+filter, args...).Scan(&total); err != nil {
		return nil, 0, 0, err
	}
	offset = pageOffset(offset, total, 200)
	query := "WITH page AS (SELECT path" + filter + " GROUP BY path ORDER BY MAX(end) DESC,path LIMIT 200 OFFSET ?) SELECT f.path,f.source,f.count,f.end,f.totals FROM page CROSS JOIN folder_totals f ON f.path=page.path"
	// Apply the same group constraints when loading summaries for the page.
	query += " WHERE (?='' OR f.source=?) AND (? OR f.diagnostic=0)"
	params := append(append([]any{}, args...), offset, source, source, diagnostics)
	rows, err := tx.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	out, positions := []FolderSummary{}, map[string]int{}
	for rows.Next() {
		var path, source, end, encoded string
		var count int
		if err = rows.Scan(&path, &source, &count, &end, &encoded); err != nil {
			return nil, 0, 0, err
		}
		index, found := positions[path]
		if !found {
			index = len(out)
			positions[path] = index
			out = append(out, FolderSummary{Path: path, Sources: []string{}})
		}
		f := &out[index]
		f.Count += count
		if end > f.End {
			f.End = end
		}
		if !containsSource(f.Sources, source) {
			f.Sources = append(f.Sources, source)
		}
		var aggregate folderAggregate
		if err = json.Unmarshal([]byte(encoded), &aggregate); err != nil {
			return nil, 0, 0, err
		}
		f.Usage.add(aggregate.Usage)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	for i := range out {
		sort.Strings(out[i].Sources)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].End == out[j].End {
			return out[i].Path < out[j].Path
		}
		return out[i].End > out[j].End
	})
	return out, total, offset, nil
}

func containsSource(sources []string, source string) bool {
	for _, existing := range sources {
		if existing == source {
			return true
		}
	}
	return false
}
