package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func backfillUsage(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,session,origin,source,parent,time,sequence,signal,trace,span,parent_span,detail FROM event_cards WHERE id>? ORDER BY id LIMIT 200", cursor)
	if err != nil {
		return cursor, 0, err
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
		return cursor, 0, errors.Join(err, rowErr)
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
			err = storeUsage(ctx, tx, c.e, document{}, c.p)
			if err != nil {
				break
			}
		}
		if relevant {
			e, eErr := scanEvent(tx.QueryRowContext(ctx, "SELECT "+qualifiedColumns("e")+" FROM events e WHERE id=?", c.e.ID))
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
				if _, err = tx.ExecContext(ctx, "UPDATE event_cards SET detail=json_set(detail,'$.title',?) WHERE id=?", full.Title, e.ID); err != nil {
					break
				}
			}
			if err = storeUsage(ctx, tx, e, doc, full); err != nil {
				break
			}
		}
		cursor = c.e.ID
	}

	if err != nil {
		return cursor, 0, err
	}
	if err = refreshUsage(ctx, tx); err != nil {
		return cursor, 0, err
	}
	return cursor, len(batch), nil
}
