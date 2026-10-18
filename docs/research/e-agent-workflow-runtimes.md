# Research track (e): agent and workflow runtimes

Status: draft, 2026-10-18. Inputs: `docs/decisions.md` §4, §6, §7; `docs/research/plan.md` (e).

## Summary

All four durable runtimes (Temporal, Restate, Inngest, LangGraph) share one model: **the workflow body re-executes from the start on recovery, and every side-effecting or nondeterministic step is journaled so replay returns the recorded result instead of re-running it.** They differ only in journal keying (command sequence; journal order; hashed step id + counter; per-thread checkpoints) and enforcement (Temporal fails the run; Inngest warns). The agent loop is identical across Mastra, LangGraph and the Claude Agent SDK: `prompt -> model -> tool calls -> execute -> feed results -> repeat` until no tool call, a stop predicate, or a turn/cost cap. Human-in-the-loop is always "suspend, persist, resume with a typed payload" plus a timeout.

Hence jippitese must own the **replay-safety rule** (which effects may appear in a workflow body and how each is journaled) and the **typed shapes** of `Workflow`, `Agent` and `approve`; journal storage, retries, timers, signal delivery and versioning are a runtime library configured in `app.jip`. The evidence reopens two decisions (see Recommendations).

## Findings (per system)

**Temporal (Go SDK).** State is rebuilt by replaying the event history against the same code; if the emitted commands diverge from the recorded ones the SDK raises a nondeterminism error. Go rules: no `time.Now`/`time.Sleep` (use `workflow.Now`/`workflow.Sleep`), no `go`/`chan`/`select` (use `workflow.Go`/`Channel`/`Selector`), no `range` over maps, no external calls except via Activities. Escape hatches: `SideEffect(ctx, f)` records a marker on first run and returns it on replay (must not mutate workflow state); `MutableSideEffect(ctx, id, f, equals)` re-records only when the value changes. Versioning: `GetVersion(ctx, changeID, min, max)` writes a marker so old histories take the old branch; Worker Versioning pins runs to a build id. HITL: Signals, Updates, timers. Long runs use `ContinueAsNew`.

**Restate.** Each invocation keeps an execution journal; on failure or suspension the handler re-runs and journaled entries replay. Side effects go in `ctx.run(name, fn)`, whose result is journaled, whose body may not call other `ctx` methods, and which is retried per policy. Combinators (`Promise.all/any`) are journaled so ordering replays. Handler kinds: stateless Services, keyed Virtual Objects, Workflows (one `run` plus signal handlers). HITL: **awakeables** (ID handed to an external system, resolved/rejected over HTTP) and **durable promises** (`ctx.promise("review")`, workflow-scoped, name-keyed, resolved from another handler), raced against a durable timer for timeouts.

**Inngest.** The whole function re-invokes on every execution; each `step.run(id, fn)` result is memoized under hash(step id, occurrence counter), so only the next unmemoized step executes. Nondeterministic logic must live inside steps; pure code may stay outside. No version markers: changed step order logs a warning, new steps run when first encountered, a renamed step re-executes. `step.sleep("7d")`, `step.sleepUntil`, `step.waitForEvent(id, {event, timeout, match})` returns `null` on timeout and only matches events sent after the wait began. Third-party reports cite limits of ~1,000 steps and 4 MB per step output.

**LangGraph.** `StateGraph` nodes run in super-steps; a checkpointer saves state after each super-step per `thread_id`, enabling time travel. Nodes must be deterministic and idempotent; side effects and randomness belong in `@task` functions, whose results persist and are reused on resume. `interrupt(payload)` pauses the graph; `Command(resume=value)` restarts the run and `value` becomes the interrupt's return. Critically, **the node re-runs from its beginning on resume**, so code before `interrupt()` executes again (a subgraph interrupt re-runs the parent node too).

**Mastra.** Workflows are `createStep` units with `inputSchema`/`outputSchema` composed by `.then/.branch/.parallel`; `suspend()` snapshots the run into the storage provider and `resume({step, resumeData})` restarts from that step, validated by `suspendSchema`/`resumeSchema`. Agents: tool approval via `requireToolApproval` (per run) or `requireApproval` (per tool), then `approveToolCall`/`declineToolCall` keyed by `runId`. Loop control: `maxSteps`, AI-SDK `stopWhen`/`stepCountIs(n)`; a run ends on a final answer, stop predicate, or cap.

**Claude Agent SDK.** Packages the Claude Code loop: prompt -> model -> `tool_use` blocks -> execute -> `tool_result` -> repeat until `end_turn`, `max_turns` (tool-use round trips) or `max_budget_usd` (spend, includes subagents; may overshoot by one response). Results carry `stop_reason` (`end_turn`, `max_tokens`, `refusal`) and error subtypes (`error_max_turns`, `error_max_budget_usd`). Permission order is fixed: hooks (`PreToolUse` can deny) -> deny rules (even in `bypassPermissions`) -> ask rules -> mode/allow rules -> `canUseTool` callback (the human prompt). Sessions persist transcripts (`resume` by id, `fork` copies history); context is compacted automatically. No journal: a crash yields `error_during_execution`; durability is the host's job.

## Trade-offs

- **Journal keying.** Sequence-keyed (Temporal, Restate) catches every divergence but makes code edits a versioning event. Id-keyed (Inngest, Mastra) tolerates edits but silently re-runs renamed steps. LangGraph keys by node and re-executes node prefixes, pushing idempotency onto authors.
- **Enforcement.** Temporal fails loudly; Inngest warns; LangGraph and Mastra trust the author. A compiler can reject nondeterminism statically, which none of them can.
- **Granularity.** Journaling every call costs storage and hits size caps; coarse steps lose progress on retry. `SideEffect`/`LocalActivity` and `ctx.run` exist because one size does not fit.
- **HITL.** Signals, awakeables and durable promises are one primitive (name-keyed, typed resume payload, timeout race); approval-before-tool-call is a specialisation of it.
- **Agent durability.** No agent framework journals the LLM loop; LangGraph and Mastra get it only by hosting the loop inside their workflow engine. Agent SDK sessions are resumable transcripts, not replayable journals.

## Recommendations for jippitese

1. **Replay-safety rule (language-owned).** Inside a `Workflow.run` body, every call with effect `io`, `net`, `llm`, `decide`, `nondet` or `approve` must be a *step*: it is journaled by position in a per-run sequence, returns the recorded value on replay, and must not touch workflow state from inside. `mut` on shared cells is forbidden in a workflow body. Pure code and `Workflow`-provided `now()`, `sleep()`, `random()` are allowed. The compiler enforces this from effect signatures; the generated Go calls the runtime's step API. Journal keying is sequence-based with a compiler-derived step id (`<fn>#<ordinal>`), and each `Workflow` impl carries a `version` field; a mismatch between history and current code is a typed `ReplayError`, never a silent re-run.
2. **`Workflow` behaviour shape.**
   ```
   behaviour Workflow {
     type Input; type Output; type Signal   // Signal: closed sum type
     fn run(self, in: Input) -> Result<Output, WorkflowError> with io, net, llm, decide, nondet, approve, spawn
     fn on_signal(self, s: Signal)          // optional
     const version: u32
   }
   ```
   Runtime provides `step`, `sleep`, `now`, `random`, `await_signal<T>(name, timeout) -> Option<T>` and `continue_as_new`. `spawn` inside `run` lowers to runtime-scheduled deterministic coroutines, not goroutines, which keeps §7 structured concurrency semantics but with a different implementation.
3. **`approve` effect = durable promise.** `approve<T>(prompt: Review) -> Result<T, Rejected> with approve` is a named, journaled suspension with a mandatory `timeout`; the resume payload type `T` is a closed sum or `bool`. It is usable inside `Workflow` (durable) and, via the tool gate, inside `Agent` (per tool call, like Mastra `requireApproval`/Agent SDK `canUseTool`).
4. **`Agent` behaviour shape.**
   ```
   behaviour Agent {
     type State; type Answer
     const model: Role; const tools: [Tool]
     const budget: Budget            // max_turns and max_cost, both required
     fn stop(self, st: State) -> Option<Answer>      // pure
     fn gate(self, call: ToolCall) -> Gate            // Allow | Deny(reason) | Ask, with decide or approve
   }
   ```
   Runtime owns the loop and context compaction; a run ends with `Answer`, `Stopped(budget)` or `Refused`. An `Agent` run inside a `Workflow` is one step per model call so that it replays.
5. **Delegate to a runtime library**: journal storage, retry/timeout policy (attached to effects per §4), timers, signal transport, worker versioning, telemetry. Selected by one `app.jip` line (`workflow: embedded | temporal | restate`), with `embedded` (SQLite journal, single process) as the v0 default.
6. **Decisions reopened.** (a) §4 effect list: `nondet` must split time/random from the step-journaled effects, and `io/net/llm/decide` need a journaled form inside workflows; this interacts with track (b)'s choice of effect surface. (b) §7 "structured concurrency only": inside `Workflow.run`, `spawn` must map to runtime coroutines, not goroutines, and `mut` is disallowed, so the spec must say the two concurrency models differ. (c) Deferred "Durable workflow runtime model": resolved as "language owns the step rule and types, library owns the journal."

## Sources

- Temporal Go `workflow` package reference: https://pkg.go.dev/go.temporal.io/sdk/workflow
- Temporal workflow determinism and versioning: https://docs.temporal.io/develop/go/versioning ; https://docs.temporal.io/workflow-definition
- Restate journaling and `ctx.run`: https://docs.restate.dev/concepts/durable_execution ; https://docs.restate.dev/develop/ts/journaling-results
- Restate awakeables and durable promises: https://docs.restate.dev/develop/ts/awakeables ; https://docs.restate.dev/develop/ts/workflows
- Inngest steps, memoization and `waitForEvent`: https://www.inngest.com/docs/learn/how-functions-are-executed ; https://www.inngest.com/docs/reference/functions/step-wait-for-event
- LangGraph durable execution and interrupts: https://docs.langchain.com/oss/python/langgraph/durable-execution ; https://docs.langchain.com/oss/python/langgraph/interrupts
- Mastra suspend/resume and tool approval: https://mastra.ai/docs/workflows/suspend-and-resume ; https://mastra.ai/docs/agents/human-in-the-loop
- Claude Agent SDK overview, agent loop, permissions, sessions: https://code.claude.com/docs/en/agent-sdk/overview ; https://code.claude.com/docs/en/agent-sdk/agent-loop ; https://code.claude.com/docs/en/agent-sdk/permissions ; https://code.claude.com/docs/en/agent-sdk/sessions
