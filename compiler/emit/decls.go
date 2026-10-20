package emit

import (
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
)

// emitModule writes one module's package: module.go with every type,
// function, const and impl, plus scenarios_test.go when the module has a
// scenario module and derived_test.go when a `pub fn` gets a derived check.
func (e *emitter) emitModule(p *pkg) {
	m := p.mod
	f := p.newFile("module.go", false)
	for _, name := range m.Order {
		if ti, ok := m.Types[name]; ok {
			f.emitType(ti)
			continue
		}
		if fi, ok := m.Fns[name]; ok {
			f.emitFn(fi)
			continue
		}
		if ci, ok := m.Consts[name]; ok {
			f.emitConst(ci)
		}
	}
	for _, impl := range m.Impls {
		f.emitImpl(impl)
	}
	for _, sm := range e.prog.Modules {
		if sm.IsScenarios && sm.Parent == m {
			e.emitScenarios(p, sm)
		}
	}
	e.emitDerived(p)
}

func (f *file) doc(d string) {
	for _, l := range strings.Split(strings.TrimSpace(d), "\n") {
		if l != "" {
			f.printf("// %s\n", strings.TrimSpace(l))
		}
	}
}

// ---- types ----------------------------------------------------------------

func (f *file) emitType(ti *checker.TypeInfo) {
	name := f.p.e.typeNames[ti]
	tps := f.typeParamList(ti.Params)
	f.printf("\n")
	f.doc(ti.Doc)
	switch ti.Kind {
	case checker.Record:
		f.printf("type %s%s struct {\n", name, tps)
		for _, fl := range ti.Fields {
			f.printf("\t%s %s `json:\"%s\"`\n", fieldName(fl.Name), f.goType(fl.Type), fl.Name)
		}
		f.printf("}\n")
		if len(ti.Inv) > 0 {
			f.emitInvCheck(ti, name)
		}
	case checker.Newtype:
		f.printf("type %s%s %s\n", name, tps, f.goType(ti.Inner))
		if len(ti.Inv) > 0 {
			f.emitInvCheck(ti, name)
		}
	case checker.Enum:
		f.emitEnum(ti, name, tps)
	case checker.Extern:
		// A user extern type is the Go type itself.
		f.printf("type %s = %s.%s\n", name, f.goImport(ti.GoPkg), ti.GoIdent)
	case checker.Alias:
		// Expanded by the checker; nothing to emit.
	}
}

// variantField is the Go field of a variant payload: `<V>_<i>` or
// `<V>_<Field>`.
func variantField(v *checker.VariantInfo, i int) string {
	if len(v.Named) > 0 {
		return v.Name + "_" + fieldName(v.Named[i].Name)
	}
	return v.Name + "_" + strconv.Itoa(i)
}

func (f *file) emitEnum(ti *checker.TypeInfo, name, tps string) {
	use := typeParamUse(ti.Params)
	f.printf("type %s%s struct {\n\tTag uint8\n", name, tps)
	for _, v := range ti.Variants {
		for i, t := range v.Positional {
			f.printf("\t%s %s\n", variantField(v, i), f.goType(t))
		}
		for i, fl := range v.Named {
			f.printf("\t%s %s `json:\"%s\"`\n", variantField(v, i), f.goType(fl.Type), fl.Name)
		}
	}
	f.printf("}\n\nconst (\n")
	for i, v := range ti.Variants {
		if i == 0 {
			f.printf("\t%sTag_%s uint8 = iota\n", name, v.Name)
		} else {
			f.printf("\t%sTag_%s\n", name, v.Name)
		}
	}
	f.printf(")\n\n")
	var names []string
	for _, v := range ti.Variants {
		names = append(names, strconv.Quote(v.Name))
		var params, inits []string
		n := len(v.Positional) + len(v.Named)
		for i := 0; i < n; i++ {
			var t checker.Type
			if len(v.Named) > 0 {
				t = v.Named[i].Type
			} else {
				t = v.Positional[i]
			}
			params = append(params, "a"+strconv.Itoa(i)+" "+f.goType(t))
			inits = append(inits, ", "+variantField(v, i)+": a"+strconv.Itoa(i))
		}
		f.printf("func %s_%s%s(%s) %s%s { return %s%s{Tag: %sTag_%s%s} }\n",
			name, v.Name, tps, strings.Join(params, ", "), name, use, name, use, name, v.Name, strings.Join(inits, ""))
	}
	f.printf("func (%s%s) JipVariants() []string { return []string{%s} }\n", name, use, strings.Join(names, ", "))
	rtp := f.rt()
	f.printf("func (v %s%s) MarshalJSON() ([]byte, error) { return %s.MarshalEnum(v) }\n", name, use, rtp)
	f.printf("func (v *%s%s) UnmarshalJSON(b []byte) error { return %s.UnmarshalEnum(v, b) }\n", name, use, rtp)
}

// emitInvCheck writes `<Type>_inv(ctx, v) T`, the construction check of a
// record or newtype invariant (spec 05), and `<Type>_invOK(ctx, v) bool`,
// the predicate generators redraw on.
func (f *file) emitInvCheck(ti *checker.TypeInfo, name string) {
	fn := f.p.e.invName(ti)
	use := typeParamUse(ti.Params)
	tps := f.typeParamList(ti.Params)
	rtp := f.rt()
	f.printf("\nfunc %sOK%s(ctx *%s.Ctx, self %s%s) bool {\n", fn, tps, rtp, name, use)
	g := newGen(f, nil)
	g.selfName = "self"
	g.bindFields(ti)
	for _, inv := range ti.Inv {
		stmts, v, _ := g.capture(func() string { return g.expr(inv) })
		g.printf("%sif !(%s) {\nreturn false\n}\n", stmts, v)
	}
	f.body.WriteString(g.w.String())
	f.printf("\treturn true\n}\n")
	f.printf("\nfunc %s%s(ctx *%s.Ctx, self %s%s) %s%s {\n", fn, tps, rtp, name, use, name, use)
	g = newGen(f, nil)
	g.selfName = "self"
	g.bindFields(ti)
	for _, inv := range ti.Inv {
		g.contract("inv", inv)
	}
	f.body.WriteString(g.w.String())
	f.printf("\treturn self\n}\n")
}

// bindFields puts every field of a record in scope as a local, as the
// checker does for `inv` (and for a newtype, nothing: `self.0` is used).
func (g *gen) bindFields(ti *checker.TypeInfo) {
	for _, fl := range ti.Fields {
		n := localName(fl.Name)
		g.printf("%s := self.%s\n_ = %s\n", n, fieldName(fl.Name), n)
	}
}

func paramTypes(ps []*checker.TParam) []checker.Type {
	out := make([]checker.Type, len(ps))
	for i, p := range ps {
		out[i] = p
	}
	return out
}

// invName is the Go name of a type's invariant check.
func (e *emitter) invName(ti *checker.TypeInfo) string {
	if n, ok := e.invNames[ti]; ok {
		return n
	}
	p := e.pkgs[ti.Module]
	n := p.alloc(e.typeNames[ti] + "_inv")
	p.names[n+"OK"] = true
	e.invNames[ti] = n
	return n
}

// invRef is the qualified reference to a type's invariant check, or "".
func (f *file) invRef(ti *checker.TypeInfo) string {
	if len(ti.Inv) == 0 || ti.Module == nil || ti.Module.IsStd {
		return ""
	}
	return f.ref(f.p.e.pkgs[ti.Module], f.p.e.invName(ti))
}

// goImport records an import of a Go package for an extern and returns
// its alias.
func (f *file) goImport(path string) string {
	if path == "rt" {
		return f.rt()
	}
	return f.use("x_"+sanitize(strings.ReplaceAll(path, "/", "_")), path)
}

// ---- functions ------------------------------------------------------------

// signature is `Name[T any](ctx *rt.Ctx, a A, …) R` for fi.
func (f *file) signature(fi *checker.FnInfo, name string) string {
	var b strings.Builder
	b.WriteString(name + f.typeParamList(fi.TypeParams) + "(ctx *" + f.rt() + ".Ctx")
	for i, p := range fi.Decl.Params {
		b.WriteString(", " + paramGoName(f.p.e.prog, &fi.Decl.Params[i]) + " " + f.goType(fi.Type.Params[i]))
		_ = p
	}
	b.WriteString(") " + f.goType(fi.Type.Ret))
	return b.String()
}

// paramGoName is the Go name of a declared parameter.
func paramGoName(prog *checker.Program, p *ast.Param) string {
	if l := prog.Params[p]; l != nil && l.Go != "" {
		return localName(l.Go)
	}
	if p.Self {
		return "self"
	}
	return localName(p.Name)
}

func (f *file) emitFn(fi *checker.FnInfo) {
	name := f.p.e.fnNames[fi]
	f.printf("\n")
	f.doc(fi.Doc)
	f.line(fi.Pos)
	f.printf("func %s {\n", f.signature(fi, name))
	if fi.Extern != nil {
		f.emitExternBody(fi)
	} else {
		g := newGen(f, fi)
		g.fnBody(fi.Decl, fi.Type.Ret)
		f.body.WriteString(g.w.String())
	}
	f.printf("}\n")
}

// emitExternBody forwards to the Go function through rt.Extern, which
// converts arguments and the `(T, error)` result (spec 11).
func (f *file) emitExternBody(fi *checker.FnInfo) {
	var args []string
	for i := range fi.Decl.Params {
		args = append(args, paramGoName(f.p.e.prog, &fi.Decl.Params[i]))
	}
	target := f.goImport(fi.Extern.Pkg) + "." + fi.Extern.Ident
	if fi.Extern.Pkg == "rt" {
		// A dependency extern into rt returning Result<R, E> instantiates
		// rt's generic R and E (rt.HttpPost, rt.HttpGet).
		if tc, ok := checker.Zonk(fi.Type.Ret).(*checker.TCon); ok && tc.Info.Mod == "prelude" && tc.Info.Name == "Result" && len(fi.TypeParams) == 0 {
			target += f.typeArgs(tc.Args)
		}
		f.printf("\treturn %s(ctx", target)
		for _, a := range args {
			f.printf(", %s", a)
		}
		f.printf(")\n")
		return
	}
	f.emitExternCall(fi)
}

func (f *file) emitConst(ci *checker.ConstInfo) {
	name := f.p.e.constNames[ci]
	f.printf("\n")
	f.doc(ci.Doc)
	f.printf("func %s(ctx *%s.Ctx) %s {\n", name, f.rt(), f.goType(ci.Type))
	g := newGen(f, nil)
	if ci.Impl != nil {
		g.implParams(ci.Impl)
	}
	v := g.expr(ci.Decl.Value)
	f.body.WriteString(g.w.String())
	f.printf("\treturn %s\n}\n", v)
}

// ---- impls ----------------------------------------------------------------

func (f *file) emitImpl(impl *checker.ImplInfo) {
	for _, n := range impl.MethodOrder {
		fi := impl.Methods[n]
		if fi == nil || (impl.Behaviour != nil && !hasMethod(impl, n)) {
			continue
		}
		f.emitFn(fi)
	}
	for _, n := range sortedConstNames(impl) {
		f.emitConst(impl.Consts[n])
	}
	if impl.Behaviour != nil && isV0Behaviour(impl.Behaviour.Name) {
		f.emitSpec(impl)
	}
}
