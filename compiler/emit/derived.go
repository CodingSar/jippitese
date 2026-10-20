package emit

import (
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
)

// Derivable reports whether a function gets a derived property check
// (spec 05 rule 14), and why not when it does not.
func Derivable(fi *checker.FnInfo) (bool, string) {
	switch {
	case fi.Module == nil || fi.Module.IsStd || fi.Module.IsDep || fi.Module.IsScenarios:
		return false, "not an app module"
	case fi.Vis != ast.Pub:
		return false, "not `pub`"
	case fi.Extern != nil || fi.Builtin != "" || fi.Impl != nil || fi.Decl == nil:
		return false, "not a declared module function"
	case len(fi.TypeParams) > 0:
		return false, "generic"
	}
	effs := fi.Type.Effects
	switch {
	case len(effs) == 0:
	case len(effs) == 1 && effs[0].Name == "mut":
		for _, p := range fi.Type.Params {
			if mentionsCell(p, map[*checker.TypeInfo]bool{}) {
				return false, "`mut` with a `Cell` parameter"
			}
		}
	default:
		return false, "effects beyond `{mut}`"
	}
	oracle := len(fi.Decl.Post) > 0
	for _, t := range append(append([]checker.Type{}, fi.Type.Params...), fi.Type.Ret) {
		oracle = oracle || carriesInv(t, map[*checker.TypeInfo]bool{})
	}
	if !oracle {
		return false, "no `post` and no `inv` on a parameter or return type"
	}
	for _, p := range fi.Type.Params {
		if !generable(p, map[*checker.TypeInfo]bool{}) {
			return false, "no structural generator for `" + checker.TypeString(p) + "`"
		}
	}
	return true, ""
}

// walkType visits t and every type it is built from: type arguments,
// fields, newtype payloads and variant payloads, each named type once.
func walkType(t checker.Type, seen map[*checker.TypeInfo]bool, visit func(checker.Type) bool) bool {
	t = checker.Zonk(t)
	if visit(t) {
		return true
	}
	switch x := t.(type) {
	case *checker.TTuple:
		for _, el := range x.Elems {
			if walkType(el, seen, visit) {
				return true
			}
		}
	case *checker.TCon:
		for _, a := range x.Args {
			if walkType(a, seen, visit) {
				return true
			}
		}
		ti := x.Info
		if seen[ti] {
			return false
		}
		seen[ti] = true
		var parts []checker.Type
		for _, fl := range ti.Fields {
			parts = append(parts, fl.Type)
		}
		if ti.Kind == checker.Newtype && ti.Inner != nil {
			parts = append(parts, ti.Inner)
		}
		for _, v := range ti.Variants {
			parts = append(parts, v.Positional...)
			for _, nf := range v.Named {
				parts = append(parts, nf.Type)
			}
		}
		for _, p := range parts {
			if walkType(p, seen, visit) {
				return true
			}
		}
	}
	return false
}

func mentionsCell(t checker.Type, seen map[*checker.TypeInfo]bool) bool {
	return walkType(t, seen, func(t checker.Type) bool {
		c, ok := t.(*checker.TCon)
		return ok && c.Info.Name == "Cell"
	})
}

func carriesInv(t checker.Type, seen map[*checker.TypeInfo]bool) bool {
	return walkType(t, seen, func(t checker.Type) bool {
		c, ok := t.(*checker.TCon)
		return ok && len(c.Info.Inv) > 0 && c.Info.Module != nil && !c.Info.Module.IsStd
	})
}

// generable mirrors genFor: the types a structural generator exists for.
func generable(t checker.Type, seen map[*checker.TypeInfo]bool) bool {
	switch x := checker.Zonk(t).(type) {
	case *checker.TTuple:
		for _, el := range x.Elems {
			if !generable(el, seen) {
				return false
			}
		}
		return true
	case *checker.TCon:
		ti := x.Info
		for _, a := range x.Args {
			if !generable(a, seen) {
				return false
			}
		}
		if seen[ti] {
			return true
		}
		seen[ti] = true
		switch ti.Kind {
		case checker.Prim:
			switch ti.Name {
			case "Int", "Float", "Bool", "Str", "Bytes", "Char", "Duration":
				return true
			}
			return false
		case checker.Record:
			for _, fl := range ti.Fields {
				if !generable(fl.Type, seen) {
					return false
				}
			}
			return true
		case checker.Newtype:
			return generable(ti.Inner, seen)
		case checker.Enum:
			for _, v := range ti.Variants {
				for _, pt := range v.Positional {
					if !generable(pt, seen) {
						return false
					}
				}
				for _, nf := range v.Named {
					if !generable(nf.Type, seen) {
						return false
					}
				}
			}
			return true
		case checker.Extern:
			return ti.Mod == "prelude" && ti.Name == "List" || ti.Mod == "std.time" && ti.Name == "Instant"
		}
	}
	return false
}

// emitDerived writes derived_test.go: one Go test per derivable `pub fn`
// of the module (spec 05 rules 14–16), run by rt.Scenario as a property.
func (e *emitter) emitDerived(p *pkg) {
	m := p.mod
	var fns []*checker.FnInfo
	for _, name := range m.Order {
		if fi, ok := m.Fns[name]; ok {
			if ok, _ := Derivable(fi); ok {
				fns = append(fns, fi)
			}
		}
	}
	repos, workers := behaviourChecks(m, "Repository"), behaviourChecks(m, "Worker")
	if len(fns) == 0 && len(repos) == 0 && len(workers) == 0 {
		return
	}
	f := p.newFile("derived_test.go", true)
	tst := f.use("testing", "testing")
	rtp := f.rt()
	for _, fi := range fns {
		d := fi.Decl
		f.printf("\nfunc TestDerived_%s(t *%s.T) {\n", sanitize(slugName(fi.Name)), tst)
		f.line(fi.Pos)
		f.printf("%s.Scenario(t, %s.ScnOpts{Module: %q, Name: %q, Derived: true, Property: true, File: %q, Line: %d, Col: %d}, func(scn *%s.Scn) {\n",
			rtp, rtp, m.Path, fi.Name, fi.Pos.File, fi.Pos.Line, fi.Pos.Col, rtp)
		f.printf("ctx := scn.Ctx\nctx.Config = %s\n", f.cfgRef("TestConfig()"))
		g := newGen(f, nil)
		names := make([]string, len(d.Params))
		var sig []string
		for i := range d.Params {
			names[i] = paramGoName(g.prog, &d.Params[i])
			g.printf("var %s %s\n", names[i], f.goType(fi.Type.Params[i]))
			sig = append(sig, d.Params[i].Name+": "+checker.TypeString(fi.Type.Params[i]))
		}
		at := strconv.Quote(fi.Pos.String())
		g.printf("scn.Given(%s, %q)\n", at, "any "+strings.Join(sig, ", "))
		// The draw closure generates every parameter, narrowed by the `pre`
		// bounds, then evaluates every `pre` clause on them; a false clause
		// redraws (rule 15). A clause that became a bound is still checked,
		// so a bound only saves rejections.
		s := g.sub(nil)
		bs := preBounds(s, fi)
		for i := range d.Params {
			s.printf("%s = (%s)(ctx, gn)\n", names[i], g.genForB(fi.Type.Params[i], bs[i]))
		}
		var pres []string
		for _, c := range d.Pre {
			stmts, v, _ := s.capture(func() string { return s.expr(c) })
			s.printf("%sif !(%s) {\nreturn false\n}\n", stmts, v)
			pres = append(pres, checker.ExprString(c))
		}
		s.printf("return true\n")
		g.printf("%s.Pre(scn, %s, %q, func(ctx *%s.Ctx, gn *%s.Gen) bool {\n%s})\n", rtp, at, strings.Join(pres, " && "), rtp, rtp, s.w.String())
		for i := range d.Params {
			g.printf("scn.Bind(%q, %s)\n", d.Params[i].Name, names[i])
		}
		g.printf("scn.When(%s, %q)\n", at, "ret = "+fi.Name+"("+strings.Join(paramNames(d), ", ")+")")
		g.printf("ret := %s(ctx%s)\nscn.Bind(\"ret\", ret)\n", f.fnRef(fi), prefixEach(", ", names))
		if c, ok := checker.Zonk(fi.Type.Ret).(*checker.TCon); ok {
			if inv := f.invRef(c.Info); inv != "" {
				g.printf("scn.Then(%s, %q, %sOK(ctx, ret))\n", at, "inv of "+c.Info.Name+" holds for ret", inv)
			}
		}
		f.body.WriteString(g.w.String())
		f.printf("})\n}\n")
	}
	for _, impl := range repos {
		emitRoundTrip(f, impl)
	}
	for _, impl := range workers {
		emitIdempotent(f, impl)
	}
}

// behaviourChecks are the module's non-generic impls of std behaviour b
// whose derived check can draw its inputs: `K` and `T` of a `Repository`
// (spec 06 rule 10), `Msg` of a `Worker` (rule 8).
func behaviourChecks(m *checker.Module, b string) []*checker.ImplInfo {
	var out []*checker.ImplInfo
	for _, impl := range m.Impls {
		if impl.Behaviour == nil || impl.Behaviour.Name != b || impl.Behaviour.Module == nil || !impl.Behaviour.Module.IsStd ||
			len(impl.TypeParams) > 0 {
			continue
		}
		var drawn []checker.Type
		switch b {
		case "Repository":
			drawn = impl.BehaviourArgs
		case "Worker":
			if t := impl.Assoc["Msg"]; t != nil {
				drawn = []checker.Type{t}
			}
		}
		ok := len(drawn) > 0
		for _, t := range drawn {
			ok = ok && generable(t, map[*checker.TypeInfo]bool{})
		}
		if ok {
			out = append(out, impl)
		}
	}
	return out
}

// behaviourScenario opens the derived scenario `<Impl>.<check>` of an impl.
func behaviourScenario(f *file, impl *checker.ImplInfo, check string) (*gen, string) {
	rtp := f.rt()
	m, pos := impl.Module, impl.Decl.Pos
	f.printf("\nfunc TestDerived_%s_%s(t *%s.T) {\n", sanitize(impl.Name), check, f.use("testing", "testing"))
	f.line(pos)
	f.printf("%s.Scenario(t, %s.ScnOpts{Module: %q, Name: %q, Derived: true, Property: true, File: %q, Line: %d, Col: %d}, func(scn *%s.Scn) {\n",
		rtp, rtp, m.Path, impl.Name+"."+check, pos.File, pos.Line, pos.Col, rtp)
	f.printf("ctx := scn.Ctx\nctx.Config = %s\n", f.cfgRef("TestConfig()"))
	return newGen(f, nil), strconv.Quote(pos.String())
}

// emitIdempotent writes the derived idempotency check of a `Worker` impl:
// for `any Msg`, `perform(m); perform(m)` leaves every `Repository` and
// `Cache` fake as `perform(m)` does and ends in the same result (spec 06
// rule 8).
func emitIdempotent(f *file, impl *checker.ImplInfo) {
	rtp := f.rt()
	mt := impl.Assoc["Msg"]
	g, at := behaviourScenario(f, impl, "idempotent")
	g.printf("var m %s\n", f.goType(mt))
	g.printf("scn.Given(%s, %q)\n", at, "any m: "+checker.TypeString(mt))
	g.printf("%s.Pre(scn, %s, \"\", func(ctx *%s.Ctx, gn *%s.Gen) bool {\nm = (%s)(ctx, gn)\nreturn true\n})\n",
		rtp, at, rtp, rtp, g.genForB(mt, nil))
	g.printf("scn.Bind(\"m\", m)\n")
	g.printf("scn.When(%s, %q)\n", at, "perform(m) against fresh fakes, and perform(m); perform(m)")
	g.printf("once, twice, diff := %s.WorkerTwice(ctx, %s, m)\n", rtp, f.implRef(impl, "spec"))
	g.printf("scn.Bind(\"once\", once)\nscn.Bind(\"twice\", twice)\n")
	g.printf("if diff != \"\" {\nscn.Bind(\"differs\", diff)\n}\n")
	g.printf("scn.Then(%s, %q, diff == \"\")\n", at, "perform(m); perform(m) leaves the fakes as perform(m)")
	g.printf("scn.Then(%s, %q, %s.Equal(once, twice))\n", at, "perform(m); perform(m) ends as perform(m)", rtp)
	f.body.WriteString(g.w.String())
	f.printf("})\n}\n")
}

// emitRoundTrip writes the derived round-trip of a `Repository` impl:
// on a fresh instance, `put(k, v)` then `get(k)` is `Ok(Some(v))` for
// `any K`, `any T` (spec 06 rule 10).
func emitRoundTrip(f *file, impl *checker.ImplInfo) {
	rtp := f.rt()
	tt, kt := impl.BehaviourArgs[0], impl.BehaviourArgs[1]
	g, at := behaviourScenario(f, impl, "round_trip")
	g.printf("var k %s\nvar v %s\n", f.goType(kt), f.goType(tt))
	g.printf("scn.Given(%s, %q)\n", at, "any k: "+checker.TypeString(kt)+", v: "+checker.TypeString(tt))
	g.printf("%s.Pre(scn, %s, \"\", func(ctx *%s.Ctx, gn *%s.Gen) bool {\nk = (%s)(ctx, gn)\nv = (%s)(ctx, gn)\nreturn true\n})\n",
		rtp, at, rtp, rtp, g.genForB(kt, nil), g.genForB(tt, nil))
	g.printf("scn.Bind(\"k\", k)\nscn.Bind(\"v\", v)\n")
	inst := g.instance(impl)
	g.printf("scn.When(%s, %q)\n", at, impl.Name+".put(k, v)")
	g.printf("put := %s(ctx, %s, k, v)\nscn.Bind(\"put\", put)\n", f.fnRef(impl.Methods["put"]), inst)
	g.printf("scn.Then(%s, %q, put.Tag == %s.ResultTag_Ok)\n", at, "put(k, v) is Ok", rtp)
	g.printf("got := %s(ctx, %s, k)\nscn.Bind(\"got\", got)\n", f.fnRef(impl.Methods["get"]), inst)
	g.printf("scn.Then(%s, %q, %s.Equal(got, %s.Result_Ok[%s.Option[%s], %s.RepoError](%s.Option_Some[%s](v))))\n",
		at, "get(k) == Ok(Some(v))", rtp, rtp, rtp, f.goType(tt), rtp, rtp, f.goType(tt))
	f.body.WriteString(g.w.String())
	f.printf("})\n}\n")
}

// bound narrows one generator (05 rule 15). Int and Float leaves take
// lo and hi, List, Str and Bytes a length range; records, newtypes and
// tuples narrow their parts, keyed by field name or index. Every entry is
// a Go expression; several on one end combine with max or min.
type bound struct {
	lo, hi       []string
	lenLo, lenHi []string
	parts        map[string]*bound
}

func (b *bound) at(keys []string) *bound {
	for _, k := range keys {
		if b.parts == nil {
			b.parts = map[string]*bound{}
		}
		if b.parts[k] == nil {
			b.parts[k] = &bound{}
		}
		b = b.parts[k]
	}
	return b
}

func (b *bound) part(k string) *bound {
	if b == nil {
		return nil
	}
	return b.parts[k]
}

// combine joins the Go expressions of one end, open when there are none.
func combine(fn, open string, xs []string) string {
	switch len(xs) {
	case 0:
		return open
	case 1:
		return xs[0]
	}
	return fn + "(" + strings.Join(xs, ", ") + ")"
}

func (b *bound) lower(rtp string) string { return combine("max", "-"+rtp+".GenOpen", b.lo) }
func (b *bound) upper(rtp string) string { return combine("min", rtp+".GenOpen", b.hi) }
func (b *bound) lenLower() string        { return combine("max", "0", b.lenLo) }
func (b *bound) lenUpper(rtp string) string {
	return combine("min", rtp+".GenOpen", b.lenHi)
}

// preBounds turns the `pre` conjuncts of d of the shapes of rule 15 into
// generator bounds, one per parameter: `p op e` or `e op p` with `op` a
// comparison, and `(lo..hi).contains(p)`, where `p` is a parameter, a
// field path of one or such a path's `.len()`, and `e` mentions only
// parameters drawn before `p`'s. Any other conjunct stays a filter only.
func preBounds(s *gen, fi *checker.FnInfo) []*bound {
	d := fi.Decl
	out := make([]*bound, len(d.Params))
	for i := range out {
		out[i] = &bound{}
	}
	var conj []ast.Expr
	var split func(e ast.Expr)
	split = func(e ast.Expr) {
		if b, ok := e.(*ast.BinaryExpr); ok && b.Op == "&&" {
			split(b.L)
			split(b.R)
			return
		}
		conj = append(conj, e)
	}
	for _, c := range d.Pre {
		split(c)
	}
	flip := map[string]string{"<": ">", "<=": ">=", ">": "<", ">=": "<=", "==": "=="}
	for _, c := range conj {
		switch x := c.(type) {
		case *ast.BinaryExpr:
			if _, ok := flip[x.Op]; !ok {
				continue
			}
			if !boundOn(s, fi, out, x.L, x.Op, x.R) {
				boundOn(s, fi, out, x.R, flip[x.Op], x.L)
			}
		case *ast.CallExpr:
			fe, ok := x.Fn.(*ast.FieldExpr)
			if !ok || fe.Name != "contains" || len(x.Args) != 1 {
				continue
			}
			if r, ok := fe.X.(*ast.RangeExpr); ok {
				hiOp := "<"
				if r.Incl {
					hiOp = "<="
				}
				if boundOn(s, fi, out, x.Args[0], ">=", r.Lo) {
					boundOn(s, fi, out, x.Args[0], hiOp, r.Hi)
				}
			}
		}
	}
	return out
}

// boundOn records `path op e` when path is a bounded parameter path and
// e a Go-evaluable expression over parameters drawn before it.
func boundOn(s *gen, fi *checker.FnInfo, out []*bound, path ast.Expr, op string, e ast.Expr) bool {
	d := fi.Decl
	i, keys, isLen, leaf := paramPath(s, fi, path)
	if i < 0 {
		return false
	}
	ok := true
	ast.WalkExpr(e, func(x ast.Expr) {
		if id, isID := x.(*ast.Ident); isID {
			if j := paramIndex(s, d, id); j >= i {
				ok = false
			}
		}
	})
	if !ok {
		return false
	}
	stmts, v, dead := s.capture(func() string { return s.expr(e) })
	if stmts != "" || dead {
		return false
	}
	b := out[i].at(keys)
	isInt := isLen
	if !isLen {
		c, isCon := leaf.(*checker.TCon)
		switch {
		case isCon && c.Info.Kind == checker.Prim && c.Info.Name == "Int":
			isInt = true
		case isCon && c.Info.Kind == checker.Prim && c.Info.Name == "Float":
		default:
			return false
		}
	}
	lo, hi := &b.lo, &b.hi
	if isLen {
		lo, hi = &b.lenLo, &b.lenHi
	}
	// An Int bound is exact; a strict Float bound is left to the filter.
	switch op {
	case ">":
		if isInt {
			v = "(" + v + ") + 1"
		}
		*lo = append(*lo, v)
	case ">=":
		*lo = append(*lo, v)
	case "<":
		if isInt {
			v = "(" + v + ") - 1"
		}
		*hi = append(*hi, v)
	case "<=":
		*hi = append(*hi, v)
	case "==":
		*lo = append(*lo, v)
		*hi = append(*hi, v)
	}
	return true
}

// paramPath resolves e as a parameter, a field path of one, or such a
// path's `.len()`: the parameter index (-1 when e is none of these), the
// field keys, whether it ends in `.len()`, and the type at the path.
func paramPath(s *gen, fi *checker.FnInfo, e ast.Expr) (int, []string, bool, checker.Type) {
	isLen := false
	if c, ok := e.(*ast.CallExpr); ok {
		fe, ok := c.Fn.(*ast.FieldExpr)
		if !ok || fe.Name != "len" || len(c.Args) != 0 {
			return -1, nil, false, nil
		}
		e, isLen = fe.X, true
	}
	var keys []string
	for {
		fe, ok := e.(*ast.FieldExpr)
		if !ok {
			break
		}
		keys = append([]string{fe.Name}, keys...)
		e = fe.X
	}
	id, ok := e.(*ast.Ident)
	if !ok {
		return -1, nil, false, nil
	}
	i := paramIndex(s, fi.Decl, id)
	if i < 0 {
		return -1, nil, false, nil
	}
	t := fi.Type.Params[i]
	for _, k := range keys {
		if t = partType(t, k); t == nil {
			return -1, nil, false, nil
		}
	}
	if isLen {
		c, ok := checker.Zonk(t).(*checker.TCon)
		if !ok || !(c.Info.Kind == checker.Prim && (c.Info.Name == "Str" || c.Info.Name == "Bytes") ||
			c.Info.Kind == checker.Extern && c.Info.Mod == "prelude" && c.Info.Name == "List") {
			return -1, nil, false, nil
		}
	}
	return i, keys, isLen, checker.Zonk(t)
}

// partType is the type of field or index k of t, as genForB narrows it.
func partType(t checker.Type, k string) checker.Type {
	switch x := checker.Zonk(t).(type) {
	case *checker.TTuple:
		if n, err := strconv.Atoi(k); err == nil && n < len(x.Elems) {
			return x.Elems[n]
		}
	case *checker.TCon:
		m := map[*checker.TParam]checker.Type{}
		for i, p := range x.Info.Params {
			if i < len(x.Args) {
				m[p] = x.Args[i]
			}
		}
		switch x.Info.Kind {
		case checker.Record:
			for _, fl := range x.Info.Fields {
				if fl.Name == k {
					return checker.Subst(fl.Type, m)
				}
			}
		case checker.Newtype:
			if k == "0" {
				return checker.Subst(x.Info.Inner, m)
			}
		}
	}
	return nil
}

// paramIndex is the index of the parameter id names, or -1.
func paramIndex(s *gen, d *ast.FnDecl, id *ast.Ident) int {
	r := s.prog.Refs[id]
	if r == nil || r.Kind != checker.RefLocal {
		return -1
	}
	for i := range d.Params {
		if s.prog.Params[&d.Params[i]] == r.Local {
			return i
		}
	}
	return -1
}

func paramNames(d *ast.FnDecl) []string {
	out := make([]string, len(d.Params))
	for i, p := range d.Params {
		out[i] = p.Name
	}
	return out
}

func prefixEach(pre string, xs []string) string {
	var b strings.Builder
	for _, x := range xs {
		b.WriteString(pre + x)
	}
	return b.String()
}
