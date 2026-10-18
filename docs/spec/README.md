# jippitese language specification (v0)

Status: **draft**. Governed by `docs/decisions.md`; where the two disagree,
`decisions.md` wins and this spec is wrong.

Primary reader: an agent about to write or change `.jip` code. Secondary
reader: a human reviewing that code. Read `00-language-card.md` first; it is
the ~2k-token summary agents load before writing. Each later section goes
deeper on one construct.

## Sections

| File | Covers |
|---|---|
| `00-language-card.md` | Every construct in one compiling example |
| `01-lexical-structure.md` | Files, `module` line, tokens, literals, comments, statement termination |
| `02-types.md` | Primitives, records, enums, newtypes, `Option`/`Result`, collections, generics |
| `03-functions-and-effects.md` | `fn`, signatures, effect sets, closures, `impl` methods |
| `04-expressions-and-control.md` | `let`/`var`, blocks, `if`, `match`, loops, `?`, operators |
| `05-contracts.md` | `pre`, `post`, `inv`, levels, derived property checks |
| `06-behaviours.md` | `behaviour`, `impl … for`, the eight v0 behaviours, child specs |
| `07-agents-and-workflows.md` | `llm`, `decide`, `approve`, tools, `Workflow`, `Agent` |
| `08-concurrency-and-mutation.md` | `spawn`/`task`, `Cell`, `mut`, persistent and mutable collections |
| `09-modules-and-context.md` | `module`, `use`, `pub`, interface files, `jip check`, context packs |
| `10-config.md` | `app.jip` schema, overlays, dependencies, child specs |
| `11-extern.md` | Go interop, allowlist, effect floor |
| `12-testing.md` | `scenarios.jip`, Given/When/Then, `any` generators |
| `13-tooling-and-diagnostics.md` | `jip` CLI, diagnostics format, build pipeline |
| `grammar.md` | Consolidated grammar |

## How each section is written

1. **Example first.** A minimal `.jip` example, then the Go or TypeScript
   equivalent so the section doubles as few-shot context.
2. **Rules.** Numbered, each one checkable by the compiler or `jip check`.
3. **Diagnostics.** At least one example error in the v0 format
   (`file:line:col`, expected vs. found, suggested edit).
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
- **Closures.** `(x) => expr` or `(x) => { … }`; parameter types inferred
  from context, annotations allowed `(x: Int) => …`.
- **Method calls.** `x.f(y)`; methods take `self` first; the implementing
  type is `Self`.
- **Generic calls.** `json.decode<Receipt>(s)`; `llm<smart>(prompt, input)`,
  `decide<fast>(input)`, `approve(review)` infer `T` from the binding
  annotation when it is not written.
- **Literals.** `1_000`, `3.5`, `"text {expr}"` (interpolation, `{{` for a
  literal brace), `b"bytes"`, `'c'`, `true`, `[1, 2]` for lists, durations
  `5s 100ms 48h`. No map literal; `Map.of((k, v), …)`.
- **Comments.** `//` line comments; `///` doc line, mandatory on `pub` items,
  one line, first sentence is the summary.
- **Effects.** `with {io, net, mut, llm<role>, decide<role>, nondet, spawn,
  approve}` after the return type; omitted means pure.
- **Contracts.** `pre`, `post`, `inv` lines between signature and body.
- **Scenarios.** `scenario "name" { given … when … then … }`, one binding
  per `given`/`when` line, one boolean per `then` line, `any T where expr`
  for generated values, `@extern` tag line before `scenario`.
