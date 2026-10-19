package driver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler/app"
	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/diag"
	"github.com/codingsar/jippitese/compiler/iface"
	"github.com/codingsar/jippitese/compiler/parser"
)

// TreeHash is the hash of a vendored tree (spec 10 rule 7): SHA-256 over
// the sorted lines `<path> <sha256 of content>` of every regular file,
// `.git` excluded, written `sha256-<base64>`.
func TreeHash(dir string) (string, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	var lines []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		h := sha256.Sum256(b)
		lines = append(lines, filepath.ToSlash(rel)+" "+hex.EncodeToString(h[:]))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
	return "sha256-" + base64.StdEncoding.EncodeToString(h[:]), nil
}

// readApp parses app.jip only.
func (e *Env) readApp(bag *diag.Bag) (*app.App, string) {
	src, err := os.ReadFile(filepath.Join(e.Root, "app.jip"))
	if err != nil {
		bag.Errorf("E9999", ast.Pos{}, "internal: %v", err)
		return nil, ""
	}
	f, err := parser.ParseFile("app.jip", string(src))
	if err != nil {
		bag.Errorf("E0301", ast.Pos{File: "app.jip", Line: 1, Col: 1}, "app.jip does not parse").Note("%v", err)
		return nil, ""
	}
	a, err := app.Parse(f)
	if err != nil {
		bag.Errorf("E0301", ast.Pos{File: "app.jip", Line: 1, Col: 1}, "app.jip is invalid").Note("%v", err)
		return nil, ""
	}
	return a, string(src)
}

// Fetch clones every `deps {}` entry at its commit into vendor/<alias> and
// verifies its hash; save writes computed hashes into app.jip instead of
// refusing a mismatch (spec 13 rule 7).
func (e *Env) Fetch(save bool) int {
	bag := diag.NewBag()
	a, src := e.readApp(bag)
	if a == nil {
		e.report(bag, false)
		return exitFor(bag)
	}
	lines := strings.Split(src, "\n")
	changed := false
	for _, d := range a.Deps {
		tmp, err := os.MkdirTemp("", "jip-fetch-")
		if err != nil {
			bag.Errorf("E9999", ast.Pos{}, "internal: %v", err)
			continue
		}
		defer os.RemoveAll(tmp)
		if out, err := exec.Command("git", "clone", "--quiet", d.URL, tmp).CombinedOutput(); err != nil {
			bag.Errorf("E0309", d.Pos, "cannot fetch `%s`", d.Alias).Note("%s", strings.TrimSpace(string(out))).Helpf("check the url and network access")
			continue
		}
		if out, err := exec.Command("git", "-C", tmp, "checkout", "--quiet", d.Commit).CombinedOutput(); err != nil {
			bag.Errorf("E0309", d.Pos, "commit `%s` of `%s` not found", d.Commit, d.Alias).Note("%s", strings.TrimSpace(string(out))).Helpf("fix `commit:` in app.jip")
			continue
		}
		os.RemoveAll(filepath.Join(tmp, ".git"))
		got, err := TreeHash(tmp)
		if err != nil {
			bag.Errorf("E9999", ast.Pos{}, "internal: %v", err)
			continue
		}
		if got != d.Hash {
			if !save {
				dg := bag.Errorf("E0309", d.Pos, "fetched `%s` does not match its hash", d.Alias)
				dg.Label("app.jip says `%s`", d.Hash)
				dg.Note("commit %s hashes to `%s`", d.Commit, got)
				dg.Helpf("run `jip fetch --save` if this commit is the one you want")
				continue
			}
			ln := d.Pos.Line - 1
			if ln >= 0 && ln < len(lines) && strings.Contains(lines[ln], strconv.Quote(d.Hash)) {
				lines[ln] = strings.Replace(lines[ln], strconv.Quote(d.Hash), strconv.Quote(got), 1)
				changed = true
			} else {
				bag.Errorf("E0306", d.Pos, "cannot write the hash of `%s`", d.Alias).Helpf("set `hash: %q` by hand", got)
				continue
			}
		}
		dst := filepath.Join(e.Root, "vendor", d.Alias)
		os.RemoveAll(dst)
		os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := os.Rename(tmp, dst); err != nil {
			if out, cerr := exec.Command("cp", "-r", tmp+"/.", dst).CombinedOutput(); cerr != nil {
				bag.Errorf("E9999", ast.Pos{}, "internal: %v %s", err, out)
				continue
			}
		}
		fmt.Fprintf(e.Stdout, "fetched %s at %s (%s)\n", d.Alias, d.Commit, got)
	}
	if changed {
		e.writeFile(bag, filepath.Join(e.Root, "app.jip"), strings.Join(lines, "\n"))
	}
	e.report(bag, false)
	return exitFor(bag)
}

// Context prints the context pack of a module (spec 09 rule 10).
func (e *Env) Context(module string, o iface.ContextOptions) int {
	bag := diag.NewBag()
	prog := e.load(bag)
	if prog == nil {
		e.report(bag, false)
		return exitFor(bag)
	}
	m := prog.ByPath[module]
	if m == nil {
		fmt.Fprintf(e.Stderr, "error: no module `%s`\n", module)
		return ExitUsage
	}
	fmt.Fprint(e.Stdout, iface.Context(prog, m, o))
	return ExitOK
}

// New scaffolds a project (spec 13 rule 1).
func New(dir, name string) error {
	root := filepath.Join(dir, name)
	if _, err := os.Stat(root); err == nil {
		return fmt.Errorf("%s already exists", root)
	}
	files := map[string]string{
		"app.jip": `app {
  name: "` + name + `"
  contracts: pre
  workflow { mode: embedded }
  run Main
}
`,
		name + "/main.jip": `module ` + name + `

/// A greeting request.
pub type Hello { who: Str }

/// The service state.
pub type Main {}

/// Greets.
pub fn greet(h: Hello) -> Str { "hello {h.who}" }

/// Answers greetings over HTTP.
impl Service for Main {
  type Req = Hello
  type Resp = Str
  type E = Str
  fn init(cfg: Config) -> Result<Self, Str> with {io} { Ok(Main {}) }
  fn handle(self, req: Hello) -> Result<Str, Str> with {net} { Ok(greet(req)) }
}
`,
		name + "/scenarios.jip": `module ` + name + `.scenarios
use ` + name + `

scenario "greet says hello" {
  when  s = greet(Hello { who: "world" })
  then  s == "hello world"
}
`,
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}
