package app

import (
	"context"
	"database/sql"
	"sync"
)

type skillSummaryKey struct {
	session, origin string
	revision        int64
}
type skillSummaryCache struct {
	sync.Mutex
	entries map[skillSummaryKey][]SkillSummary
	order   []skillSummaryKey
}

func (a *App) cachedSessionSkills(ctx context.Context, tx *sql.Tx, session, origin string) ([]SkillSummary, error) {
	var revision int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT revision FROM session_versions WHERE session=?),0)", session).Scan(&revision); err != nil {
		return nil, err
	}
	key := skillSummaryKey{session, origin, revision}
	cache := &a.skillSummaries
	cache.Lock()
	value, ok := cache.entries[key]
	cache.Unlock()
	if ok {
		return value, nil
	}
	value, err := sessionSkills(ctx, tx, session, origin)
	if err != nil || len(value) > 200 {
		return value, err
	}
	cache.Lock()
	defer cache.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[skillSummaryKey][]SkillSummary)
	}
	if _, ok := cache.entries[key]; !ok {
		// Both entry count and each value are bounded; historic revisions cannot grow
		// memory without limit. Revision and origin come from this read snapshot.
		if len(cache.order) == 64 {
			delete(cache.entries, cache.order[0])
			cache.order = cache.order[1:]
		}
		cache.order = append(cache.order, key)
		cache.entries[key] = value
	}
	return value, nil
}
