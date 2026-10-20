// Package format prints the AST back to canonical jippitese source. Expr
// prints in full on one line with the minimal parentheses the precedence
// table of spec 04 rule 10 needs; Short abbreviates bodies for diagnostics.
// Interface files (spec 09 rule 7) use the full form, so two expressions
// print the same only when they parse the same.
package format

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
)

type printer struct{ short bool }

// Expr prints e in full on one line.
func Expr(e ast.Expr) string { return printer{}.expr(e) }

// Short prints e with block, match and loop bodies elided.
func Short(e ast.Expr) string { return printer{short: true}.expr(e) }

// Type prints a syntactic type.
func Type(t ast.TypeExpr) string { return printer{}.typ(t) }

// Pattern prints a pattern.
func Pattern(p ast.Pattern) string { return printer{}.pat(p) }

// Stmt prints a statement on one line.
func Stmt(s ast.Stmt) string { return printer{}.stmt(s) }

// Effects prints an effect set in canonical order.
func Effects(es []ast.Effect) string { return ast.EffectSetString(es) }

// Precedence levels (spec 04 rule 10), loosest first.
const (
	precLow   = iota // closures, return, quantifiers, if: extend rightwards
	precRange        // ..
	precOr           // ||
	precAnd          // &&
	precCmp          // == != < > <= >=
	precAdd          // + -
	precMul          // * / %
	precUnary        // ! -
	precPost         // calls, fields, ?, literals, braces
)

func prec(e ast.Expr) int {
	switch e := e.(type) {
	case *ast.RangeExpr:
		return precRange
	case *ast.BinaryExpr:
		switch e.Op {
		case "||":
			return precOr
		case "&&":
			return precAnd
		case "+", "-":
			return precAdd
		case "*", "/", "%":
			return precMul
		}
		return precCmp
	case *ast.UnaryExpr:
		return precUnary
	case *ast.Closure, *ast.ReturnExpr, *ast.QuantExpr, *ast.IfExpr, *ast.AnyExpr, *ast.TaskExpr:
		return precLow
	}
	return precPost
}

// operand prints e, parenthesised when it binds looser than min.
func (p printer) operand(e ast.Expr, min int) string {
	s := p.expr(e)
	if prec(e) < min {
		return "(" + s + ")"
	}
	return s
}

func (p printer) expr(e ast.Expr) string {
	switch e := e.(type) {
	case nil:
		return ""
	case *ast.IntLit:
		if e.Raw != "" {
			return e.Raw
		}
		return strconv.FormatInt(e.V, 10)
	case *ast.FloatLit:
		if e.Raw != "" {
			return e.Raw
		}
		s := strconv.FormatFloat(e.V, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eEn") {
			s += ".0"
		}
		return s
	case *ast.BoolLit:
		return strconv.FormatBool(e.V)
	case *ast.CharLit:
		return strconv.QuoteRune(e.V)
	case *ast.BytesLit:
		return "b" + strconv.Quote(string(e.V))
	case *ast.DurationLit:
		if e.Raw != "" {
			return e.Raw
		}
		return Duration(e.Ns)
	case *ast.StrLit:
		var b strings.Builder
		b.WriteByte('"')
		for _, part := range e.Parts {
			if part.Expr != nil {
				b.WriteString("{" + p.expr(part.Expr) + "}")
			} else {
				b.WriteString(strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\t", "\\t", "{", "{{", "}", "}}").Replace(part.Lit))
			}
		}
		b.WriteByte('"')
		return b.String()
	case *ast.Ident:
		return e.Name
	case *ast.SelfExpr:
		return "self"
	case *ast.FieldExpr:
		return p.operand(e.X, precPost) + "." + e.Name
	case *ast.CallExpr:
		if e.Sugar != "" && len(e.Args) == 1 {
			return e.Sugar + " " + p.expr(e.Args[0])
		}
		var b strings.Builder
		b.WriteString(p.operand(e.Fn, precPost))
		if len(e.TypeArgs) > 0 {
			b.WriteString("::<" + p.types(e.TypeArgs) + ">")
		}
		b.WriteString("(" + p.exprs(e.Args) + ")")
		return b.String()
	case *ast.UnaryExpr:
		return e.Op + p.operand(e.X, precUnary)
	case *ast.BinaryExpr:
		pr := prec(e)
		r := pr + 1
		l := pr
		if pr == precCmp {
			l = pr + 1
		}
		return p.operand(e.L, l) + " " + e.Op + " " + p.operand(e.R, r)
	case *ast.TryExpr:
		return p.operand(e.X, precPost) + "?"
	case *ast.Block:
		return p.block(e)
	case *ast.IfExpr:
		s := "if " + p.expr(e.Cond) + " " + p.block(e.Then)
		if e.Else != nil {
			s += " else " + p.expr(e.Else)
		}
		return s
	case *ast.MatchExpr:
		if p.short {
			return "match " + p.expr(e.X) + " { … }"
		}
		var arms []string
		for _, a := range e.Arms {
			s := p.pat(a.Pat)
			if a.Guard != nil {
				s += " if " + p.expr(a.Guard)
			}
			arms = append(arms, s+" => "+p.expr(a.Body))
		}
		return "match " + p.expr(e.X) + " { " + strings.Join(arms, ", ") + " }"
	case *ast.WhileExpr:
		return "while " + p.expr(e.Cond) + " " + p.block(e.Body)
	case *ast.ForExpr:
		return "for " + p.pat(e.Pat) + " in " + p.expr(e.Iter) + " " + p.block(e.Body)
	case *ast.RangeExpr:
		op := ".."
		if e.Incl {
			op = "..="
		}
		return p.operand(e.Lo, precOr) + op + p.operand(e.Hi, precOr)
	case *ast.ReturnExpr:
		if e.X != nil {
			return "return " + p.expr(e.X)
		}
		return "return"
	case *ast.BreakExpr:
		return "break"
	case *ast.ContinueExpr:
		return "continue"
	case *ast.Closure:
		var ps []string
		for _, prm := range e.Params {
			if prm.Type != nil {
				ps = append(ps, prm.Name+": "+p.typ(prm.Type))
			} else {
				ps = append(ps, prm.Name)
			}
		}
		s := "fn(" + strings.Join(ps, ", ") + ")"
		if e.Ret != nil {
			s += " -> " + p.typ(e.Ret)
		}
		return s + " => " + p.expr(e.Body)
	case *ast.RecordLit:
		var xs []string
		if e.Base != nil {
			xs = append(xs, ".."+p.expr(e.Base))
		}
		for _, f := range e.Fields {
			if id, ok := f.Value.(*ast.Ident); ok && id.Name == f.Name {
				xs = append(xs, f.Name)
			} else {
				xs = append(xs, f.Name+": "+p.expr(f.Value))
			}
		}
		if len(xs) == 0 {
			return strings.Join(e.Path, ".") + " {}"
		}
		return strings.Join(e.Path, ".") + " { " + strings.Join(xs, ", ") + " }"
	case *ast.TupleLit:
		if len(e.Elems) == 1 {
			return "(" + p.expr(e.Elems[0]) + ",)"
		}
		return "(" + p.exprs(e.Elems) + ")"
	case *ast.ListLit:
		return "[" + p.exprs(e.Elems) + "]"
	case *ast.SpawnExpr:
		return "spawn " + p.block(e.Body)
	case *ast.TaskExpr:
		return "task " + p.expr(e.X)
	case *ast.LlmExpr:
		return "llm<" + e.Role + ">(" + p.expr(e.Prompt) + ", " + p.expr(e.Input) + ")"
	case *ast.DecideExpr:
		return "decide<" + e.Role + ">(" + p.expr(e.Input) + ")"
	case *ast.ApproveExpr:
		return "approve::<" + p.typ(e.Type) + ">(" + p.expr(e.Review) + ")"
	case *ast.ToolsExpr:
		names := make([]string, len(e.Fns))
		for i, f := range e.Fns {
			names[i] = f.Name
		}
		return "tools [" + strings.Join(names, ", ") + "]"
	case *ast.OldExpr:
		return "old(" + p.expr(e.X) + ")"
	case *ast.RetExpr:
		return "ret"
	case *ast.QuantExpr:
		q := "exists"
		if e.Forall {
			q = "forall"
		}
		return q + " " + e.Var + " in " + p.operand(e.Coll, precRange) + ": " + p.expr(e.Body)
	case *ast.AnyExpr:
		s := "any " + p.typ(e.Type)
		if e.Via != nil {
			s += " via " + p.expr(e.Via)
		}
		if e.Where != nil {
			s += " where " + p.expr(e.Where)
		}
		return s
	}
	return fmt.Sprintf("<%T>", e)
}

func (p printer) exprs(es []ast.Expr) string {
	xs := make([]string, len(es))
	for i, x := range es {
		xs[i] = p.expr(x)
	}
	return strings.Join(xs, ", ")
}

func (p printer) block(b *ast.Block) string {
	if b == nil {
		return "{}"
	}
	if len(b.Stmts) == 0 && b.Tail == nil {
		return "{}"
	}
	if p.short && len(b.Stmts) > 0 {
		return "{ … }"
	}
	var xs []string
	for _, s := range b.Stmts {
		xs = append(xs, p.stmt(s))
	}
	if b.Tail != nil {
		xs = append(xs, p.expr(b.Tail))
	}
	return "{ " + strings.Join(xs, "; ") + " }"
}

func (p printer) stmt(s ast.Stmt) string {
	switch s := s.(type) {
	case *ast.LetStmt:
		t := ""
		if s.Type != nil {
			t = ": " + p.typ(s.Type)
		}
		return "let " + p.pat(s.Pat) + t + " = " + p.expr(s.Value)
	case *ast.VarStmt:
		t := ""
		if s.Type != nil {
			t = ": " + p.typ(s.Type)
		}
		return "var " + s.Name + t + " = " + p.expr(s.Value)
	case *ast.AssignStmt:
		return s.Target.Name + " " + s.Op + " " + p.expr(s.Value)
	case *ast.ExprStmt:
		return p.expr(s.X)
	}
	return fmt.Sprintf("<%T>", s)
}

func (p printer) types(ts []ast.TypeExpr) string {
	xs := make([]string, len(ts))
	for i, t := range ts {
		xs[i] = p.typ(t)
	}
	return strings.Join(xs, ", ")
}

func (p printer) typ(t ast.TypeExpr) string {
	switch t := t.(type) {
	case nil:
		return "()"
	case *ast.TypePath:
		s := strings.Join(t.Path, ".")
		if len(t.Args) > 0 {
			s += "<" + p.types(t.Args) + ">"
		}
		return s
	case *ast.TupleType:
		if len(t.Elems) == 1 {
			return "(" + p.typ(t.Elems[0]) + ",)"
		}
		return "(" + p.types(t.Elems) + ")"
	case *ast.FnType:
		s := "fn(" + p.types(t.Params) + ")"
		if t.Ret != nil {
			s += " -> " + p.typ(t.Ret)
		}
		if t.Effects != nil {
			s += " with " + Effects(t.Effects)
		}
		return s
	}
	return fmt.Sprintf("<%T>", t)
}

func (p printer) pat(pt ast.Pattern) string {
	switch x := pt.(type) {
	case nil, *ast.WildPat:
		return "_"
	case *ast.BindPat:
		return x.Name
	case *ast.LitPat:
		return p.expr(x.Lit)
	case *ast.VariantPat:
		s := strings.Join(x.Path, ".")
		if x.Parens {
			var xs []string
			for _, sp := range x.Positional {
				xs = append(xs, p.pat(sp))
			}
			s += "(" + strings.Join(xs, ", ") + ")"
		} else if x.Braces {
			s += " { " + p.fieldPats(x.Named) + " }"
		}
		return s
	case *ast.TuplePat:
		var xs []string
		for _, sp := range x.Elems {
			xs = append(xs, p.pat(sp))
		}
		if len(xs) == 1 {
			return "(" + xs[0] + ",)"
		}
		return "(" + strings.Join(xs, ", ") + ")"
	case *ast.RecordPat:
		return strings.Join(x.Path, ".") + " { " + p.fieldPats(x.Fields) + " }"
	case *ast.OrPat:
		var xs []string
		for _, sp := range x.Alts {
			xs = append(xs, p.pat(sp))
		}
		return strings.Join(xs, " | ")
	case *ast.AtPat:
		return x.Name + " @ " + p.pat(x.Pat)
	}
	return fmt.Sprintf("<%T>", pt)
}

func (p printer) fieldPats(fs []ast.FieldPat) string {
	var xs []string
	for _, f := range fs {
		switch {
		case f.Name == "..":
			xs = append(xs, "..")
		case isBind(f.Pat, f.Name):
			xs = append(xs, f.Name)
		default:
			xs = append(xs, f.Name+": "+p.pat(f.Pat))
		}
	}
	return strings.Join(xs, ", ")
}

func isBind(pt ast.Pattern, name string) bool {
	b, ok := pt.(*ast.BindPat)
	return ok && b.Name == name
}

// Duration prints a duration literal in the largest exact unit the
// lexer accepts (`h` at most).
func Duration(ns int64) string {
	if ns == 0 {
		return "0s"
	}
	units := []struct {
		n    int64
		name string
	}{{3600e9, "h"}, {60e9, "m"}, {1e9, "s"}, {1e6, "ms"}, {1e3, "us"}, {1, "ns"}}
	for _, u := range units {
		if ns%u.n == 0 {
			return strconv.FormatInt(ns/u.n, 10) + u.name
		}
	}
	return strconv.FormatInt(ns, 10) + "ns"
}
