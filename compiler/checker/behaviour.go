package checker

import (
	"fmt"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
)

// checkImpl checks the bodies of an impl block. For a behaviour impl it
// first checks conformance: every associated type, const and required
// method is present (E0701), signatures match after substitution (E0709),
// declared effects stay inside the behaviour's budget (E0702), and no
// method outside the behaviour is declared (E0708).
func (c *Checker) checkImpl(impl *ImplInfo) {
	b := impl.Behaviour
	if b == nil {
		for _, name := range impl.MethodOrder {
			c.checkFnBody(impl.Methods[name], nil, false)
		}
		c.checkInv(impl.TargetInfo, impl.Inv, impl.Module, true)
		return
	}
	d := impl.Decl
	header := fmt.Sprintf("impl %s for %s", behaviourHeader(b, impl), impl.TargetInfo.Name)

	bsub := map[*TParam]Type{b.Self: impl.Target}
	for i, p := range b.Params {
		if i < len(impl.BehaviourArgs) {
			bsub[p] = impl.BehaviourArgs[i]
		}
	}
	for _, name := range b.AssocOrder {
		p := b.Assoc[name]
		if t, ok := impl.Assoc[name]; ok {
			bsub[p] = t
			continue
		}
		dg := c.errorf("E0701", d.Pos, "`%s` is missing `type %s`", header, name)
		dg.Label("required by `%s`", b.Name)
		dg.Helpf("add `type %s = …` to the impl", name)
		bsub[p] = c.newVar()
	}
	for _, k := range b.Decl.Consts {
		if _, ok := impl.Consts[k.Name]; ok {
			continue
		}
		dg := c.errorf("E0701", d.Pos, "`%s` is missing `const %s`", header, k.Name)
		dg.Label("required by `%s`", b.Name)
		dg.Helpf("add `const %s: %s = …` to the impl", k.Name, TypeString(subst(b.Consts[k.Name], bsub)))
	}
	for _, name := range impl.MethodOrder {
		if _, ok := b.Methods[name]; ok {
			continue
		}
		im := impl.Methods[name]
		dg := c.errorf("E0708", im.Pos, "`%s` is not a method of `%s`", name, b.Name)
		dg.Label("not declared by `%s`", b.Name)
		dg.Helpf("move `%s` to `impl %s { … }`, or remove it", name, impl.TargetInfo.Name)
	}
	for _, name := range b.MethodOrder {
		bm := b.Methods[name]
		im, ok := impl.Methods[name]
		if !ok {
			if bm.Decl != nil && bm.Decl.Body != nil {
				impl.Defaults = append(impl.Defaults, name)
				continue
			}
			dg := c.errorf("E0701", d.Pos, "`%s` is missing `%s`", header, name)
			dg.Label("required by `%s`", b.Name)
			dg.Note("%s requires `%s`", b.Name, c.methodSig(bm, nil, false))
			dg.Helpf("add `%s { … }`", c.methodSig(bm, bsub, true))
			continue
		}
		sub := map[*TParam]Type{}
		for k, v := range bsub {
			sub[k] = v
		}
		if len(bm.OwnParams) != len(im.OwnParams) {
			dg := c.errorf("E0709", im.Pos, "`%s` takes %d type parameter(s) in `%s`, found %d", name, len(bm.OwnParams), b.Name, len(im.OwnParams))
			dg.Note("%s requires `%s`", b.Name, c.methodSig(bm, bsub, true))
		} else {
			for i, p := range bm.OwnParams {
				sub[p] = im.OwnParams[i]
			}
		}
		want := subst(bm.Type, sub).(*TFn)
		got := im.Type
		mismatch := ""
		switch {
		case len(want.Params) != len(got.Params):
			mismatch = fmt.Sprintf("takes %d parameter(s), expected %d", len(got.Params), len(want.Params))
		case bm.HasSelf != im.HasSelf:
			if bm.HasSelf {
				mismatch = "must take `self`"
			} else {
				mismatch = "must not take `self`"
			}
		default:
			for i := range want.Params {
				if !typeEqual(want.Params[i], got.Params[i]) {
					pn := name
					if ps := im.Decl_Params(); i < len(ps) {
						pn = ps[i]
					}
					mismatch = fmt.Sprintf("parameter `%s` is `%s`, expected `%s`", pn, TypeString(got.Params[i]), TypeString(want.Params[i]))
					break
				}
			}
			if mismatch == "" && !typeEqual(want.Ret, got.Ret) {
				mismatch = fmt.Sprintf("returns `%s`, expected `%s`", TypeString(got.Ret), TypeString(want.Ret))
			}
		}
		if mismatch != "" {
			dg := c.errorf("E0709", im.Pos, "`%s` does not match its declaration in `%s`", name, b.Name)
			dg.Label("%s", mismatch)
			dg.Note("%s requires `%s`", b.Name, c.methodSig(bm, bsub, true))
			dg.Helpf("change the signature to `%s`", c.methodSig(bm, bsub, true))
		}
		isRun := b.Name == "Workflow" && name == "run" && b.Mod == "prelude"
		if isRun {
			c.workflowRuns = append(c.workflowRuns, im)
		}
		f := c.checkFnBody(im, bm.Type.Effects, isRun)
		// A declared effect outside the budget is reported where the body
		// performs it; one that the body never performs is reported here.
		if im.Decl != nil {
			for _, e := range im.Decl.Effects {
				if effContains(bm.Type.Effects, e) || (f != nil && f.budgetReported[e.String()]) {
					continue
				}
				if isRun {
					dg := c.errorf("E0703", e.Pos, "effect `%s` is not allowed inside `Workflow.run`", e.String())
					dg.Label("declared, not in the budget")
					dg.Note("`run` may use %s", EffectSetString(bm.Type.Effects))
					dg.Helpf("keep workflow state in a `var` or return it from a step")
					continue
				}
				dg := c.errorf("E0702", e.Pos, "effect `%s` exceeds the budget of `%s.%s`", e.String(), b.Name, name)
				dg.Label("declared, not in %s", EffectSetString(bm.Type.Effects))
				dg.Helpf("%s", c.budgetHelp(e, b))
			}
		}
	}
	c.checkInv(impl.TargetInfo, impl.Inv, impl.Module, true)
}

// behaviourHeader renders `Worker` or `Repository<Account, AccountId>`.
func behaviourHeader(b *BehaviourInfo, impl *ImplInfo) string {
	if len(impl.BehaviourArgs) == 0 {
		return b.Name
	}
	var as []string
	for _, a := range impl.BehaviourArgs {
		as = append(as, TypeString(a))
	}
	return b.Name + "<" + strings.Join(as, ", ") + ">"
}

// methodSig renders a behaviour method signature, substituting sub when
// given. withBody=false prints the declaration as written in the behaviour.
func (c *Checker) methodSig(fi *FnInfo, sub map[*TParam]Type, substituted bool) string {
	var t *TFn = fi.Type
	if substituted && sub != nil {
		t = subst(fi.Type, sub).(*TFn)
	}
	var sb strings.Builder
	sb.WriteString("fn " + fi.Name)
	if len(fi.OwnParams) > 0 {
		var ps []string
		for _, p := range fi.OwnParams {
			s := p.Name
			if len(p.Bounds) > 0 {
				s += ": " + strings.Join(p.Bounds, " + ")
			}
			ps = append(ps, s)
		}
		sb.WriteString("<" + strings.Join(ps, ", ") + ">")
	}
	sb.WriteString("(")
	names := fi.Decl_Params()
	for i, pt := range t.Params {
		if i > 0 {
			sb.WriteString(", ")
		}
		if fi.HasSelf && i == 0 {
			sb.WriteString("self")
			continue
		}
		n := ""
		if i < len(names) {
			n = names[i]
		}
		if n == "" {
			n = fmt.Sprintf("p%d", i)
		}
		sb.WriteString(n + ": " + TypeString(pt))
	}
	sb.WriteString(")")
	if !isUnit(t.Ret) {
		sb.WriteString(" -> " + TypeString(t.Ret))
	}
	if len(t.Effects) > 0 {
		sb.WriteString(" with " + EffectSetString(t.Effects))
	}
	return sb.String()
}

// behaviourImplOf returns the impl of behaviour name (from the prelude)
// for the type, if any.
func behaviourImplOf(ti *TypeInfo, name string) *ImplInfo {
	for _, impl := range ti.BImpls {
		if impl.Behaviour != nil && impl.Behaviour.Name == name && impl.Behaviour.Mod == "prelude" {
			return impl
		}
	}
	return nil
}

var _ = ast.Pub
