# v0 revision plan: acting on the external critique

Status: **proposed**. Input: `docs/review-brief.md` and the critic's answers
to its §8 questions (recorded in the session that produced this file).
Output: a sequence of pull requests that bring `docs/decisions.md` and
`docs/spec/` into one consistent v0, then the first compiled example.

Rule from `decisions.md` line 3: the spec must not contradict decisions, so
every change below edits `decisions.md` first and the spec second. Nothing in
this plan edits the spec ahead of its decision.

## 0. Decisions the owner must ratify before any edit

Each item reverses or narrows something in brief §5 (user-ratified) or §6
(invented). The default applies if the owner does not answer; the plan is
written against the defaults.

| # | Change | Reverses | Default |
|---|---|---|---|
| R1 | v0 behaviours are `Service`, `Worker`, `Repository`, `Workflow`, `Cron`, `Cache`. `Machine` and `Agent` leave v0 (06 r1, decisions §5). | §5 "eight behaviours" narrowed by two | ratified 2026-10-19 |
| R2 | `Int` traps on overflow instead of wrapping (02 r1). Checked ops `checked_add` etc. return `Option<Int>`. | §6 | adopt |
| R3 | Inherent impls `impl T { … }` are allowed; the stdlib already needs them (`Cell.new`, `MutList.new`, `List.builder`). Resolves 03 r4 vs `grammar.md`. | §6 | adopt |
| R4 | `approve` is admitted only in `Workflow.run`, `Agent.gate`, and functions reachable only from them (07 r8, 06 budgets). | §6 budgets | adopt |
| R5 | `app.jip` gains a `config { field: Type }` block; `Config` is that record plus `secrets`. This defines the `Config` type that 06 r5 and 05 r17 use without defining. | new | adopt |
| R6 | Step key becomes `<callee>@<site>#<n>` with a per-task execution counter and journaled site-id set (07 r10, r15, r16, r21). | §5 "step rule" | adopt |
| R7 | Interface file gains a `module-visible` section and second digest covering `pub(module)` items; cache key of modules under the parent includes it (09 r5, r7, r9). | §5 "interface digests" | adopt |
| R8 | `jip check --accept-breaking <module>` or commit trailer `jip-breaking: <module>` lets an intentional break exit 0 (13 r8). | new | adopt |
| R9 | Scenario table form `given rows = [...]` (12 r2) and generator escape hatch `given x = any T via g` (12 r4, 05 r15). BDD-only stays. | §5 "BDD-only" narrowed, not reversed | adopt |
| R10 | Behaviours lower to dictionaries, not Go interfaces; the emitter never emits a generic method; Go pin 1.24, the 1.27 clause is deleted (decisions §2, 02 r19, 06 r16, 13 r11). | §2 | adopt |
| R11 | `WorkflowError<E> = Replay(ReplayError) \| Approval(Rejected) \| Cancelled \| App(E)`; `?` uses the ordinary 04 r11 rule, no `Show` lift (07 r9). | §6 | adopt |
| R12 | Contract violations inside a behaviour method become that method's `Err` (or HTTP 500 for `Service`) at the behaviour boundary instead of a supervisor-visible panic (05 r10, 10 r3 `run` restarts). | §4 "violation is a panic" narrowed | adopt |
| R13 | Extern effect floor uses a direct-import package table only; no transitive union, no goroutine detection (11 r5). | §12 | adopt |
| R14 | `jip test` installs a fake clock and seeded random by default; `given real_time` opts out (12 r10, 05 r16). | §6 | adopt |
| R15 | Every number in 09 r10 and decisions §13 (620, 2,900, 240 tokens) is removed until measured. | §13 | adopt |

Not proposed, because the critic's counter-argument won or the cost is
low: closed effect set and `nondet` stay (Q1); `Cell.new` stays `mut`;
ASCII identifiers stay; no `if let`; diamond stays a compile error (Q8)
with `jip fetch --resolve` added; single hidden ctx parameter stays (Q11)
but is named as a record.

## 1. Phase A: `docs/decisions.md`

One pull request. Edits by section:

- **§2 Implementation strategy.** Replace the Go 1.27 paragraph with R10.
  Replace "behaviours become Go interfaces" with the dictionary lowering.
  Add: "the hidden context parameter is the runtime record
  `Ctx { cancel, journal, effects, fakes }`; externs receive none, so
  cancellation never reaches an extern."
- **§3 Type system.** Add R2 and R3.
- **§4 Effects.** Define `nondet` extensionally as the list in 07 r12. Add
  R4. Add R14.
- **§4 Contracts.** Add R12. Add the generation size model (depth ≤ 3,
  collection length ≤ 8, `Str` ≤ 32 bytes) and the W0510 filter warning.
  Add R9's `via`.
- **§5 Behaviours.** Replace the eight with R1. Record the fate of the two
  removed ones: `Machine` = `Workflow` plus signals; `Agent` = stdlib loop
  over `Workflow`. Each returns only with a compiled example. Record that
  `Cron` and `Cache` stay because the owner wants every v0 behaviour wired
  through the same dictionary lowering, run lines and `app.jip` overlay
  from the start rather than retrofitted.
- **§6 Agent primitives.** Add R6, R11. Reconcile the approval channel to
  one name, `effects.approve.channel`. `Review.timeout` becomes optional and
  takes `effects.approve.default_timeout`.
- **§9 Modules.** Add R7, R8. State that contract classification is
  textual after `jip fmt` normalisation, labelled `contract-changed` unless a
  conjunct is purely added or removed.
- **§10 Config.** Add R5. Remove `custom` provider and the three workflow
  runtimes from v0. Note the `vendor/modules.txt` synthesis requirement for
  `gomod` deps.
- **§11 Syntax.** Add the three grammar rules from Phase C.
- **§12 Interop.** Add R13.
- **§13 Evaluation.** Replace the metric text with: total tokens of every
  file and tool output an agent reads before its first green `jip check`, on
  a frozen task set, fixed tokenizer named by version, reported with
  iterations-to-green, baseline the same tasks in Go with `go doc` output
  counted the same way. R15.
- **§14 Testing.** Add R9. State that scenarios see `pub` and `pub(module)`
  items of `<path>` and nothing private.
- **§16 Project.** Replace the process line with the gate in Phase E: no
  spec section is "done" until the card compiles.
- **Resolved deferred decisions table.** Add a row per R-item with track
  letters. **Still deferred table.** Add `Machine`,
  `Agent`, the three workflow runtimes, providers beyond one, `any T where`
  and derived checks, persistent collections as a data structure.

## 2. Phase B: spec sections

One pull request per section, in this order, each rebased on Phase A.
Rule numbers refer to the current text; renumber on edit.

**00-language-card.md.** Add `fn init` to `impl Workflow for Billing` or
drop `init` from the Workflow decl (06 line 128). Parenthesise
`if (risk(o) == Tier.High)` or rely on the Phase C rule. Replace
`approve(...)?` with the R11 shape. Rename one of the three `Rejected`
names. Replace the `while attempts < 3 && resp.status == 503 { attempts += 1 }`
loop with a `retry` call. Mark the `charge` scenario with a `FakeHttp` or
move `net` out of `charge`. Replace `extern fn read_file … with {io} = go
"os".ReadFile` with a stdlib call; it is E0501 under 11 r5 even after R13
only if the table says so, so check the table first. Replace
`cfg.gateway` with a `config { gateway: Url }` entry per R5.

**01-lexical-structure.md.** r3: `use` order is sorted by `jip fmt`
(matches 13 r20). r5: delete the duplicate `for`; add `via`, `rows`,
`real_time` if they become keywords, otherwise state they are contextual.
r15, r16: replace the lexer adjacency rule and the `>` terminator with the
Phase C parser rule. Add: nested tuple access `t.0.1` lexes as two field
accesses, never as a float.

**02-types.md.** r1: R2. r14, r16: unconstrained `T` does not admit `==`
or `Show`; require `T: Eq` or `T: Show`. r19: R10 lowering table for
behaviours and generics. Add `TypeName "." TypeName` is an associated type
only when the left name is a type parameter, which casing (01 r4)
guarantees.

**03-functions-and-effects.md.** r4: R3. r15: `nondet` points to 07 r12.
r18: name the `Ctx` record. Add: cancellation is observed only at stdlib
effect sites (cross-reference 08 r6).

**04-expressions-and-control.md.** r2/r17: a closure may read but not
assign a captured `var` (matches 08 r12). r11: `?` has no implicit `Show`
lift.

**05-contracts.md.** r2 vs r8: state that an `inv` on an impl reads `Cell`
payload through a snapshot and that this is the one exception to purity.
r10: R12. r14: unchanged. r15: W0510 and `via`. r16: size model; seeds
reproducible under R14. r17: define "generate a config" as generating the
R5 `config` record from its field types.

**06-behaviours.md.** r1: R1. Budget table: remove `approve` from every
row except `Workflow`. r5: `init(cfg: Config)` with `Config` defined by R5;
`Repository` default budget drops `net`. r6/r14: run lines required for
`Repository` too, or `Repository` instances resolve by type name without a
run line; pick one and align 10 r3. r7: delete "configured on the run line"
or add `transport { method, path }` to `RunSpec`. r9 (`Cron`), r12
(`Cache`): stay in v0; `Cron` needs a schedule grammar in `RunSpec` and a
`tick` fake for scenarios; `Cache` needs `ttl` on the run line and a fake
clock (R14) so expiry is testable. r10 (`Machine`): move under a "Deferred
from v0" heading, not deleted.
r15: `app.paid.publish` needs a grammar form; add `app.<topic>` as a
`Primary` or rename to `topics.paid`. r16: R10.

**07-agents-and-workflows.md.** r7, r8: R4, channel name, optional
timeout. r9: R11. r10, r15, r16, r21: R6; include the two ambiguous
programs from the review as negative examples with their keys. r18: `gate`
matches a per-agent generated `Tools` enum, not a `Str`. `Agent` section
moves under "Deferred from v0"; `Workflow` stays in the spec but out of
the Phase E gate.

**08-concurrency-and-mutation.md.** r6: add the extern clause and
reconcile with r9 `with_timeout` (it cancels at the next effect site; a
pure `f` runs to completion and `None` is returned). r12: unchanged, 04
aligns to it. r15, r18: persistent API stays, implementation is copying
slices until the §13 benchmark; say so.

**09-modules-and-context.md.** r3: `use http` resolves dependency alias
before app module; a root-level app module that collides with an alias is
E0xxx. Define the dependency root module as `vendor/<alias>/` with its own
`app.jip` `name`. r5, r7, r9: R7. r8: textual classification. r10: R15.
Grammar: `Signature` drops `RunLine` and `pub(module)`.

**10-config.md.** r3: add `config { }` (R5); `run` lines align with 06
r6; `custom` and `temporal | restate` deferred; `workflow.store` either
becomes `workflow { mode, store }` or `AppField` admits a dotted key. r8:
add `jip fetch --resolve`. Example: fix the dotted key.

**11-extern.md.** r5: R13 with the table in the section (`os`, `io`,
`net`, `net/http`, `time`, `math/rand`, `sync`, `context` is forbidden).
Re-check the card's `read_file` against it.

**12-testing.md.** r1: `std.testing` names are the one unqualified import;
say so against 09 r3. r2: table form. r4: `via`. r10: R14. r13: `--name`
becomes `--filter` (matches 13 r3). r7: `--runs` added to 13.

**13-tooling-and-diagnostics.md.** r3: `--filter`, `--runs`,
`--accept-breaking`, `fetch --resolve`. r8: R8. r11: R10, pin 1.24. r13:
unchanged; note it is a QA burden the Phase E gate measures.

**grammar.md.** Regenerate from the section grammars after all of the
above; it is not edited by hand. Add `ImplDecl ::= "impl" TypeName Block`
for inherent impls, `Effect ::= … | "llm" | "decide"` bare forms only
inside behaviour budgets, `UsePath` precedence note.

## 3. Phase C: grammar rules

Three rules, added to decisions §11 and spec 01/04, each with the
two-parse program from the review as its diagnostic example:

1. A `RecordExpr` may not appear unparenthesised as the condition of
   `if`/`while`, the iterable of `for`, or the scrutinee of `match`.
2. Closures start with `fn`: `fn(a, b) => a + b`, `fn(x: Int) => { … }`.
   Tuples keep `(a, b)`. This removes the unbounded lookahead.
3. Type arguments on calls use `f::<T>(x)` in expressions and `List<T>` in
   types. The lexer has no adjacency rule and no `>` terminator; the
   parser resolves `<` by context.

Rule 2 changes every closure in every example; `jip fmt` is not available
to migrate them, so the pull request for 04 does the rewrite by hand and
greps for `) =>` afterwards.

## 4. Phase D: scope cut markers

Rather than deleting text, each deferred construct gets a one-line banner
`Status: deferred from v0 (revision-plan R1)` under its heading, and the
spec README table gains a "v0" column. Deferred: `Machine`, `Agent`, `temporal`, `restate`, `custom`, `any T where`,
derived property checks, `approve` outside workflows, persistent
collections as a data structure, API classification beyond drift.

## 5. Phase E: the gate

The spec is frozen only when this passes, and nothing in `stdlib/` or
`evals/` starts before it:

- `compiler/` builds `jip check`, `jip build`, `jip test`, `jip context`
  for the subset: 01, 02, 03, 04, 05 r10 only, 06 all six v0 behaviours
  (R1), 07 journal and replay for `Workflow` on the in-process runtime
  only, 09 r1 to r7 plus r9 and r10, 10 `name`/`config`/`secrets`/`http`/
  `run`/`topics`/overlay, 12 without `any`, 13 pipeline with `//line`.
- Acceptance: the language card's `charge`, `withdraw`, `total`, `max_by`
  and `BillingApi` compile to Go, `go vet` is clean on the output, the
  card's two scenarios pass with a `FakeHttp`, `interface.jipi` round-trips
  through `jip check` with a stable digest, and `jip context orders.billing`
  prints without a number claimed in advance. Plus one example per
  remaining v0 behaviour in `examples/`, each with a scenario that passes
  under the default fakes: a `Worker` consuming a topic, a `Cron` fired by
  an injected `tick`, a `Cache` whose entry expires under the fake clock, a
  `Workflow` that replays a journal with one `approve` step. The owner's
  waterfall rule applies: the gate passes as a whole or not at all.
- Order inside `compiler/`: lexer and parser with Phase C; type checker
  without behaviours; effects; modules and interface files; behaviours with
  dictionary lowering; contracts as boundary `Err`; scenarios and fakes; Go
  emission; CLI.
- Every rule that the compiler cannot check as written is a spec bug filed
  against this plan, not a compiler workaround.

## 6. Phase F: evaluation and numbers

After the gate: pick the tokenizer by name and version, freeze a task set
of at least ten changes to the `examples/` corpus, measure the §13 metric
on jippitese and Go, and only then write numbers back into 09 r10 and
decisions §13.

## 7. Risk register (new section for `decisions.md`)

Ranked as the critic ranked them, with the plan item that retires each:

| Rank | Risk | Retired by |
|---|---|---|
| 1 | No compiled line; contradictions accumulate | Phase E gate |
| 2 | Behaviour and generic lowering unspecified; Go 1.27 bet | R10 |
| 3 | `Config` undefined | R5 |
| 4 | Step ordinal corrupts replay under branching, closures, recursion | R6 |
| 5 | Transitive extern floor forbids every extern | R13 |
| 6 | Grammar ambiguities | Phase C |
| 7 | `pub(module)` outside digest and cache key | R7 |
| 8 | Contract panics inside handlers cause restart storms | R12 |
| 9 | Extern cancellation hole | §2 `Ctx` clause |
| 10 | Token metric unbaselined | R15, Phase F |
| 11 | Generators degrade to filtering | R9 `via`, size model |
| 12 | Real clock in seeded property runs | R14 |
| 13 | `-mod=vendor` with `gomod` deps; hard-coded providers | §10 note, `custom` deferred |
| 14 | BDD ceremony for helpers | R9 table form |
| 15 | Persistent collections at 2× | §13 benchmark, copying implementation meanwhile |

## 8. Pull request sequence

1. `docs: revision plan` (this file).
2. `decisions: adopt R1–R15` (Phase A).
3. `spec: grammar rules for records, closures, type arguments` (Phase C,
   edits 01, 04, grammar.md, card).
4. `spec: card, 01, 02, 03, 04` (Phase B, first half).
5. `spec: 05, 06, 07, 08` with Phase D banners.
6. `spec: 09, 10, 11, 12, 13` and regenerated `grammar.md`.
7. `compiler: lexer and parser` and onward per Phase E.

Steps 4 to 6 can run in parallel after 3; each must pass a grep for every
rule number it renumbers, since later sections cite earlier ones by number.

## 9. Open questions for the owner

Answered 2026-10-19:

- R1: `Worker`, `Cron` and `Cache` stay in v0; only `Machine` and `Agent`
  defer. Reason: waterfall, wire every behaviour through the same lowering
  from the start.
- R2: overflow traps in every build, release included. A trap is one
  compare per arithmetic op in the emitted Go; the owner does not want a
  debug/release behaviour split.
- Phase C rule 2: closures use `fn`.
- R7: `pub(module)` items are digested for the cache only, not classified
  for breaking changes. A `pub(module)` change can only break siblings
  under the same parent, which the same commit must already fix for
  `jip check` to pass, so classifying it adds a report nobody acts on.
