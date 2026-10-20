package checker

import (
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
)

// Agent support (spec 07, rules 17 to 23). Each `impl Agent for X` gets a
// generated `pub enum XTools` with one variant per entry of `const tools`;
// inside the impl `Tools` names it, and bare `Event` and `ToolCall` mean
// `Event<Tools>` and `ToolCall<Tools>`.

// isAgent says whether d implements the prelude `Agent`.
func isAgent(d *ast.ImplDecl) bool { return d.Behaviour == "Agent" && len(d.BehaviourArgs) == 0 }

// ToolVariant is the variant of a tool function: `issue_refund` is
// `IssueRefund`.
func ToolVariant(fn string) string {
	var b strings.Builder
	for _, part := range strings.Split(fn, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}

// implConst is the const `name` of an impl declaration, or nil.
func implConst(d *ast.ImplDecl, name string) *ast.ConstDecl {
	for _, k := range d.Consts {
		if k.Name == name {
			return k
		}
	}
	return nil
}

// declareAgentTools declares the Tools enum of an Agent impl in m and
// rewrites `const model = smart` to `Role("smart")`.
func (c *Checker) declareAgentTools(m *Module, d *ast.ImplDecl) {
	tp, ok := d.Target.(*ast.TypePath)
	if !ok {
		return
	}
	if k := implConst(d, "model"); k != nil {
		if id, ok := k.Value.(*ast.Ident); ok {
			k.Value = &ast.CallExpr{Pos: id.Pos, Fn: &ast.Ident{Pos: id.Pos, Name: "Role"}, Args: []ast.Expr{&ast.StrLit{Pos: id.Pos, Parts: []ast.StrPart{{Lit: id.Name}}}}}
		}
	}
	target := tp.Path[len(tp.Path)-1]
	ed := &ast.EnumDecl{Pos: d.Pos, Doc: "Tools of `" + target + "`.", Vis: ast.Pub, Name: target + "Tools"}
	if k := implConst(d, "tools"); k != nil {
		ed.Pos = k.Pos
		if te, ok := k.Value.(*ast.ToolsExpr); ok {
			for _, f := range te.Fns {
				ed.Variants = append(ed.Variants, ast.Variant{Pos: f.Pos, Name: ToolVariant(f.Name)})
			}
		}
	}
	if prev, ok := m.Types[ed.Name]; ok {
		dg := c.errorf("E0406", ed.Pos, "duplicate definition of `%s`", ed.Name)
		dg.Label("generated for the tools of `%s`; already defined at %s", target, prev.Pos)
		return
	}
	info := &TypeInfo{Name: ed.Name, Mod: m.Path, Module: m, Kind: Enum, Vis: ast.Pub, Doc: ed.Doc, Decl: ed, Pos: ed.Pos}
	m.Types[ed.Name] = info
	m.Order = append(m.Order, ed.Name)
	if c.agentEnums == nil {
		c.agentEnums = map[*ast.ImplDecl]*TypeInfo{}
	}
	c.agentEnums[d] = info
}

// agentType resolves `Tools`, and bare `Event` and `ToolCall`, inside an
// Agent impl; nil otherwise.
func (c *Checker) agentType(sc *tscope, tp *ast.TypePath) Type {
	if c.agentTools == nil || len(tp.Path) != 1 || len(tp.Args) != 0 || sc.m != c.agentTools.Module {
		return nil
	}
	switch name := tp.Path[0]; name {
	case "Tools":
		return con(c.agentTools)
	case "Event", "ToolCall":
		if info := c.lookupTypeInfo(sc.m, name); info != nil && info.Mod == "prelude" {
			return con(info, con(c.agentTools))
		}
	}
	return nil
}

// agentToolFns are the functions named by the `const tools` of an Agent impl
// that resolve to module functions; inferTools reports the others.
func (c *Checker) agentToolFns(impl *ImplInfo) []*FnInfo {
	k := implConst(impl.Decl, "tools")
	if k == nil {
		return nil
	}
	te, ok := k.Value.(*ast.ToolsExpr)
	if !ok {
		return nil
	}
	var out []*FnInfo
	for _, id := range te.Fns {
		if fi := c.lookupFn(impl.Module, id.Name); fi != nil && fi.Module == impl.Module {
			out = append(out, fi)
		}
	}
	return out
}

// AgentRole is the role of `const model`, after declareAgentTools rewrote
// it to `Role("smart")`.
func AgentRole(impl *ImplInfo) string {
	if impl.Decl == nil {
		return ""
	}
	k := implConst(impl.Decl, "model")
	if k == nil {
		return ""
	}
	if ce, ok := k.Value.(*ast.CallExpr); ok && len(ce.Args) == 1 {
		if s, ok := ce.Args[0].(*ast.StrLit); ok && len(s.Parts) == 1 && s.Parts[0].Expr == nil {
			return s.Parts[0].Lit
		}
	}
	return ""
}

// agentRun is the type of `X.run`: `fn(Str) -> Outcome<Answer>` with
// `{llm<model>}` joined by the effects of `gate` and of every tool (rule 18).
// An `approve` among them makes the caller an approving function (E0707).
func (c *Checker) agentRun(impl *ImplInfo, pos ast.Pos) Type {
	ans := impl.Assoc["Answer"]
	if ans == nil {
		ans = c.newVar()
	}
	effs := []ast.Effect{{Name: "llm", Role: AgentRole(impl)}}
	calls := c.agentToolFns(impl)
	if g := impl.Methods["gate"]; g != nil {
		calls = append(calls, g)
	}
	for _, fi := range calls {
		effs = effUnion(effs, fi.Type.Effects)
		if f := c.fnInfo(); f != nil {
			f.Calls = append(f.Calls, fi)
		}
	}
	if fi := c.fnInfo(); fi != nil && effContains(effs, ast.Effect{Name: "approve"}) {
		fi.HasApprove = true
		if _, ok := c.approvePos[fi]; !ok {
			c.approvePos[fi] = pos
		}
	}
	return &TFn{Params: []Type{con(tStr)}, Ret: con(c.preludeType("Outcome"), ans), Effects: effs, Explicit: true}
}

// inferTools checks `tools [f, g]`: only the `const tools` of an Agent, and
// every entry a schema-derivable `pub fn` of the impl's module (rules 17, 18).
func (c *Checker) inferTools(e *ast.ToolsExpr) Type {
	list := con(c.preludeType("List"), con(c.preludeType("Tool")))
	impl := c.toolsImpl
	if impl == nil {
		dg := c.errorf("E0706", e.Pos, "`tools [...]` outside an `Agent`")
		dg.Helpf("write it as the `const tools` of an `impl Agent`")
		return list
	}
	for _, id := range e.Fns {
		fi := c.lookupFn(impl.Module, id.Name)
		if fi == nil {
			c.errorf("E0407", id.Pos, "unknown name `%s`", id.Name)
			continue
		}
		fi.Used = true
		c.prog.Refs[id] = &Ref{Kind: RefFn, Fn: fi}
		if fi.Module != impl.Module || fi.Vis != ast.Pub || len(fi.OwnParams) > 0 || fi.Extern != nil {
			dg := c.errorf("E0706", id.Pos, "`%s` cannot be a tool", id.Name)
			switch {
			case fi.Module != impl.Module:
				dg.Label("not a function of module `%s`", impl.Module.Path)
			case fi.Vis != ast.Pub:
				dg.Label("not `pub`")
				dg.Helpf("declare it `pub fn %s`", id.Name)
			case fi.Extern != nil:
				dg.Label("an `extern` function")
				dg.Helpf("wrap it in a `pub fn` that declares its effects")
			default:
				dg.Label("generic")
			}
			continue
		}
		what := "tool `" + id.Name + "`"
		for _, p := range fi.Type.Params {
			c.pendingSchema = append(c.pendingSchema, schemaCheck{t: p, pos: id.Pos, what: what})
		}
		ret := fi.Type.Ret
		if tc, ok := Resolve(ret).(*TCon); ok && tc.Info.Name == "Result" && tc.Info.Mod == "prelude" && len(tc.Args) == 2 {
			c.pendingSchema = append(c.pendingSchema, schemaCheck{t: tc.Args[0], pos: id.Pos, what: what}, schemaCheck{t: tc.Args[1], pos: id.Pos, what: what})
		} else {
			c.pendingSchema = append(c.pendingSchema, schemaCheck{t: ret, pos: id.Pos, what: what})
		}
	}
	return list
}

// budgetMissing reports E0705 for a `Budget { … }` literal without both
// limits (rule 19).
func (c *Checker) budgetMissing(ci *ConstInfo) bool {
	rl, ok := ci.Decl.Value.(*ast.RecordLit)
	if !ok || rl.Base != nil {
		return false
	}
	have := map[string]bool{}
	for _, f := range rl.Fields {
		have[f.Name] = true
	}
	for _, name := range []string{"max_turns", "max_cost"} {
		if !have[name] {
			dg := c.errorf("E0705", rl.Pos, "`Agent.budget` is missing `%s`", name)
			dg.Label("expected `Budget { max_turns: Int, max_cost: Money }`")
			dg.Helpf("write `Budget { max_turns: 12, max_cost: Money(500) }`; both limits are required")
			return true
		}
	}
	return false
}

// fnInfo is the function being checked, or nil.
func (c *Checker) fnInfo() *FnInfo {
	if c.fn == nil {
		return nil
	}
	return c.fn.info
}
