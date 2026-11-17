package app

import (
	"encoding/xml"
	"golang.org/x/text/unicode/norm"
	"strings"
)

func skillSearch(s string) string { return strings.ToLower(norm.NFKC.String(s)) }
func skillString(v any, max int) string {
	s, ok := v.(string)
	if !ok || len(s) > max {
		return ""
	}
	return strings.TrimSpace(s)
}
func skillFromPath(path string) SkillEvidence {
	path = strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(strings.TrimRight(path, "/"), "/")
	if len(parts) < 2 || !strings.EqualFold(parts[len(parts)-1], "SKILL.md") {
		return SkillEvidence{}
	}
	name := parts[len(parts)-2]
	if name == "" || name == "." || name == ".." || len(name) > 256 || len(path) > 32768 {
		return SkillEvidence{}
	}
	scope := ""
	if strings.Contains(path, "/.system/") {
		scope = "system"
	}
	return SkillEvidence{Name: name, Scope: scope, Evidence: "pending", Path: path}
}

func extractedSkills(e Event, d Detail) []SkillEvidence {
	if (e.Source != "codex" && e.Source != "claude" && e.Source != "copilot") || strings.HasPrefix(e.Session, "trace:") || strings.HasPrefix(e.Session, "unassigned:") {
		return nil
	}
	if d.Name == "codex.skill_invocation" {
		name := skillString(d.Attributes["skill.name"], 256)
		if name == "" {
			return nil
		}
		return []SkillEvidence{{Name: name, Plugin: skillString(d.Attributes["skill.plugin_id"], 256), Scope: skillString(d.Attributes["skill.scope"], 64), Mode: skillString(d.Attributes["skill.invocation_type"], 64), Evidence: "reported"}}
	}
	refs := append([]SkillEvidence{}, d.SkillFacts...)
	if e.Source == "claude" && eventSuffix(d.Name) == "skill_activated" {
		if name := skillString(d.Attributes["skill.name"], 256); name != "" {
			return []SkillEvidence{{Name: name, Plugin: skillString(d.Attributes["plugin.name"], 256), Scope: skillString(d.Attributes["skill.source"], 64), Mode: skillString(d.Attributes["invocation_trigger"], 64), Evidence: "reported", Instance: "activation"}}
		}
	}
	seen := map[string]bool{}
	add := func(f SkillEvidence) {
		if f.Name != "" && !seen[f.Name] && len(refs) < 16 {
			seen[f.Name] = true
			refs = append(refs, f)
		}
	}
	if d.Kind == "user" {
		for _, b := range d.Blocks {
			if !strings.HasPrefix(strings.TrimSpace(b.Text), "<skill>") {
				continue
			}
			decoder := xml.NewDecoder(strings.NewReader(strings.TrimSpace(b.Text)))
			name, path := "", ""
			for {
				token, err := decoder.Token()
				if err != nil {
					break
				}
				if tag, ok := token.(xml.StartElement); ok {
					switch tag.Name.Local {
					case "name":
						decoder.DecodeElement(&name, &tag)
					case "path":
						decoder.DecodeElement(&path, &tag)
					}
				}
				if name != "" && path != "" {
					break
				}
			}
			f := skillFromPath(path)
			if f.Name != "" && len(name) <= 256 && name != "" {
				f.Name = name
			}
			if f.Name != "" && path != "" {
				f.Evidence = "injection"
				add(f)
			}
		}
	}
	return refs
}
