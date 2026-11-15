package app

import (
	"sort"
)

type ConflictValue struct {
	Origin string `json:"origin"`
	Value  string `json:"value"`
}
type Conflict struct {
	Field  string          `json:"field"`
	Values []ConflictValue `json:"values"`
}
type TimelineEvent struct {
	ID         string         `json:"id"`
	Session    string         `json:"session"`
	Source     string         `json:"source"`
	Time       string         `json:"time"`
	Signal     string         `json:"signal"`
	Trace      string         `json:"trace,omitempty"`
	Span       string         `json:"span,omitempty"`
	ParentSpan string         `json:"parent_span,omitempty"`
	Detail     Detail         `json:"detail"`
	Origins    []string       `json:"origins"`
	Records    []Event        `json:"records"`
	Subagents  []SubagentLink `json:"subagents,omitempty"`
}

func projectEvent(id string, records []Event) (TimelineEvent, error) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].Origin != records[j].Origin {
			return records[i].Origin == "import"
		}
		return records[i].ID < records[j].ID
	})
	out := TimelineEvent{ID: id, Session: records[0].Session, Source: records[0].Source, Time: records[0].Time, Signal: records[0].Signal, Records: records, Origins: []string{}}
	for i, e := range records {
		d, err := decodeDocument(e.Raw)
		if err != nil {
			return out, err
		}
		p := describe(e, d)
		if i == 0 {
			out.Detail = p
		} else {
			out.Detail = combineDetails(out.Detail, p, records[0].Origin, e.Origin)
		}
		out.Origins = append(out.Origins, e.Origin)
		if validEventTime(e.Time) && (!validEventTime(out.Time) || e.Time < out.Time) {
			out.Time = e.Time
		}
		if out.Trace == "" && e.Trace != "" {
			out.Trace = e.Trace
			out.Span = e.Span
			out.ParentSpan = e.ParentSpan
			out.Signal = e.Signal
		}
	}
	return out, nil
}
func validEventTime(s string) bool { return s != "" && s != "1970-01-01T00:00:00.000000000Z" }
func combineDetails(base, other Detail, baseOrigin, otherOrigin string) Detail {
	conflict := func(field string, a, b any) {
		if !present(a) || !present(b) || display(a) == display(b) {
			return
		}
		base.Conflicts = append(base.Conflicts, Conflict{field, []ConflictValue{{baseOrigin, display(a)}, {otherOrigin, display(b)}}})
	}
	base.Error = base.Error || other.Error
	if base.Kind == "tool" && base.Title == base.Name && other.Title != "" && other.Title != other.Name {
		base.Title = other.Title
	}
	for _, b := range other.Blocks {
		found := -1
		for j, old := range base.Blocks {
			if old.Label == b.Label {
				found = j
				break
			}
		}
		if found < 0 {
			base.Blocks = append(base.Blocks, b)
			continue
		}
		old := base.Blocks[found]
		if old.Text == "Content not exported" || old.Text == "Tool content not exported" {
			base.Blocks[found] = b
			continue
		}
		if b.Text != "Content not exported" && b.Text != "Tool content not exported" {
			conflict("Content: "+b.Label, old.Text, b.Text)
		}
	}
	for _, f := range other.Fields {
		found := false
		for _, old := range base.Fields {
			if old[0] == f[0] {
				found = true
				conflict(f[0], old[1], f[1])
				break
			}
		}
		if !found {
			base.Fields = append(base.Fields, f)
		}
	}
	for k, v := range other.Attributes {
		if old := base.Attributes[k]; present(old) {
			if k != "agentlog.origin" {
				conflict("Attribute: "+k, old, v)
			}
		} else {
			base.Attributes[k] = v
		}
	}
	base.Attributes["agentlog.origin"] = []string{baseOrigin, otherOrigin}
	for _, pair := range []struct {
		name  string
		dest  *any
		value any
	}{
		{"Model", &base.Model, other.Model}, {"Host", &base.Host, other.Host}, {"Working directory", &base.Cwd, other.Cwd}, {"Project", &base.Project, other.Project}, {"Tags", &base.Tags, other.Tags},
	} {
		if !present(*pair.dest) {
			*pair.dest = pair.value
		} else {
			conflict(pair.name, *pair.dest, pair.value)
		}
	}
	return base
}
