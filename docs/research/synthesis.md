# Research synthesis

Nine reports (`a`–`i`) are in. This file reconciles them against
`docs/decisions.md`: what they confirm, what they reopen, where they disagree,
and what needs an owner decision before the spec starts. `decisions.md` is
unchanged until those decisions are made.

Evidence caveat shared by all tracks: most vendor doc hosts were blocked by the
session proxy, so primary text came from GitHub mirrors, pkg.go.dev and search
excerpts. Each report marks what it could not verify. Only (g) and (i) contain
first-hand measurements (Go-intrinsic costs; extern counts in Gleam and Koka
stdlibs).

## 1. Confirmed without change

- Host Go, emit Go source, Go GC, no ownership (§2). (g) found no precedent
  that needed anything else.
- Strict typing, ADTs, exhaustive match, `Option`, full signatures at
  boundaries (§3). (c) rates explicit boundary types as the strongest
  syntax-level accuracy lever it found.
- Brace syntax, one way per construct, zero-option formatter (§11). (c)
  adds: no significant whitespace, no optional delimiters.
- Compiler-extracted, committed, drift-checked interface files (§9).
- Single typed `app.jip`, no YAML, git-pinned vendored deps, no registry (§10).
- BDD-only tests (§14). (f) found no system that unifies scenarios with
  contracts, so jippitese would be first; the design is in §3 below.
- Deferred: language server, solver verification (§15, §16).

## 2. Deferred decisions now resolvable

| Deferred item | Track | Proposed resolution |
|---|---|---|
| Content-addressed code | d | **No.** Keep two cheap wins: a content digest in every interface file so `jip check` is re-extract, re-hash, compare; and a build cache keyed on interface digests so body-only edits never rebuild dependents. |
| Effect surface | b | **Simple effect sets, closed label set, no handlers, no row types.** Written after the return type, omitted when pure. Unannotated closure parameters pass their effects to the call site (Effekt's contextual rule), so `map`/`retry` need no effect variables. Declared set is an upper bound; an unused declared effect is a `jip check` warning. Effects erase to one hidden `ctx` parameter in Go. |
| Behaviour list and lifecycle | a | **`Service`, `Worker`, `Cron`, `Machine`, `Workflow`, `Agent`, `Repository`, `Cache`.** Drop `Pipeline` (chain `Worker`s over a `Topic<T>` type). Lifecycle fixed to `init / handle / stop / health`; `health` has defaults. Supervisor is not user-facing: each `impl` instance gets an OTP-style child spec in `app.jip`. |
| Durable workflow runtime | e | **Language owns the step rule and types; a library owns the journal.** Inside `Workflow.run`, every effectful call is a journaled step keyed by a compiler-derived ordinal; `mut` is forbidden; each impl has a `version`; history mismatch is a typed `ReplayError`. Runtime selected by one `app.jip` line, embedded SQLite journal by default. |
| Contract syntax and property derivation | f | `pre` / `post` / `inv` lines between signature and body; `ret` names the result; `old()` only on names; quantifiers only over finite collections; contract expressions must be effect-free (compiler-enforced). Checking is levelled: `contracts: all \| pre \| none` in `app.jip`, default `pre` for build, `all` for test. Property checks are derived only for effect-free (or fresh-`mut`) functions, with generators from types narrowed by `pre`. |
| Env overlay rule | h | Three fixed layers: `app` body < `env <name> {}` block selected by `JIP_ENV` < `JIP_<PATH>` variables. Only `default`-marked leaves and secrets may be overridden; unknown or ill-typed `JIP_*` aborts startup; `jip check --effective --env X` prints the resolved config with each value's source. |
| Go `extern` gating | i | **Accept with a gate.** `extern` carries a full jippitese signature, an explicit effect set and a Go target. No Go types in signatures. Stdlib by default; app code only in modules listed in an `app.jip` `externs { allow: [...] }` block. Compiler enforces an effect floor per Go import path, a doc line, and one BDD scenario per extern. `jip check` reports the extern count and ratio per module. |
| Syntax vs LLM accuracy | c | Keep Go/TS-shaped syntax; add a mandatory file header so models do not drift into Go; make rustc-quality diagnostics a v0 feature; write the spec examples-first with a ~2k-token "language card". |

## 3. Amendments the evidence asks for

Numbered for the owner's yes/no.

1. **§4 Workflow effects.** (a) says a workflow body has no direct effects;
   (e) says the body declares effects and each call becomes a journaled step.
   Same semantics, different wording. Proposed: (e)'s formulation. `run`
   declares its effects normally; the compiler lowers every effectful call in
   a workflow body to a step; a raw un-journaled effect is impossible.
2. **§4 `nondet` label.** (e) suggests splitting time/random from journaled
   effects; (b) wants a closed small label set. Proposed: keep one `nondet`
   label. Inside workflows, `now()`/`random()`/`sleep()` resolve to
   runtime-journaled versions, so no split is needed.
3. **§4 telemetry example.** (h): `telemetry: otel` should be a block, not an
   atom, and the exporter must be an open protocol. Proposed:
   `telemetry { exporter: otlp }` is the one line, with endpoint and sampling
   defaults; the same shape for `effects { net {...} llm {...} }` where
   retries and timeouts attach.
4. **§4 and §14 contracts.** "Runtime-checked and generate property checks"
   becomes levelled checking (item above) and derivation only for effect-free
   functions. A contract violation is a `panic`; validating external data
   stays `Result`, never `pre`. No frame (`modifies`) clause is needed because
   immutability plus effects supply it; the spec should state this.
5. **§5 behaviours.** Adopt (a)'s list and lifecycle. `Agent` takes (e)'s
   shape (runtime owns the loop; impl provides pure `stop`, a `gate` for tool
   calls, a required `budget` with both max turns and max cost) with (a)'s
   budget contract. `Machine` is a closed `State × Event` exhaustive match,
   turn-based per key.
6. **§6 `approve`.** A durable promise with a name, a mandatory timeout, and a
   closed-sum or `bool` payload. Usable in `Workflow` and as the `Agent` tool
   gate.
7. **§7 concurrency.** Two things to state: long-lived `Service`/`Worker`
   instances are children of the app root with a restart policy from the
   child spec; `spawn` inside a workflow lowers to runtime coroutines, not
   goroutines. Persistent-by-default collections stay, but (g) measured
   roughly 2× insert cost for HAMT-style structures, so the §13 performance
   eval must include a collections benchmark before v1 locks the default.
8. **§9 interface files and context packs.** Interface file content = per
   `pub` item: one-line doc, full signature, effects, `pre`/`post`;
   canonically sorted; carries a digest. `jip check` classifies API changes as
   breaking/compatible/none and prints an `elm diff`-style summary. Add
   source-level narrowing (`pub type T` opaque, `pub(module)`). Context pack
   has two tiers: full target interface, plus only the referenced items of
   direct dependencies and the types their signatures mention.
9. **§10 dependencies.** Imports use an alias declared in `deps {}`, never a
   URL. The content hash is the truth, the URL a mirror. Flat `vendor/`, one
   commit per URL graph-wide; a diamond is a compile error the root resolves
   by hand. *Flag:* diamond-as-error is strict and could become a tax once
   there is an ecosystem; cheap to relax later, expensive to tighten.
10. **§10 `app.jip` additions.** `externs {}` block, per-impl child-spec
    fields, `contracts:` level, `workflow:` runtime, `secret<T>` fields that
    may never hold a literal, `env <name> {}` blocks. No per-environment
    files.
11. **§11 terseness policy.** Reword to "terse in bodies, explicit at
    boundaries". Every keyword or operator that deviates from Go/TS must earn
    its place in the eval.
12. **§13 evals.** Add "iterations to compile" beside "tokens to read", and
    the collections benchmark from item 7. Budget for a Gleam-sized launch
    accuracy gap; an `examples/` corpus is the mitigation.
13. **§14 BDD format.** Scenarios are Given/When/Then; a `given x = any T
    where <expr>` step draws from the contract generators, so a scenario is
    a property when it quantifies and an example otherwise. Contracts are
    active during scenarios, so a `Then` failure and a `post` failure print
    the same shrunk example. File naming still open (see §4 below).
14. **§15 tooling.** Diagnostics are a v0 feature: file:line, expected vs
    found, concrete suggested edit. Record a pinned minimum Go version
    (generic methods need Go 1.27, otherwise lift to free functions). Emit
    `//line` directives so leaked Go errors and panics name `.jip` positions.
15. **§15 stdlib.** Native implementations for collections, strings, JSON,
    `Option`/`Result`, contracts; externs only as leaf primitives (file,
    socket, clock, random, process, HTTP transport). HTTP and filesystem are
    native APIs over those leaves. The leaf list doubles as the port plan for
    any future backend.

## 4. Questions for the owner

1. **Effect syntax.** Reports used three spellings: `-> T ! {net, llm<smart>}`
   (b), `-> T with net, llm` (e), `-> T / io` (i). Recommendation: `with`
   keyword, braces, since (c) penalises unfamiliar operators:
   `fn fetch(u: Url) -> Result<Body, NetError> with {net, llm<smart>}`.
2. **BDD file naming.** Recommendation: scenarios live beside the module in
   `<module>/scenarios.jip`, one `scenario "..." { given ...; when ...; then
   ... }` block each; `jip test` runs scenarios plus derived property checks.
3. **Diamond dependencies.** Keep as compile error (recommended for v0) or
   adopt minimal-version selection now?
4. **Contract default.** `pre` in release builds (recommended, per AdaCore's
   measurements) or `all`?
5. **Accept amendments 1–15** as edits to `decisions.md`? On yes, I update
   `decisions.md`, close the deferred rows, and start `docs/spec/` with the
   language card and the examples-first structure from (c).
