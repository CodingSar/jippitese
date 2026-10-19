// Package checker resolves names, infers and checks types, checks effects
// and contracts, and verifies behaviour impls for the v0 subset (spec 02,
// 03, 04, 05, 06, 07, 08, 12). Its output, a *Program with side tables, is
// what the emitter lowers to Go.
package checker

import (
	"fmt"
	"sort"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
)

// Type is a checked type. Types are compared after Resolve.
type Type interface{ isType() }

// TCon is a type constructor applied to arguments: Int, List<T>, Result<T, E>.
type TCon struct {
	Info *TypeInfo
	Args []Type
}

// TVar is an inference variable.
type TVar struct {
	id      int
	ref     Type
	numeric bool // only ever unified with Int or Float; defaults to Int
}

// TParam is a generic parameter in scope of a declaration.
type TParam struct {
	Name   string
	Bounds []string
	id     int
}

// TFn is a function type. Effects is the declared set; Explicit says a
// `with {…}` clause was written (an explicit empty set is a pure bound).
type TFn struct {
	Params   []Type
	Ret      Type
	Effects  []ast.Effect
	Explicit bool
}

// TTuple is a tuple; the empty tuple is unit.
type TTuple struct{ Elems []Type }

func (*TCon) isType()   {}
func (*TVar) isType()   {}
func (*TParam) isType() {}
func (*TFn) isType()    {}
func (*TTuple) isType() {}

var unit = &TTuple{}

// TypeKind classifies a TypeInfo.
type TypeKind int

const (
	Prim TypeKind = iota
	Record
	Newtype
	Alias
	Enum
	Extern
)

var (
	tInt      = prim("Int")
	tFloat    = prim("Float")
	tBool     = prim("Bool")
	tStr      = prim("Str")
	tBytes    = prim("Bytes")
	tChar     = prim("Char")
	tDuration = prim("Duration")
	tRange    = &TypeInfo{Name: "Range", Mod: "prelude", Kind: Extern, Params: []*TParam{{Name: "T"}}}
)

func prim(name string) *TypeInfo {
	return &TypeInfo{Name: name, Mod: "prelude", Kind: Prim, Vis: ast.Pub}
}

func con(info *TypeInfo, args ...Type) *TCon { return &TCon{Info: info, Args: args} }

func (c *Checker) newVar() *TVar {
	c.nextVar++
	return &TVar{id: c.nextVar}
}

// Resolve follows inference variable links.
func Resolve(t Type) Type {
	for {
		v, ok := t.(*TVar)
		if !ok || v.ref == nil {
			return t
		}
		t = v.ref
	}
}

// Zonk substitutes every solved variable inside t.
func Zonk(t Type) Type {
	t = Resolve(t)
	switch t := t.(type) {
	case *TCon:
		if len(t.Args) == 0 {
			return t
		}
		args := make([]Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = Zonk(a)
		}
		return &TCon{Info: t.Info, Args: args}
	case *TFn:
		ps := make([]Type, len(t.Params))
		for i, p := range t.Params {
			ps[i] = Zonk(p)
		}
		return &TFn{Params: ps, Ret: Zonk(t.Ret), Effects: t.Effects, Explicit: t.Explicit}
	case *TTuple:
		if len(t.Elems) == 0 {
			return t
		}
		es := make([]Type, len(t.Elems))
		for i, e := range t.Elems {
			es[i] = Zonk(e)
		}
		return &TTuple{Elems: es}
	}
	return t
}

// ---- unification ----------------------------------------------------------

type unifier struct {
	trail []*TVar
}

func (u *unifier) mark() int { return len(u.trail) }

func (u *unifier) rollback(m int) {
	for i := len(u.trail) - 1; i >= m; i-- {
		u.trail[i].ref = nil
	}
	u.trail = u.trail[:m]
}

func (u *unifier) bind(v *TVar, t Type) bool {
	if occurs(v, t) {
		return false
	}
	if v.numeric {
		switch r := Resolve(t).(type) {
		case *TCon:
			if r.Info != tInt && r.Info != tFloat && r.Info != tDuration {
				return false
			}
		case *TVar:
			r.numeric = true
		default:
			return false
		}
	}
	v.ref = t
	u.trail = append(u.trail, v)
	return true
}

func occurs(v *TVar, t Type) bool {
	t = Resolve(t)
	switch t := t.(type) {
	case *TVar:
		return t == v
	case *TCon:
		for _, a := range t.Args {
			if occurs(v, a) {
				return true
			}
		}
	case *TFn:
		for _, a := range t.Params {
			if occurs(v, a) {
				return true
			}
		}
		return occurs(v, t.Ret)
	case *TTuple:
		for _, a := range t.Elems {
			if occurs(v, a) {
				return true
			}
		}
	}
	return false
}

// unify makes a and b equal, binding variables. Effects on function types
// are not part of equality. On failure the caller rolls back.
func (u *unifier) unify(a, b Type) bool {
	a, b = Resolve(a), Resolve(b)
	if a == b {
		return true
	}
	if va, ok := a.(*TVar); ok {
		if vb, ok := b.(*TVar); ok && vb == va {
			return true
		}
		return u.bind(va, b)
	}
	if vb, ok := b.(*TVar); ok {
		return u.bind(vb, a)
	}
	switch a := a.(type) {
	case *TCon:
		b, ok := b.(*TCon)
		if !ok || a.Info != b.Info || len(a.Args) != len(b.Args) {
			return false
		}
		for i := range a.Args {
			if !u.unify(a.Args[i], b.Args[i]) {
				return false
			}
		}
		return true
	case *TParam:
		b, ok := b.(*TParam)
		return ok && a == b
	case *TFn:
		b, ok := b.(*TFn)
		if !ok || len(a.Params) != len(b.Params) {
			return false
		}
		for i := range a.Params {
			if !u.unify(a.Params[i], b.Params[i]) {
				return false
			}
		}
		return u.unify(a.Ret, b.Ret)
	case *TTuple:
		b, ok := b.(*TTuple)
		if !ok || len(a.Elems) != len(b.Elems) {
			return false
		}
		for i := range a.Elems {
			if !u.unify(a.Elems[i], b.Elems[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// tryUnify unifies and rolls back on failure.
func (u *unifier) tryUnify(a, b Type) bool {
	m := u.mark()
	if u.unify(a, b) {
		return true
	}
	u.rollback(m)
	return false
}

// probe reports whether a and b could unify, without keeping any binding.
func (u *unifier) probe(a, b Type) bool {
	m := u.mark()
	ok := u.unify(a, b)
	u.rollback(m)
	return ok
}

// ---- substitution ---------------------------------------------------------

// subst replaces type parameters.
func subst(t Type, m map[*TParam]Type) Type {
	if len(m) == 0 {
		return t
	}
	t = Resolve(t)
	switch t := t.(type) {
	case *TParam:
		if r, ok := m[t]; ok {
			return r
		}
		return t
	case *TCon:
		if len(t.Args) == 0 {
			return t
		}
		args := make([]Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = subst(a, m)
		}
		return &TCon{Info: t.Info, Args: args}
	case *TFn:
		ps := make([]Type, len(t.Params))
		for i, p := range t.Params {
			ps[i] = subst(p, m)
		}
		return &TFn{Params: ps, Ret: subst(t.Ret, m), Effects: t.Effects, Explicit: t.Explicit}
	case *TTuple:
		if len(t.Elems) == 0 {
			return t
		}
		es := make([]Type, len(t.Elems))
		for i, e := range t.Elems {
			es[i] = subst(e, m)
		}
		return &TTuple{Elems: es}
	}
	return t
}

// instantiate replaces the given parameters with fresh variables.
func (c *Checker) instantiate(params []*TParam) (map[*TParam]Type, []Type) {
	m := map[*TParam]Type{}
	var vs []Type
	for _, p := range params {
		v := c.newVar()
		m[p] = v
		vs = append(vs, v)
	}
	return m, vs
}

// typeEqual is structural equality after resolution (used for impl
// conformance, where no inference should happen).
func typeEqual(a, b Type) bool {
	a, b = Resolve(a), Resolve(b)
	if a == b {
		return true
	}
	switch a := a.(type) {
	case *TCon:
		b, ok := b.(*TCon)
		if !ok || a.Info != b.Info || len(a.Args) != len(b.Args) {
			return false
		}
		for i := range a.Args {
			if !typeEqual(a.Args[i], b.Args[i]) {
				return false
			}
		}
		return true
	case *TParam:
		b, ok := b.(*TParam)
		return ok && a == b
	case *TFn:
		b, ok := b.(*TFn)
		if !ok || len(a.Params) != len(b.Params) {
			return false
		}
		for i := range a.Params {
			if !typeEqual(a.Params[i], b.Params[i]) {
				return false
			}
		}
		return typeEqual(a.Ret, b.Ret)
	case *TTuple:
		b, ok := b.(*TTuple)
		if !ok || len(a.Elems) != len(b.Elems) {
			return false
		}
		for i := range a.Elems {
			if !typeEqual(a.Elems[i], b.Elems[i]) {
				return false
			}
		}
		return true
	case *TVar:
		b, ok := b.(*TVar)
		return ok && a == b
	}
	return false
}

// hasVars reports whether an unsolved variable remains in t.
func hasVars(t Type) bool {
	t = Resolve(t)
	switch t := t.(type) {
	case *TVar:
		return true
	case *TCon:
		for _, a := range t.Args {
			if hasVars(a) {
				return true
			}
		}
	case *TFn:
		for _, a := range t.Params {
			if hasVars(a) {
				return true
			}
		}
		return hasVars(t.Ret)
	case *TTuple:
		for _, a := range t.Elems {
			if hasVars(a) {
				return true
			}
		}
	}
	return false
}

// defaultNumeric binds every numeric variable in t to Int.
func defaultNumeric(t Type) {
	t = Resolve(t)
	switch t := t.(type) {
	case *TVar:
		if t.numeric {
			t.ref = con(tInt)
		}
	case *TCon:
		for _, a := range t.Args {
			defaultNumeric(a)
		}
	case *TFn:
		for _, a := range t.Params {
			defaultNumeric(a)
		}
		defaultNumeric(t.Ret)
	case *TTuple:
		for _, a := range t.Elems {
			defaultNumeric(a)
		}
	}
}

// ---- printing -------------------------------------------------------------

// curMod is the module whose diagnostics are being printed; types from other
// user modules print qualified by the last segment of their module path.
var curMod *Module

func qualifiedName(info *TypeInfo) string {
	if info.Mod == "prelude" || info.Mod == "appcfg" || curMod == nil {
		return info.Name
	}
	if info.Mod == curMod.Path || (curMod.Parent != nil && info.Mod == curMod.Parent.Path) {
		return info.Name
	}
	parts := strings.Split(info.Mod, ".")
	return parts[len(parts)-1] + "." + info.Name
}

// TypeString prints a type the way the spec's diagnostics do.
func TypeString(t Type) string {
	t = Resolve(t)
	switch t := t.(type) {
	case nil:
		return "?"
	case *TVar:
		if t.numeric {
			return "{number}"
		}
		return "_"
	case *TParam:
		return t.Name
	case *TCon:
		name := qualifiedName(t.Info)
		if len(t.Args) == 0 {
			return name
		}
		var parts []string
		for _, a := range t.Args {
			parts = append(parts, TypeString(a))
		}
		return name + "<" + strings.Join(parts, ", ") + ">"
	case *TTuple:
		if len(t.Elems) == 0 {
			return "()"
		}
		var parts []string
		for _, e := range t.Elems {
			parts = append(parts, TypeString(e))
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *TFn:
		var parts []string
		for _, p := range t.Params {
			parts = append(parts, TypeString(p))
		}
		s := "fn(" + strings.Join(parts, ", ") + ")"
		if r := Resolve(t.Ret); !isUnit(r) {
			s += " -> " + TypeString(r)
		}
		if t.Explicit {
			s += " with " + EffectSetString(t.Effects)
		}
		return s
	}
	return fmt.Sprintf("%T", t)
}

func isUnit(t Type) bool {
	tt, ok := Resolve(t).(*TTuple)
	return ok && len(tt.Elems) == 0
}

// ---- effects --------------------------------------------------------------

var effectOrder = map[string]int{"io": 0, "net": 1, "mut": 2, "llm": 3, "decide": 4, "nondet": 5, "spawn": 6, "approve": 7}

// SortEffects orders a set canonically (spec 03 rule 4).
func SortEffects(es []ast.Effect) []ast.Effect {
	out := append([]ast.Effect(nil), es...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if effectOrder[a.Name] != effectOrder[b.Name] {
			return effectOrder[a.Name] < effectOrder[b.Name]
		}
		return a.Role < b.Role
	})
	return out
}

// EffectSetString prints `{io, net, llm<smart>}`.
func EffectSetString(es []ast.Effect) string {
	var parts []string
	for _, e := range SortEffects(es) {
		parts = append(parts, e.String())
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// effContains reports whether set admits e. An entry without a role admits
// every role of the same effect (a budget written as `llm`), while a
// declared `llm<smart>` admits only `llm<smart>` and the role-less use.
func effContains(set []ast.Effect, e ast.Effect) bool {
	for _, s := range set {
		if s.Name != e.Name {
			continue
		}
		if s.Role == "" || e.Role == "" || s.Role == e.Role {
			return true
		}
	}
	return false
}

func effAdd(set []ast.Effect, e ast.Effect) []ast.Effect {
	for _, s := range set {
		if s.Name == e.Name && s.Role == e.Role {
			return set
		}
	}
	return append(set, e)
}

func effUnion(a, b []ast.Effect) []ast.Effect {
	out := append([]ast.Effect(nil), a...)
	for _, e := range b {
		out = effAdd(out, e)
	}
	return out
}

// effSubset reports the first effect of a that b does not admit.
func effSubset(a, b []ast.Effect) (ast.Effect, bool) {
	for _, e := range a {
		if !effContains(b, e) {
			return e, false
		}
	}
	return ast.Effect{}, true
}
