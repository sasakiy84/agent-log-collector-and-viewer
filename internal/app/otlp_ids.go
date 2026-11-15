package app

import (
	"encoding/base64"
	"encoding/hex"
)

func convertIDs(v any, toBase64 bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == "traceId" || k == "spanId" || k == "parentSpanId" {
				s, ok := v.(string)
				if !ok {
					continue
				}
				if toBase64 {
					want := 16
					if k == "traceId" {
						want = 32
					}
					if len(s) == want {
						if b, e := hex.DecodeString(s); e == nil {
							x[k] = base64.StdEncoding.EncodeToString(b)
						}
					}
				} else {
					if b, e := base64.StdEncoding.DecodeString(s); e == nil {
						x[k] = hex.EncodeToString(b)
					}
				}
			} else {
				convertIDs(v, toBase64)
			}
		}
	case []any:
		for _, v := range x {
			convertIDs(v, toBase64)
		}
	}
}
