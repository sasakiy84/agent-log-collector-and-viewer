package app

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type skillRead struct {
	SkillEvidence
	Certain bool `json:"certain"`
	Nested  bool `json:"nested,omitempty"`
}

// Parse syntax, never run shell code. Certain means a zero exit status for
// this command establishes that this particular read ran successfully.
func shellSkillReads(command, cwd string, depth int) []skillRead {
	if depth > 4 || len(command) > 64<<10 {
		return nil
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	var reads []skillRead
	var stmt func(*syntax.Stmt, string, bool) string
	var list func([]*syntax.Stmt, string, bool) string
	list = func(stmts []*syntax.Stmt, dir string, certain bool) string {
		for i, s := range stmts {
			dir = stmt(s, dir, certain && i == len(stmts)-1)
		}
		return dir
	}
	stmt = func(s *syntax.Stmt, dir string, certain bool) string {
		certain = certain && !s.Negated && !s.Background && len(s.Redirs) == 0
		switch c := s.Cmd.(type) {
		case *syntax.BinaryCmd:
			left, right := false, false
			switch c.Op {
			case syntax.AndStmt:
				left, right = certain, certain
			case syntax.OrStmt:
			case syntax.Pipe, syntax.PipeAll:
				right = certain
			}
			next := stmt(c.X, dir, left)
			if c.Op != syntax.AndStmt {
				next = dir
			}
			return stmt(c.Y, next, right)
		case *syntax.Block:
			return list(c.Stmts, dir, certain)
		case *syntax.Subshell:
			list(c.Stmts, dir, certain)
		case *syntax.CallExpr:
			words := []string{}
			for _, word := range c.Args {
				value, ok := shellLiteral(word)
				if !ok {
					return dir
				}
				words = append(words, value)
			}
			if len(words) == 0 {
				return dir
			}
			name := strings.ToLower(path.Base(words[0]))
			if name == "cd" && len(words) == 2 {
				if !certain {
					return ""
				}
				return resolveSkillPath(dir, words[1])
			}
			if name == "bash" || name == "sh" || name == "zsh" {
				for i, w := range words[1:] {
					if strings.HasPrefix(w, "-") && strings.Contains(w, "c") && i+2 < len(words) {
						nested := shellSkillReads(words[i+2], dir, depth+1)
						if !certain {
							for j := range nested {
								nested[j].Certain = false
							}
						}
						reads = append(reads, nested...)
						break
					}
				}
				return dir
			}
			switch name {
			case "cat", "head", "tail", "sed", "bat", "less", "more", "get-content":
			default:
				return dir
			}
			if name == "sed" {
				for _, w := range words[1:] {
					if strings.HasPrefix(w, "-i") || strings.HasPrefix(w, "--in-place") {
						return dir
					}
				}
			}
			for _, w := range words[1:] {
				if strings.HasPrefix(w, "-") {
					continue
				}
				f := skillFromPath(resolveSkillPath(dir, w))
				if f.Name != "" {
					reads = append(reads, skillRead{SkillEvidence: f, Certain: certain})
				}
			}
		}
		return dir
	}
	list(file.Stmts, cwd, true)
	return reads
}

func shellLiteral(w *syntax.Word) (string, bool) {
	var out strings.Builder
	var parts func([]syntax.WordPart) bool
	parts = func(values []syntax.WordPart) bool {
		for _, p := range values {
			switch p := p.(type) {
			case *syntax.Lit:
				out.WriteString(p.Value)
			case *syntax.SglQuoted:
				out.WriteString(p.Value)
			case *syntax.DblQuoted:
				if !parts(p.Parts) {
					return false
				}
			case *syntax.ParamExp:
				if p.Param == nil || (p.Param.Value != "HOME" && p.Param.Value != "PWD") || p.Exp != nil || p.Slice != nil {
					return false
				}
				out.WriteString("$" + p.Param.Value)
			default:
				return false
			}
		}
		return true
	}
	if !parts(w.Parts) {
		return "", false
	}
	return out.String(), true
}
func resolveSkillPath(cwd, p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "$") && !strings.HasPrefix(p, "~") {
		return path.Join(cwd, p)
	}
	return path.Clean(p)
}
