package app

import (
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

// Only literal tool arguments are inspected. Branches can supply candidates,
// but cannot establish execution; declarations and dynamic expressions do not.
func scriptSkillReads(code, cwd string) []skillRead {
	if len(code) > 64<<10 {
		return nil
	}
	program, err := parser.ParseFile(nil, "", "(async function(){\n"+code+"\n})", 0)
	if err != nil || len(program.Body) != 1 {
		return nil
	}
	expr, ok := program.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return nil
	}
	fn, ok := expr.Expression.(*ast.FunctionLiteral)
	if !ok {
		return nil
	}
	var reads []skillRead
	toolCalls := 0
	var expression func(ast.Expression, bool)
	var statements func([]ast.Statement, bool)
	expression = func(e ast.Expression, certain bool) {
		switch e := e.(type) {
		case *ast.AwaitExpression:
			expression(e.Argument, certain)
		case *ast.AssignExpression:
			expression(e.Right, certain)
		case *ast.SequenceExpression:
			for _, v := range e.Sequence {
				expression(v, certain)
			}
		case *ast.ArrayLiteral:
			for _, v := range e.Value {
				expression(v, certain)
			}
		case *ast.ConditionalExpression:
			expression(e.Consequent, false)
			expression(e.Alternate, false)
		case *ast.CallExpression:
			name := scriptName(e.Callee)
			if strings.HasPrefix(name, "tools.") {
				toolCalls++
				if len(e.ArgumentList) == 0 {
					return
				}
				if input, ok := scriptObject(e.ArgumentList[0]); ok {
					found := toolSkillReads(strings.TrimPrefix(name, "tools."), input, cwd, false)
					for i := range found {
						found[i].Certain = found[i].Certain && certain
						found[i].Nested = true
					}
					reads = append(reads, found...)
				}
				return
			}
			if name == "text" || name == "image" || name == "Promise.all" || name == "Promise.allSettled" {
				for _, v := range e.ArgumentList {
					expression(v, certain)
				}
			}
		}
	}
	statements = func(body []ast.Statement, certain bool) {
		for _, s := range body {
			switch s := s.(type) {
			case *ast.ExpressionStatement:
				expression(s.Expression, certain)
			case *ast.VariableStatement:
				for _, v := range s.List {
					expression(v.Initializer, certain)
				}
			case *ast.LexicalDeclaration:
				for _, v := range s.List {
					expression(v.Initializer, certain)
				}
			case *ast.ReturnStatement:
				expression(s.Argument, certain)
				return
			case *ast.BlockStatement:
				statements(s.List, certain)
			case *ast.IfStatement:
				statements([]ast.Statement{s.Consequent}, false)
				if s.Alternate != nil {
					statements([]ast.Statement{s.Alternate}, false)
				}
				certain = false
			}
		}
	}
	statements(fn.Body.List, true)
	if toolCalls != 1 {
		for i := range reads {
			reads[i].Certain = false
		}
	}
	return reads
}

func scriptName(e ast.Expression) string {
	switch e := e.(type) {
	case *ast.Identifier:
		return e.Name.String()
	case *ast.DotExpression:
		return scriptName(e.Left) + "." + e.Identifier.Name.String()
	}
	return ""
}
func scriptObject(e ast.Expression) (map[string]any, bool) {
	o, ok := e.(*ast.ObjectLiteral)
	if !ok {
		return nil, false
	}
	out := map[string]any{}
	for _, p := range o.Value {
		p, ok := p.(*ast.PropertyKeyed)
		if !ok || p.Computed {
			return nil, false
		}
		if p.Kind != ast.PropertyKindValue {
			return nil, false
		}
		key := scriptName(p.Key)
		if s, ok := p.Key.(*ast.StringLiteral); ok {
			key = s.Value.String()
		}
		if _, exists := out[key]; exists {
			return nil, false
		}
		switch v := p.Value.(type) {
		case *ast.StringLiteral:
			out[key] = v.Value.String()
		case *ast.TemplateLiteral:
			if len(v.Expressions) != 0 || len(v.Elements) != 1 {
				return nil, false
			}
			out[key] = v.Elements[0].Parsed.String()
		default:
			continue
		}
	}
	return out, true
}
