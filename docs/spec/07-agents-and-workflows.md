# 07 Agents and workflows

Three primitives in v0: `llm` (structured model call), `decide` (System One
classifier) and `approve` (durable human promise); one behaviour that hosts
them: `Workflow` (journaled, replayable). Tools derived from functions and the
`Agent` behaviour (runtime-owned model loop) are v1 and specified at the end.
Declarations are in section 06; semantics are here.

## Example

```jip
module support.escalation
use std.workflow
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

/// A human's answer to a severe ticket.
pub enum Verdict { Escalate, Close }

/// Escalation failure.
pub enum EscalationError { Missing(TicketId), Model(LlmError), Closed(TicketId) }

/// Loads a ticket by id.
pub fn lookup(id: TicketId) -> Result<Ticket, EscalationError> with {io} {
  Tickets.get(id).map_err(fn(_) => Missing(id))?.ok_or(Missing(id))
}

/// Escalates a ticket durably: classify, summarise, ask a human when severe.
impl Workflow for Escalation {
  type Input = TicketId
  type Output = Summary
  type Signal = Cancel
  type E = EscalationError
  const version = 1
  fn init(cfg: Config) -> Result<Self, EscalationError> with {io} { Ok(Escalation {}) }
  fn run(self, id: TicketId) -> Result<Summary, WorkflowError<EscalationError>> with {io, net, nondet, llm<smart>, decide<fast>, approve} {
    let t = lookup(id)?                                        // step lookup@<site>#1; Missing lifts to App(_)
    let sev: Severity = decide<fast>(t)                        // step decide@<site>#1
    let s = llm<smart>("Summarise this ticket.", t).map_err(fn(e) => WorkflowError.App(Model(e)))?   // step llm@<site>#1
    if sev == High {
      let review = Review { title: "Severe ticket {id}", body: s.headline, timeout: Some(24h), data: None }
      match approve::<Verdict>(review)? {                      // step approve@<site>#1, durable promise; Rejected lifts to Approval(_)
        Escalate => Ok(s),
        Close => Err(WorkflowError.App(Closed(id))),
      }
    } else {
      Ok(s)
    }
  }
}
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
   inferred from the binding annotation or written as `llm<role, O>`. `llm`
   is a keyword (section 01 rule 5), so `llm<role>` is a special form, not a
   generic call, and takes no turbofish.
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

### `decide`

4. `decide<role>(input: I) -> O with {decide<role>}`, no prompt string. `I`
   is schema-derivable. `O` is one of: a closed payload-less enum, `Score` (a
   stdlib newtype over `Float` with `inv` 0 ≤ value ≤ 1), or
   `Calibrated<Bool>` (`{ value: Bool, p: Score }`). Anything else is a
   compile error. The call is provider-agnostic; `app.jip` maps the role.
   `decide` is a keyword like `llm` (rule 1).

### `approve`

5. `approve::<T>(review: Review) -> Result<T, Rejected> with {approve}` with
   `Review { title: Str, body: Str, timeout: Option<Duration>, data:
   Option<Json> }`. `T` is a closed payload-less enum or `Bool` with `T:
   Decode`, written in the turbofish or inferred from the binding annotation.
   A `timeout` of `None` uses `effects.approve.default_timeout` from
   `app.jip`. `data` is passed to the channel unread; v0 attaches no meaning
   to it and it is reserved for the approvals library (revision-plan §10).
   `Rejected { reason: Option<Str>, timed_out: Bool }`.
6. **Admission (revision-plan R4).** `approve` is allowed only in
   `Workflow.run`, in `Agent.gate`, and in functions
   reachable only from those (the effect check of section 03 makes any other
   caller carry `approve` and fail its budget, E0702; a plain `pub fn` with
   `approve` that nothing in a `run` calls is E0707). There is no blocking
   form: an approval is always a durable suspension of a journaled run.
7. **Delivery.** An `approve` call is a durable promise keyed `approve@<site>#<n>`
   like a step (rule 9). The runtime delivers the review to the channel
   named by `effects.approve.channel` in `app.jip` and suspends the run; a
   reply resolves it, the timeout rejects it with `timed_out: true`. v1
   ships one channel, `std.approve.http`: an HTTP endpoint without
   authentication, so it listens only on a loopback address,
   `JIP_APPROVE_ADDR` (default `127.0.0.1:8089`, any other host aborts
   startup). `GET /reviews` lists pending reviews oldest first as
   `{id, title, body, data, deadline}`, `GET /reviews/{id}` shows one,
   `POST /reviews/{id}/approve` takes the `T` value as its JSON body (empty
   when `T` is `Unit`; a body that does not decode as `T` is a 400 and the
   review stays pending) and `POST /reviews/{id}/reject` takes an optional
   `{"reason": Str}`; answering an id that is not pending is a 404. Pending
   reviews live in memory; see the restart rule below for what survives.
   Any other `effects.approve.channel` names a module of the project that
   declares `pub fn ask(id: Str, review: Review) -> ReviewAnswer` (anything
   else is E0301), with the prelude's `enum ReviewAnswer { Approve(Json),
   Reject(Option<Str>) }`. The runtime calls `ask` once per pending
   review, on its own task, with the review's id `<run>/approve@<site>#<n>`
   (stable across restarts, so a channel can deduplicate on it); `ask` may
   block until a reviewer answers. `Approve(j)` resolves the review with
   `j` decoded as `T` (a value that does not decode rejects it, with a
   reason saying so); `Reject(r)` rejects it with reason `r`; the timeout
   rejects it with `timed_out: true` and cancels `ask`'s task. A panic in
   `ask` fails the run as a panic in the workflow would. **Restart.** Under
   `workflow { mode: embedded }` with a store, a run's journal header keeps
   its id and input; on start every `Workflow` runner resumes the runs of
   its workflow whose journal ends suspended on an `approve`, replaying
   their recorded steps and asking the channel again. The review keeps its
   id for a user channel and gets a new one, with a fresh deadline, under
   `std.approve.http`. Runs under `temporal` and `restate` are not resumed
   in v1.

### `Workflow`

8. **Declaration.** As in section 06: `Input`, `Output`, `Signal` (closed
   sum), `E` (closed sum), `const version: Int`, `init(cfg: Config) ->
   Result<Self, E>`, `run(self, in: Input) -> Result<Output,
   WorkflowError<E>> with {io, net, llm, decide, nondet, spawn, approve}`,
   `on_signal(self, s: Signal) {}`. `WorkflowError<E> = Replay(ReplayError)
   | Approval(Rejected) | Cancelled | App(E)`. `?` inside `run` is the
   ordinary `?` of section 04: a `Result<_, E>` lifts through `App`, a
   `Result<_, Rejected>` through `Approval`, both by the generated `From`
   impls; any other error type is converted explicitly with `map_err`. There
   is no lift of `Show` into a string variant.
9. **Step rule (revision-plan R6).** Every call carrying `io`, `net`,
   `nondet`, `llm`, `decide` or `approve` in `run`, or in a function `run`
   calls transitively that carries one of those effects, is lowered to a
   journaled step keyed `<callee>@<site>#<n>`: `<callee>` is the callee
   name, `<site>` a stable id of the call site (file path, item name and a
   per-item counter assigned in source order), `<n>` the number of times
   this site has executed so far in the current task (one counter per
   task, rule 15). The set of site ids a version uses is written to the
   journal with the version (rule 14). On replay a step returns the recorded
   value without executing. A pure call is never a step. Two consequences,
   stated as the examples a reviewer asks for: inserting a new step before
   `charge(o)` does not change the key of `charge(o)` (the site id is per
   site, not an ordinal), so an old history still replays once `version` is
   bumped and the new site is absent from the history's site set, which
   rule 14 reports as `Replay`; a `charge(o)` inside a retry loop, a closure
   or a recursive call gets `charge@<site>#1`, `#2`, `#3` from the same site,
   so the third attempt replays the third recorded value and never the
   first.
10. **No `mut`.** `mut` is forbidden in `run` and in anything it calls; it
    is absent from the budget, so this is the ordinary effect-subset check,
    reported with the workflow-specific message below. `var` locals are
    allowed.
11. **`nondet` primitives.** `time.now()`, `random()`, `time.sleep(d)` and
    `await_signal` (rule 12) inside `run` resolve to runtime-journaled
    versions: `now()` returns the first-run time on replay, `sleep` is a
    durable timer, a buffered signal replays as recorded. Under `jip test`
    these sit on the fake clock of section 12 rule 10.
12. **Signals.** `await_signal::<Signal>(timeout: Duration) -> Option<Signal>
    with {nondet}` is a step that suspends until a signal sent by
    `Name.signal(run, s)` arrives or the timeout elapses (`None`). Signals
    sent while no `await_signal` is pending are buffered in arrival order.
    `on_signal` runs, pure, on each arrival; v0 gives it no way to reject.
13. **`continue_as_new(in: Input) -> Result<Output, WorkflowError<E>> with
    {io}`** closes the current history and starts a fresh one under the same
    `RunId`; `run` must return its value directly.
14. **Versioning and replay.** `version` must change whenever the step
    sequence changes. On replay the runtime compares the history's version
    and site set with the current code and each recorded step key with the
    key the current code produces; any mismatch yields
    `Err(WorkflowError.Replay(ReplayError { expected, found }))` for that
    run. There is no silent re-run.
15. **`spawn`.** Inside `run`, `spawn` and `task` keep section 08 semantics
    but lower to deterministic runtime coroutines; each task has its own
    step counter (rule 9) and its steps are keyed under the task's site,
    `task@<site>#<n>/<callee>@<site>#<m>`. `Cell` cannot be created or
    passed in.
16. **Runtime.** `workflow { mode: embedded, store: default
    ".jip/workflow" }` in `app.jip` selects the journal: one append-only
    JSON-lines file per run, `<store>/<Impl>-<run id>.jsonl`, one process. The mode names a runtime adapter behind one interface
    (journal, results, signals). `temporal` keeps each run as a Temporal
    workflow execution whose history is the journal (`store`:
    `host:port[/namespace]`); `restate` keeps it as a key of the virtual
    object `jip` whose state is the journal (`store`: the ingress
    `host:port`, optionally `,admin=host:port` and `,endpoint=host:port`).
    Under every mode the run's body executes in the process that started
    it and signals are buffered in that process; a run whose process dies
    is not resumed. `Name.start(in: Input) -> RunId with
    {io}`, `Name.signal(run: RunId, s: Signal) with {io}` and
    `Name.result(run: RunId) -> Option<Result<Output, WorkflowError<E>>>
    with {io}` are generated for every impl; `None` means still running.

### Tools and `Agent`

*Status: v1 (backlog L5).* An `Agent` is invoked through `Name.run(goal)`;
it is not admitted in the `run` list of `app.jip`.

17. `tools [f, g]` is a `List<Tool>` built from `pub fn` values. For each
    function the tool takes its name, its `///` line as the description, its
    parameters as a schema (names, types, their doc lines), and its declared
    effects. Every parameter and the return type must be schema-derivable;
    the return type may be `Result<T, E>` with derivable `T` and `E`.
18. A tool's effects join the agent's: `Name.run` carries `{llm<model>} ∪
    gate effects ∪ every tool's effects`. A non-`pub` function, a closure or
    a method cannot be a tool.
19. **Declaration.** `State`, `Answer`, `const model: Role`, `const tools:
    List<Tool>`, `const budget: Budget` with `Budget { max_turns: Int,
    max_cost: Money }` both required; `begin(self, goal: Str) -> State`,
    `observe(self, st: State, ev: Event) -> State` and `stop(self, st:
    State) -> Option<Answer>` are pure; `gate(self, call: ToolCall) -> Gate
    with {decide, approve}`. `Event = Said(Str) | Called(ToolCall) |
    Returned(ToolCall, Str) | Compacted`; `ToolCall { tool: Tools, args:
    Json }` where `Tools` is a closed enum generated per impl with one
    variant per entry of `tools` (`lookup` becomes `Lookup`), so `gate`
    matches exhaustively and a misspelt tool name is a compile error;
    inside the impl it is `Tools`, outside `<Target>Tools`; `Gate = Allow | Deny(Str) |
    Ask`; `Answer` must be schema-derivable, since the model is asked to
    produce it.
20. **Loop.** `Name.run(goal: Str) -> Outcome<Answer>` (effects per rule 18)
    runs: `st = begin(goal)`; repeat { model call with the transcript and
    the tool schemas; for each tool call, `gate` (`Ask` routes through
    `approve` with a generated `Review`, `Deny(reason)` returns the reason
    to the model); execute allowed tools; `st = observe(st, ev)` per event;
    `stop(st)` } until `Some(answer)`, the budget is hit, or the model
    refuses. The runtime owns context compaction and reports `Compacted`.
21. **Outcome.** `enum Outcome<Answer> { Finished(Answer),
    Stopped(BudgetExceeded), Refused(Str) }` with `BudgetExceeded = Turns |
    Cost`. `max_cost` is checked before each model call and may be exceeded
    by at most one call.
22. **Inside a `Workflow`.** `Name.run(goal)` called from `run` is journaled
    as one step per model call (`Triage.run@<site>#1/turn#k`) plus one per
    gate decision (`/gate#k`), reviewer answer (`/approve#k`) and tool
    execution (`/tool#k`), so a crashed agent resumes mid-loop.
23. **Example (v1).**

```jip
use std.json

/// What the agent has seen so far; `TriageTools` is generated (rule 19).
pub type TriageState { goal: Str, seen: List<TriageTools>, answer: Option<Summary> }

/// Resolves one support ticket with a bounded tool loop.
impl Agent for Triage {
  type State = TriageState
  type Answer = Summary
  const model = smart
  const tools = tools [lookup, refund]
  const budget = Budget { max_turns: 12, max_cost: Money(200) }
  fn init(cfg: Config) -> Result<Self, AgentError> with {io} { Ok(Triage {}) }
  fn begin(self, goal: Str) -> TriageState { TriageState { goal, seen: [], answer: None } }
  fn observe(self, st: TriageState, ev: Event) -> TriageState {
    match ev {
      Event.Returned(call, _) => TriageState { ..st, seen: st.seen.push(call.tool) },
      Event.Said(text) => TriageState { ..st, answer: json.decode::<Summary>(text.bytes()).ok() },
      Event.Called(_) | Event.Compacted => st,
    }
  }
  fn stop(self, st: TriageState) -> Option<Summary> { st.answer }
  fn gate(self, call: ToolCall) -> Gate with {approve} {
    match call.tool {
      Tools.Refund => Gate.Ask,              // routed through approve
      Tools.Lookup => Gate.Allow,
    }
  }
}
```

TypeScript equivalent, with the Vercel AI SDK:

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

## Diagnostics

`mut` inside `Workflow.run` (rule 10):

```text
error[E0703]: effect `mut` is not allowed inside `Workflow.run`
  --> support/escalation.jip:52:5
   |
52 |     bump(counter)
   |     ^^^^^^^^^^^^^ `bump` has effect `mut`
   = note: `run` may use {io, net, llm, decide, nondet, spawn, approve}
   = help: keep workflow state in a `var` or return it from a step
```

A type that is not schema-derivable (rule 2):

```text
error[E0704]: type `Summary` is not schema-derivable for `llm<smart>`
  --> support/escalation.jip:12:3
   |
12 |   render: fn(Ticket) -> Str,
   |   ^^^^^^^^^^^^^^^^^^^^^^^^^ function types have no schema
   = note: schema-derivable: primitives, Duration, newtypes, records, closed enums, tuples, Option, List, Map<Str, _>
   = help: remove `render` from `Summary` or move it to a separate type
```

`approve` outside a workflow (rule 6):

```text
error[E0707]: `approve` is reachable only from `Workflow.run`
  --> support/escalation.jip:30:60
   |
30 | pub fn confirm(t: Ticket) -> Result<Bool, Rejected> with {approve} {
   |                                                           ^^^^^^^ no `Workflow.run` calls `confirm`
   = note: an approval suspends a journaled run; a plain function has no journal
   = help: call `confirm` from a `Workflow.run`, or move the `approve` into one
```

A budget without `max_cost` (rule 19, v1):

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
ApproveCall ::= "approve" ("::" "<" Type ">")? "(" Expr ")"
AwaitSignal ::= "await_signal" ("::" "<" Type ">")? "(" Expr ")"
ToolsExpr   ::= "tools" "[" (Ident ("," Ident)*)? "]"      /* v1 */
Expr        ::= … | LlmCall | DecideCall | ApproveCall | AwaitSignal | ToolsExpr
```

`llm`, `decide` and `approve` are keywords; `await_signal` and `tools` are
ordinary identifiers resolved from `std.workflow` and `std.agent`.
`BehaviourDecl`, `ImplDecl`, `ConstDecl`, `RoleLit` are in section 06;
`FnDecl`, `EffectSet`, `Type` in sections 02 and 03.
