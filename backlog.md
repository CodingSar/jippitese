# Backlog

v0 (sections 1–8) passed the Phase E gate at commit `35c602c`. Sections
9–12 are the v0 follow-ups and the v1 work, in the order they are done.

v0 history: remaining work to pass the Phase E gate (`docs/revision-plan.md` §5) and
run Phase F (§6). Ordered by dependency; items inside a group are in the
order they should be done. State as of commit `9ff4224`: lexer, parser,
AST, diagnostics, app parser, module loader, checker, embedded stdlib and
the `rt` runtime build and pass their tests. The `emit` package is a
scaffold (`emit.go`, `names.go`) and does not build; `cmd/jip` is empty.

Rule from the plan: every rule the compiler cannot check as written is a
spec bug filed against the plan, not a compiler workaround. Log such cases
under "Spec bugs found" at the bottom instead of patching around them.

## 1. Go emitter (`compiler/emit`)

Working method: write it in small slices that each compile, commit each
slice, drive every slice from the orders card fixture
(`compiler/checker/testdata/orders/`) only. Do not re-derive the design;
it is settled in `emit.go` and below.

- [x] **E1 `types.go`** — `goType(checker.Type) string` with the spec 11
      mapping (Int→int64, Float→float64, Bool→bool, Str→string,
      Bytes→[]byte, Duration→time.Duration, ()→rt.Unit, tuples→rt.T2..T6,
      records/enums by `typeRef`, prelude externs by `rtTypeName`,
      `TParam` by name, `TFn` → `func(*rt.Ctx, …) R`). `Zonk` first, then
      type-switch. Unit tests against the card's types.
- [x] **E2 `names.go` fix** — `fnRef` must resolve std externs to
      `f.rt() + "." + fi.Extern.Ident`, not `f.std() + "." + name`.
- [x] **E3 `decls.go`** — record structs with json tags, newtypes, enums
      (`{Tag uint8; <V>_<i>}` + `XTag_V` consts + `X_V(...)` constructors +
      `JipVariants` + JSON methods, same shape as `rt/prelude_gen.go`),
      free functions `func Name[T any](ctx *rt.Ctx, …) R`, consts as
      functions, impl methods as `<Target>_<method>`.
- [x] **E4 `expr.go` / `walk.go`** — statement-hoisting lowering with `_tN`
      temps; blocks, let/var/assign, if, while, for/range, return/break/
      continue, closures, record/tuple/list literals, field access by
      `FieldRef`, string interpolation via `rt.Str`, Int arithmetic via
      `rt.Add/Sub/Mul/Div/Rem` (no `rt.Neg`: use `rt.Sub(ctx, 0, x)` or
      add `Neg` to rt), `rt.Equal` for `==`, `?` via a return hook using
      `TryInfo`, `spawn`/`task` via `rt.Spawn`/`rt.TaskJoin`.
- [x] **E5 call lowering** — every `CallInfo.Kind`: CallFn with type
      args, CallMethod (type args absent: infer by matching receiver type
      against impl target with a small binder), CallInstance via
      `rt.Instance`, CallVariant/CallNewtype constructors, CallBuiltin
      (`panic`, `random`, `run_workflow`, `tick`, `send_signal`, enqueue,
      `fake`), CallClosure, CallRecordInv, topic publish via
      `rt.TopicPublish`, workflow steps via `rt.Step(ctx, site, …)` when
      `info.Step != ""`, `llm`/`decide`/`approve` via `rt.Llm/Decide/
      Approve`.
- [x] **E6 `pattern.go`** — `match` → `_m :=` plus if/else chain for every
      pattern kind (wild, bind, lit, variant positional/named, tuple,
      record, or, at, guards). Use `PatInfo`/`PatBinds`/`AtBinds`.
- [x] **E7 contracts** — `pre`/`post`/`inv` lowered to checks that call
      `rt.ContractFail(ctx, kind, expr, at)` with `old()` captured before
      the body and `ret` bound after; `rt.SetContracts` from the app's
      `contracts` level.
- [x] **E8 behaviour specs** — `var <Base>_spec = &rt.<B>Spec[...]{…}` for
      Service, Worker, Cron, Workflow, Repository, Cache with Init wrapper,
      defaults (`stop`, `health`, `on_signal`), `Sites` for Workflow, and
      `rt.RunOpts` from the `run` line (restart, max_restarts, period,
      shutdown_timeout, topic, port, ttl, overlaid via `RunStr/RunInt/
      RunDuration`).
- [x] **E9 `appcfg.go`** — `Config`/`Secrets` structs, `SecretsFake`,
      `Load()` driving `rt.ConfigLoader` with the env blocks and the
      fixed/required/overridable mode per field, `TestConfig`, `Finish`
      with the known key list.
- [x] **E10 `scenario.go`** — `scenarios_test.go` per module with
      `rt.Scenario(t, rt.ScnOpts{…}, body)`; givens (expr, `any`, table,
      real_time), whens, thens; fakes installed through `scn.Install`;
      property scenarios with `rt.Gen`/`rt.Any`.
- [x] **E11 `std.go`** — `emitStd`: the std package is thin (std types are
      rt types, std fns are rt externs); emit only what `names.go` still
      allocates there, or drop the `std` package and route everything to
      `rt`.
- [x] **E12 `main.go` and `go.mod`** — already scaffolded in `emit.go`
      (`emitMain`, `emitGoMod`); wire `findRunImpl` to the specs from E8.
- [x] **E13 `emit_test.go`** — end-to-end: emit the orders card, write to
      a temp dir, `go vet` clean, `go test` runs the card's two scenarios
      green with `FakeHttp`. Golden-file check that output is byte-stable
      across two runs.
- [x] **E14 `extern.go`** — `CheckExterns(prog, bag)`: E0501–E0508 per
      spec 11 using `go list -json -export` and `importer.ForCompiler`
      (type mapping table, effect floor by package, allow-list, `context`
      import ban, signature mismatch, method refs, unmappable types,
      `@extern` scenario requirement). `rt.ioError` must become exported
      or mapped for `Result<T, IoError>`.
- [x] **E15 `//line` directives** — `--go-positions` off by default; one
      `//line <file>:<line>:<col>` per statement, gofmt the result,
      `fixLines` keeps them at column 0.

## 2. Interface files (`compiler/iface`, new)

- [x] **I1** Extract `interface.jipi` from a checked module: `pub` items
      only, printed with `TypeExprString`/`EffectSetString`, sorted and
      canonical so the digest is stable.
- [x] **I2** Digest (sha256 of the canonical text) and the breaking-change
      classifier for spec 09 r1–r7, r9, r10; `pub(module)` digested only.
- [x] **I3** Round-trip test: write, re-read, re-check, same digest.
      `--accept-breaking` path.

## 3. Command (`compiler/cmd/jip`)

- [x] **C1** `jip check` (`--write-interfaces`, `--accept-breaking`,
      `--effective --env`, `--json`); exit codes 0/1/2/3.
- [x] **C2** `jip build` (`--release`, `--target`, `--go-positions`):
      emit to `.jip/gen/`, copy `rt`, write `go.mod` with
      `toolchain go1.24.0`, run `go build -trimpath -mod=vendor` to
      `.jip/bin/<name>`; Go build failure → `error[E9999]`;
      `compiler/toolchain.go` pin; `JIP_GO` override.
- [x] **C3** `jip test` (`--filter`, `--runs`, `--seed`): build then
      `go test` of the gen tree with `JIP_SCENARIO_FILTER`/`JIP_SEED`/
      `JIP_RUNS`; map results back to scenario names and positions.
- [x] **C4** `jip context <module>` (`--short`, `--for`): print the
      module's public surface and effects per spec 13.
- [x] **C5** `jip fmt` and `jip fetch` (fetch: vendored deps by URL,
      commit and hash from `app.jip`).
- [x] **C6** Deterministic output check: two builds of the card produce
      identical `.jip/gen/`.

- [x] **C7** (superseded by T1) The jippitese build cache `.jip/cache/<module>` of spec 13
      rule 12 (skip typecheck/emit per module on unchanged digests). Not
      needed for correctness; `GOCACHE` already skips unchanged Go.

## 4. Standard library gaps (`compiler/stdlib/*.jip`, `rt`)

- [x] **S1** `std.testing` journal API for scenarios: a way to run a
      Workflow, capture its journal, and replay it (needed by the Workflow
      example). `rt` has `NewJournal/LoadJournal/RunWorkflow/
      ReplayWorkflow`; expose them as externs.
- [x] **S2** Unary minus on `Int` lowers to `rt.Sub(ctx, 0, x)`, so it traps on overflow like every other `Int` operation; no `rt.Neg`.
- [x] **S3** Export or map `rt.ioError` for `IoError` (`rt.IoErrorOf`, `NetErrorOf`, `GoErrorOf`).
- [x] **S4** Replace placeholder `stdlib/README.md` with the module list
      and the rule that every std fn is an `rt` extern.

## 5. Examples (`examples/`)

Each is a full app (`app.jip` + modules + scenarios) that passes under the
default fakes.

- [x] **X1** Orders card app (move the checker fixture up as the first
      example; keep the fixture in `testdata` for the checker test).
- [x] **X2** Worker consuming a topic (`run … { topic }`, `enqueue`,
      dead letters, retry).
- [x] **X3** Cron fired by an injected `tick` under the fake clock.
- [x] **X4** Cache whose entry expires under the fake clock (`ttl`).
- [x] **X5** Workflow replaying a journal with one `approve` step
      (`FakeApproveScript`, journal round-trip from S1).
- [x] **X6** Replace placeholder `examples/README.md` with the corpus
      description and how to run `jip test` on each.

## 6. Acceptance run (gate, all or nothing)

- [x] **G1** Card's `charge`, `withdraw`, `total`, `max_by`, `BillingApi`
      compile to Go; `go vet` clean.
- [x] **G2** Card's two scenarios pass with `FakeHttp`.
- [x] **G3** `interface.jipi` round-trips through `jip check` with a
      stable digest.
- [x] **G4** `jip context orders.billing` prints.
- [x] **G5** X2–X5 each pass `jip test`.
- [x] **G6** Replace placeholder `compiler/README.md` with package map,
      build/test commands and the gen layout.

## 7. Spec and docs edits found during implementation

- [x] **D1** Spec 06: `mut` is now in every `init` budget and in the
      `Repository` and `Cache` budgets, matching the prelude; without it no
      impl could construct in-process state (found while writing the
      Worker example).
- [x] **D2** Spec 11: add the extern adapter example matching the
      implemented mapping table.
- [x] **D3** Language card: `both` → `http.Body` in the `BillingApi`
      example.
- [x] **D4** `grammar.md`: note on contextual keywords (`pre`, `post`,
      `inv`, `old`, `ret` are contextual, lexed as identifiers).
- [x] **D5** `decisions.md`: record deviations between spec text and
      what the checker/emitter implement (constructor type params dropped
      on impls, behaviour type args kept on impls, appcfg synthesis).
- [x] **D6** Replace placeholder `evals/README.md` once Phase F defines
      the task set.

## 8. Phase F: evaluation and numbers

- [x] **F1** Pick the tokenizer by name and version; record it in
      decisions §13.
- [x] **F2** Freeze a task set of at least ten changes over the
      `examples/` corpus (each: description, jippitese diff, Go diff of
      the emitted code or a hand-written Go equivalent).
- [ ] **F3** (blocked: needs Anthropic API credentials; `evals/run.mjs` and the Go baseline are ready) Measure the spec 13 metric on jippitese and Go for every
      task; script it under `evals/`.
- [ ] **F4** (blocked on F3) Write the numbers into spec 09 r10 and decisions §13; only
      then claim any figure in the docs.

## Spec bugs found

None open. The four found while building v0 (11 r3 error types, 11 r5
floor source, 13 r11 toolchain, 11 r1 `rt` targets) and the journal store
text were settled under V1; see `docs/decisions.md`, "Implementation
decisions".

## 9. v0 follow-ups

- [x] **V1** Settle the four spec bugs above in the spec, record each in
      `decisions.md` (owner may reverse): 11 r3 any extern may return
      `IoError`/`NetError`; 11 r5 floor from the target package itself;
      13 r11 "pins" instead of "embeds"; 11 r1 `rt` is the toolchain's own
      package. Plus the journal store: spec 07 r16 and 10 say SQLite, the
      runtime writes one JSON-lines file per run; the spec follows the
      runtime.
- [x] **V2** Ratify D1 (`mut` in `init` budgets) in `decisions.md`.
- [ ] **V3** A `run` line in `app.jip` naming an unknown item, or one that
      is not runnable (an `Agent`, a `Workflow`), is silently ignored;
      report it (spec 10, section on `run`).
- [ ] **V4** A lexer error inside a string literal (`"{\"id\": 7}"`, an
      unclosed interpolation) is reported at 1:1 instead of at the
      literal.

## 10. v1 language

Each item: spec banner removed, checker, emitter, runtime, one example or
scenario that passes, then commit. Designs are the ones the spec already
fixes under "deferred from v0"; deviations go to `decisions.md`.

- [x] **L1** `any T where expr` (12 r4): inline filter on the structural
      generator; W0510 when more than 90% of candidates are rejected.
- [x] **L2** Derived property checks for functions (05 r14–16): eligible
      `pub fn`s, structural generators, oracle (`post`, `inv`, no panic),
      N = 200, integrated shrinking, seeds under `.jip/seeds/`, replayed as
      run 1. `emit/derived.go`; `driver_test.go` `TestDerivedCheck`.
- [x] **L2b** `pre`-derived generator bounds (05 r15): a comparison of a
      parameter, field path or `.len()` against a constant or another
      parameter bounds that generator; a range membership becomes a range
      generator; `Range.contains`; `any Str` at 32 bytes. `preBounds` in
      `emit/derived.go`; `TestDerivedCheck` covers `pct`, `above`.
- [x] **L3** Derived stateful check for impls with `inv` (05 r17) and the
      stdlib behaviour contracts (05 r18, 06 r8, r10): `Worker` idempotency,
      `Repository` get-after-put, with `MemRepo`/`MemCache` substitution.
      `emitStateful`, `emitRoundTrip`, `emitIdempotent` in
      `emit/derived.go`; `rt/substitute.go`; `driver/behaviour_test.go`.
- [x] **L4** `Machine` behaviour (06): `Key`, `State`, `Event`, `Action`;
      exhaustive `on`; one turn per key; `Name.send`; `deactivate` on idle;
      `run` line; example `examples/doors`. `rt/machine.go`
      (`TestMachineTurnsAndIdle`); `machine_send` in `checker/expr.go` and
      `emit/call.go`; multi-line docs in `iface` (`TestMultiLineDocRoundTrip`).
- [x] **L5** `Agent` behaviour and `tools [...]` (07 r17–23): tool schemas
      from `pub fn`s, generated `Tools` enum, `Event`, `Gate`, `Outcome`,
      budget, loop in `rt` against the `llm` fake, journaled inside a
      `Workflow`; example `examples/triage`. `rt/agent.go` (`TestAgent*`,
      `TestFakeLlmTurns`); `checker/agent.go` (`TestAgentChecks`,
      `TestAgentToolNotPub`, `TestToolsOutsideAgent`,
      `TestAgentToolsNamedByUser`, `TestAgentRunEffects`);
      `emit/agent.go`; spec 06, 07, 12 and grammar updated.
- [x] **L6** Externs on methods (`go "path".Type.Method`, 11 r1) and
      `gomod(...)` Go dependencies: `jip fetch` vendors them, generated
      `go.mod` gets `require`/`replace` lines and `vendor/modules.txt`.
      - [x] L6a method externs: `emit/extern.go` (`lookupMethod`,
            receiver conversion), tests `TestExternRules`,
            `TestExternMethod`, `TestMethodExternRuns`; spec 11 r1,
            grammar, decisions.
      - [x] L6b gomod: `app.Dep.Kind/GoPath`, `GoModFor`; vendored-source
            importer in `emit/extern.go`; `require`/`replace` in
            `emitGoMod`; `driver/gomod.go` (`vendorGo` links and
            `modules.txt`, `checkGoMods`); `Output.Links`; test
            `TestGoModDependency`; spec 10 r7, 13 r10, decisions.
- [x] **L7** `models { role = custom(url, model) }` provider (10 r3).
      - [x] L7a `app.Model.URL`, provider and arity checks, `ModelFor`;
            E0307 in the checker (`checkRole`); tests `TestParseModels`,
            `TestUndeclaredRole`; spec 10 r3.
      - [x] L7b `rt/schema.go` (`SchemaOf`), `rt/provider.go` (`Models`,
            `NewModel`, `ChatClient`), `LlmClient.Call` gets the output
            type; tests `rt/provider_test.go`.
      - [x] L7c generated `main.go` installs `rt.Models` from `models {}`;
            test `TestModelsInMain`; spec 10 r3, decisions.
- [ ] **L7d** Remaining providers: Anthropic Messages protocol for
      `anthropic` (and `jev` once its protocol is fixed), `decide<role>`
      on a provider, token usage priced into `AgentReply.Cost` so
      `max_cost` binds, `llm.timeout`/`llm.budget` from `effects {}`.
- [x] **L8** Persistent collections (08 r15): run the §13 collections
      benchmark; if within budget, structurally shared `List`/`Map`/`Set`
      in `rt` behind the same API; record the numbers.
      Done for `Map`/`Set`: `rt/hamt.go` (HAMT, insertion order by
      sequence number), `rt/hash.go` (`Hash`, consistent with `Equal`);
      `MutMap`/`MutSet` wrap it, so freeze/thaw are O(1). Benchmark
      `go test ./rt -run '^$' -bench Collections -benchmem`, 4096 string
      keys: insert-all 617 ms → 7.1 ms (Go map 0.42 ms), get-all
      615 ms → 0.13 ms (Go map 0.07 ms), entries 39 µs → 0.19 ms.
      Spec 08 r15–r18, spec 02 r9/r10 and the type table, decisions §7.
- [ ] **L8b** Structurally shared `List` (RRB vector): `List` is `[]T`
      across the emitter, externs and JSON, so it stays a copying slice;
      do it only if a program needs `push` in a loop despite W0520.

## 11. v1 toolchain and stdlib

- [x] **T1** Build cache (13 r12, was C7). Measured first: `jip check` was
      almost all Go-package loading for the extern check, so the cache
      records clean extern checks under `.jip/cache/externs` instead of
      per-module typecheck/emit. Orders: `jip check` ~340ms → 15ms, rebuild
      ~460ms → ~160ms. Spec 13 r12 and 09 r9 rewritten to match.
- [x] **T2** Stdlib `std.fs` (read, read_text, write, write_text, append,
      list, present, remove, mkdir) over `IoError`, with `FakeFs` in
      `std.testing` and `Path.of/join/to_str/name/parent` in the prelude.
      `exists` is the contract quantifier keyword, so the check is
      `present`. 12 r10 updated.
- [x] **T3** Stdlib `std.http` client (get, post, request with headers)
      over `NetError`, `FakeHttp` routing, and `std.approve.http` channel
      (10 r3).
      - [x] T3a `std.http` (get, post, request; `Response` with status,
            headers, body, ok, text), `RealHttp` timeout and idempotent
            retry, `FakeHttp.route`, `Request` with method and headers.
      - [x] T3b `effects {}` in `app.jip` (net timeout and retry, approve
            channel and default timeout) wired into the generated main.
            Model key variables accepted; E0303 on env blocks.
      - [x] T3c `std.approve.http` channel: loopback HTTP endpoint
            (`JIP_APPROVE_ADDR`) listing pending reviews, approve with a
            `T` body, reject with an optional reason, timeout from the
            review or `approve.default_timeout`. 07 r7 lists the routes.
      - [ ] T3d User approval channels: a module implementing
            `std.approve.Channel` named by `effects.approve.channel`
            (today any path but `std.approve.http` is E0301), and pending
            reviews that survive a restart (re-listed from the journal's
            pending entries).
- [x] **T4** Stdlib breadth: `std.strings` (replace, index, pad, repeat,
      format of floats), `std.math`, `std.time` (Instant arithmetic,
      formatting, parsing), `std.json` (navigating `Json`), `std.log`.
  - [x] **T4a** Str, Int and Float methods in the prelude (replace,
        index_of, slice, pad, repeat, lines, parse_float, strip_prefix;
        pow, clamp, checked/wrapping arithmetic; format, ceil, to_int,
        min/max/clamp, trapping Float to Int).
  - [x] **T4b** `std.math`: sqrt, pow, exp, ln, log10, log2, trig, hypot,
        pi, e.
  - [x] **T4c** `std.time`: Instant before/after/sub, unix millis, RFC 3339
        format and parse, UTC calendar fields; Duration minutes/hours.
  - [x] **T4d** `Json` navigation (get, at, as_*, items, keys) and
        `json.parse`.
  - [x] **T4e** `std.log`.
- [x] **T5** Language server `jip lsp` (LSP over stdio): diagnostics on
      change, hover with type and effects, go to definition, document
      symbols, formatting via `jip fmt`.
  - [x] **T5a** Open-buffer overlay over the module loader, diagnostics on
        open/change/save/close, whole-document formatting, `jip lsp`.
  - [x] **T5b** Document symbols.
  - [x] **T5c** Hover (type, effects, doc) and go to definition.

## 12. v1 evaluation and runtimes

- [x] **F5** Go baseline corpus for the twelve tasks (`evals/go/`), each
      task's Go starting point plus solution, building and testing green.
- [x] **F6** Agent runner `evals/run.mjs` against the Anthropic API
      (models configurable, default the current Claude models); records
      tokens and iterations per task per language. Running it needs
      `ANTHROPIC_API_KEY`; F3/F4 then use its output.
- [x] **W1** `workflow { mode: temporal | restate }` (07 r16): runtime
      adapter interface in `rt` (`rt.WorkflowRuntime`, picked by mode from
      `rt.WorkflowRuntimes`), embedded as the first adapter; `temporal`
      and `restate` parse and fail with "no runtime adapter yet".
- [ ] **W1b** Temporal and Restate adapters behind `rt.WorkflowRuntime`
      (blocked: their Go SDKs vendored and a server to test against);
      each adds its name to `app.WorkflowAdapters`.
