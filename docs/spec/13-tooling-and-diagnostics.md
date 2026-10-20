# 13 — Tooling and diagnostics

One binary, `jip`. Every error names a file, a line, a column, what was
expected, what was found, and one edit that fixes it.

## Example

```text
$ jip new orders && cd orders
  app.jip
  orders/billing/charge.jip
  orders/billing/scenarios.jip
$ jip build
error[E0412]: missing effect `net`
  --> orders/billing/charge.jip:14:14
   |
14 |   let resp = http.post(gw.url, body)?
   |              ^^^^^^^^^ `http.post` has effect `net`
   = note: `charge` declares `with {nondet}`
   = help: change the signature to `-> Result<Receipt, BillError> with {net, nondet}`

orders/billing/charge.jip: 1 error
$ echo $?
1
```

Go equivalent: `go build` + `go vet` + `gofmt`. TypeScript equivalent:
`tsc` + `eslint` + `prettier`. The difference is that `jip build` never
shows a Go error and `jip fmt` has no configuration.

## Rules

### The CLI

1. `jip new <name>` scaffolds `<name>/app.jip` (name set, `contracts: pre`,
   `workflow { mode: embedded }`), one module `<name>/<name>/main.jip` with a
   `Service` impl and its `run` line, and `<name>/<name>/scenarios.jip`.
2. `jip build` runs the pipeline (rule 10) and writes the binary to
   `.jip/bin/<name>`. `--release` strips symbols and disables the Go race
   detector; the contract level stays what `app.jip` says (default `pre`).
   `--target GOOS/GOARCH` cross-compiles.
   `--go-positions` emits no `//line` directives, for compiler debugging
   only.
3. `jip test` runs every scenario (§12) under `contracts: all` with the
   fake clock and seeded generator installed (R14). `--filter <substring>`
   matches scenario names or module paths; `--runs <int>` sets the property
   run count; `--seed <int>` fixes the generator seed and is printed on
   every failure. It also runs the derived checks of §05 rules 14, 17 and 18
   (`derived …` lines).
4. `jip check` type-checks without emitting Go, then: extracts interfaces
   and reports drift and API classification (§09); prints warnings (rule 9);
   prints the extern report (§11); verifies vendored hashes (§10).
   `--write-interfaces` rewrites `interface.jipi` files. `--accept-breaking
   <module>` turns a `breaking` classification for that module into exit 0,
   as does the commit trailer `jip-breaking: <module>` on `HEAD` (§09 rule
   8, R8). `--effective --env <name>` prints the resolved config (§10).
   `--json` switches to JSON Lines.
5. `jip fmt` rewrites every `.jip` under the root to canonical form. Zero
   options. `--check` writes nothing and exits 1 if any file would change.
6. `jip context <module>` prints the context pack; `--short`, `--for <file>`
   as in §09.
7. `jip fetch` downloads and verifies every `deps {}` entry into `vendor/`;
   `--save` writes computed hashes into `app.jip`.
8. **Exit codes.** `0` success; `1` diagnostics with severity `error`, failed
   scenarios, drift, a breaking classification that is not accepted (rule
   4), an extern violation or `fmt --check` differences; `2` bad invocation;
   `3` internal compiler error (E9999).
9. **Warnings.** Unused effect, unused import, unused variable, unused
   `as` alias and declared-but-unreferenced `pub(module)` items are
   warnings. `jip check` prints them; `jip build` does not. Warnings never
   change an exit code.

### The build pipeline

10. **Phases.** parse → resolve (modules, `use`, visibility, cycles) →
    typecheck (types, effects, contracts purity, exhaustiveness) → lower
    (§02 ADT encodings, `?`, `match`, workflow steps, spawn scopes) → emit
    `gofmt`-ed Go under `.jip/gen/<module path>/` with one `//line
    <file>:<line>:<col>` directive per statement → generate `.jip/gen/go.mod`
    from `app.jip` (module `jip/<name>`, a `replace` line per `gomod(...)`
    dependency pointing into `vendor/`, a synthesised `vendor/modules.txt`,
    `toolchain go1.24.0`) → `go build -trimpath -mod=vendor`. Output is deterministic: the same sources and
    `app.jip` produce byte-identical Go.
11. **Toolchain.** The compiler pins a Go toolchain version; v0 pins 1.24
    (R10). The pin lives in `compiler/toolchain.go` and is bumped
    deliberately; the generated `go.mod` carries `toolchain go1.24.0`.
    `jip` runs `JIP_GO=<path>` if set, else `go` on `PATH`, and refuses a
    toolchain older than the pin; nothing else changes. A distribution may
    ship Go next to `jip` and point `JIP_GO` at it. The emitter never relies on
    generic methods: behaviours lower to dictionaries (§06 rule 15).
12. **Two caches.** The jippitese cache at `.jip/cache/<module>` is keyed on
    the module's source hash, its direct dependencies' interface digests and
    the `module-visible` digests of every module under its parent (§09 rule
    9): a hit skips typecheck, lower and emit for that module. Below it,
    `GOCACHE` keys on the emitted Go, so unchanged generated files cost
    nothing to rebuild.
13. **Never a Go error.** Every type, effect, contract and exhaustiveness
    error is produced by the jippitese checker before emission. If `go
    build` still fails, the emitter has a bug: the output is
    `error[E9999]: internal` with the `//line`-mapped `.jip` position, the
    Go message as a `note:`, and the `help:` "report this with `jip build
    --go-positions` output". No Go position reaches the user otherwise.

### Diagnostics format (normative)

14. **Shape.** The format shown in the example is fixed:
    `<severity>[<code>]: <message>`, an `-->` line with `file:line:col`, a
    gutter with the source line and `^` markers under the offending span
    with a label, zero or more `= note:` lines, and at least one `= help:`
    line proposing a concrete edit. `error` and `warning` are the only
    severities.
15. **Codes.** Every diagnostic has a stable code, `E` for errors and `W`
    for warnings, followed by four digits, never reused. Families: `E01xx` modules and names, `E02xx`
    interfaces, `E03xx` config and dependencies, `E04xx` types and effects,
    `E05xx` extern, `E06xx` contracts, `E07xx` behaviours and workflows,
    `E08xx` scenarios, `E9999` internal.
16. **Content.** The message states expected vs. found where a type, effect
    set or shape is involved. The `help:` edit is a replacement the agent
    can apply without further reading; when an edit spans the signature,
    the whole new signature is printed.
17. **Grouping.** Diagnostics are grouped per file in path order, then line
    order. At most 20 are shown; the remainder prints as
    `… and 7 more errors (run with --all)`. Each file ends with
    `<file>: N errors`.
18. **`--json`.** Every command emits one JSON object per diagnostic on
    stdout, no gutter art, same codes:

```text
{"code":"E0412","severity":"error","file":"orders/billing/charge.jip",
 "line":14,"col":14,"end_line":14,"end_col":23,
 "message":"missing effect `net`",
 "notes":["`charge` declares `with {nondet}`"],
 "help":[{"text":"change the signature to `-> Result<Receipt, BillError> with {net, nondet}`",
          "edit":{"file":"orders/billing/charge.jip",
                  "range":{"line":12,"col":44,"end_line":12,"end_col":57},
                  "replacement":"with {net, nondet}"}}]}
```

    Schema: `code: Str`, `severity: "error" | "warning"`, `file: Str`,
    `line, col, end_line, end_col: Int` (1-based, `end_col` exclusive),
    `message: Str`, `notes: List<Str>`, `help: List<{text: Str, edit?:
    {file: Str, range: {line, col, end_line, end_col}, replacement: Str}}>`.
    A summary object `{"summary":true,"errors":N,"warnings":M}` ends the
    stream.

### Runtime panics

19. A panic (contract violation in a plain function, §05 rule 10; `inv`
    failure at construction; extern panic; runtime bug) prints the reason, then frames mapped through `//line` to `.jip`
    positions, innermost first, then exits 101:

```text
panic: pre violated: `amt.0 > 0` (orders/billing/charge.jip:35:8)
  orders.billing.withdraw          orders/billing/charge.jip:34:1
  orders.billing.charge            orders/billing/charge.jip:52:11
  orders.billing.BillingApi.handle orders/billing/api.jip:20:5
  [runtime] Service BillingApi      (child spec app.jip:24, restart: permanent, 1 of 3)
```

    Go frames of the runtime and of extern packages appear only below a
    `[go]` separator and only with `JIP_TRACE=go`.

### `jip fmt` canonical form

20. Two-space indent; one statement per line, no `;`; `module` line, blank
    line, `use` lines sorted (`std.*` first, then aliases, then app modules),
    blank line, items separated by one blank line; `pub` items ordered as the
    interface file (§09 rule 7), private items after; `with {…}` effects
    sorted in the fixed label order `io, net, mut, llm, decide, nondet,
    spawn, approve`; `pre`/`post`/`inv` on their own lines indented two
    spaces; trailing comma in every multi-line list, record and match;
    `return x` as a tail expression rewritten to `x`; single quotes never
    used for strings; spaces around binary operators, none inside brackets;
    line width is not enforced.

## Diagnostics

```text
error[E9999]: internal: emitted Go did not compile
  --> orders/billing/charge.jip:48:3
   |
48 |   while attempts < 3 && resp.status == 503 { attempts += 1 }
   |   ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ statement whose Go output failed
   = note: go: .jip/gen/orders/billing/charge.go:112:9: attempts declared and not used
   = help: this is a compiler bug; report it with the output of `jip build --go-positions`
```

```text
warning[W0401]: unused effect `io`
  --> orders/billing/charge.jip:43:74
   |
43 | pub fn charge(o: Order, gw: Gateway) -> Result<Receipt, BillError> with {io, net, nondet} {
   |                                                                          ^^ declared, never used in the body
   = help: change the signature to `-> Result<Receipt, BillError> with {net, nondet}`
```

## Grammar

No source grammar in this section. The `--json` schema above is the
normative machine format; the human format is defined by rule 14.
