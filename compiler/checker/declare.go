package checker

import (
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
)

// tscope is the scope in which a type expression is resolved.
type tscope struct {
	m      *Module
	params []*TParam
	self   Type
}

func (sc *tscope) param(name string) *TParam {
	for i := len(sc.params) - 1; i >= 0; i-- {
		if sc.params[i].Name == name {
			return sc.params[i]
		}
	}
	return nil
}

func (sc *tscope) with(ps ...*TParam) *tscope {
	n := &tscope{m: sc.m, self: sc.self}
	n.params = append(append([]*TParam{}, sc.params...), ps...)
	return n
}

// ---- lookup ---------------------------------------------------------------

// lookupTypeInfo finds a single-segment type name as seen from module m.
func (c *Checker) lookupTypeInfo(m *Module, name string) *TypeInfo {
	if m == nil {
		return c.prelude.Types[name]
	}
	if t, ok := m.Types[name]; ok {
		return t
	}
	if m.IsScenarios && m.Parent != nil {
		if t, ok := m.Parent.Types[name]; ok {
			return t
		}
		if t, ok := c.testing.Types[name]; ok {
			return t
		}
	}
	if m != c.prelude {
		if t, ok := c.prelude.Types[name]; ok {
			return t
		}
	}
	return nil
}

// lookupFn finds a single-segment function name as seen from module m.
func (c *Checker) lookupFn(m *Module, name string) *FnInfo {
	if f, ok := m.Fns[name]; ok {
		return f
	}
	if m.IsScenarios && m.Parent != nil {
		if f, ok := m.Parent.Fns[name]; ok {
			return f
		}
		if f, ok := c.testing.Fns[name]; ok {
			return f
		}
	}
	if m != c.prelude {
		if f, ok := c.prelude.Fns[name]; ok {
			return f
		}
	}
	return nil
}

// lookupConst finds a single-segment const name as seen from module m.
func (c *Checker) lookupConst(m *Module, name string) *ConstInfo {
	if k, ok := m.Consts[name]; ok {
		return k
	}
	if m.IsScenarios && m.Parent != nil {
		if k, ok := m.Parent.Consts[name]; ok {
			return k
		}
	}
	if m != c.prelude {
		if k, ok := c.prelude.Consts[name]; ok {
			return k
		}
	}
	return nil
}

func (c *Checker) lookupBehaviour(m *Module, name string) *BehaviourInfo {
	if b, ok := m.Behaviours[name]; ok {
		return b
	}
	if m.IsScenarios && m.Parent != nil {
		if b, ok := m.Parent.Behaviours[name]; ok {
			return b
		}
	}
	return c.prelude.Behaviours[name]
}

// visible reports whether an item with visibility vis declared in module
// itemMod can be named from module from.
func (c *Checker) visible(vis ast.Visibility, itemMod string, from *Module) bool {
	if from == nil || from.Path == itemMod {
		return true
	}
	switch vis {
	case ast.Pub:
		return true
	case ast.PubModule:
		parent := itemMod
		if i := strings.LastIndex(itemMod, "."); i >= 0 {
			parent = itemMod[:i]
		}
		return from.Path == parent || strings.HasPrefix(from.Path, parent+".")
	default:
		return from.Parent != nil && from.Parent.Path == itemMod
	}
}

// depModule resolves a `use` alias from module m, marking it used.
func (c *Checker) depModule(m *Module, alias string) *Module {
	if m == nil {
		return nil
	}
	if d, ok := m.Deps[alias]; ok {
		m.UsedAliases[alias] = true
		return d
	}
	if m.IsScenarios && m.Parent != nil {
		if d, ok := m.Parent.Deps[alias]; ok {
			m.Parent.UsedAliases[alias] = true
			return d
		}
	}
	return nil
}

func (c *Checker) privateError(pos ast.Pos, name, itemMod string) {
	d := c.errorf("E0408", pos, "`%s` is private to `%s`", name, itemMod)
	d.Label("not `pub`")
	d.Helpf("mark it `pub` in `%s`, or use a public item", itemMod)
}

// ---- type resolution ------------------------------------------------------

// resolveType turns a type expression into a Type, reporting unknown names.
func (c *Checker) resolveType(sc *tscope, te ast.TypeExpr) Type {
	switch te := te.(type) {
	case nil:
		return unit
	case *ast.TupleType:
		if len(te.Elems) == 0 {
			return unit
		}
		t := &TTuple{}
		for _, e := range te.Elems {
			t.Elems = append(t.Elems, c.resolveType(sc, e))
		}
		return t
	case *ast.FnType:
		f := &TFn{Explicit: te.Effects != nil, Effects: te.Effects}
		for _, p := range te.Params {
			f.Params = append(f.Params, c.resolveType(sc, p))
		}
		f.Ret = c.resolveType(sc, te.Ret)
		return f
	case *ast.TypePath:
		return c.resolveTypePath(sc, te)
	}
	c.errorf("E0404", te.TypePos(), "unsupported type expression")
	return c.newVar()
}

func (c *Checker) resolveTypePath(sc *tscope, tp *ast.TypePath) Type {
	name := tp.Path[len(tp.Path)-1]
	var info *TypeInfo
	if len(tp.Path) == 1 {
		if p := sc.param(name); p != nil {
			if len(tp.Args) > 0 {
				c.errorf("E0405", tp.Pos, "type parameter `%s` takes no type arguments", name)
			}
			return p
		}
		if name == "Self" {
			if sc.self == nil {
				c.errorf("E0404", tp.Pos, "`Self` outside an `impl` or `behaviour`")
				return c.newVar()
			}
			return sc.self
		}
		info = c.lookupTypeInfo(sc.m, name)
		if info == nil {
			d := c.errorf("E0404", tp.Pos, "unknown type `%s`", name)
			d.Label("not defined in this module, its imports, or the prelude")
			return c.newVar()
		}
	} else {
		alias := strings.Join(tp.Path[:len(tp.Path)-1], ".")
		dep := c.depModule(sc.m, alias)
		if dep == nil {
			d := c.errorf("E0404", tp.Pos, "unknown type `%s.%s`", alias, name)
			d.Note("no `use %s` in this module", alias)
			return c.newVar()
		}
		info = dep.Types[name]
		if info == nil {
			d := c.errorf("E0404", tp.Pos, "unknown type `%s.%s`", alias, name)
			d.Label("`%s` has no type `%s`", dep.Path, name)
			return c.newVar()
		}
		if !c.visible(info.Vis, info.Mod, sc.m) {
			c.privateError(tp.Pos, alias+"."+name, info.Mod)
		}
	}
	if len(tp.Args) != len(info.Params) {
		c.errorf("E0405", tp.Pos, "`%s` takes %d type argument(s), found %d", info.Name, len(info.Params), len(tp.Args))
		return c.newVar()
	}
	args := make([]Type, len(tp.Args))
	for i, a := range tp.Args {
		args[i] = c.resolveType(sc, a)
	}
	if info.Kind == Alias {
		target := c.aliasTarget(info)
		if target == nil {
			return c.newVar()
		}
		m := map[*TParam]Type{}
		for i, p := range info.Params {
			m[p] = args[i]
		}
		return subst(target, m)
	}
	return con(info, args...)
}

// aliasTarget resolves an alias lazily (aliases may be declared in any order).
func (c *Checker) aliasTarget(info *TypeInfo) Type {
	if info.Inner != nil {
		return info.Inner
	}
	if c.resolving == nil {
		c.resolving = map[*TypeInfo]bool{}
	}
	if c.resolving[info] {
		c.errorf("E0404", info.Pos, "type alias `%s` refers to itself", info.Name)
		return nil
	}
	d, ok := info.Decl.(*ast.TypeDecl)
	if !ok || d.Inner == nil {
		return nil
	}
	c.resolving[info] = true
	info.Inner = c.resolveType(&tscope{m: info.Module, params: info.Params}, d.Inner)
	delete(c.resolving, info)
	return info.Inner
}

func tparams(ps []ast.TypeParam) []*TParam {
	var out []*TParam
	for _, p := range ps {
		out = append(out, &TParam{Name: p.Name, Bounds: p.Bounds})
	}
	return out
}

// ---- phase 1: names -------------------------------------------------------

func (c *Checker) dupError(pos ast.Pos, name string, prev ast.Pos) {
	d := c.errorf("E0406", pos, "duplicate definition of `%s`", name)
	d.Label("already defined at %s", prev)
}

func (c *Checker) declareNames(m *Module) {
	c.mod = m
	curMod = m
	for _, f := range m.Files {
		for _, it := range f.Items {
			switch d := it.(type) {
			case *ast.TypeDecl:
				if prev, ok := m.Types[d.Name]; ok {
					c.dupError(d.Pos, d.Name, prev.Pos)
					continue
				}
				info := &TypeInfo{Name: d.Name, Mod: m.Path, Module: m, Params: tparams(d.Params), Vis: d.Vis, Opaque: d.Opaque, Doc: d.Doc, Inv: d.Inv, Decl: d, Pos: d.Pos}
				switch d.Kind {
				case ast.RecordKind:
					info.Kind = Record
				case ast.NewtypeKind:
					info.Kind = Newtype
				case ast.AliasKind:
					info.Kind = Alias
				case ast.ExternKind:
					info.Kind = Extern
					if d.Extern != nil {
						info.GoPkg, info.GoIdent = d.Extern.Pkg, d.Extern.Ident
					}
				}
				m.Types[d.Name] = info
				m.Order = append(m.Order, d.Name)
			case *ast.EnumDecl:
				if prev, ok := m.Types[d.Name]; ok {
					c.dupError(d.Pos, d.Name, prev.Pos)
					continue
				}
				info := &TypeInfo{Name: d.Name, Mod: m.Path, Module: m, Kind: Enum, Params: tparams(d.Params), Vis: d.Vis, Opaque: false, Doc: d.Doc, Decl: d, Pos: d.Pos}
				m.Types[d.Name] = info
				m.Order = append(m.Order, d.Name)
			case *ast.BehaviourDecl:
				if prev, ok := m.Behaviours[d.Name]; ok {
					c.dupError(d.Pos, d.Name, prev.Decl.Pos)
					continue
				}
				if prev, ok := m.Types[d.Name]; ok {
					c.dupError(d.Pos, d.Name, prev.Pos)
					continue
				}
				b := &BehaviourInfo{Name: d.Name, Mod: m.Path, Module: m, Decl: d, Params: tparams(d.Params), Self: &TParam{Name: "Self"}, Assoc: map[string]*TParam{}, Consts: map[string]Type{}, Methods: map[string]*FnInfo{}, Vis: d.Vis}
				for _, a := range d.Assoc {
					b.Assoc[a] = &TParam{Name: a}
					b.AssocOrder = append(b.AssocOrder, a)
				}
				m.Behaviours[d.Name] = b
				m.Order = append(m.Order, d.Name)
			}
		}
		for i, s := range f.Scenarios {
			m.Scenarios = append(m.Scenarios, &ScenarioInfo{Decl: s, Module: m, Index: i})
		}
	}
}

// ---- phase 2: type bodies -------------------------------------------------

func (c *Checker) declareBodies(m *Module) {
	c.mod = m
	curMod = m
	for _, name := range m.Order {
		info, ok := m.Types[name]
		if !ok {
			continue
		}
		sc := &tscope{m: m, params: info.Params}
		switch d := info.Decl.(type) {
		case *ast.TypeDecl:
			switch info.Kind {
			case Record:
				seen := map[string]bool{}
				for _, f := range d.Fields {
					if seen[f.Name] {
						c.errorf("E0406", f.Pos, "duplicate field `%s` in `%s`", f.Name, info.Name)
						continue
					}
					seen[f.Name] = true
					info.Fields = append(info.Fields, &FieldInfo{Name: f.Name, Type: c.resolveType(sc, f.Type), Pos: f.Pos})
				}
			case Newtype:
				info.Inner = c.resolveType(sc, d.Inner)
			case Alias:
				c.aliasTarget(info)
			}
		case *ast.EnumDecl:
			seen := map[string]bool{}
			for i, v := range d.Variants {
				if seen[v.Name] {
					c.errorf("E0406", v.Pos, "duplicate variant `%s` in `%s`", v.Name, info.Name)
					continue
				}
				seen[v.Name] = true
				vi := &VariantInfo{Name: v.Name, Index: i, Enum: info, Pos: v.Pos}
				for _, p := range v.Positional {
					vi.Positional = append(vi.Positional, c.resolveType(sc, p))
				}
				for _, f := range v.Named {
					vi.Named = append(vi.Named, &FieldInfo{Name: f.Name, Type: c.resolveType(sc, f.Type), Pos: f.Pos})
				}
				info.Variants = append(info.Variants, vi)
			}
		}
		if m.IsStd {
			continue
		}
		if info.Vis == ast.Pub && info.Doc == "" {
			c.missingDoc(info.Pos, info.Name)
		}
		if info.Vis != ast.Private && !info.Opaque {
			for _, f := range info.Fields {
				c.checkExported(info.Vis, info.Name, f.Type, f.Pos)
			}
			if info.Kind == Newtype {
				c.checkExported(info.Vis, info.Name, info.Inner, info.Pos)
			}
			for _, v := range info.Variants {
				for _, p := range v.Positional {
					c.checkExported(info.Vis, info.Name, p, v.Pos)
				}
				for _, f := range v.Named {
					c.checkExported(info.Vis, info.Name, f.Type, f.Pos)
				}
			}
		}
	}
}

func (c *Checker) missingDoc(pos ast.Pos, name string) {
	d := c.errorf("E0105", pos, "missing doc comment on `%s`", name)
	d.Label("every `pub` item needs one `///` line")
	d.Helpf("add `/// <one line describing %s>` above the declaration", name)
}

// checkExported reports E0104 when a type named in an exported signature is
// less visible than the item that exports it.
func (c *Checker) checkExported(ownerVis ast.Visibility, owner string, t Type, pos ast.Pos) {
	t = Resolve(t)
	switch t := t.(type) {
	case *TCon:
		info := t.Info
		if info.Module != nil && !info.Module.IsStd && info.Mod != "appcfg" && info.Vis < ownerVis {
			d := c.errorf("E0104", pos, "type `%s` is not `pub` but appears in the public signature of `%s`", info.Name, owner)
			d.Helpf("mark `%s` as `pub`, or make `%s` private", info.Name, owner)
		}
		for _, a := range t.Args {
			c.checkExported(ownerVis, owner, a, pos)
		}
	case *TFn:
		for _, p := range t.Params {
			c.checkExported(ownerVis, owner, p, pos)
		}
		c.checkExported(ownerVis, owner, t.Ret, pos)
	case *TTuple:
		for _, e := range t.Elems {
			c.checkExported(ownerVis, owner, e, pos)
		}
	}
}

// ---- phase 3: signatures --------------------------------------------------

// fnSig builds the TFn of a declaration. self, when non-nil, is the type of a
// `self` parameter.
func (c *Checker) fnSig(sc *tscope, d *ast.FnDecl, self Type) (*TFn, bool) {
	f := &TFn{Effects: d.Effects, Explicit: d.HasWith}
	hasSelf := false
	for i, p := range d.Params {
		if p.Self {
			if i != 0 {
				c.errorf("E0404", p.Pos, "`self` must be the first parameter")
			}
			hasSelf = true
			if self == nil {
				c.errorf("E0404", p.Pos, "`self` parameter outside an `impl` or `behaviour`")
				f.Params = append(f.Params, c.newVar())
			} else {
				f.Params = append(f.Params, self)
			}
			continue
		}
		f.Params = append(f.Params, c.resolveType(sc, p.Type))
	}
	f.Ret = c.resolveType(sc, d.Ret)
	return f, hasSelf
}

func (c *Checker) declareSigs(m *Module) {
	c.mod = m
	curMod = m
	for _, f := range m.Files {
		for _, it := range f.Items {
			switch d := it.(type) {
			case *ast.FnDecl:
				c.declareFn(m, d)
			case *ast.ConstDecl:
				c.declareConst(m, d)
			case *ast.BehaviourDecl:
				c.declareBehaviour(m, m.Behaviours[d.Name])
			}
		}
	}
	// Impls after every free signature so impl bodies can name them.
	for _, f := range m.Files {
		for _, it := range f.Items {
			if d, ok := it.(*ast.ImplDecl); ok {
				c.declareImpl(m, d)
			}
		}
	}
}

func (c *Checker) declareFn(m *Module, d *ast.FnDecl) {
	if prev, ok := m.Fns[d.Name]; ok {
		c.dupError(d.Pos, d.Name, prev.Pos)
		return
	}
	if prev, ok := m.Consts[d.Name]; ok {
		c.dupError(d.Pos, d.Name, prev.Pos)
		return
	}
	own := tparams(d.TypeParams)
	sc := &tscope{m: m, params: own}
	t, _ := c.fnSig(sc, d, nil)
	fi := &FnInfo{Name: d.Name, Mod: m.Path, Module: m, Decl: d, TypeParams: own, OwnParams: own, Type: t, Pos: d.Pos, Vis: d.Vis, Doc: d.Doc, Extern: d.Extern, Unsafe: d.Unsafe}
	m.Fns[d.Name] = fi
	m.Order = append(m.Order, d.Name)
	if d.Extern != nil {
		m.Externs = append(m.Externs, fi)
		if d.Unsafe {
			c.prog.UnsafeExterns = append(c.prog.UnsafeExterns, fi)
		}
	}
	if m.IsStd {
		return
	}
	if (d.Vis == ast.Pub || d.Extern != nil) && d.Doc == "" {
		c.missingDoc(d.Pos, d.Name)
	}
	if d.Vis != ast.Private {
		for i, p := range t.Params {
			c.checkExported(d.Vis, d.Name, p, d.Params[i].Pos)
		}
		c.checkExported(d.Vis, d.Name, t.Ret, d.Pos)
	}
}

func (c *Checker) declareConst(m *Module, d *ast.ConstDecl) {
	if prev, ok := m.Consts[d.Name]; ok {
		c.dupError(d.Pos, d.Name, prev.Pos)
		return
	}
	if prev, ok := m.Fns[d.Name]; ok {
		c.dupError(d.Pos, d.Name, prev.Pos)
		return
	}
	var t Type
	if d.Type != nil {
		t = c.resolveType(&tscope{m: m}, d.Type)
	} else {
		t = c.newVar()
	}
	ci := &ConstInfo{Name: d.Name, Module: m, Decl: d, Type: t, Vis: d.Vis, Doc: d.Doc, Pos: d.Pos}
	m.Consts[d.Name] = ci
	m.Order = append(m.Order, d.Name)
	if m.IsStd {
		return
	}
	if d.Vis == ast.Pub && d.Doc == "" {
		c.missingDoc(d.Pos, d.Name)
	}
	if d.Vis != ast.Private && d.Type != nil {
		c.checkExported(d.Vis, d.Name, t, d.Pos)
	}
}

func (c *Checker) declareBehaviour(m *Module, b *BehaviourInfo) {
	if b == nil {
		return
	}
	d := b.Decl
	sc := &tscope{m: m, params: append(append([]*TParam{}, b.Params...), assocParams(b)...), self: b.Self}
	for _, k := range d.Consts {
		var t Type
		switch {
		case k.Type != nil:
			t = c.resolveType(sc, k.Type)
		case k.Value != nil:
			t = litType(k.Value)
		}
		if t == nil {
			c.errorf("E0403", k.Pos, "cannot infer the type of const `%s`; add a type annotation", k.Name)
			t = c.newVar()
		}
		if _, dup := b.Consts[k.Name]; dup {
			c.dupError(k.Pos, k.Name, k.Pos)
			continue
		}
		b.Consts[k.Name] = t
	}
	for _, md := range d.Methods {
		if prev, dup := b.Methods[md.Name]; dup {
			c.dupError(md.Pos, md.Name, prev.Pos)
			continue
		}
		t, hasSelf := c.fnSig(sc.with(tparams(md.TypeParams)...), md, b.Self)
		fi := &FnInfo{Name: md.Name, Mod: m.Path, Module: m, Decl: md, OwnParams: tparams(md.TypeParams), Type: t, HasSelf: hasSelf, Pos: md.Pos, Vis: ast.Pub, Doc: md.Doc}
		fi.TypeParams = fi.OwnParams
		b.Methods[md.Name] = fi
		b.MethodOrder = append(b.MethodOrder, md.Name)
	}
	if !m.IsStd && d.Vis == ast.Pub && d.Doc == "" {
		c.missingDoc(d.Pos, d.Name)
	}
}

func assocParams(b *BehaviourInfo) []*TParam {
	var out []*TParam
	for _, a := range b.AssocOrder {
		out = append(out, b.Assoc[a])
	}
	return out
}

// implicitParams collects the single-segment, argument-less type names in
// the impl header that do not name a known type: `impl List<T>` binds T.
func (c *Checker) implicitParams(m *Module, d *ast.ImplDecl) []*TParam {
	var out []*TParam
	seen := map[string]bool{}
	var walk func(te ast.TypeExpr)
	walk = func(te ast.TypeExpr) {
		switch te := te.(type) {
		case *ast.TypePath:
			if len(te.Path) == 1 && len(te.Args) == 0 && te.Path[0] != "Self" && c.lookupTypeInfo(m, te.Path[0]) == nil {
				if !seen[te.Path[0]] {
					seen[te.Path[0]] = true
					out = append(out, &TParam{Name: te.Path[0]})
				}
				return
			}
			for _, a := range te.Args {
				walk(a)
			}
		case *ast.TupleType:
			for _, e := range te.Elems {
				walk(e)
			}
		case *ast.FnType:
			for _, p := range te.Params {
				walk(p)
			}
			walk(te.Ret)
		}
	}
	for _, a := range d.BehaviourArgs {
		walk(a)
	}
	walk(d.Target)
	return out
}

func (c *Checker) declareImpl(m *Module, d *ast.ImplDecl) {
	params := c.implicitParams(m, d)
	sc := &tscope{m: m, params: params}
	target := c.resolveType(sc, d.Target)
	tc, ok := Resolve(target).(*TCon)
	if !ok {
		c.errorf("E0404", d.Target.TypePos(), "`impl` target must be a named type")
		return
	}
	sc.self = target
	impl := &ImplInfo{Decl: d, Module: m, Target: target, TargetInfo: tc.Info, TypeParams: params, Assoc: map[string]Type{}, Consts: map[string]*ConstInfo{}, Methods: map[string]*FnInfo{}, Inv: d.Inv, Name: tc.Info.Name}
	m.Impls = append(m.Impls, impl)

	var bsub map[*TParam]Type
	if d.Behaviour != "" {
		b := c.lookupBehaviour(m, d.Behaviour)
		if b == nil {
			if d.Behaviour == "Agent" {
				dg := c.errorf("E0706", d.Pos, "`%s` is not available yet", d.Behaviour)
				dg.Label("`Agent` is deferred (revision-plan R1)")
				dg.Helpf("call `llm` and `decide` from a `Workflow` step instead")
			} else {
				c.errorf("E0404", d.Pos, "unknown behaviour `%s`", d.Behaviour)
			}
			return
		}
		impl.Behaviour = b
		if len(d.BehaviourArgs) != len(b.Params) {
			c.errorf("E0405", d.Pos, "`%s` takes %d type argument(s), found %d", b.Name, len(b.Params), len(d.BehaviourArgs))
			return
		}
		bsub = map[*TParam]Type{b.Self: target}
		for i, a := range d.BehaviourArgs {
			t := c.resolveType(sc, a)
			impl.BehaviourArgs = append(impl.BehaviourArgs, t)
			bsub[b.Params[i]] = t
		}
		for _, a := range d.Assoc {
			if _, known := b.Assoc[a.Name]; !known {
				dg := c.errorf("E0708", a.Type.TypePos(), "`%s` is not an associated type of `%s`", a.Name, b.Name)
				dg.Helpf("remove `type %s = …`", a.Name)
				continue
			}
			impl.Assoc[a.Name] = c.resolveType(sc, a.Type)
		}
		for name, p := range b.Assoc {
			if t, ok := impl.Assoc[name]; ok {
				bsub[p] = t
			}
		}
		for _, k := range d.Consts {
			bt, known := b.Consts[k.Name]
			if !known {
				dg := c.errorf("E0708", k.Pos, "`%s` is not a const of `%s`", k.Name, b.Name)
				dg.Helpf("remove `const %s`", k.Name)
				continue
			}
			t := subst(bt, bsub)
			if k.Type != nil {
				declared := c.resolveType(sc, k.Type)
				if !typeEqual(declared, t) {
					dg := c.errorf("E0709", k.Pos, "const `%s` has type `%s` in `%s`", k.Name, TypeString(t), b.Name)
					dg.Label("declared as `%s`", TypeString(declared))
				}
			}
			impl.Consts[k.Name] = &ConstInfo{Name: k.Name, Module: m, Decl: k, Type: t, Impl: impl, Vis: ast.Pub, Doc: k.Doc, Pos: k.Pos}
		}
		tc.Info.BImpls = append(tc.Info.BImpls, impl)
	} else {
		if len(d.Assoc) > 0 || len(d.Consts) > 0 {
			c.errorf("E0404", d.Pos, "an inherent `impl` cannot declare associated types or consts")
		}
		tc.Info.Impls = append(tc.Info.Impls, impl)
	}

	for _, md := range d.Methods {
		if prev, dup := impl.Methods[md.Name]; dup {
			c.dupError(md.Pos, md.Name, prev.Pos)
			continue
		}
		own := tparams(md.TypeParams)
		t, hasSelf := c.fnSig(sc.with(own...), md, target)
		fi := &FnInfo{Name: md.Name, Mod: m.Path, Module: m, Decl: md, TypeParams: append(append([]*TParam{}, params...), own...), OwnParams: own, Type: t, HasSelf: hasSelf, Impl: impl, Pos: md.Pos, Vis: md.Vis, Doc: md.Doc, Extern: md.Extern, Unsafe: md.Unsafe}
		if d.Behaviour != "" {
			fi.Vis = ast.Pub
		}
		impl.Methods[md.Name] = fi
		impl.MethodOrder = append(impl.MethodOrder, md.Name)
		if md.Extern != nil {
			m.Externs = append(m.Externs, fi)
			if md.Unsafe {
				c.prog.UnsafeExterns = append(c.prog.UnsafeExterns, fi)
			}
		}
		if !m.IsStd && (md.Vis == ast.Pub || md.Extern != nil) && md.Doc == "" && d.Behaviour == "" {
			c.missingDoc(md.Pos, tc.Info.Name+"."+md.Name)
		}
	}
}
