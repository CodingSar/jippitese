# jippitese — design decisions

Status: **pre-spec**. This file is the record of the design interview that
precedes the language spec. Every entry is a decision the project owner made;
entries marked *deferred* are open and waiting on research in
`docs/research/`. The spec (`docs/spec/`) must not contradict this file; if it
needs to, this file changes first.

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

## 3. Type system

- Strict static typing. No `any`-style escape in user code.
- Algebraic data types (sum and product types) with **exhaustive** `match`.
- **No null.** `Option<T>` only.
- Structural generics with constraints. Generic constraints are behaviours
  (see §5).
- **Full type signatures at every function boundary. Local inference only**
  (inside bodies).
- No higher-kinded types in v1.
- No implicit conversions. No operator overloading.

## 4. Verification

- **Effect types are in the core.** Tracked effects at minimum: `io`, `net`,
  `mut` (shared mutation), `llm`, `decide`, `nondet` (randomness/time),
  `spawn`, `approve` (human approval). A function's signature declares its
  effects; the compiler checks them.
- **Contracts** (`pre`, `post`, invariants) are part of the language and are
  runtime-checked. Contracts also generate property checks for the test
  runner.
- Solver-based static proof (SMT) is a **v1 non-goal**.
- **Design principle: effects are the instrumentation points.** Observability
  (OpenTelemetry, OTEL), retries, timeouts, rate limits, auth and similar
  cross-cutting concerns attach to effect types through app configuration,
  never through code at call sites. Example: a single `telemetry: otel` line
  in `app.jip` instruments every effectful call.

## 5. Behaviours (reusable patterns)

- A **behaviour** is a stdlib-defined pattern contract: an interface, its
  associated types, its required effects, its contracts, and a lifecycle.
- Planned v0 behaviours: `Service`, `Worker`, `Pipeline`, `Workflow`, `Agent`,
  `Cache`, `Repository` (final list is a research output, track (a)).
- **One `impl` construct** implements any behaviour. No per-pattern syntax.
- Applications are composed from behaviours; the compiler checks that an
  `impl` satisfies the behaviour completely (methods, effects, contracts).

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
- **Durable workflows** are a behaviour (`Workflow`), with replay-safe
  effects. Runtime design is a research output, track (e).
- **Human approval** is an effect (`approve`) so that a reviewer can see
  from a signature where a human is in the loop.
- **Agent loop** is a behaviour (`Agent`): model, tools, stop condition,
  budget.

## 7. Concurrency and mutation

- **Structured concurrency only.** No detached tasks. `spawn` is an effect
  scoped to a block that joins or cancels its children.
- Immutable by default. `var` for local mutable bindings.
- Shared mutation only through effect-tracked cells (`mut` effect).
- Persistent (immutable) collections by default, with explicit mutable
  variants.

## 8. Errors

- Errors are values: `Result<T, E>` and `Option<T>`; `?` propagates.
- Error types are ordinary ADTs. No exceptions.
- `panic` is for bugs only and is not catchable in user code.

## 9. Modules and context

- A module is a directory. Implementation files mark exported items with
  `pub`.
- **Interface files are extracted by the compiler, committed to the repo, and
  checked for drift in CI** (`jip check`). They are the canonical thing an
  agent reads before editing a module.
- **`jip context <module>`** emits a *context pack*: the module's interface plus
  the interfaces of its dependencies, nothing else. "Tokens to read a context
  pack" is an eval metric (§13).
- **One-line doc comment is mandatory on every `pub` item**, compiler-enforced.
- Content-addressed code (Unison-style): *deferred* pending track (d).

## 10. Configuration and bootstrap

- **Single `app.jip`** containing a typed `app { ... }` declaration. No YAML,
  no JSON config. Compiler-validated against stdlib config types.
- Environment-variable overlay by one fixed rule (research output, track (h)).
- Dependencies are declared in `app.jip` as git URLs pinned by commit and
  vendored. **No package registry.** The stdlib is the only first-party
  dependency.
- Opinionated one-line switches for cross-cutting concerns (see §4
  principle), e.g. `telemetry: otel`.

## 11. Syntax

- C/Go/TypeScript-shaped: braces, `fn`, infix operators, familiar literals.
- **One way to do each thing.** No macros in v1. No operator overloading. No
  implicit conversions.
- One canonical formatter (`jip fmt`) with zero options.
- Terseness policy: terse where it protects the context window; explicit
  where terseness would cost understanding. The eval decides (§13).

## 12. Interop

- Go interop through `extern` declarations that carry declared effects.
  **Accepted provisionally.** Track (i) must report the downsides (drift
  toward writing Go, thin-wrapper stdlib, leaked effect guarantees, backend
  lock-in) before the gating decision is made. *Deferred.*

## 13. Evaluation

- Evals compare jippitese against Go, TypeScript and Python on:
  performance, abstraction, understandability, intuitiveness, and
  **tokens to read** (whole program, and the context pack needed to make a
  given change).
- Run across multiple models. The eval harness is built after the spec and
  before most of the stdlib.
- The spec is written with agents as the primary reader and is itself
  measured by the intuitiveness eval.

## 14. Testing

- **BDD (Behavior-Driven Development) tests only. No unit-test files.** The
  test suite is a set of scenarios written against behaviours and public
  interfaces. File naming and scenario format: to be fixed in the spec.
- Contracts generate property checks automatically (§4); these run as part of
  `jip test` but are not hand-written tests.

## 15. Tooling

- `jip` CLI: `build`, `test`, `fmt`, `check`, `context`, `new`.
- Language server: deferred.
- v0 stdlib: core types, collections, strings, JSON, HTTP client and server,
  filesystem, time, testing, `llm`, tools.

## 16. Project

- License: Apache-2.0.
- Layout: `docs/research/`, `docs/spec/`, `docs/decisions.md`, `compiler/`
  (Go module), `stdlib/`, `evals/`, `examples/`.
- Process: research (`docs/research/plan.md`) → synthesis → spec → eval
  harness → compiler v0 → stdlib → evals.

## Deferred decisions

| Item | Waiting on |
|---|---|
| Go `extern` interop gating | track (i) |
| Content-addressed code | track (d) |
| Final behaviour list and lifecycle shape | track (a) |
| Effect system surface (handlers vs. capability passing vs. row types) | track (b) |
| Durable workflow runtime model | track (e) |
| Contract syntax and property-test derivation | track (f) |
| Config env overlay rule | track (h) |
| Syntax-level choices affecting LLM accuracy | track (c) |
| Language server | post-v1 |
| Solver-based verification | post-v1 |
