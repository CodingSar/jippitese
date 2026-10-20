package emit

import (
	"go/importer"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/diag"
)

// ---- the boundary mapping table (spec 11 rule 3) ----------------------------

var identityPrims = map[string]string{
	"Int": "int64", "Float": "float64", "Bool": "bool", "Str": "string",
	"Bytes": "[]byte", "Duration": "time.Duration",
}

// extType is the Go type a mappable jippitese value type crosses the
// boundary as, or "" when t is outside the table.
func extType(t checker.Type) string {
	tc, ok := checker.Zonk(t).(*checker.TCon)
	if !ok {
		return ""
	}
	switch tc.Info.Kind {
	case checker.Prim:
		return identityPrims[tc.Info.Name]
	case checker.Newtype:
		return extType(tc.Info.Inner)
	case checker.Extern:
		if tc.Info.Mod != "prelude" {
			return ""
		}
		switch tc.Info.Name {
		case "Path":
			return "string"
		case "List":
			if el := extType(tc.Args[0]); el != "" {
				return "[]" + el
			}
		case "Map":
			k, v := extType(tc.Args[0]), extType(tc.Args[1])
			if k != "" && v != "" && identity(tc.Args[0]) && identity(tc.Args[1]) && k != "[]byte" {
				return "map[" + k + "]" + v
			}
		}
	}
	return ""
}

// identity says whether t has the same Go representation inside and
// outside the boundary.
func identity(t checker.Type) bool {
	tc, ok := checker.Zonk(t).(*checker.TCon)
	if !ok {
		return false
	}
	if tc.Info.Kind == checker.Prim {
		return identityPrims[tc.Info.Name] != ""
	}
	if tc.Info.Kind == checker.Extern && tc.Info.Mod == "prelude" && tc.Info.Name == "List" {
		return identity(tc.Args[0])
	}
	return false
}

// externErrors are the error types an extern may return in a Result.
var externErrors = map[string]string{"GoError": "GoErrorOf", "IoError": "IoErrorOf", "NetError": "NetErrorOf"}

// extResult classifies an extern's return type: the Go results it maps to
// and how to rebuild the jippitese value.
type extResult struct {
	kind  string // "unit", "value", "option", "result", "result_unit"
	value checker.Type
	err   string // rt helper for the error
	gores []string
}

func classifyResult(t checker.Type) (extResult, bool) {
	t = checker.Zonk(t)
	if checker.IsUnit(t) {
		return extResult{kind: "unit"}, true
	}
	tc, ok := t.(*checker.TCon)
	if ok && tc.Info.Mod == "prelude" && tc.Info.Kind == checker.Enum {
		switch tc.Info.Name {
		case "Option":
			if x := extType(tc.Args[0]); x != "" {
				return extResult{kind: "option", value: tc.Args[0], gores: []string{x, "bool"}}, true
			}
			return extResult{}, false
		case "Result":
			et, eok := checker.Zonk(tc.Args[1]).(*checker.TCon)
			if !eok || et.Info.Mod != "prelude" || externErrors[et.Info.Name] == "" {
				return extResult{}, false
			}
			r := extResult{err: externErrors[et.Info.Name], value: tc.Args[0]}
			if checker.IsUnit(tc.Args[0]) {
				r.kind, r.gores = "result_unit", []string{"error"}
				return r, true
			}
			if x := extType(tc.Args[0]); x != "" {
				r.kind, r.gores = "result", []string{x, "error"}
				return r, true
			}
			return extResult{}, false
		}
	}
	if x := extType(t); x != "" {
		return extResult{kind: "value", value: t, gores: []string{x}}, true
	}
	return extResult{}, false
}

// ---- conversions --------------------------------------------------------------

// toGo converts the jippitese value v of type t to its boundary Go type.
func (f *file) toGo(t checker.Type, v string) string {
	if identity(t) {
		return v
	}
	tc := checker.Zonk(t).(*checker.TCon)
	switch tc.Info.Kind {
	case checker.Newtype:
		return f.toGo(tc.Info.Inner, f.goType(tc.Info.Inner)+"("+v+")")
	case checker.Extern:
		switch tc.Info.Name {
		case "Path":
			return "string(" + v + ")"
		case "List":
			el := tc.Args[0]
			return "func() " + extType(t) + " {\nout := make(" + extType(t) + ", len(" + v + "))\nfor i, x := range " + v + " {\nout[i] = " + f.toGo(el, "x") + "\n}\nreturn out\n}()"
		case "Map":
			return f.rt() + ".MapToGo(" + v + ")"
		}
	}
	f.p.e.failf("no boundary conversion for `%s`", checker.TypeString(t))
	return ""
}

// fromGo converts the Go value v back to the jippitese type t.
func (f *file) fromGo(t checker.Type, v string) string {
	if identity(t) {
		return v
	}
	tc := checker.Zonk(t).(*checker.TCon)
	switch tc.Info.Kind {
	case checker.Newtype:
		return f.goType(t) + "(" + f.fromGo(tc.Info.Inner, v) + ")"
	case checker.Extern:
		switch tc.Info.Name {
		case "Path":
			return f.goType(t) + "(" + v + ")"
		case "List":
			el := tc.Args[0]
			gt := f.goType(t)
			return "func() " + gt + " {\nout := make(" + gt + ", len(" + v + "))\nfor i, x := range " + v + " {\nout[i] = " + f.fromGo(el, "x") + "\n}\nreturn out\n}()"
		case "Map":
			return f.rt() + ".MapFromGo(" + v + ")"
		}
	}
	f.p.e.failf("no boundary conversion for `%s`", checker.TypeString(t))
	return ""
}

// emitExternCall writes the body of a non-rt extern: a direct call of the
// Go function with inline conversions (spec 11 rule 9).
func (f *file) emitExternCall(fi *checker.FnInfo) {
	var args []string
	for i := range fi.Decl.Params {
		args = append(args, f.toGo(fi.Type.Params[i], paramGoName(f.p.e.prog, &fi.Decl.Params[i])))
	}
	call := f.goImport(fi.Extern.Pkg) + "." + fi.Extern.Ident + "(" + strings.Join(args, ", ") + ")"
	if typ, meth, ok := strings.Cut(fi.Extern.Ident, "."); ok {
		// A method: the first parameter converts to the receiver type and
		// is held in a variable so pointer-receiver methods apply too.
		// localName never yields an `m_` name.
		f.printf("\tm_recv := %s.%s(%s)\n", f.goImport(fi.Extern.Pkg), typ, args[0])
		call = "m_recv." + meth + "(" + strings.Join(args[1:], ", ") + ")"
	}
	r, ok := classifyResult(fi.Type.Ret)
	if !ok {
		f.p.e.failf("extern `%s` has an unmappable result", fi.Name)
	}
	rtp := f.rt()
	ret := checker.Zonk(fi.Type.Ret)
	switch r.kind {
	case "unit":
		f.printf("\t%s\n\treturn %s.U\n", call, rtp)
	case "value":
		f.printf("\treturn %s\n", f.fromGo(r.value, call))
	case "option":
		vt := f.goType(r.value)
		f.printf("\tv, ok := %s\n\tif !ok {\n\t\treturn %s.Option_None[%s]()\n\t}\n\treturn %s.Option_Some(%s)\n", call, rtp, vt, rtp, f.fromGo(r.value, "v"))
	case "result", "result_unit":
		rc := ret.(*checker.TCon)
		tv, te := f.goType(rc.Args[0]), f.goType(rc.Args[1])
		if r.kind == "result" {
			f.printf("\tv, err := %s\n", call)
		} else {
			f.printf("\terr := %s\n", call)
		}
		f.printf("\tif err != nil {\n\t\treturn %s.Result_Err[%s, %s](%s.%s(err))\n\t}\n", rtp, tv, te, rtp, r.err)
		if r.kind == "result" {
			f.printf("\treturn %s.Result_Ok[%s, %s](%s)\n", rtp, tv, te, f.fromGo(r.value, "v"))
		} else {
			f.printf("\treturn %s.Result_Ok[%s, %s](%s.U)\n", rtp, tv, te, rtp)
		}
	}
}

// ---- checks (spec 11 rules 1–7) ---------------------------------------------

// effectFloor is the floor table of spec 11 rule 5, by Go package.
func effectFloor(path string) []string {
	switch {
	case path == "os/exec":
		return []string{"io"}
	case path == "os" || path == "io" || path == "bufio" || path == "path/filepath" || path == "io/fs":
		return []string{"io"}
	case path == "net" || strings.HasPrefix(path, "net/"):
		return []string{"net"}
	case path == "time" || path == "math/rand" || path == "math/rand/v2" || path == "crypto/rand":
		return []string{"nondet"}
	case path == "sync" || path == "sync/atomic":
		return []string{"mut"}
	}
	return nil
}

// CheckExterns applies spec 11 to every app and vendored extern, reporting
// E0501–E0508 into bag. Externs whose target is the compiler runtime
// ("rt") belong to the toolchain and are exempt.
func CheckExterns(prog *checker.Program, bag *diag.Bag) {
	imp := importer.ForCompiler(token.NewFileSet(), "source", nil)
	pkgs := map[string]*types.Package{}
	maxN, count := 0, 0
	allowList := "[]"
	if prog.App != nil {
		maxN = prog.App.Externs.Max
		allowList = "[" + strings.Join(prog.App.Externs.Allow, ", ") + "]"
	}
	for _, m := range prog.Modules {
		if m.IsStd || m.IsScenarios {
			continue
		}
		for _, fi := range m.Externs {
			if fi.Extern == nil || fi.Extern.Pkg == "rt" {
				continue
			}
			count++
			ref := `"` + fi.Extern.Pkg + `".` + fi.Extern.Ident
			if !m.IsDep && (prog.App == nil || !prog.App.ExternAllowed(m.Path)) {
				dg := bag.Errorf("E0502", fi.Pos, "extern in a module that is not allow-listed")
				dg.Label("`%s` is not in `app.jip` externs.allow", m.Path)
				dg.Note("externs.allow = %s, %d of max %d used", allowList, count, maxN)
				dg.Helpf("add `%s` to `externs { allow: [...] }` in app.jip", m.Path)
			} else if count > maxN {
				dg := bag.Errorf("E0502", fi.Pos, "too many externs")
				dg.Label("extern %d of max %d", count, maxN)
				dg.Helpf("raise `externs { max: … }` in app.jip")
			}
			first := strings.SplitN(fi.Extern.Pkg, "/", 2)[0]
			if strings.Contains(first, ".") {
				dg := bag.Errorf("E0504", fi.Pos, "extern import path is not pinned")
				dg.Label("`%s` is not a Go standard-library path", fi.Extern.Pkg)
				dg.Helpf("add a `gomod(...)` entry for its module to `deps {}`")
				continue
			}
			typ, meth, isMethod := strings.Cut(fi.Extern.Ident, ".")
			params := fi.Type.Params
			if isMethod {
				if len(params) == 0 {
					dg := bag.Errorf("E0503", fi.Pos, "method extern without a receiver")
					dg.Label("`%s` is a method; its receiver is the first parameter", ref)
					continue
				}
				params = params[1:]
			}
			want, ok := wantSignature(params, fi.Type.Ret)
			if ok && isMethod {
				ok = extType(fi.Type.Params[0]) != ""
			}
			if !ok {
				dg := bag.Errorf("E0505", fi.Pos, "extern type outside the boundary table")
				dg.Label("only Int, Float, Bool, Str, Bytes, Duration, newtypes over them, List, Map, Option and Result<_, GoError> cross")
				continue
			}
			p, cached := pkgs[fi.Extern.Pkg]
			if !cached {
				var err error
				p, err = imp.Import(fi.Extern.Pkg)
				if err != nil {
					bag.Errorf("E0504", fi.Pos, "cannot load Go package `%s`", fi.Extern.Pkg).Note("%v", err)
					continue
				}
				pkgs[fi.Extern.Pkg] = p
			}
			for _, ip := range p.Imports() {
				if ip.Path() == "context" {
					dg := bag.Errorf("E0508", fi.Pos, "extern package imports `context`")
					dg.Label("`%s` imports `context`; externs receive no context and are never interrupted", fi.Extern.Pkg)
					break
				}
			}
			var fn *types.Func
			if isMethod {
				if fn = lookupMethod(p, typ, meth, fi, ref, bag); fn == nil {
					continue
				}
			} else {
				obj := p.Scope().Lookup(fi.Extern.Ident)
				isFn := false
				if fn, isFn = obj.(*types.Func); !isFn {
					bag.Errorf("E0506", fi.Pos, "`%s` is not a function", ref)
					continue
				}
			}
			got := sigString(fn.Type().(*types.Signature))
			if got != want {
				dg := bag.Errorf("E0506", fi.Pos, "extern signature does not match Go")
				dg.Note("declared maps to `func%s`", want)
				dg.Note("`%s` is `func%s`", ref, got)
			}
			checkFloor(fi, ref, bag)
			checkExternScenario(prog, m, fi, bag)
		}
	}
}

// lookupMethod finds the exported method meth of the named type typ in p
// and checks that the extern's first parameter converts to typ (E0503).
func lookupMethod(p *types.Package, typ, meth string, fi *checker.FnInfo, ref string, bag *diag.Bag) *types.Func {
	tn, ok := p.Scope().Lookup(typ).(*types.TypeName)
	if !ok || !tn.Exported() || tn.IsAlias() {
		bag.Errorf("E0503", fi.Pos, "`%s` is not an exported named type of `%s`", typ, fi.Extern.Pkg)
		return nil
	}
	named, ok := tn.Type().(*types.Named)
	if !ok || named.TypeParams().Len() > 0 {
		bag.Errorf("E0503", fi.Pos, "`%s.%s` is not a non-generic named type", fi.Extern.Pkg, typ)
		return nil
	}
	obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(named), true, p, meth)
	fn, ok := obj.(*types.Func)
	if !ok || !fn.Exported() {
		bag.Errorf("E0506", fi.Pos, "`%s` is not an exported method", ref)
		return nil
	}
	q := func(p *types.Package) string { return p.Name() }
	recv := extType(fi.Type.Params[0])
	if recv != types.TypeString(named, q) && recv != types.TypeString(named.Underlying(), q) {
		dg := bag.Errorf("E0503", fi.Pos, "extern receiver does not match Go")
		dg.Label("the first parameter crosses as `%s`", recv)
		u := types.TypeString(named.Underlying(), q)
		if _, isStruct := named.Underlying().(*types.Struct); isStruct {
			u = "struct{…}"
		}
		dg.Note("`%s.%s` is `%s`", fi.Extern.Pkg, typ, u)
		dg.Helpf("bind a function, or a method of a type over the parameter's Go type")
		return nil
	}
	return fn
}

// wantSignature is the Go signature parameters and result map to, as
// `(int64, string) ([]byte, error)`.
func wantSignature(params []checker.Type, ret checker.Type) (string, bool) {
	var ps []string
	for _, t := range params {
		x := extType(t)
		if x == "" {
			return "", false
		}
		ps = append(ps, x)
	}
	r, ok := classifyResult(ret)
	if !ok {
		return "", false
	}
	return "(" + strings.Join(ps, ", ") + ")" + resultString(r.gores), true
}

func resultString(rs []string) string {
	switch len(rs) {
	case 0:
		return ""
	case 1:
		return " " + rs[0]
	}
	return " (" + strings.Join(rs, ", ") + ")"
}

// sigString prints a Go signature in the same form as wantSignature.
func sigString(s *types.Signature) string {
	q := func(p *types.Package) string { return p.Name() }
	var ps, rs []string
	for i := 0; i < s.Params().Len(); i++ {
		t := types.TypeString(s.Params().At(i).Type(), q)
		if s.Variadic() && i == s.Params().Len()-1 {
			t = "..." + strings.TrimPrefix(t, "[]")
		}
		ps = append(ps, t)
	}
	for i := 0; i < s.Results().Len(); i++ {
		rs = append(rs, types.TypeString(s.Results().At(i).Type(), q))
	}
	return "(" + strings.Join(ps, ", ") + ")" + resultString(rs)
}

// checkFloor is rule 5 and rule 6: the declared effects cover the floor of
// the target package, and `unsafe pure` only where there is no floor.
func checkFloor(fi *checker.FnInfo, ref string, bag *diag.Bag) {
	floor := effectFloor(fi.Extern.Pkg)
	have := map[string]bool{}
	for _, ef := range fi.Type.Effects {
		have[ef.Name] = true
	}
	var missing []string
	for _, ef := range floor {
		if !have[ef] {
			missing = append(missing, ef)
		}
	}
	switch {
	case len(missing) > 0:
		dg := bag.Errorf("E0501", fi.Pos, "extern below the effect floor")
		if len(fi.Type.Effects) == 0 {
			dg.Label("declared pure")
		} else {
			dg.Label("declared with {%s}", effectNames(fi.Type.Effects))
		}
		dg.Note("`%s` needs `%s` (floor table: package `%s`)", ref, strings.Join(missing, "`, `"), fi.Extern.Pkg)
		dg.Helpf("add `%s` to the extern's `with { … }`", strings.Join(missing, ", "))
	case fi.Unsafe && floor != nil:
		dg := bag.Errorf("E0501", fi.Pos, "`unsafe pure` on a package with an effect floor")
		dg.Note("package `%s` has floor {%s}", fi.Extern.Pkg, strings.Join(floor, ", "))
	case floor == nil && len(fi.Type.Effects) == 0 && !fi.Unsafe:
		dg := bag.Errorf("E0501", fi.Pos, "pure extern over a package outside the floor table")
		dg.Helpf("prefix it `unsafe pure` with a `/// why:` line, or declare its effects")
	}
}

func effectNames(effs []ast.Effect) string {
	var ns []string
	for _, e := range effs {
		ns = append(ns, e.Name)
	}
	sort.Strings(ns)
	return strings.Join(ns, ", ")
}

// checkExternScenario is rule 7: some `@extern` scenario of the module
// calls the extern.
func checkExternScenario(prog *checker.Program, m *checker.Module, fi *checker.FnInfo, bag *diag.Bag) {
	for _, sm := range prog.Modules {
		if !sm.IsScenarios || sm.Parent != m {
			continue
		}
		for _, si := range sm.Scenarios {
			tagged := false
			for _, tg := range si.Decl.Tags {
				if tg.Name == "extern" {
					tagged = true
				}
			}
			if tagged && scenarioCalls(prog, si.Decl, fi) {
				return
			}
		}
	}
	dg := bag.Errorf("E0507", fi.Pos, "extern has no @extern scenario")
	dg.Label("`%s` is never called from a scenario tagged `@extern`", fi.Name)
	dg.Helpf("add `@extern scenario \"…\" { … }` calling `%s` to the module's scenarios.jip", fi.Name)
}

func scenarioCalls(prog *checker.Program, sc *ast.Scenario, fi *checker.FnInfo) bool {
	found := false
	visit := func(x ast.Expr) {
		if c, ok := x.(*ast.CallExpr); ok {
			if info := prog.Calls[c]; info != nil && info.Fn == fi {
				found = true
			}
		}
	}
	for _, gv := range sc.Givens {
		ast.WalkExpr(gv.Expr, visit)
		ast.WalkExpr(gv.Rows, visit)
	}
	for _, w := range sc.Whens {
		ast.WalkExpr(w.Expr, visit)
	}
	for _, th := range sc.Thens {
		ast.WalkExpr(th, visit)
	}
	return found
}
