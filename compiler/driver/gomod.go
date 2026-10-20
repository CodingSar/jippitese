package driver

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codingsar/jippitese/compiler/app"
	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/diag"
	"github.com/codingsar/jippitese/compiler/emit"
)

// goMod is what jip reads from a vendored module's go.mod.
type goMod struct {
	Module string
	Go     string
	Reqs   []string // required module paths
}

// readGoMod parses the module, go and require lines of a go.mod.
func readGoMod(path string) (*goMod, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := &goMod{}
	inReq := false
	for _, ln := range strings.Split(string(b), "\n") {
		if i := strings.Index(ln, "//"); i >= 0 {
			ln = ln[:i]
		}
		f := strings.Fields(ln)
		if len(f) == 0 {
			continue
		}
		if inReq {
			if f[0] == ")" {
				inReq = false
			} else {
				m.Reqs = append(m.Reqs, unquote(f[0]))
			}
			continue
		}
		switch {
		case f[0] == "module" && len(f) > 1:
			m.Module = unquote(f[1])
		case f[0] == "go" && len(f) > 1:
			m.Go = f[1]
		case f[0] == "require" && len(f) > 1 && f[1] == "(":
			inReq = true
		case f[0] == "require" && len(f) > 1:
			m.Reqs = append(m.Reqs, unquote(f[1]))
		}
	}
	return m, nil
}

func unquote(s string) string { return strings.Trim(s, "\"`") }

// checkGoMods checks that each gomod entry's vendored go.mod declares its
// path (E0309) and that every module it requires has a gomod entry (E0504).
func (e *Env) checkGoMods(a *app.App, bag *diag.Bag) {
	for _, d := range a.Deps {
		if d.Kind != "gomod" {
			continue
		}
		m, err := readGoMod(filepath.Join(e.Root, "vendor", d.Alias, "go.mod"))
		if err != nil {
			continue // checkVendor reports a missing tree
		}
		if m.Module != d.GoPath {
			dg := bag.Errorf("E0309", d.Pos, "vendored `%s` is not module `%s`", d.Alias, d.GoPath)
			dg.Label("app.jip says `%s`", d.GoPath)
			dg.Note("vendor/%s/go.mod declares `module %s`", d.Alias, m.Module)
			dg.Helpf("name the module path the repository declares")
			continue
		}
		for _, r := range m.Reqs {
			if dep := a.GoModFor(r); dep == nil || dep.GoPath != r {
				dg := bag.Errorf("E0504", d.Pos, "`%s` requires `%s`, which has no gomod entry", d.GoPath, r)
				dg.Helpf("add `%s = gomod(%q, commit: \"…\", hash: \"…\")` to `deps`", lastElem(r), r)
			}
		}
	}
}

func lastElem(path string) string {
	s := path[strings.LastIndex(path, "/")+1:]
	return strings.NewReplacer(".", "_", "-", "_").Replace(s)
}

// vendorGo makes the generated module build its gomod dependencies from
// vendor/<alias> (spec 13 rule 10): it links .jip/gen/vendor/<path> to the
// tree and synthesises vendor/modules.txt.
func (e *Env) vendorGo(prog *checker.Program, bag *diag.Bag, out *emit.Output) {
	if prog.App == nil {
		return
	}
	var txt strings.Builder
	for _, d := range prog.App.Deps {
		if d.Kind != "gomod" {
			continue
		}
		dir := filepath.Join(e.Root, "vendor", d.Alias)
		m, err := readGoMod(filepath.Join(dir, "go.mod"))
		if err != nil {
			dg := bag.Errorf("E0309", d.Pos, "dependency `%s` is not vendored", d.Alias)
			dg.Label("no `vendor/%s/go.mod`", d.Alias)
			dg.Helpf("run `jip fetch`")
			continue
		}
		link := filepath.Join(e.genDir(), "vendor", filepath.FromSlash(d.GoPath))
		target, err := filepath.Rel(filepath.Dir(link), dir)
		if err != nil {
			target = dir
		}
		if out.Links == nil {
			out.Links = map[string]string{}
		}
		out.Links["vendor/"+d.GoPath] = target
		ver := emit.GoModVersion(d.GoPath)
		repl := "../../vendor/" + d.Alias
		txt.WriteString("# " + d.GoPath + " " + ver + " => " + repl + "\n")
		if m.Go != "" {
			txt.WriteString("## explicit; go " + m.Go + "\n")
		} else {
			txt.WriteString("## explicit\n")
		}
		for _, p := range goPackages(dir) {
			if p == "." {
				txt.WriteString(d.GoPath + "\n")
			} else {
				txt.WriteString(d.GoPath + "/" + p + "\n")
			}
		}
		txt.WriteString("# " + d.GoPath + " => " + repl + "\n")
	}
	if txt.Len() > 0 {
		out.Files["vendor/modules.txt"] = []byte(txt.String())
	}
}

// goPackages lists the directories of a module tree holding non-test Go
// files, relative and sorted; testdata, `.`/`_` directories, nested modules
// and vendor are skipped as `go` skips them.
func goPackages(root string) []string {
	seen := map[string]bool{}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if p != root {
				if n == "testdata" || n == "vendor" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		n := d.Name()
		if strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") && !strings.HasPrefix(n, ".") && !strings.HasPrefix(n, "_") {
			rel, _ := filepath.Rel(root, filepath.Dir(p))
			rel = filepath.ToSlash(rel)
			if !seen[rel] {
				seen[rel] = true
				out = append(out, rel)
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}
