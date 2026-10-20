// Command jip is the jippitese toolchain (spec 13): new, build, test,
// check, fmt, context, fetch and lsp.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/codingsar/jippitese/compiler/driver"
	"github.com/codingsar/jippitese/compiler/iface"
	"github.com/codingsar/jippitese/compiler/lsp"
)

const usage = `usage: jip <command> [flags]

commands:
  new <name>        scaffold a project
  build             build .jip/bin/<name>   [--release] [--target GOOS/GOARCH] [--go-positions]
  test              run every scenario      [--filter s] [--runs n] [--seed n]
  check             check without building  [--write-interfaces] [--accept-breaking m] [--effective --env e] [--json]
  fmt               format every .jip file  [--check]
  context <module>  print the context pack  [--short] [--for file]
  fetch             vendor dependencies     [--save]
  lsp               language server on stdin and stdout
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return driver.ExitUsage
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet("jip "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "diagnostics as JSON Lines")
	all := fs.Bool("all", false, "show every diagnostic")
	env := func() (*driver.Env, int) {
		wd, _ := os.Getwd()
		e, err := driver.NewEnv(wd, stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return nil, driver.ExitUsage
		}
		e.JSON, e.All = *jsonOut, *all
		return e, 0
	}
	parse := func() bool { return fs.Parse(rest) == nil }
	switch cmd {
	case "new":
		if !parse() || fs.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: jip new <name>")
			return driver.ExitUsage
		}
		wd, _ := os.Getwd()
		if err := driver.New(wd, fs.Arg(0)); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return driver.ExitFail
		}
		fmt.Fprintf(stdout, "created %s\n", fs.Arg(0))
		return driver.ExitOK
	case "build":
		release := fs.Bool("release", false, "strip symbols")
		target := fs.String("target", "", "GOOS/GOARCH")
		gopos := fs.Bool("go-positions", false, "no //line directives")
		if !parse() {
			return driver.ExitUsage
		}
		e, code := env()
		if e == nil {
			return code
		}
		return e.Build(driver.BuildOptions{Release: *release, Target: *target, GoPositions: *gopos})
	case "test":
		filter := fs.String("filter", "", "scenario name or module substring")
		runs := fs.Int("runs", 0, "property runs")
		seed := fs.String("seed", "", "generator seed")
		if !parse() {
			return driver.ExitUsage
		}
		if fs.NArg() == 1 && *filter == "" {
			*filter = fs.Arg(0)
		}
		e, code := env()
		if e == nil {
			return code
		}
		return e.Test(driver.TestOptions{Filter: *filter, Runs: *runs, Seed: *seed})
	case "check":
		write := fs.Bool("write-interfaces", false, "rewrite interface.jipi files")
		var accept multi
		fs.Var(&accept, "accept-breaking", "accept a breaking change of a module")
		effective := fs.Bool("effective", false, "print the resolved config")
		envName := fs.String("env", "", "env block for --effective")
		if !parse() {
			return driver.ExitUsage
		}
		e, code := env()
		if e == nil {
			return code
		}
		return e.Check(driver.CheckOptions{WriteInterfaces: *write, AcceptBreaking: accept, Effective: *effective, EnvName: *envName})
	case "fmt":
		check := fs.Bool("check", false, "report files that would change")
		if !parse() {
			return driver.ExitUsage
		}
		e, code := env()
		if e == nil {
			return code
		}
		return e.Fmt(*check)
	case "context":
		short := fs.Bool("short", false, "one line per symbol")
		forFile := fs.String("for", "", "narrow to one file")
		// Flags may follow the module.
		var mod string
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			mod, rest = rest[0], rest[1:]
		}
		if !parse() {
			return driver.ExitUsage
		}
		if mod == "" && fs.NArg() == 1 {
			mod = fs.Arg(0)
		}
		if mod == "" {
			fmt.Fprintln(stderr, "usage: jip context <module> [--short] [--for file]")
			return driver.ExitUsage
		}
		e, code := env()
		if e == nil {
			return code
		}
		return e.Context(mod, iface.ContextOptions{Short: *short, For: *forFile})
	case "fetch":
		save := fs.Bool("save", false, "write computed hashes into app.jip")
		if !parse() {
			return driver.ExitUsage
		}
		e, code := env()
		if e == nil {
			return code
		}
		return e.Fetch(*save)
	case "lsp":
		fs.Bool("stdio", true, "serve over stdin and stdout (the only transport)")
		if !parse() {
			return driver.ExitUsage
		}
		return lsp.Run(os.Stdin, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return driver.ExitOK
	}
	fmt.Fprintf(stderr, "jip: unknown command %q\n\n%s", cmd, usage)
	return driver.ExitUsage
}
