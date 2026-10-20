// Package modules locates an app root and loads every .jip file under it
// into a checker.Input (09 rules 1, 2 and 3).
package modules

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codingsar/jippitese/compiler/app"
	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/diag"
	"github.com/codingsar/jippitese/compiler/parser"
)

// FindRoot walks up from dir to the nearest directory containing app.jip.
func FindRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for d := abs; ; d = filepath.Dir(d) {
		if st, err := os.Stat(filepath.Join(d, "app.jip")); err == nil && !st.IsDir() {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("no app.jip found in %s or any parent directory", abs)
		}
	}
}

// Load parses every .jip file under root. Parse errors and module-line
// mismatches are added to bag; the returned Input is complete only when
// bag has no errors. Files under .jip/ and hidden directories are skipped.
func Load(root string, bag *diag.Bag) (*checker.Input, error) {
	in := &checker.Input{}
	byPath := map[string]*checker.ModuleSrc{}
	var order []string
	addFile := func(modPath, dir string, f *ast.File, scen, dep bool) {
		key := modPath
		if scen {
			key = modPath + ".scenarios"
		}
		m := byPath[key]
		if m == nil {
			m = &checker.ModuleSrc{Path: key, Dir: dir, IsScenarios: scen, IsDep: dep}
			byPath[key] = m
			order = append(order, key)
		}
		m.Files = append(m.Files, f)
	}
	var files []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".jip") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	rootAliases := map[string]ast.Pos{}
	goMods := map[string]bool{} // aliases of gomod deps: Go trees, no modules
	for _, p := range files {
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		bag.AddSource(rel, string(src))
		dir := filepath.ToSlash(filepath.Dir(rel))
		base := filepath.Base(rel)
		parts := strings.Split(dir, "/")
		dep := false
		if parts[0] == "vendor" {
			dep = true
			parts = parts[1:]
			if len(parts) > 0 && goMods[parts[0]] {
				continue
			}
		}
		if base == "app.jip" {
			if rel == "app.jip" {
				f, err := parser.ParseFile(rel, string(src))
				if err != nil {
					addParseError(bag, rel, err)
					continue
				}
				in.AppFile = f
				a, err := app.Parse(f)
				if err != nil {
					addParseError(bag, rel, err)
					continue
				}
				in.App = a
				for _, d := range a.Deps {
					rootAliases[d.Alias] = d.Pos
					goMods[d.Alias] = d.Kind == "gomod"
				}
			}
			// A dependency's own app.jip only names its root module.
			continue
		}
		f, err := parser.ParseFile(rel, string(src))
		if err != nil {
			addParseError(bag, rel, err)
			continue
		}
		if dir == "." || (dep && len(parts) == 0) {
			bag.Errorf("E0101", ast.Pos{File: rel, Line: 1, Col: 1}, "module file outside a module directory").
				Note("every module lives in its own directory under the app root; `%s` is at the root", rel)
			continue
		}
		expected := strings.Join(parts, ".")
		scen := base == "scenarios.jip"
		if scen {
			expected += ".scenarios"
		}
		found := strings.Join(f.Module, ".")
		if found != expected {
			pos := ast.Pos{File: rel, Line: 1, Col: 8}
			bag.Errorf("E0101", pos, "module line does not match directory").
				Width(len(found)).
				Label("found `%s`", found).
				Note("this file is in `%s/`, expected `module %s`", dir, expected).
				Helpf("replace line 1 with `module %s`", expected)
			continue
		}
		addFile(strings.Join(parts, "."), dir, f, scen, dep)
	}
	if in.App == nil && in.AppFile == nil && !bag.HasErrors() {
		bag.Errorf("E0100", ast.Pos{File: "app.jip", Line: 1, Col: 1}, "no app.jip at the app root")
	}
	for _, k := range order {
		m := byPath[k]
		if !m.IsDep && !m.IsScenarios && !strings.Contains(m.Path, ".") {
			if pos, ok := rootAliases[m.Path]; ok {
				bag.Errorf("E0106", pos, "root module `%s` collides with dependency alias `%s`", m.Path, m.Path).
					Note("a single-segment `use %s` resolves to the dependency first", m.Path).
					Helpf("rename the directory `%s/` or the alias in `deps {}`", m.Dir)
			}
		}
		in.Modules = append(in.Modules, m)
	}
	return in, nil
}

func addParseError(bag *diag.Bag, rel string, err error) {
	if pe, ok := err.(*parser.Error); ok {
		bag.Errorf(pe.Code, pe.Pos, "%s", pe.Msg)
		return
	}
	bag.Errorf("E0001", ast.Pos{File: rel, Line: 1, Col: 1}, "%v", err)
}
