// Package emit lowers a checked program to Go source under `.jip/gen/`
// (spec 13 pipeline). Every statement carries a `//line` directive back to
// its jippitese position unless Options.GoPositions is set. Output is
// deterministic: the same program emits byte-identical files.
package emit

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codingsar/jippitese/compiler"
	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/rt"
)

// Options tunes the emission.
type Options struct {
	// GoPositions drops the `//line` directives so Go tools report
	// positions in the generated files (`jip build --go-positions`).
	GoPositions bool
	// Release sets contract checking to the app's level instead of `all`.
	Release bool
}

// Output is the generated tree, keyed by path relative to `.jip/gen/`.
type Output struct {
	Files map[string][]byte
	// Links are symlinks to create, path → target.
	Links map[string]string
	// Packages maps module paths to import paths under `jip/`.
	Packages map[string]string
}

// Write materialises the output under dir, removing stale generated files.
func (o *Output) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Remove files we do not produce, so stale packages cannot break the
	// build. Directories are pruned when emptied.
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if _, ok := o.Files[filepath.ToSlash(rel)]; !ok {
			if t, err := os.Readlink(p); err != nil || t != o.Links[filepath.ToSlash(rel)] {
				os.Remove(p)
			}
		}
		return nil
	})
	for _, name := range o.sorted() {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, o.Files[name]) {
			continue
		}
		if err := os.WriteFile(p, o.Files[name], 0o644); err != nil {
			return err
		}
	}
	for name, target := range o.Links {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if t, err := os.Readlink(p); err == nil && t == target {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(target, p); err != nil {
			return err
		}
	}
	pruneEmpty(dir)
	return nil
}

func pruneEmpty(dir string) {
	var dirs []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != dir {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		es, err := os.ReadDir(d)
		if err == nil && len(es) == 0 {
			os.Remove(d)
		}
	}
}

func (o *Output) sorted() []string {
	names := make([]string, 0, len(o.Files))
	for n := range o.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// emitter holds the whole-program state of one emission.
type emitter struct {
	prog       *checker.Program
	opts       Options
	typeNames  map[*checker.TypeInfo]string
	fnNames    map[*checker.FnInfo]string
	constNames map[*checker.ConstInfo]string
	implNames  map[*checker.ImplInfo]string // the `<Target>` base used for spec/check names
	invNames   map[*checker.TypeInfo]string
	stateInvs  map[*checker.ImplInfo]string // `<Target>_stateInv`, the check of an impl `inv`
	cfgPkg     *pkg
	pkgs       map[*checker.Module]*pkg
	pkgOrder   []*pkg
	out        *Output
	mainPkg    string
}

// pkg is one generated Go package.
type pkg struct {
	e     *emitter
	name  string // Go package name
	path  string // import path
	dir   string // directory under .jip/gen
	mod   *checker.Module
	names map[string]bool
	files []*file
}

// file is one generated Go file.
type file struct {
	p       *pkg
	name    string
	imports map[string]string // alias → import path
	body    strings.Builder
	test    bool
}

func (p *pkg) alloc(base string) string {
	name := base
	for i := 2; p.names[name] || reservedName(name); i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	p.names[name] = true
	return name
}

func (p *pkg) newFile(name string, test bool) *file {
	f := &file{p: p, name: name, imports: map[string]string{}, test: test}
	p.files = append(p.files, f)
	return f
}

// use records an import and returns the alias to use in this file.
func (f *file) use(alias, path string) string {
	f.imports[alias] = path
	return alias
}

func (f *file) rt() string                        { return f.use("rt", f.p.e.mainPkg+"/rt") }
func (f *file) appcfg() string                    { return f.use("appcfg", f.p.e.mainPkg+"/appcfg") }
func (f *file) gotime() string                    { return f.use("time", "time") }
func (f *file) printf(format string, args ...any) { fmt.Fprintf(&f.body, format, args...) }

// line emits a `//line` directive at column 0 for pos.
func (f *file) line(pos ast.Pos) {
	if f.p.e.opts.GoPositions || pos.File == "" {
		return
	}
	f.body.WriteString("\n//line " + pos.File + ":" + fmt.Sprint(pos.Line) + ":" + fmt.Sprint(pos.Col) + "\n")
}

// render formats the file; on a gofmt failure the raw source is returned
// with the error so `jip build` can report an internal error with context.
func (f *file) render() ([]byte, error) {
	var b strings.Builder
	b.WriteString("// Code generated by jip; DO NOT EDIT.\n\n")
	b.WriteString("package " + f.p.name + "\n\n")
	if len(f.imports) > 0 {
		aliases := make([]string, 0, len(f.imports))
		for a := range f.imports {
			aliases = append(aliases, a)
		}
		sort.Strings(aliases)
		b.WriteString("import (\n")
		for _, a := range aliases {
			p := f.imports[a]
			last := p[strings.LastIndex(p, "/")+1:]
			if last == a {
				b.WriteString("\t\"" + p + "\"\n")
			} else {
				b.WriteString("\t" + a + " \"" + p + "\"\n")
			}
		}
		b.WriteString(")\n\n")
	}
	b.WriteString(f.body.String())
	src := []byte(b.String())
	out, err := format.Source(src)
	if err != nil {
		return src, fmt.Errorf("%s: %w", f.name, err)
	}
	return fixLines(out), nil
}

// fixLines moves every `//line` directive back to column 0 after gofmt.
func fixLines(src []byte) []byte {
	lines := bytes.Split(src, []byte("\n"))
	for i, l := range lines {
		t := bytes.TrimLeft(l, " \t")
		if bytes.HasPrefix(t, []byte("//line ")) {
			lines[i] = t
		}
	}
	return bytes.Join(lines, []byte("\n"))
}

// Emit lowers prog. It returns an error only on an internal failure: the
// checker has already rejected every program the emitter cannot lower.
func Emit(prog *checker.Program, opts Options) (out *Output, err error) {
	defer func() {
		if r := recover(); r != nil {
			if ie, ok := r.(internalError); ok {
				err = ie
				return
			}
			panic(r)
		}
	}()
	e := &emitter{
		prog:       prog,
		opts:       opts,
		typeNames:  map[*checker.TypeInfo]string{},
		fnNames:    map[*checker.FnInfo]string{},
		constNames: map[*checker.ConstInfo]string{},
		implNames:  map[*checker.ImplInfo]string{},
		invNames:   map[*checker.TypeInfo]string{},
		stateInvs:  map[*checker.ImplInfo]string{},
		pkgs:       map[*checker.Module]*pkg{},
		out:        &Output{Files: map[string][]byte{}, Packages: map[string]string{}},
		mainPkg:    "jip/" + sanitize(prog.AppName),
	}
	e.allocatePackages()
	e.allocateNames()
	e.emitRuntime()
	e.emitAppCfg()
	for _, p := range e.pkgOrder {
		if p.mod == nil || p.mod.IsStd {
			continue
		}
		e.emitModule(p)
	}
	e.emitMain()
	e.emitGoMod()
	for _, p := range e.pkgOrder {
		for _, f := range p.files {
			b, err := f.render()
			if err != nil {
				e.out.Files[p.dir+"/"+f.name] = b
				return e.out, fmt.Errorf("internal: generated Go does not format: %w", err)
			}
			e.out.Files[p.dir+"/"+f.name] = b
		}
	}
	return e.out, nil
}

type internalError struct{ msg string }

func (e internalError) Error() string { return e.msg }

func (e *emitter) failf(format string, args ...any) {
	panic(internalError{fmt.Sprintf("internal: "+format, args...)})
}

// allocatePackages creates one Go package per module (scenario modules join
// their parent as a test file).
func (e *emitter) allocatePackages() {
	for _, m := range e.prog.Modules {
		if m.IsStd || m.IsScenarios {
			continue
		}
		p := &pkg{e: e, mod: m, names: map[string]bool{}}
		segs := strings.Split(m.Path, ".")
		for i, s := range segs {
			segs[i] = sanitize(s)
		}
		if m.IsDep {
			p.dir = "deps/" + strings.Join(segs, "/")
		} else {
			p.dir = strings.Join(segs, "/")
		}
		p.path = e.mainPkg + "/" + p.dir
		p.name = goPkgName(segs[len(segs)-1])
		e.pkgs[m] = p
		e.pkgOrder = append(e.pkgOrder, p)
		e.out.Packages[m.Path] = p.path
	}
	for _, m := range e.prog.Modules {
		if m.IsScenarios && m.Parent != nil {
			e.pkgs[m] = e.pkgs[m.Parent]
		}
	}
}

// goPkgName makes a module segment a Go package name that cannot collide
// with the import aliases the emitter reserves.
func goPkgName(seg string) string {
	if reservedName(seg) || reservedAlias[seg] {
		return seg + "_"
	}
	return seg
}

func sanitize(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_', r >= '0' && r <= '9' && i > 0:
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	return b.String()
}

// emitRuntime copies the rt package sources into the output.
func (e *emitter) emitRuntime() {
	entries, err := fs.ReadDir(rt.Sources, ".")
	if err != nil {
		e.failf("runtime sources: %v", err)
	}
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), "_test.go") || !strings.HasSuffix(en.Name(), ".go") {
			continue
		}
		b, err := fs.ReadFile(rt.Sources, en.Name())
		if err != nil {
			e.failf("runtime sources: %v", err)
		}
		e.out.Files["rt/"+en.Name()] = b
	}
}

func (e *emitter) emitGoMod() {
	var b strings.Builder
	b.WriteString("module " + e.mainPkg + "\n\ngo " + compiler.GoVersion + "\n\ntoolchain " + compiler.GoToolchain + "\n")
	// A gomod dependency builds from vendor/<alias> (spec 13 rule 10).
	if e.prog.App != nil {
		for _, d := range e.prog.App.Deps {
			if d.Kind == "gomod" {
				b.WriteString("\nrequire " + d.GoPath + " " + GoModVersion(d.GoPath) + "\n")
				b.WriteString("\nreplace " + d.GoPath + " => ../../vendor/" + d.Alias + "\n")
			}
		}
	}
	e.out.Files["go.mod"] = []byte(b.String())
}

// GoModVersion is the placeholder version a replaced module is required at:
// v0, or the major version its path ends in (`/v2`, gopkg.in's `.v3`).
func GoModVersion(path string) string {
	major := "v0"
	last := path[strings.LastIndex(path, "/")+1:]
	isMajor := func(v string) bool { return len(v) > 1 && v[0] == 'v' && strings.Trim(v[1:], "0123456789") == "" }
	if i := strings.LastIndex(last, ".v"); strings.HasPrefix(path, "gopkg.in/") && i >= 0 && isMajor(last[i+1:]) {
		major = last[i+1:]
	} else if isMajor(last) && last != "v0" && last != "v1" && strings.Count(path, "/") > 0 {
		major = last
	}
	return major + ".0.0-00010101000000-000000000000"
}

// emitMain writes main.go: config load, contract level, runners.
func (e *emitter) emitMain() {
	app := e.prog.App
	p := &pkg{e: e, name: "main", path: e.mainPkg, dir: ".", names: map[string]bool{}}
	e.pkgOrder = append(e.pkgOrder, p)
	f := p.newFile("main.go", false)
	f.use("os", "os")
	f.use("fmt", "fmt")
	rtp := f.rt()
	cfg := f.appcfg()
	level := "all"
	if e.opts.Release && app != nil {
		level = app.ContractLevel()
	}
	f.printf("func main() {\n")
	f.printf("\tctx := %s.NewCtx()\n", rtp)
	f.printf("\t%s.SetContracts(%q)\n", rtp, level)
	f.printf("\tcfg, runs, fx, err := %s.Load()\n", cfg)
	f.printf("\tif err != nil {\n\t\tfmt.Fprintln(os.Stderr, \"error: \"+err.Error())\n\t\tos.Exit(2)\n\t}\n")
	f.printf("\tctx.Config = cfg\n\t_ = runs\n")
	f.printf("\tif err := ctx.ApplyEffects(fx); err != nil {\n\t\tfmt.Fprintln(os.Stderr, \"error: \"+err.Error())\n\t\tos.Exit(2)\n\t}\n")
	if app != nil {
		f.printf("\tctx.Store = %q\n", app.Workflow.Store)
	}
	if app != nil && len(app.Models) > 0 {
		f.printf("\tctx.Llm = %s.Models{\n", rtp)
		for _, m := range app.Models {
			f.printf("\t\t%q: %s.NewModel(%q, %q, %q, %q),\n", m.Role, rtp, m.Role, m.Provider, m.URL, m.Name)
		}
		f.printf("\t}\n")
	}
	if app != nil && len(app.Runs) > 0 {
		f.printf("\t%s.Serve(ctx", rtp)
		for _, r := range app.Runs {
			impl, pk := e.findRunImpl(r.Name)
			if impl == nil {
				continue
			}
			alias := f.use(pk.importAlias(), pk.path)
			f.printf(",\n\t\t%s.%sRunner(%s.%s_spec, runs[%q])", rtp, impl.Behaviour.Name, alias, e.implNames[impl], r.Name)
		}
		f.printf(")\n")
	}
	f.printf("}\n")
}

// findRunImpl finds the behaviour impl a `run` line names.
func (e *emitter) findRunImpl(name string) (*checker.ImplInfo, *pkg) {
	for _, m := range e.prog.Modules {
		if m.IsStd || m.IsScenarios {
			continue
		}
		for _, impl := range m.Impls {
			if impl.Behaviour != nil && impl.TargetInfo != nil && impl.TargetInfo.Name == name {
				// An Agent is invoked through `X.run(goal)`, never supervised.
				if isV0Behaviour(impl.Behaviour.Name) && impl.Behaviour.Name != "Agent" {
					return impl, e.pkgs[m]
				}
			}
		}
	}
	return nil, nil
}

func isV0Behaviour(name string) bool {
	switch name {
	case "Service", "Worker", "Cron", "Workflow", "Repository", "Cache", "Machine", "Agent":
		return true
	}
	return false
}

// importAlias is the alias other packages use for this package.
func (p *pkg) importAlias() string {
	if p.mod == nil {
		return p.name
	}
	segs := strings.Split(p.mod.Path, ".")
	for i, s := range segs {
		segs[i] = sanitize(s)
	}
	return "m_" + strings.Join(segs, "_")
}

// ref returns the qualified reference to a top-level name of pkg q from
// file f.
func (f *file) ref(q *pkg, name string) string {
	if q == f.p {
		return name
	}
	if q == nil {
		f.p.e.failf("reference to `%s` in no package", name)
	}
	return f.use(q.importAlias(), q.path) + "." + name
}
