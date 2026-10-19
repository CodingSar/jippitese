# compiler

The v0 jippitese compiler and the `jip` command (spec 13). Go 1.24, module
`github.com/codingsar/jippitese/compiler`, no dependencies outside the Go
standard library.

```text
go build -o bin/jip ./cmd/jip     # the command
go test ./...                     # every package, the card and the examples
```

`go test ./driver -run TestExamples` is the Phase E gate over `examples/`:
every project formatted, checked, built, scenarios passing.

## Packages

| Package | Role |
|---|---|
| `lexer`, `parser`, `ast` | Spec 01 tokens, the grammar, the syntax tree and its walkers |
| `diag` | Diagnostics: codes, the gutter format of 13 rules 14–17, `--json` |
| `app` | `app.jip`: config, secrets, deps, models, externs, contracts, run lines, topics, env blocks |
| `modules` | Finds the root, loads every module, scenario module and vendored dependency (E0101, E0106) |
| `stdlib` | The standard library `.jip` sources, embedded |
| `checker` | Names, types, effects, contracts, behaviours and budgets, workflows, scenarios; produces the side tables the emitter reads |
| `rt` | The runtime every program links: prelude types, std externs, behaviour runners, journal and replay, config overlay, fakes, scenario harness. Copied into each build. |
| `emit` | Lowers a checked program to Go; extern rules E0501–E0508 |
| `format` | Canonical printing: single-line expressions for interfaces and diagnostics, whole files with comments for `jip fmt` |
| `iface` | Interface files, digests, API classification, context packs (spec 09) |
| `driver` | The `jip` commands: build, test, check, fmt, context, fetch, new |
| `cmd/jip` | Flag parsing; calls `driver` |
| `toolchain.go` | The Go toolchain pin |

## Pipeline

`jip build` and `jip test` run parse → resolve → check → extern rules →
emit → `go build -trimpath -mod=vendor` (or `go test`). The generated tree
under `.jip/gen/` is deterministic:

```text
.jip/gen/
  go.mod                 module jip/<name>, go 1.24, toolchain go1.24.0
  main.go                config load, contract level, rt.Serve over the run lines
  rt/                    the runtime sources
  appcfg/appcfg.go       Config and Secrets, the overlay loader, run options
  <module/path>/module.go            one package per module, //line per statement
  <module/path>/scenarios_test.go    the module's scenarios as Go tests
  deps/<alias>/…         vendored jippitese dependencies
```

A Go build failure is a compiler bug and is reported as `error[E9999]` at
the `//line`-mapped `.jip` position. `--go-positions` drops the `//line`
directives for debugging the emitter.
