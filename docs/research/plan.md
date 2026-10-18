# Research plan

Nine tracks, run in parallel, each producing one report in this directory of
about two pages (hard cap ~1,200 words plus a short source list). Each report
must end with a **Recommendations for jippitese** section that names any
decision in `docs/decisions.md` the evidence reopens.

Report format: `docs/research/<letter>-<slug>.md`, sections: Summary,
Findings (per system studied), Trade-offs, Recommendations for jippitese,
Sources (URLs).

## (a) Pattern catalogs — `a-pattern-catalogs.md`
Systems: Erlang/OTP behaviours (gen_server, gen_statem, supervisor), Encore,
Dapr building blocks, Rails conventions, Spring Boot starters/stereotypes.
Questions:
1. What is the minimal set of patterns each system found worth naming?
2. How does each express a pattern: interface, base class, annotation, config?
3. What lifecycle hooks are universal (init, handle, terminate, health)?
4. Which patterns compose, and how are compositions checked?
5. Proposed v0 behaviour list for jippitese with required effects per behaviour.

## (b) Effect systems — `b-effect-systems.md`
Systems: Koka, Unison abilities, Effekt, Flix, OCaml 5 effects.
Questions:
1. Row-polymorphic effects vs. capability passing vs. simple effect sets: cost
   in signature size and in explanation effort.
2. How do they handle effect polymorphism in higher-order functions (`map`
   over an effectful closure)?
3. Which parts are needed if the goal is *tracking* (for review, config
   attachment, instrumentation) rather than user-defined handlers?
4. Compile-to-Go implications: can effects be erased, or do they need runtime
   representation?
5. Recommend the effect surface for jippitese v1.

## (c) Language features vs. LLM accuracy — `c-llm-code-accuracy.md`
Questions:
1. What does published work say about LLM accuracy on low-resource languages
   (e.g. MultiPL-E, studies on new/niche languages, few-shot grammar prompting)?
2. Which syntax choices help or hurt: braces vs. indentation, keyword
   familiarity, explicit types, verbosity?
3. Token efficiency: measurements of tokens per program across languages.
4. Evidence on whether in-context specs/interface files improve accuracy.
5. Recommendations: syntax rules and spec-writing rules for jippitese.

## (d) Context-bounded module systems — `d-module-systems.md`
Systems: Unison (content addressing, codebase manager), OCaml `.mli`
interfaces, Elm packages and semver enforcement, Go packages and `go doc`.
Questions:
1. How does each separate interface from implementation, and who writes the
   interface?
2. What does "the minimum text to read before changing module X" look like in
   each?
3. Content addressing: benefits (no name conflicts, perfect caching, rename
   safety) vs. costs (tooling, diff readability, learning curve). Verdict for
   jippitese.
4. Drift detection between interface and implementation.
5. Recommend the module/interface/context-pack design.

## (e) Agent and workflow runtimes — `e-agent-workflow-runtimes.md`
Systems: Temporal, Restate, Inngest, LangGraph, Mastra, Anthropic Agent SDK.
Questions:
1. Durable execution model: replay, journaling, determinism constraints.
2. How is nondeterminism (time, random, LLM calls) isolated?
3. Agent loop abstraction: what is the shared shape across frameworks?
4. Human-in-the-loop primitives.
5. What must the language own vs. delegate to a runtime library? Recommend the
   `Workflow` and `Agent` behaviour shapes and the replay-safety rule for
   effects.

## (f) Contracts and property testing — `f-contracts-property-testing.md`
Systems: Dafny, Eiffel, SPARK, Hypothesis (plus QuickCheck lineage).
Questions:
1. Contract syntax that stays readable and short.
2. Runtime checking cost and how systems let you turn it off in production.
3. Deriving property tests from contracts: what is automatable?
4. Where contracts fail in practice (quantifiers, side effects, frame
   conditions).
5. Recommend contract syntax and the contract→property-check rule.

## (g) Compile-to-Go precedents — `g-compile-to-go.md`
Systems: Borgo, XGo (Go+), templ, Gleam (multi-backend), plus any others found.
Questions:
1. How do they map ADTs, generics, Option/Result and pattern matching to Go?
2. Error messages and source maps: how do they point back to the source?
3. Performance overhead measured vs. hand-written Go.
4. Build pipeline: embedding `go build`, module handling, caching.
5. Pitfalls to avoid; recommended lowering strategy for jippitese.

## (h) Config and bootstrap — `h-config-bootstrap.md`
Systems: Encore (infra from code), Wasp, StrictYAML, CUE.
Questions:
1. Typed config: how each validates and reports errors.
2. Environment overlay rules (env vars, per-environment files, secrets).
3. One-line cross-cutting switches (telemetry, auth): precedents and pitfalls.
4. Dependencies without a registry: git-pinned and vendored precedents (Go
   pre-modules, Deno, Zig).
5. Recommend the `app { ... }` schema outline and the env overlay rule.

## (i) Go interop downsides — `i-go-interop.md`
Questions:
1. Precedents for FFI-with-effects (Haskell `unsafePerformIO`, Rust `unsafe`,
   Unison `Scope`/`IO`): how do they keep guarantees honest?
2. Drift risk: languages where interop became the path of least resistance
   (Kotlin/Java, Scala/Java, Elixir/Erlang, Clojure/Java). What happened?
3. Stdlib design: thin wrappers over host vs. native implementations; effects
   on portability and on the "tokens to read" goal.
4. Backend lock-in: cost of a future non-Go backend if `extern` is widespread.
5. Recommend a gating rule: who may declare `extern`, where, and what the
   compiler requires (e.g. effect annotation, audit list in `app.jip`,
   stdlib-only by default).
