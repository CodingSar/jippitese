# 07 Agents and workflows

Four primitives: `llm` (structured model call), `decide` (System One
classifier), `approve` (durable human promise) and tools derived from
functions; two behaviours that host them: `Workflow` (journaled, replayable)
and `Agent` (runtime-owned model loop). Declarations are in section 06;
semantics are here.

## Example

```jip
module support.triage
use std.agent
use support.tickets               // Ticket, TicketId, Tickets (Repository impl)

/// Severity as the fast model sees it.
pub enum Severity { Low, High }

/// What the smart model produces for one ticket.
pub type Summary {
  /// One sentence a human reads first.
  headline: Str,
  /// Product areas mentioned, lower-case.
  areas: List<Str>,
}

/// Loads a ticket by id.
pub fn lookup(id: TicketId) -> Result<Ticket, RepoError> with {io} { Tickets.get(id)?.ok_or(NotFound) }

/// Refunds the ticket's order; irreversible.
pub fn refund(id: TicketId, amount: Money) -> Result<(), RepoError> with {net} { billing.refund(id, amount) }

/// Resolves one support ticket with a bounded tool loop.
impl Agent for Triage {
  type State = TriageState
  type Answer = Summary
  const model = smart
  const tools = tools [lookup, refund]
  const budget = Budget { max_turns: 12, max_cost: Money(200) }
  fn init(cfg: Config) -> Result<Self, AgentError> with {io} { Ok(Triage {}) }
  fn begin(self, task: Str) -> TriageState { TriageState { task, seen: [], answer: None } }
  fn observe(self, st: TriageState, ev: Event) -> TriageState {
    match ev {
      Returned(call, out) => TriageState { ..st, seen: st.seen.push(call.tool) },
      Said(text) => TriageState { ..st, answer: json.decode<Summary>(text).ok() },
      Called(_) | Compacted => st,
    }
  }
  fn stop(self, st: TriageState) -> Option<Summary> { st.answer }
  fn gate(self, call: ToolCall) -> Gate with {approve} {
    match call.tool {
      "refund" => Ask,                       // routed through approve
      _ => Allow,
    }
  }
}

/// Escalates a ticket durably: classify, triage, ask a human when severe.
impl Workflow for Escalation {
  type Input = TicketId
  type Output = Summary
  type Signal = Cancel
  const version = 1
  fn init(cfg: Config) -> Result<Self, WorkflowError> with {io} { Ok(Escalation {}) }
  fn run(self, id: TicketId) -> Result<Summary, WorkflowError> with {io, net, nondet, llm<smart>, decide<fast>, approve} {
    let t = lookup(id)?                                        // step lookup#1
    let sev: Severity = decide<fast>(t)                        // step decide#1
    let s = match Triage.run("Resolve ticket {id}") {          // one step per model turn
      Finished(a) => a,
      Stopped(_) | Refused(_) => llm<smart>("Summarise this ticket.", t)?,   // step llm#1
    }
    if sev == High {
      let review = Review { title: "Severe ticket {id}", body: s.headline, timeout: 24h }
      let ok: Bool = approve(review)?                          // step approve#1, durable promise
      if !ok { return Err(WorkflowError.Rejected) }
    }
    Ok(s)
  }
}
```

TypeScript equivalent of `impl Agent for Triage`, with the Vercel AI SDK:

```ts
const result = await generateText({
  model: models.smart,                 // role resolved from configuration
  tools: { lookup: tool({ parameters: z.object({ id: z.string() }), execute: lookup }),
           refund: tool({ parameters: z.object({ id: z.string(), amount: z.number() }), execute: refund }) },
  maxSteps: 12,
  prompt: task,
  // approval, cost cap, schema of the answer, compaction: hand-written around this call
});
```

Go (Temporal) equivalent of `impl Workflow for Escalation`:

```go
func Escalation(ctx workflow.Context, id TicketID) (Summary, error) {
    ao := workflow.ActivityOptions{StartToCloseTimeout: time.Minute}
    ctx = workflow.WithActivityOptions(ctx, ao)
    var t Ticket
    if err := workflow.ExecuteActivity(ctx, Lookup, id).Get(ctx, &t); err != nil { return Summary{}, err }
    // model, classifier and approval: more activities and signal waits; determinism checked only at replay
}
```

## Rules

### `llm`

1. `llm<role>(prompt: Str, input: I) -> Result<O, LlmError> with
   {llm<role>}`. `role` must be declared in `app.jip` `models {}`. `O` is
   inferred from the binding annotation or written as `llm<role, O>`.
   `LlmError = Schema(Str) | Refused(Str) | Transport(Str) | Budget`.
2. **Schema-derivable types.** `I` and `O` must be: primitives, `Str`,
   `Duration`, newtypes over them, records, enums (payload-less or with
   derivable payloads), `Option<T>`, `List<T>`, `Map<Str, T>` and tuples of
   derivable types. Function types, `Cell`, `Topic`, behaviour impl types
   and `Task` are not derivable. The compiler derives a JSON schema from `O`;
   a `///` line on a field or variant becomes its description; a `Result` in
   `O` is a compile error (the call already returns one).
3. The model's reply is decoded against the schema and checked against
   every `inv` of `O`; a failure is `Err(Schema(…))`, never a panic.

### Tools

4. `tools [f, g]` is a `List<Tool>` built from `pub fn` values. For each
   function the tool takes its name, its `///` line as the description, its
   parameters as a schema (names, types, their doc lines), and its declared
   effects. Every parameter and the return type must be schema-derivable; the
   return type may be `Result<T, E>` with derivable `T` and `E`.
5. A tool's effects join the agent's: `Name.run` carries `{llm<model>} ∪
   gate effects ∪ every tool's effects`. A non-`pub` function, a closure or a
   method cannot be a tool.

### `decide`

6. `decide<role>(input: I) -> O with {decide<role>}`, no prompt string. `I`
   is schema-derivable. `O` is one of: a closed payload-less enum, `Score` (a
   stdlib newtype over `Float` with `inv` 0 ≤ value ≤ 1), or
   `Calibrated<Bool>` (`{ value: Bool, p: Score }`). Anything else is a
   compile error. The call is provider-agnostic; `app.jip` maps the role.

### `approve`

7. `approve(review: Review) -> Result<T, Rejected> with {approve}` with
   `Review { title: Str, body: Str, timeout: Duration }`, all three fields
   required (`timeout` is mandatory; there are no default fields, section
   02). `T` is a closed payload-less enum or `Bool`, inferred from the
   binding annotation.
   `Rejected { reason: Option<Str>, timed_out: Bool }`.
8. An `approve` call is a named durable promise keyed `<fn>#<ordinal>` like
   a step. The runtime delivers the review to the channel configured by
   `approvals { channel: … }` in `app.jip` (default: a local HTTP endpoint
   that lists pending reviews and accepts one `T` value each) and suspends;
   a reply resolves it, the timeout rejects it with `timed_out: true`.
   Outside a `Workflow` or an `Agent` gate the promise is still journaled
   but the caller blocks.

### `Workflow`

9. **Declaration.** As in section 06: `Input`, `Output`, `Signal` (closed
   sum), `const version: Int`, `run(self, in: Input) -> Result<Output,
   WorkflowError> with {io, net, nondet, llm, decide, approve, spawn}`,
   `on_signal(self, s: Signal) {}`. `WorkflowError = Replay(ReplayError) |
   Approval(Rejected) | Rejected | Cancelled | Failed(Str)`. Inside `run`,
   `?` additionally lifts any error type implementing `Show` into
   `Failed(e.show())`.
10. **Step rule.** Every call carrying `io`, `net`, `nondet`, `llm`,
    `decide` or `approve` in `run`, or in a function `run` calls transitively
    that carries one of those effects, is lowered to a journaled step keyed
    `<fn>#<ordinal>`, where `<fn>` is the callee name and `<ordinal>` counts
    call sites in source order within `run`; inside a loop the key gains the
    iteration index (`charge#2/3`); inside a called effectful function the
    key is prefixed by that function's own key. On replay a step returns the
    recorded value without executing. A pure call is never a step.
11. **No `mut`.** `mut` is forbidden in `run` and in anything it calls; it
    is absent from the budget, so this is the ordinary effect-subset check,
    reported with the workflow-specific message below. `var` locals are
    allowed.
12. **`nondet` primitives.** `time.now()`, `random()` and `sleep(d)` inside
    `run` resolve to runtime-journaled versions: `now()` returns the
    first-run time on replay, `sleep` is a durable timer.
13. **Signals.** `await_signal<Signal>(timeout: Duration) -> Option<Signal>
    with {nondet}` is a step that suspends until a signal sent by
    `Name.signal(run, s)` arrives or the timeout elapses (`None`). Signals
    sent while no `await_signal` is pending are buffered in arrival order.
    `on_signal` runs, pure, on each arrival; v0 gives it no way to reject.
14. **`continue_as_new(in: Input) -> Result<Output, WorkflowError> with
    {io}`** closes the current history and starts a fresh one under the same
    `RunId`; `run` must return its value directly.
15. **Versioning and replay.** `version` must change whenever the step
    sequence changes. On replay the runtime compares the history's version
    and each recorded step key with the current code; any mismatch yields
    `Err(WorkflowError.Replay(ReplayError { expected, found }))` for that
    run. There is no silent re-run.
16. **`spawn`.** Inside `run`, `spawn` and `task` keep section 08 semantics
    but lower to deterministic runtime coroutines; each task's steps are
    keyed under the task's ordinal. `Cell` cannot be created or passed in.
17. **Runtime.** `workflow: embedded | temporal | restate` in `app.jip`
    selects the journal; `embedded` (SQLite, one process) is the default.
    `Name.start(in: Input) -> RunId with {io}`, `Name.signal(run: RunId, s:
    Signal) with {io}` and `Name.result(run: RunId) ->
    Option<Result<Output, WorkflowError>> with {io}` are generated for every
    impl; `None` means still running.

### `Agent`

18. **Declaration.** `State`, `Answer`, `const model: Role`, `const tools:
    List<Tool>`, `const budget: Budget` with `Budget { max_turns: Int,
    max_cost: Money }` both required; `begin(self, task: Str) -> State`,
    `observe(self, st: State, ev: Event) -> State` and `stop(self, st:
    State) -> Option<Answer>` are pure; `gate(self, call: ToolCall) -> Gate
    with {decide, approve}`. `Event = Said(Str) | Called(ToolCall) |
    Returned(ToolCall, Str) | Compacted`; `ToolCall { tool: Str, args: Json
    }`; `Gate = Allow | Deny(Str) | Ask`; `Answer` must be schema-derivable,
    since the model is asked to produce it.
19. **Loop.** `Name.run(task: Str) -> Outcome<Answer>` (effects per rule 5)
    runs: `st = begin(task)`; repeat { model call with the transcript and
    the tool schemas; for each tool call, `gate` (`Ask` routes through
    `approve` with a generated `Review`, `Deny(reason)` returns the reason
    to the model); execute allowed tools; `st = observe(st, ev)` per event;
    `stop(st)` } until `Some(answer)`, the budget is hit, or the model
    refuses. The runtime owns context compaction and reports `Compacted`.
20. **Outcome.** `enum Outcome<Answer> { Finished(Answer),
    Stopped(BudgetExceeded), Refused(Str) }` with `BudgetExceeded = Turns |
    Cost`. `max_cost` is checked before each model call and may be exceeded
    by at most one call.
21. **Inside a `Workflow`.** `Name.run(task)` called from `run` is journaled
    as one step per model call (`Triage.run#1/turn#k`) plus one per tool
    execution and gate decision, so a crashed agent resumes mid-loop.

## Diagnostics

`mut` inside `Workflow.run` (rule 11):

```text
error[E0703]: effect `mut` is not allowed inside `Workflow.run`
  --> support/triage.jip:52:5
   |
52 |     bump(counter)
   |     ^^^^^^^^^^^^^ `bump` has effect `mut`
   = note: `run` may use {io, net, nondet, llm, decide, approve, spawn}
   = help: keep workflow state in a `var` or return it from a step
```

A type that is not schema-derivable (rule 2):

```text
error[E0704]: type `Summary` is not schema-derivable for `llm<smart>`
  --> support/triage.jip:12:3
   |
12 |   render: fn(Ticket) -> Str,
   |   ^^^^^^^^^^^^^^^^^^^^^^^^^ function types have no schema
   = note: schema-derivable: primitives, Duration, newtypes, records, closed enums, tuples, Option, List, Map<Str, _>
   = help: remove `render` from `Summary` or move it to a separate type
```

A budget without `max_cost` (rule 18):

```text
error[E0705]: `Agent.budget` is missing `max_cost`
  --> support/triage.jip:27:3
   |
27 |   const budget = Budget { max_turns: 12 }
   |                  ^^^^^^^^^^^^^^^^^^^^^^^^ expected `Budget { max_turns: Int, max_cost: Money }`
   = help: write `Budget { max_turns: 12, max_cost: Money(500) }`; both limits are required
```

## Grammar

```
LlmCall     ::= "llm" "<" RoleLit ("," Type)? ">" "(" Expr "," Expr ")"
DecideCall  ::= "decide" "<" RoleLit ("," Type)? ">" "(" Expr ")"
ApproveCall ::= "approve" ("<" Type ">")? "(" Expr ")"
ToolsExpr   ::= "tools" "[" (Ident ("," Ident)*)? "]"
AwaitSignal ::= "await_signal" "<" Type ">" "(" Expr ")"
Expr        ::= … | LlmCall | DecideCall | ApproveCall | ToolsExpr | AwaitSignal
```

`BehaviourDecl`, `ImplDecl`, `ConstDecl`, `RoleLit` are in section 06;
`FnDecl`, `EffectSet`, `Type` in sections 02 and 03.
