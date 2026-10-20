# 06 Behaviours

A behaviour is a stdlib-declared pattern contract: associated types, consts,
methods with an effect *budget*, contracts and a lifecycle. One `impl`
construct implements any of them; the compiler checks the impl completely;
the runtime supervises every instance.

## Example

```jip
module orders.index
use std.behaviour
use orders.events                 // OrderEvent, Order, OrderId

/// Index failure.
pub enum IndexError { Backend(Str) }

/// Keeps the search index in step with order events.
pub type Indexer { search: SearchUrl }

/// Re-indexes one order per event; repeating an event is harmless.
impl Worker for Indexer {
  type Msg = OrderEvent
  type E = IndexError
  const retry = Retry { max: 5, backoff: Exp(1s) }
  fn init(cfg: Config) -> Result<Self, IndexError> with {io} { Ok(Indexer { search: cfg.search }) }
  fn perform(self, m: OrderEvent) -> Result<(), IndexError> with {net} {
    let o = Orders.get(m.order_id)?          // references the Orders impl: Orders starts first
    search.upsert(self.search, m.order_id, o).map_err(fn(e) => Backend("{e}"))
  }
}

/// Orders by id, in SQLite.
impl Repository<Order, OrderId> for Orders {
  fn init(cfg: Config) -> Result<Self, RepoError> with {io} { sqlite.open(cfg.db).map(fn(c) => Orders { conn: c }) }
  fn get(self, k: OrderId) -> Result<Option<Order>, RepoError> with {io} { sqlite.get::<Order>(self.conn, k.0) }
  fn put(self, k: OrderId, v: Order) -> Result<(), RepoError> with {io} { sqlite.put(self.conn, k.0, v) }
  fn delete(self, k: OrderId) -> Result<(), RepoError> with {io} { sqlite.delete(self.conn, k.0) }
  fn query(self, q: Query<Order>) -> Result<List<Order>, RepoError> with {io} { sqlite.query(self.conn, q) }
}
```


`app.jip` declares the config fields and the topic (section 10) and starts
both instances:

```jip
app {
  config { search: SearchUrl, db: Path }
  topics { order_events: Topic<OrderEvent> }
  run Orders
  run Indexer { topic: order_events, restart: transient, max_restarts: 5, period: 60s }
}
```

Go equivalent: an interface plus a hand-written supervisor loop; the
behaviour replaces both and adds the checks Go cannot express.

```go
type Worker[M any] interface {
    Init(cfg Config) error
    Perform(ctx context.Context, m M) error
    Stop(ctx context.Context) error
}

func supervise(ctx context.Context, w Worker[OrderEvent], in <-chan OrderEvent) {
    for m := range in { // plus restart counting, back-off and health probes by hand
        if err := retry(ctx, 5, expBackoff(time.Second), func() error { return w.Perform(ctx, m) }); err != nil {
            deadLetter(m, err)
        }
    }
}
```

## The six v0 behaviours

All are declared in `std.behaviour`. `Config` is the record of the `config {}`
block of `app.jip` plus a `secrets` field (section 10, rule 5 below).
`Health { live: Bool, ready: Bool, detail: Option<Str> }`.

```jip
pub behaviour Service {
  type Req
  type Resp
  type E
  fn init(cfg: Config) -> Result<Self, E> with {io, net, mut}
  fn handle(self, req: Req) -> Result<Resp, E> with {io, net, mut, llm, decide, nondet, spawn}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.aggregate(self) }
}

pub behaviour Worker {
  type Msg
  type E
  const retry: Retry                       // Retry { max: Int, backoff: Backoff }; Backoff = Fixed(Duration) | Exp(Duration)
  fn init(cfg: Config) -> Result<Self, E> with {io, net, mut}
  fn perform(self, m: Msg) -> Result<(), E> with {io, net, mut, llm, decide, nondet}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.aggregate(self) }
}

pub behaviour Cron {
  type E
  const schedule: Schedule                 // `every 5m` or `cron "0 * * * *"`
  fn init(cfg: Config) -> Result<Self, E> with {io, net, mut}
  fn run(self) -> Result<(), E> with {io, net, mut, llm, decide, nondet}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.aggregate(self) }
}

pub behaviour Workflow {                   // semantics in section 07
  type Input
  type Output
  type Signal                              // closed sum
  type E                                   // closed sum: the application's own failures
  const version: Int
  fn init(cfg: Config) -> Result<Self, E> with {io, net, mut}
  fn run(self, in: Input) -> Result<Output, WorkflowError<E>> with {io, net, llm, decide, nondet, spawn, approve}
  fn on_signal(self, s: Signal) {}
}

pub behaviour Repository<T, K> {
  fn init(cfg: Config) -> Result<Self, RepoError> with {io, net, mut}
  fn get(self, k: K) -> Result<Option<T>, RepoError> with {io, net}
  fn put(self, k: K, v: T) -> Result<(), RepoError> with {io, net}
  fn delete(self, k: K) -> Result<(), RepoError> with {io, net}
  fn query(self, q: Query<T>) -> Result<List<T>, RepoError> with {io, net}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.up() }
}

pub behaviour Cache<K, V> {
  fn init(cfg: Config) -> Result<Self, CacheError> with {io, net, mut}
  fn get(self, k: K) -> Result<Option<V>, CacheError> with {io, net, nondet}
  fn set(self, k: K, v: V, ttl: Option<Duration>) -> Result<(), CacheError> with {io, net, nondet}
  fn invalidate(self, k: K) -> Result<(), CacheError> with {io, net, nondet}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.up() }
}
```

`RepoError = NotFound | Conflict | Unavailable(Str) | Invalid(Str)`;
`CacheError = Unavailable(Str)`; `WorkflowError<E>` is declared in section 07
rule 9. Budgets, summarised:

| Behaviour | Method | Budget |
|---|---|---|
| `Service` | `handle` | `io net mut nondet llm decide spawn` |
| `Worker` | `perform` | `io net mut nondet llm decide` |
| `Cron` | `run` | as `Worker` |
| `Workflow` | `run` | `io net nondet llm decide approve spawn`, every call journaled |
| `Repository` | all but `init` | `io net mut` |
| `Cache` | all but `init` | `io net mut nondet` |
| all | `init` | `io net mut` |
| all | `stop`, `health` | `io net` |

`nondet` is added to the `Service`, `Worker` and `Cron` budgets relative to
the research table because `time.now()` carries it. `mut` is in every
`init` budget because constructing a `Cell`, `MutList`, `MutMap` or
`MutSet` is `mut`, and without it no impl could hold in-process state; it
is in the `Repository` and `Cache` budgets so an in-process store can
read and write its own maps. `approve` is in the
`Workflow` budget only: an approval is a durable suspension, and only a
journaled `run` can resume after one (section 07 rule 7).

### Deferred from v0

*Status: deferred from v0 (revision-plan R1).* `Machine` and `Agent` keep
their declarations here so that the v1 shape is fixed, but no v0 compiler
accepts an `impl` of either (E0706) and the `run` list of section 10 does not
admit them. The `Agent` semantics are in section 07 under the same banner.

```jip
pub behaviour Machine {
  type Key
  type State                               // closed sum
  type Event                               // closed sum
  type Action                              // closed sum
  type E
  fn init(cfg: Config) -> Result<Self, E> with {io, net, mut}
  fn activate(self, key: Key) -> State
  fn on(self, s: State, e: Event) -> (State, List<Action>)
  fn act(self, key: Key, a: Action) -> Result<List<Event>, E> with {io, net, llm, decide, nondet}
  fn deactivate(self, key: Key, s: State) with {io, net} {}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.aggregate(self) }
}

pub behaviour Agent {                      // semantics in section 07
  type State
  type Answer
  const model: Role
  const tools: List<Tool>
  const budget: Budget                     // Budget { max_turns: Int, max_cost: Money }
  fn init(cfg: Config) -> Result<Self, AgentError> with {io, net, mut}
  fn begin(self, task: Str) -> State
  fn observe(self, st: State, ev: Event) -> State
  fn stop(self, st: State) -> Option<Answer>
  fn gate(self, call: ToolCall) -> Gate with {decide, approve}
}
```

| Behaviour | Method | Budget |
|---|---|---|
| `Machine` | `on` pure; `act` | `io net nondet llm decide` |
| `Agent` | `gate` | `decide approve`; `begin`, `observe`, `stop` pure; tools add their own |

`Machine` rule (v1): `on` is pure and must `match` exhaustively over `(State,
Event)`. One turn per key at a time: `activate` on the first event, `on`,
persist the state, then `act` for each returned `Action`, whose events queue
as later turns of the same key. Events enter by `Name.send(key, e) ->
Result<(), MachineError> with {io}`. `deactivate` runs when a key idles past
the runtime's timeout; the state is reloaded on the next event.

## Rules

1. **Declaring.** A `behaviour` body holds `type A`, `const c: T`, `fn`
   signatures, `fn` with a body (default method) and `inv` lines. The effect
   set on a method is its budget. User code may declare only *constraint*
   behaviours: methods only, no `type`, `const`, `inv`, defaults or effects
   (`behaviour Show { fn show(self) -> Str }`), for generic bounds. Lifecycle
   behaviours come from the stdlib.
2. **Implementing.** `impl Name for Type { … }` supplies every associated
   type, every const and every method without a default; a default may be
   overridden. Nothing else may appear. `impl Type { … }` with no `for` is an
   inherent impl (section 03 rule 4): ordinary methods on `Type`, no
   behaviour involved.
3. **Effect subset.** Each impl method's effect set must be a subset of the
   budget. A budget label `llm` or `decide` without a role admits any role.
4. **Contract refinement.** An impl method may not add `pre`; it may add
   `post`, checked in addition to the behaviour's. A behaviour `inv` applies
   to `Self` and is checked when `init` returns.
5. **Lifecycle.** `init(cfg: Config) -> Result<Self, E> with {io, net, mut}` is
   static (no `self`). `Config` is a record with one field per entry of the
   `config {}` block of `app.jip` plus `secrets: Secrets`, one field per entry
   of `secrets {}` (section 10 rules 5 and 6); a scenario builds one with
   `Secrets.fake()` (section 12). `stop` and `health` have defaults.
   `Health.aggregate` reports `live = true` once `init` returned and `ready`
   as the conjunction of `health().ready` over every `Repository` and `Cache`
   the type references (rule 12); `Health.up()` reports both true. A contract
   violation inside a behaviour method (a `pre` on a callee, a `post`, an
   `inv`) never crashes the instance: the method returns `Err` carrying the
   runtime error `ContractViolation` and the runtime maps it to the
   behaviour's failure path, HTTP 500 for a `Service`, a failed message
   (retried per `retry`) for a `Worker`, a failed tick for a `Cron`, a
   failed step and task for a `Workflow`, `Err(Unavailable(_))` for a
   `Repository` or `Cache`. The violation is logged and counted
   (`jip.contract.violations`, section 13). Plain functions panic instead
   (section 04 rule 13, section 05 rule 10).
6. **Instances by name.** A started impl is addressed by its type name:
   `Orders.get(k)`, `Indexer.enqueue(m)`; the call has the method's effects
   and resolves to the one instance from the `run` line. There is no handle
   value.
7. **`Service`.** `E` is a closed enum. The v0 transport is fixed: `handle`
   is served at `POST /<name>` with JSON `Req` and `Resp` on the `port` of
   its `run` line (rule 13); `Ok(resp)` is 200 and `Err(e)` is 500 with `e`
   as the JSON body.
8. **`Worker`.** Messages come from the topic named on its `run` line or,
   without one, from a runtime queue fed by `Name.enqueue(m) -> Result<(),
   QueueError> with {io}`. `Err` from `perform` is retried per `retry` (a
   compile-time literal); after `max` failures the message is dead-lettered.
   `jip test` derives an idempotency check `<Impl>.idempotent` for every
   non-generic impl whose `Msg` has a structural generator: for `any Msg`,
   `perform(m)` on fresh instances and, separately, `perform(m);
   perform(m)` on fresh instances end with equal results and leave every
   fake with the same entries. In both runs each `Repository` and `Cache`
   impl the worker calls is replaced by a fresh stdlib fake
   (`std.testing.MemRepo`, `MemCache` with the default `5m` ttl), created on
   first use. It runs as a property (05 rule 16), its seed in
   `<module dir>/<Impl>.idempotent.seed`; a failure names the first entry
   that differs.
9. **`Cron`.** `schedule` is a compile-time literal, `every <Duration>` or
   `cron "<five fields>"`, validated at compile time; `run` is not re-entered
   while a tick is still running. Under `jip test` the clock is fake (section
   12 rule 10): a scenario drives one tick with `tick(Name)` from
   `std.testing`, which runs `run` once and returns its result; advancing the
   fake clock never fires a tick on its own.
10. **`Repository`.** `K` and `T` must be schema-derivable (section 11).
    `jip test` derives a round-trip check `<Impl>.round_trip` for every
    non-generic impl whose `K` and `T` have a structural generator: on a
    fresh instance, `put(k, v)` is `Ok` and then `get(k)` is `Ok(Some(v))`,
    for `any K`, `any T`. It runs as a property (05 rule 16), its seed in
    `<module dir>/<Impl>.round_trip.seed`. `std.testing.MemRepo` satisfies
    it by construction.
11. **`Cache`.** `get` may return `None` after `set`; nothing is derived.
    `set(k, v, ttl)` with `None` uses the `ttl` of the `run` line (rule 13,
    default `5m`); `Some(d)` overrides it per entry. Under the fake clock of
    `jip test` an entry expires when the clock is advanced past its `ttl`,
    so `std.testing.MemCache` and a real impl agree on expiry.
12. **Reference graph.** Impl A references impl B when A's methods, or
    functions they call transitively, name B (rule 6) or a topic B consumes.
    B starts before A and stops after it. A cycle is a compile error. The
    supervisor is not user-facing.
13. **Child specs.** An impl of a lifecycle behaviour is started by one
    `run Name { … }` line in `app.jip`, fields `restart: permanent |
    transient | temporary` (default `permanent`), `max_restarts: Int` (3),
    `period: Duration` (60s), `shutdown_timeout: Duration` (10s), `topic:
    <name>` for a `Worker`, `port: Int` for a `Service` (8080) and `ttl:
    Duration` for a `Cache` (5m). An impl without a `run` line is never
    started and naming it (rule 6) is a compile error; a second `run` line
    for one impl is a compile error: one line, one instance.
14. **`Topic<T>`.** Declared in the `topics { name: Topic<T> }` block of
    `app.jip` (section 10), `T` schema-derivable. In code `topics.<name>` has
    `publish(self, m: T) -> Result<(), TopicError> with {io}`, delivered at
    least once to every `Worker` whose `run` line names it; a `Msg` other
    than `T` is a compile error.
15. **Lowering.** A behaviour lowers to a Go struct of function values (a
    dictionary), one field per method, plus one field per associated type
    witness (its JSON codec and schema). An impl lowers to a struct with
    methods and one dictionary value built once in `init`; the runtime loop,
    retry, journal and probes take the dictionary, never a Go interface or a
    generic method (revision-plan R10). Go is pinned at 1.24 (section 13),
    and nothing in the emitted code depends on a later release.

## Diagnostics

An incomplete impl (rule 2):

```text
error[E0701]: `impl Worker for Indexer` is missing `perform`
  --> orders/index.jip:14:1
   |
14 | impl Worker for Indexer {
   | ^^^^^^^^^^^^^^^^^^^^^^^ required by `Worker`
   = note: `Worker` requires `fn perform(self, m: Msg) -> Result<(), E>`
   = help: add `fn perform(self, m: OrderEvent) -> Result<(), IndexError> with {io, net, mut, llm, decide, nondet} { … }`
```

An effect outside the budget (rule 3):

```text
error[E0702]: effect `spawn` exceeds the budget of `Worker.perform`
  --> orders/index.jip:19:60
   |
19 |   fn perform(self, m: OrderEvent) -> Result<(), IndexError> with {net, spawn} {
   |                                                                       ^^^^^ not in {io, net, mut, llm, decide, nondet}
   = help: remove `spawn`, or implement `Service`, whose `handle` budget includes it
```

A deferred behaviour:

```text
error[E0706]: `Machine` is not available in v0
  --> orders/lock.jip:8:1
   |
 8 | impl Machine for Lock {
   | ^^^^^^^^^^^^^^^^^^^^^ `Machine` and `Agent` are deferred (revision-plan R1)
   = help: model the states as a `Workflow` with signals, or a `Worker` over a `Repository`
```

## Grammar

```
BehaviourDecl ::= "pub"? "behaviour" Ident TypeParams? "{" BehaviourItem* "}"
BehaviourItem ::= AssocTypeSig | ConstSig | FnSig | FnDecl | InvLine
AssocTypeSig  ::= "type" Ident
ConstSig      ::= "const" Ident ":" Type
FnSig         ::= "fn" Ident "(" Params? ")" ("->" Type)? ("with" EffectSet)? ContractLine*
ImplDecl      ::= DocLine? "impl" (Ident TypeArgs? "for")? Type "{" ImplItem* "}"
                  /* no "for": inherent impl, section 03 rule 4 */
ImplItem      ::= AssocType | ConstDecl | FnDecl | InvLine
AssocType     ::= "type" Ident "=" Type
ConstDecl     ::= "const" Ident "=" ConstExpr
ConstExpr     ::= Expr | ScheduleLit | RoleLit
ScheduleLit   ::= "every" DurationLit | "cron" StrLit
RoleLit       ::= Ident /* a role from app.jip models {} */
RunLine       ::= "run" Ident ("{" RunField ("," RunField)* "}")?
RunField      ::= "restart" ":" ("permanent" | "transient" | "temporary")
                  | "max_restarts" ":" IntLit
                  | "period" ":" DurationLit
                  | "shutdown_timeout" ":" DurationLit
                  | "topic" ":" Ident
                  | "port" ":" IntLit
                  | "ttl" ":" DurationLit
```

The `topics { … }` block is defined in section 10.

`FnDecl`, `EffectSet`, `Type`, `Expr`, `ContractLine` and `InvLine` are defined in
sections 03, 02, 04 and 05.
