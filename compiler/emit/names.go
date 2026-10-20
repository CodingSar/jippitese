package emit

import (
	"strings"

	"github.com/codingsar/jippitese/compiler/checker"
)

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

var goPredeclared = map[string]bool{
	"any": true, "bool": true, "byte": true, "cap": true, "close": true, "complex": true,
	"error": true, "false": true, "float32": true, "float64": true, "imag": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true, "iota": true,
	"len": true, "make": true, "new": true, "nil": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true, "rune": true, "string": true,
	"true": true, "uint": true, "uint8": true, "uint16": true, "uint32": true,
	"uint64": true, "uintptr": true, "append": true, "copy": true, "delete": true,
	"comparable": true, "clear": true, "min": true, "max": true,
}

// reservedAlias are the import aliases every generated file may use.
var reservedAlias = map[string]bool{
	"rt": true, "std": true, "appcfg": true, "time": true, "cmp": true, "os": true,
	"testing": true, "fmt": true, "main": true, "ctx": true, "scn": true, "jip_ctx": true,
}

func reservedName(n string) bool {
	return goKeywords[n] || goPredeclared[n] || reservedAlias[n] || strings.HasPrefix(n, "m_") || strings.HasPrefix(n, "_")
}

// localName makes a checker-assigned Go local name safe.
func localName(n string) string {
	if n == "" || n == "_" {
		return "_"
	}
	if goKeywords[n] || goPredeclared[n] || reservedAlias[n] || strings.HasPrefix(n, "m_") {
		return n + "_"
	}
	return n
}

func capFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// fieldName is the Go field of a record field or named variant payload.
func fieldName(s string) string { return capFirst(s) }

// implBase is the `<Target>` prefix of an impl's names.
func implBase(impl *checker.ImplInfo) string {
	if impl.TargetInfo != nil {
		return impl.TargetInfo.Name
	}
	if impl.Name != "" {
		return sanitize(impl.Name)
	}
	return "Impl"
}

// stdPrefix is the prefix of a std module's free functions: `Json_` for
// std.json; prelude functions take none.
func stdPrefix(mod string) string {
	if mod == "prelude" {
		return ""
	}
	parts := strings.Split(mod, ".")
	return capFirst(parts[len(parts)-1]) + "_"
}

// allocateNames assigns Go names to every type, function and constant in
// module and declaration order, so names are stable across builds.
func (e *emitter) allocateNames() {
	for _, m := range e.prog.Modules {
		p := e.pkgs[m]
		if m.IsStd || p == nil {
			continue
		}
		e.allocateModule(m, p)
	}
	if e.prog.AppCfg != nil {
		for _, n := range e.prog.AppCfg.Order {
			if ti := e.prog.AppCfg.Types[n]; ti != nil {
				e.typeNames[ti] = n
			}
		}
		for _, impl := range e.prog.AppCfg.Impls {
			for _, n := range impl.MethodOrder {
				e.fnNames[impl.Methods[n]] = "SecretsFake"
			}
		}
	}
}

func (e *emitter) allocateModule(m *checker.Module, p *pkg) {
	prefix := ""
	if m.IsStd {
		prefix = stdPrefix(m.Path)
	}
	for _, name := range m.Order {
		if ti, ok := m.Types[name]; ok {
			if m.IsStd {
				e.typeNames[ti] = ti.Name
				continue
			}
			e.typeNames[ti] = p.alloc(ti.Name)
			continue
		}
		if fi, ok := m.Fns[name]; ok {
			e.fnNames[fi] = p.alloc(prefix + capFirst(fi.Name))
			continue
		}
		if ci, ok := m.Consts[name]; ok {
			e.constNames[ci] = p.alloc(prefix + capFirst(ci.Name))
		}
	}
	for _, impl := range m.Impls {
		base := implBase(impl)
		if impl.TargetInfo != nil && !m.IsStd {
			if n, ok := e.typeNames[impl.TargetInfo]; ok {
				base = n
			}
		}
		if impl.Behaviour != nil && !m.IsStd {
			e.implNames[impl] = base
		}
		for _, n := range impl.MethodOrder {
			fi := impl.Methods[n]
			if fi == nil {
				continue
			}
			if impl.Behaviour != nil && !hasMethod(impl, n) {
				continue
			}
			e.fnNames[fi] = p.alloc(base + "_" + n)
		}
		for _, n := range sortedConstNames(impl) {
			e.constNames[impl.Consts[n]] = p.alloc(base + "_" + n)
		}
		if len(impl.Inv) > 0 && !m.IsStd {
			e.stateInvs[impl] = p.alloc(base + "_stateInv")
		}
	}
}

// hasMethod says whether an impl declares the method itself (defaults are
// taken from the behaviour and never emitted as functions).
func hasMethod(impl *checker.ImplInfo, name string) bool {
	for _, d := range impl.Defaults {
		if d == name {
			return false
		}
	}
	if impl.Decl == nil {
		return true
	}
	for _, m := range impl.Decl.Methods {
		if m.Name == name {
			return true
		}
	}
	return false
}

func sortedConstNames(impl *checker.ImplInfo) []string {
	var names []string
	if impl.Decl != nil {
		for _, c := range impl.Decl.Consts {
			if _, ok := impl.Consts[c.Name]; ok {
				names = append(names, c.Name)
			}
		}
		return names
	}
	for n := range impl.Consts {
		names = append(names, n)
	}
	sortStrings(names)
	return names
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// fnRef is the qualified Go reference to a function from file f.
func (f *file) fnRef(fi *checker.FnInfo) string {
	e := f.p.e
	if fi.Module != nil && fi.Module.IsStd && fi.Extern != nil {
		// Every std function is an rt extern (stdlib/*.jip).
		return f.rt() + "." + fi.Extern.Ident
	}
	name, ok := e.fnNames[fi]
	if !ok {
		e.failf("no Go name for function `%s`", fi.Name)
	}
	if fi.Mod == "appcfg" {
		return f.cfgRef(name)
	}
	return f.ref(e.pkgs[fi.Module], name)
}

// constRef is the qualified Go reference to a constant function.
func (f *file) constRef(ci *checker.ConstInfo) string {
	e := f.p.e
	name, ok := e.constNames[ci]
	if !ok {
		e.failf("no Go name for constant `%s`", ci.Name)
	}
	return f.ref(e.pkgs[ci.Module], name)
}

// typeRef is the qualified Go reference to a user or std type's name.
func (f *file) typeRef(ti *checker.TypeInfo) string {
	e := f.p.e
	if ti.Mod == "appcfg" {
		return f.cfgRef(ti.Name)
	}
	if ti.Module != nil && ti.Module.IsStd || ti.Mod == "prelude" {
		return f.rt() + "." + rtTypeName(ti)
	}
	name, ok := e.typeNames[ti]
	if !ok {
		e.failf("no Go name for type `%s`", ti.Name)
	}
	return f.ref(e.pkgs[ti.Module], name)
}

// implRef is the qualified reference to `<Target>_<suffix>` of an impl.
func (f *file) implRef(impl *checker.ImplInfo, suffix string) string {
	e := f.p.e
	base, ok := e.implNames[impl]
	if !ok {
		e.failf("impl has no name")
	}
	return f.ref(e.pkgs[impl.Module], base+"_"+suffix)
}

// rtTypeName is the rt name of a std type: prelude types keep their name,
// other std modules prefix the module's last segment.
func rtTypeName(ti *checker.TypeInfo) string {
	if ti.Kind == checker.Extern && ti.GoIdent != "" {
		return ti.GoIdent
	}
	if ti.Mod == "prelude" {
		return ti.Name
	}
	parts := strings.Split(ti.Mod, ".")
	return capFirst(parts[len(parts)-1]) + ti.Name
}

// cfgRef is a reference to a name of package appcfg.
func (f *file) cfgRef(name string) string {
	if f.p == f.p.e.cfgPkg {
		return name
	}
	return f.appcfg() + "." + name
}
