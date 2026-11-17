package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// recordAnalysis is immutable analyzer output. Parsing, provider interpretation
// and Japanese tokenization do not hold the SQLite writer reservation. The
// writer owns persistence and all affected projections in one transaction.
type recordAnalysis struct {
	ready    bool
	event    Event
	document document
	detail   Detail
	keys     []correlationKey
	search   searchDocument
}

func analyzeRecord(ctx context.Context, e Event) (recordAnalysis, error) {
	e = identifyRecord(e)
	doc, err := decodeDocument(e.Raw)
	if err != nil {
		return recordAnalysis{}, err
	}
	detail := describe(e, doc)
	e.Origin = "otel"
	if detail.Attributes["agentlog.origin"] == "import" {
		e.Origin = "import"
	}
	search, err := analyzeSearch(ctx, e, doc)
	if err != nil {
		return recordAnalysis{}, err
	}
	return recordAnalysis{ready: true, event: e, document: doc, detail: detail, keys: correlationKeys(e, detail, doc), search: search}, nil
}

func identifyRecord(e Event) Event {
	if e.ID == "" {
		digest := sha256.Sum256(append([]byte(e.Source+e.Signal), e.Raw...))
		e.ID = hex.EncodeToString(digest[:])
	}
	return e
}
