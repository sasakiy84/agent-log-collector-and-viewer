package app

import (
	"encoding/xml"
	"golang.org/x/text/unicode/norm"
	"regexp"
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
	return SkillEvidence{Name: name, Scope: scope, Evidence: "reference", Path: path}
}

var shellSkillWords = regexp.MustCompile(`"(?:\\.|[^"\\])*"|'[^']*'|[^\s"']+`)

// Conservative evidence only: direct read commands, no shell control flow,
// substitutions or redirections. This never claims that a skill was invoked.
func skillReadPaths(command string) []string {
	if strings.ContainsAny(command, "\r\n;&|<>`#") || strings.Contains(command, "$(") {
		return nil
	}
	matches := shellSkillWords.FindAllStringIndex(command, -1)
	words := []string{}
	end := 0
	for _, m := range matches {
		if strings.TrimSpace(command[end:m[0]]) != "" {
			return nil
		}
		word := command[m[0]:m[1]]
		if strings.HasPrefix(word, "\"") || strings.HasPrefix(word, "'") {
			word = word[1 : len(word)-1]
		} else if strings.Contains(word, "\\") {
			return nil
		}
		words = append(words, word)
		end = m[1]
	}
	if strings.TrimSpace(command[end:]) != "" || len(words) == 0 {
		return nil
	}
	name := words[0]
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	switch strings.ToLower(name) {
	case "cat", "head", "tail", "sed", "bat", "less", "more", "get-content":
	default:
		return nil
	}
	if name == "sed" {
		for _, word := range words[1:] {
			if strings.HasPrefix(word, "-i") || strings.HasPrefix(word, "--in-place") {
				return nil
			}
		}
	}
	return words[1:]
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
	if d.Kind == "tool" {
		name := strings.ToLower(d.Title)
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		if name == "skill" {
			params := obj(decoded(d.Attributes["tool_parameters"]))
			input := obj(decoded(firstValue(d.Attributes, "gen_ai.tool.call.arguments", "tool_input", "arguments")))
			skill := firstValue(d.Attributes, "skill_name")
			if !present(skill) {
				skill = firstValue(params, "skill_name")
			}
			if !present(skill) {
				skill = firstValue(input, "skill", "name")
			}
			add(SkillEvidence{Name: skillString(skill, 256), Plugin: skillString(d.Attributes["plugin.name"], 256), Evidence: "reference"})
		}
		for _, b := range d.Blocks {
			if b.Label == "Command" {
				for _, path := range skillReadPaths(b.Text) {
					add(skillFromPath(path))
				}
			}
			if b.Label == "File" {
				switch name {
				case "read", "read_file", "read_text", "get_file_contents", "view_file", "view":
					add(skillFromPath(b.Text))
				}
			}
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
				add(f)
			}
		}
	}
	return refs
}
