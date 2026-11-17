package importer

import (
	"context"
	"github.com/sasakiy84/agent-log-collector-and-viewer/internal/checkpoint"
)

// Reparse for canonical-message/journal correctness, but only deliver records
// whose normalized bytes changed. Persistent lookups are bounded to one batch;
// no original payloads are retained between scans.
type recordDelta struct {
	store  *checkpoint.Store
	key    string
	memory map[string][32]byte
}

func (d *recordDelta) changed(ctx context.Context, batch []any, hashes []checkpoint.Record) ([]any, []checkpoint.Record, error) {
	previous := d.memory
	if d.store != nil {
		var err error
		previous, err = d.store.Records(ctx, d.key, hashes)
		if err != nil {
			return nil, nil, err
		}
	}
	records, changed := []any{}, []checkpoint.Record{}
	// Include changes of the same ID within this batch, in their source order.
	seen := map[string][32]byte{}
	for i, r := range hashes {
		old, ok := seen[r.ID]
		if !ok {
			old, ok = previous[r.ID]
		}
		if !ok || old != r.Hash {
			records = append(records, batch[i])
			changed = append(changed, r)
		}
		seen[r.ID] = r.Hash
	}
	return records, changed, nil
}

func (d *recordDelta) acknowledge(ctx context.Context, hashes []checkpoint.Record) error {
	if len(hashes) == 0 {
		return nil
	}
	if d.store != nil {
		return d.store.PutRecords(ctx, d.key, hashes)
	}
	if d.memory == nil {
		d.memory = map[string][32]byte{}
	}
	for _, r := range hashes {
		d.memory[r.ID] = r.Hash
	}
	return nil
}
