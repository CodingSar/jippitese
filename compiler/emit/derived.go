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
			return ti.Mod == "prelude" && ti.Name == "List"
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
	if len(fns) == 0 {
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
		// The draw closure generates every parameter, then evaluates the
		// `pre` clauses on them; a false clause redraws (rule 15).
		s := g.sub(nil)
		for i := range d.Params {
			s.printf("%s = (%s)(ctx, gn)\n", names[i], g.genFor(fi.Type.Params[i]))
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
