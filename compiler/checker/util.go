package checker

import (
	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/format"
)

// exprString renders an expression for diagnostics: bodies elided.
func exprString(e ast.Expr) string { return format.Short(e) }

// durationString prints a duration literal in the largest exact unit.
func durationString(ns int64) string { return format.Duration(ns) }

// typeExprString renders a syntactic type.
func typeExprString(t ast.TypeExpr) string { return format.Type(t) }

// patString renders a pattern back to source form.
func patString(p ast.Pattern) string { return format.Pattern(p) }

// ExprString renders an expression back to source form; the emitter uses it
// for contract and scenario step text.
func ExprString(e ast.Expr) string { return exprString(e) }

// TypeExprString renders a syntactic type back to source form.
func TypeExprString(t ast.TypeExpr) string { return typeExprString(t) }

// PatString renders a pattern back to source form.
func PatString(p ast.Pattern) string { return patString(p) }

// DurationString prints a duration literal in the largest exact unit.
func DurationString(ns int64) string { return durationString(ns) }
