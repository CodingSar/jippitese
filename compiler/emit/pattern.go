package emit

import (
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
)

// pattern compiles p against the pure Go expression subj: the conditions
// that must hold (joined with &&) and the binding statements to run after
// they hold.
func (g *gen) pattern(p ast.Pattern, subj string, t checker.Type) (conds, binds []string) {
	pi := g.prog.Pats[p]
	if pi != nil && pi.Type != nil {
		t = pi.Type
	}
	switch x := p.(type) {
	case *ast.WildPat:
	case *ast.BindPat:
		name := g.localGo(g.prog.PatBinds[x], x.Name)
		binds = append(binds, name+" := "+subj+"\n_ = "+name+"\n")
	case *ast.AtPat:
		name := g.localGo(g.prog.AtBinds[x], x.Name)
		binds = append(binds, name+" := "+subj+"\n_ = "+name+"\n")
		c, b := g.pattern(x.Pat, subj, t)
		conds, binds = append(conds, c...), append(binds, b...)
	case *ast.LitPat:
		stmts, v, _ := g.capture(func() string { return g.expr(x.Lit) })
		if stmts != "" {
			g.e.failf("literal pattern needs statements")
		}
		if g.goComparable(t) {
			conds = append(conds, subj+" == "+v)
		} else {
			conds = append(conds, g.rt()+".Equal("+subj+", "+v+")")
		}
	case *ast.TuplePat:
		tt, _ := checker.Zonk(t).(*checker.TTuple)
		for i, sp := range x.Elems {
			var et checker.Type
			if tt != nil && i < len(tt.Elems) {
				et = tt.Elems[i]
			}
			c, b := g.pattern(sp, subj+".F"+strconv.Itoa(i), et)
			conds, binds = append(conds, c...), append(binds, b...)
		}
	case *ast.VariantPat:
		if pi != nil && pi.Variant != nil {
			v := pi.Variant
			conds = append(conds, subj+".Tag == "+g.tagConst(v))
			for i, sp := range x.Positional {
				c, b := g.pattern(sp, subj+"."+variantField(v, i), nil)
				conds, binds = append(conds, c...), append(binds, b...)
			}
			for _, fp := range x.Named {
				for i, nf := range v.Named {
					if nf.Name == fp.Name {
						c, b := g.pattern(fp.Pat, subj+"."+variantField(v, i), nil)
						conds, binds = append(conds, c...), append(binds, b...)
					}
				}
			}
			return
		}
		if pi != nil && pi.Newtype != nil && len(x.Positional) == 1 {
			inner := g.f.goType(pi.Newtype.Inner) + "(" + subj + ")"
			return g.pattern(x.Positional[0], inner, nil)
		}
		g.e.failf("unresolved variant pattern at %s", x.Pos)
	case *ast.RecordPat:
		var v *checker.VariantInfo
		if pi != nil {
			v = pi.Variant
		}
		if v != nil {
			conds = append(conds, subj+".Tag == "+g.tagConst(v))
		}
		for _, fp := range x.Fields {
			path := subj + "." + fieldName(fp.Name)
			if v != nil {
				for i, nf := range v.Named {
					if nf.Name == fp.Name {
						path = subj + "." + variantField(v, i)
					}
				}
			}
			c, b := g.pattern(fp.Pat, path, nil)
			conds, binds = append(conds, c...), append(binds, b...)
		}
	case *ast.OrPat:
		var alts []string
		for _, a := range x.Alts {
			c, b := g.pattern(a, subj, t)
			if len(b) > 0 {
				g.e.failf("or-pattern with bindings at %s", x.Pos)
			}
			if len(c) == 0 {
				alts = append(alts, "true")
			} else {
				alts = append(alts, "("+strings.Join(c, " && ")+")")
			}
		}
		conds = append(conds, "("+strings.Join(alts, " || ")+")")
	default:
		g.e.failf("pattern %T", p)
	}
	return
}

// match lowers a match: the subject goes to a temp, the arms to an if/else
// chain; arms with guards use a done flag so a failed guard falls through.
func (g *gen) match(x *ast.MatchExpr, s sink) {
	st := g.typeOf(x.X)
	v := g.expr(x.X)
	if g.dead {
		return
	}
	subj := g.newTmp()
	g.printf("%s := %s\n_ = %s\n", subj, v, subj)
	guarded := false
	for _, a := range x.Arms {
		if a.Guard != nil {
			guarded = true
		}
	}
	if guarded {
		g.matchGuarded(x, subj, st, s)
		return
	}
	allDead := true
	for i, a := range x.Arms {
		conds, binds := g.pattern(a.Pat, subj, st)
		cond := strings.Join(conds, " && ")
		// The last arm, or any irrefutable arm, ends the chain: the
		// checker proved exhaustiveness, so it needs no test.
		last := i == len(x.Arms)-1 || cond == ""
		switch {
		case i == 0 && last:
			g.printf("{\n")
		case i == 0:
			g.printf("if %s {\n", cond)
		case last:
			g.printf("} else {\n")
		default:
			g.printf("} else if %s {\n", cond)
		}
		g.dead = false
		g.line(a.Pos)
		for _, b := range binds {
			g.printf("%s", b)
		}
		g.lowerInto(a.Body, s)
		allDead = allDead && g.dead
		g.dead = false
		if last {
			break
		}
	}
	g.printf("}\n")
	if allDead {
		g.dead = true
	}
}

func (g *gen) matchGuarded(x *ast.MatchExpr, subj string, st checker.Type, s sink) {
	done := g.newTmp()
	g.printf("%s := false\n", done)
	allDead := true
	for i, a := range x.Arms {
		conds, binds := g.pattern(a.Pat, subj, st)
		if i > 0 {
			conds = append([]string{"!" + done}, conds...)
		}
		cond := strings.Join(conds, " && ")
		if cond == "" {
			cond = "true"
		}
		g.printf("if %s {\n", cond)
		g.dead = false
		for _, b := range binds {
			g.printf("%s", b)
		}
		closeGuard := false
		if a.Guard != nil {
			gs, gv, _ := g.capture(func() string { return g.expr(a.Guard) })
			g.printf("%sif %s {\n", gs, gv)
			closeGuard = true
		}
		g.printf("%s = true\n", done)
		g.lowerInto(a.Body, s)
		allDead = allDead && g.dead
		g.dead = false
		if closeGuard {
			g.printf("}\n")
		}
		g.printf("}\n")
	}
	if allDead {
		g.printf("panic(\"unreachable\")\n")
		g.dead = true
	}
}
