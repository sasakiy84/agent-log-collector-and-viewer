package app

import (
	"encoding/json"
	"math/big"
	"strings"
)

// Usage normalization adds counters and makes Anthropic input inclusive. Keep
// comparison with pre-usage archive tombstones possible without reviving deleted
// sessions on the next watcher replay. Real changes to the old fields still
// produce a different fingerprint and remain importable.
func legacyUsageRaw(doc document) ([]byte, bool) {
	if str(doc.Scope["name"]) != "agentlog.import" || str(doc.Record["eventName"]) != "gen_ai.client.operation" {
		return nil, false
	}
	a := doc.attributes()
	source := str(a["service.name"])
	if source != "codex" && source != "claude" {
		return nil, false
	}
	changed := false
	attributes := []any{}
	for _, item := range arr(doc.Record["attributes"]) {
		attr := obj(item)
		key := str(attr["key"])
		if strings.HasPrefix(key, "agentlog.usage.") || source == "codex" && (key == "gen_ai.usage.cache_write.input_tokens" || key == "gen_ai.usage.reasoning.output_tokens") || source == "claude" && key == "gen_ai.response.id" {
			changed = true
			continue
		}
		if source == "claude" && key == "gen_ai.usage.input_tokens" && str(a["agentlog.usage.input_includes_cache"]) == "true" {
			if n, ok := new(big.Int).SetString(str(a[key]), 10); ok {
				for _, cache := range []string{"gen_ai.usage.cache_read.input_tokens", "gen_ai.usage.cache_write.input_tokens"} {
					if count, ok := new(big.Int).SetString(str(a[cache]), 10); ok {
						n.Sub(n, count)
					}
				}
				if n.Sign() >= 0 {
					attr = map[string]any{"key": key, "value": map[string]any{"intValue": n.String()}}
					changed = true
				}
			}
		}
		attributes = append(attributes, attr)
	}
	if !changed {
		return nil, false
	}
	record := map[string]any{}
	for k, v := range doc.Record {
		record[k] = v
	}
	record["attributes"] = attributes
	encoded, err := json.Marshal(map[string]any{"record": record, "resource": doc.Resource, "scope": doc.Scope})
	return encoded, err == nil
}
