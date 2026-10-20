# 11 — Extern (Go interop)

`extern` binds a jippitese signature to a Go function. The declaration is
the trust point: it carries the full signature, an explicit effect set, a
doc line and a scenario, and it is allowed only where `app.jip` says so.

## Example

`orders/pdf/render.jip`, a module listed in `externs { allow: [orders.pdf] }`:

```jip
module orders.pdf
use std.gen

/// Renders an invoice to PDF bytes with the vendored gopdf library.
extern fn render(html: Str, opts: Map<Str, Str>) -> Result<Bytes, GoError> with {io}
  = go "github.com/acme/gopdf".Render

/// Returns the page count of a PDF, None when the bytes are not a PDF.
extern fn pages(doc: Bytes) -> Option<Int> = go "github.com/acme/gopdf".Pages

/// Pure hashing routine; the package touches no I/O, clock or shared state.
/// why: xxhash is a pure function of its input.
unsafe pure extern fn fingerprint(doc: Bytes) -> Int = go "github.com/cespare/xxhash/v2".Sum64

/// Generator for `any Bytes via non_pdf`: bytes that never start with the PDF magic.
pub fn non_pdf(g: Gen) -> Bytes { b"junk-".concat(g.bytes(0, 64)) }
```

`orders/pdf/scenarios.jip`:

```jip
module orders.pdf.scenarios
use orders.pdf

@extern
scenario "render produces a PDF header" {
  given html = "<h1>Invoice</h1>"
  when  r = pdf.render(html, Map.of())
  then  match r { Ok(b) => b.starts_with(b"%PDF-"), Err(_) => false }
}

@extern
scenario "pages rejects non-PDF bytes" {
  given junk = any Bytes via non_pdf              // named generator (§12 rule 4)
  when  n = pdf.pages(junk)
  then  n == None
}
```

`app.jip` pins the Go module like any dependency:

```jip
deps { pdf = gomod("github.com/acme/gopdf", commit: "c0ffee1", hash: "sha256-Xk…") }
externs { allow: [orders.pdf], max: 10 }
```

Go side, as emitted under `.jip/gen/` (conversions inline, no reflection;
the Go package is imported under an alias derived from its path):

```go
//line orders/pdf/render.jip:4:1
func Render(ctx *rt.Ctx, html string, opts rt.Map[string, string]) rt.Result[[]byte, rt.GoError] {
	v, err := x_github_com_acme_gopdf.Render(html, rt.MapToGo(opts))
	if err != nil {
		return rt.Result_Err[[]byte, rt.GoError](rt.GoErrorOf(err))
	}
	return rt.Result_Ok[[]byte, rt.GoError](v)
}
```

An `Option<T>` result reads `v, ok := …` and returns `rt.Option_None` or
`rt.Option_Some`; a newtype argument converts to its inner Go type
(`string(p)` for a `Path`); `IoError` and `NetError` results map the Go
error through `rt.IoErrorOf` and `rt.NetErrorOf`.

TypeScript equivalent: a hand-written `.d.ts` over a JS module, except that
here the compiler checks the declared effects against the Go import path.

## Rules

1. **Form.** `extern fn name(params) -> Ret [with {effects}] = go "<import path>".Ident`.
   v0 binds package-level functions only; `go "<path>".Type.Method` is a
   compile error (E0503). The import path is a Go standard-library path or
   the module path of a `gomod(...)` entry in `deps {}` (E0504 otherwise).
   The path `"rt"` names the toolchain's own runtime package, shipped with
   `jip` and copied into every build: it needs no `gomod` entry, rules 3
   to 8 do not apply to it, and a generic `rt` function is instantiated
   from the declared signature (a `Result<R, E>` result instantiates `R`
   and `E`). The stdlib and vendored dependencies bind `rt` this way.
2. **Explicit effects.** The effect set is written or the extern is pure.
   Effects are never inferred from the Go side; the Go side is only used to
   check a floor (rule 5).
3. **Signature types.** Only jippitese types appear. No Go type, pointer,
   interface, channel, struct or `context.Context` may be named. The boundary mapping table:

   | jippitese | Go |
   |---|---|
   | `Int` | `int64` |
   | `Float` | `float64` |
   | `Bool` | `bool` |
   | `Str` | `string` |
   | `Bytes` | `[]byte` |
   | `Duration` | `time.Duration` |
   | `()` | no return value |
   | newtype over a mappable `T` (`Path`, `Url`, `Money`) | the mapping of `T` |
   | `List<T>` | `[]T` |
   | `Map<K, V>` | `map[K]V` |
   | `Option<T>` | `(T, bool)` |
   | `Result<T, GoError>` | `(T, error)` |
   | `Result<(), GoError>` | `error` |
   | `Result<T, IoError>`, `Result<T, NetError>` | `(T, error)` |

   `GoError(Str)` is a prelude newtype holding `err.Error()`. Any extern,
   in any module, may use `GoError`, `IoError` or `NetError` as its error
   type; the mapping from Go's `error` to each is built into the compiler
   (`rt.GoErrorOf`, `rt.IoErrorOf`, `rt.NetErrorOf`: `fs.ErrNotExist` is
   `NotFound`, `fs.ErrPermission` is `Permission`, a timeout is
   `Timeout`, anything else `Other` with the message). The card's
   `read_file` returns `IoError`. A
   parameter or return type outside the table is E0505. A Go function whose
   real signature does not match the mapped one is E0506, reported with
   both signatures.
4. **Where.** Stdlib modules may declare `extern` freely. An app or vendored
   module may declare `extern` only if its path is in `app.jip`
   `externs.allow`; the total count across app and vendored modules may not
   exceed `externs.max`. An unlisted extern is a compile error (E0502), not
   a warning. A vendored dependency's own `externs.allow` is honoured only
   for its own modules and still counts against the root's `max`.
5. **Effect floor.** The compiler ships a table from Go import path to the
   minimum effect set:

   | Go package | floor |
   |---|---|
   | `os`, `io`, `bufio`, `path/filepath`, `io/fs` | `io` |
   | `net`, `net/http`, `net/*` | `net` |
   | `time`, `math/rand`, `math/rand/v2`, `crypto/rand` | `nondet` |
   | `sync`, `sync/atomic` | `mut` |
   | `os/exec` | `io` |

   The floor of an extern is the table entry of the target function's own
   package (`net/*` matches every package under `net/`); a package not in
   the table has floor `{}`. The package's imports are not unioned in:
   `os` itself imports `time` and `sync`, and a union would put every
   file extern at `{io, nondet, mut}`. Nothing is resolved transitively and
   no goroutine analysis is attempted (R13). `spawn` is never inferred: an extern that
   wants it writes it. A target package that imports `context` is a compile
   error (E0508): externs receive no context and are never interrupted
   (rule 9). A declaration whose set is not a superset of the floor is a
   compile error (E0501). A declaration above the floor is allowed.
6. **`unsafe pure`.** A pure extern (no `with`) whose Go package is not in
   the table is E0501 unless prefixed `unsafe pure` and followed by a second
   `///` line starting `why:`. `unsafe pure` on a package that is in the
   table is E0501 regardless. `jip check` lists every `unsafe pure` extern.
7. **Doc and scenario.** Every extern has a `///` line (E0105) and at least
   one scenario tagged `@extern` in the module's `scenarios.jip` that calls it
   (E0507, reported by `jip check` and `jip test`).
8. **Pinning.** The Go module of a non-stdlib import path is a `gomod(...)`
   entry in `deps {}` with `commit` and `hash` (§10); it is vendored under
   `vendor/<alias>` and reached from the generated `go.mod` by a `replace`
   line. One commit per Go module path graph-wide, as for `git(...)`.
9. **Codegen.** The generated Go calls the target directly with inline
   conversions; no reflection, no wrapper interface, no closure per call.
   The Go function receives no `context` and is never interrupted
   (section 08 rule 6); cancellation is observed at the next effectful call
   after it returns. A panic inside the Go function becomes a jippitese
   panic (§08): the trace is `//line`-mapped so the first frame names the
   `extern` line in `.jip`, followed by the Go frames of the external package.
10. **Interface files.** An extern appears in `interface.jipi` as an
    ordinary `pub fn` entry when it is `pub`; the `= go …` target is omitted,
    so no Go path reaches a dependent module's context pack.

### `jip check` extern report

```text
externs: 3 declared in app/vendored modules (max 10), 118 in stdlib
  module        fn           go                                        effects
  orders.pdf    render       "github.com/acme/gopdf".Render            {io}
  orders.pdf    pages        "github.com/acme/gopdf".Pages              {}
  orders.pdf    fingerprint  "github.com/cespare/xxhash/v2".Sum64       {} unsafe pure
violations: 1
  orders/pdf/render.jip:12:1  `fingerprint` has no @extern scenario
ratio (extern / pub fn):
  std.fs        14 / 36   0.39   budget 0.40
  std.net       22 / 51   0.43   budget 0.40  over
  orders.pdf     3 / 3    1.00
backend surface: 121 functions over 19 Go packages
  "os": ReadFile, WriteFile, Open, Stat, Remove, MkdirAll
  "net": Dial, Listen
  …
  "github.com/acme/gopdf": Render, Pages
```

`jip check` exits non-zero on any violation and on a stdlib module over
budget.

## Diagnostics

```text
error[E0501]: extern below the effect floor
  --> std/fs/leaf.jip:12:1
   |
12 | extern fn read_file(p: Path) -> Result<Bytes, IoError> = go "os".ReadFile
   | ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ declared pure
   = note: `"os".ReadFile` needs `io` (floor table: package `os`)
   = help: change the signature to `-> Result<Bytes, IoError> with {io}`
```

```text
error[E0502]: extern in a module that is not allow-listed
  --> orders/customers/leaf.jip:40:1
   |
40 | extern fn read_file(p: Path) -> Result<Bytes, GoError> with {io} = go "os".ReadFile
   | ^^^^^^ `orders.customers` is not in `app.jip` externs.allow
   = note: externs.allow = [orders.pdf], 3 of max 10 used
   = help: either add `orders.customers` to `externs { allow: [...] }` in app.jip, or call `fs.read_file` from `std.fs`
```

## Grammar

```text
ExternDecl  ::= DocLine WhyLine? ("unsafe" "pure")? "pub"? "extern" "fn" Ident
                "(" Params? ")" "->" Type EffectSet? "=" GoTarget NEWLINE
WhyLine     ::= "///" "why:" Text NEWLINE
GoTarget    ::= "go" Str "." GoIdent
GoIdent     ::= [A-Z] [A-Za-z0-9_]*
EffectSet   ::= "with" "{" (Effect ("," Effect)*)? "}"
```
