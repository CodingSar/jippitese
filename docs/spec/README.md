# jippitese language specification (v0)

Status: **draft**. Governed by `docs/decisions.md`; where the two disagree,
`decisions.md` wins and this spec is wrong.

Primary reader: an agent about to write or change `.jip` code. Secondary
reader: a human reviewing that code. Read `00-language-card.md` first; it is
the summary agents load before writing (its measured size is a §13-evaluation
figure, R15). Each later section goes deeper on one construct.

## Sections

The **v0** column says what each section contributes to the v0 gate
(`docs/decisions.md` §0 and R1–R15). *Deferred* items carry a
`*Status: deferred from v0*` banner at the rule and are v1 design, not
v0 behaviour.

| File | Covers | v0 |
|---|---|---|
| `00-language-card.md` | Every construct in one compiling example | all of it compiles under v0 |
| `01-lexical-structure.md` | Files, `module` line, tokens, literals, comments, statement termination | all |
| `02-types.md` | Primitives, records, enums, newtypes, `Option`/`Result`, collections, generics | all; `Int` traps on overflow (R2) |
| `03-functions-and-effects.md` | `fn`, signatures, effect sets, `fn` closures, inherent `impl` (R3) | all |
| `04-expressions-and-control.md` | `let`/`var`, blocks, `if`, `match`, loops, `?`, operators, turbofish | all |
| `05-contracts.md` | `pre`, `post`, `inv`, levels, generators | contracts, generators, `via`, derived function checks; `pre`-derived bounds and derived stateful checks pending |
| `06-behaviours.md` | `behaviour`, `impl … for`, the six v0 behaviours, child specs | `Service, Worker, Cron, Workflow, Repository, Cache`; `Machine` deferred (R1) |
| `07-agents-and-workflows.md` | `llm`, `decide`, `approve`, `Workflow`, `WorkflowError<E>` | all but `Agent` and tools, deferred (R1) |
| `08-concurrency-and-mutation.md` | `spawn`/`task`, `Cell`, `mut`, persistent and mutable collections | all |
| `09-modules-and-context.md` | `module`, `use`, `pub`, interface files, `jip check`, context packs | all; two digests (R7), `--accept-breaking` (R8) |
| `10-config.md` | `app.jip` schema, `config {}`, overlays, dependencies, child specs | all; `custom` provider and non-embedded workflow runtimes deferred |
| `11-extern.md` | Go interop, allowlist, effect floor | all; direct-import floor only (R13) |
| `12-testing.md` | `scenarios.jip`, Given/When/Then, `any T`, `any T via g`, `any T where`, table rows, fakes by default (R14) | all |
| `13-tooling-and-diagnostics.md` | `jip` CLI, diagnostics format, build pipeline | `build`, `test`, `check`, `fmt`, `context`; Go 1.24 (R10) |
| `grammar.md` | Consolidated grammar | v0 grammar; deferred forms marked |

## How each section is written

1. **Example first.** A minimal `.jip` example, then the Go or TypeScript
   equivalent so the section doubles as few-shot context.
2. **Rules.** Numbered, each one checkable by the compiler or `jip check`.
3. **Diagnostics.** At least one example error in the normative format of
   section 13 (`error[Ennnn]`, `-->` file:line:col, source gutter, `= note:`,
   `= help:` with a concrete edit).
4. **Grammar.** The BNF subset for the section. `grammar.md` concatenates
   them.

Target length per section: 800–1,500 words. Nothing is normative that is not
also exemplified.

## Conventions fixed for all sections

- **Naming.** Types, enums, variants, behaviours: `PascalCase`. Functions,
  variables, fields, modules, consts: `snake_case`. Effects: lowercase labels.
- **Statement termination.** Go's rule: a newline ends a statement unless
  the line ends in an operator, comma, or opening bracket. `;` is never
  written.
- **Blocks are expressions.** The final expression of a block is its value.
  `return` is for early exit; a tail `return x` compiles and `jip fmt`
  rewrites it to `x`.
- **Variants.** `Enum.Variant` always works. The bare `Variant` works where
  the expected type is known (patterns, arguments, returns, `Err(...)`).
  `Some`, `None`, `Ok`, `Err` are always bare.
- **Records.** Construct with `Name { field: value }`; update with
  `Name { ..base, field: value }` (spread first, then overrides).
- **Newtypes.** `type Money(Int)`; the payload is `.0`.
- **Tuples and unit.** `(a, b)`, type `(A, B)`; unit is `()`.
- **Closures.** `fn(x) => expr` or `fn(x) => { … }`; parameter types
  inferred from context, annotations allowed `fn(x: Int) => …`. A
  parenthesised list without `fn` is a tuple.
- **Method calls.** `x.f(y)`; methods take `self` first; the implementing
  type is `Self`.
- **Generic calls.** An explicit type argument is the turbofish,
  `json.decode::<Receipt>(s)`, `approve::<Decision>(review)`; a bare `<` in
  an expression is always less-than. `llm<smart>(prompt, input)` and
  `decide<fast>(input)` are keywords with a role, not generic calls. `T` is
  inferred from the binding annotation when it is not written.
- **Literals.** `1_000`, `3.5`, `"text {expr}"` (interpolation, `{{` for a
  literal brace), `b"bytes"`, `'c'`, `true`, `[1, 2]` for lists, durations
  `5s 100ms 48h`. No map literal; `Map.of((k, v), …)`.
- **Comments.** `//` line comments; `///` doc line, mandatory on `pub` items,
  one line, first sentence is the summary.
- **Effects.** `with {io, net, mut, llm<role>, decide<role>, nondet, spawn,
  approve}` after the return type; omitted means pure.
- **Contracts.** `pre`, `post`, `inv` lines between signature and body.
- **Scenarios.** `scenario "name" { given … when … then … }`, one binding
  per `given`/`when` line, one boolean per `then` line, `any T` or `any T
  via g` for generated values, `given rows = [...]` then `given (a, b) in
  rows` for tables, `@extern` tag line before `scenario`.
