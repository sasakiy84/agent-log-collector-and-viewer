package app

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
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
func intervalSchema() string {
	var sql strings.Builder
	for _, kind := range []string{"INSERT", "DELETE"} {
		ref, timing := "NEW", "AFTER"
		if kind == "DELETE" {
			ref, timing = "OLD", "BEFORE"
		}
		fmt.Fprintf(&sql, "CREATE TRIGGER IF NOT EXISTS usage_interval_%s %s %s ON usage_interval_data BEGIN\n", strings.ToLower(kind), timing, kind)
		if kind == "INSERT" {
			fmt.Fprintf(&sql, "INSERT INTO usage_interval_index VALUES(%[1]s.id,%[1]s.stream,%[1]s.stream,%[1]s.min_day,%[1]s.max_day,%[1]s.min_second,%[1]s.max_second,%[1]s.min_nano,%[1]s.max_nano);\n", ref)
		}
		fmt.Fprintf(&sql, `INSERT INTO usage_dirty SELECT p.session,p.origin,p.basis,p.group_key FROM usage_interval_index i CROSS JOIN usage_points p ON p.rowid=i.id
 WHERE i.min_stream<=%[1]s.stream AND i.max_stream>=%[1]s.stream AND i.min_day<=%[1]s.max_day AND i.max_day>=%[1]s.min_day
 AND i.min_second<=%[1]s.max_second AND i.max_second>=%[1]s.min_second AND i.min_nano<=%[1]s.max_nano AND i.max_nano>=%[1]s.min_nano
 AND p.start<%[1]s.end AND p.end>%[1]s.start ON CONFLICT DO NOTHING;
`, ref)
		if kind == "DELETE" {
			sql.WriteString("DELETE FROM usage_interval_index WHERE id=OLD.id;\n")
		}
		sql.WriteString("END;\n")
	}
	sql.WriteString(`CREATE TRIGGER IF NOT EXISTS usage_interval_stream_delete AFTER DELETE ON usage_interval_data BEGIN
 DELETE FROM usage_streams WHERE id=OLD.stream AND NOT EXISTS(SELECT 1 FROM usage_interval_data WHERE stream=OLD.stream);END;`)
	return sql.String()
}
func storeInterval(ctx context.Context, tx *sql.Tx, e Event, u *usagePoint) error {
	if u.Basis != "metric-delta" || u.Series == "" {
		return nil
	}
	var stream int64
	if err := tx.QueryRowContext(ctx, "INSERT INTO usage_streams(session,origin,series) VALUES(?,?,?) ON CONFLICT(session,origin,series) DO UPDATE SET series=excluded.series RETURNING id", e.Session, e.Origin, u.Series).Scan(&stream); err != nil {
		return err
	}
	if stream > 2147483647 {
		return fmt.Errorf("too many accounting streams")
	}
	b := intervalBounds(u.Start, u.End)
	_, err := tx.ExecContext(ctx, `INSERT INTO usage_interval_data SELECT rowid,id,?,?,?,?,?,?,?,?,? FROM usage_points WHERE id=?`, stream, u.Start, u.End, b[0], b[1], b[2], b[3], b[4], b[5], e.ID)
	return err
}
func overlappingUsage(ctx context.Context, tx *sql.Tx, eventID, key string) (bool, error) {
	var overlap bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM usage_interval_data d CROSS JOIN usage_interval_index i CROSS JOIN usage_points p ON p.rowid=i.id
 WHERE d.event_id=? AND i.min_stream<=d.stream AND i.max_stream>=d.stream AND i.min_day<=d.max_day AND i.max_day>=d.min_day
 AND i.min_second<=d.max_second AND i.max_second>=d.min_second AND i.min_nano<=d.max_nano AND i.max_nano>=d.min_nano
 AND p.start<d.end AND p.end>d.start AND p.group_key<>?)`, eventID, key).Scan(&overlap)
	return overlap, err
}
