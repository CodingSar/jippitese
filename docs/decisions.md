# jippitese — design decisions

Status: **spec in progress**. This file is the record of the design interview
and the research synthesis (`docs/research/synthesis.md`). Every entry is a
decision the project owner made. The spec (`docs/spec/`) must not contradict
this file; if it needs to, this file changes first.

File extension: `.jip`. CLI: `jip`.

## 1. Positioning

- **General-purpose core plus first-class agent and workflow primitives.** The
  core language is a normal statically typed language. Agent primitives live
  in the core library (or the first additional library), not in syntax.
- **Authors are agents; humans review.** Humans will never write jippitese by
  hand. Both agents and humans must be able to re-enter the code cold months
  later.
- **Two pillars: verifiability and bounded context.** Every feature is judged
  by whether it makes code easier to check mechanically and whether it keeps
  the amount of text an agent must read to act small.
- **Performance target: Go-class.** Much closer to C/Rust than to Python. Not
  a C/Rust competitor.

## 2. Implementation strategy

- **Host language: Go.** The compiler is written in Go (`compiler/`, one Go
  module).
- **Backend: emit Go source from v0.** The Go toolchain builds it. We inherit
  Go's garbage collector, goroutines, native binaries and cross-compilation.
  No interpreter phase.
- **No manual memory management, no ownership annotations.** GC only.
- **Pinned minimum Go version.** The compiler embeds a pinned Go toolchain.
  Generic methods need Go 1.27; below that, generic methods are lifted to free
  functions. The pin is recorded in `compiler/` and bumped deliberately.
- **Lowering rules (track g).** Small non-recursive sum types lower to a
  flattened tagged struct, others to a sealed interface with one struct per
  variant. `Option<T>` lowers to `{v T; ok bool}`, `Result<T, E>` to
  `{ok bool; v T; e E}`; neither allocates. `match` lowers to a `switch`
  without `default`. Type parameters map 1:1 to Go generics; behaviours become
  Go interfaces; the compiler monomorphises selectively for primitive
  collection operations. Persistent collections are an own stdlib (HAMT map
  and set, RRB vector) with transient builders.
- **Build.** `jip build` writes a deterministic, `gofmt`-ed Go module under
  `.jip/gen/`, generates `go.mod` from `app.jip`, and runs
  `go build -trimpath -mod=vendor`. Build cache keyed on source hash plus
  dependency interface digests (§9).
- **`//line` directives** map every generated statement to its `.jip`
  position, so leaked Go errors and panic traces name `.jip` files. Users
  never see a Go type error; all type errors come from the jippitese checker.

## 3. Type system

- Strict static typing. No `any`-style escape in user code. An `extern`
  outside the allowlist (§12) counts as such an escape and is a compile error.
- Algebraic data types (sum and product types) with **exhaustive** `match`.
- **No null.** `Option<T>` only.
- Structural generics with constraints. Generic constraints are behaviours
  (see §5).
- **Full type signatures at every function boundary. Local inference only**
  (inside bodies).
- No higher-kinded types in v1.
- No implicit conversions. No operator overloading.

## 4. Verification

### Effects

- **Effect types are in the core.** Closed label set: `io`, `net`, `mut`
  (shared mutation), `llm<role>`, `decide<role>`, `nondet` (randomness and
  time), `spawn`, `approve` (human approval). No handlers, no row types.
- **Syntax:** the effect set is written after the return type with the `with`
  keyword and braces; omitted means pure.
  `fn fetch(u: Url) -> Result<Body, NetError> with {net, llm<smart>}`.
- **Declared set is an upper bound.** A missing effect is a compile error; an
  unused declared effect is a `jip check` warning. An `impl` method's effects
  must be a subset of the behaviour's effect budget (§5).
- **Closure parameters propagate contextually.** An unannotated
  function-typed parameter contributes its argument's effects to the call
  site, so `map`, `fold`, `retry` need no effect variables. An annotated
  function type is a bound; `with {}` means must be pure.
- **Stored or returned closures carry explicit sets.** Generic combinators may
  use effect-set variables with union only (`e1 + e2`).
- A `spawn`ed block's effects must be a subset of the enclosing declared set.
- **Erasure.** Effects leave no trace in Go types. Every function with a
  non-empty set takes one hidden leading context parameter carrying
  cancellation and the `app.jip` instrumentation policy.
- **One `nondet` label.** Inside a workflow body, `now()`, `random()` and
  `sleep()` resolve to runtime-journaled versions, so there is no split
  between journaled and raw time.
- **Workflow bodies.** `Workflow.run` declares its effects normally; the
  compiler lowers every effectful call in a workflow body to a journaled
  step. A raw un-journaled effect inside a workflow is impossible.
- **Design principle: effects are the instrumentation points.** Observability
  (OpenTelemetry, OTEL), retries, timeouts, rate limits, auth and similar
  cross-cutting concerns attach to effect types through app configuration,
  never through code at call sites. The one line is a block:
  `telemetry { exporter: otlp }` with endpoint and sampling defaults; the same
  shape for `effects { net {...} llm {...} io {...} }` where retries and
  timeouts attach. Export targets are open protocols only.

### Contracts

- **`pre`, `post`, `inv` lines** sit between a signature and its body, one per
  line. `ret` names the result in `post`. `old(e)` is allowed only in `post`;
  `e` is a pure expression over parameter names. `forall x in c` and `exists x in c` quantify only over
  finite collections and ranges. Contract expressions are typed with the empty
  effect set, so purity is compiler-enforced.
- **`inv` attaches to ADTs** and is checked at construction; `mut` cells
  re-check `inv` after each write under `all`.
- **Levelled checking.** `contracts: all | pre | none` in `app.jip`.
  Default `pre` for `jip build`, `all` under `jip test`.
- **A contract violation is a `panic`** (§8). Validation of external data is a
  `Result`, never a `pre`.
- **No frame clause.** Immutability plus effects supply it; the spec states
  this.
- **Property derivation.** For every `pub fn` whose effect set is empty or
  `{mut}` on fresh state, and that has a `post` or whose parameter or return
  types carry `inv`, `jip test` derives one property check: generators from
  types narrowed by `pre`, N runs under `contracts: all`, oracle is `post`,
  every `inv` and no panic, integrated shrinking, shrunk seed persisted as a
  regression case. For an `impl` with `inv`, a stateful check draws method
  sequences. Functions with `io`, `net`, `llm` or `decide` get no derived
  check; scenarios with fakes cover them.
- Solver-based static proof (SMT) is a **v1 non-goal**.

## 5. Behaviours (reusable patterns)

- A **behaviour** is a stdlib-defined pattern contract: an interface, its
  associated types, its effect budget, its contracts, and a lifecycle.
- **v0 behaviours:** `Service`, `Worker`, `Cron`, `Machine`, `Workflow`,
  `Agent`, `Repository`, `Cache`. No `Pipeline`: chain `Worker`s over the
  stdlib type `Topic<T>`. `Secret` is a stdlib type, not a behaviour.
- **Lifecycle** is fixed to `init / handle / stop / health`; `health` has
  defaults (liveness = booted, readiness = aggregate of `Repository` and
  `Cache` impls). `Machine` adds `deactivate`.
- **Effect budget.** Each behaviour fixes the maximum effects an `impl`
  method may declare (table in `docs/research/a-pattern-catalogs.md`); the
  runtime supplies the rest.
- **`Machine`** is a closed `State × Event` exhaustive match, one turn per
  key, state persisted per turn; side effects only through returned
  `Action`s.
- **Stdlib-provided contracts** for behaviours (`Worker` idempotency,
  `Repository` get-after-put round-trip) auto-generate property checks.
- **Schedules and retry policies are compile-time literals.**
- **One `impl` construct** implements any behaviour. No per-pattern syntax.
- Applications are composed from behaviours; the compiler checks that an
  `impl` satisfies the behaviour completely (methods, effects, contracts).
- **Supervisor is not user-facing.** Each `impl` instance gets an OTP-style
  child spec in `app.jip` (`restart: permanent | transient | temporary`,
  `max_restarts`, `period`, `shutdown_timeout`). Start order is derived from
  the reference graph.

## 6. Agent primitives

- **`llm` effect.** Calling a language model is an effect. A jippitese type
  doubles as the structured-output schema; the compiler derives the schema.
- **Tools** are derived from function signatures (name, typed parameters,
  mandatory doc line, declared effects).
- **`decide` effect** for System One models (e.g. Jev by TypeSafe AI). Output
  type is restricted to a closed, payload-less sum type, a bounded score, or a
  bool with a calibrated probability. Provider-agnostic.
- **Model roles, not model names, in code.** Code says `llm<smart>` or
  `decide<fast>`; `app.jip` maps each role to a provider and model.
- **`Workflow` behaviour.** Associated types `Input`, `Output`, `Signal`
  (closed sum); `run(Input) -> Result<Output, WorkflowError>`; optional
  `on_signal`; `const version`. Every effectful call in `run` is a journaled
  step keyed `<fn>#<ordinal>`; `mut` is forbidden in the body; a history
  mismatch is a typed `ReplayError`, never a silent re-run. The runtime
  provides `step`, `sleep`, `now`, `random`,
  `await_signal<T>(name, timeout) -> Option<T>` and `continue_as_new`.
  `spawn` inside `run` lowers to runtime coroutines, not goroutines.
  **Language owns the step rule and types; a library owns the journal.**
  Runtime selected by `workflow: embedded | temporal | restate` in `app.jip`;
  `embedded` (SQLite journal, single process) is the v0 default.
- **`approve` effect = durable promise.**
  `approve<T>(review: Review) -> Result<T, Rejected> with {approve}` is a
  named, journaled suspension with a mandatory timeout; `T` is a closed sum or
  `bool`. Usable inside `Workflow` and as the `Agent` tool gate.
- **`Agent` behaviour.** Associated types `State`, `Answer`; `const model:
  Role`, `const tools`, `const budget` (`max_turns` and `max_cost`, both
  required); pure `stop(State) -> Option<Answer>`; `gate(ToolCall) -> Allow |
  Deny(reason) | Ask`. The runtime owns the loop and context compaction; a
  run ends with `Answer`, `Stopped(budget)` or `Refused`. An `Agent` run
  inside a `Workflow` is one step per model call.

## 7. Concurrency and mutation

- **Structured concurrency only.** No detached tasks. `spawn` is an effect
  scoped to a block that joins or cancels its children. Lowers to an
  errgroup-shaped runtime scope.
- Long-lived `Service`, `Worker` and `Cron` instances are children of the app
  root with the restart policy from their child spec (§5).
- Inside `Workflow.run`, `spawn` lowers to deterministic runtime coroutines;
  the semantics are the same, the implementation differs.
- Immutable by default. `var` for local mutable bindings.
- Shared mutation only through effect-tracked cells (`mut` effect).
- Persistent (immutable) collections by default, with explicit mutable
  variants that lower to native slices and maps. **Flag:** track (g) measured
  roughly 2× insert cost for HAMT maps; the §13 collections benchmark must
  run before v1 locks this default.

## 8. Errors

- Errors are values: `Result<T, E>` and `Option<T>`; `?` propagates.
- Error types are ordinary ADTs. No exceptions.
- `panic` is for bugs and contract violations only and is not catchable in
  user code.

## 9. Modules and context

- A module is a directory. Implementation files mark exported items with
  `pub`. `pub type T` with an opaque body hides the representation;
  `pub(module)` limits visibility to the enclosing module.
- **Interface files are extracted by the compiler, committed to the repo, and
  checked for drift in CI** (`jip check`). Nobody edits them; their git diff
  is the review surface. Content per `pub` item: one-line doc, full
  signature, effects, `pre`/`post`. Items are canonically sorted; the file
  carries a content digest of the normalised interface, so drift checking is
  re-extract, re-hash, compare.
- **`jip check` classifies API changes** as `breaking`, `compatible` or
  `none` per module and prints an `elm diff`-style summary (`Added:`,
  `Removed:`, `Changed:`). A dependency bump reports against the vendored
  interface.
- **`jip context <module>`** emits a *context pack* in two tiers: the module's
  full interface, plus for each direct dependency only the referenced items
  and the types their signatures mention. `--short` prints one line per
  symbol. "Tokens to read a context pack" is an eval metric (§13).
- **One-line doc comment is mandatory on every `pub` item**, compiler-enforced.
- **No content-addressed code.** Resolved; the digest and the interface-keyed
  build cache are the two wins kept.

## 10. Configuration and bootstrap

- **Single `app.jip`** containing a typed `app { ... }` declaration. No YAML,
  no JSON config. An unknown field is a compile error; no implicit typing.
- **Secrets** are `secret<T>` fields and never carry a literal; they arrive
  only through the environment.
- **Overlay rule:** exactly three layers, fixed order: `app` body <
  `env <name> {}` block selected by `JIP_ENV` < process variables
  `JIP_<PATH>` (field path upper-cased, `__` between segments). Only
  `default`-marked scalar leaves and secrets may be overridden; a `JIP_*`
  variable matching no field, or one that fails to parse by the declared
  type, aborts startup before the first effect runs.
  `jip check --effective --env <name>` prints the resolved config with each
  value's source layer. **No per-environment files.**
- **Dependencies** are declared in `deps { alias = git(url, commit, hash) }`.
  The hash is the truth, the URL a mirror; `jip fetch --save` computes it and
  a missing hash is an error. Imports name the alias, never the URL.
  Vendoring is flat (`vendor/<alias>`), one commit per URL graph-wide; a
  transitive conflict (diamond) is a compile error the root resolves by
  pinning. No minimal-version selection in v0 (commits have no order).
  **No package registry.** The stdlib is the only first-party dependency.
  **Flag:** diamond-as-error is strict and may become a tax once there is an
  ecosystem; it is cheap to relax later and expensive to tighten.
- **Blocks:** `deps`, `models` (role to provider and model), `secrets`,
  `effects` (per-effect timeout, retry, budget, rate limit), `telemetry`
  (`exporter: otlp`, endpoint, sample), `externs` (§12), `contracts:` level,
  `workflow:` runtime, per-impl child specs (§5), `env <name> {}` overlays.
  Defaults are conservative (sample 0.1, retries only on `net`).

## 11. Syntax

- C/Go/TypeScript-shaped: braces, `fn`, infix operators, familiar literals.
  Keep `fn`, `match`, `let`/`var`, `?`, `pub`; no novel sigils.
- **Tokenizer-unambiguous:** no significant whitespace, no optional
  delimiters, one canonical form per construct, fixed by `jip fmt`.
- **Mandatory file header.** Every `.jip` file starts with a `module` line so
  models do not drift into Go or TypeScript.
- **One way to do each thing.** No macros in v1. No operator overloading. No
  implicit conversions.
- One canonical formatter (`jip fmt`) with zero options.
- **Terseness policy: terse in bodies, explicit at boundaries.** Every
  keyword or operator that deviates from Go/TS must earn its place in the
  eval (§13).

## 12. Interop

- **Go interop through `extern`, accepted with a gate.** An `extern`
  declaration carries a full jippitese signature, an explicit effect set
  (never inferred) and a Go target:
  `extern fn read_file(p: Path) -> Result<Bytes, IoError> with {io} = go "os".ReadFile`.
  Only jippitese types in the signature; no Go types leak into interface
  files. At the boundary `(T, bool)` maps to `Option<T>` and `(T, error)` to
  `Result<T, E>`, inlined.
- **Where:** stdlib by default. App or vendored code may declare `extern`
  only in modules listed in `app.jip` `externs { allow: [...], max: N }`.
  An unlisted `extern` is a compile error.
- **Effect floor.** The compiler ships a table mapping Go import paths to
  minimum effects (`os`, `io` → `io`; `net/*` → `net`; `time`, `math/rand`,
  `crypto/rand` → `nondet`; `sync`, `sync/atomic` → `mut`; goroutine launch →
  `spawn`) and rejects declarations below it. A declared-pure extern over a
  package outside the table needs `unsafe pure` plus a `why:` doc line.
- Each extern needs a one-line doc and at least one BDD scenario tagged
  `@extern`. The Go module is pinned by commit in `app.jip` like any
  dependency.
- **`jip check` reports** the extern count and list, floor/doc/scenario
  violations, the externs-to-pub-fn ratio per module (stdlib budget ≈ 0.4,
  about 150 leaf primitives for v0), and the backend surface a second backend
  would have to reimplement. No second backend in v1.

## 13. Evaluation

- Evals compare jippitese against Go, TypeScript and Python on:
  performance, abstraction, understandability, intuitiveness,
  **tokens to read** (whole program, and the context pack needed to make a
  given change) and **iterations to compile** (retries drive agent cost, not
  static length).
- The performance eval includes a **collections benchmark** (persistent vs.
  mutable) before v1 locks the §7 default.
- Run across multiple models. The eval harness is built after the spec and
  before most of the stdlib.
- The spec is written with agents as the primary reader and is itself
  measured by the intuitiveness eval. The ~2k-token language card is
  measured the same way.
- Budget for a Gleam-sized accuracy gap at launch; the `examples/` corpus is
  the mitigation.

## 14. Testing

- **BDD (Behavior-Driven Development) tests only. No unit-test files.**
- **Files:** scenarios live beside the module in `<module>/scenarios.jip`.
- **Format:** one `scenario "..." { given ...; when ...; then ... }` block per
  scenario, Given/When/Then order. A `given x = any T where <expr>` step
  draws from the contract generators, so a scenario is a property when it
  quantifies and an example otherwise.
- Contracts are active during scenarios, so a `then` failure and a `post`
  failure print the same shrunk example.
- `jip test` runs scenarios plus the derived property checks (§4); derived
  checks are not hand-written.
- Every `extern` has at least one `@extern` scenario (§12).

## 15. Tooling

- `jip` CLI: `build`, `test`, `fmt`, `check`, `context`, `fetch`, `new`.
- **Diagnostics are a v0 feature.** Every compiler, effect and contract error
  names file:line, expected vs. found, and a concrete suggested edit.
- Language server: deferred. The interface files are the shallow export data
  it will need.
- **v0 stdlib:** core types, collections, strings, JSON, HTTP client and
  server, filesystem, time, testing, `llm`, tools, behaviours. Native
  implementations for collections, strings, JSON, `Option`/`Result`,
  contracts; externs only as leaf primitives (file, socket, clock, random,
  process, HTTP transport). HTTP and filesystem are native APIs over those
  leaves. The leaf list doubles as the port plan for any future backend.

## 16. Project

- License: Apache-2.0.
- Layout: `docs/research/`, `docs/spec/`, `docs/decisions.md`, `compiler/`
  (Go module), `stdlib/`, `evals/`, `examples/`.
- Process: research → synthesis → spec → eval harness → compiler v0 → stdlib
  → evals.

## Resolved deferred decisions

| Item | Resolution | Track |
|---|---|---|
| Go `extern` interop gating | Accepted with allowlist, effect floor, doc, scenario (§12) | i |
| Content-addressed code | No; interface digests and digest-keyed build cache instead (§9) | d |
| Behaviour list and lifecycle | Eight behaviours, `init / handle / stop / health`, child specs (§5) | a |
| Effect system surface | Effect sets, closed labels, `with {…}`, contextual closure rule (§4) | b |
| Durable workflow runtime | Language owns step rule; library owns journal; `embedded` default (§6) | e |
| Contract syntax and derivation | `pre`/`post`/`inv` lines, levelled, default `pre` (§4) | f |
| Config env overlay | `app` < `env` block < `JIP_<PATH>`, defaults only (§10) | h |
| Syntax vs LLM accuracy | Go/TS shape kept, module header, diagnostics v0, examples-first spec (§11, §15) | c |
| BDD file naming | `<module>/scenarios.jip` (§14) | f |
| Diamond dependencies | Compile error in v0 (§10) | h |
| Contract default in release | `pre` (§4) | f |

## Still deferred

| Item | Waiting on |
|---|---|
| Language server | post-v1 |
| Solver-based verification | post-v1 |
| Persistent-by-default collections (final) | §13 collections benchmark |
| Minimal-version selection for deps | ecosystem pressure |
