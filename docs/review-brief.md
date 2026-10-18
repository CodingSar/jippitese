# jippitese v0 spec: brief for an independent critic

You are reviewing the design of **jippitese**, a new programming language
(`.jip` files, `jip` CLI). Everything you need is in this repository; this
brief tells you what was decided, why, what is still open, and where to look.
Your job is to find the flaws before any compiler code is written.

## 1. What the project is

- **Goal.** A general-purpose, statically typed language "optimized for
  agentic workflows, human understanding and quality abstractions that LLMs
  can work with." Agents write most of the code; humans review it.
- **Two pillars.** *Verifiability* (effects, contracts, scenarios, drift-checked
  interface files) and *bounded context* (a module's public surface must fit
  in a small, mechanically extracted context pack).
- **Performance target.** Go-class. The compiler emits Go.
- **Non-goals for v0.** Language server, solver-based verification, a
  package registry, exceptions, inheritance.

## 2. Where things are

| Path | What it is |
|---|---|
| `docs/decisions.md` | Normative decision log (§1–16 plus resolved and deferred tables). Wins over the spec where they disagree. |
| `docs/spec/00-language-card.md` | Every construct in one ~2k-token example; what an agent loads before writing `.jip`. |
| `docs/spec/01` … `13` | One section per construct: example, numbered rules, a diagnostic, grammar fragment. |
| `docs/spec/grammar.md` | Consolidated grammar with a "Reconciliations" list of notation conflicts it resolved. |
| `docs/research/a` … `i`, `synthesis.md` | Research tracks that fed the decisions (pattern catalogs, effect systems, LLM code accuracy, module systems, agent runtimes, contracts/property testing, compile-to-Go, config/bootstrap, Go interop). |
| `compiler/`, `stdlib/`, `examples/`, `evals/` | Placeholders with a README each. **No compiler code exists.** |

State: spec is complete as a v0 draft (about 3,950 lines across spec and
decisions). Nothing has been implemented or executed. No example program has
ever been compiled, because there is no compiler.

## 3. Core design decisions

**Implementation.** Compiler written in Go, emits Go source to `.jip/gen/`,
built with `go build -trimpath -mod=vendor` on a pinned toolchain. Garbage
collected only. `//line` directives map Go positions back to `.jip`. Build
cache keyed on source hash plus interface digests.

**Lowering.** Small non-recursive enums become a flattened tagged struct;
large or recursive enums become a sealed interface. `Option<T>` lowers to
`{v T; ok bool}`, `Result<T,E>` to `{ok bool; v T; e E}`. `match` lowers to a
`switch` with no default (exhaustiveness is checked by the jip compiler).
Persistent collections are an own stdlib (HAMT map/set, RRB vector);
`MutList/MutMap/MutSet` lower to native slices and maps.

**Types.** Records, enums (sum types with payloads), newtypes (`type Money(Int)`),
generics with bounds (`K: Ord`), `Option`/`Result`, no null, no inheritance.
`Int` wraps on overflow. Full signatures at module boundaries, local inference
in bodies.

**Effects.** Closed label set: `io, net, mut, llm<role>, decide<role>, nondet,
spawn, approve`. Written `-> T with {…}` after the return type; omitted means
pure. Effects erase to one hidden leading context parameter in Go. Closures
take the effect set of their enclosing context ("contextual closure rule").
Annotated effect sets on parameters are upper bounds. Effect-set variables
are lowercase and support union only. Unused declared effect is warning
W0401.

**Contracts.** `pre`, `post`, `inv` lines between signature and body. `ret`
names the return value; `old(e)` is allowed only in `post` and `e` must be
pure over parameter names. `forall`/`exists` exist only inside contracts.
An `impl` method may add `post` but never `pre`. Check levels
`contracts: all | pre | none`; release default is `pre`. Contracts double as
generators for derived property checks; failing seeds are saved under
`.jip/seeds/`. Scenarios always run with `contracts: all`.

**Concurrency and mutation.** Structured only: `spawn { … }` scopes a block
that joins or cancels its children; `task expr` yields `Task<T>`; `join()`.
Capture rule: a spawned task may capture only `let` bindings and `Cell<T>`.
`Cell<T>` is the only shared mutable state; every operation on it is `mut`.
`.freeze()`/`.thaw()` convert between mutable and persistent collections in
O(n). Immutable by default; `var` for local mutable bindings.

**Errors.** `Result`/`Option` plus `?`. Error types are ordinary enums.
`panic` only for bugs and contract violations; not catchable.

**Behaviours.** Eight in v0: `Service`, `Worker`, `Cron`, `Repository`,
`Cache`, `Workflow`, `Agent`, `Machine`. Lifecycle `init/handle/stop/health`.
Child specs in `app.jip` carry restart policy (`permanent`, `max_restarts`,
`period`). `impl Behaviour for Type { … }` with associated types and consts.

**Agent primitives.** `llm<role>(prompt, input) -> Result<T, LlmError>` where
`T` is the output schema. `decide<role>(x)` for System One models; output is a
closed payload-less enum, a bounded score, or a calibrated bool.
`approve(Review { title, body, timeout })` is a durable named suspension.
Tools are derived from function signatures and doc lines. Roles, not model
names, appear in code; `app.jip` maps roles to providers
(`openai | anthropic | jev | ollama | custom`). `Workflow.run` journals every
effectful call as a step keyed `<fn>#<ordinal>`; `mut` is forbidden in the
body; replay mismatch is a typed `ReplayError`. Runtime chosen by
`workflow: embedded | temporal | restate`; `embedded` (SQLite) is the default.
`Agent` has `State`, `Answer`, `const model`, `const tools`, `const budget`
(`max_turns` and `max_cost` both required), pure `stop`, and a `gate` that
returns `Allow | Deny(reason) | Ask`.

**Modules and context.** Directory = module. `module a.b.c` is the first line
of every file. `pub`, `pub(module)`, `pub opaque`. The compiler extracts
`interface.jipi` per module with a sha256 digest; it is committed and
`jip check` fails on drift. `jip check` classifies API changes as
`breaking | compatible | none`. `jip context <module>` emits a two-tier
context pack. One-line doc is mandatory on every `pub` item. Diamond
dependencies are a compile error in v0.

**Configuration.** Single `app.jip` with `deps` (`git(url, commit, hash)` or
`gomod`), `models`, `secrets` (filled from `JIP_SECRETS__<NAME>`), `topics`,
`effects` policies (timeouts, retries), `telemetry`, `contracts`,
`workflow`, `run <Type> { child spec }`, and `env <name> { overrides }`.
Overlay order: `app` < `env` block < `JIP_<PATH>` variables; only values
marked `default` may be overridden.

**Extern.** `extern fn … = go "<path>".Ident`. Allowed only in stdlib or
allow-listed modules. Each extern declares an effect floor and needs an
`@extern` scenario. `unsafe pure` marks a pure extern outside the allowlist.

**Testing.** Behaviour-Driven Development (BDD) scenarios only; no unit test
framework. `<module>/scenarios.jip` with `given* when then+` and `any T where
…` generators. Fakes: `FakeHttp`, `FakeClock`, `FakeRandom`, `FakeLlm`,
`FakeDecide`, `FakeApprove`, `MemRepo`, `MemCache`. `jip test` runs scenarios
and derived property checks.

**Tooling and diagnostics.** `jip build | test | check | fmt | context`.
Diagnostics: `error[Ennnn]: msg`, `--> file:line:col`, source gutter,
`= note:`, `= help:` with a concrete edit. Code families E01xx modules through
E08xx scenarios, E9999 internal.

**Syntax.** Go/TypeScript shape on purpose (LLM accuracy research, track c).
Semicolon-free, newline-terminated. `(x) => body` closures. String
interpolation `"{e}"`. Type arguments use `<`, disambiguated from less-than
by an adjacency rule (01 rule 15: no whitespace before `<`).

## 4. Evaluation plan

Compare against Go, TypeScript and Python on: performance, abstraction
quality, understandability, intuitiveness, tokens to read a context pack,
and iterations to compile. Multi-model. The eval harness is built after the
spec and before the stdlib. The team budgets for a Gleam-sized accuracy gap
(models write unfamiliar languages worse) and mitigates with an `examples/`
corpus. A collections benchmark must run before v1 locks persistent-by-default.

## 5. Decisions the user has explicitly ratified

Positioning, Go backend, effect system shape, contracts shape, eight
behaviours, workflow step rule with library-owned journal, config overlay
order, BDD-only testing ("we don't need unit tests, only BDD tests"),
`<module>/scenarios.jip`, diamond dependencies as a compile error, `pre` as
the release default, interface digests instead of content-addressed code.

## 6. Rules invented while writing the spec, not yet reviewed by the user

Scrutinise these hardest. Each was a judgment call made to fill a gap.

- `Cell.new` is itself `mut`.
- Identifiers are ASCII-only.
- `Int` wraps on overflow (no trap, no bigint).
- No `if let`; `match` is the only destructuring conditional.
- The `<` adjacency rule for type arguments versus less-than.
- A static `init(cfg: Config)` on all eight behaviours.
- `Machine` uses `activate/act`; `Agent` uses `begin/observe` plus a
  `BudgetExceeded` outcome.
- `Agent.budget` requires `max_cost`, not just `max_turns`.
- `nondet` is in the default effect budget of `Service`, `Worker`, `Cron`.
- `WorkflowError` has a fixed variant set, and `?` inside `run` lifts any
  `Show` error into `WorkflowError.Failed`.
- `Review.body` is required, not optional.
- `Service` default transport is `POST /<name>`.
- `Cron` schedules are `every <Duration>` or `cron "<fields>"`.
- Role literals are bare identifiers (`llm<smart>`), not strings.
- `deps` support exactly two sources, `git` and `gomod`.
- A `custom` model provider exists alongside the named ones.
- `unsafe pure` as the escape hatch for externs.
- Module-level `const` appears in the grammar but has no rule text.
- Impls are addressed by the type name (`impl Service for BillingApi`),
  with no inherent impls.
- Effect erasure to a single hidden leading context parameter.
- Lowering choices for enums, `Option`, `Result` (section 3 above).

## 7. Known gaps and risks

- **Nothing has been compiled.** The language card claims "everything below
  compiles under the v0 spec"; that is an assertion, not a test result.
- **Grammar ambiguity.** The `<` adjacency rule, newline termination with
  multi-line expressions, and `match` arms as both statements and expressions
  were reconciled on paper only.
- **Effect erasure cost.** One hidden context parameter on every effectful
  function is cheap in Go, but the interaction with closures, `Task<T>`,
  and extern boundaries is described, not proven.
- **Journaled workflows.** Keying steps by `<fn>#<ordinal>` across
  `if`/loops/`spawn` is the hard part of durable execution; the spec asserts a
  deterministic ordinal without a precise definition of the ordinal under
  branching.
- **Contracts as generators.** `any T where pred` falls back to filtering a
  generic generator; sparse predicates will time out. No cost model given.
- **Token-budget claim.** "Tokens to read a context pack" is the headline
  metric, and there is no baseline number yet.
- **BDD-only testing** makes small pure helpers awkward to test; the spec
  answers with derived property checks, which depend on contracts being
  written.
- **Go generic methods.** Methods cannot have their own type parameters
  before Go 1.27; the spec lifts them to free functions, which changes call
  syntax in generated code but should not surface in `.jip`.
- **Persistent collections** cost about 2× on inserts in the research
  measurement; the default is provisional.
- **Scope.** Eight behaviours, three workflow runtimes, five model providers,
  an own persistent-collections library, and a property-testing engine, all
  before v1, with zero lines of compiler.

## 8. Questions for the critic

Answer each with a verdict, the strongest counter-argument, and what you
would change.

1. Is the closed effect label set sufficient, or will real programs need
   user-defined effects immediately? Is `nondet` a useful label or noise?
2. Does "emit Go" hold up? Consider GC-only, no tail calls, generic method
   limits, error-prone `//line` mapping, and whether a bytecode VM or
   LLVM would serve the goals better.
3. Are contracts-as-generators a sound basis for the testing story, or will
   `any T where` degrade into unusable filtering?
4. Is BDD-only testing viable for a general-purpose language? What breaks
   first?
5. Is the "tokens to read" metric measurable and meaningful? How would you
   define it so that it cannot be gamed?
6. Find grammar ambiguities. Start with the `<` adjacency rule, statement
   termination, closures `(x) => { … }` versus blocks, and `match` as
   expression versus statement.
7. Is the eight-behaviour set right? Which would you cut, merge, or add?
   Is a `Machine` behaviour earning its place?
8. Is "diamond dependency = compile error" workable for v0, or does it block
   any nontrivial dependency graph?
9. Is the durable workflow step rule (`<fn>#<ordinal>`, `mut` forbidden,
   typed `ReplayError`) precise enough to implement deterministically?
   Define a program where the ordinal is ambiguous.
10. Does the `approve` effect as a durable promise generalise beyond
    workflows, or should it be workflow-only?
11. Is effect erasure to a single hidden parameter the right ABI, versus
    capability objects passed explicitly?
12. Is the interface-file plus digest plus `jip check` loop enough to keep
    agents from silently breaking APIs? Where does it leak?
13. Review the invented rules in section 6. Which are wrong, which are
    under-specified, which should be reversed?
14. Rank the risks in section 7 by likelihood of killing the project, and
    name any risk missing from the list.
15. What is the smallest subset of this spec that should be implemented
    first to test the two pillars, and what in the spec should be cut from
    v0 entirely?

Be specific: cite the section and rule number you are objecting to, and
propose the replacement rule text.
