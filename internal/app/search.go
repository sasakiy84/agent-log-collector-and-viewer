package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/ikawaha/kagome-dict/ipa"
	"github.com/ikawaha/kagome/v2/filter/ja"
	"github.com/ikawaha/kagome/v2/tokenizer"
	"golang.org/x/text/unicode/norm"
)

// FTS5 is compiled into SQLite on every supported platform. Contentless-delete
// stores the inverted index without a second copy of the full log text.

type japaneseAnalyzer struct {
	tokenizer *tokenizer.Tokenizer
	filter    *ja.Filter
}

var loadJapanese = sync.OnceValues(func() (*japaneseAnalyzer, error) {
	t, err := tokenizer.New(ipa.Dict(), tokenizer.OmitBosEos())
	if err != nil {
		return nil, err
	}
	f, err := ja.NewFilter()
	if err != nil {
		return nil, err
	}
	return &japaneseAnalyzer{t, f}, nil
})

func japaneseRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) || r == 'ー' || r == '々' || r == '〆'
}
func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) }

func emitSearchWords(text string, emit func(string)) {
	for term := range strings.FieldsFuncSeq(text, func(r rune) bool { return !wordRune(r) }) {
		if strings.IndexFunc(term, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) >= 0 {
			emit(term)
		}
	}
}

// Apply identical NFKC/case normalization and Japanese search-mode analysis to
// both documents and queries. Latin/code text uses Unicode word boundaries.
func searchTokens(ctx context.Context, text string, emit func(string)) error {
	text = strings.ToLower(norm.NFKC.String(text))
	consume := func(run string, japanese bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		words := []string{run}
		if japanese {
			analyzer, err := loadJapanese()
			if err != nil {
				return err
			}
			return analyzer.tokens(ctx, run, func(word string) { emitSearchWords(word, emit) })
		}
		for _, word := range words {
			emitSearchWords(word, emit)
		}
		return nil
	}
	start := 0
	current := false
	first := true
	for i, r := range text {
		isJapanese := japaneseRune(r)
		if first {
			current = isJapanese
			first = false
		} else if isJapanese != current {
			if err := consume(text[start:i], current); err != nil {
				return err
			}
			start = i
			current = isJapanese
		}
	}
	if start < len(text) {
		return consume(text[start:], current)
	}
	return ctx.Err()
}

// Keep the Viterbi lattice bounded even for megabytes of unpunctuated text.
// Commit complete tokens before the midpoint, retaining the trailing context
// for the next window so words crossing a window boundary remain searchable.
func (a *japaneseAnalyzer) tokens(ctx context.Context, text string, emit func(string)) error {
	for len(text) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		end, midpoint, count := len(text), len(text), 0
		for i := range text {
			if count == 1024 {
				midpoint = i
			}
			if count == 2048 {
				end = i
				break
			}
			count++
		}
		tokens := a.tokenizer.Analyze(text[:end], tokenizer.Search)
		committed, advance := len(tokens), end
		if end < len(text) {
			committed, advance = 0, 0
			for i, token := range tokens {
				last := token.Position + len(token.Surface)
				if last > midpoint {
					break
				}
				committed = i + 1
				advance = last
			}
			// A single unknown word may fill the window. Its tokenizer boundary still
			// gives bounded progress, as with a standard tokenizer's maximum word size.
			if committed == 0 {
				committed = 1
				advance = tokens[0].Position + len(tokens[0].Surface)
			}
		}
		for _, term := range a.filter.Yield(tokens[:committed]) {
			emit(term)
		}
		text = text[advance:]
	}
	return nil
}

func searchMatch(ctx context.Context, query string) (string, error) {
	if utf8.RuneCountInString(query) > 512 {
		return "", fmt.Errorf("Search is limited to 512 characters")
	}
	terms := []string{}
	seen := map[string]bool{}
	err := searchTokens(ctx, query, func(term string) {
		if !seen[term] {
			seen[term] = true
			terms = append(terms, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		}
	})
	if err != nil {
		return "", err
	}
	if len(terms) > 64 {
		return "", fmt.Errorf("Search is limited to 64 terms")
	}
	if len(terms) == 0 {
		return "", nil
	}
	terms[len(terms)-1] += "*"
	return strings.Join(terms, " AND "), nil
}

// Decode nested JSON strings too: a literal Japanese query must find text sent
// as JSON \u escapes, including JSON embedded in OTLP string attributes.
func searchableValues(ctx context.Context, value any, emit func(string) error, identifier func(string, any)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if len(trimmed) > 1 && (trimmed[0] == '{' || trimmed[0] == '[' || trimmed[0] == '"') {
			decoder := json.NewDecoder(strings.NewReader(trimmed))
			decoder.UseNumber()
			var nested any
			if decoder.Decode(&nested) == nil && decoder.Decode(new(any)) == io.EOF {
				return searchableValues(ctx, nested, emit, identifier)
			}
		}
		return emit(v)
	case map[string]any:
		// OTLP AnyValue preserves numeric/boolean types even though protobuf
		// JSON encodes int64 as strings. Never feed wire wrappers to the FTS.
		for _, key := range []string{"intValue", "doubleValue", "boolValue", "bytesValue"} {
			if _, ok := v[key]; ok && len(v) == 1 {
				return nil
			}
		}
		if key, ok := v["key"].(string); ok {
			if item, exists := v["value"]; exists && len(v) == 2 {
				if identifierKey(key) {
					identifier(key, item)
					return nil
				}
				return searchableValues(ctx, item, emit, identifier)
			}
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if identifierKey(key) {
				identifier(key, v[key])
				continue
			}
			if err := searchableValues(ctx, v[key], emit, identifier); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := searchableValues(ctx, item, emit, identifier); err != nil {
				return err
			}
		}
	}
	return nil
}
func storeSearch(ctx context.Context, tx *sql.Tx, event Event, doc document) error {
	var text strings.Builder
	identifiers := map[searchIdentifier]bool{}
	identifier := func(key string, value any) {
		identifierStrings(value, func(s string) {
			identifiers[searchIdentifier{key, s}] = true
			identifiers[searchIdentifier{identifierAlias(key), s}] = true
		})
	}
	for key, value := range map[string]string{"event.id": event.ID, "session.id": event.Session, "parent.session.id": event.Parent, "trace.id": event.Trace, "span.id": event.Span, "parent.span.id": event.ParentSpan} {
		identifier(key, value)
	}
	emit := func(value string) error {
		return searchTokens(ctx, value, func(term string) { text.WriteString(term); text.WriteByte(' ') })
	}
	for _, value := range []string{event.Source, event.Name} {
		if err := emit(value); err != nil {
			return err
		}
	}
	// These are numeric OTLP wire fields, not arbitrary string-valued keys
	// inside a tool's JSON output. Do not mutate the original document.
	record := maps.Clone(doc.Record)
	for _, key := range []string{"timeUnixNano", "observedTimeUnixNano", "startTimeUnixNano", "endTimeUnixNano", "asInt", "asDouble", "count", "sum", "min", "max", "zeroCount", "bucketCounts", "positive", "negative", "quantileValues"} {
		delete(record, key)
	}
	for _, scope := range []map[string]any{doc.Resource, doc.Scope, record} {
		if err := searchableValues(ctx, scope, emit, identifier); err != nil {
			return err
		}
	}
	if err := storeIdentifiers(ctx, tx, event, identifiers); err != nil {
		return err
	}
	var rowid int64
	err := tx.QueryRowContext(ctx, `INSERT INTO search_documents(event_id,session) VALUES(?,?) ON CONFLICT(event_id) DO UPDATE SET session=excluded.session RETURNING rowid`, event.ID, event.Session).Scan(&rowid)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM event_search WHERE rowid=?", rowid); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO event_search(rowid,text) VALUES(?,?)", rowid, text.String()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM search_dirty WHERE event_id=?", event.ID)
	return err
}
func (a *App) prepareSearch(ctx context.Context) error {
	if err := a.drainBatches(ctx, "full-text search", refreshSearchBatch); err != nil {
		return err
	}
	return a.runMaintenance(ctx, "search_strings.v1", backfillSearchStrings, 0)
}

func backfillSearchStrings(ctx context.Context, tx *sql.Tx, cursor string) (string, int, error) {
	batch, err := loadEventBatch(ctx, tx, "SELECT "+eventColumns+" FROM events WHERE id>? ORDER BY id LIMIT 200", cursor)
	if err != nil {
		return cursor, 0, err
	}
	for _, event := range batch {
		doc, err := decodeDocument(event.Raw)
		if err != nil {
			return cursor, 0, err
		}
		if err = storeSearch(ctx, tx, event, doc); err != nil {
			return cursor, 0, err
		}
		cursor = event.ID
	}
	return cursor, len(batch), nil
}
func refreshSearchBatch(ctx context.Context, tx *sql.Tx) (int, error) {
	batch, err := loadEventBatch(ctx, tx, "SELECT "+qualifiedColumns("e")+" FROM search_dirty d CROSS JOIN events e ON e.id=d.event_id LIMIT 200")
	if err != nil {
		return 0, err
	}
	for _, event := range batch {
		doc, err := decodeDocument(event.Raw)
		if err != nil {
			return 0, err
		}
		if err = storeSearch(ctx, tx, event, doc); err != nil {
			return 0, err
		}
	}
	return len(batch), nil
}
func refreshSearch(ctx context.Context, tx *sql.Tx) error {
	for {
		count, err := refreshSearchBatch(ctx, tx)
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
	}
}

// Follow SQLite's documented incremental optimize sequence. Removing FTS
// tombstones before vacuum returns archive space without one long optimize
// transaction monopolizing ingestion.
func (a *App) mergeSearchIndex(ctx context.Context) error {
	pages := -128
	for {
		tx, err := a.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var before, after int64
		err = tx.QueryRowContext(ctx, "SELECT total_changes()").Scan(&before)
		if err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO event_search(event_search,rank) VALUES('merge',?)", pages)
		}
		if err == nil {
			err = tx.QueryRowContext(ctx, "SELECT total_changes()").Scan(&after)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return err
		}
		if after-before < 2 {
			return nil
		}
		pages = 128
	}
}
