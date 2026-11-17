package app

import (
	"strings"
	"testing"
)

func TestSkillIdentityLookupsUseIndexes(t *testing.T) {
	a := setup(t)
	queries := []struct {
		name, query string
		args        []any
	}{
		{"rebuild", skillLogicalIDLookup, []any{"missing-id", "missing-id"}},
		{"injection rollup", skillRollup() + " FROM skill_events k GROUP BY k.name,k.plugin", nil},
	}
	for _, test := range queries {
		t.Run(test.name, func(t *testing.T) {
			rows, err := a.DB.Query("EXPLAIN QUERY PLAN "+test.query, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			primary, paired := false, false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(detail, "SCAN logical_events") {
					t.Fatal("skill lookup scans the complete timeline:", detail)
				}
				primary = primary || strings.Contains(detail, "sqlite_autoindex_logical_events_1")
				paired = paired || strings.Contains(detail, "timeline_pair_otel")
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !primary || !paired {
				t.Fatal("lookup must seek both original and merged identities", primary, paired)
			}
		})
	}
}
