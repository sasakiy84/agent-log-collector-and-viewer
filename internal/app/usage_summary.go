package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
)

// Decimal strings preserve token counts above JavaScript's integer limit and
// exact reported costs. Coverage distinguishes missing measurements from zero.
type UsageSummary struct {
	Values   map[string]string        `json:"values"`
	Coverage map[string]int           `json:"coverage"`
	Sessions int                      `json:"sessions"`
	Basis    map[string]string        `json:"basis,omitempty"`
	Overlap  bool                     `json:"overlap,omitempty"`
	Excluded int                      `json:"excluded,omitempty"`
	Legacy   int                      `json:"legacy,omitempty"`
	Variants map[string]*UsageSummary `json:"variants,omitempty"`
}

func emptyUsage() UsageSummary {
	return UsageSummary{Values: map[string]string{"errors": "0"}, Coverage: map[string]int{"errors": 1}, Sessions: 1}
}

const usageColumn = "COALESCE((SELECT summary FROM usage_summary u WHERE u.session=s.session AND u.origin=''),'{}')"

func decodeUsage(encoded string) (UsageSummary, error) {
	s := emptyUsage()
	err := json.Unmarshal([]byte(encoded), &s)
	return s, err
}

func folderUsage(ctx context.Context, tx *sql.Tx, filter string, args []any, folders []FolderSummary) error {
	if len(folders) == 0 {
		return nil
	}
	positions := map[string]int{}
	marks := []string{}
	for i, f := range folders {
		positions[f.Path] = i
		args = append(args, f.Path)
		marks = append(marks, "?")
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+directoryColumn+","+usageColumn+filter+" AND "+directoryColumn+" IN ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var path, encoded string
		if err = rows.Scan(&path, &encoded); err != nil {
			return err
		}
		s, e := decodeUsage(encoded)
		if e != nil {
			return e
		}
		folders[positions[path]].Usage.add(s)
	}
	return rows.Err()
}
func (s *UsageSummary) add(other UsageSummary) {
	if s.Values == nil {
		s.Values = map[string]string{}
		s.Coverage = map[string]int{}
	}
	s.Sessions += other.Sessions
	s.Overlap = s.Overlap || other.Overlap
	s.Excluded += other.Excluded
	s.Legacy += other.Legacy
	for k, v := range other.Values {
		n, _ := new(big.Rat).SetString(v)
		if old, ok := s.Values[k]; ok {
			x, _ := new(big.Rat).SetString(old)
			n.Add(n, x)
		}
		s.Values[k] = decimalText(n)
		s.Coverage[k] += other.Coverage[k]
	}
}
func chooseUsage(s *UsageSummary, bases map[string]map[string]string, family string, components []string, qualifies func(map[string]string) bool) {
	available := 0
	for _, basis := range []string{"codex-total", "metric-cumulative", "metric-delta", "traces", "logs"} {
		values := bases[basis]
		if !qualifies(values) {
			continue
		}
		available++
		if s.Basis[family] != "" {
			continue
		}
		s.Basis[family] = basis
		for _, k := range components {
			if v, ok := values[k]; ok {
				s.Values[k] = v
				s.Coverage[k] = 1
			}
		}
	}
	if available > 1 {
		s.Overlap = true
	}
}
func makeUsage(bases map[string]map[string]string) UsageSummary {
	s := emptyUsage()
	s.Basis = map[string]string{}
	chooseUsage(&s, bases, "tokens", []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens"}, func(v map[string]string) bool { return v["input_tokens"] != "" || v["output_tokens"] != "" })
	if s.Basis["tokens"] == "" {
		chooseUsage(&s, bases, "tokens", []string{"cache_read_tokens", "cache_write_tokens", "reasoning_tokens"}, func(v map[string]string) bool {
			return v["cache_read_tokens"] != "" || v["cache_write_tokens"] != "" || v["reasoning_tokens"] != ""
		})
	}
	chooseUsage(&s, bases, "cost", []string{"cost_usd"}, func(v map[string]string) bool { return v["cost_usd"] != "" })
	chooseUsage(&s, bases, "latency", []string{"duration_ms", "duration_samples"}, func(v map[string]string) bool {
		return v["duration_ms"] != "" && v["duration_samples"] != "" && v["duration_samples"] != "0"
	})
	for _, values := range bases {
		if n, e := strconv.Atoi(values["excluded"]); e == nil {
			s.Excluded += n
		}
		if n, e := strconv.Atoi(values["legacy"]); e == nil {
			s.Legacy += n
		}
	}
	if v := bases["events"]["errors"]; v != "" {
		s.Values["errors"] = v
	} else {
		s.Values["errors"] = "0"
	}
	s.Coverage["errors"] = 1
	return s
}
func combinedUsage(imported, native UsageSummary, errors string) UsageSummary {
	s := emptyUsage()
	s.Basis = map[string]string{}
	for _, f := range []struct {
		name       string
		components []string
	}{
		{"tokens", []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens"}},
		{"cost", []string{"cost_usd"}}, {"latency", []string{"duration_ms", "duration_samples"}},
	} {
		pick, origin := native, "otel"
		if native.Basis[f.name] == "" || f.name == "tokens" && (imported.Basis[f.name] == "codex-total" || (native.Values["input_tokens"] == "" && native.Values["output_tokens"] == "") && (imported.Values["input_tokens"] != "" || imported.Values["output_tokens"] != "")) {
			pick, origin = imported, "import"
		}
		if pick.Basis[f.name] == "" {
			continue
		}
		s.Basis[f.name] = origin + "/" + pick.Basis[f.name]
		for _, k := range f.components {
			if v, ok := pick.Values[k]; ok {
				s.Values[k] = v
				s.Coverage[k] = 1
			}
		}
		if native.Basis[f.name] != "" && imported.Basis[f.name] != "" {
			s.Overlap = true
		}
	}
	s.Values["errors"] = errors
	s.Coverage["errors"] = 1
	s.Overlap = s.Overlap || native.Overlap || imported.Overlap
	s.Excluded = native.Excluded + imported.Excluded
	s.Legacy = imported.Legacy
	if s.Overlap {
		s.Variants = map[string]*UsageSummary{"import": &imported, "otel": &native}
	}
	return s
}
func refreshUsageSummaries(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT session FROM usage_sessions_dirty")
	if err != nil {
		return err
	}
	sessions := []string{}
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			break
		}
		sessions = append(sessions, s)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return errors.Join(err, rowErr)
	}
	for _, session := range sessions {
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM session_catalog WHERE session=?)", session).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		rows, err = tx.QueryContext(ctx, "SELECT origin,basis,component,value FROM usage_totals WHERE session=?", session)
		if err != nil {
			return err
		}
		all := map[string]map[string]map[string]string{"import": {}, "otel": {}, "": {}}
		for rows.Next() {
			var origin, basis, k, v string
			if err = rows.Scan(&origin, &basis, &k, &v); err != nil {
				break
			}
			if all[origin] == nil {
				all[origin] = map[string]map[string]string{}
			}
			if all[origin][basis] == nil {
				all[origin][basis] = map[string]string{}
			}
			all[origin][basis][k] = v
		}
		rowErr = rows.Err()
		rows.Close()
		if err != nil || rowErr != nil {
			return errors.Join(err, rowErr)
		}
		imported, native := makeUsage(all["import"]), makeUsage(all["otel"])
		errs := all[""]["events"]["errors"]
		if errs == "" {
			errs = "0"
		}
		for origin, summary := range map[string]UsageSummary{"import": imported, "otel": native, "": combinedUsage(imported, native, errs)} {
			encoded, e := json.Marshal(summary)
			if e != nil {
				return e
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO usage_summary VALUES(?,?,?) ON CONFLICT(session,origin) DO UPDATE SET summary=excluded.summary", session, origin, string(encoded)); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM usage_sessions_dirty")
	return err
}

func (a *App) initUsage() error {
	for {
		var cursor string
		var complete bool
		if err := a.DB.QueryRow("SELECT cursor,complete FROM usage_backfill WHERE id=1").Scan(&cursor, &complete); err != nil {
			return err
		}
		if complete {
			return nil
		}
		tx, err := a.DB.Begin()
		if err != nil {
			return err
		}
		rows, err := tx.Query("SELECT id,session,origin,source,parent,time,sequence,signal,trace,span,parent_span,detail FROM event_cards WHERE id>? ORDER BY id LIMIT 200", cursor)
		if err != nil {
			tx.Rollback()
			return err
		}
		type card struct {
			e Event
			p Detail
		}
		batch := []card{}
		for rows.Next() {
			var c card
			var raw []byte
			if err = rows.Scan(&c.e.ID, &c.e.Session, &c.e.Origin, &c.e.Source, &c.e.Parent, &c.e.Time, &c.e.Sequence, &c.e.Signal, &c.e.Trace, &c.e.Span, &c.e.ParentSpan, &raw); err != nil {
				break
			}
			if err = json.Unmarshal(raw, &c.p); err != nil {
				break
			}
			batch = append(batch, c)
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil || rowErr != nil {
			tx.Rollback()
			return errors.Join(err, rowErr)
		}
		for _, c := range batch {
			// Most histories are large messages/tool outputs. Only normalized
			// usage candidates require original payloads during this upgrade.
			relevant := c.p.Kind == "model" || c.p.Kind == "metric"
			for _, f := range c.p.Fields {
				switch f[0] {
				case "Input tokens", "Output tokens", "Cache read", "Cache write", "Reasoning", "Cost (USD)":
					relevant = true
				}
			}
			if !relevant && c.p.Error {
				err = storeUsage(context.Background(), tx, c.e, document{}, c.p)
				if err != nil {
					break
				}
			}
			if relevant {
				e, eErr := scanEvent(tx.QueryRow("SELECT "+qualifiedColumns("e")+" FROM events e WHERE id=?", c.e.ID))
				if eErr != nil {
					err = eErr
					break
				}
				doc, eErr := decodeDocument(e.Raw)
				if eErr != nil {
					err = eErr
					break
				}
				full := describe(e, doc)
				if full.Title == "Token usage" && c.p.Title != full.Title {
					if _, err = tx.Exec("UPDATE event_cards SET detail=json_set(detail,'$.title',?) WHERE id=?", full.Title, e.ID); err != nil {
						break
					}
				}
				if err = storeUsage(context.Background(), tx, e, doc, full); err != nil {
					break
				}
			}
			cursor = c.e.ID
		}
		if err == nil {
			err = refreshUsage(context.Background(), tx)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE usage_backfill SET cursor=?,complete=? WHERE id=1", cursor, len(batch) == 0)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return err
		}
	}
}
