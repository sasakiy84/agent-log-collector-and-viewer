package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ikawaha/kagome-dict/ipa"
	"github.com/ikawaha/kagome/v2/filter/ja"
	"github.com/ikawaha/kagome/v2/tokenizer"
	"golang.org/x/text/unicode/norm"
)

// FTS5 is compiled into SQLite on every supported platform. Contentless-delete
// stores the inverted index without a second copy of the full log text.
const searchSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS event_search USING fts5(
 text,content='',contentless_delete=1,tokenize='unicode61 remove_diacritics 2',prefix='2 3');
CREATE TABLE IF NOT EXISTS search_documents(
 rowid INTEGER PRIMARY KEY,event_id TEXT NOT NULL UNIQUE REFERENCES events(id) ON DELETE CASCADE,session TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS search_dirty(event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE);
CREATE TRIGGER IF NOT EXISTS search_document_delete AFTER DELETE ON search_documents BEGIN
 DELETE FROM event_search WHERE rowid=OLD.rowid;END;
CREATE TRIGGER IF NOT EXISTS search_event_insert AFTER INSERT ON events BEGIN
 INSERT INTO search_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;
CREATE TRIGGER IF NOT EXISTS search_event_update AFTER UPDATE ON events
 WHEN OLD.raw IS NOT NEW.raw OR OLD.session<>NEW.session OR OLD.name<>NEW.name OR OLD.source<>NEW.source BEGIN
 INSERT INTO search_dirty VALUES(NEW.id) ON CONFLICT DO NOTHING;END;
`

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
func searchableValues(ctx context.Context, value any, emit func(string) error) error {
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
				return searchableValues(ctx, nested, emit)
			}
		}
		return emit(v)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if err := emit(key); err != nil {
				return err
			}
			if err := searchableValues(ctx, v[key], emit); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := searchableValues(ctx, item, emit); err != nil {
				return err
			}
		}
	case json.Number:
		return emit(string(v))
	case bool:
		if v {
			return emit("true")
		}
		return emit("false")
	}
	return nil
}
func storeSearch(ctx context.Context, tx *sql.Tx, event Event, doc document) error {
	var text strings.Builder
	emit := func(value string) error {
		return searchTokens(ctx, value, func(term string) { text.WriteString(term); text.WriteByte(' ') })
	}
	for _, value := range []string{event.Session, event.Parent, event.Source, event.Name, event.Trace, event.Span} {
		if err := emit(value); err != nil {
			return err
		}
	}
	for _, scope := range []map[string]any{doc.Resource, doc.Scope, doc.Record} {
		if err := searchableValues(ctx, scope, emit); err != nil {
			return err
		}
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
func (a *App) initSearchSchema() error {
	if _, err := a.DB.Exec(searchSchema); err != nil {
		return err
	}
	_, err := a.DB.Exec(`INSERT INTO search_dirty SELECT e.id FROM events e LEFT JOIN search_documents d ON d.event_id=e.id WHERE d.event_id IS NULL ON CONFLICT DO NOTHING`)
	return err
}
func (a *App) initSearchIndex() error {
	var total int
	if err := a.DB.QueryRow("SELECT COUNT(*) FROM search_dirty").Scan(&total); err != nil {
		return err
	}
	if total > 0 {
		log.Printf("Preparing Japanese full-text index for %d existing records (one-time upgrade)", total)
	}
	lastReport := time.Now()
	for processed := 0; processed < total; {
		tx, err := a.DB.Begin()
		if err != nil {
			return err
		}
		count, err := refreshSearchBatch(context.Background(), tx)
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return err
		}
		if count == 0 {
			return errors.New("full-text indexing made no progress")
		}
		processed += count
		if time.Since(lastReport) >= 5*time.Second || processed == total {
			log.Printf("Japanese full-text index: %d/%d records", processed, total)
			lastReport = time.Now()
		}
	}
	return nil
}
func refreshSearchBatch(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+qualifiedColumns("e")+" FROM search_dirty d CROSS JOIN events e ON e.id=d.event_id LIMIT 200")
	if err != nil {
		return 0, err
	}
	batch := []Event{}
	size := 0
	for rows.Next() {
		event, e := scanEvent(rows)
		if e != nil {
			err = e
			break
		}
		batch = append(batch, event)
		size += len(event.Raw)
		if size >= 8<<20 {
			break
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return 0, errors.Join(err, rowErr)
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
