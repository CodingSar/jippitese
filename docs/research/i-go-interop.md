# (i) Go interop downsides — `extern` gating

Status: research output for decisions.md §12 (deferred). Counts marked *measured* were taken from upstream repos on 2026-10-18 (gleam-lang/stdlib `main`, koka-lang/koka `master`).

## Summary

Languages that kept FFI honest made the **declaration** the trust point (Rust `unsafe extern`, Haskell pure imports, Koka `extern : effect`), kept it **out of application code** (Elm kernel-only, Unison no-FFI), and kept the primitive surface **small and listed** (Gleam: 134 externs under 353 public fns). Drift happened where interop was cheaper than the native idiom and nobody counted it (Kotlin platform types, Clojure "Java with Lisp syntax", TypeScript `any` — which agents use 9x more than humans). Backend ports cost roughly the extern surface plus every signature that leaked a host type. Recommendation: keep `extern`, stdlib-only by default, app code only via an `app.jip` allowlist, mandatory effect signature checked against a compiler-computed effect floor per Go import, and `jip check` reporting the surface as a number.

## Findings

**Q1 — FFI-with-guarantees precedents.**
- *Haskell*: a `foreign import` with a non-`IO` type carries "the same proof obligations as `unsafePerformIO`" (FFI spec). Safe Haskell turns this into policy: `-XSafe` modules may not use `unsafePerformIO` or pure FFI imports; `Trustworthy` is an author's *claim*, flagged if GHC could have inferred `Safe`.
- *Rust*: RFC 3484 (edition 2024) makes `extern` blocks `unsafe extern` because "the compiler cannot itself verify these assertions"; the obligation sits with the declarer, and a `safe fn` inside is callable from safe code with no caller-side proof. Developers keep unsafe "minimal, encapsulated, well documented" but "rarely audited their dependencies" (McCormack et al.); most Rust CVEs are soundness bugs in safe abstractions, not in callers.
- *Koka*: externs carry the full effect type (`extern os-get-env() : ndet vector<string>`, `extern xprintsln(s) : console ()`), with per-backend inline bodies (`c`/`js`/`cs`). The effect-stripping casts are themselves externs with `_Unsafe_` doc lines (`unsafe-total-cast`, `unsafe-no-ndet-cast`), and a source comment notes one variant "can segfault". *Measured*: 469 `extern` vs 1511 `fun` declarations in `lib/std` (217 vs 417 in `std/core`) — Koka's stdlib is ~25% extern.
- *Unison*: no FFI at all; the outside world is reachable only through the runtime-provided `IO` ability (or sockets). Planned design: a foreign API is exposed as a top-level ability (`'{GPU} ()`) that the runtime handles — i.e. foreign code enters only as an *effect*.
- *Elm 0.19*: kernel JS restricted to `elm/*` and `elm-explorations/*`; packages must be pure Elm. Rationale: optimisation, "if it compiles it works", portability to Wasm/servers. The earlier review committee for native packages was judged "a failure overall" — mostly "bindings to whatever.js". Cost: Humio's 80k-line app took ~2.2 developer-weeks; "Elm 0.19 broke us" captured the anger; NoRedInk found ports + custom elements "also fine".

**Q2 — Drift case studies.**
- *Kotlin/Java*: platform types were an explicit relaxation ("we lost some type-safety") so Java null semantics enter Kotlin; `kotlin.collections.List` compiles to `java.util.List`. KMP migrations then had to rip out `java.io`, `java.time.Duration`, Kryo, `ProcessBuilder` and JVM types in public APIs.
- *Scala/Java*: no fixed idiom — codebases range from "readable Java with classes" to fully monadic; reflection/classloaders made libraries unportable, so Scala.js *forked* Scalaz/Shapeless and reimplemented a JDK subset in Scala.
- *Clojure/Java*: idiom is to call Java directly ("no wrappers that need to be maintained"); the community admits interop "can be a little too easy" and fears "Java with lisp syntax". Honest only because the JVM is the sole target.
- *Elixir/Erlang*: docs "discourage simply wrapping Erlang libraries"; direct `:ets`/`:timer` calls are idiomatic, yet Elixir wrote native `Enum`/`String` where semantics differ (protocols, UTF-8 vs bytes, zero indexing). No drift cost because there is one runtime — jippitese's situation until a second backend exists.
- *TypeScript `any`*: ~1 `any` per 100 LOC across 305 projects (MSR 2022); MSR 2026 finds AI agents use `any` **9x more** than humans and bypass type checks more often, while their PRs are accepted 1.8x more. An agent-authored codebase will take any escape hatch the compiler permits.
- *Gleam `@external`*: docs say use sparingly, the compiler "cannot verify foreign code", externals can break dual-target builds. *Measured*: 134 extern functions under 353 `pub fn`; 84 for both targets, 36 Erlang-only, 14 JS-only, 37 with pure-Gleam fallback (`list.reverse` → `lists:reverse` as optimisation only). Host code: 546 lines `.erl` + 1120 lines `.mjs`.

**Q3 — Stdlib strategy.** Thin-over-host (Koka ~25% extern, Kotlin mapped types, Borgo `use fmt` with `strconv.Atoi` adapted to `Option<int>`) gives instant breadth and no wrapper maintenance, but imports host semantics and types, and reading it means reading Go. Native-with-leaf-primitives (Gleam, Elixir `Enum`, Scala.js javalib) keeps the interface pack self-describing. For "tokens to read" the extern *body* is hidden behind the interface file either way; the real cost is whether the *signature* mentions Go types.

**Q4 — Backend lock-in cost.** Gleam added JS in v0.16, spent v0.17 reworking stdlib/http for both targets and moving Erlang-only modules to `gleam_erlang`, and v0.34 adding per-expression target tracking. Kotlin needed `expect/actual`, typealiases and years of library ports; Scala.js needed a JDK reimplementation and forks. Port cost ≈ (extern primitives) + (public signatures leaking a host type) + (libraries that bypassed the stdlib). Only the first term is bounded and measurable. Borgo's full Go-package compatibility is total lock-in by design.

## Trade-offs

- **Stdlib-only extern** (Elm): maximal guarantees and portability; blocks any Go library not yet wrapped. Elm shows the community cost is real but survivable when the escape is principled.
- **App-code extern behind allowlist** (Safe Haskell `Trustworthy`): unblocks Go libraries with one reviewable list; guarantees rest on claims `jip check` can only partially validate.
- **Open extern** (Clojure/Borgo): fastest ecosystem access; the TypeScript-agent evidence says drift is near-certain and the backend is permanently Go.
- **Effect annotations on externs are assertions** (Rust `safe fn` in `unsafe extern`): honest only if rare, listed, and floored mechanically.

## Recommendations for jippitese

1. **Keep `extern`; make the declaration the trust point.** Syntax carries a full signature with effects and a Go target: `extern fn read_file(p: Path) -> Result<Bytes, IoError> / io = go "os".ReadFile`. Only jippitese types in the signature; no `go.Value`, no Go pointers, no Go interfaces leaking into interface files.
2. **Where:** stdlib by default. App or vendored code may declare `extern` only in modules listed in `app.jip`: `externs { allow: ["acme/pdf"], max: 10 }`. Unlisted `extern` is a compile error, not a warning (the `any` evidence: agents will use whatever compiles).
3. **Compiler requirements per extern:** (a) effect set written explicitly, never inferred; (b) *effect floor*: the compiler ships a table mapping Go import paths to minimum effects (`os`,`io`→`io`; `net/*`→`net`; `time`,`math/rand`,`crypto/rand`→`nondet`; `sync`,`sync/atomic`→`mut`; goroutine launch→`spawn`) and rejects declarations below the floor; a declared-pure extern over a package outside the table needs the keyword `unsafe pure` plus a `why:` doc line (Haskell/Koka precedent); (c) mandatory one-line doc; (d) at least one BDD scenario tagged `@extern` exercising it (§14-consistent); (e) Go module pinned by commit in `app.jip` like any dependency.
4. **`jip check` reports:** total extern count and list (module, Go path, effects); any extern missing floor compliance, doc, or scenario; externs/pub-fn ratio per module with a stdlib budget (target ≤ Gleam's ~0.4, i.e. ~150 primitives for v0 stdlib); and a "backend surface" summary — the exact list a second backend would have to reimplement. CI fails on new unlisted externs, the same way it fails on interface drift (§9).
5. **Stdlib policy:** native implementations for collections, strings, JSON, Option/Result, contracts; externs only as leaf primitives (file, socket, clock, random, process, HTTP transport). Gleam-style fallbacks are pointless with one backend — instead the leaf list *is* the port plan.

**Decisions reopened:** §12 (resolve: accepted with the gate above). §10 `app.jip` schema gains an `externs` block (track (h) should include it). §3 "no `any`-style escape in user code" should be extended to say `extern` outside the allowlist is such an escape. §15 v0 stdlib list stands, but HTTP server/client and filesystem should be specified as native APIs over named leaf primitives, not as Go wrappers. §2 unchanged: no second backend in v1, but the surface is measured from v0.

## Sources

- Haskell FFI spec, primitive bindings: https://www.haskell.org/definition/ffi/sec-primitive.html
- GHC Safe Haskell user guide: https://www.haskell.org/ghc/docs/7.6.1/html/users_guide/safe-haskell.html
- Rust RFC 3484 unsafe extern blocks: https://github.com/rust-lang/rfcs/blob/master/text/3484-unsafe-extern-blocks.md
- McCormack et al., "A Mixed-Methods Study on the Implications of Unsafe Rust": https://arxiv.org/abs/2404.02230
- Rao et al., unsafe encapsulation practice: https://arxiv.org/abs/2406.07936 ; Rust CVE study: https://arxiv.org/abs/2003.03296
- Koka repo (`lib/std/core/unsafe.kk`, `console.kk`, `os/env.kk`): https://github.com/koka-lang/koka ; Koka book: https://koka-lang.github.io/koka/doc/book.html
- Unison FAQ (no FFI, abilities as foreign API): https://www.unison-lang.org/docs/usage-topics/general-faqs
- Elm "Native Code in 0.19": https://discourse.elm-lang.org/t/native-code-in-0-19/826 ; Elm interop limits: https://guide.elm-lang.org/interop/limits ; Humio upgrade report: https://discourse.elm-lang.org/t/our-experience-upgrading-to-0-19-at-humio/3258 ; "Elm 0.19 broke us": https://dev.to/kspeakman/elm-019-broke-us--khn
- Gleam externals docs: https://gleam.run/documentation/externals/ ; v0.16 JS target: https://gleam.run/news/v0.16-gleam-compiles-to-javascript/ ; v0.17 stdlib rework: https://gleam.run/news/gleam-v0.17-released/ ; v0.34 multi-target: https://gleam.run/news/v0.34-multi-target-projects ; stdlib repo (measured): https://github.com/gleam-lang/stdlib
- Elixir "Erlang libraries" guide: https://elixir.hexdocs.pm/erlang-libraries.html ; Valim on Enum/zero-indexing: https://erlang.org/pipermail/erlang-questions/2013-January/071877.html
- Clojure design wiki on interop: https://archive.clojure.org/design-wiki/display/design/Improvements%2Bto%2Binterop%2Bfrom%2BJava.html ; community discussion: https://clojurians-log.clojureverse.org/clojure/2016-09-18
- Kotlin platform types (M9): https://blog.jetbrains.com/kotlin/2014/10/m9-is-here/ ; mapped types: https://discuss.kotlinlang.org/t/confused-about-mapped-types/14335/3 ; KMP migration (JProfiler): https://www.ej-technologies.com/blog/2025/04/bringing-jprofiler-to-vs-code-with-kotlin-multi-platform/ ; Kotter migration: https://github.com/varabyte/kotter/blob/main/MIGRATING.md
- Scala.js JAVALIB policy: https://github.com/gzm0/scala-js/blob/main/JAVALIB.md ; Scala Native javalib: https://scala-native.org/en/latest/lib/javalib.html ; portable-scala-reflect: https://scaladex.scala-lang.org/portable-scala/portable-scala-reflect ; Scala Native next steps thread: https://contributors.scala-lang.org/t/scala-native-next-steps/4216?page=4
- TypeScript `any` prevalence (MSR 2022): https://arxiv.org/pdf/2203.11115 ; AI agents and `any` (MSR 2026): https://arxiv.org/abs/2602.17955
- Borgo README: https://github.com/borgo-lang/borgo
