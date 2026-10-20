package checker

import (
	"fmt"
	"sort"
	"strings"

	"github.com/codingsar/jippitese/compiler/app"
	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/diag"
	"github.com/codingsar/jippitese/compiler/parser"
	"github.com/codingsar/jippitese/compiler/stdlib"
)

// stdOrder is the order in which the embedded standard library is loaded.
// The prelude is implicit in every module; the others are reached by `use`.
var stdOrder = []string{"prelude", "std.json", "std.time", "std.math", "std.fs", "std.http", "std.gen", "std.testing"}

// Check type-checks a whole program. The caller has already added the user
// sources to bag; the checker adds the standard library sources itself.
// Diagnostics land in bag; the returned Program is complete only when
// bag.HasErrors() is false.
func Check(in *Input, bag *diag.Bag) *Program {
	c := &Checker{bag: bag, prog: newProgram(), approvePos: map[*FnInfo]ast.Pos{}}
	p := c.prog
	p.App = in.App
	if in.App != nil {
		p.AppName = in.App.Name
	}

	// Standard library.
	var std []*Module
	for _, name := range stdOrder {
		src := stdlib.Source(name)
		path := stdlib.Path(name)
		bag.AddSource(path, src)
		f, err := parser.ParseFile(path, src)
		if err != nil {
			pos := ast.Pos{File: path, Line: 1, Col: 1}
			if pe, ok := err.(*parser.Error); ok {
				pos = pe.Pos
			}
			bag.Errorf("E0000", pos, "internal error: standard library module `%s` does not parse: %v", name, err)
			return p
		}
		m := newModule(name, "", []*ast.File{f})
		m.IsStd = true
		std = append(std, m)
		p.ByPath[name] = m
		switch name {
		case "prelude":
			c.prelude, p.Prelude = m, m
		case "std.testing":
			c.testing, p.Testing = m, m
		}
	}
	for _, t := range []*TypeInfo{tInt, tFloat, tBool, tStr, tBytes, tChar, tDuration} {
		t.Module = c.prelude
		c.prelude.Types[t.Name] = t
		c.prelude.Order = append(c.prelude.Order, t.Name)
	}

	// User modules.
	var users []*Module
	for _, src := range in.Modules {
		if _, dup := p.ByPath[src.Path]; dup {
			bag.Errorf("E0406", firstPos(src), "duplicate module `%s`", src.Path)
			continue
		}
		m := newModule(src.Path, src.Dir, src.Files)
		m.IsScenarios, m.IsDep = src.IsScenarios, src.IsDep
		p.ByPath[src.Path] = m
		users = append(users, m)
	}
	for _, m := range users {
		if !m.IsScenarios {
			continue
		}
		parentPath := strings.TrimSuffix(m.Path, ".scenarios")
		parent := p.ByPath[parentPath]
		if parent == nil || parent.IsScenarios {
			bag.Errorf("E0102", firstPos(moduleSrc(in, m.Path)), "scenarios module `%s` has no module `%s` to test", m.Path, parentPath)
			continue
		}
		m.Parent = parent
	}
	for _, m := range users {
		c.resolveUses(m, in.App)
	}
	if bag.HasErrors() {
		return p
	}

	// Dependency order with cycle detection.
	ordered := c.orderModules(users)
	p.Modules = append(append([]*Module{}, std...), ordered...)
	if bag.HasErrors() {
		return p
	}

	// Phases over every module.
	for _, m := range p.Modules {
		c.declareNames(m)
	}
	c.synthesizeAppCfg(in)
	c.declareTopics(in)
	for _, m := range p.Modules {
		c.declareBodies(m)
	}
	for _, m := range p.Modules {
		c.declareSigs(m)
	}
	for _, m := range p.Modules {
		c.checkModule(m)
	}
	c.postChecks()
	curMod = nil
	return p
}

func newModule(path, dir string, files []*ast.File) *Module {
	return &Module{
		Path:        path,
		Dir:         dir,
		Files:       files,
		Deps:        map[string]*Module{},
		UsePos:      map[string]ast.Pos{},
		UsedAliases: map[string]bool{},
		Types:       map[string]*TypeInfo{},
		Fns:         map[string]*FnInfo{},
		Consts:      map[string]*ConstInfo{},
		Behaviours:  map[string]*BehaviourInfo{},
	}
}

func moduleSrc(in *Input, path string) *ModuleSrc {
	for _, s := range in.Modules {
		if s.Path == path {
			return s
		}
	}
	return nil
}

func firstPos(src *ModuleSrc) ast.Pos {
	if src != nil && len(src.Files) > 0 {
		return ast.Pos{File: src.Files[0].Path, Line: 1, Col: 1}
	}
	return ast.Pos{}
}

// resolveUses fills m.Deps from the `use` lines of every file in the module.
func (c *Checker) resolveUses(m *Module, a *app.App) {
	p := c.prog
	for _, f := range m.Files {
		for _, u := range f.Uses {
			var target *Module
			alias := u.Path[len(u.Path)-1]
			full := strings.Join(u.Path, ".")
			switch {
			case len(u.Path) == 2 && u.Path[0] == "std":
				target = p.ByPath[full]
			case len(u.Path) == 1:
				if a != nil && a.DepFor(u.Path[0]) != nil {
					target = p.ByPath[u.Path[0]]
				} else {
					target = p.ByPath[u.Path[0]]
				}
			default:
				target = p.ByPath[full]
			}
			if target == nil || target.IsScenarios {
				d := c.bag.Errorf("E0102", u.Pos, "unknown module `%s`", full)
				d.Label("not a module of this app, a dependency, or `std`")
				continue
			}
			if u.Alias != "" {
				alias = u.Alias
			}
			if prev, ok := m.Deps[alias]; ok {
				if prev != target {
					d := c.bag.Errorf("E0102", u.Pos, "`%s` is already bound to `%s`", alias, prev.Path)
					d.Helpf("import one of them with `use %s as <name>`", full)
				}
				continue
			}
			m.Deps[alias] = target
			m.UseOrder = append(m.UseOrder, alias)
			m.UsePos[alias] = u.Pos
		}
	}
}

// orderModules returns the user modules dependencies-first and reports
// import cycles as E0103.
func (c *Checker) orderModules(users []*Module) []*Module {
	const (
		white = iota
		grey
		black
	)
	colour := map[*Module]int{}
	var out []*Module
	var stack []*Module
	reported := map[*Module]bool{}
	var visit func(m *Module)
	visit = func(m *Module) {
		switch colour[m] {
		case black:
			return
		case grey:
			if reported[m] {
				return
			}
			reported[m] = true
			var cyc []string
			start := 0
			for i, s := range stack {
				if s == m {
					start = i
					break
				}
			}
			for _, s := range stack[start:] {
				cyc = append(cyc, s.Path)
			}
			cyc = append(cyc, m.Path)
			pos := ast.Pos{}
			if len(stack) > 0 {
				last := stack[len(stack)-1]
				for alias, dep := range last.Deps {
					if dep == m {
						pos = last.UsePos[alias]
					}
				}
			}
			d := c.bag.Errorf("E0103", pos, "import cycle")
			d.Label("%s", strings.Join(cyc, " → "))
			d.Note("modules must form a directed acyclic graph")
			return
		}
		colour[m] = grey
		stack = append(stack, m)
		var deps []*Module
		if m.Parent != nil {
			deps = append(deps, m.Parent)
		}
		for _, alias := range m.UseOrder {
			deps = append(deps, m.Deps[alias])
		}
		for _, d := range deps {
			if d.IsStd {
				continue
			}
			visit(d)
		}
		stack = stack[:len(stack)-1]
		colour[m] = black
		out = append(out, m)
	}
	for _, m := range users {
		visit(m)
	}
	return out
}

// synthesizeAppCfg builds the `Config` and `Secrets` records from app.jip.
func (c *Checker) synthesizeAppCfg(in *Input) {
	p := c.prog
	cfgMod := &Module{Path: "appcfg", IsStd: true, Deps: map[string]*Module{}, Types: map[string]*TypeInfo{}, Fns: map[string]*FnInfo{}, Consts: map[string]*ConstInfo{}, Behaviours: map[string]*BehaviourInfo{}, UsePos: map[string]ast.Pos{}, UsedAliases: map[string]bool{}}
	p.AppCfg = cfgMod
	secrets := &TypeInfo{Name: "Secrets", Mod: "appcfg", Module: cfgMod, Kind: Record, Vis: ast.Pub, Doc: "The app's secrets."}
	config := &TypeInfo{Name: "Config", Mod: "appcfg", Module: cfgMod, Kind: Record, Vis: ast.Pub, Doc: "The app's configuration."}
	sc := &tscope{m: c.prelude}
	if in.App != nil {
		config.Pos = in.App.Pos
		secrets.Pos = in.App.Pos
		for _, cf := range in.App.Config {
			var t Type
			switch {
			case cf.Type != nil:
				t = c.resolveType(sc, cf.Type)
			case cf.Default != nil:
				t = litType(cf.Default)
				if t == nil {
					c.bag.Errorf("E0402", cf.Pos, "config field `%s` has a default that is not a literal", cf.Name)
					t = c.newVar()
				}
			default:
				t = c.newVar()
			}
			config.Fields = append(config.Fields, &FieldInfo{Name: cf.Name, Type: t, Pos: cf.Pos})
		}
		for _, s := range in.App.Secrets {
			secrets.Fields = append(secrets.Fields, &FieldInfo{Name: s.Name, Type: c.resolveType(sc, s.Type), Pos: s.Pos})
		}
	}
	config.Fields = append(config.Fields, &FieldInfo{Name: "secrets", Type: con(secrets), Pos: config.Pos})
	fake := &FnInfo{
		Name:    "fake",
		Mod:     "appcfg",
		Module:  cfgMod,
		Type:    &TFn{Ret: con(secrets)},
		Vis:     ast.Pub,
		Doc:     "A Secrets value for scenarios.",
		Extern:  &ast.ExternRef{Pkg: "appcfg", Ident: "SecretsFake"},
		Builtin: "fake",
	}
	impl := &ImplInfo{Module: cfgMod, Target: con(secrets), TargetInfo: secrets, Assoc: map[string]Type{}, Consts: map[string]*ConstInfo{}, Methods: map[string]*FnInfo{"fake": fake}, MethodOrder: []string{"fake"}, Name: "Secrets"}
	fake.Impl = impl
	secrets.Impls = append(secrets.Impls, impl)
	cfgMod.Impls = append(cfgMod.Impls, impl)
	cfgMod.Types["Config"], cfgMod.Types["Secrets"] = config, secrets
	cfgMod.Order = []string{"Config", "Secrets"}
	for _, t := range []*TypeInfo{config, secrets} {
		if prev, ok := c.prelude.Types[t.Name]; ok {
			c.bag.Errorf("E0406", prev.Pos, "duplicate definition of `%s`", t.Name)
		}
		c.prelude.Types[t.Name] = t
	}
	p.Config, p.Secrets = config, secrets
}

func litType(e ast.Expr) Type {
	switch e.(type) {
	case *ast.StrLit:
		return con(tStr)
	case *ast.IntLit:
		return con(tInt)
	case *ast.FloatLit:
		return con(tFloat)
	case *ast.BoolLit:
		return con(tBool)
	case *ast.DurationLit:
		return con(tDuration)
	case *ast.BytesLit:
		return con(tBytes)
	case *ast.CharLit:
		return con(tChar)
	}
	return nil
}

// declareTopics resolves `topics { name: Topic<T> }` into Topic<T> types.
func (c *Checker) declareTopics(in *Input) {
	if in.App == nil {
		return
	}
	topicInfo := c.prelude.Types["Topic"]
	for _, t := range in.App.Topics {
		p := c.prog
		if _, dup := p.Topics[t.Name]; dup {
			c.bag.Errorf("E0406", t.Pos, "duplicate topic `%s`", t.Name)
			continue
		}
		p.Topics[t.Name] = t
		var msg Type
		tp, ok := t.Type.(*ast.TypePath)
		if !ok {
			msg = c.resolveType(&tscope{m: c.prelude}, t.Type)
		} else if len(tp.Path) > 1 {
			modPath := strings.Join(tp.Path[:len(tp.Path)-1], ".")
			m := p.ByPath[modPath]
			if m == nil {
				c.bag.Errorf("E0102", t.Pos, "unknown module `%s`", modPath)
				continue
			}
			msg = c.resolveType(&tscope{m: m}, &ast.TypePath{Pos: tp.Pos, Path: tp.Path[len(tp.Path)-1:], Args: tp.Args})
		} else {
			var found []*TypeInfo
			for _, m := range p.Modules {
				if m.IsStd || m.IsScenarios {
					continue
				}
				if ti, ok := m.Types[tp.Path[0]]; ok && ti.Vis == ast.Pub {
					found = append(found, ti)
				}
			}
			switch len(found) {
			case 1:
				msg = c.resolveType(&tscope{m: found[0].Module}, tp)
			case 0:
				msg = c.resolveType(&tscope{m: c.prelude}, tp)
			default:
				var names []string
				for _, f := range found {
					names = append(names, f.Mod+"."+f.Name)
				}
				sort.Strings(names)
				d := c.bag.Errorf("E0404", t.Pos, "ambiguous topic type `%s`", tp.Path[0])
				d.Helpf("qualify it: %s", strings.Join(names, " or "))
				continue
			}
		}
		if topicInfo != nil {
			p.TopicType[t.Name] = con(topicInfo, msg)
		}
	}
}

// checkModule checks every body in a module.
func (c *Checker) checkModule(m *Module) {
	c.mod = m
	curMod = m
	for _, name := range m.Order {
		if ci, ok := m.Consts[name]; ok && ci.Impl == nil {
			c.checkConst(ci)
		}
	}
	for _, name := range m.Order {
		if ti, ok := m.Types[name]; ok && len(ti.Inv) > 0 && (ti.Kind == Record || ti.Kind == Newtype) {
			c.checkInv(ti, ti.Inv, m, false)
		}
	}
	for _, name := range m.Order {
		if fi, ok := m.Fns[name]; ok {
			c.checkFn(fi)
		}
	}
	for _, impl := range m.Impls {
		c.checkImpl(impl)
		if impl.Decl != nil {
			for _, k := range impl.Decl.Consts {
				if ci := impl.Consts[k.Name]; ci != nil {
					c.checkConst(ci)
				}
			}
		}
	}
	for _, s := range m.Scenarios {
		c.checkScenario(s)
	}
}

// postChecks runs whole-program checks: E0707 and unused imports.
func (c *Checker) postChecks() {
	reach := map[*FnInfo]bool{}
	var walk func(f *FnInfo)
	walk = func(f *FnInfo) {
		if reach[f] {
			return
		}
		reach[f] = true
		for _, g := range f.Calls {
			walk(g)
		}
	}
	for _, r := range c.workflowRuns {
		walk(r)
	}
	var offenders []*FnInfo
	for f, pos := range c.approvePos {
		if !reach[f] {
			_ = pos
			offenders = append(offenders, f)
		}
	}
	sort.Slice(offenders, func(i, j int) bool { return posLess(c.approvePos[offenders[i]], c.approvePos[offenders[j]]) })
	for _, f := range offenders {
		curMod = f.Module
		d := c.bag.Errorf("E0707", c.approvePos[f], "`approve` is reachable only from `Workflow.run`")
		d.Label("no `Workflow.run` calls `%s`", f.Name)
		d.Note("an approval suspends a journaled run; a plain function has no journal")
		d.Helpf("call `%s` from a `Workflow.run`, or move the `approve` into one", f.Name)
	}
	for _, m := range c.prog.Modules {
		if m.IsStd {
			continue
		}
		curMod = m
		for _, alias := range m.UseOrder {
			if m.UsedAliases[alias] || (m.Parent != nil && m.Deps[alias] == m.Parent) {
				continue
			}
			d := c.bag.Warnf("W0102", m.UsePos[alias], "unused import `%s`", m.Deps[alias].Path)
			d.Label("imported, nothing referenced")
			d.Helpf("remove the `use %s` line", m.Deps[alias].Path)
		}
	}
}

func posLess(a, b ast.Pos) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

// errorf is a shorthand for bag.Errorf with the current module set for printing.
func (c *Checker) errorf(code string, pos ast.Pos, format string, args ...any) *diag.Diagnostic {
	return c.bag.Errorf(code, pos, format, args...)
}

func (c *Checker) warnf(code string, pos ast.Pos, format string, args ...any) *diag.Diagnostic {
	return c.bag.Warnf(code, pos, format, args...)
}

var _ = fmt.Sprintf
