package checker

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/diag"
)

// ---- function context ------------------------------------------------------

func (c *Checker) newFnCtx(info *FnInfo, m *Module) *fnCtx {
	return &fnCtx{
		info:       info,
		module:     m,
		used:       map[string]ast.Pos{},
		goNames:    map[string]int{},
		localDepth: map[*Local]int{},
	}
}

func (c *Checker) pushScope() { c.fn.scopes = append(c.fn.scopes, map[string]*Local{}) }
func (c *Checker) popScope()  { c.fn.scopes = c.fn.scopes[:len(c.fn.scopes)-1] }

func (c *Checker) declareLocal(l *Local) *Local {
	f := c.fn
	if len(f.scopes) == 0 {
		c.pushScope()
	}
	n := f.goNames[l.Name]
	f.goNames[l.Name] = n + 1
	if n == 0 {
		l.Go = l.Name
	} else {
		l.Go = fmt.Sprintf("%s_%d", l.Name, n)
	}
	f.scopes[len(f.scopes)-1][l.Name] = l
	f.locals = append(f.locals, l)
	f.localDepth[l] = len(f.scopes)
	return l
}

func (c *Checker) lookupLocal(name string) *Local {
	if c.fn == nil {
		return nil
	}
	for i := len(c.fn.scopes) - 1; i >= 0; i-- {
		if l, ok := c.fn.scopes[i][name]; ok {
			return l
		}
	}
	return nil
}

// record stores the type of an expression.
func (c *Checker) record(e ast.Expr, t Type) Type {
	c.prog.Types[e] = t
	if c.fn != nil {
		c.fn.recorded = append(c.fn.recorded, e)
	}
	return t
}

// ---- effects ----------------------------------------------------------------

// useEffect registers that the current body performs e at pos. what names
// the callee for diagnostics.
func (c *Checker) useEffect(e ast.Effect, pos ast.Pos, what string) {
	f := c.fn
	if f == nil {
		return
	}
	if f.inContract {
		dg := c.errorf("E0601", pos, "effect in contract")
		dg.Label("`%s` has effect {%s}; contracts are typed with {}", what, e.String())
		if f.contractWhat != "" {
			dg.Note("%s", f.contractWhat)
		}
		return
	}
	if len(f.sinks) > 0 {
		s := f.sinks[len(f.sinks)-1]
		s.effs = effAdd(s.effs, e)
		return
	}
	if f.ret == nil { // scenario body
		return
	}
	if f.budget != nil && !effContains(f.budget, e) {
		if f.budgetReported == nil {
			f.budgetReported = map[string]bool{}
		}
		if f.budgetReported[e.String()] {
			return
		}
		f.budgetReported[e.String()] = true
		if f.isWorkflowRun {
			dg := c.errorf("E0703", pos, "effect `%s` is not allowed inside `Workflow.run`", e.String())
			dg.Label("`%s` has effect `%s`", what, e.String())
			dg.Note("`run` may use %s", EffectSetString(f.budget))
			dg.Helpf("keep workflow state in a `var` or return it from a step")
		} else {
			b := f.info.Impl.Behaviour
			dg := c.errorf("E0702", pos, "effect `%s` exceeds the budget of `%s.%s`", e.String(), b.Name, f.info.Name)
			dg.Label("not in %s", EffectSetString(f.budget))
			dg.Helpf("%s", c.budgetHelp(e, b))
		}
		return
	}
	if !effContains(f.declared, e) {
		dg := c.errorf("E0412", pos, "missing effect `%s`", e.String())
		dg.Label("`%s` has effect `%s`", what, e.String())
		if !f.hasWith {
			dg.Note("`%s` declares no effect set", f.info.Name)
		} else {
			dg.Note("`%s` declares `with %s`", f.info.Name, EffectSetString(f.declared))
		}
		want := SortEffects(effAdd(append([]ast.Effect{}, f.declared...), e))
		if isUnit(f.ret) {
			dg.Helpf("change the signature to `with %s`", EffectSetString(want))
		} else {
			dg.Helpf("change the signature to `-> %s with %s`", TypeString(f.ret), EffectSetString(want))
		}
		// Record it anyway so a second use does not repeat the error.
		f.declared = append(f.declared, e)
		return
	}
	f.used[e.String()] = pos
}

// budgetHelp finds another prelude behaviour whose corresponding method
// admits e.
func (c *Checker) budgetHelp(e ast.Effect, b *BehaviourInfo) string {
	for _, name := range []string{"Service", "Worker", "Cron", "Workflow"} {
		if name == b.Name {
			continue
		}
		ob := c.prelude.Behaviours[name]
		if ob == nil {
			continue
		}
		for _, mn := range ob.MethodOrder {
			m := ob.Methods[mn]
			if m.Decl != nil && m.Decl.Body == nil && effContains(m.Type.Effects, e) {
				return fmt.Sprintf("remove `%s`, or implement `%s`, whose `%s` budget includes it", e.String(), name, mn)
			}
		}
	}
	return fmt.Sprintf("remove `%s`", e.String())
}

// finishFn runs the end-of-body checks shared by functions and methods.
func (c *Checker) finishFn(decl *ast.FnDecl) {
	f := c.fn
	for _, v := range f.diverge {
		if _, ok := Resolve(v).(*TVar); ok {
			c.u.unify(v, unit)
		}
	}
	for _, e := range f.recorded {
		defaultNumeric(c.prog.Types[e])
	}
	for _, l := range f.locals {
		defaultNumeric(l.Type)
	}
	reported := false
	for _, e := range f.recorded {
		if hasVars(c.prog.Types[e]) && !reported {
			if _, isBlock := e.(*ast.Block); isBlock {
				continue
			}
			c.errorf("E0403", e.ExprPos(), "cannot infer the type of this expression; add a type annotation")
			reported = true
		}
	}
	if decl != nil && decl.Body != nil && f.budget == nil && f.ret != nil {
		for _, d := range decl.Effects {
			usedOK := false
			for k, pos := range f.used {
				_ = pos
				if used, ok := parseEffectKey(k); ok && effContains([]ast.Effect{d}, used) {
					usedOK = true
					break
				}
			}
			if usedOK {
				continue
			}
			dg := c.warnf("W0401", d.Pos, "unused effect `%s`", d.String())
			dg.Label("declared, never used in the body")
			var rest []ast.Effect
			for _, o := range decl.Effects {
				if o.String() != d.String() {
					rest = append(rest, o)
				}
			}
			switch {
			case len(rest) == 0:
				dg.Helpf("remove the `with` clause")
			case isUnit(f.ret):
				dg.Helpf("change the signature to `with %s`", EffectSetString(rest))
			default:
				dg.Helpf("change the signature to `-> %s with %s`", TypeString(f.ret), EffectSetString(rest))
			}
		}
	}
	if decl != nil {
		var used []ast.Effect
		for k := range f.used {
			if e, ok := parseEffectKey(k); ok {
				used = effAdd(used, e)
			}
		}
		c.prog.FnEffects[decl] = SortEffects(used)
	}
	c.checkSchemas()
}

func parseEffectKey(k string) (ast.Effect, bool) {
	if i := strings.IndexByte(k, '<'); i >= 0 && strings.HasSuffix(k, ">") {
		return ast.Effect{Name: k[:i], Role: k[i+1 : len(k)-1]}, true
	}
	if k == "" {
		return ast.Effect{}, false
	}
	return ast.Effect{Name: k}, true
}

// ---- functions and consts ---------------------------------------------------

func (c *Checker) checkConst(ci *ConstInfo) {
	if ci.Decl == nil || ci.Decl.Value == nil {
		return
	}
	c.fn = c.newFnCtx(nil, ci.Module)
	c.fn.ret = ci.Type
	c.fn.inContract = true
	c.fn.contractWhat = fmt.Sprintf("const `%s` must be a pure expression", ci.Name)
	c.pushScope()
	t := c.infer(ci.Decl.Value, ci.Type)
	c.expect(ci.Decl.Value, t, ci.Type, "")
	c.popScope()
	c.finishFn(nil)
	c.fn = nil
}

// checkFn checks a free function or an inherent method body.
func (c *Checker) checkFn(fi *FnInfo) {
	c.checkFnBody(fi, nil, false)
}

// checkFnBody checks one body; budget is the behaviour budget (nil outside
// behaviour impls).
func (c *Checker) checkFnBody(fi *FnInfo, budget []ast.Effect, isRun bool) *fnCtx {
	d := fi.Decl
	if d == nil || d.Body == nil && d.Extern == nil {
		return nil
	}
	c.fn = c.newFnCtx(fi, fi.Module)
	f := c.fn
	f.ret = fi.Type.Ret
	f.declared = append([]ast.Effect{}, d.Effects...)
	f.hasWith = d.HasWith
	f.budget = budget
	f.isWorkflowRun = isRun
	c.pushScope()
	c.bindParams(fi)
	c.checkContracts(fi)
	if d.Body != nil {
		t := c.inferBlock(d.Body, f.ret)
		c.expectRet(d.Body, t, f.ret, fi.Name)
	}
	c.popScope()
	if d.Extern == nil {
		c.finishFn(d)
	}
	c.fn = nil
	return f
}

func (c *Checker) bindParams(fi *FnInfo) {
	for i := range fi.Decl.Params {
		p := &fi.Decl.Params[i]
		var t Type
		if i < len(fi.Type.Params) {
			t = fi.Type.Params[i]
		} else {
			t = c.newVar()
		}
		name := p.Name
		if p.Self {
			name = "self"
		}
		l := &Local{Name: name, Type: t, Pos: p.Pos}
		if ft, ok := Resolve(t).(*TFn); ok {
			l.Fn = ft
			l.Explicit = ft.Explicit
			l.Effects = ft.Effects
		}
		c.declareLocal(l)
		c.prog.Params[p] = l
	}
}

// checkContracts checks pre and post of a function.
func (c *Checker) checkContracts(fi *FnInfo) {
	d := fi.Decl
	if len(d.Pre) == 0 && len(d.Post) == 0 {
		return
	}
	f := c.fn
	f.inContract = true
	for _, e := range d.Pre {
		t := c.infer(e, con(tBool))
		c.expect(e, t, con(tBool), "`pre` expects `Bool`")
	}
	if len(d.Post) > 0 {
		c.pushScope()
		f.inPost = true
		c.declareLocal(&Local{Name: "ret", Type: f.ret, Pos: d.Pos})
		for _, e := range d.Post {
			t := c.infer(e, con(tBool))
			c.expect(e, t, con(tBool), "`post` expects `Bool`")
		}
		f.inPost = false
		c.popScope()
	}
	f.inContract = false
}

// checkInv checks record or impl invariants with the fields in scope.
func (c *Checker) checkInv(info *TypeInfo, inv []ast.Expr, m *Module, impl bool) {
	if len(inv) == 0 {
		return
	}
	c.fn = c.newFnCtx(nil, m)
	f := c.fn
	f.ret = con(tBool)
	f.inContract = true
	if impl {
		f.contractWhat = "a `Cell` field in an `impl` inv reads as its payload"
	}
	c.pushScope()
	c.declareLocal(&Local{Name: "self", Type: con(info), Pos: info.Pos})
	for _, fd := range info.Fields {
		t := fd.Type
		if impl {
			if tc, ok := Resolve(t).(*TCon); ok && tc.Info.Name == "Cell" && tc.Info.Mod == "prelude" && len(tc.Args) == 1 {
				t = tc.Args[0]
			}
		}
		c.declareLocal(&Local{Name: fd.Name, Type: t, Pos: fd.Pos})
	}
	for _, e := range inv {
		t := c.infer(e, con(tBool))
		c.expect(e, t, con(tBool), "`inv` expects `Bool`")
	}
	c.popScope()
	c.finishFn(nil)
	c.fn = nil
}

// ---- expectations -----------------------------------------------------------

// expect unifies found with want, reporting E0402 on failure.
func (c *Checker) expect(e ast.Expr, found, want Type, what string) bool {
	if want == nil || found == nil {
		return true
	}
	if c.u.tryUnify(found, want) {
		return true
	}
	defaultNumeric(found)
	dg := c.errorf("E0402", e.ExprPos(), "type mismatch")
	dg.Label("expected `%s`, found `%s`", TypeString(want), TypeString(found))
	if what != "" {
		dg.Note("%s", what)
	}
	c.newtypeHelp(dg, e, found, want, nil)
	return false
}

// expectRet reports a body/return type mismatch.
func (c *Checker) expectRet(e ast.Expr, found, want Type, fn string) {
	if c.u.tryUnify(found, want) {
		return
	}
	pos := e.ExprPos()
	if b, ok := e.(*ast.Block); ok && b.Tail != nil {
		pos = b.Tail.ExprPos()
	}
	defaultNumeric(found)
	dg := c.errorf("E0402", pos, "type mismatch")
	dg.Label("expected `%s`, found `%s`", TypeString(want), TypeString(found))
	dg.Note("`%s` returns `%s`; there is no implicit conversion from `%s`", fn, TypeString(want), TypeString(found))
}

// newtypeHelp adds the "wrap the value" help when want is a newtype over found.
func (c *Checker) newtypeHelp(dg *diag.Diagnostic, e ast.Expr, found, want Type, call *ast.CallExpr) {
	tc, ok := Resolve(want).(*TCon)
	if !ok || tc.Info == nil || tc.Info.Kind != Newtype || tc.Info.Inner == nil {
		return
	}
	inner := subst(tc.Info.Inner, typeArgSubst(tc))
	if !c.u.probe(found, inner) {
		return
	}
	wrapped := fmt.Sprintf("%s(%s)", tc.Info.Name, exprString(e))
	if call != nil {
		for i, a := range call.Args {
			if a == e {
				dg.Helpf("wrap the value: `%s`", callWithArg(call, i, wrapped))
				return
			}
		}
	}
	dg.Helpf("wrap the value: `%s`", wrapped)
}

// ---- blocks and statements --------------------------------------------------

func (c *Checker) inferBlock(b *ast.Block, want Type) Type {
	c.pushScope()
	for _, s := range b.Stmts {
		c.checkStmt(s)
	}
	var t Type
	if b.Tail != nil {
		t = c.infer(b.Tail, want)
	} else {
		t = unit
		if want != nil && len(b.Stmts) > 0 {
			// `{ …; return x }` and friends: a block whose last statement
			// diverges takes any type.
			if last, ok := b.Stmts[len(b.Stmts)-1].(*ast.ExprStmt); ok && diverges(last.X) {
				t = c.newVar()
				c.fn.diverge = append(c.fn.diverge, t.(*TVar))
			}
		}
	}
	c.popScope()
	return c.record(b, t)
}

func diverges(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.ReturnExpr, *ast.BreakExpr, *ast.ContinueExpr:
		return true
	case *ast.CallExpr:
		if id, ok := e.Fn.(*ast.Ident); ok && id.Name == "panic" {
			return true
		}
	}
	return false
}

func (c *Checker) checkStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.LetStmt:
		var want Type
		if s.Type != nil {
			want = c.resolveType(&tscope{m: c.fn.module, params: c.fnTypeParams()}, s.Type)
		}
		t := c.infer(s.Value, want)
		if want != nil {
			c.expect(s.Value, t, want, "")
			t = want
		}
		if bp, ok := s.Pat.(*ast.BindPat); ok {
			l := &Local{Name: bp.Name, Type: t, Pos: bp.Pos}
			if cl, isCl := unparen(s.Value).(*ast.Closure); isCl && c.prog.Closures[cl] != nil {
				ci := c.prog.Closures[cl]
				l.Fn = ci.Type
				l.Effects = ci.Effects
				l.Explicit = true
			} else if ft, ok := Resolve(t).(*TFn); ok {
				l.Fn = ft
				l.Effects = ft.Effects
				l.Explicit = ft.Explicit
			}
			c.declareLocal(l)
			c.prog.PatBinds[bp] = l
			c.prog.Pats[bp] = &PatInfo{Type: t}
			return
		}
		if c.refutable(s.Pat, t) {
			c.errorf("E0402", s.Pat.PatPos(), "refutable pattern in `let`").Helpf("use `match` or `let … else` is not available in v0; match on the value instead")
		}
		c.checkPattern(s.Pat, t)
	case *ast.VarStmt:
		var want Type
		if s.Type != nil {
			want = c.resolveType(&tscope{m: c.fn.module, params: c.fnTypeParams()}, s.Type)
		}
		t := c.infer(s.Value, want)
		if want != nil {
			c.expect(s.Value, t, want, "")
			t = want
		}
		l := &Local{Name: s.Name, Type: t, Var: true, Pos: s.Pos}
		c.declareLocal(l)
		c.prog.VarDefs[s] = l
	case *ast.AssignStmt:
		l := c.lookupLocal(s.Target.Name)
		if l == nil {
			c.errorf("E0407", s.Target.Pos, "unknown name `%s`", s.Target.Name)
			c.infer(s.Value, nil)
			return
		}
		c.prog.Refs[s.Target] = &Ref{Kind: RefLocal, Local: l}
		c.record(s.Target, l.Type)
		if !l.Var {
			dg := c.errorf("E0440", s.Target.Pos, "cannot assign to immutable binding `%s`", l.Name)
			dg.Label("`%s` was bound with `let` at line %d", l.Name, l.Pos.Line)
			dg.Helpf("change line %d to `var %s = …`, or bind a new name: `let next = …`", l.Pos.Line, l.Name)
		}
		c.checkSpawnCapture(l, s.Target.Pos)
		t := c.infer(s.Value, l.Type)
		c.expect(s.Value, t, l.Type, fmt.Sprintf("`%s` is `%s`; there is no implicit conversion from `%s`", l.Name, TypeString(l.Type), TypeString(t)))
		if s.Op != "=" {
			if !c.isNumericOrStr(l.Type, s.Op) {
				c.errorf("E0402", s.Pos, "operator `%s` is not defined on `%s`", s.Op, TypeString(l.Type))
			}
		}
	case *ast.ExprStmt:
		t := c.infer(s.X, nil)
		_ = t
	}
}

func unparen(e ast.Expr) ast.Expr { return e }

func (c *Checker) fnTypeParams() []*TParam {
	if c.fn == nil || c.fn.info == nil {
		return nil
	}
	return c.fn.info.TypeParams
}

func (c *Checker) isNumericOrStr(t Type, op string) bool {
	tc, ok := Resolve(t).(*TCon)
	if !ok {
		_, isVar := Resolve(t).(*TVar)
		return isVar
	}
	switch tc.Info {
	case tInt, tFloat, tDuration:
		return true
	case tStr:
		return op == "+=" || op == "+"
	}
	return false
}

// checkSpawnCapture reports E0450 when a `var` declared outside the
// innermost spawn block is used inside it.
func (c *Checker) checkSpawnCapture(l *Local, pos ast.Pos) {
	f := c.fn
	if !l.Var || len(f.spawnDepth) == 0 {
		return
	}
	if f.localDepth[l] <= f.spawnDepth[len(f.spawnDepth)-1] {
		dg := c.errorf("E0450", pos, "`spawn` block captures the `var` binding `%s`", l.Name)
		dg.Label("`%s` is a `var`, declared at %s", l.Name, l.Pos.String())
		dg.Note("a `spawn` block may capture only `let` bindings of immutable types, or a `Cell`")
		dg.Helpf("replace `var %s = …` with `let %s = Cell.new(…)` and write `%s.update(fn(v) => …)`", l.Name, l.Name, l.Name)
	}
}

// ---- expressions ------------------------------------------------------------

// infer computes the type of e. want, when non-nil, is the expected type
// and is used only to guide inference (literals, bare variants, closures);
// callers unify afterwards.
func (c *Checker) infer(e ast.Expr, want Type) Type {
	t := c.inferInner(e, want)
	if t == nil {
		t = c.newVar()
	}
	return c.record(e, t)
}

func (c *Checker) inferInner(e ast.Expr, want Type) Type {
	switch e := e.(type) {
	case *ast.IntLit:
		if want != nil {
			if tc, ok := Resolve(want).(*TCon); ok && (tc.Info == tInt || tc.Info == tFloat || tc.Info == tDuration) {
				return tc
			}
		}
		v := c.newVar()
		v.numeric = true
		return v
	case *ast.FloatLit:
		return con(tFloat)
	case *ast.BoolLit:
		return con(tBool)
	case *ast.CharLit:
		return con(tChar)
	case *ast.BytesLit:
		return con(tBytes)
	case *ast.DurationLit:
		return con(tDuration)
	case *ast.StrLit:
		for _, p := range e.Parts {
			if p.Expr != nil {
				c.infer(p.Expr, nil)
			}
		}
		return con(tStr)
	case *ast.Ident:
		return c.inferIdent(e, want)
	case *ast.SelfExpr:
		l := c.lookupLocal("self")
		if l == nil {
			c.errorf("E0407", e.Pos, "`self` outside a method")
			return c.newVar()
		}
		return l.Type
	case *ast.FieldExpr:
		return c.inferField(e, want)
	case *ast.CallExpr:
		return c.inferCall(e, want)
	case *ast.UnaryExpr:
		return c.inferUnary(e, want)
	case *ast.BinaryExpr:
		return c.inferBinary(e, want)
	case *ast.TryExpr:
		return c.inferTry(e, want)
	case *ast.Block:
		return c.inferBlock(e, want)
	case *ast.IfExpr:
		ct := c.infer(e.Cond, con(tBool))
		c.expect(e.Cond, ct, con(tBool), "`if` condition must be `Bool`")
		if e.Else == nil {
			c.inferBlock(e.Then, nil)
			return unit
		}
		tt := c.inferBlock(e.Then, want)
		et := c.infer(e.Else, tt)
		c.expect(e.Else, et, tt, "both branches of `if` must have the same type")
		return tt
	case *ast.MatchExpr:
		return c.inferMatch(e, want)
	case *ast.WhileExpr:
		ct := c.infer(e.Cond, con(tBool))
		c.expect(e.Cond, ct, con(tBool), "`while` condition must be `Bool`")
		c.fn.loops++
		c.inferBlock(e.Body, nil)
		c.fn.loops--
		return unit
	case *ast.ForExpr:
		it := c.infer(e.Iter, nil)
		elem := c.iterElem(it, e.Iter)
		c.pushScope()
		if bp, ok := e.Pat.(*ast.BindPat); ok {
			l := c.declareLocal(&Local{Name: bp.Name, Type: elem, Pos: bp.Pos})
			c.prog.PatBinds[bp] = l
			c.prog.Pats[bp] = &PatInfo{Type: elem}
			c.prog.ForLocals[e] = l
		} else {
			c.checkPattern(e.Pat, elem)
		}
		c.fn.loops++
		c.inferBlock(e.Body, nil)
		c.fn.loops--
		c.popScope()
		return unit
	case *ast.RangeExpr:
		lt := c.infer(e.Lo, con(tInt))
		c.expect(e.Lo, lt, con(tInt), "range bounds are `Int`")
		ht := c.infer(e.Hi, con(tInt))
		c.expect(e.Hi, ht, con(tInt), "range bounds are `Int`")
		return con(tRange, con(tInt))
	case *ast.ReturnExpr:
		if c.fn.ret == nil && len(c.fn.closureRets) == 0 {
			c.errorf("E0402", e.Pos, "`return` outside a function")
		}
		want := c.fn.ret
		if len(c.fn.closureRets) > 0 {
			want = c.fn.closureRets[len(c.fn.closureRets)-1]
		}
		if e.X != nil {
			t := c.infer(e.X, want)
			c.expect(e.X, t, want, "")
		} else if want != nil {
			c.expect(e, unit, want, "`return` without a value in a function that returns a value")
		}
		return c.divergeVar()
	case *ast.BreakExpr:
		if c.fn.loops == 0 {
			c.errorf("E0402", e.Pos, "`break` outside a loop")
		}
		return c.divergeVar()
	case *ast.ContinueExpr:
		if c.fn.loops == 0 {
			c.errorf("E0402", e.Pos, "`continue` outside a loop")
		}
		return c.divergeVar()
	case *ast.Closure:
		return c.inferClosure(e, want)
	case *ast.RecordLit:
		return c.inferRecordLit(e, want)
	case *ast.TupleLit:
		var wants []Type
		if want != nil {
			if tt, ok := Resolve(want).(*TTuple); ok && len(tt.Elems) == len(e.Elems) {
				wants = tt.Elems
			}
		}
		var es []Type
		for i, el := range e.Elems {
			var w Type
			if wants != nil {
				w = wants[i]
			}
			es = append(es, c.infer(el, w))
		}
		return &TTuple{Elems: es}
	case *ast.ListLit:
		var elem Type
		if want != nil {
			if tc, ok := Resolve(want).(*TCon); ok && tc.Info.Name == "List" && tc.Info.Mod == "prelude" && len(tc.Args) == 1 {
				elem = tc.Args[0]
			}
		}
		if elem == nil {
			elem = c.newVar()
		}
		for _, el := range e.Elems {
			t := c.infer(el, elem)
			c.expect(el, t, elem, "every element of a list literal has the same type")
		}
		return con(c.preludeType("List"), elem)
	case *ast.SpawnExpr:
		c.useEffect(ast.Effect{Name: "spawn"}, e.Pos, "spawn")
		c.fn.inSpawn++
		c.fn.spawnDepth = append(c.fn.spawnDepth, len(c.fn.scopes))
		t := c.inferBlock(e.Body, want)
		c.fn.spawnDepth = c.fn.spawnDepth[:len(c.fn.spawnDepth)-1]
		c.fn.inSpawn--
		return t
	case *ast.TaskExpr:
		if c.fn.inSpawn == 0 {
			dg := c.errorf("E0452", e.Pos, "`task` outside a `spawn` block")
			dg.Label("no enclosing `spawn { … }`")
			name := "this function"
			if c.fn.info != nil {
				name = c.fn.info.Name
			}
			dg.Helpf("wrap the statements that start and join the task in `spawn { … }` and add `spawn` to the effect set of `%s`", name)
		}
		var inner Type
		if want != nil {
			if tc, ok := Resolve(want).(*TCon); ok && tc.Info.Name == "Task" && len(tc.Args) == 1 {
				inner = tc.Args[0]
			}
		}
		t := c.infer(e.X, inner)
		return con(c.preludeType("Task"), t)
	case *ast.LlmExpr:
		return c.inferLlm(e, want)
	case *ast.DecideExpr:
		return c.inferDecide(e, want)
	case *ast.ApproveExpr:
		return c.inferApprove(e, want)
	case *ast.OldExpr:
		if !c.fn.inPost {
			c.errorf("E0402", e.Pos, "`old(…)` is only allowed in `post`")
		}
		return c.infer(e.X, want)
	case *ast.RetExpr:
		if !c.fn.inPost {
			c.errorf("E0402", e.Pos, "`ret` is only allowed in `post`")
			return c.newVar()
		}
		l := c.lookupLocal("ret")
		if l == nil {
			return c.newVar()
		}
		return l.Type
	case *ast.QuantExpr:
		ct := c.infer(e.Coll, nil)
		elem := c.iterElem(ct, e.Coll)
		c.pushScope()
		l := c.declareLocal(&Local{Name: e.Var, Type: elem, Pos: e.Pos})
		c.prog.QuantLocals[e] = l
		bt := c.infer(e.Body, con(tBool))
		c.expect(e.Body, bt, con(tBool), "quantifier body must be `Bool`")
		c.popScope()
		return con(tBool)
	case *ast.AnyExpr:
		return c.inferAny(e)
	}
	c.errorf("E0000", e.ExprPos(), "internal error: unhandled expression %T", e)
	return c.newVar()
}

func (c *Checker) divergeVar() Type {
	v := c.newVar()
	c.fn.diverge = append(c.fn.diverge, v)
	return v
}

func (c *Checker) preludeType(name string) *TypeInfo {
	if ti := c.prelude.Types[name]; ti != nil {
		return ti
	}
	return &TypeInfo{Name: name, Mod: "prelude", Kind: Extern}
}

func (c *Checker) iterElem(it Type, e ast.Expr) Type {
	switch t := Resolve(it).(type) {
	case *TCon:
		if t.Info.Mod == "prelude" {
			switch t.Info.Name {
			case "List", "Set", "MutList", "MutSet":
				if len(t.Args) == 1 {
					return t.Args[0]
				}
			case "Map", "MutMap":
				if len(t.Args) == 2 {
					return &TTuple{Elems: []Type{t.Args[0], t.Args[1]}}
				}
			case "Range":
				return con(tInt)
			}
		}
		dg := c.errorf("E0402", e.ExprPos(), "type mismatch")
		dg.Label("expected `List<_>`, `Set<_>`, `Map<_, _>` or a range, found `%s`", TypeString(it))
	case *TVar:
		c.errorf("E0403", e.ExprPos(), "cannot infer the type of this expression; add a type annotation")
	default:
		dg := c.errorf("E0402", e.ExprPos(), "type mismatch")
		dg.Label("expected `List<_>`, `Set<_>`, `Map<_, _>` or a range, found `%s`", TypeString(it))
	}
	return c.newVar()
}

// ---- identifiers ------------------------------------------------------------

func (c *Checker) inferIdent(e *ast.Ident, want Type) Type {
	if r, ok := c.prog.Refs[e]; ok && r.Kind == RefImpl && r.Impl != nil {
		return r.Impl.Target
	}
	if l := c.lookupLocal(e.Name); l != nil {
		c.prog.Refs[e] = &Ref{Kind: RefLocal, Local: l}
		c.markCapture(l)
		if l.Var {
			c.checkSpawnCapture(l, e.Pos)
		}
		return l.Type
	}
	if fi := c.lookupFn(c.fn.module, e.Name); fi != nil {
		if !c.visible(fi.Vis, fi.Mod, c.fn.module) {
			c.privateError(e.Pos, e.Name, fi.Mod)
		}
		fi.Used = true
		c.prog.Refs[e] = &Ref{Kind: RefFn, Fn: fi}
		m, _ := c.instantiate(fi.TypeParams)
		return subst(fi.Type, m)
	}
	if ci := c.lookupConst(c.fn.module, e.Name); ci != nil {
		if !c.visible(ci.Vis, ci.Module.Path, c.fn.module) {
			c.privateError(e.Pos, e.Name, ci.Module.Path)
		}
		c.prog.Refs[e] = &Ref{Kind: RefConst, Const: ci}
		return ci.Type
	}
	if e.Name == "topics" {
		// handled by inferField; as a bare value it is an error
	}
	if isTypeName(e.Name) {
		if v, enum := c.bareVariant(e.Name, want, e.Pos); v != nil {
			if len(v.Positional) > 0 || len(v.Named) > 0 {
				dg := c.errorf("E0402", e.Pos, "variant `%s` carries a payload", v.Name)
				dg.Helpf("construct it: `%s(…)`", v.Name)
			}
			c.prog.Refs[e] = &Ref{Kind: RefVariant, Variant: v}
			return c.variantResult(v, enum, want)
		}
		if ti := c.lookupTypeInfo(c.fn.module, e.Name); ti != nil {
			c.prog.Refs[e] = &Ref{Kind: RefType, Type: ti}
			dg := c.errorf("E0407", e.Pos, "`%s` is a type, not a value", e.Name)
			dg.Helpf("call a constructor or a static function: `%s.…`", e.Name)
			return c.newVar()
		}
	}
	dg := c.errorf("E0407", e.Pos, "unknown name `%s`", e.Name)
	dg.Label("not a local, a function, a const or a variant in scope")
	return c.newVar()
}

func isTypeName(s string) bool {
	return s != "" && s[0] >= 'A' && s[0] <= 'Z'
}

func (c *Checker) markCapture(l *Local) {
	f := c.fn
	for _, s := range f.sinks {
		if f.localDepth[l] <= s.depth {
			if s.captures == nil {
				s.captures = map[*Local]bool{}
			}
			s.captures[l] = true
			l.Captured = true
		}
	}
}

// bareVariant resolves an unqualified variant name. The expected type wins;
// otherwise the name must be unique among the enums in scope.
func (c *Checker) bareVariant(name string, want Type, pos ast.Pos) (*VariantInfo, *TCon) {
	if want != nil {
		if tc, ok := Resolve(want).(*TCon); ok && tc.Info.Kind == Enum {
			for _, v := range tc.Info.Variants {
				if v.Name == name {
					return v, tc
				}
			}
		}
	}
	var found []*VariantInfo
	seen := map[*TypeInfo]bool{}
	add := func(m *Module) {
		if m == nil {
			return
		}
		for _, ti := range m.Types {
			if ti.Kind != Enum || seen[ti] {
				continue
			}
			seen[ti] = true
			for _, v := range ti.Variants {
				if v.Name == name {
					found = append(found, v)
				}
			}
		}
	}
	m := c.fn.module
	add(m)
	if m.IsScenarios {
		add(m.Parent)
		add(c.testing)
	}
	add(c.prelude)
	if len(found) == 1 {
		v := found[0]
		_, args := c.instantiate(v.Enum.Params)
		return v, con(v.Enum, args...)
	}
	if len(found) > 1 {
		dg := c.errorf("E0407", pos, "ambiguous variant `%s`", name)
		var names []string
		for _, v := range found {
			names = append(names, "`"+qualifiedName(v.Enum)+"."+name+"`")
		}
		dg.Label("could be %s", strings.Join(names, " or "))
		dg.Helpf("qualify it with the enum name")
		return found[0], con(found[0].Enum, c.freshArgs(found[0].Enum)...)
	}
	return nil, nil
}

func (c *Checker) freshArgs(ti *TypeInfo) []Type {
	_, args := c.instantiate(ti.Params)
	return args
}

// variantResult is the type of a payload-less variant used as a value.
func (c *Checker) variantResult(v *VariantInfo, enum *TCon, want Type) Type {
	if want != nil {
		c.u.tryUnify(enum, want)
	}
	return enum
}

// variantSubst maps the enum's parameters to the instantiated arguments.
func variantSubst(v *VariantInfo, enum *TCon) map[*TParam]Type {
	m := map[*TParam]Type{}
	for i, p := range v.Enum.Params {
		if i < len(enum.Args) {
			m[p] = enum.Args[i]
		}
	}
	return m
}

// ---- field access -----------------------------------------------------------

// staticBase resolves the base of a FieldExpr when it is not a value.
// It returns a module, a type or nil (meaning: a value).
func (c *Checker) staticBase(x ast.Expr) (*Module, *TypeInfo, bool) {
	switch b := x.(type) {
	case *ast.Ident:
		if c.lookupLocal(b.Name) != nil {
			return nil, nil, false
		}
		if isTypeName(b.Name) {
			if ti := c.lookupTypeInfo(c.fn.module, b.Name); ti != nil {
				c.prog.Refs[b] = &Ref{Kind: RefType, Type: ti}
				return nil, ti, true
			}
			return nil, nil, false
		}
		if c.lookupFn(c.fn.module, b.Name) != nil || c.lookupConst(c.fn.module, b.Name) != nil {
			return nil, nil, false
		}
		if dep := c.depModule(c.fn.module, b.Name); dep != nil {
			c.prog.Refs[b] = &Ref{Kind: RefModule, Module: dep}
			return dep, nil, true
		}
		return nil, nil, false
	case *ast.FieldExpr:
		if id, ok := b.X.(*ast.Ident); ok && isTypeName(b.Name) && c.lookupLocal(id.Name) == nil && !isTypeName(id.Name) {
			if dep := c.depModule(c.fn.module, id.Name); dep != nil {
				if ti := dep.Types[b.Name]; ti != nil {
					if !c.visible(ti.Vis, ti.Mod, c.fn.module) {
						c.privateError(b.Pos, b.Name, ti.Mod)
					}
					c.prog.Refs[id] = &Ref{Kind: RefModule, Module: dep}
					c.prog.Fields[b] = &FieldRef{Kind: FieldModuleItem, Type: ti}
					return nil, ti, true
				}
			}
		}
	}
	return nil, nil, false
}

func (c *Checker) inferField(e *ast.FieldExpr, want Type) Type {
	// topics.<name>
	if id, ok := e.X.(*ast.Ident); ok && id.Name == "topics" && c.lookupLocal("topics") == nil {
		if tp, ok := c.prog.Topics[e.Name]; ok {
			c.prog.Refs[id] = &Ref{Kind: RefTopic, Topic: tp}
			c.prog.Fields[e] = &FieldRef{Kind: FieldTopic, Topic: tp}
			return c.prog.TopicType[e.Name]
		}
		dg := c.errorf("E0407", e.Pos, "unknown topic `%s`", e.Name)
		dg.Helpf("declare it in `app.jip`: `topics { %s: Topic<…> }`", e.Name)
		return c.newVar()
	}
	mod, ti, static := c.staticBase(e.X)
	if static {
		if mod != nil {
			return c.inferModuleItem(e, mod, want)
		}
		return c.inferStaticMember(e, ti, want, false)
	}
	xt := c.infer(e.X, nil)
	return c.inferValueField(e, xt, want)
}

func (c *Checker) inferModuleItem(e *ast.FieldExpr, mod *Module, want Type) Type {
	if fi, ok := mod.Fns[e.Name]; ok {
		if !c.visible(fi.Vis, fi.Mod, c.fn.module) {
			c.privateError(e.Pos, e.Name, fi.Mod)
		}
		fi.Used = true
		c.prog.Fields[e] = &FieldRef{Kind: FieldModuleItem, Fn: fi}
		m, _ := c.instantiate(fi.TypeParams)
		return subst(fi.Type, m)
	}
	if ci, ok := mod.Consts[e.Name]; ok {
		if !c.visible(ci.Vis, ci.Module.Path, c.fn.module) {
			c.privateError(e.Pos, e.Name, ci.Module.Path)
		}
		c.prog.Fields[e] = &FieldRef{Kind: FieldModuleItem, Const: ci}
		return ci.Type
	}
	if ti, ok := mod.Types[e.Name]; ok {
		c.prog.Fields[e] = &FieldRef{Kind: FieldModuleItem, Type: ti}
		dg := c.errorf("E0407", e.Pos, "`%s.%s` is a type, not a value", lastSeg(mod.Path), e.Name)
		dg.Helpf("call a constructor or a static function: `%s.%s.…`", lastSeg(mod.Path), e.Name)
		return c.newVar()
	}
	dg := c.errorf("E0407", e.Pos, "`%s` has no item `%s`", mod.Path, e.Name)
	dg.Label("not a `pub` function, const or type of `%s`", mod.Path)
	return c.newVar()
}

func lastSeg(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// inferStaticMember resolves `T.name`: a variant, a static function, or an
// instance method of a behaviour impl (`Orders.get`). asCallee says whether
// the result is about to be called.
func (c *Checker) inferStaticMember(e *ast.FieldExpr, ti *TypeInfo, want Type, asCallee bool) Type {
	if ti.Kind == Enum {
		for _, v := range ti.Variants {
			if v.Name == e.Name {
				_, args := c.instantiate(ti.Params)
				enum := con(ti, args...)
				c.prog.Fields[e] = &FieldRef{Kind: FieldVariant, Variant: v, Type: ti}
				if len(v.Positional) > 0 || len(v.Named) > 0 {
					if !asCallee {
						dg := c.errorf("E0402", e.Pos, "variant `%s.%s` carries a payload", ti.Name, v.Name)
						dg.Helpf("construct it: `%s.%s(…)`", ti.Name, v.Name)
					}
					m := variantSubst(v, enum)
					var ps []Type
					for _, p := range v.Positional {
						ps = append(ps, subst(p, m))
					}
					return &TFn{Params: ps, Ret: enum}
				}
				return c.variantResult(v, enum, want)
			}
		}
	}
	// Inherent static functions, then behaviour-impl methods.
	for _, impl := range ti.Impls {
		if fi, ok := impl.Methods[e.Name]; ok && !fi.HasSelf {
			if !c.visible(fi.Vis, fi.Mod, c.fn.module) {
				c.privateError(e.Pos, ti.Name+"."+e.Name, fi.Mod)
			}
			fi.Used = true
			c.prog.Fields[e] = &FieldRef{Kind: FieldStaticFn, Fn: fi, Impl: impl, Type: ti}
			m, _ := c.instantiate(fi.TypeParams)
			return subst(fi.Type, m)
		}
	}
	for _, impl := range ti.BImpls {
		if fi, ok := impl.Methods[e.Name]; ok {
			fi.Used = true
			if fi.HasSelf {
				// Instance call by name: drop self.
				c.prog.Fields[e] = &FieldRef{Kind: FieldImpl, Fn: fi, Impl: impl, Type: ti}
				m, _ := c.instantiate(fi.TypeParams)
				t := subst(fi.Type, m).(*TFn)
				return &TFn{Params: t.Params[1:], Ret: t.Ret, Effects: t.Effects, Explicit: t.Explicit}
			}
			c.prog.Fields[e] = &FieldRef{Kind: FieldStaticFn, Fn: fi, Impl: impl, Type: ti}
			m, _ := c.instantiate(fi.TypeParams)
			return subst(fi.Type, m)
		}
		if e.Name == "enqueue" && impl.Behaviour != nil && impl.Behaviour.Name == "Worker" {
			c.prog.Fields[e] = &FieldRef{Kind: FieldImpl, Impl: impl, Type: ti}
			msg := impl.Assoc["Msg"]
			if msg == nil {
				msg = c.newVar()
			}
			qe := c.preludeType("QueueError")
			return &TFn{Params: []Type{msg}, Ret: con(c.preludeType("Result"), unit, con(qe)), Effects: []ast.Effect{{Name: "io"}}, Explicit: true}
		}
	}
	// Methods with self on inherent impls, named statically: not a value.
	for _, impl := range ti.Impls {
		if fi, ok := impl.Methods[e.Name]; ok && fi.HasSelf {
			dg := c.errorf("E0407", e.Pos, "`%s.%s` is an instance method", ti.Name, e.Name)
			dg.Helpf("call it on a value: `x.%s(…)`", e.Name)
			return c.newVar()
		}
	}
	dg := c.errorf("E0407", e.Pos, "`%s` has no variant or static function `%s`", ti.Name, e.Name)
	dg.Label("not defined on `%s`", qualifiedName(ti))
	return c.newVar()
}

// inferValueField resolves `x.name` on a value: record field, tuple index,
// newtype payload. Methods are handled by inferCall.
func (c *Checker) inferValueField(e *ast.FieldExpr, xt Type, want Type) Type {
	rt := Resolve(xt)
	switch t := rt.(type) {
	case *TTuple:
		if i, err := strconv.Atoi(e.Name); err == nil {
			if i < 0 || i >= len(t.Elems) {
				c.errorf("E0402", e.Pos, "tuple `%s` has no element %d", TypeString(xt), i)
				return c.newVar()
			}
			c.prog.Fields[e] = &FieldRef{Kind: FieldTupleIndex, Index: i}
			return t.Elems[i]
		}
	case *TCon:
		switch t.Info.Kind {
		case Record:
			m := typeArgSubst(t)
			for _, fd := range t.Info.Fields {
				if fd.Name == e.Name {
					if t.Info.Opaque && t.Info.Module != c.fn.module && !(c.fn.module.IsScenarios && c.fn.module.Parent == t.Info.Module) {
						dg := c.errorf("E0408", e.Pos, "`%s` is opaque outside `%s`", t.Info.Name, t.Info.Mod)
						dg.Label("field `%s` is not accessible", e.Name)
					}
					c.prog.Fields[e] = &FieldRef{Kind: FieldRecord, Field: fd, Type: t.Info}
					return subst(fd.Type, m)
				}
			}
		case Newtype:
			if e.Name == "0" {
				if t.Info.Opaque && t.Info.Module != c.fn.module && !(c.fn.module.IsScenarios && c.fn.module.Parent == t.Info.Module) {
					c.errorf("E0408", e.Pos, "`%s` is opaque outside `%s`", t.Info.Name, t.Info.Mod)
				}
				c.prog.Fields[e] = &FieldRef{Kind: FieldNewtype0, Type: t.Info}
				return subst(t.Info.Inner, typeArgSubst(t))
			}
		}
		// A method used as a value is not supported; say so clearly.
		if fi, _, _ := c.findMethod(t, e.Name); fi != nil {
			dg := c.errorf("E0407", e.Pos, "`%s` is a method of `%s`", e.Name, TypeString(xt))
			dg.Helpf("call it: `.%s(…)`", e.Name)
			return c.newVar()
		}
		dg := c.errorf("E0407", e.Pos, "`%s` has no field `%s`", TypeString(xt), e.Name)
		if t.Info.Kind == Record {
			var names []string
			for _, fd := range t.Info.Fields {
				names = append(names, "`"+fd.Name+"`")
			}
			dg.Label("fields: %s", strings.Join(names, ", "))
		}
		return c.newVar()
	case *TVar:
		c.errorf("E0403", e.X.ExprPos(), "cannot infer the type of this expression; add a type annotation")
		return c.newVar()
	}
	c.errorf("E0407", e.Pos, "`%s` has no field `%s`", TypeString(xt), e.Name)
	return c.newVar()
}

func typeArgSubst(t *TCon) map[*TParam]Type {
	m := map[*TParam]Type{}
	for i, p := range t.Info.Params {
		if i < len(t.Args) {
			m[p] = t.Args[i]
		}
	}
	return m
}

// findMethod looks for a method named name on the receiver type. It returns
// the method, its impl, and the instantiated function type with the impl's
// parameters unified against the receiver. Nothing is bound on failure.
func (c *Checker) findMethod(recv *TCon, name string) (*FnInfo, *ImplInfo, *TFn) {
	try := func(impls []*ImplInfo) (*FnInfo, *ImplInfo, *TFn) {
		for _, impl := range impls {
			fi, ok := impl.Methods[name]
			if !ok || !fi.HasSelf {
				continue
			}
			m, _ := c.instantiate(impl.TypeParams)
			mk := c.u.mark()
			if !c.u.unify(subst(impl.Target, m), recv) {
				c.u.rollback(mk)
				continue
			}
			own, _ := c.instantiate(fi.OwnParams)
			for k, v := range own {
				m[k] = v
			}
			return fi, impl, subst(fi.Type, m).(*TFn)
		}
		return nil, nil, nil
	}
	if fi, impl, t := try(recv.Info.Impls); fi != nil {
		return fi, impl, t
	}
	return try(recv.Info.BImpls)
}

// ---- unary and binary -------------------------------------------------------

func (c *Checker) inferUnary(e *ast.UnaryExpr, want Type) Type {
	switch e.Op {
	case "-":
		t := c.infer(e.X, want)
		if !c.isNumeric(t) {
			c.errorf("E0402", e.Pos, "type mismatch").Label("unary `-` expects a number, found `%s`", TypeString(t))
		}
		return t
	case "!":
		t := c.infer(e.X, con(tBool))
		c.expect(e.X, t, con(tBool), "`!` expects `Bool`")
		return con(tBool)
	}
	c.errorf("E0000", e.Pos, "internal error: unknown unary operator %q", e.Op)
	return c.newVar()
}

func (c *Checker) isNumeric(t Type) bool {
	switch r := Resolve(t).(type) {
	case *TCon:
		return r.Info == tInt || r.Info == tFloat || r.Info == tDuration
	case *TVar:
		return r.numeric || true
	}
	return false
}

func (c *Checker) inferBinary(e *ast.BinaryExpr, want Type) Type {
	switch e.Op {
	case "+", "-", "*", "/", "%":
		lt := c.infer(e.L, want)
		rt := c.infer(e.R, lt)
		if !c.u.tryUnify(lt, rt) {
			dg := c.errorf("E0402", e.R.ExprPos(), "type mismatch")
			dg.Label("expected `%s`, found `%s`", TypeString(lt), TypeString(rt))
			dg.Note("both operands of `%s` must have the same type; there is no implicit conversion", e.Op)
			return lt
		}
		r := Resolve(lt)
		if tc, ok := r.(*TCon); ok {
			okOp := tc.Info == tInt || tc.Info == tFloat || tc.Info == tDuration || (tc.Info == tStr && e.Op == "+")
			if !okOp {
				dg := c.errorf("E0402", e.Pos, "operator `%s` is not defined on `%s`", e.Op, TypeString(lt))
				if tc.Info.Kind == Newtype {
					dg.Helpf("unwrap with `.0` and wrap the result: `%s(a.0 %s b.0)`", tc.Info.Name, e.Op)
				}
			}
		} else if v, ok := r.(*TVar); ok {
			v.numeric = true
		}
		return lt
	case "<", "<=", ">", ">=":
		lt := c.infer(e.L, nil)
		rt := c.infer(e.R, lt)
		if !c.u.tryUnify(lt, rt) {
			dg := c.errorf("E0402", e.R.ExprPos(), "type mismatch")
			dg.Label("expected `%s`, found `%s`", TypeString(lt), TypeString(rt))
		}
		switch r := Resolve(lt).(type) {
		case *TCon:
			if !(r.Info == tInt || r.Info == tFloat || r.Info == tDuration || r.Info == tStr || r.Info == tChar) {
				c.errorf("E0402", e.Pos, "operator `%s` is not defined on `%s`", e.Op, TypeString(lt)).Note("comparison needs `Int`, `Float`, `Duration`, `Str`, `Char` or a type parameter bounded by `Ord`")
			}
		case *TParam:
			if !hasBound(r, "Ord") {
				dg := c.errorf("E0402", e.Pos, "operator `%s` is not defined on `%s`", e.Op, r.Name)
				dg.Helpf("add the bound: `%s: Ord`", r.Name)
			}
		case *TVar:
			r.numeric = true
		}
		return con(tBool)
	case "==", "!=":
		lt := c.infer(e.L, nil)
		rt := c.infer(e.R, lt)
		if !c.u.tryUnify(lt, rt) {
			// Allow the right side to drive inference of the left (e.g. a bare variant).
			dg := c.errorf("E0402", e.R.ExprPos(), "type mismatch")
			dg.Label("expected `%s`, found `%s`", TypeString(lt), TypeString(rt))
		}
		return con(tBool)
	case "&&", "||":
		lt := c.infer(e.L, con(tBool))
		c.expect(e.L, lt, con(tBool), fmt.Sprintf("`%s` expects `Bool` operands", e.Op))
		rt := c.infer(e.R, con(tBool))
		c.expect(e.R, rt, con(tBool), fmt.Sprintf("`%s` expects `Bool` operands", e.Op))
		return con(tBool)
	}
	c.errorf("E0000", e.Pos, "internal error: unknown binary operator %q", e.Op)
	return c.newVar()
}

func hasBound(p *TParam, b string) bool {
	for _, x := range p.Bounds {
		if x == b {
			return true
		}
	}
	return false
}

// ---- closures ---------------------------------------------------------------

func (c *Checker) inferClosure(e *ast.Closure, want Type) Type {
	var wantFn *TFn
	if want != nil {
		if ft, ok := Resolve(want).(*TFn); ok && len(ft.Params) == len(e.Params) {
			wantFn = ft
		}
	}
	f := c.fn
	sink := &effSet{closure: e, depth: len(f.scopes)}
	f.sinks = append(f.sinks, sink)
	c.pushScope()
	sc := &tscope{m: f.module, params: c.fnTypeParams()}
	t := &TFn{}
	for i := range e.Params {
		p := &e.Params[i]
		var pt Type
		switch {
		case p.Type != nil:
			pt = c.resolveType(sc, p.Type)
			if wantFn != nil {
				c.u.tryUnify(pt, wantFn.Params[i])
			}
		case wantFn != nil:
			pt = wantFn.Params[i]
		default:
			pt = c.newVar()
		}
		t.Params = append(t.Params, pt)
		l := &Local{Name: p.Name, Type: pt, Pos: p.Pos}
		if ft, ok := Resolve(pt).(*TFn); ok {
			l.Fn, l.Effects, l.Explicit = ft, ft.Effects, ft.Explicit
		}
		c.declareLocal(l)
		c.prog.Params[p] = l
	}
	var ret Type
	switch {
	case e.Ret != nil:
		ret = c.resolveType(sc, e.Ret)
	case wantFn != nil:
		ret = wantFn.Ret
	default:
		ret = c.newVar()
	}
	f.closureRets = append(f.closureRets, ret)
	savedLoops := f.loops
	f.loops = 0
	bt := c.infer(e.Body, ret)
	c.expect(e.Body, bt, ret, "closure body does not match the declared return type")
	f.loops = savedLoops
	f.closureRets = f.closureRets[:len(f.closureRets)-1]
	t.Ret = ret
	c.popScope()
	f.sinks = f.sinks[:len(f.sinks)-1]
	t.Effects = SortEffects(sink.effs)
	var caps []*Local
	for l := range sink.captures {
		caps = append(caps, l)
	}
	sortLocals(caps)
	c.prog.Closures[e] = &ClosureInfo{Type: t, Effects: t.Effects, Captures: caps}
	// A closure's effects flow outward to the enclosing closure (if any)
	// only when it is called there; a closure passed as an argument is
	// accounted for at the call site.
	return t
}

func sortLocals(ls []*Local) {
	for i := 1; i < len(ls); i++ {
		for j := i; j > 0 && posLess(ls[j].Pos, ls[j-1].Pos); j-- {
			ls[j], ls[j-1] = ls[j-1], ls[j]
		}
	}
}

// argEffects returns the effect set of an argument passed to a function-typed
// parameter, and whether that set is a declared (explicit) bound.
func (c *Checker) argEffects(a ast.Expr) ([]ast.Effect, bool, bool) {
	switch a := a.(type) {
	case *ast.Closure:
		if ci, ok := c.prog.Closures[a]; ok {
			return ci.Effects, false, true
		}
	case *ast.Ident:
		if r, ok := c.prog.Refs[a]; ok {
			switch r.Kind {
			case RefLocal:
				if r.Local.Fn != nil {
					if r.Local.Explicit {
						return r.Local.Effects, true, true
					}
					// Unannotated fn parameter: contributes nothing (rule 10).
					return nil, false, true
				}
			case RefFn:
				return r.Fn.Type.Effects, true, true
			}
		}
	case *ast.FieldExpr:
		if r, ok := c.prog.Fields[a]; ok && r.Fn != nil {
			return r.Fn.Type.Effects, true, true
		}
	}
	return nil, false, false
}

// ---- calls ------------------------------------------------------------------

// calleeName renders the callee for diagnostics.
func calleeName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.FieldExpr:
		if _, ok := e.X.(*ast.SelfExpr); ok {
			return "self." + e.Name
		}
		return calleeName(e.X) + "." + e.Name
	case *ast.SelfExpr:
		return "self"
	}
	return exprString(e)
}

func (c *Checker) inferCall(e *ast.CallExpr, want Type) Type {
	info := &CallInfo{}
	c.prog.Calls[e] = info
	var ft *TFn
	var fi *FnInfo
	what := calleeName(e.Fn)
	var recv ast.Expr
	var paramNames []string

	switch fn := e.Fn.(type) {
	case *ast.Ident:
		ft, fi = c.resolveIdentCallee(fn, e, info, want)
	case *ast.FieldExpr:
		ft, fi, recv = c.resolveFieldCallee(fn, e, info, want)
	default:
		t := c.infer(e.Fn, nil)
		if f, ok := Resolve(t).(*TFn); ok {
			ft = f
			info.Kind = CallClosure
		} else {
			c.errorf("E0402", e.Fn.ExprPos(), "type mismatch").Label("expected a function, found `%s`", TypeString(t))
		}
	}
	if ft == nil {
		for _, a := range e.Args {
			c.infer(a, nil)
		}
		return c.newVar()
	}
	if fi != nil {
		info.Fn = fi
		fi.Used = true
		paramNames = fi.Decl_Params()
		if fi.HasSelf && recv != nil && len(paramNames) > 0 {
			paramNames = paramNames[1:]
		}
		if len(paramNames) > len(ft.Params) {
			paramNames = paramNames[:len(ft.Params)]
		}
	}
	// Turbofish.
	if len(e.TypeArgs) > 0 && fi != nil {
		ft = c.applyTurbofish(e, fi, info, ft, recv != nil)
		c.record(e.Fn, ft)
	}
	// Let the expected type flow into the arguments.
	if want != nil {
		c.u.tryUnify(ft.Ret, want)
	}
	params := ft.Params
	if len(params) != len(e.Args) {
		dg := c.errorf("E0402", e.Pos, "`%s` takes %d argument(s), found %d", what, len(params), len(e.Args))
		if fi != nil {
			dg.Label("declared at %s", fi.Pos.String())
		}
		for _, a := range e.Args {
			c.infer(a, nil)
		}
		return ft.Ret
	}
	for i, a := range e.Args {
		pt := params[i]
		at := c.infer(a, pt)
		if !c.u.tryUnify(at, pt) {
			defaultNumeric(at)
			dg := c.errorf("E0402", a.ExprPos(), "type mismatch")
			dg.Label("expected `%s`, found `%s`", TypeString(pt), TypeString(at))
			pn := ""
			if i < len(paramNames) {
				pn = paramNames[i]
			}
			if pn != "" {
				dg.Note("parameter `%s` of `%s` is `%s`; there is no implicit conversion from `%s`", pn, what, TypeString(pt), TypeString(at))
			} else {
				dg.Note("argument %d of `%s` is `%s`; there is no implicit conversion from `%s`", i+1, what, TypeString(pt), TypeString(at))
			}
			if tc, ok := Resolve(pt).(*TCon); ok && tc.Info.Kind == Newtype && c.u.probe(subst(tc.Info.Inner, typeArgSubst(tc)), at) {
				dg.Helpf("wrap the value: `%s`", callWithArg(e, i, tc.Info.Name+"("+exprString(a)+")"))
			}
			continue
		}
		// Function-typed parameters: closure bounds (rules 10 and 11).
		if pft, ok := Resolve(pt).(*TFn); ok {
			effs, _, known := c.argEffects(a)
			if !known {
				if at2, ok := Resolve(at).(*TFn); ok {
					effs = at2.Effects
				}
			}
			if pft.Explicit {
				if bad, ok := effSubset(effs, pft.Effects); !ok {
					pn := fmt.Sprintf("argument %d", i+1)
					if i < len(paramNames) && paramNames[i] != "" {
						pn = paramNames[i]
					}
					dg := c.errorf("E0411", a.ExprPos(), "closure exceeds the effect bound of parameter `%s`", pn)
					dg.Label("closure has effects %s", EffectSetString(effs))
					dg.Note("`%s: %s` accepts only %s", pn, TypeString(pft), boundDesc(pft.Effects))
					dg.Helpf("%s outside the closure and pass the data in, or declare `%s: %s with %s`", effectVerb(bad), pn, TypeStringNoEffects(pft), EffectSetString(effAdd(append([]ast.Effect{}, pft.Effects...), bad)))
				}
				for _, ef := range pft.Effects {
					c.useEffect(ef, e.Pos, what)
				}
			} else {
				for _, ef := range effs {
					c.useEffect(ef, a.ExprPos(), what)
				}
				info.Effects = effUnion(info.Effects, effs)
			}
		}
	}
	// The callee's own effects.
	var declared []ast.Effect
	switch info.Kind {
	case CallClosure:
		if info.Local != nil {
			if info.Local.Explicit {
				declared = info.Local.Effects
			}
		} else {
			declared = ft.Effects
		}
	default:
		declared = ft.Effects
	}
	for _, ef := range declared {
		c.useEffect(ef, e.Pos, what)
	}
	info.Effects = effUnion(info.Effects, declared)
	if fi != nil && c.fn.info != nil {
		c.fn.info.Calls = append(c.fn.info.Calls, fi)
	}
	if c.fn.isWorkflowRun && len(c.fn.sinks) == 0 && c.fn.inSpawn == 0 && len(declared) > 0 && info.Kind != CallVariant && info.Kind != CallNewtype {
		info.Step = true
		info.StepKey = what + "@" + e.Pos.String()
	}
	if id, ok := e.Fn.(*ast.Ident); ok && id.Name == "panic" && fi != nil && fi.Mod == "prelude" {
		return c.divergeVar()
	}
	return ft.Ret
}

func boundDesc(effs []ast.Effect) string {
	if len(effs) == 0 {
		return "pure closures"
	}
	return "closures with effects within " + EffectSetString(effs)
}

func effectVerb(e ast.Effect) string {
	switch e.Name {
	case "net":
		return "fetch"
	case "io":
		return "read"
	case "mut":
		return "mutate"
	default:
		return "perform `" + e.String() + "`"
	}
}

// TypeStringNoEffects prints a function type without its effect clause.
func TypeStringNoEffects(t *TFn) string {
	return TypeString(&TFn{Params: t.Params, Ret: t.Ret})
}

func callWithArg(e *ast.CallExpr, i int, repl string) string {
	var args []string
	for j, a := range e.Args {
		if j == i {
			args = append(args, repl)
		} else {
			args = append(args, exprString(a))
		}
	}
	return exprString(e.Fn) + "(" + strings.Join(args, ", ") + ")"
}

// Decl_Params returns the declared parameter names (empty for synthesised
// functions).
func (fi *FnInfo) Decl_Params() []string {
	if fi.Decl == nil {
		return nil
	}
	var out []string
	for _, p := range fi.Decl.Params {
		if p.Self {
			out = append(out, "self")
		} else {
			out = append(out, p.Name)
		}
	}
	return out
}

// applyTurbofish binds explicit type arguments.
func (c *Checker) applyTurbofish(e *ast.CallExpr, fi *FnInfo, info *CallInfo, ft *TFn, hasRecv bool) *TFn {
	sc := &tscope{m: c.fn.module, params: c.fnTypeParams()}
	var args []Type
	for _, ta := range e.TypeArgs {
		args = append(args, c.resolveType(sc, ta))
	}
	var params []*TParam
	switch {
	case len(args) == len(fi.OwnParams):
		params = fi.OwnParams
	case len(args) == len(fi.TypeParams):
		params = fi.TypeParams
	default:
		c.errorf("E0405", e.Pos, "`%s` takes %d type argument(s), found %d", fi.Name, len(fi.OwnParams), len(args))
		return ft
	}
	// ft was built from a fresh instantiation of fi.TypeParams; rebuild it
	// with the explicit arguments substituted for the chosen parameters.
	m, _ := c.instantiate(fi.TypeParams)
	for i, p := range params {
		m[p] = args[i]
	}
	nt := subst(fi.Type, m).(*TFn)
	if hasRecv && fi.HasSelf {
		// Keep the receiver binding from the previous instantiation.
		c.u.tryUnify(nt.Params[0], ft.Params[0])
	}
	info.TypeArgs = nil
	for _, p := range fi.OwnParams {
		info.TypeArgs = append(info.TypeArgs, m[p])
	}
	info.ImplTypeArgs = nil
	if fi.Impl != nil {
		for _, p := range fi.Impl.TypeParams {
			info.ImplTypeArgs = append(info.ImplTypeArgs, m[p])
		}
	}
	return nt
}

// recordTypeArgs stores the instantiation of fi's parameters in info.
func recordTypeArgs(info *CallInfo, fi *FnInfo, m map[*TParam]Type) {
	info.TypeArgs = nil
	for _, p := range fi.OwnParams {
		info.TypeArgs = append(info.TypeArgs, m[p])
	}
	info.ImplTypeArgs = nil
	if fi.Impl != nil {
		for _, p := range fi.Impl.TypeParams {
			info.ImplTypeArgs = append(info.ImplTypeArgs, m[p])
		}
	}
}

// resolveIdentCallee resolves `name(args)`.
func (c *Checker) resolveIdentCallee(id *ast.Ident, e *ast.CallExpr, info *CallInfo, want Type) (*TFn, *FnInfo) {
	if l := c.lookupLocal(id.Name); l != nil {
		c.prog.Refs[id] = &Ref{Kind: RefLocal, Local: l}
		c.markCapture(l)
		ft, ok := Resolve(l.Type).(*TFn)
		if !ok {
			c.errorf("E0402", id.Pos, "type mismatch").Label("`%s` is `%s`, not a function", id.Name, TypeString(l.Type))
			return nil, nil
		}
		info.Kind = CallClosure
		info.Local = l
		c.record(id, l.Type)
		return ft, nil
	}
	if fi := c.lookupFn(c.fn.module, id.Name); fi != nil {
		if !c.visible(fi.Vis, fi.Mod, c.fn.module) {
			c.privateError(id.Pos, id.Name, fi.Mod)
		}
		c.prog.Refs[id] = &Ref{Kind: RefFn, Fn: fi}
		info.Kind = CallFn
		m, _ := c.instantiate(fi.TypeParams)
		recordTypeArgs(info, fi, m)
		ft := subst(fi.Type, m).(*TFn)
		c.record(id, ft)
		return ft, fi
	}
	if isTypeName(id.Name) {
		if v, enum := c.bareVariant(id.Name, want, id.Pos); v != nil {
			c.prog.Refs[id] = &Ref{Kind: RefVariant, Variant: v}
			return c.variantCallee(v, enum, info, want, id.Pos), nil
		}
		if ti := c.lookupTypeInfo(c.fn.module, id.Name); ti != nil {
			c.prog.Refs[id] = &Ref{Kind: RefType, Type: ti}
			return c.newtypeCallee(ti, info, id.Pos, want), nil
		}
	}
	switch id.Name {
	case "run_workflow", "tick", "send_signal":
		if c.fn.module.IsScenarios || c.fn.ret == nil {
			c.prog.Refs[id] = &Ref{Kind: RefBuiltin, Builtin: id.Name}
			info.Kind = CallBuiltin
			info.Builtin = id.Name
			return c.scenarioBuiltin(id.Name, e, info), nil
		}
	}
	dg := c.errorf("E0407", id.Pos, "unknown function `%s`", id.Name)
	dg.Label("not a function, variant or newtype in scope")
	return nil, nil
}

func (c *Checker) variantCallee(v *VariantInfo, enum *TCon, info *CallInfo, want Type, pos ast.Pos) *TFn {
	info.Kind = CallVariant
	info.Variant = v
	if want != nil {
		c.u.tryUnify(enum, want)
	}
	m := variantSubst(v, enum)
	ft := &TFn{Ret: enum}
	if len(v.Positional) == 0 && len(v.Named) == 0 {
		dg := c.errorf("E0402", pos, "variant `%s` has no payload", v.Name)
		dg.Helpf("write `%s` without parentheses", v.Name)
		return ft
	}
	if len(v.Named) > 0 {
		dg := c.errorf("E0402", pos, "variant `%s` has named fields", v.Name)
		dg.Helpf("construct it with braces: `%s { … }`", v.Name)
		return ft
	}
	for _, p := range v.Positional {
		ft.Params = append(ft.Params, subst(p, m))
	}
	return ft
}

func (c *Checker) newtypeCallee(ti *TypeInfo, info *CallInfo, pos ast.Pos, want Type) *TFn {
	if ti.Kind != Newtype {
		dg := c.errorf("E0407", pos, "`%s` is not callable", ti.Name)
		switch ti.Kind {
		case Record:
			dg.Helpf("construct a record with braces: `%s { … }`", ti.Name)
		case Enum:
			dg.Helpf("name a variant: `%s.…`", ti.Name)
		}
		return nil
	}
	if ti.Opaque && ti.Module != c.fn.module && !(c.fn.module.IsScenarios && c.fn.module.Parent == ti.Module) {
		c.errorf("E0408", pos, "`%s` is opaque outside `%s`", ti.Name, ti.Mod).Label("its constructor is not accessible")
	}
	if !c.visible(ti.Vis, ti.Mod, c.fn.module) {
		c.privateError(pos, ti.Name, ti.Mod)
	}
	info.Kind = CallNewtype
	info.Newtype = ti
	_, args := c.instantiate(ti.Params)
	t := con(ti, args...)
	if want != nil {
		c.u.tryUnify(t, want)
	}
	return &TFn{Params: []Type{subst(ti.Inner, typeArgSubst(t))}, Ret: t}
}

// resolveFieldCallee resolves `x.name(args)`, `T.name(args)`, `m.name(args)`.
func (c *Checker) resolveFieldCallee(fe *ast.FieldExpr, e *ast.CallExpr, info *CallInfo, want Type) (*TFn, *FnInfo, ast.Expr) {
	if id, ok := fe.X.(*ast.Ident); ok && id.Name == "topics" && c.lookupLocal("topics") == nil {
		t := c.infer(fe, nil)
		if ft, ok := Resolve(t).(*TFn); ok {
			info.Kind = CallClosure
			return ft, nil, nil
		}
		return nil, nil, nil
	}
	mod, ti, static := c.staticBase(fe.X)
	if static {
		var t Type
		if mod != nil {
			t = c.inferModuleItem(fe, mod, want)
			if r := c.prog.Fields[fe]; r != nil && r.Fn != nil {
				info.Kind = CallFn
				m, _ := c.instantiate(r.Fn.TypeParams)
				ft := subst(r.Fn.Type, m).(*TFn)
				recordTypeArgs(info, r.Fn, m)
				c.record(fe, ft)
				return ft, r.Fn, nil
			}
		} else {
			t = c.inferStaticMember(fe, ti, want, true)
			if r := c.prog.Fields[fe]; r != nil {
				switch r.Kind {
				case FieldVariant:
					c.record(fe, t)
					var enum *TCon
					if ft, ok := Resolve(t).(*TFn); ok {
						enum, _ = Resolve(ft.Ret).(*TCon)
					} else {
						enum, _ = Resolve(t).(*TCon)
					}
					if enum == nil {
						enum = con(r.Variant.Enum, c.freshArgs(r.Variant.Enum)...)
					}
					return c.variantCallee(r.Variant, enum, info, want, fe.Pos), nil, nil
				case FieldStaticFn:
					info.Kind = CallFn
					info.Impl = r.Impl
					m, _ := c.instantiate(r.Fn.TypeParams)
					ft := subst(r.Fn.Type, m).(*TFn)
					recordTypeArgs(info, r.Fn, m)
					c.record(fe, ft)
					return ft, r.Fn, nil
				case FieldImpl:
					info.Impl = r.Impl
					if r.Fn != nil {
						info.Kind = CallInstance
						c.record(fe, t)
						return t.(*TFn), r.Fn, nil
					}
					info.Kind = CallBuiltin
					info.Builtin = "enqueue"
					c.record(fe, t)
					return t.(*TFn), nil, nil
				}
			}
		}
		c.record(fe, t)
		if ft, ok := Resolve(t).(*TFn); ok {
			return ft, nil, nil
		}
		return nil, nil, nil
	}
	// A value: field holding a closure, or a method.
	xt := c.infer(fe.X, nil)
	rt := Resolve(xt)
	if tc, ok := rt.(*TCon); ok {
		if tc.Info.Kind == Record {
			for _, fd := range tc.Info.Fields {
				if fd.Name == fe.Name {
					if ft, ok := Resolve(subst(fd.Type, typeArgSubst(tc))).(*TFn); ok {
						c.prog.Fields[fe] = &FieldRef{Kind: FieldRecord, Field: fd, Type: tc.Info}
						info.Kind = CallClosure
						c.record(fe, ft)
						return ft, nil, nil
					}
				}
			}
		}
		fi, impl, ft := c.findMethod(tc, fe.Name)
		if fi != nil {
			if !c.visible(fi.Vis, fi.Mod, c.fn.module) {
				c.privateError(fe.Pos, tc.Info.Name+"."+fe.Name, fi.Mod)
			}
			info.Kind = CallMethod
			info.Impl = impl
			info.Receiver = fe.X
			c.prog.Fields[fe] = &FieldRef{Kind: FieldStaticFn, Fn: fi, Impl: impl, Type: tc.Info}
			c.u.unify(ft.Params[0], xt)
			rest := &TFn{Params: ft.Params[1:], Ret: ft.Ret, Effects: ft.Effects, Explicit: ft.Explicit}
			c.record(fe, rest)
			// Type arguments for the emitter: own params instantiated freshly.
			info.TypeArgs = nil
			info.ImplTypeArgs = nil
			return rest, fi, fe.X
		}
		if tc.Info.Kind == Record || tc.Info.Kind == Newtype || len(tc.Info.Fields) > 0 {
			// Maybe a field access that is not a function.
			for _, fd := range tc.Info.Fields {
				if fd.Name == fe.Name {
					c.errorf("E0402", fe.Pos, "type mismatch").Label("field `%s` is `%s`, not a function", fe.Name, TypeString(fd.Type))
					return nil, nil, nil
				}
			}
		}
		dg := c.errorf("E0407", fe.Pos, "`%s` has no method `%s`", TypeString(xt), fe.Name)
		dg.Label("no `impl` in scope defines `%s` for `%s`", fe.Name, qualifiedName(tc.Info))
		return nil, nil, nil
	}
	if _, ok := rt.(*TVar); ok {
		c.errorf("E0403", fe.X.ExprPos(), "cannot infer the type of this expression; add a type annotation")
		return nil, nil, nil
	}
	if tt, ok := rt.(*TTuple); ok {
		if i, err := strconv.Atoi(fe.Name); err == nil && i >= 0 && i < len(tt.Elems) {
			if ft, ok := Resolve(tt.Elems[i]).(*TFn); ok {
				c.prog.Fields[fe] = &FieldRef{Kind: FieldTupleIndex, Index: i}
				info.Kind = CallClosure
				return ft, nil, nil
			}
		}
	}
	c.errorf("E0407", fe.Pos, "`%s` has no method `%s`", TypeString(xt), fe.Name)
	return nil, nil, nil
}

// ---- `?` --------------------------------------------------------------------

func (c *Checker) inferTry(e *ast.TryExpr, want Type) Type {
	xt := c.infer(e.X, nil)
	tc, ok := Resolve(xt).(*TCon)
	if !ok {
		if _, isVar := Resolve(xt).(*TVar); isVar {
			c.errorf("E0403", e.X.ExprPos(), "cannot infer the type of this expression; add a type annotation")
		} else {
			c.errorf("E0402", e.Pos, "type mismatch").Label("`?` expects `Result<_, _>` or `Option<_>`, found `%s`", TypeString(xt))
		}
		return c.newVar()
	}
	ti := &TryInfo{From: xt}
	c.prog.Tries[e] = ti
	f := c.fn
	switch {
	case tc.Info.Mod == "prelude" && tc.Info.Name == "Option" && len(tc.Args) == 1:
		ti.Option = true
		c.checkTryContext(e, true, nil, ti)
		return tc.Args[0]
	case tc.Info.Mod == "prelude" && tc.Info.Name == "Result" && len(tc.Args) == 2:
		c.checkTryContext(e, false, tc.Args[1], ti)
		return tc.Args[0]
	}
	_ = f
	c.errorf("E0402", e.Pos, "type mismatch").Label("`?` expects `Result<_, _>` or `Option<_>`, found `%s`", TypeString(xt))
	return c.newVar()
}

// checkTryContext checks that `?` can propagate from the enclosing function
// (or closure), computing the error lift.
func (c *Checker) checkTryContext(e *ast.TryExpr, option bool, e1 Type, ti *TryInfo) {
	f := c.fn
	var ret Type
	switch {
	case len(f.closureRets) > 0:
		ret = f.closureRets[len(f.closureRets)-1]
		if v, ok := Resolve(ret).(*TVar); ok {
			if option {
				c.u.bind(v, con(c.preludeType("Option"), c.newVar()))
			} else {
				c.u.bind(v, con(c.preludeType("Result"), c.newVar(), e1))
			}
		}
	case f.ret == nil:
		// Scenario: `?` fails the scenario.
		ti.To = nil
		return
	default:
		ret = f.ret
	}
	rc, ok := Resolve(ret).(*TCon)
	if !ok || rc.Info.Mod != "prelude" {
		dg := c.errorf("E0402", e.Pos, "`?` in a function that does not return `Result` or `Option`")
		dg.Label("the enclosing function returns `%s`", TypeString(ret))
		return
	}
	if option {
		if rc.Info.Name != "Option" {
			dg := c.errorf("E0402", e.Pos, "`?` on an `Option` in a function returning `%s`", TypeString(ret))
			dg.Helpf("convert first: `….ok_or(…)?`")
		}
		ti.To = ret
		return
	}
	if rc.Info.Name != "Result" || len(rc.Args) != 2 {
		dg := c.errorf("E0402", e.Pos, "`?` on a `Result` in a function returning `%s`", TypeString(ret))
		if rc.Info.Name == "Option" {
			dg.Helpf("convert first: `….ok()?`")
		}
		return
	}
	e2 := rc.Args[1]
	ti.To = ret
	if c.u.probe(e1, e2) {
		c.u.unify(e1, e2)
		return
	}
	if v, ok := Resolve(e2).(*TVar); ok {
		c.u.bind(v, e1)
		return
	}
	if e2c, ok := Resolve(e2).(*TCon); ok && e2c.Info.Kind == Enum {
		m := typeArgSubst(e2c)
		var lift *VariantInfo
		n := 0
		for _, v := range e2c.Info.Variants {
			if len(v.Positional) == 1 && len(v.Named) == 0 && c.u.probe(subst(v.Positional[0], m), e1) {
				lift = v
				n++
			}
		}
		if n == 1 {
			c.u.unify(subst(lift.Positional[0], m), e1)
			ti.Lift = lift
			return
		}
	}
	fnName := "this function"
	if f.info != nil {
		fnName = f.info.Name
	}
	dg := c.errorf("E0430", e.Pos, "`?` cannot convert the error type")
	dg.Label("error type `%s`", TypeString(e1))
	dg.Note("`%s` returns `%s`; `?` lifts `%s` only into `%s`", fnName, TypeString(ret), TypeString(e1), TypeString(e1))
	dg.Note("or into an enum with exactly one variant holding `%s`", TypeString(e1))
	dg.Helpf("map the error first: `%s.map_err(fn(e) => …)?`", exprString(e.X))
}

// ---- record literals --------------------------------------------------------

func (c *Checker) inferRecordLit(e *ast.RecordLit, want Type) Type {
	var ti *TypeInfo
	var variant *VariantInfo
	switch len(e.Path) {
	case 1:
		if t := c.lookupTypeInfo(c.fn.module, e.Path[0]); t != nil {
			ti = t
		} else if v, enum := c.bareVariant(e.Path[0], want, e.Pos); v != nil {
			variant = v
			ti = v.Enum
			_ = enum
		}
	case 2:
		if dep := c.depModule(c.fn.module, e.Path[0]); dep != nil && !isTypeName(e.Path[0]) {
			ti = dep.Types[e.Path[1]]
			if ti != nil && !c.visible(ti.Vis, ti.Mod, c.fn.module) {
				c.privateError(e.Pos, e.Path[1], ti.Mod)
			}
		} else if enum := c.lookupTypeInfo(c.fn.module, e.Path[0]); enum != nil && enum.Kind == Enum {
			for _, v := range enum.Variants {
				if v.Name == e.Path[1] {
					variant = v
					ti = enum
				}
			}
		}
	case 3:
		if dep := c.depModule(c.fn.module, e.Path[0]); dep != nil {
			if enum := dep.Types[e.Path[1]]; enum != nil && enum.Kind == Enum {
				for _, v := range enum.Variants {
					if v.Name == e.Path[2] {
						variant = v
						ti = enum
					}
				}
			}
		}
	}
	if ti == nil {
		c.errorf("E0404", e.Pos, "unknown type `%s`", strings.Join(e.Path, ".")).Label("not defined in this module, its imports, or the prelude")
		for _, f := range e.Fields {
			c.infer(f.Value, nil)
		}
		return c.newVar()
	}
	_, args := c.instantiate(ti.Params)
	t := con(ti, args...)
	if want != nil {
		c.u.tryUnify(t, want)
	}
	m := typeArgSubst(t)
	var fields []*FieldInfo
	switch {
	case variant != nil:
		if len(variant.Named) == 0 {
			dg := c.errorf("E0402", e.Pos, "variant `%s` has no named fields", variant.Name)
			if len(variant.Positional) > 0 {
				dg.Helpf("construct it: `%s(…)`", variant.Name)
			} else {
				dg.Helpf("write `%s` without braces", variant.Name)
			}
			return t
		}
		fields = variant.Named
		c.prog.RecordLits[e] = ti
		c.prog.Refs[&ast.Ident{Pos: e.Pos, Name: variant.Name}] = &Ref{Kind: RefVariant, Variant: variant}
	case ti.Kind == Record:
		if ti.Opaque && ti.Module != c.fn.module && !(c.fn.module.IsScenarios && c.fn.module.Parent == ti.Module) {
			c.errorf("E0408", e.Pos, "`%s` is opaque outside `%s`", ti.Name, ti.Mod).Label("its fields are not accessible")
		}
		fields = ti.Fields
		c.prog.RecordLits[e] = ti
	default:
		dg := c.errorf("E0402", e.Pos, "`%s` is not a record", ti.Name)
		if ti.Kind == Newtype {
			dg.Helpf("construct it: `%s(…)`", ti.Name)
		}
		return t
	}
	if e.Base != nil {
		bt := c.infer(e.Base, t)
		c.expect(e.Base, bt, t, "the `..base` must have the record's type")
	}
	seen := map[string]bool{}
	for _, f := range e.Fields {
		var fd *FieldInfo
		for _, x := range fields {
			if x.Name == f.Name {
				fd = x
			}
		}
		if fd == nil {
			dg := c.errorf("E0402", f.Value.ExprPos(), "`%s` has no field `%s`", ti.Name, f.Name)
			var names []string
			for _, x := range fields {
				names = append(names, "`"+x.Name+"`")
			}
			dg.Label("fields: %s", strings.Join(names, ", "))
			c.infer(f.Value, nil)
			continue
		}
		if seen[f.Name] {
			c.errorf("E0406", f.Value.ExprPos(), "duplicate field `%s`", f.Name)
		}
		seen[f.Name] = true
		ft := subst(fd.Type, m)
		vt := c.infer(f.Value, ft)
		if !c.u.tryUnify(vt, ft) {
			dg := c.errorf("E0402", f.Value.ExprPos(), "type mismatch")
			dg.Label("expected `%s`, found `%s`", TypeString(ft), TypeString(vt))
			dg.Note("field `%s` of `%s` is `%s`; there is no implicit conversion from `%s`", f.Name, ti.Name, TypeString(ft), TypeString(vt))
			if nc, ok := Resolve(ft).(*TCon); ok && nc.Info.Kind == Newtype && c.u.probe(subst(nc.Info.Inner, typeArgSubst(nc)), vt) {
				dg.Helpf("wrap the value: `%s: %s(%s)`", f.Name, nc.Info.Name, exprString(f.Value))
			}
		}
	}
	if e.Base == nil {
		var missing []string
		for _, x := range fields {
			if !seen[x.Name] {
				missing = append(missing, "`"+x.Name+"`")
			}
		}
		if len(missing) > 0 {
			dg := c.errorf("E0402", e.Pos, "missing field(s) %s in `%s { … }`", strings.Join(missing, ", "), ti.Name)
			dg.Helpf("add the missing field(s), or copy the rest from a value: `%s { ..base, … }`", ti.Name)
		}
	}
	return t
}

// ---- llm / decide / approve -------------------------------------------------

func (c *Checker) inferLlm(e *ast.LlmExpr, want Type) Type {
	pt := c.infer(e.Prompt, con(tStr))
	c.expect(e.Prompt, pt, con(tStr), "the prompt of `llm` is `Str`")
	c.infer(e.Input, nil)
	var out Type
	if want != nil {
		if tc, ok := Resolve(want).(*TCon); ok && tc.Info.Mod == "prelude" && tc.Info.Name == "Result" && len(tc.Args) == 2 {
			out = tc.Args[0]
		}
	}
	if out == nil {
		out = c.newVar()
	}
	c.prog.LlmOut[e] = out
	c.pendingSchema = append(c.pendingSchema, schemaCheck{t: out, pos: e.Pos, what: "llm<" + e.Role + ">"})
	c.useEffect(ast.Effect{Name: "llm", Role: e.Role}, e.Pos, "llm<"+e.Role+">")
	return con(c.preludeType("Result"), out, con(c.preludeType("LlmError")))
}

func (c *Checker) inferDecide(e *ast.DecideExpr, want Type) Type {
	c.infer(e.Input, nil)
	out := want
	if out == nil {
		out = c.newVar()
	}
	c.prog.DecideOut[e] = out
	c.pendingSchema = append(c.pendingSchema, schemaCheck{t: out, pos: e.Pos, what: "decide<" + e.Role + ">", decide: true})
	c.useEffect(ast.Effect{Name: "decide", Role: e.Role}, e.Pos, "decide<"+e.Role+">")
	return out
}

func (c *Checker) inferApprove(e *ast.ApproveExpr, want Type) Type {
	t := c.resolveType(&tscope{m: c.fn.module, params: c.fnTypeParams()}, e.Type)
	rt := c.infer(e.Review, con(c.preludeType("Review")))
	c.expect(e.Review, rt, con(c.preludeType("Review")), "`approve` takes a `Review`")
	c.prog.ApproveOut[e] = t
	c.pendingSchema = append(c.pendingSchema, schemaCheck{t: t, pos: e.Pos, what: "approve"})
	c.useEffect(ast.Effect{Name: "approve"}, e.Pos, "approve")
	if c.fn.info != nil {
		c.fn.info.HasApprove = true
		if _, ok := c.approvePos[c.fn.info]; !ok {
			c.approvePos[c.fn.info] = e.Pos
		}
	}
	return con(c.preludeType("Result"), t, con(c.preludeType("Rejected")))
}

type schemaCheck struct {
	t      Type
	pos    ast.Pos
	what   string
	decide bool
}

// checkSchemas runs the deferred E0704 checks once types are known.
func (c *Checker) checkSchemas() {
	for _, s := range c.pendingSchema {
		t := Zonk(s.t)
		if s.decide {
			if tc, ok := t.(*TCon); ok && tc.Info.Kind == Enum && closedEnum(tc.Info) {
				continue
			}
			dg := c.errorf("E0704", s.pos, "type `%s` is not schema-derivable for `%s`", TypeString(t), s.what)
			dg.Note("`decide` chooses a variant of a closed enum whose variants carry no payload")
			continue
		}
		if !c.schemaDerivable(t, map[*TypeInfo]bool{}) {
			dg := c.errorf("E0704", s.pos, "type `%s` is not schema-derivable for `%s`", TypeString(t), s.what)
			dg.Note("schema-derivable: primitives, Duration, newtypes, records, closed enums, tuples, Option, List, Map<Str, _>")
		}
	}
	c.pendingSchema = nil
}

func closedEnum(ti *TypeInfo) bool {
	for _, v := range ti.Variants {
		if len(v.Positional) > 0 || len(v.Named) > 0 {
			return false
		}
	}
	return len(ti.Variants) > 0
}

func (c *Checker) schemaDerivable(t Type, seen map[*TypeInfo]bool) bool {
	switch t := Resolve(t).(type) {
	case *TTuple:
		for _, e := range t.Elems {
			if !c.schemaDerivable(e, seen) {
				return false
			}
		}
		return true
	case *TCon:
		switch t.Info.Kind {
		case Prim:
			return true
		case Newtype:
			return c.schemaDerivable(subst(t.Info.Inner, typeArgSubst(t)), seen)
		case Record:
			if seen[t.Info] {
				return true
			}
			seen[t.Info] = true
			m := typeArgSubst(t)
			for _, f := range t.Info.Fields {
				if !c.schemaDerivable(subst(f.Type, m), seen) {
					return false
				}
			}
			return true
		case Enum:
			if t.Info.Mod == "prelude" && t.Info.Name == "Option" {
				return c.schemaDerivable(t.Args[0], seen)
			}
			if seen[t.Info] {
				return true
			}
			seen[t.Info] = true
			m := typeArgSubst(t)
			for _, v := range t.Info.Variants {
				for _, p := range v.Positional {
					if !c.schemaDerivable(subst(p, m), seen) {
						return false
					}
				}
				for _, f := range v.Named {
					if !c.schemaDerivable(subst(f.Type, m), seen) {
						return false
					}
				}
			}
			return true
		case Extern:
			if t.Info.Mod == "prelude" {
				switch t.Info.Name {
				case "List":
					return c.schemaDerivable(t.Args[0], seen)
				case "Map":
					if kc, ok := Resolve(t.Args[0]).(*TCon); ok && kc.Info == tStr {
						return c.schemaDerivable(t.Args[1], seen)
					}
					return false
				case "Json":
					return true
				}
			}
			return false
		case Alias:
			return c.schemaDerivable(c.aliasTarget(t.Info), seen)
		}
	}
	return false
}

// ---- any ----------------------------------------------------------------------

func (c *Checker) inferAny(e *ast.AnyExpr) Type {
	t := c.resolveType(&tscope{m: c.fn.module, params: c.fnTypeParams()}, e.Type)
	c.prog.AnyTypes[e] = t
	if e.Via != nil {
		vt := c.infer(e.Via, nil)
		want := &TFn{Params: []Type{con(c.preludeType("Gen"))}, Ret: t}
		if !c.u.tryUnify(vt, want) {
			dg := c.errorf("E0402", e.Via.ExprPos(), "type mismatch")
			dg.Label("expected `%s`, found `%s`", TypeString(want), TypeString(vt))
			dg.Note("the generator after `via` takes a `Gen` and returns `%s`", TypeString(t))
		}
	}
	return t
}
