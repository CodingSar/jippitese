// Package driver runs the `jip` commands (spec 13): it loads and checks a
// project, emits Go under .jip/gen, and drives the Go toolchain. cmd/jip
// only parses flags and calls it.
package driver

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/codingsar/jippitese/compiler"
	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/diag"
	"github.com/codingsar/jippitese/compiler/emit"
	"github.com/codingsar/jippitese/compiler/modules"
)

// Exit codes (spec 13 rule 8).
const (
	ExitOK       = 0
	ExitFail     = 1
	ExitUsage    = 2
	ExitInternal = 3
)

// Env is where a command runs and writes.
type Env struct {
	Root   string // directory holding app.jip
	Stdout io.Writer
	Stderr io.Writer
	JSON   bool // diagnostics as JSON Lines on stdout
	All    bool // show every diagnostic, not the first 20
}

// NewEnv finds the project root from dir.
func NewEnv(dir string, stdout, stderr io.Writer) (*Env, error) {
	root, err := modules.FindRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Env{Root: root, Stdout: stdout, Stderr: stderr}, nil
}

// report prints the diagnostics of bag.
func (e *Env) report(bag *diag.Bag, warnings bool) {
	if e.JSON {
		bag.FormatJSON(e.Stdout, warnings)
		return
	}
	bag.Format(e.Stderr, e.All, warnings)
}

// load parses and checks the project. Paths in diagnostics are relative to
// the root, so the commands run with the root as working directory.
func (e *Env) load(bag *diag.Bag) *checker.Program {
	wd, _ := os.Getwd()
	if err := os.Chdir(e.Root); err != nil {
		bag.Errorf("E9999", ast.Pos{}, "internal: %v", err)
		return nil
	}
	defer os.Chdir(wd)
	in, err := modules.Load(".", bag)
	if err != nil {
		bag.Errorf("E9999", ast.Pos{}, "internal: %v", err)
		return nil
	}
	if bag.HasErrors() {
		return nil
	}
	prog := checker.Check(in, bag)
	if bag.HasErrors() {
		return nil
	}
	return prog
}

// exitFor is 1 when bag holds an error, 3 when one is internal.
func exitFor(bag *diag.Bag) int {
	for _, d := range bag.Diags {
		if d.Code == "E9999" {
			return ExitInternal
		}
	}
	if bag.HasErrors() {
		return ExitFail
	}
	return ExitOK
}

func (e *Env) genDir() string { return filepath.Join(e.Root, ".jip", "gen") }

// generate checks, applies the extern rules and writes .jip/gen.
func (e *Env) generate(bag *diag.Bag, opts emit.Options) *checker.Program {
	prog := e.load(bag)
	if prog == nil {
		return nil
	}
	emit.CheckExterns(prog, bag)
	if bag.HasErrors() {
		return nil
	}
	out, err := emit.Emit(prog, opts)
	if err != nil {
		bag.Errorf("E9999", ast.Pos{}, "internal: %v", err).Helpf("report this with `jip build --go-positions` output")
		return nil
	}
	if err := out.Write(e.genDir()); err != nil {
		bag.Errorf("E9999", ast.Pos{}, "internal: %v", err)
		return nil
	}
	return prog
}

// ---- the Go toolchain (spec 13 rule 11) ------------------------------------

// goTool finds the go command: JIP_GO, else `go` on PATH, at least the
// pinned version.
func goTool() (string, error) {
	g := os.Getenv("JIP_GO")
	if g == "" {
		p, err := exec.LookPath("go")
		if err != nil {
			return "", fmt.Errorf("no Go toolchain: install Go %s or set JIP_GO", compiler.GoVersion)
		}
		g = p
	}
	out, err := exec.Command(g, "env", "GOVERSION").Output()
	if err != nil {
		return "", fmt.Errorf("JIP_GO=%s: %v", g, err)
	}
	have := strings.TrimSpace(string(out))
	if !versionAtLeast(strings.TrimPrefix(have, "go"), compiler.GoVersion) {
		return "", fmt.Errorf("%s is %s; jip needs Go %s or newer", g, have, compiler.GoVersion)
	}
	return g, nil
}

func versionAtLeast(have, want string) bool {
	h, w := strings.Split(have, "."), strings.Split(want, ".")
	for i := 0; i < len(w); i++ {
		if i >= len(h) {
			return false
		}
		a, _ := strconv.Atoi(strings.TrimLeftFunc(h[i], func(r rune) bool { return r < '0' || r > '9' }))
		b, _ := strconv.Atoi(w[i])
		if a != b {
			return a > b
		}
	}
	return true
}

// goRun runs the go command in .jip/gen.
func (e *Env) goRun(env []string, stream func(line string), args ...string) (string, error) {
	g, err := goTool()
	if err != nil {
		return err.Error(), err
	}
	cmd := exec.Command(g, args...)
	cmd.Dir = e.genDir()
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=-mod=vendor", "GOPROXY=off")
	cmd.Env = append(cmd.Env, env...)
	var buf bytes.Buffer
	if stream == nil {
		cmd.Stdout, cmd.Stderr = &buf, &buf
		err := cmd.Run()
		return buf.String(), err
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			buf.WriteString(sc.Text() + "\n")
			stream(sc.Text())
		}
		close(done)
	}()
	err = cmd.Run()
	pw.Close()
	<-done
	return buf.String(), err
}

var goPosRe = regexp.MustCompile(`(?m)^(?:\./)?([^\s:]+\.jip):(\d+):(\d+): (.*)$`)
var goAnyPosRe = regexp.MustCompile(`(?m)^(?:\./)?([^\s:]+\.go):(\d+):(\d+): (.*)$`)

// goFailure turns Go toolchain output into E9999 (spec 13 rule 13): the
// //line-mapped .jip position, the Go message as a note.
func goFailure(bag *diag.Bag, out string) {
	pos := ast.Pos{}
	msg := strings.TrimSpace(out)
	if m := goPosRe.FindStringSubmatch(out); m != nil {
		l, _ := strconv.Atoi(m[2])
		c, _ := strconv.Atoi(m[3])
		pos = ast.Pos{File: m[1], Line: l, Col: c}
		msg = m[4]
	} else if m := goAnyPosRe.FindStringSubmatch(out); m != nil {
		msg = m[0]
	}
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	d := bag.Errorf("E9999", pos, "internal")
	d.Label("the generated Go does not build")
	d.Note("%s", msg)
	d.Helpf("report this with `jip build --go-positions` output")
}

// ---- jip build ------------------------------------------------------------

// BuildOptions are the flags of `jip build`.
type BuildOptions struct {
	Release     bool
	Target      string // GOOS/GOARCH
	GoPositions bool
}

// Build runs the pipeline and writes .jip/bin/<name>.
func (e *Env) Build(o BuildOptions) int {
	bag := diag.NewBag()
	prog := e.generate(bag, emit.Options{GoPositions: o.GoPositions, Release: o.Release})
	if prog == nil {
		e.report(bag, false)
		return exitFor(bag)
	}
	var env []string
	if o.Target != "" {
		goos, goarch, ok := strings.Cut(o.Target, "/")
		if !ok {
			fmt.Fprintf(e.Stderr, "error: --target wants GOOS/GOARCH, got %q\n", o.Target)
			return ExitUsage
		}
		env = append(env, "GOOS="+goos, "GOARCH="+goarch)
	}
	bin := filepath.Join(e.Root, ".jip", "bin", prog.AppName)
	args := []string{"build", "-trimpath", "-o", bin}
	if o.Release {
		args = append(args, "-ldflags=-s -w")
	}
	args = append(args, ".")
	if out, err := e.goRun(env, nil, args...); err != nil {
		goFailure(bag, out)
		e.report(bag, false)
		return ExitInternal
	}
	fmt.Fprintf(e.Stdout, "built %s\n", relTo(e.Root, bin))
	return ExitOK
}

func relTo(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return r
	}
	return p
}

// ---- jip test -------------------------------------------------------------

// TestOptions are the flags of `jip test`.
type TestOptions struct {
	Filter string
	Runs   int
	Seed   string
}

// Test runs every scenario under the fakes (spec 12, spec 13 rule 3).
func (e *Env) Test(o TestOptions) int {
	bag := diag.NewBag()
	prog := e.generate(bag, emit.Options{})
	if prog == nil {
		e.report(bag, false)
		return exitFor(bag)
	}
	env := []string{"JIP_SEED_DIR=" + filepath.Join(e.Root, ".jip", "seeds")}
	if o.Filter != "" {
		env = append(env, "JIP_SCENARIO_FILTER="+o.Filter)
	}
	if o.Runs > 0 {
		env = append(env, "JIP_RUNS="+strconv.Itoa(o.Runs))
	}
	if o.Seed != "" {
		env = append(env, "JIP_SEED="+o.Seed)
	}
	passed, failed, skipped := 0, 0, 0
	inFail := false
	stream := func(l string) {
		switch {
		case strings.HasPrefix(l, "@jip ok"):
			passed++
			inFail = false
			fmt.Fprintln(e.Stdout, strings.TrimPrefix(l, "@jip "))
		case strings.HasPrefix(l, "@jip FAIL"):
			failed++
			inFail = true
			fmt.Fprintln(e.Stdout, strings.TrimPrefix(l, "@jip "))
		case strings.HasPrefix(l, "@jip skip"), strings.HasPrefix(l, "@jip slow"):
			skipped++
			inFail = false
			fmt.Fprintln(e.Stdout, strings.TrimPrefix(l, "@jip "))
		case strings.HasPrefix(l, "@jip warn"):
			fmt.Fprintln(e.Stdout, "warning "+strings.TrimPrefix(l, "@jip warn "))
		case inFail && strings.HasPrefix(l, "    "):
			fmt.Fprintln(e.Stdout, l)
		default:
			inFail = false
		}
	}
	out, err := e.goRun(env, stream, "test", "-count=1", "-v", "./...")
	if err != nil && passed+failed+skipped == 0 {
		goFailure(bag, out)
		e.report(bag, false)
		return ExitInternal
	}
	fmt.Fprintf(e.Stdout, "%d scenarios: %d passed, %d failed, %d skipped\n", passed+failed+skipped, passed, failed, skipped)
	if failed > 0 || err != nil {
		return ExitFail
	}
	return ExitOK
}
