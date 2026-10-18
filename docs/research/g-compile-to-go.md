# (g) Compile-to-Go precedents

Status: research output for `docs/decisions.md` §2, §3, §7, §8, §12. Primary
sources read where the sandbox allowed (GitHub raw files, pkg.go.dev, Go
issues); `gleam.run`, `templ.guide` and `jerf.org` were unreachable and are
cited via search snippets.

## Summary

- **Nobody has measured overhead vs. hand-written Go.** Borgo, XGo, Dingo and
  templ publish no benchmarks (Dingo's "zero overhead" is asserted). The
  measurable costs are Go's own: GC-shape generics (dictionary calls, no
  inlining through type parameters), interface-encoded sums (heap alloc per
  boxing), persistent maps (~1.1–1.5× lookup, ~2× insert vs. built-in `map`
  in the only Go HAMT benchmark found).
- **Two ADT encodings in practice:** one flattened tagged struct (Borgo,
  Dingo) or an interface with a struct per variant (idiomatic Go, needs
  `go-sumtype` for exhaustiveness). No system lowers `match` to a `switch`;
  Borgo emits if-chains with a status integer.
- **Source positions:** only XGo (via `gogen`) emits `//line file:line:col`
  per statement. templ rejected `//line` (PR #476: it rewrites runtime stack
  traces); gopls dropped `//line` support (#55043; #65001 still open). Go 1.27
  fixes relative `//line` paths (#70478).
- **Build pipeline:** everyone shells out to `go build`. XGo wraps the whole
  `go` CLI; templ and Borgo leave `go.mod` to the user. Gleam, the only one
  with its own build tool, caches a per-module interface and recompiles by
  topological staleness, the same shape as jippitese's interface files (§9).
- **Survival:** single-author compile-to-Go languages die (Oden 2017, Fo
  archived, Have dormant, Borgo silent since Oct 2024). Survivors are Go
  supersets (XGo, templ) that reuse `go/parser`, `go/types` and Go modules.

## Findings

**Borgo** (Rust, `.brg` → `.go`). Enum → `type FooTag int` + `iota` + one
generic struct `Foo[T] { tag FooTag; A T; B0 int; B1 string }` holding all
variants' fields; constructors `make_Foo_B[T](...)`. `match` → chain of `if`
blocks guarded by a status int, recursing into `subject.Con0`. Go `(T, bool)`
/ `(T, error)` returns surface as `Option`/`Result` through an
immediately-invoked closure per call; `?` → `if err != nil { return *new(T),
err }`. Methods drop their own type parameters; generic collection methods
become free functions; tuples are `unreachable!()`. No `//line`, manual
`go mod init`, `go run .`. Last commit Oct 2024.

**XGo / Go+** (Go superset). Go and XGo files share a package; `xgo go`
"converts XGo packages into Go packages"; `xgo build/run/mod/install` wrap the
toolchain with `go`, `llgo` or `tinygo` backends. Code is built with `gogen`,
a builder API that **type-checks each generated expression** and attaches
`//line source.spx:10:1` per statement (`line_directive_test.go` asserts
`go/parser` reports the `.spx` file, line and column).

**templ** (Go). `.templ` → `_templ.go`, plain `go build`. Source map kept in
memory for an LSP proxying gopls; maintainer: emitting `//line` "would be
easy" (#65001). PR #476 was closed: "the directive changes runtime debug
info", panic traces would mislead. CLI: `-watch`, `-lazy`, `-cmd`,
`-source-map-visualisations`. Compile errors point into `_templ.go`.

**Gleam** (Rust; Erlang + JS). One target-agnostic typed `Module` AST;
`codegen.rs` has an `Erlang` renderer (`.erl`/binary `.abstr`, `.hrl`, `.app`)
and a `JavaScript` renderer (`.mjs`, `.d.mts`, `.mjs.map`, prelude
`gleam.mjs`). Custom types: tagged tuples/atoms on Erlang, `class X extends
CustomType` on JS. Per-target code: stacked `@external(erlang|javascript,
mod, fn)` with optional Gleam fallback; target support tracked per expression
since v0.34. Build: per-module `.cache` (encoded interface) + `.cache_meta`;
mtime+hash staleness; a `StaleTracker` recompiles dependents in topological
order; failed modules lose their cache so they retry.

**Dingo** (Go, 0.1.0-alpha). Regex preprocessor → `go/parser` → plugin
passes → `go/printer`. `Option[T]` is `struct{ value *T; isSet bool }`; enums
are a `uint8` tag + struct with pointer fields + `IsX()` methods; `?` → `if
err != nil` with IIFE lifting in expression position. Post-AST source maps
feed an LSP proxy (self-reported 98.7–100%); no `//line`. `dingo
build/run/go`, incremental cache.

**Others.** Oden (Haskell → Go, dead 2017); Have (dormant); Fo (generics
transpiler, archived after Go 1.18). `go-sumtype`/`go-check-sumtype` exist
because Go cannot check exhaustiveness over interface sums.

**Go facts that bound the design.** `//line file:line:col` and `/*line*/`
forms; relative paths now resolve against the generated file's directory (Go
1.27, plus `-line-trimpath`). Generics use GC-shape stenciling: pointer-shaped
instantiations share one body and call methods through a runtime dictionary,
so no devirtualisation or inlining (PlanetScale). Generic methods approved
March 2026 (#77273); interfaces still cannot carry them. Boxing a non-pointer
value in an interface allocates; tagged structs grow to the sum of variants.
`go build -overlay` substitutes file contents unwritten (ignored by `go
run`/`go test`); `-trimpath`; `GOOS`/`GOARCH`; `GOCACHE` is content-addressed,
so deterministic output gives free incremental builds.

## Trade-offs

| Choice | For | Against |
|---|---|---|
| Tagged struct per enum | no alloc, value semantics, `switch tag` jump table | size = sum of variants; recursion needs pointers anyway |
| Sealed interface + struct per variant | compact, recursive-friendly, Go-interop idiom | heap alloc per construction, pointer chase |
| Go generics 1:1 | small output, fast builds | dictionary calls on hot paths; no generic interface methods |
| Compiler-side monomorphisation | Go-class speed guaranteed | code bloat, slower `go build`, must be deterministic |
| `//line` directives | `go build`/`vet`/panics name `.jip` lines | gopls ignores them; traces lose Go positions |
| Own build tool over `go build` | one CLI, vendored deps, pinned toolchain | must generate `go.mod`/`go.sum`; `-overlay` unusable for run/test |

## Recommendations for jippitese

1. **ADTs: hybrid lowering chosen by the compiler.** Flattened tagged struct
   (`tag uint8` + fields) when ≤ ~64 bytes and non-recursive; otherwise a
   sealed interface (unexported marker method, struct per variant). `match` →
   `switch v.tag` / type switch with **no `default`** (exhaustiveness is
   checked upstream); never Borgo's if-chains. `Option<T>` → value struct
   `{v T; ok bool}` (no pointer, no alloc; Dingo's `*T` is a mistake).
   `Result<T,E>` → `{ok bool; v T; e E}`; `?` → `if !r.ok { return
   Result[T,E]{e: r.e} }`. At `extern` boundaries adopt Borgo's rule, inlined
   rather than via IIFEs: `(T, bool)` ↔ `Option`, `(T, error)` ↔
   `Result<T, error>`.
2. **Generics: Go generics by default, monomorphise selectively.** Type
   parameters 1:1; behaviours become Go interfaces; associated types become
   extra type parameters. The compiler sees the whole program, so specialise
   stdlib collection operations on primitive element types (where dictionary
   cost bites) and keep dictionaries elsewhere. Lift generic methods to free
   functions (Borgo) unless Go ≥ 1.27 is required.
3. **Diagnostics: never show a Go error.** All type errors come from the
   jippitese checker, and the emitter should self-check like `gogen`. Emit
   `//line <path relative to generated file>:L:C` per statement so any leaked
   `go build`/`vet` error and every panic trace names `.jip` positions.
   templ's objection does not apply: nobody edits the Go. Keep `jip build
   --go-positions` for compiler debugging and a structured source-map file
   for the deferred LSP.
4. **Build: own the module.** `jip build` writes a deterministic, `gofmt`-ed
   Go module under `.jip/gen/`, generates `go.mod`/`go.sum` from `app.jip`
   (vendored git deps via `replace`, pinned `toolchain`), then `go build
   -trimpath -mod=vendor`; `GOOS`/`GOARCH` for cross-compilation. Two cache
   layers: `GOCACHE` for Go, and a Gleam-style per-module cache keyed by
   source hash + dependency interface hashes — the §9 interface file is that
   key. Use `-overlay` only for `jip check`. Embed a pinned Go toolchain.
5. **Effects: erase, plus one hidden parameter.** Effects are static metadata
   in interface files; codegen emits no effect machinery. Every effectful
   function takes a leading `ctx context.Context` carrying cancellation and
   app-configured instrumentation; `telemetry: otel` makes the compiler wrap
   calls at each effect's emission site, so uninstrumented builds pay one
   pointer argument. `spawn` → an `errgroup`-shaped scope in the jippitese
   runtime that joins/cancels children and re-panics.
6. **Persistent collections: own stdlib, builders mandatory.** HAMT map/set
   and a chunked/RRB vector as Go generics over value types, with transient
   builders (benbjohnson/immutable: ~10× for bulk builds). Mutable variants
   lower to native slice/map.

**Decisions reopened.** §7 "persistent by default": ~2× insert cost on maps;
keep the default, but the §13 eval must benchmark persistent vs. mutable so
the stdlib can use native types where no sharing escapes. §12 `extern`: fix
the `(T,bool)`/`(T,error)` wrapping rule now, before track (i) decides gating.
§15: record a pinned minimum Go version (1.27 if generic methods are wanted).

## Sources

- Borgo codegen: https://raw.githubusercontent.com/borgo-lang/borgo/main/compiler/src/codegen.rs ; repo/commits: https://github.com/borgo-lang/borgo , https://github.com/borgo-lang/borgo/commits/main ; https://www.infoq.com/news/2024/05/borgo-adds-types-to-go
- XGo: https://github.com/goplus/xgo , https://raw.githubusercontent.com/goplus/xgo/main/doc/docs.md ; gogen: https://github.com/goplus/gogen , https://raw.githubusercontent.com/goplus/gogen/main/line_directive_test.go
- templ: https://github.com/a-h/templ , https://github.com/a-h/templ/pull/476 , https://raw.githubusercontent.com/a-h/templ/main/docs/docs/09-developer-tools/01-cli.md ; gopls: https://github.com/golang/go/issues/65001
- Gleam: https://raw.githubusercontent.com/gleam-lang/gleam/main/compiler-core/src/codegen.rs , https://raw.githubusercontent.com/gleam-lang/gleam/main/compiler-core/src/build/package_loader.rs , https://raw.githubusercontent.com/gleam-lang/gleam/main/compiler-core/src/javascript/typescript.rs , https://gleam.run/documentation/externals/ , https://gleam.run/news/v0.34-multi-target-projects , https://gleam.run/news/v0.26-incremental-compilation-and-deno/ , https://gleam.run/cheatsheets/gleam-for-erlang-users
- Dingo: https://github.com/MadAppGang/dingo
- Oden: https://www.infoworld.com/article/2246666/oden-uses-googles-golang-ecosystem-to-cook-up-a-new-language.html ; Have: https://github.com/vrok/have ; Fo: https://github.com/albrow/fo
- Go `//line`: https://pkg.go.dev/cmd/compile ; relative paths: https://github.com/golang/go/issues/70478 ; `-overlay`/`-trimpath`/cache: https://pkg.go.dev/cmd/go
- Generics cost: https://www.plushcap.com/content/planetscale/blog/generics-can-make-your-go-code-slower , https://go.googlesource.com/proposal/+/HEAD/design/generics-implementation-gcshape.md ; generic methods: https://github.com/golang/go/issues/77273
- Sum-type encodings: https://jerf.org/iri/post/2917/ , https://github.com/alecthomas/go-check-sumtype
- Persistent collections: https://github.com/rsms/go-immutable , https://raw.githubusercontent.com/benbjohnson/immutable/master/README.md
