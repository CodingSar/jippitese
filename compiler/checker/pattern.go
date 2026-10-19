package checker

import (
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
)

// ---- pattern checking -------------------------------------------------------

// checkPattern checks p against the scrutinee type t and declares its
// bindings in the current scope.
func (c *Checker) checkPattern(p ast.Pattern, t Type) {
	switch p := p.(type) {
	case nil:
		return
	case *ast.WildPat:
		c.prog.Pats[p] = &PatInfo{Type: t}
	case *ast.BindPat:
		l := &Local{Name: p.Name, Type: t, Pos: p.Pos}
		if ft, ok := Resolve(t).(*TFn); ok {
			l.Fn = ft
			l.Effects = ft.Effects
			l.Explicit = ft.Explicit
		}
		c.declareLocal(l)
		c.prog.PatBinds[p] = l
		c.prog.Pats[p] = &PatInfo{Type: t}
	case *ast.LitPat:
		lt := c.infer(p.Lit, t)
		c.expect(p.Lit, lt, t, "")
		c.prog.Pats[p] = &PatInfo{Type: t}
	case *ast.AtPat:
		l := &Local{Name: p.Name, Type: t, Pos: p.Pos}
		c.declareLocal(l)
		c.prog.AtBinds[p] = l
		c.prog.Pats[p] = &PatInfo{Type: t}
		c.checkPattern(p.Pat, t)
	case *ast.OrPat:
		for _, a := range p.Alts {
			c.checkPattern(a, t)
		}
		c.prog.Pats[p] = &PatInfo{Type: t}
	case *ast.TuplePat:
		tt, ok := Resolve(t).(*TTuple)
		if !ok {
			var elems []Type
			for range p.Elems {
				elems = append(elems, c.newVar())
			}
			tt = &TTuple{Elems: elems}
			if !c.u.tryUnify(t, tt) {
				dg := c.errorf("E0402", p.Pos, "type mismatch")
				dg.Label("expected `%s`, found a tuple pattern", TypeString(t))
				c.prog.Pats[p] = &PatInfo{Type: t}
				for _, e := range p.Elems {
					c.checkPattern(e, c.newVar())
				}
				return
			}
		}
		if len(tt.Elems) != len(p.Elems) {
			dg := c.errorf("E0402", p.Pos, "tuple pattern has %d elements", len(p.Elems))
			dg.Label("the value has %d", len(tt.Elems))
		}
		for i, e := range p.Elems {
			var et Type = c.newVar()
			if i < len(tt.Elems) {
				et = tt.Elems[i]
			}
			c.checkPattern(e, et)
		}
		c.prog.Pats[p] = &PatInfo{Type: t}
	case *ast.RecordPat:
		// The parser produces VariantPat for `Type { … }`; keep this for safety.
		vp := &ast.VariantPat{Pos: p.Pos, Path: p.Path, Named: p.Fields, Braces: true}
		c.checkPattern(vp, t)
		c.prog.Pats[p] = c.prog.Pats[vp]
	case *ast.VariantPat:
		c.checkVariantPat(p, t)
	}
}

// checkVariantPat handles `Name`, `Name(…)` and `Name { … }` patterns: enum
// variants, newtype constructors and record patterns.
func (c *Checker) checkVariantPat(p *ast.VariantPat, t Type) {
	info := &PatInfo{Type: t}
	c.prog.Pats[p] = info
	rt, _ := Resolve(t).(*TCon)
	name := p.Path[len(p.Path)-1]

	var v *VariantInfo
	var enum *TCon
	var ti *TypeInfo // newtype or record

	switch len(p.Path) {
	case 1:
		if rt != nil && rt.Info.Kind == Enum {
			for _, cand := range rt.Info.Variants {
				if cand.Name == name {
					v, enum = cand, rt
				}
			}
		}
		if v == nil && rt != nil && (rt.Info.Kind == Newtype || rt.Info.Kind == Record) && rt.Info.Name == name {
			ti = rt.Info
		}
		if v == nil && ti == nil {
			v, enum = c.bareVariant(name, nil, p.Pos)
		}
		if v == nil && ti == nil {
			ti = c.lookupTypeInfo(c.fn.module, name)
		}
	case 2:
		// Enum.Variant or module.Type
		if et := c.lookupTypeInfo(c.fn.module, p.Path[0]); et != nil && isTypeName(p.Path[0]) {
			if et.Kind != Enum {
				c.errorf("E0404", p.Pos, "`%s` is not an enum", p.Path[0])
				c.bindAll(p)
				return
			}
			for _, cand := range et.Variants {
				if cand.Name == name {
					v = cand
				}
			}
			if v == nil {
				c.errorf("E0407", p.Pos, "enum `%s` has no variant `%s`", et.Name, name)
				c.bindAll(p)
				return
			}
			if rt != nil && rt.Info == et {
				enum = rt
			} else {
				enum = con(et, c.freshArgs(et)...)
			}
		} else if dep := c.depModule(c.fn.module, p.Path[0]); dep != nil {
			ti = dep.Types[name]
			if ti == nil {
				c.errorf("E0404", p.Pos, "unknown type `%s.%s`", p.Path[0], name)
				c.bindAll(p)
				return
			}
			if !c.visible(ti.Vis, ti.Mod, c.fn.module) {
				c.privateError(p.Pos, name, ti.Mod)
			}
		} else {
			c.errorf("E0404", p.Pos, "unknown type or module `%s`", p.Path[0])
			c.bindAll(p)
			return
		}
	case 3:
		dep := c.depModule(c.fn.module, p.Path[0])
		if dep == nil {
			c.errorf("E0102", p.Pos, "unknown module `%s`", p.Path[0])
			c.bindAll(p)
			return
		}
		et := dep.Types[p.Path[1]]
		if et == nil || et.Kind != Enum {
			c.errorf("E0404", p.Pos, "unknown enum `%s.%s`", p.Path[0], p.Path[1])
			c.bindAll(p)
			return
		}
		if !c.visible(et.Vis, et.Mod, c.fn.module) {
			c.privateError(p.Pos, et.Name, et.Mod)
		}
		for _, cand := range et.Variants {
			if cand.Name == name {
				v = cand
			}
		}
		if v == nil {
			c.errorf("E0407", p.Pos, "enum `%s` has no variant `%s`", et.Name, name)
			c.bindAll(p)
			return
		}
		if rt != nil && rt.Info == et {
			enum = rt
		} else {
			enum = con(et, c.freshArgs(et)...)
		}
	default:
		c.errorf("E0407", p.Pos, "unknown pattern `%s`", strings.Join(p.Path, "."))
		c.bindAll(p)
		return
	}

	if v != nil {
		info.Variant = v
		if !c.u.tryUnify(t, enum) {
			dg := c.errorf("E0402", p.Pos, "type mismatch")
			dg.Label("expected `%s`, found a pattern for `%s`", TypeString(t), qualifiedName(v.Enum))
		}
		m := variantSubst(v, enum)
		if p.Parens {
			if len(v.Named) > 0 {
				dg := c.errorf("E0402", p.Pos, "variant `%s` has named fields", v.Name)
				dg.Helpf("write `%s { … }`", v.Name)
				c.bindAll(p)
				return
			}
			if len(p.Positional) != len(v.Positional) {
				dg := c.errorf("E0402", p.Pos, "variant `%s` has %d payload field(s)", v.Name, len(v.Positional))
				dg.Label("pattern has %d", len(p.Positional))
			}
			for i, sp := range p.Positional {
				var ft Type = c.newVar()
				if i < len(v.Positional) {
					ft = subst(v.Positional[i], m)
				}
				c.checkPattern(sp, ft)
			}
			return
		}
		if p.Braces {
			if len(v.Named) == 0 {
				dg := c.errorf("E0402", p.Pos, "variant `%s` has no named fields", v.Name)
				if len(v.Positional) > 0 {
					dg.Helpf("write `%s(…)`", v.Name)
				}
				c.bindAll(p)
				return
			}
			c.checkNamedFields(p, v.Named, m, v.Name)
			return
		}
		if len(v.Positional) > 0 || len(v.Named) > 0 {
			dg := c.errorf("E0402", p.Pos, "variant `%s` carries a payload", v.Name)
			if len(v.Positional) > 0 {
				dg.Helpf("write `%s(_)` or bind the payload", v.Name)
			} else {
				dg.Helpf("write `%s { .. }` or bind the fields", v.Name)
			}
		}
		return
	}

	if ti == nil {
		c.errorf("E0407", p.Pos, "unknown variant or type `%s`", strings.Join(p.Path, "."))
		c.bindAll(p)
		return
	}
	tc := con(ti, c.freshArgs(ti)...)
	if rt != nil && rt.Info == ti {
		tc = rt
	}
	if !c.u.tryUnify(t, tc) {
		dg := c.errorf("E0402", p.Pos, "type mismatch")
		dg.Label("expected `%s`, found a pattern for `%s`", TypeString(t), qualifiedName(ti))
	}
	m := typeArgSubst(tc)
	switch ti.Kind {
	case Newtype:
		info.Newtype = ti
		if !p.Parens || len(p.Positional) != 1 {
			dg := c.errorf("E0402", p.Pos, "newtype pattern `%s` takes exactly one payload", ti.Name)
			dg.Helpf("write `%s(n)`", ti.Name)
			c.bindAll(p)
			return
		}
		c.checkPattern(p.Positional[0], subst(ti.Inner, m))
	case Record:
		info.Record = ti
		if !p.Braces {
			dg := c.errorf("E0402", p.Pos, "record pattern `%s` needs braces", ti.Name)
			dg.Helpf("write `%s { .. }`", ti.Name)
			c.bindAll(p)
			return
		}
		if ti.Opaque && ti.Mod != c.fn.module.Path && !(c.fn.module.IsScenarios && c.fn.module.Parent != nil && ti.Mod == c.fn.module.Parent.Path) {
			c.errorf("E0408", p.Pos, "`%s` is opaque; its fields cannot be matched outside `%s`", ti.Name, ti.Mod)
		}
		c.checkNamedFields(p, ti.Fields, m, ti.Name)
	default:
		c.errorf("E0407", p.Pos, "`%s` cannot be used as a pattern", ti.Name)
		c.bindAll(p)
	}
}

// checkNamedFields checks `{ a, b: pat, .. }` against the declared fields.
func (c *Checker) checkNamedFields(p *ast.VariantPat, fields []*FieldInfo, m map[*TParam]Type, owner string) {
	seen := map[string]bool{}
	rest := false
	for _, fp := range p.Named {
		if fp.Name == ".." {
			rest = true
			continue
		}
		if seen[fp.Name] {
			c.errorf("E0406", p.Pos, "field `%s` matched twice", fp.Name)
		}
		seen[fp.Name] = true
		var ft Type
		for _, f := range fields {
			if f.Name == fp.Name {
				ft = subst(f.Type, m)
			}
		}
		if ft == nil {
			dg := c.errorf("E0407", p.Pos, "`%s` has no field `%s`", owner, fp.Name)
			var names []string
			for _, f := range fields {
				names = append(names, "`"+f.Name+"`")
			}
			dg.Label("fields: %s", strings.Join(names, ", "))
			ft = c.newVar()
		}
		c.checkPattern(fp.Pat, ft)
	}
	if !rest {
		var missing []string
		for _, f := range fields {
			if !seen[f.Name] {
				missing = append(missing, "`"+f.Name+"`")
			}
		}
		if len(missing) > 0 {
			dg := c.errorf("E0402", p.Pos, "pattern does not mention %s", strings.Join(missing, ", "))
			dg.Helpf("add `..` to ignore the remaining fields")
		}
	}
}

// bindAll declares every name a malformed pattern would bind, so that later
// errors do not cascade into unknown-name reports.
func (c *Checker) bindAll(p ast.Pattern) {
	switch p := p.(type) {
	case *ast.BindPat:
		if c.lookupLocal(p.Name) == nil {
			c.checkPattern(p, c.newVar())
		}
	case *ast.AtPat:
		c.declareLocal(&Local{Name: p.Name, Type: c.newVar(), Pos: p.Pos})
		c.bindAll(p.Pat)
	case *ast.VariantPat:
		for _, sp := range p.Positional {
			c.bindAll(sp)
		}
		for _, fp := range p.Named {
			c.bindAll(fp.Pat)
		}
	case *ast.TuplePat:
		for _, sp := range p.Elems {
			c.bindAll(sp)
		}
	case *ast.OrPat:
		for _, sp := range p.Alts {
			c.bindAll(sp)
		}
	}
}

// refutable reports whether p can fail to match a value of type t.
func (c *Checker) refutable(p ast.Pattern, t Type) bool {
	switch p := p.(type) {
	case nil, *ast.WildPat, *ast.BindPat:
		return false
	case *ast.LitPat:
		return true
	case *ast.AtPat:
		return c.refutable(p.Pat, t)
	case *ast.OrPat:
		for _, a := range p.Alts {
			if !c.refutable(a, t) {
				return false
			}
		}
		return true
	case *ast.TuplePat:
		tt, _ := Resolve(t).(*TTuple)
		for i, e := range p.Elems {
			var et Type
			if tt != nil && i < len(tt.Elems) {
				et = tt.Elems[i]
			}
			if c.refutable(e, et) {
				return true
			}
		}
		return false
	case *ast.VariantPat:
		name := p.Path[len(p.Path)-1]
		rt, _ := Resolve(t).(*TCon)
		if rt != nil {
			switch rt.Info.Kind {
			case Enum:
				if len(rt.Info.Variants) > 1 {
					return true
				}
				v := rt.Info.Variants[0]
				m := variantSubst(v, rt)
				for i, sp := range p.Positional {
					var ft Type
					if i < len(v.Positional) {
						ft = subst(v.Positional[i], m)
					}
					if c.refutable(sp, ft) {
						return true
					}
				}
				for _, fp := range p.Named {
					if c.refutable(fp.Pat, c.fieldType(v.Named, fp.Name, m)) {
						return true
					}
				}
				return false
			case Newtype:
				if rt.Info.Name == name && len(p.Positional) == 1 {
					return c.refutable(p.Positional[0], subst(rt.Info.Inner, typeArgSubst(rt)))
				}
				return true
			case Record:
				m := typeArgSubst(rt)
				for _, fp := range p.Named {
					if c.refutable(fp.Pat, c.fieldType(rt.Info.Fields, fp.Name, m)) {
						return true
					}
				}
				return false
			}
		}
		// Unknown scrutinee type: a newtype or record pattern is irrefutable,
		// an enum variant is refutable.
		if ti := c.lookupTypeInfo(c.fn.module, name); ti != nil && len(p.Path) == 1 && (ti.Kind == Newtype || ti.Kind == Record) {
			return false
		}
		return true
	}
	return true
}

func (c *Checker) fieldType(fields []*FieldInfo, name string, m map[*TParam]Type) Type {
	for _, f := range fields {
		if f.Name == name {
			return subst(f.Type, m)
		}
	}
	return nil
}

// ---- match ------------------------------------------------------------------

func (c *Checker) inferMatch(e *ast.MatchExpr, want Type) Type {
	st := c.infer(e.X, nil)
	var result Type = want
	for i := range e.Arms {
		arm := &e.Arms[i]
		c.pushScope()
		c.checkPattern(arm.Pat, st)
		if arm.Guard != nil {
			gt := c.infer(arm.Guard, con(tBool))
			c.expect(arm.Guard, gt, con(tBool), "a match guard must be `Bool`")
		}
		bt := c.infer(arm.Body, result)
		if result == nil {
			result = bt
		} else if !c.u.tryUnify(bt, result) {
			if tv, ok := Resolve(bt).(*TVar); ok && c.isDiverge(tv) {
				// a diverging arm takes any type
			} else {
				dg := c.errorf("E0402", arm.Body.ExprPos(), "type mismatch")
				dg.Label("expected `%s`, found `%s`", TypeString(result), TypeString(bt))
				dg.Note("every arm of a `match` must have the same type; the first arm has `%s`", TypeString(result))
			}
		}
		c.popScope()
	}
	if result == nil {
		result = unit
	}
	if len(e.Arms) == 0 {
		dg := c.errorf("E0420", e.Pos, "non-exhaustive match on `%s`", TypeString(st))
		dg.Label("no arms")
		return result
	}
	c.checkExhaustive(e, st)
	return result
}

func (c *Checker) isDiverge(v *TVar) bool {
	for _, d := range c.fn.diverge {
		if d == v {
			return true
		}
	}
	return false
}

// ---- exhaustiveness ---------------------------------------------------------

// spat is a simplified pattern used by the usefulness algorithm.
type spat struct {
	wild bool
	ctor string  // variant name, "true"/"false", "()" (tuple/record/newtype), or a literal
	lit  bool    // ctor is a literal from an infinite domain
	args []*spat // sub-patterns in constructor order
	v    *VariantInfo
}

var wildPat = &spat{wild: true}

// simplify converts an AST pattern into one or more spats (one per or-alternative).
func (c *Checker) simplify(p ast.Pattern, t Type) []*spat {
	switch p := p.(type) {
	case nil, *ast.WildPat, *ast.BindPat:
		return []*spat{wildPat}
	case *ast.AtPat:
		return c.simplify(p.Pat, t)
	case *ast.OrPat:
		var out []*spat
		for _, a := range p.Alts {
			out = append(out, c.simplify(a, t)...)
		}
		return out
	case *ast.LitPat:
		if b, ok := p.Lit.(*ast.BoolLit); ok {
			if b.V {
				return []*spat{{ctor: "true"}}
			}
			return []*spat{{ctor: "false"}}
		}
		return []*spat{{ctor: exprString(p.Lit), lit: true}}
	case *ast.TuplePat:
		tt, _ := Resolve(t).(*TTuple)
		var elemTypes []Type
		if tt != nil {
			elemTypes = tt.Elems
		}
		return c.product("()", p.Elems, elemTypes)
	case *ast.RecordPat:
		vp := &ast.VariantPat{Pos: p.Pos, Path: p.Path, Named: p.Fields, Braces: true}
		return c.simplify(vp, t)
	case *ast.VariantPat:
		info := c.prog.Pats[p]
		if info == nil {
			return []*spat{wildPat}
		}
		rt, _ := Resolve(t).(*TCon)
		switch {
		case info.Variant != nil:
			v := info.Variant
			var m map[*TParam]Type
			if rt != nil {
				m = variantSubst(v, rt)
			}
			if len(v.Named) > 0 {
				var subs []ast.Pattern
				var types []Type
				for _, f := range v.Named {
					var sp ast.Pattern = &ast.WildPat{}
					for _, fp := range p.Named {
						if fp.Name == f.Name {
							sp = fp.Pat
						}
					}
					subs = append(subs, sp)
					types = append(types, subst(f.Type, m))
				}
				return c.productV(v, subs, types)
			}
			var types []Type
			for _, pt := range v.Positional {
				types = append(types, subst(pt, m))
			}
			var subs []ast.Pattern
			subs = append(subs, p.Positional...)
			for len(subs) < len(types) {
				subs = append(subs, &ast.WildPat{})
			}
			return c.productV(v, subs, types)
		case info.Newtype != nil:
			var inner Type
			if rt != nil {
				inner = subst(info.Newtype.Inner, typeArgSubst(rt))
			}
			var sub ast.Pattern = &ast.WildPat{}
			if len(p.Positional) == 1 {
				sub = p.Positional[0]
			}
			return c.product("()", []ast.Pattern{sub}, []Type{inner})
		case info.Record != nil:
			var m map[*TParam]Type
			if rt != nil {
				m = typeArgSubst(rt)
			}
			var subs []ast.Pattern
			var types []Type
			for _, f := range info.Record.Fields {
				var sp ast.Pattern = &ast.WildPat{}
				for _, fp := range p.Named {
					if fp.Name == f.Name {
						sp = fp.Pat
					}
				}
				subs = append(subs, sp)
				types = append(types, subst(f.Type, m))
			}
			return c.product("()", subs, types)
		}
		return []*spat{wildPat}
	}
	return []*spat{wildPat}
}

// product expands sub-patterns (each possibly an or-pattern) into the
// cartesian product of constructor patterns.
func (c *Checker) product(ctor string, subs []ast.Pattern, types []Type) []*spat {
	return c.productV(nil, subs, types)
}

func (c *Checker) productV(v *VariantInfo, subs []ast.Pattern, types []Type) []*spat {
	ctor := "()"
	if v != nil {
		ctor = v.Name
	}
	rows := [][]*spat{{}}
	for i, sp := range subs {
		var t Type
		if i < len(types) {
			t = types[i]
		}
		alts := c.simplify(sp, t)
		var next [][]*spat
		for _, r := range rows {
			for _, a := range alts {
				nr := append(append([]*spat{}, r...), a)
				next = append(next, nr)
			}
		}
		rows = next
	}
	var out []*spat
	for _, r := range rows {
		out = append(out, &spat{ctor: ctor, args: r, v: v})
	}
	return out
}

// ctorInfo describes one constructor of a type.
type ctorInfo struct {
	name  string
	args  []Type
	v     *VariantInfo
	named bool
}

// ctors lists the constructors of t, or nil when the domain is infinite
// (Int, Str, …) or unknown.
func (c *Checker) ctors(t Type) []ctorInfo {
	switch rt := Resolve(t).(type) {
	case *TTuple:
		return []ctorInfo{{name: "()", args: rt.Elems}}
	case *TCon:
		switch rt.Info.Kind {
		case Enum:
			var out []ctorInfo
			for _, v := range rt.Info.Variants {
				m := variantSubst(v, rt)
				ci := ctorInfo{name: v.Name, v: v}
				if len(v.Named) > 0 {
					ci.named = true
					for _, f := range v.Named {
						ci.args = append(ci.args, subst(f.Type, m))
					}
				} else {
					for _, pt := range v.Positional {
						ci.args = append(ci.args, subst(pt, m))
					}
				}
				out = append(out, ci)
			}
			return out
		case Newtype:
			return []ctorInfo{{name: "()", args: []Type{subst(rt.Info.Inner, typeArgSubst(rt))}}}
		case Record:
			ci := ctorInfo{name: "()", named: true}
			m := typeArgSubst(rt)
			for _, f := range rt.Info.Fields {
				ci.args = append(ci.args, subst(f.Type, m))
			}
			return []ctorInfo{ci}
		case Prim:
			if rt.Info == tBool {
				return []ctorInfo{{name: "false"}, {name: "true"}}
			}
		}
	}
	return nil
}

// witness returns a vector of patterns not matched by any row, or nil when
// the rows cover every value of the given types.
func (c *Checker) witness(rows [][]*spat, types []Type) []*spat {
	if len(types) == 0 {
		if len(rows) == 0 {
			return []*spat{}
		}
		return nil
	}
	t := types[0]
	cs := c.ctors(t)
	used := map[string]bool{}
	for _, r := range rows {
		if !r[0].wild {
			used[r[0].ctor] = true
		}
	}
	complete := cs != nil
	if complete {
		for _, ci := range cs {
			if !used[ci.name] {
				complete = false
				break
			}
		}
	}
	if complete {
		for _, ci := range cs {
			spec := specialize(rows, ci.name, len(ci.args))
			w := c.witness(spec, append(append([]Type{}, ci.args...), types[1:]...))
			if w != nil {
				head := &spat{ctor: ci.name, args: w[:len(ci.args)], v: ci.v}
				return append([]*spat{head}, w[len(ci.args):]...)
			}
		}
		return nil
	}
	var def [][]*spat
	for _, r := range rows {
		if r[0].wild {
			def = append(def, r[1:])
		}
	}
	w := c.witness(def, types[1:])
	if w == nil {
		return nil
	}
	if cs == nil {
		return append([]*spat{wildPat}, w...)
	}
	for _, ci := range cs {
		if !used[ci.name] {
			var args []*spat
			for range ci.args {
				args = append(args, wildPat)
			}
			return append([]*spat{{ctor: ci.name, args: args, v: ci.v}}, w...)
		}
	}
	return append([]*spat{wildPat}, w...)
}

func specialize(rows [][]*spat, ctor string, arity int) [][]*spat {
	var out [][]*spat
	for _, r := range rows {
		h := r[0]
		switch {
		case h.wild:
			args := make([]*spat, arity)
			for i := range args {
				args[i] = wildPat
			}
			out = append(out, append(args, r[1:]...))
		case h.ctor == ctor:
			out = append(out, append(append([]*spat{}, h.args...), r[1:]...))
		}
	}
	return out
}

// checkExhaustive reports E0420 when the arms of e do not cover st.
func (c *Checker) checkExhaustive(e *ast.MatchExpr, st Type) {
	var rows [][]*spat
	var heads []string
	seenHead := map[string]bool{}
	for i := range e.Arms {
		arm := &e.Arms[i]
		if arm.Guard != nil {
			continue
		}
		for _, sp := range c.simplify(arm.Pat, st) {
			rows = append(rows, []*spat{sp})
			h := headString(sp)
			if !seenHead[h] {
				seenHead[h] = true
				heads = append(heads, "`"+h+"`")
			}
		}
	}
	w := c.witness(rows, []Type{st})
	if w == nil {
		return
	}
	dg := c.errorf("E0420", e.Pos, "non-exhaustive match on `%s`", TypeString(st))
	missing := witnessString(w[0], false)
	if len(heads) == 0 {
		dg.Label("missing `%s`", missing)
	} else {
		dg.Label("arms cover %s; missing `%s`", strings.Join(heads, ", "), missing)
	}
	if tc, ok := Resolve(st).(*TCon); ok && tc.Info.Kind == Enum {
		dg.Note("`match` must cover every variant of `%s`", TypeString(st))
	} else {
		dg.Note("`match` must cover every value of `%s`", TypeString(st))
	}
	dg.Helpf("add an arm `%s => …` before the closing `}` on line %d", witnessString(w[0], true), e.End.Line)
}

// headString prints the top-level constructor of a row with wildcard
// arguments, as in `Cube(_)` or `Box { .. }`.
func headString(p *spat) string {
	if p.wild {
		return "_"
	}
	if p.v != nil {
		switch {
		case len(p.v.Named) > 0:
			return p.v.Name + " { .. }"
		case len(p.v.Positional) > 0:
			return p.v.Name + "(" + strings.TrimSuffix(strings.Repeat("_, ", len(p.v.Positional)), ", ") + ")"
		}
		return p.v.Name
	}
	if p.ctor == "()" {
		if len(p.args) == 0 {
			return "()"
		}
		return "(" + strings.TrimSuffix(strings.Repeat("_, ", len(p.args)), ", ") + ")"
	}
	return p.ctor
}

// witnessString prints a witness. With fields set, named-field variants show
// their field names (for the help text); otherwise `{ .. }`.
func witnessString(p *spat, fields bool) string {
	if p.wild {
		return "_"
	}
	if p.v != nil {
		switch {
		case len(p.v.Named) > 0:
			if !fields {
				return p.v.Name + " { .. }"
			}
			var names []string
			for _, f := range p.v.Named {
				names = append(names, f.Name)
			}
			return p.v.Name + " { " + strings.Join(names, ", ") + " }"
		case len(p.v.Positional) > 0:
			var as []string
			for _, a := range p.args {
				as = append(as, witnessString(a, fields))
			}
			return p.v.Name + "(" + strings.Join(as, ", ") + ")"
		}
		return p.v.Name
	}
	if p.ctor == "()" {
		var as []string
		for _, a := range p.args {
			as = append(as, witnessString(a, fields))
		}
		if len(as) == 1 {
			return "(" + as[0] + ",)"
		}
		return "(" + strings.Join(as, ", ") + ")"
	}
	return p.ctor
}
