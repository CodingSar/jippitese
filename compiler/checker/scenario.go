package checker

import (
	"github.com/codingsar/jippitese/compiler/ast"
)

// checkScenario checks one scenario (spec 12). Scenario bodies have no
// return type: `?` fails the scenario, effects are not tracked, and the
// `then` expressions must be `Bool`.
func (c *Checker) checkScenario(s *ScenarioInfo) {
	d := s.Decl
	c.fn = c.newFnCtx(nil, s.Module)
	c.pushScope()
	for i := range d.Givens {
		g := &d.Givens[i]
		switch g.Kind {
		case ast.GivenExpr:
			t := c.infer(g.Expr, nil)
			l := c.declareLocal(&Local{Name: g.Name, Type: t, Pos: g.Pos})
			c.prog.GivenLocals[g] = l
		case ast.GivenAny:
			t := c.inferAny(g.Any)
			l := c.declareLocal(&Local{Name: g.Name, Type: t, Pos: g.Pos})
			c.prog.GivenLocals[g] = l
		case ast.GivenTable:
			elem := c.newVar()
			want := con(c.preludeType("List"), elem)
			t := c.infer(g.Rows, want)
			if !c.u.tryUnify(t, want) {
				dg := c.errorf("E0803", g.Rows.ExprPos(), "table rows must be a `List`")
				dg.Label("found `%s`", TypeString(t))
			} else if tp, ok := g.Pat.(*ast.TuplePat); ok {
				if et, ok := Resolve(elem).(*TTuple); ok && len(et.Elems) != len(tp.Elems) {
					dg := c.errorf("E0803", tp.Pos, "table pattern has %d element(s), rows have %d", len(tp.Elems), len(et.Elems))
					dg.Label("pattern `%s`", patString(g.Pat))
					dg.Helpf("bind one name per column: %d names for %d columns", len(et.Elems), len(et.Elems))
					c.bindAll(g.Pat)
					continue
				}
			}
			c.checkPattern(g.Pat, elem)
			if c.refutable(g.Pat, elem) {
				dg := c.errorf("E0803", g.Pat.PatPos(), "table pattern must be irrefutable")
				dg.Label("`%s` can fail to match", patString(g.Pat))
			}
		case ast.GivenRealTime:
			// `given real_time` only affects the runtime.
		}
	}
	for i := range d.Whens {
		w := &d.Whens[i]
		t := c.infer(w.Expr, nil)
		if w.Name == "_" || w.Name == "" {
			continue
		}
		l := c.declareLocal(&Local{Name: w.Name, Type: t, Pos: w.Pos})
		c.prog.WhenLocals[w] = l
	}
	for _, e := range d.Thens {
		t := c.infer(e, con(tBool))
		if !c.u.tryUnify(t, con(tBool)) {
			dg := c.errorf("E0801", e.ExprPos(), "`then` expects `Bool`")
			dg.Label("found `%s`", TypeString(t))
			if tc, ok := Resolve(t).(*TCon); ok && tc.Info.Mod == "prelude" {
				switch tc.Info.Name {
				case "Result":
					dg.Helpf("replace `%s` with `%s.is_ok()`", exprString(e), exprString(e))
				case "Option":
					dg.Helpf("replace `%s` with `%s.is_some()`", exprString(e), exprString(e))
				}
			}
		}
	}
	c.popScope()
	c.finishFn(nil)
	c.fn = nil
}

// scenarioBuiltin types `run_workflow(Impl, input)`, `tick(Impl)` and
// `send_signal(Impl, signal)` (spec 12 rule 11). The first argument names a
// type with the matching behaviour impl.
func (c *Checker) scenarioBuiltin(name string, e *ast.CallExpr, info *CallInfo) *TFn {
	var impl *ImplInfo
	var target Type = c.newVar()
	bname := map[string]string{"run_workflow": "Workflow", "record_workflow": "Workflow", "replay_workflow": "Workflow", "tick": "Cron", "send_signal": "Workflow"}[name]
	if len(e.Args) > 0 {
		if id, ok := e.Args[0].(*ast.Ident); ok {
			ti := c.lookupTypeInfo(c.fn.module, id.Name)
			if ti == nil {
				c.errorf("E0404", id.Pos, "unknown type `%s`", id.Name)
			} else if impl = behaviourImplOf(ti, bname); impl == nil {
				dg := c.errorf("E0404", id.Pos, "`%s` does not implement `%s`", id.Name, bname)
				dg.Helpf("`%s` takes a type with `impl %s for %s`", name, bname, id.Name)
			} else {
				c.prog.Refs[id] = &Ref{Kind: RefImpl, Impl: impl, Type: ti}
				target = impl.Target
			}
		} else {
			dg := c.errorf("E0404", e.Args[0].ExprPos(), "`%s` expects a type name as its first argument", name)
			dg.Helpf("write the implementing type, e.g. `%s(Checkout, …)`", name)
		}
	}
	info.Impl = impl
	assoc := func(n string) Type {
		if impl != nil {
			if t, ok := impl.Assoc[n]; ok {
				return t
			}
		}
		return c.newVar()
	}
	switch name {
	case "run_workflow":
		ret := con(c.preludeType("Result"), assoc("Output"), con(c.preludeType("WorkflowError"), assoc("E")))
		return &TFn{Params: []Type{target, assoc("Input")}, Ret: ret}
	case "record_workflow":
		ret := con(c.preludeType("Result"), assoc("Output"), con(c.preludeType("WorkflowError"), assoc("E")))
		return &TFn{Params: []Type{target, assoc("Input")}, Ret: &TTuple{Elems: []Type{ret, c.journalType()}}}
	case "replay_workflow":
		ret := con(c.preludeType("Result"), assoc("Output"), con(c.preludeType("WorkflowError"), assoc("E")))
		return &TFn{Params: []Type{target, assoc("Input"), c.journalType()}, Ret: ret}
	case "tick":
		return &TFn{Params: []Type{target}, Ret: con(c.preludeType("Result"), unit, assoc("E"))}
	default:
		return &TFn{Params: []Type{target, assoc("Signal")}, Ret: unit}
	}
}

// journalType is `std.testing.Journal`, the history record_workflow
// returns.
func (c *Checker) journalType() Type {
	if c.prog.Testing != nil {
		if ti := c.prog.Testing.Types["Journal"]; ti != nil {
			return con(ti)
		}
	}
	return c.newVar()
}
