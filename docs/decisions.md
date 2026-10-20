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
  `go build -trimpath -mod=vendor`. An extern-check cache under
  `.jip/cache` and `GOCACHE` on the emitted Go (§9, §13 rule 12).
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
- **Property derivation (v1).**
  For every `pub fn` whose effect set is empty or
  `{mut}` on fresh state, and that has a `post` or whose parameter or return
  types carry `inv`, `jip test` derives one property check: generators from
  types narrowed by `pre`, N runs under `contracts: all`, oracle is `post`,
  every `inv` and no panic, integrated shrinking, shrunk seed persisted as a
  regression case. For an `impl` with `inv`, a stateful check calls `init`,
  then draws method sequences and checks `inv` after every call. Functions with `io`, `net`, `llm` or `decide` get no derived
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
  `Repository`, `Cache`. **`Machine` and `Agent` were deferred** from v0:
  `Machine` returned in v1 with `examples/doors`; `Agent`, a stdlib loop
  over `Workflow`, returns only with a compiled example. `Cron` and `Cache`
  stay because every v0 behaviour must go through the same dictionary
  lowering, run lines and `app.jip` overlay from the start rather than be
  retrofitted. No `Pipeline`: chain `Worker`s over the stdlib type
  `Topic<T>`. `Secret` is a stdlib type, not a behaviour.
- **Lifecycle** is fixed to `init / handle / stop / health`; `health` has
  defaults (liveness = booted, readiness = aggregate of `Repository` and
  `Cache` impls). `init(cfg: Config)` receives the `Config` record defined
  in §10. `Machine` adds `deactivate`.
- **Effect budget.** Each behaviour fixes the maximum effects an `impl`
  method may declare (table in `docs/research/a-pattern-catalogs.md`); the
  runtime supplies the rest.
- **`Machine`** (v1) is a closed `State × Event` exhaustive match, one
  turn per key, state persisted per turn; side effects only through returned
  `Action`s.
- **Stdlib-provided contracts** for behaviours (`Worker` idempotency,
  `Repository` get-after-put round-trip) are derived checks
  (`<Impl>.idempotent`, `<Impl>.round_trip`), not `post` clauses.
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
  `embedded` (one JSON-lines journal file per run, single process) is the v0 default and the only
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
  variants. **Flag:** track (g) measured roughly 2× insert cost for HAMT
  maps; the §13 collections benchmark (L8, `rt/collections_bench_test.go`)
  measured the v1 HAMT at about 17× a native Go map for 4096 inserts and
  about 2× for lookups, against about 1500× and 10000× for the v0 copying
  map. The default stays; `List` is still a copying slice.

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
  only on `net`). `custom(url, model)` (v1) is any OpenAI-compatible endpoint. For `gomod` dependencies
  `jip build` synthesises `vendor/modules.txt`, since `go build
  -mod=vendor` rejects a vendor tree without it (v1, see the table).

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
- **Tokenizer:** `o200k_base` from `js-tiktoken` 1.0.21 (`evals/package.json`).
  **Task set:** the twelve changes frozen in `evals/tasks/` (Phase F), restated
  for the Go baseline in `evals/go/tasks/` (v1, F5).
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
  a second test kind. `any T where`, derived function checks and derived
  stateful checks are in v1.
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

## Implementation decisions (Phase E)

Settled while building the gate compiler. Each either fills a gap the spec
left open or is a spec change made in the same commit. The owner may
reverse any row; reversing one is a spec change.

| Item | Decision |
|---|---|
| `init` budget | `{io, net, mut}` for every behaviour; `Repository` and `Cache` methods carry `mut`. Without it no impl can construct in-process state (spec 06 updated). Ratified after the v0 gate (backlog D1, V2). |
| Standard library shape | Every std function is `extern fn … = go "rt".X`; the generated tree has no `std` package. |
| `Query<T>` | `Query.all()` is the v0 constructor; queries carry no filter. |
| Workflow replay in scenarios | Builtins `record_workflow(Impl, input) -> (Result, Journal)` and `replay_workflow(Impl, input, journal)`; `std.testing.Journal` is opaque with `len()`. |
| `Ord` bound | A type parameter bounded by `Ord` lowers to Go `cmp.Ordered`; other bounds are checked and erased. |
| Unary minus on `Int` | Lowers to `rt.Sub(ctx, 0, x)` and traps on overflow like every `Int` operation (R2). |
| Extern codegen | Direct call with inline conversions (11 rule 9); `rt.IoErrorOf`, `NetErrorOf`, `GoErrorOf`, `MapToGo`, `MapFromGo` carry the boundary. Effect floor from the target package itself, imports not unioned: `os` imports `time` and `sync`, and a union would put every file extern at `{io, nondet, mut}` (spec 11 rule 5 updated). |
| `rt` as an extern target | The toolchain's own package: no `gomod` entry, exempt from 11 rules 3–8; a `Result<R, E>` result instantiates rt's generic `R, E` (spec 11 rule 1 updated). |
| Extern error types | Any extern in any module may return `GoError`, `IoError` or `NetError`; the card's app-module `read_file` returns `IoError` (spec 11 rule 3 updated). |
| Workflow journal | `embedded` writes one append-only JSON-lines file per run, `<store>/<Impl>-<run id>.jsonl`, default store `.jip/workflow`; no SQLite dependency (spec 07 rule 16 and 10 updated). |
| Config overlay paths | A config leaf is `config.<name>` (`JIP_CONFIG__<NAME>`), secrets `secrets.<name>`, run fields `run.<Impl>.<field>`. Scenarios load the `env test` block whatever `JIP_ENV` says. |
| Worker topics | A `run X { topic: t }` subscribes the Worker from a Go `init()`; in scenarios `publish` delivers synchronously. |
| Interface file layout | Contract lines print as `pre  …`, `post …`, `inv  …`; effects in canonical order; impl members indented two spaces. |
| API classification of const values | Changing a `const` value, including a workflow `version` bump, is `compatible`: a value is not a signature, and spec 09 rule 8 lists no such break. |
| Dependency tree hash | SHA-256 over the sorted lines `<path> <sha256 of content>` of every regular file, `.git` excluded, written `sha256-<base64>`. |
| Toolchain | `compiler/toolchain.go` pins Go 1.24 (`toolchain go1.24.0` in the generated `go.mod`); `jip` uses `JIP_GO` or `go` on `PATH` at or above the pin. A binary cannot embed a Go toolchain; a distribution may ship one next to `jip` (spec 13 rule 11 updated). |
| `any T where` (v1) | The filter sees the candidate under the `given` name; rejected candidates are redrawn from the same generator; 1000 rejections in a row fail the scenario; W0510 is counted per `where` over all runs of the property. |
| Structural `Int` generator | `Size` bounds the magnitude around zero when the range holds zero, else from the end nearest zero. v0 clamped from `lo`, so `any Int` never drew a positive value. |
| `jip fmt` | Keeps every comment (trailing ones aligned), literal text (`1_000_000`, `60s`) and schedule sugar (`every 5m`); a body stays on one line only when written so. Refuses to write a result that would not reparse to the same program. Skips `vendor/`. |
| Derived function checks (v1) | Module-level, non-generic `pub fn`s of app modules only; methods are covered by the stateful check. Each emits `TestDerived_<fn>` in the module's `derived_test.go` and prints `derived <fn>`; the summary counts scenarios and derived checks apart. A check whose `pre` rejects every candidate of 3 runs is reported `skip` with W0510, never `ok`. A saved seed runs before the N fresh runs unless `--seed` pins the run. |
| `pre`-derived bounds | Only `&&` conjuncts are split; a bound side may mention parameters declared earlier, never later, and must lower without statements. An open end is ±2^61, so Size, not the end, bounds the draw; a length bound draws at most 8 above its minimum. Every clause still runs as a filter. `Range<T>` is a prelude extern type with `contains`; a literal range receiver lowers to `rt.InRange`. `any Str` draws up to 32 bytes (was 16). |
| `impl` `inv` at run time | v0 parsed and type-checked an `impl` `inv` but never evaluated it. Each impl with `inv` now gets `<Target>_stateInv`; `init` (on `Ok`) and every method on `self` call it after the body under `contracts: all`. A `Cell` field is read once per check. Under `contracts: pre` and `none` the wrapper skips the call. |
| Repository round-trip | Spec 06 rule 10 is now a derived property, `<Impl>.round_trip`, for each non-generic `Repository` impl whose `K` and `T` generate. It runs on a fresh instance per run with the module's test `Config`. `any Instant` now generates too, within a day of the fake clock's epoch, so records with timestamps qualify. |
| Worker idempotency | Spec 06 rule 8 is now a derived property, `<Impl>.idempotent`, for each non-generic `Worker` impl whose `Msg` generates. Calls to a `Repository` or `Cache` impl go through `rt.RepoGet`, `rt.CacheSet` and siblings, which use the impl's instance unless the ctx substitutes fakes; the check runs `perform` once and twice on two such ctxs and compares the fakes' entries and the final results. The check found `shop` double-counting a redelivered order, so `Placed` gained an `id` and the `Indexer` records counted ids in a `Counted` repository; eval tasks 06 and 07 were re-frozen with the new `Placed` literals before any measurement was taken. |
| Derived stateful checks (v1) | Spec 05 rule 17 for each non-generic `impl` with `inv` whose declared `init` (returning `Ok(self)` or `Self`) and at least one method on `self` are within `{mut}` with generable parameters; any other method is never drawn. `init` is redrawn until `Ok` under the `W0510` count; a step whose `pre` fails is redrawn; `stop` is called last when it qualifies. The check is `derived <Impl>`, seed `<module dir>/<Impl>.seed`, and its `when` line is the call sequence. A generated `Config` always holds `Secrets.fake()`. Shrinking halves the size as rule 16, so a failing sequence is shorter but not minimal. Rule 18 is the dedicated `round_trip` and `idempotent` checks of spec 06, not stdlib `post` clauses. No example `impl` carries `inv` yet, so the check is covered by a driver test that adds one to `shop`. |
| `Machine` (v1) | Spec 06 `Machine`. State is held in process per impl, keyed by the shown `Key`, and is lost on restart; durable state waits for a storage design. Turns of one impl are serialised (the spec's one-per-key holds a fortiori). Under `jip test` `send` runs its turns before returning, so scenarios need no settling step; under `jip build` it queues on the impl's runtime queue (`Err(Full)` when full). A failed `act` is logged, never retried. `idle` on the `run` line (default `5m`) is checked every `idle/2`; the fake clock never deactivates. `act` lacks `mut`, so `examples/doors` records positions through a topic and a `Worker`. E0706 now covers `Agent` only. Interface files now keep every line of a multi-line doc comment (they lost the `///` of all but the first). |
| `Agent` (v1) | Spec 07 rules 17 to 23. Each impl gets a closed `<Target>Tools` enum (`Tools` inside the impl; `lookup` becomes `Lookup`); a user `type Tools` is an error. A tool's parameters are sent as their jippitese type names, its description is the first `///` line, its arguments are decoded from JSON into the parameters (a decode failure goes back to the model as `error: invalid arguments: …`), and an `Err` result goes back as `error: …`. `Gate.Ask` routes through `approve` with the title `<Agent> wants to call <tool>` and the arguments as body and data; a rejection goes back to the model as `denied by a reviewer[: reason]`. The runtime compacts the transcript past 64 entries to the goal plus the last 32 and reports `Compacted`. Inside a workflow the site is the call's step key, so turns, gates, approvals and tool runs replay from the journal. `jip test` scripts turns with `FakeLlm.turns`; a build sends them to the role's provider (L7). An `Agent` is not admitted in the `run` list. E0706 now covers tools (not `pub`, other module, outside an `Agent`). |
| Method externs (v1) | `go "<path>".Type.Method` binds a method of an exported, non-generic named type; the first parameter is the receiver and crosses as `Type` or its underlying type, converted with `Type(x)` into a local so pointer-receiver methods apply. Receiver problems are E0503 (which v0 used for every method extern); a missing method or a mismatched signature is E0506. |
| Model providers (v1) | `rt.Models` routes by role; `rt.ChatClient` speaks OpenAI chat-completions for `openai`, `ollama` and `custom`. `llm<role>` sends the prompt and the input as JSON and asks for `{"value": T}` under `rt.SchemaOf(T)` (the schema follows the JSON encodings: enums as a name or a one-key object, `Option` as `anyOf [T, null]`, a non-string-keyed `Map` as `{F0, F1}` entries, a recursive type open where it recurs); an unwrapped reply is decoded as is, a decode failure is `Schema`, a provider refusal `Refused`, anything else `Transport`. Agent tools go as functions with each parameter described by its jippitese type (scalars also typed); a tool result whose call was compacted away is dropped, since the protocol rejects it. Keys come from `JIP_MODELS__<ROLE>__KEY` (assumed name; `OPENAI_API_KEY` for `openai`). A real turn reports cost 0, so `max_cost` binds only under `FakeLlm` until L7d. |
| `gomod` dependencies (v1) | `gomod("path", commit:, hash:)` clones `https://<path>`; other hosts go through git's `insteadOf`, so the spec needs no URL field. An extern may name any package of a gomod module (longest module path wins); `jip check` type-checks it from `vendor/<alias>` with `go/types`, so no network and no module cache. The generated `go.mod` requires each module at a placeholder `v0.0.0-00010101000000-000000000000` (or `vN` for a `/vN` or gopkg.in `.vN` path) and replaces it with `../../vendor/<alias>`; `.jip/gen/vendor/<path>` is a symlink to the tree and `modules.txt` lists its package directories (testdata, `_`/`.` directories and nested modules skipped). Every `require` of a vendored module needs its own gomod entry (E0504), so the whole Go graph is pinned by hash; its required versions are ignored. A non-stdlib package has no floor: declare its effects or use `unsafe pure`. Only the root `app.jip`'s entries count, as for `git`. |
| Build cache (v1) | Measured before choosing: on `examples/orders`, `jip check` spent nearly all of its ~330ms type-checking the Go packages externs name from source; the jippitese checker is whole-program and takes ~15ms (`triage`: 14ms). So 13 rule 12 caches the extern check, not per-module typecheck/emit, which would need separate compilation in the checker for little gain. A marker file per clean extern under `.jip/cache/externs`, named by a SHA-256 over the target, the declared Go signature and receiver, `runtime.Version`, `GOROOT` and its `VERSION`, every `gomod` alias/path/hash and the `jip` executable's path, size and mtime. Failures are never recorded. The vendored tree is trusted to match its pinned hash, which `jip check` verifies (E0309). Orders: `jip check` ~340ms → 15ms, `jip build` rebuild ~460ms → ~160ms. Spec 09 rule 9 now leaves dependent rebuilds to `GOCACHE`. |
| std.fs (v1) | Functions over `Path` returning `Result<_, IoError>`, all `with {io}`, bound to `rt.Fs*`, which go through `ctx.Fs` (the real file system when nil). `FakeFs` in `std.testing` installs an in-memory tree that mirrors `os` errors (missing parent → `NotFound`, non-empty directory remove fails), so `IoError` mapping is shared. `io` stays real by default: only `std.fs` is routed, an `io` extern into Go is not. `exists` is a keyword, so the check is `present`. `Path.of` cleans its argument. |
| std.http (v1) | `get`, `post` (JSON body) and `request(method, url, headers, body)` return `Result<Response, NetError> with {net}`; every status is a `Response`, only a transport failure is a `NetError` (`Timeout`, `Refused`, `Other`). All `net` goes through `ctx.Http.Do(HttpReq)`, shared with the `http` dependency module. Production is `RealHttp` with the `effects.net` defaults (5s per attempt, 3 retries, exponential from 100ms) on the ctx clock; only idempotent methods retry, and only on a transport failure or 502/503/504, so a POST is never sent twice. Response header names are lower-case. `FakeHttp.route` matches method and exact URL before the scripted replies, and the recorded `Request` now carries method and headers. `use a.b as c` parses `as` contextually, as it is not reserved. |
| `effects {}` (v1) | `app.jip` `effects` is parsed against the 10 r3 schema (unknown record or field, wrong kind, a leaf set twice: E0301) and flattened to `effects.<path>` leaves, each fixed or overridable as config leaves are; a `default` record marks its leaves. The generated `Load` resolves them through the overlay into `rt.Effects` and the main applies them to the ctx: `net.timeout`/`net.retry` configure `RealHttp`, `llm.timeout` bounds each provider request, `approve.*` feeds the approval channel. The rest is resolved but not enforced in v1. `2_000_000 tokens` and `Money(n)` parse as app literals. `JIP_MODELS__<ROLE>__KEY` is a known field, so setting it no longer aborts startup. An env block setting a fixed config or effects leaf is now E0303 at compile time (it was only a startup abort). |
| std.approve.http (v1) | The only approval channel in v1: `effects.approve.channel` naming anything else is E0301 at compile time and a startup error. A loopback HTTP endpoint (`JIP_APPROVE_ADDR`, default `127.0.0.1:8089`, exempt from the unknown-variable check) because it has no authentication. `ApproveClient.Ask` now gets the answer's Go type (`Result[D, Rejected]`) so the channel can decode a reviewer's JSON into `D`; a body that does not decode is a 400 and leaves the review pending. The timeout is the review's, else `approve.default_timeout`, on the wall clock. A shutdown while a review is pending cancels the run and the journal keeps the pending entry; pending reviews are in memory only. `dottedPath` accepts a keyword after a dot so `std.approve.http` parses. |
| Str and number methods (v1, T4a) | What the backlog called `std.strings` lives as prelude methods on `Str`, `Int` and `Float`, not a module: `s.replace(a, b)` reads better and costs fewer tokens than `strings.replace(s, a, b)`, and needs no `use`. Indices are characters, matching `len` (its doc said bytes; it always counted characters); `slice` clamps like `List.take`. Every Float to Int conversion (`floor`, `ceil`, `round`, `to_int`) traps on NaN, infinities and values outside Int, like Int overflow, where it used to wrap silently. `parse_float` rejects NaN and infinities. The `checked_*`, `wrapping_*` and `to_int` methods spec 02 already promised now exist; spec 02 cites `s.parse_int()` instead of a never-built `Int.parse`. `replace` takes `from`/`to` because `old` is reserved. A method call on a bare Int literal (`2.pow(10)`) defaults the literal to Int, per 01 r6; it was E0403. |
| std.math and std.time (v1, T4b, T4c) | `std.math` is free functions over Float (`sqrt`, `pow`, `exp`, `ln`, `log10`, `log2`, trig, `atan2`, `hypot`), with `pi()` and `e()` as functions because a std module emits only externs, not consts; out-of-domain inputs give NaN, as IEEE 754 does, and `is_nan` tests for it. `std.time` gains `before`/`after` (comparison operators are not defined on `Instant`), `sub`, `unix_millis` (rounded toward the past), `to_rfc3339`, UTC calendar fields with ISO weekdays, `from_unix_millis`, `parse_rfc3339` and `utc(...)`, which returns `None` for a field out of range instead of normalising 31 April to 1 May. There are no time zones beyond parsing an offset; Instants are representable from 1678 to 2261 and parsing outside that is `None`. `Instant` arithmetic now traps on overflow like Int. `Duration` gains `minutes`, `hours`, `to_str`, `of_millis`, `of_seconds` and `times(n)`, since `d * n` with `n: Int` is a type error. |
| Json navigation (v1, T4d) | `Json` stays an opaque document with total accessors: `get`, `at`, `as_str`, `as_int`, `as_float`, `as_bool`, `as_list`, `as_map`, `keys` answer `None` when the document has another shape or is not valid JSON, so navigation never traps and chains with `Option` combinators. `as_int` accepts any integral number in Int range (`2.0`, `1e3`), because JSON does not distinguish integers; `as_float` refuses numbers outside Float range. Strings are never coerced to numbers. A duplicated object key keeps its first position and its last value, matching Go and most parsers. There is no separate `items`: `as_map()` keeps source order, so `.entries()` serves. `std.json` gains `parse` (`decode<Json>`) and `of<T>`, and `to_str` is compact text. |
| std.log (v1, T4e) | `std.log` has `debug`, `info`, `warn` and `error(msg: Str)`, writing `level: msg` through the runtime logger (stderr in an app, the scenario's test log under `jip test`, so lines show only when a scenario fails). Logging is not an effect: a log line changes nothing the program can observe, and requiring `{io}` would force every function that wants a trace line to widen its signature. `JIP_LOG` sets the lowest level written (default `info`), read on each call. Messages are plain `Str`; structured fields and routing to `telemetry` wait for telemetry itself. |
| jip lsp (v1, T5a) | `jip lsp` speaks the Language Server Protocol (LSP) over stdio with full-text sync. Every change re-loads and re-checks the whole app the buffer belongs to, with all open buffers laid over the files on disk (`modules.LoadOverlay`), and publishes the diagnostics `jip check` prints from loading and type checking; files whose diagnostics went away get an empty publish. A file outside any app is only parsed. Positions are UTF-16 unless the client offers UTF-8. Formatting replaces the whole document with `jip fmt` output and leaves a buffer that does not parse alone. A checker panic becomes an E9999 diagnostic so the server survives half-typed code. No debounce and no incremental checking yet: apps in the corpus check in milliseconds. |
| LSP navigation (v1, T5b, T5c) | Document symbols come from the parsed buffer alone: items and scenarios, with fields, variants, consts and methods as children; a symbol runs to the last code line before its next sibling, since the AST keeps no end positions. Hover and go to definition read the last check of the app, kept per root: a name resolves through the checker's identifier, field and call tables, and local bindings through its binding tables, so the answer is the one the compiler used. Hover shows the declaration line (a function's header with the effects it uses when it declares none), the type of a local, and the doc comment. Names inside type signatures do not resolve yet, and a buffer that does not load has no hover until it does. Definitions in the standard library return nothing, as there is no file to open. |
| Go baseline (v1, F5) | `evals/go/` holds idiomatic Go versions of the five task projects, standard library only, and `evals/go/tasks/` the twelve tasks restated in Go (`task.md`, a hidden `accept_test.go` in the target package, `solution.patch`, frozen by `MANIFEST`, proved by `evals/validate_go.sh`). Where jippitese has a construct Go lacks, the Go side gets the plainest equivalent, never a framework: behaviours are plain types and methods (`Indexer.Deliver` for a `Worker`, `Store.RunSweeper` on a ticker for a `Cron`); the `Workflow` is a method over a small `durable` journal package (record and replay by step name, a version check), not Temporal, because the scenarios test replay and a Temporal SDK would add a server and thousands of lines an agent may read; repositories are one in-memory generic `Repo` without errors; preconditions are panics, postconditions and invariants are dropped or kept as doc comments (task 01 loses its postcondition); a declared break has no Go counterpart; fakes are a `Now func() time.Time` field (a package var in `billing`), `httptest` and `testing/quick`; there is no `main` or app wiring, since no task touches `app.jip`. Every choice makes the Go corpus smaller than its jippitese counterpart, so a measured jippitese advantage is understated, not inflated. |

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
| Minimal-version selection for deps | ecosystem pressure |
| Approvals library (decision types, visualisations, routing, audit, channels) | v0 gate; design notes in `docs/revision-plan.md` §10 |
| `temporal` and `restate` workflow runtimes | `embedded` passing the gate |
| Providers beyond one per role | ecosystem pressure |
| `anthropic` and `jev` protocols, `decide<role>` on a provider, provider cost into `max_cost` | L7d |
| Structurally shared `List` (RRB vector) | a program where `List.push` in a loop matters after W0520 |
