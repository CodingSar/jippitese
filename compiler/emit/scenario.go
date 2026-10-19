package emit

import (
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
)

// emitScenarios writes one scenario module as a test file of its parent
// package (spec 12): one Go test per scenario, driven by rt.Scenario.
func (e *emitter) emitScenarios(p *pkg, sm *checker.Module) {
	name := "scenarios_test.go"
	for _, f := range p.files {
		if f.name == name {
			name = "scenarios_" + sanitize(strings.ReplaceAll(sm.Path, ".", "_")) + "_test.go"
		}
	}
	f := p.newFile(name, true)
	tst := f.use("testing", "testing")
	rtp := f.rt()
	for _, si := range sm.Scenarios {
		sc := si.Decl
		f.printf("\nfunc TestScenario_%d_%s(t *%s.T) {\n", si.Index, sanitize(slugName(sc.Name)), tst)
		var tags []string
		for _, tg := range sc.Tags {
			if tg.Arg != "" {
				tags = append(tags, strconv.Quote(tg.Name+":"+tg.Arg))
			} else {
				tags = append(tags, strconv.Quote(tg.Name))
			}
		}
		f.line(sc.Pos)
		f.printf("%s.Scenario(t, %s.ScnOpts{Module: %q, Name: %q, Tags: []string{%s}, Property: %v, File: %q, Line: %d, Col: %d}, func(scn *%s.Scn) {\n",
			rtp, rtp, sm.Parent.Path, sc.Name, strings.Join(tags, ", "), isProperty(sc), sc.Pos.File, sc.Pos.Line, sc.Pos.Col, rtp)
		f.printf("ctx := scn.Ctx\nctx.Config = %s\n", f.cfgRef("TestConfig()"))
		g := newGen(f, nil)
		g.scenario = true
		loops := 0
		for i := range sc.Givens {
			gv := &sc.Givens[i]
			g.line(gv.Pos)
			switch gv.Kind {
			case ast.GivenExpr, ast.GivenAny:
				l := g.prog.GivenLocals[gv]
				var x ast.Expr = gv.Expr
				if gv.Kind == ast.GivenAny {
					x = gv.Any
				}
				g.printf("scn.Given(%q, %q)\n", gv.Pos.String(), gv.Name+" = "+checker.ExprString(x))
				v := g.expr(x)
				n := g.localGo(l, gv.Name)
				g.printf("%s := %s\n_ = %s\nscn.Bind(%q, %s)\nscn.Install(%s)\n", n, v, n, gv.Name, n, n)
			case ast.GivenTable:
				g.printf("scn.Given(%q, %q)\n", gv.Pos.String(), checker.PatString(gv.Pat)+" in "+checker.ExprString(gv.Rows))
				rows := g.expr(gv.Rows)
				el := g.newTmp()
				g.printf("for _, %s := range %s {\n", el, rows)
				_, binds := g.pattern(gv.Pat, el, g.elemType(g.typeOf(gv.Rows)))
				for _, b := range binds {
					g.printf("%s", b)
				}
				loops++
			case ast.GivenRealTime:
				g.printf("scn.RealTime()\n")
			}
		}
		for i := range sc.Whens {
			w := &sc.Whens[i]
			g.line(w.Pos)
			g.printf("scn.When(%q, %q)\n", w.Pos.String(), w.Name+" = "+checker.ExprString(w.Expr))
			v := g.expr(w.Expr)
			if w.Name == "_" {
				g.discard(v)
				continue
			}
			n := g.localGo(g.prog.WhenLocals[w], w.Name)
			g.printf("%s := %s\n_ = %s\nscn.Bind(%q, %s)\n", n, v, n, w.Name, n)
		}
		for _, th := range sc.Thens {
			g.line(th.ExprPos())
			stmts, v, _ := g.capture(func() string { return g.expr(th) })
			g.printf("%sscn.Then(%q, %q, %s)\n", stmts, th.ExprPos().String(), checker.ExprString(th), v)
		}
		for ; loops > 0; loops-- {
			g.printf("}\n")
		}
		f.body.WriteString(g.w.String())
		f.printf("})\n}\n")
	}
}

func slugName(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			if up {
				b.WriteString(strings.ToUpper(string(r)))
			} else {
				b.WriteRune(r)
			}
			up = false
		} else {
			up = true
		}
	}
	return b.String()
}

func isProperty(sc *ast.Scenario) bool {
	for _, gv := range sc.Givens {
		if gv.Kind == ast.GivenAny {
			return true
		}
	}
	return false
}

// anyExpr draws a value: through the `via` generator, or the structural
// generator of the type.
func (g *gen) anyExpr(x *ast.AnyExpr) string {
	t := g.prog.AnyTypes[x]
	var gen string
	if x.Via != nil {
		gen = g.expr(x.Via)
	} else {
		gen = g.genFor(t)
	}
	return g.rt() + ".Any[" + g.f.goType(t) + "](scn, " + strconv.Quote(x.Pos.String()) + ", " + gen + ")"
}

// genFor is the structural generator of t (05 rule 15) as a Go function
// value `func(*rt.Ctx, *rt.Gen) T`. Candidates that break an `inv` are
// redrawn.
func (g *gen) genFor(t checker.Type) string {
	rtp := g.rt()
	gt := g.f.goType(t)
	head := "func(ctx *" + rtp + ".Ctx, gn *" + rtp + ".Gen) " + gt + " {\n"
	t = checker.Zonk(t)
	switch x := t.(type) {
	case *checker.TTuple:
		if len(x.Elems) == 0 {
			return head + "return " + rtp + ".U\n}"
		}
		parts := make([]string, len(x.Elems))
		for i, el := range x.Elems {
			parts[i] = "F" + strconv.Itoa(i) + ": (" + g.genFor(el) + ")(ctx, gn)"
		}
		return head + "return " + gt + "{" + strings.Join(parts, ", ") + "}\n}"
	case *checker.TCon:
		ti := x.Info
		m := map[*checker.TParam]checker.Type{}
		for i, p := range ti.Params {
			if i < len(x.Args) {
				m[p] = x.Args[i]
			}
		}
		switch ti.Kind {
		case checker.Prim:
			return rtp + ".GenAny" + ti.Name
		case checker.Record:
			var parts []string
			for _, fl := range ti.Fields {
				parts = append(parts, fieldName(fl.Name)+": ("+g.genFor(checker.Subst(fl.Type, m))+")(ctx, gn)")
			}
			return head + g.retry(ti, gt, gt+"{"+strings.Join(parts, ", ")+"}") + "}"
		case checker.Newtype:
			return head + g.retry(ti, gt, gt+"(("+g.genFor(checker.Subst(ti.Inner, m))+")(ctx, gn))") + "}"
		case checker.Enum:
			var b strings.Builder
			b.WriteString(head)
			b.WriteString("switch " + rtp + ".GenInt(ctx, gn, 0, " + strconv.Itoa(len(ti.Variants)-1) + ") {\n")
			for i, v := range ti.Variants {
				var args []string
				for _, pt := range v.Positional {
					args = append(args, "("+g.genFor(checker.Subst(pt, m))+")(ctx, gn)")
				}
				for _, nf := range v.Named {
					args = append(args, "("+g.genFor(checker.Subst(nf.Type, m))+")(ctx, gn)")
				}
				ctor := g.f.typeRef(ti) + "_" + v.Name + g.f.typeArgs(x.Args)
				if i == len(ti.Variants)-1 {
					b.WriteString("default:\n")
				} else {
					b.WriteString("case " + strconv.Itoa(i) + ":\n")
				}
				b.WriteString("return " + ctor + "(" + strings.Join(args, ", ") + ")\n")
			}
			b.WriteString("}\n}")
			return b.String()
		case checker.Extern:
			if ti.Mod == "prelude" && ti.Name == "List" {
				return head + "return " + rtp + ".GenList(ctx, gn, int64(gn.GenLen(8)), " + g.genFor(x.Args[0]) + ")\n}"
			}
		}
	}
	g.e.failf("no generator for `%s`", checker.TypeString(t))
	return ""
}

// retry wraps a candidate expression in the redraw loop of an `inv`.
func (g *gen) retry(ti *checker.TypeInfo, gt, cand string) string {
	ok := g.f.invRef(ti)
	if ok == "" {
		return "return " + cand + "\n"
	}
	return "for i := 0; i < 1000; i++ {\nv := " + cand + "\nif " + ok + "OK(ctx, v) {\nreturn v\n}\n}\npanic(\"no candidate of " + ti.Name + " satisfies its inv\")\n"
}
