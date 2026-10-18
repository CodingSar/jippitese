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
    search.upsert(self.search, m.order_id, o).map_err((e) => Backend("{e}"))
  }
}

/// Orders by id, in SQLite.
impl Repository<Order, OrderId> for Orders {
  fn init(cfg: Config) -> Result<Self, RepoError> with {io} { sqlite.open(cfg.db).map((c) => Orders { conn: c }) }
  fn get(self, k: OrderId) -> Result<Option<Order>, RepoError> with {io} { sqlite.get<Order>(self.conn, k.0) }
  fn put(self, k: OrderId, v: Order) -> Result<(), RepoError> with {io} { sqlite.put(self.conn, k.0, v) }
  fn delete(self, k: OrderId) -> Result<(), RepoError> with {io} { sqlite.delete(self.conn, k.0) }
  fn query(self, q: Query<Order>) -> Result<List<Order>, RepoError> with {io} { sqlite.query(self.conn, q) }
}
```


`app.jip` declares the topic (section 10) and starts both instances:

```jip
app {
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

## The eight v0 behaviours

All are declared in `std.behaviour`. `Config` is the typed `app {}` record
(section 10). `Health { live: Bool, ready: Bool, detail: Option<Str> }`.

```jip
pub behaviour Service {
  type Req
  type Resp
  type E
  fn init(cfg: Config) -> Result<Self, E> with {io, net}
  fn handle(self, req: Req) -> Result<Resp, E> with {io, net, mut, nondet, llm, decide, spawn, approve}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.aggregate(self) }
}

pub behaviour Worker {
  type Msg
  type E
  const retry: Retry                       // Retry { max: Int, backoff: Backoff }; Backoff = Fixed(Duration) | Exp(Duration)
  fn init(cfg: Config) -> Result<Self, E> with {io, net}
  fn perform(self, m: Msg) -> Result<(), E> with {io, net, mut, nondet, llm, decide, approve}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.aggregate(self) }
}

pub behaviour Cron {
  type E
  const schedule: Schedule                 // `every 5m` or `cron "0 * * * *"`
  fn init(cfg: Config) -> Result<Self, E> with {io, net}
  fn run(self) -> Result<(), E> with {io, net, mut, nondet, llm, decide, approve}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.aggregate(self) }
}

pub behaviour Machine {
  type Key
  type State                               // closed sum
  type Event                               // closed sum
  type Action                              // closed sum
  type E
  fn init(cfg: Config) -> Result<Self, E> with {io, net}
  fn activate(self, key: Key) -> State
  fn on(self, s: State, e: Event) -> (State, List<Action>)
  fn act(self, key: Key, a: Action) -> Result<List<Event>, E> with {io, net, nondet, llm, decide}
  fn deactivate(self, key: Key, s: State) with {io, net} {}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.aggregate(self) }
}

pub behaviour Workflow {                   // semantics in section 07
  type Input
  type Output
  type Signal                              // closed sum
  const version: Int
  fn init(cfg: Config) -> Result<Self, WorkflowError> with {io, net}
  fn run(self, in: Input) -> Result<Output, WorkflowError> with {io, net, nondet, llm, decide, approve, spawn}
  fn on_signal(self, s: Signal) {}
}

pub behaviour Agent {                      // semantics in section 07
  type State
  type Answer
  const model: Role
  const tools: List<Tool>
  const budget: Budget                     // Budget { max_turns: Int, max_cost: Money }
  fn init(cfg: Config) -> Result<Self, AgentError> with {io, net}
  fn begin(self, task: Str) -> State
  fn observe(self, st: State, ev: Event) -> State
  fn stop(self, st: State) -> Option<Answer>
  fn gate(self, call: ToolCall) -> Gate with {decide, approve}
}

pub behaviour Repository<T, K> {
  fn init(cfg: Config) -> Result<Self, RepoError> with {io, net}
  fn get(self, k: K) -> Result<Option<T>, RepoError> with {io, net}
  fn put(self, k: K, v: T) -> Result<(), RepoError> with {io, net}
  fn delete(self, k: K) -> Result<(), RepoError> with {io, net}
  fn query(self, q: Query<T>) -> Result<List<T>, RepoError> with {io, net}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.up() }
}

pub behaviour Cache<K, V> {
  fn init(cfg: Config) -> Result<Self, CacheError> with {io, net}
  fn get(self, k: K) -> Result<Option<V>, CacheError> with {io, net, nondet}
  fn set(self, k: K, v: V, ttl: Duration) -> Result<(), CacheError> with {io, net, nondet}
  fn invalidate(self, k: K) -> Result<(), CacheError> with {io, net, nondet}
  fn stop(self) with {io, net} {}
  fn health(self) -> Health with {io, net} { Health.up() }
}
```

`RepoError = NotFound | Conflict | Unavailable(Str) | Invalid(Str)`;
`CacheError = Unavailable(Str)`. Budgets, summarised:

| Behaviour | Method | Budget |
|---|---|---|
| `Service` | `handle` | `io net mut nondet llm decide spawn approve` |
| `Worker` | `perform` | `io net mut nondet llm decide approve` |
| `Cron` | `run` | as `Worker` |
| `Machine` | `on` pure; `act` | `io net nondet llm decide` |
| `Workflow` | `run` | `io net nondet llm decide approve spawn`, every call journaled |
| `Agent` | `gate` | `decide approve`; `begin`, `observe`, `stop` pure; tools add their own |
| `Repository` | all | `io net` |
| `Cache` | all | `io net nondet` |
| all | `init`, `stop`, `health` | `io net` |

`nondet` is added to the `Service`, `Worker` and `Cron` budgets relative to
the research table because `time.now()` carries it.

## Rules

1. **Declaring.** A `behaviour` body holds `type A`, `const c: T`, `fn`
   signatures, `fn` with a body (default method) and `inv` lines. The effect
   set on a method is its budget. User code may declare only *constraint*
   behaviours: methods only, no `type`, `const`, `inv`, defaults or effects
   (`behaviour Show { fn show(self) -> Str }`), for generic bounds. Lifecycle
   behaviours come from the stdlib.
2. **Implementing.** `impl Name for Type { … }` supplies every associated
   type, every const and every method without a default; a default may be
   overridden. Nothing else may appear.
3. **Effect subset.** Each impl method's effect set must be a subset of the
   budget. A budget label `llm` or `decide` without a role admits any role.
4. **Contract refinement.** An impl method may not add `pre`; it may add
   `post`, checked in addition to the behaviour's. A behaviour `inv` applies
   to `Self` and is checked when `init` returns.
5. **Lifecycle.** `init(cfg: Config) -> Result<Self, E> with {io, net}` is
   static (no `self`). `stop` and `health` have defaults. `Health.aggregate`
   reports `live = true` once `init` returned and `ready` as the conjunction
   of `health().ready` over every `Repository` and `Cache` the type
   references (rule 13); `Health.up()` reports both true.
6. **Instances by name.** A started impl is addressed by its type name:
   `Orders.get(k)`, `Lock.send(key, e)`, `Indexer.enqueue(m)`; the call has
   the method's effects and resolves to the one instance from the `run`
   line. There is no handle value.
7. **`Service`.** `E` is a closed enum. Transport is configured on the `run`
   line (section 10); the v0 default serves `handle` at `POST /<name>` with
   JSON `Req` and `Resp`.
8. **`Worker`.** Messages come from the topic named on its `run` line or,
   without one, from a runtime queue fed by `Name.enqueue(m) -> Result<(),
   QueueError> with {io}`. `Err` from `perform` is retried per `retry` (a
   compile-time literal); after `max` failures the message is dead-lettered.
   `jip test` derives the idempotency check `perform(m); perform(m) ≡
   perform(m)` on `any Msg`: each `Repository` and `Cache` the worker
   references is replaced by the stdlib fake (`std.testing.MemRepo`,
   `MemCache`) and their contents compared after one and after two calls. A
   worker referencing something with no fake gets a warning, not a check.
9. **`Cron`.** `schedule` is a compile-time literal, `every <Duration>` or
   `cron "<five fields>"`, validated at compile time; `run` is not re-entered
   while a tick is still running.
10. **`Machine`.** `on` is pure and must `match` exhaustively over `(State,
    Event)`. One turn per key at a time: `activate` on the first event, `on`,
    persist the state, then `act` for each returned `Action`, whose events
    queue as later turns of the same key. Events enter by `Name.send(key, e)
    -> Result<(), MachineError> with {io}`. `deactivate` runs when a key
    idles past the runtime's timeout; the state is reloaded on the next event.
11. **`Repository`.** `K` and `T` must be schema-derivable (section 07). `jip
    test` derives the round-trip check: for `any K` and `any T`, `put(k, v)`
    then `get(k)` is `Ok(Some(v))`, against `init` under `env test`.
12. **`Cache`.** `get` may return `None` after `set`; nothing is derived.
13. **Reference graph.** Impl A references impl B when A's methods, or
    functions they call transitively, name B (rule 6) or a topic B consumes.
    B starts before A and stops after it. A cycle is a compile error. The
    supervisor is not user-facing.
14. **Child specs.** An impl of a lifecycle behaviour is started by one
    `run Name { … }` line in `app.jip`, fields `restart: permanent |
    transient | temporary` (default `permanent`), `max_restarts: Int` (3),
    `period: Duration` (60s), `shutdown_timeout: Duration` (10s), and `topic:
    <name>` for a `Worker`. An impl without a `run` line is never started
    and naming it (rule 6) is a compile error; a second `run` line for one
    impl is a compile error: one line, one instance.
15. **`Topic<T>`.** Declared in the `topics { name: Topic<T> }` block of
    `app.jip` (section 10), `T` schema-derivable. In code `app.name:
    Topic<T>` has `publish(self, m: T) -> Result<(), TopicError> with {io}`,
    delivered at least once to every `Worker` whose `run` line names it; a
    `Msg` other than `T` is a compile error.
16. **Lowering.** A behaviour is a Go interface, an impl a struct with
    methods; the runtime supplies loop, retry, journal and probes.

## Diagnostics

An incomplete impl (rule 2):

```text
error[E0701]: `impl Worker for Indexer` is missing `perform`
  --> orders/index.jip:14:1
   |
14 | impl Worker for Indexer {
   | ^^^^^^^^^^^^^^^^^^^^^^^ required by `Worker`
   = note: `Worker` requires `fn perform(self, m: Msg) -> Result<(), E>`
   = help: add `fn perform(self, m: OrderEvent) -> Result<(), IndexError> with {net} { … }`
```

An effect outside the budget (rule 3):

```text
error[E0702]: effect `spawn` exceeds the budget of `Worker.perform`
  --> orders/index.jip:19:60
   |
19 |   fn perform(self, m: OrderEvent) -> Result<(), IndexError> with {net, spawn} {
   |                                                                       ^^^^^ not in {io, net, mut, nondet, llm, decide, approve}
   = help: remove `spawn`, or implement `Service`, whose `handle` budget includes it
```

## Grammar

```
BehaviourDecl ::= "pub"? "behaviour" Ident TypeParams? "{" BehaviourItem* "}"
BehaviourItem ::= AssocTypeSig | ConstSig | FnSig | FnDecl | InvLine
AssocTypeSig  ::= "type" Ident
ConstSig      ::= "const" Ident ":" Type
FnSig         ::= "fn" Ident "(" Params? ")" ("->" Type)? ("with" EffectSet)? ContractLine*
ImplDecl      ::= DocLine? "impl" Ident TypeArgs? "for" Type "{" ImplItem* "}"
ImplItem      ::= AssocType | ConstDecl | FnDecl
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
```

The `topics { … }` block is defined in section 10.

`FnDecl`, `EffectSet`, `Type`, `Expr` and `ContractLine` are defined in
sections 03, 02, 04 and 05.
