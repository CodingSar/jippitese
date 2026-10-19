# jippitese — design decisions

Status: **spec in progress; revised per `docs/revision-plan.md` (R1–R15,
ratified 2026-10-19)**. This file is the record of the design interview,
the research synthesis (`docs/research/synthesis.md`) and the external
critique (`docs/review-brief.md`). Every entry is a
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
- **Pinned Go version: 1.24.** The compiler embeds a pinned Go toolchain.
  The emitter never emits a generic method: every generic operation becomes a
  free function, and nothing in the design waits on a future Go release. The
  pin is recorded in `compiler/` and bumped deliberately. (R10)
- **Lowering rules (track g).** Small non-recursive sum types lower to a
  flattened tagged struct, others to a sealed interface with one struct per
  variant. `Option<T>` lowers to `{v T; ok bool}`, `Result<T, E>` to
  `{ok bool; v T; e E}`; neither allocates. `match` lowers to a `switch`
  without `default`. Type parameters map 1:1 to Go generics. **Behaviours lower to
  dictionaries, not Go interfaces** (R10): an `impl` becomes a struct of
  function values plus its associated types' witnesses, built once at
  `init`; Go interfaces cannot carry associated types, static `init` or
  `impl Ord for Int`. The compiler monomorphises selectively for primitive
  collection operations. Persistent collections are an own stdlib (HAMT map
  and set, RRB vector) with transient builders.
- **Build.** `jip build` writes a deterministic, `gofmt`-ed Go module under
  `.jip/gen/`, generates `go.mod` from `app.jip`, and runs
  `go build -trimpath -mod=vendor`. Build cache keyed on source hash plus
  dependency interface digests (§9).
- **Hidden context parameter.** Every function with a non-empty effect set
  takes one hidden leading parameter, the runtime record
  `Ctx { cancel, journal, effects, fakes }`. Externs receive none, so
  cancellation never reaches an extern; an extern that must be cancellable
  is wrapped in a stdlib function that owns the `Ctx`.
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
- **`Int` traps on overflow** in every build, release included; the trap is
  one compare per operation in the emitted Go. `checked_add`, `checked_sub`,
  `checked_mul` return `Option<Int>`; `wrapping_*` variants exist for the
  rare bit-twiddling case. (R2)
- **Inherent impls `impl T { … }` are allowed** beside `impl B for T`. The
  stdlib needs them (`Cell.new`, `MutList.new`, `List.builder`). (R3)

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
- **One `nondet` label**, defined extensionally: `time.now()`, `random()`,
  `sleep(d)` and `await_signal`. Nothing else carries it. Inside a workflow
  body these resolve to runtime-journaled versions, so there is no split
  between journaled and raw time.
- **`approve` admission (R4).** `approve` may appear only in `Workflow.run`,
  `Agent.gate`, and functions reachable only from them. Elsewhere it is a
  compile error: a blocking human promise outside a journal has no owner.
- **Fakes by default (R14).** `jip test` installs a fake clock and a seeded
  random source for every scenario and derived check, so `nondet` functions
  are reproducible from the seed. `given real_time` opts a scenario out.
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
- **A contract violation is a `panic`** (§8) in plain functions. **Inside a
  behaviour method it becomes that method's `Err`** at the behaviour
  boundary (HTTP 500 for `Service`, a failed message for `Worker`, a failed
  step for `Workflow`), never a supervisor-visible crash, so a single bad
  request cannot start a restart storm. (R12) Validation of external data is
  a `Result`, never a `pre`.
- **No frame clause.** Immutability plus effects supply it; the spec states
  this.
- **Property derivation (deferred from v0; boundary contracts ship first).**
  For every `pub fn` whose effect set is empty or
  `{mut}` on fresh state, and that has a `post` or whose parameter or return
  types carry `inv`, `jip test` derives one property check: generators from
  types narrowed by `pre`, N runs under `contracts: all`, oracle is `post`,
  every `inv` and no panic, integrated shrinking, shrunk seed persisted as a
  regression case. For an `impl` with `inv`, a stateful check draws method
  sequences. Functions with `io`, `net`, `llm` or `decide` get no derived
  check; scenarios with fakes cover them.
- **Generation size model.** Structural generators stop at depth 3,
  collections at length 8, `Str` at 32 bytes; a `pre` clause the generator
  cannot turn into a bound is a filter, and a filter rejecting more than 90%
  of candidates is the warning `W0510`, not a failure. The owner may name a
  generator explicitly with `via` (R9, §14).
- Solver-based static proof (SMT) is a **v1 non-goal**.

## 5. Behaviours (reusable patterns)

- A **behaviour** is a stdlib-defined pattern contract: an interface, its
  associated types, its effect budget, its contracts, and a lifecycle.
- **v0 behaviours (R1):** `Service`, `Worker`, `Cron`, `Workflow`,
  `Repository`, `Cache`. **`Machine` and `Agent` are deferred** from v0:
  `Machine` is `Workflow` plus signals, `Agent` is a stdlib loop over
  `Workflow`; each returns only with a compiled example. `Cron` and `Cache`
  stay because every v0 behaviour must go through the same dictionary
  lowering, run lines and `app.jip` overlay from the start rather than be
  retrofitted. No `Pipeline`: chain `Worker`s over the stdlib type
  `Topic<T>`. `Secret` is a stdlib type, not a behaviour.
- **Lifecycle** is fixed to `init / handle / stop / health`; `health` has
  defaults (liveness = booted, readiness = aggregate of `Repository` and
  `Cache` impls). `init(cfg: Config)` receives the `Config` record defined
  in §10. `Machine` (deferred) adds `deactivate`.
- **Effect budget.** Each behaviour fixes the maximum effects an `impl`
  method may declare (table in `docs/research/a-pattern-catalogs.md`); the
  runtime supplies the rest.
- **`Machine`** (deferred) is a closed `State × Event` exhaustive match, one
  turn per key, state persisted per turn; side effects only through returned
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
  step keyed `<callee>@<site>#<n>` (R6): `site` is the compiler-assigned id
  of the call site, `n` a per-task execution counter for that site, and the
  set of site ids is journaled with the task so an inserted branch, a
  closure, a `retry` or a recursive call cannot shift another step's key.
  `mut` is forbidden in the body; a history
  mismatch is a typed `ReplayError`, never a silent re-run. The runtime
  provides `step`, `sleep`, `now`, `random`,
  `await_signal<T>(name, timeout) -> Option<T>` and `continue_as_new`.
  `spawn` inside `run` lowers to runtime coroutines, not goroutines.
  **Language owns the step rule and types; a library owns the journal.**
  Runtime selected by `workflow: embedded | temporal | restate` in `app.jip`;
  `embedded` (SQLite journal, single process) is the v0 default and the only
  v0 runtime; `temporal` and `restate` are deferred.
  `WorkflowError<E> = Replay(ReplayError) | Approval(Rejected) | Cancelled |
  App(E)` (R11); `?` inside `run` uses the ordinary conversion rule from the
  spec (04), with no `Show`-based lift of foreign errors.
- **`approve` effect = durable promise.**
  `approve<T>(review: Review) -> Result<T, Rejected> with {approve}` is a
  named, journaled suspension; `T` is a closed sum or `bool` with
  `T: Decode`. `Review { title, body, timeout: Option<Duration>, data:
  Option<Json> }`: a missing `timeout` takes `effects.approve.default_timeout`,
  and `data` is passed through unread by the runtime, reserved for the
  future approvals library (`docs/revision-plan.md` §10). The channel is
  configured once, as `effects.approve.channel` in `app.jip`, naming a
  module path. Admission is governed by R4 (§4).
- **`Agent` behaviour (deferred from v0, R1).** Associated types `State`, `Answer`; `const model:
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
  re-extract, re-hash, compare. **`pub(module)` items** go in a second
  `module-visible` section with its own digest (R7); that digest is part of
  the build-cache key of every module under the same parent, and is not
  classified for breaking changes, because a `pub(module)` change can only
  break siblings the same commit must already fix.
- **`jip check` classifies API changes** as `breaking`, `compatible` or
  `none` per module and prints an `elm diff`-style summary (`Added:`,
  `Removed:`, `Changed:`). A dependency bump reports against the vendored
  interface. Contract changes are classified textually after `jip fmt`
  normalisation and labelled `contract-changed`, except that purely adding a
  `post` conjunct or removing a `pre` conjunct is `compatible`; the compiler
  does not decide implication. **An intentional break exits 0** with
  `jip check --accept-breaking <module>` or the commit trailer
  `jip-breaking: <module>` (R8); without either, a `breaking` result is a
  non-zero exit.
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
- **`config { field: Type }` block (R5).** `app.jip` declares the
  application's own settings in one typed block. The record type `Config`
  that every behaviour's `init(cfg: Config)` receives is that block's fields
  plus the `secrets` block.
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
  `workflow:` runtime (`embedded` only in v0), per-impl child specs (§5),
  `env <name> {}` overlays. Defaults are conservative (sample 0.1, retries
  only on `net`). No `custom` provider in v0. For `gomod` dependencies
  `jip build` must synthesise `vendor/modules.txt`, since `go build
  -mod=vendor` rejects a vendor tree without it.

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
- **Three parsing rules** (revision-plan Phase C), each removing an
  ambiguity the critique demonstrated with a two-parse program:
  1. No unparenthesised record literal as an `if`/`while` condition, a `for`
     iterable or a `match` scrutinee; `if x == Point { x: 1 } {` must be
     written `if x == (Point { x: 1 }) {`.
  2. Closures start with `fn`: `fn(a, b) => a + b`, `fn(x: Int) => { … }`.
     A parenthesised list is always a tuple or a parameter list, never both.
  3. Explicit type arguments in expressions use the turbofish `f::<T>(x)`.
     The lexer has no adjacency rule for `<` and `>` is never a statement
     terminator.
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
- **Effect floor (R13).** The compiler ships a table mapping Go import paths
  to minimum effects (`os`, `io` → `io`; `net/*` → `net`; `time`,
  `math/rand`, `crypto/rand` → `nondet`; `sync`, `sync/atomic` → `mut`) and
  rejects declarations below it. The floor is computed from the **direct
  imports of the target package only**: no transitive union (which would
  forbid every extern, since everything imports `os` somewhere) and no
  goroutine detection. `spawn` is never inferred for an extern. A declared-pure extern over a
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
  **tokens to read** and **iterations to compile** (retries drive agent
  cost, not static length).
- **Metric definition.** Tokens to read is the total tokens of every file
  and tool output an agent reads before its first green `jip check`, on a
  frozen task set of at least ten changes, under one named tokenizer
  version, reported together with iterations to green. The Go baseline runs
  the same tasks with `go doc` output counted the same way. **No token
  number appears in any document before it is measured this way** (R15).
- The performance eval includes a **collections benchmark** (persistent vs.
  mutable) before v1 locks the §7 default.
- Run across multiple models. The eval harness is built after the spec and
  before most of the stdlib.
- The spec is written with agents as the primary reader and is itself
  measured by the intuitiveness eval. The language card is measured the
  same way.
- Budget for a Gleam-sized accuracy gap at launch; the `examples/` corpus is
  the mitigation.

## 14. Testing

- **BDD (Behavior-Driven Development) tests only. No unit-test files.**
- **Files:** scenarios live beside the module in `<module>/scenarios.jip`.
- **Format:** one `scenario "..." { given ...; when ...; then ... }` block per
  scenario, Given/When/Then order. A `given x = any T where <expr>` step
  draws from the contract generators, so a scenario is a property when it
  quantifies and an example otherwise. `given x = any T via g` names a
  generator explicitly, and the table form `given rows = [ … ]` runs the
  block once per row (R9). BDD-only stays; these are forms of `given`, not
  a second test kind. `any T where` and derived checks are deferred from v0.
- Scenarios see the `pub` and `pub(module)` items of the module under test
  and nothing private.
- Scenarios run under the default fake clock and seeded random (§4, R14).
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
- Process: research → synthesis → spec → compiler v0 → eval harness →
  stdlib → evals, waterfall. **Gate:** no spec section is "done" until the
  language card compiles, `go vet` is clean on the output, its scenarios pass
  under the default fakes, and one example per v0 behaviour does the same
  (`docs/revision-plan.md` Phase E). The gate passes as a whole or not at
  all.

## Resolved deferred decisions

| Item | Resolution | Track |
|---|---|---|
| Go `extern` interop gating | Accepted with allowlist, effect floor, doc, scenario (§12) | i |
| Content-addressed code | No; interface digests and digest-keyed build cache instead (§9) | d |
| Behaviour list and lifecycle | Six v0 behaviours, `init / handle / stop / health`, child specs (§5, R1) | a |
| Effect system surface | Effect sets, closed labels, `with {…}`, contextual closure rule (§4) | b |
| Durable workflow runtime | Language owns step rule; library owns journal; `embedded` default (§6) | e |
| Contract syntax and derivation | `pre`/`post`/`inv` lines, levelled, default `pre` (§4) | f |
| Config env overlay | `app` < `env` block < `JIP_<PATH>`, defaults only (§10) | h |
| Syntax vs LLM accuracy | Go/TS shape kept, module header, diagnostics v0, examples-first spec (§11, §15) | c |
| BDD file naming | `<module>/scenarios.jip` (§14) | f |
| Diamond dependencies | Compile error in v0 (§10) | h |
| Contract default in release | `pre` (§4) | f |
| R1 v0 behaviour set | `Service`, `Worker`, `Cron`, `Workflow`, `Repository`, `Cache` (§5) | a |
| R2 integer overflow | Trap in every build; `checked_*` return `Option` (§3) | g |
| R3 inherent impls | Allowed (§3) | c |
| R4 `approve` admission | `Workflow.run`, `Agent.gate` and functions reachable only from them (§4) | e |
| R5 `Config` type | `config {}` block plus `secrets` (§10) | h |
| R6 step key | `<callee>@<site>#<n>` with journaled site set (§6) | e |
| R7 `pub(module)` digest | Second digest, in sibling cache keys, not classified (§9) | d |
| R8 accepting a break | `--accept-breaking` or `jip-breaking:` trailer (§9) | d |
| R9 scenario forms | `via` generator, table rows (§14) | f |
| R10 behaviour lowering | Dictionaries; no generic methods; Go 1.24 (§2) | g |
| R11 `WorkflowError` | Four-variant sum, ordinary `?` (§6) | e |
| R12 contract violation in behaviours | Method `Err` at the boundary (§4) | f |
| R13 extern floor | Direct-import table only (§12) | i |
| R14 fakes by default | Fake clock and seeded random under `jip test` (§4, §14) | f |
| R15 token numbers | Removed until measured by the §13 definition | — |

## Still deferred

| Item | Waiting on |
|---|---|
| Language server | post-v1 |
| Solver-based verification | post-v1 |
| Persistent-by-default collections (final) | §13 collections benchmark |
| Minimal-version selection for deps | ecosystem pressure |
| `Machine` behaviour | a compiled example (R1) |
| `Agent` behaviour | a compiled example (R1) |
| Approvals library (decision types, visualisations, routing, audit, channels) | v0 gate; design notes in `docs/revision-plan.md` §10 |
| `temporal` and `restate` workflow runtimes | `embedded` passing the gate |
| Providers beyond one per role, `custom` provider | ecosystem pressure |
| `any T where` and derived property checks | boundary contracts passing the gate |
| Persistent collections as a data structure | §13 collections benchmark |
