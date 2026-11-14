package app

import "encoding/json"

type Event struct {
	Origin     string          `json:"origin"`
	Sequence   int             `json:"sequence,omitempty"`
	ID         string          `json:"id"`
	Session    string          `json:"session"`
	Parent     string          `json:"parent,omitempty"`
	Source     string          `json:"source"`
	Signal     string          `json:"signal"`
	Time       string          `json:"time"`
	Name       string          `json:"name"`
	Trace      string          `json:"trace,omitempty"`
	Span       string          `json:"span,omitempty"`
	ParentSpan string          `json:"parent_span,omitempty"`
	Raw        json.RawMessage `json:"raw"`
}
