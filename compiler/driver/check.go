package driver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/app"
	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/diag"
	"github.com/codingsar/jippitese/compiler/emit"
	"github.com/codingsar/jippitese/compiler/iface"
)

// CheckOptions are the flags of `jip check`.
type CheckOptions struct {
	WriteInterfaces bool
	AcceptBreaking  []string
	Effective       bool
	EnvName         string
}

// Check type-checks, then verifies interfaces, externs and vendored
// hashes (spec 13 rule 4). It never emits Go.
func (e *Env) Check(o CheckOptions) int {
	bag := diag.NewBag()
	prog := e.load(bag)
	if prog == nil {
		e.report(bag, true)
		return exitFor(bag)
	}
	if o.Effective {
		e.printEffective(prog, o.EnvName)
		e.report(bag, true)
		return exitFor(bag)
	}
	emit.CheckExterns(prog, bag)
	e.checkVendor(prog, bag)
	breaking := e.checkInterfaces(prog, bag, o)
	e.externReport(prog)
	e.report(bag, true)
	if code := exitFor(bag); code != ExitOK {
		return code
	}
	if breaking {
		return ExitFail
	}
	return ExitOK
}

// accepted lists the modules whose break is accepted: --accept-breaking
// and `jip-breaking: <module>` trailers on HEAD (R8).
func (e *Env) accepted(flags []string) map[string]bool {
	acc := map[string]bool{}
	for _, f := range flags {
		acc[f] = true
	}
	out, err := exec.Command("git", "-C", e.Root, "log", "-1", "--format=%B").Output()
	if err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if m, ok := strings.CutPrefix(strings.TrimSpace(l), "jip-breaking:"); ok {
				acc[strings.TrimSpace(m)] = true
			}
		}
	}
	return acc
}

// checkInterfaces extracts every app module's interface, classifies it
// against the committed file and reports drift (spec 09 rules 7 and 8).
// It reports whether an unaccepted breaking change was found.
func (e *Env) checkInterfaces(prog *checker.Program, bag *diag.Bag, o CheckOptions) bool {
	acc := e.accepted(o.AcceptBreaking)
	breaking := false
	for _, m := range prog.Modules {
		if m.IsStd || m.IsScenarios || m.IsDep || m.Dir == "" {
			continue
		}
		fresh := iface.Extract(m)
		rel := filepath.ToSlash(filepath.Join(m.Dir, iface.FileName))
		path := filepath.Join(e.Root, rel)
		text := fresh.Text()
		oldText, err := os.ReadFile(path)
		if err != nil {
			if o.WriteInterfaces {
				e.writeFile(bag, path, text)
				fmt.Fprintf(e.Stdout, "%s: new interface\n", m.Path)
				continue
			}
			d := bag.Errorf("E0201", ast.Pos{File: rel, Line: 1, Col: 1}, "no interface file for `%s`", m.Path)
			d.Label("`%s` is missing", rel)
			d.Helpf("run `jip check --write-interfaces` and commit `%s`", rel)
			continue
		}
		old, pubDigest, _, perr := iface.Parse(string(oldText))
		if perr != nil {
			if o.WriteInterfaces {
				e.writeFile(bag, path, text)
				continue
			}
			bag.Errorf("E0201", ast.Pos{File: rel, Line: 1, Col: 1}, "interface file of `%s` does not parse", m.Path).Note("%v", perr).Helpf("run `jip check --write-interfaces`")
			continue
		}
		rep := iface.Classify(old, fresh)
		if rep.Level != iface.None {
			fmt.Fprint(e.Stdout, rep.Text())
		}
		if rep.Level == iface.Breaking {
			if acc[m.Path] {
				fmt.Fprintf(e.Stdout, "%s: breaking change accepted\n", m.Path)
			} else {
				breaking = true
				fmt.Fprintf(e.Stdout, "%s: breaking change not accepted; pass `--accept-breaking %s` or add the trailer `jip-breaking: %s`\n", m.Path, m.Path, m.Path)
			}
		}
		if string(oldText) == text {
			continue
		}
		if o.WriteInterfaces {
			e.writeFile(bag, path, text)
			continue
		}
		col := len("interface "+m.Path+" digest ") + 1
		d := bag.Errorf("E0201", ast.Pos{File: rel, Line: 1, Col: col}, "interface drift in `%s`", m.Path)
		d.Label("committed digest; extracted %s", iface.Digest(fresh.Public))
		_ = pubDigest
		d.Note("classification: %s", rep.Level)
		d.Helpf("run `jip check --write-interfaces` and commit `%s`", rel)
	}
	return breaking
}

func (e *Env) writeFile(bag *diag.Bag, path, text string) {
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		bag.Errorf("E9999", ast.Pos{}, "internal: %v", err)
	}
}

// externReport prints the extern table of spec 11.
func (e *Env) externReport(prog *checker.Program) {
	type row struct{ mod, fn, target, effects, unsafe string }
	var rows []row
	std := 0
	for _, m := range prog.Modules {
		for _, fi := range m.Externs {
			if fi.Extern == nil {
				continue
			}
			if m.IsStd {
				std++
				continue
			}
			if fi.Extern.Pkg == "rt" {
				continue
			}
			u := ""
			if fi.Unsafe {
				u = " unsafe pure"
			}
			rows = append(rows, row{m.Path, fi.Name, `"` + fi.Extern.Pkg + `".` + fi.Extern.Ident, checker.EffectSetString(fi.Type.Effects), u})
		}
	}
	max := 0
	if prog.App != nil {
		max = prog.App.Externs.Max
	}
	fmt.Fprintf(e.Stdout, "externs: %d declared in app/vendored modules (max %d), %d in stdlib\n", len(rows), max, std)
	for _, r := range rows {
		fmt.Fprintf(e.Stdout, "  %-16s %-14s %-36s %s%s\n", r.mod, r.fn, r.target, r.effects, r.unsafe)
	}
}

// printEffective prints the resolved config of an env (spec 10 rule 6):
// one line per leaf with its value and the layer it came from.
func (e *Env) printEffective(prog *checker.Program, env string) {
	a := prog.App
	if a == nil {
		return
	}
	block := map[string]string{}
	for _, en := range a.Envs {
		if en.Name == env {
			for _, x := range en.Entries {
				block[x.Key] = appValue(x.Value)
			}
		}
	}
	envNote := ""
	if env != "" {
		envNote = "  (JIP_ENV=" + env + ")"
	}
	fmt.Fprintf(e.Stdout, "app %q  env: %s%s\n", a.Name, orNone(env), envNote)
	line := func(path, val, src string) {
		fmt.Fprintf(e.Stdout, "  %-32s %-28s %s\n", path, val, src)
	}
	resolve := func(path, val, src string, overridable bool) {
		if v, ok := block[path]; ok && overridable {
			val, src = v, "env "+env
		}
		key := "JIP_" + strings.ToUpper(strings.ReplaceAll(path, ".", "__"))
		if v, ok := os.LookupEnv(key); ok && overridable {
			val, src = v, key
		}
		line(path, val, src)
	}
	line("name", strconv.Quote(a.Name), "app")
	for _, cf := range a.Config {
		val, src, over := "<unset>", "-", true
		if cf.Default != nil {
			val, src, over = appValue(&ast.AppLit{X: cf.Default}), "app", cf.IsDefault
			if cf.IsDefault {
				src = "app (default)"
			}
		}
		resolve("config."+cf.Name, val, src, over)
	}
	for _, s := range a.Secrets {
		key := "JIP_SECRETS__" + strings.ToUpper(s.Name)
		if _, ok := os.LookupEnv(key); ok {
			line("secrets."+s.Name, "<set>", key)
		} else {
			line("secrets."+s.Name, "<unset>", "-")
		}
	}
	line("contracts", a.ContractLevel(), "app")
	for _, r := range a.Runs {
		defs := [][3]string{{"restart", "permanent"}, {"max_restarts", "3"}, {"period", "60s"}, {"shutdown_timeout", "10s"}}
		for _, d := range defs {
			path := "run." + r.Name + "." + d[0]
			if en := r.Fields[d[0]]; en != nil {
				resolve(path, appValue(en.Value), "app", true)
			} else {
				resolve(path, d[1], "app (default)", true)
			}
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func appValue(v ast.AppValue) string {
	switch x := v.(type) {
	case *ast.AppLit:
		if s, ok := x.X.(*ast.StrLit); ok && len(s.Parts) == 1 && s.Parts[0].Expr == nil {
			return s.Parts[0].Lit
		}
		return checker.ExprString(x.X)
	case *ast.AppPath:
		return strings.Join(x.Path, ".")
	}
	if s, ok := app.LitString(v); ok {
		return s
	}
	return "?"
}

// checkVendor re-hashes every vendored dependency against app.jip (E0309).
func (e *Env) checkVendor(prog *checker.Program, bag *diag.Bag) {
	if prog.App == nil {
		return
	}
	for _, d := range prog.App.Deps {
		dir := filepath.Join(e.Root, "vendor", d.Alias)
		got, err := TreeHash(dir)
		if err != nil {
			dg := bag.Errorf("E0309", d.Pos, "dependency `%s` is not vendored", d.Alias)
			dg.Label("no `vendor/%s`", d.Alias)
			dg.Helpf("run `jip fetch`")
			continue
		}
		if got != d.Hash {
			dg := bag.Errorf("E0309", d.Pos, "vendored `%s` does not match its hash", d.Alias)
			dg.Label("app.jip says `%s`", d.Hash)
			dg.Note("vendor/%s hashes to `%s`", d.Alias, got)
			dg.Helpf("run `jip fetch` to restore it, or `jip fetch --save` after a deliberate update")
		}
	}
}
