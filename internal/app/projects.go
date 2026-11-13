package app

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

// Request labels are defaults for resource attributes. Explicit resource values win.
func applyProjectQuery(root map[string]any, signal string, q url.Values) error {
	project := q.Get("project")
	tags := q["tag"]
	if len(q["project"]) > 1 {
		return fmt.Errorf("specify one project")
	}
	for _, v := range append([]string{project}, tags...) {
		if !utf8.ValidString(v) || len(v) > 256 || strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("project and tag values must be UTF-8 strings of at most 256 bytes")
		}
	}
	key := map[string]string{"logs": "resourceLogs", "traces": "resourceSpans", "metrics": "resourceMetrics"}[signal]
	for _, v := range arr(root[key]) {
		r := obj(v)
		resource := obj(r["resource"])
		if resource == nil {
			resource = map[string]any{}
			r["resource"] = resource
		}
		at := arr(resource["attributes"])
		foundProject, foundTags := false, false
		for _, v := range at {
			a := obj(v)
			switch str(a["key"]) {
			case "service.namespace", "project.name":
				foundProject = true
			case "project.tags":
				foundTags = true
			}
		}
		if project != "" && !foundProject {
			at = append(at, map[string]any{"key": "service.namespace", "value": map[string]any{"stringValue": project}})
		}
		if len(tags) > 0 && !foundTags {
			at = append(at, map[string]any{"key": "project.tags", "value": map[string]any{"stringValue": strings.Join(uniqueTags(tags), ",")}})
		}
		resource["attributes"] = at
	}
	return nil
}
func uniqueTags(tags []string) []string {
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			seen[tag] = true
		}
	}
	out := []string{}
	for tag := range seen {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}
