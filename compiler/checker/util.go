package checker

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
)

// exprString renders an expression back to source form for diagnostics.
// It is not a formatter: it is only required to be readable and unambiguous.
func exprString(e ast.Expr) string {
	switch e := e.(type) {
	case nil:
		return ""
	case *ast.IntLit:
		return strconv.FormatInt(e.V, 10)
	case *ast.FloatLit:
		return strconv.FormatFloat(e.V, 'g', -1, 64)
	case *ast.BoolLit:
		if e.V {
			return "true"
		}
		return "false"
	case *ast.CharLit:
		return strconv.QuoteRune(e.V)
	case *ast.BytesLit:
		return "b" + strconv.Quote(string(e.V))
	case *ast.DurationLit:
		return durationString(e.Ns)
	case *ast.StrLit:
		var b strings.Builder
		b.WriteByte('"')
		for _, p := range e.Parts {
			if p.Expr != nil {
				b.WriteString("{" + exprString(p.Expr) + "}")
			} else {
				b.WriteString(strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "{", "{{", "}", "}}").Replace(p.Lit))
			}
		}
		b.WriteByte('"')
		return b.String()
	case *ast.Ident:
		return e.Name
	case *ast.SelfExpr:
		return "self"
	case *ast.FieldExpr:
		return exprString(e.X) + "." + e.Name
	case *ast.CallExpr:
		var b strings.Builder
		b.WriteString(exprString(e.Fn))
		if len(e.TypeArgs) > 0 {
			b.WriteString("::<")
			for i, t := range e.TypeArgs {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(typeExprString(t))
			}
			b.WriteString(">")
		}
		b.WriteString("(")
		for i, a := range e.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(exprString(a))
		}
		b.WriteString(")")
		return b.String()
	case *ast.UnaryExpr:
		return e.Op + exprString(e.X)
	case *ast.BinaryExpr:
		return exprString(e.L) + " " + e.Op + " " + exprString(e.R)
	case *ast.TryExpr:
		return exprString(e.X) + "?"
	case *ast.Block:
		if len(e.Stmts) == 0 && e.Tail != nil {
			return "{ " + exprString(e.Tail) + " }"
		}
		return "{ … }"
	case *ast.IfExpr:
		s := "if " + exprString(e.Cond) + " " + exprString(e.Then)
		if e.Else != nil {
			s += " else " + exprString(e.Else)
		}
		return s
	case *ast.MatchExpr:
		return "match " + exprString(e.X) + " { … }"
	case *ast.WhileExpr:
		return "while " + exprString(e.Cond) + " { … }"
	case *ast.ForExpr:
		return "for " + patString(e.Pat) + " in " + exprString(e.Iter) + " { … }"
	case *ast.RangeExpr:
		op := ".."
		if e.Incl {
			op = "..="
		}
		return exprString(e.Lo) + op + exprString(e.Hi)
	case *ast.ReturnExpr:
		if e.X != nil {
			return "return " + exprString(e.X)
		}
		return "return"
	case *ast.BreakExpr:
		return "break"
	case *ast.ContinueExpr:
		return "continue"
	case *ast.Closure:
		var ps []string
		for _, p := range e.Params {
			if p.Type != nil {
				ps = append(ps, p.Name+": "+typeExprString(p.Type))
			} else {
				ps = append(ps, p.Name)
			}
		}
		s := "fn(" + strings.Join(ps, ", ") + ")"
		if e.Ret != nil {
			s += " -> " + typeExprString(e.Ret)
		}
		return s + " => " + exprString(e.Body)
	case *ast.RecordLit:
		var b strings.Builder
		b.WriteString(strings.Join(e.Path, "."))
		b.WriteString(" { ")
		if e.Base != nil {
			b.WriteString(".." + exprString(e.Base))
			if len(e.Fields) > 0 {
				b.WriteString(", ")
			}
		}
		for i, f := range e.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			if id, ok := f.Value.(*ast.Ident); ok && id.Name == f.Name {
				b.WriteString(f.Name)
			} else {
				b.WriteString(f.Name + ": " + exprString(f.Value))
			}
		}
		b.WriteString(" }")
		return b.String()
	case *ast.TupleLit:
		var xs []string
		for _, x := range e.Elems {
			xs = append(xs, exprString(x))
		}
		if len(xs) == 1 {
			return "(" + xs[0] + ",)"
		}
		return "(" + strings.Join(xs, ", ") + ")"
	case *ast.ListLit:
		var xs []string
		for _, x := range e.Elems {
			xs = append(xs, exprString(x))
		}
		return "[" + strings.Join(xs, ", ") + "]"
	case *ast.SpawnExpr:
		return "spawn { … }"
	case *ast.TaskExpr:
		return "task " + exprString(e.X)
	case *ast.LlmExpr:
		return "llm<" + e.Role + ">(" + exprString(e.Prompt) + ", " + exprString(e.Input) + ")"
	case *ast.DecideExpr:
		return "decide<" + e.Role + ">(" + exprString(e.Input) + ")"
	case *ast.ApproveExpr:
		return "approve::<" + typeExprString(e.Type) + ">(" + exprString(e.Review) + ")"
	case *ast.OldExpr:
		return "old(" + exprString(e.X) + ")"
	case *ast.RetExpr:
		return "ret"
	case *ast.QuantExpr:
		q := "exists"
		if e.Forall {
			q = "forall"
		}
		return q + " " + e.Var + " in " + exprString(e.Coll) + ": " + exprString(e.Body)
	case *ast.AnyExpr:
		s := "any " + typeExprString(e.Type)
		if e.Via != nil {
			s += " via " + exprString(e.Via)
		}
		return s
	}
	return fmt.Sprintf("<%T>", e)
}

// durationString prints a duration literal in the largest exact unit.
func durationString(ns int64) string {
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

// typeExprString renders a syntactic type.
func typeExprString(t ast.TypeExpr) string {
	switch t := t.(type) {
	case nil:
		return "()"
	case *ast.TypePath:
		s := strings.Join(t.Path, ".")
		if len(t.Args) > 0 {
			var as []string
			for _, a := range t.Args {
				as = append(as, typeExprString(a))
			}
			s += "<" + strings.Join(as, ", ") + ">"
		}
		return s
	case *ast.TupleType:
		var as []string
		for _, a := range t.Elems {
			as = append(as, typeExprString(a))
		}
		if len(as) == 1 {
			return "(" + as[0] + ",)"
		}
		return "(" + strings.Join(as, ", ") + ")"
	case *ast.FnType:
		var as []string
		for _, a := range t.Params {
			as = append(as, typeExprString(a))
		}
		s := "fn(" + strings.Join(as, ", ") + ")"
		if t.Ret != nil {
			s += " -> " + typeExprString(t.Ret)
		}
		if t.Effects != nil {
			s += " with " + EffectSetString(t.Effects)
		}
		return s
	}
	return fmt.Sprintf("<%T>", t)
}

// patString renders a pattern back to source form.
func patString(p ast.Pattern) string {
	switch p := p.(type) {
	case nil:
		return "_"
	case *ast.WildPat:
		return "_"
	case *ast.BindPat:
		return p.Name
	case *ast.LitPat:
		return exprString(p.Lit)
	case *ast.VariantPat:
		s := strings.Join(p.Path, ".")
		if p.Parens {
			var xs []string
			for _, x := range p.Positional {
				xs = append(xs, patString(x))
			}
			s += "(" + strings.Join(xs, ", ") + ")"
		} else if p.Braces {
			var xs []string
			for _, f := range p.Named {
				if f.Name == ".." {
					xs = append(xs, "..")
					continue
				}
				if bp, ok := f.Pat.(*ast.BindPat); ok && bp.Name == f.Name {
					xs = append(xs, f.Name)
				} else {
					xs = append(xs, f.Name+": "+patString(f.Pat))
				}
			}
			s += " { " + strings.Join(xs, ", ") + " }"
		}
		return s
	case *ast.TuplePat:
		var xs []string
		for _, x := range p.Elems {
			xs = append(xs, patString(x))
		}
		if len(xs) == 1 {
			return "(" + xs[0] + ",)"
		}
		return "(" + strings.Join(xs, ", ") + ")"
	case *ast.RecordPat:
		var xs []string
		for _, f := range p.Fields {
			if f.Name == ".." {
				xs = append(xs, "..")
				continue
			}
			xs = append(xs, f.Name+": "+patString(f.Pat))
		}
		return strings.Join(p.Path, ".") + " { " + strings.Join(xs, ", ") + " }"
	case *ast.OrPat:
		var xs []string
		for _, x := range p.Alts {
			xs = append(xs, patString(x))
		}
		return strings.Join(xs, " | ")
	case *ast.AtPat:
		return p.Name + " @ " + patString(p.Pat)
	}
	return fmt.Sprintf("<%T>", p)
}

// ExprString renders an expression back to source form; the emitter uses it
// for contract and scenario step text.
func ExprString(e ast.Expr) string { return exprString(e) }

// TypeExprString renders a syntactic type back to source form.
func TypeExprString(t ast.TypeExpr) string { return typeExprString(t) }

// PatString renders a pattern back to source form.
func PatString(p ast.Pattern) string { return patString(p) }

// DurationString prints a duration literal in the largest exact unit.
func DurationString(ns int64) string { return durationString(ns) }
