package app

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

// Exact integer rectangles keep interval lookups indexed even when a large
// historical batch arrives out of order. Split nanoseconds into bounded i32
// coordinates; crossing a boundary expands its lower-order dimensions.
func intervalBounds(start, end string) [6]int64 {
	a, _ := strconv.ParseUint(start, 10, 64)
	b, _ := strconv.ParseUint(end, 10, 64)
	const second = uint64(1000000000)
	const day = 86400 * second
	bounds := [6]int64{int64(a / day), int64(b / day), int64(a % day / second), int64(b % day / second), int64(a % second), int64(b % second)}
	if a/second != b/second {
		bounds[4] = 0
		bounds[5] = 999999999
	}
	if a/day != b/day {
		bounds[2] = 0
		bounds[3] = 86399
	}
	return bounds
}
func storeInterval(ctx context.Context, tx *sql.Tx, e Event, u *usagePoint) error {
	if u.Basis != "metric-delta" || u.Series == "" {
		return nil
	}
	var stream int64
	if err := queryRecord(ctx, tx, "INSERT INTO usage_streams(session,origin,series) VALUES(?,?,?) ON CONFLICT(session,origin,series) DO UPDATE SET series=excluded.series RETURNING id", e.Session, e.Origin, u.Series).Scan(&stream); err != nil {
		return err
	}
	if stream > 2147483647 {
		return fmt.Errorf("too many accounting streams")
	}
	b := intervalBounds(u.Start, u.End)
	_, err := execRecord(ctx, tx, `INSERT INTO usage_interval_data SELECT rowid,id,?,?,?,?,?,?,?,?,? FROM usage_samples WHERE id=?`, stream, u.Start, u.End, b[0], b[1], b[2], b[3], b[4], b[5], e.ID)
	return err
}
func overlappingUsage(ctx context.Context, tx *sql.Tx, eventID, key string) (bool, error) {
	var overlap bool
	err := queryRecord(ctx, tx, `SELECT EXISTS(SELECT 1 FROM usage_interval_data d CROSS JOIN usage_interval_index i CROSS JOIN usage_samples p ON p.rowid=i.id
 WHERE d.event_id=? AND i.min_stream<=d.stream AND i.max_stream>=d.stream AND i.min_day<=d.max_day AND i.max_day>=d.min_day
 AND i.min_second<=d.max_second AND i.max_second>=d.min_second AND i.min_nano<=d.max_nano AND i.max_nano>=d.min_nano
 AND p.start<d.end AND p.end>d.start AND p.group_key<>?)`, eventID, key).Scan(&overlap)
	return overlap, err
}
