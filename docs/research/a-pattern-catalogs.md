# (a) Pattern catalogs

Status: research output for track (a). Feeds `decisions.md` §5 (behaviour list,
lifecycle shape) and §10 (`app.jip`).

## Summary

Five mature systems converge on a short list of named patterns: request
handler, queue/event consumer, scheduled job, keyed state machine/actor,
durable workflow, data-access object, plus two non-code things: a message
topic and an application/supervision root. They express patterns four ways
(callback module, base class, annotation, typed resource object); only OTP and
Encore check an implementation at compile time, and only Encore checks
compositions. Universal lifecycle is `init → handle* → stop`; `health` is
universal in cloud frameworks but absent in OTP. Recommendation: eight v0
behaviours with explicit effect budgets, `Pipeline` dropped, `Cron` and
`Machine` added, restart/ordering moved into `app.jip` as child specs.

## Findings (per system)

**Erlang/OTP.** A behaviour is a generic engine plus a callback module;
`-callback` attributes generate `behaviour_info(callbacks)` and the compiler
warns when a module omits a required callback (Elixir's `@impl` also catches
wrong arity). `gen_server`: `init/1`, `handle_call/3`, `handle_cast/2`,
`handle_info/2`, `handle_continue/2` (OTP 21), `terminate/2`, `code_change/3`;
a callback that fails or returns a bad value terminates the server.
`gen_statem`: `callback_mode/0` (`state_functions` needs atom states;
`handle_event_function` allows any term), optional `state_enter`, events
`call|cast|info|timeout|state_timeout|internal`, actions `postpone`,
`next_event`, `reply`. `gen_event`: hot-pluggable handlers; a failing handler
is removed, the manager survives. `supervisor` `init/1` returns flags
(`strategy` default `one_for_one`, `intensity` 1, `period` 5 s) and child
specs (`id`, `start`, `restart` `permanent|transient|temporary`, `shutdown`,
`type`). `application`: `start/2` builds the tree, `prep_stop/1`, `stop/1`,
shutdown in reverse start order. Composition is data (child specs) checked at
runtime only.

**Encore.** Patterns are typed resource objects in ordinary code:
`api()`-wrapped functions, `new Topic`/`Subscription`, `new CronJob`,
`SQLDatabase`, buckets, caches, secrets, auth handlers, middleware. The
compiler statically derives an "Application Model" (services, endpoints,
schemas, topics, subscriptions, cron jobs, databases, caches, secrets,
gateways), "reports invalid declarations and resource usage before
deployment", and derives IAM permissions from it. Constraints are structural:
a cron endpoint must be `func(ctx) error` or `func(ctx) (T, error)`,
`JobConfig` fields must be constant literals, handlers must be idempotent
("may be invoked more than once"). Subscriptions carry `RetryPolicy`
(`MaxRetries` default 100, then dead-letter; `nil` acks, error nacks); topics
declare `AtLeastOnce`. Closest analogue to "behaviour = interface + config
checked by the compiler".

**Dapr.** Building blocks are independent sidecar HTTP/gRPC APIs: service
invocation, state, pub/sub, bindings, actors, workflow, secrets,
configuration. Actors: virtual (activated on first call, deactivated after
`actorIdleTimeout`), turn-based via a per-actor lock ("no other actor methods
or timer/reminder callbacks will be in progress until this callback
completes"), state saved after each turn (save failure deactivates), timers
lost on deactivation, reminders persisted. Workflow: orchestrator must be
deterministic and "side-effect free other than engine-mediated" ones; I/O in
activities (may run twice, so idempotent), waiting via durable timers, humans
via external events, current-time returns first-run time on replay. Changing
workflow code or retry policy after instances exist breaks replay.

**Rails.** Base classes plus naming conventions: `ActiveRecord::Base`
(ordered chain `before_validation → before_save → around_save → before_create
→ after_save → after_commit`, halted by `throw :abort`),
`ActionController::Base`, `ApplicationJob` (`perform`, `queue_as`,
`before/around/after_{enqueue,perform}`, `retry_on`, `discard_on` searched
"bottom to top, and up the class hierarchy"), `ApplicationCable::Connection`
(`connect`, `identified_by`) and `Channel` (`subscribed`, `unsubscribed`,
`stream_from`), Action Mailer, Active Storage. Nothing is checked statically.
Rails 7.1 `/up` returns 200 iff the app booted and deliberately does not check
dependencies.

**Spring Boot.** Annotations. `@Component` is the base; `@Service` adds no
behaviour ("mainly for clarity of intent"); `@Repository` adds AOP exception
translation into `DataAccessException`; `@Controller` adds routing.
`@Scheduled` (needs `@EnableScheduling`; no args, return ignored;
`fixedRate|fixedDelay|cron`; one shared thread by default), `@EventListener`,
`@Async`. Lifecycle: `@PostConstruct → afterPropertiesSet → init-method`,
mirrored on destroy; `SmartLifecycle` (`start`, `stop`, `isRunning`,
`getPhase`, lowest phase first, shutdown reversed); events `Starting →
Started → Ready → Failed`. Health: `HealthIndicator` beans in groups;
`liveness` has no dependency checks, `readiness` includes auto-registered
`db`, `redis`. Checking is runtime reflection; a typo'd annotation is silent.

### Answers

1. **Minimal named set** (named in ≥3 systems): request/reply service;
   queue/event consumer; scheduled job; keyed state machine/actor; durable
   workflow (Dapr only, but the others lack any answer); data access;
   topic/pub-sub; app root/supervisor. "Pipeline" is named nowhere.
2. **Expression**: callback module + attribute (OTP), typed resource object
   (Encore), sidecar API (Dapr), base class + convention (Rails), annotation +
   reflection (Spring). Compile-time checks: OTP (callbacks), Encore
   (callbacks and composition graph). Others: runtime or never.
3. **Universal hooks**: `init` and `handle` (all five); `stop` (OTP, Spring,
   Cable, Dapr actors; not Rails jobs); `health` (Spring, Rails, Dapr; not
   OTP). `code_change` is OTP-only, irrelevant for static Go binaries. Worth
   copying: retry-exhausted → dead-letter (Encore, ActiveJob) and actor
   `deactivate` (Dapr).
4. **Composition**: OTP via child specs with restart strategy and ordering,
   runtime-checked; Spring via phase integers and DI graph, runtime; Encore
   via static reference graph, build-rejected; Dapr workflow→activities,
   checked only by replay failure.
5. See Recommendations.

## Trade-offs

- **Marker vs. contract.** Spring's `@Service` is documentation only;
  `@Repository` earns its name by adding exception translation. A jippitese
  behaviour must add a checked obligation (effect budget, contract,
  lifecycle) or be cut.
- **Generic vs. typed messages.** OTP's untyped `handle_info` is flexible but
  unreviewable; Encore/Dapr typed payloads cost a type per message but let the
  compiler derive schemas. Choose typed.
- **Supervision vs. probes.** OTP restarts locally and needs no health
  endpoint; cloud frameworks externalise liveness/readiness. Go binaries under
  Kubernetes need both.
- **Workflow determinism.** Dapr's silent replay breakage is exactly what
  effect types can prevent at compile time.
- **Idempotency** is required by Encore and Dapr in prose; a contract makes it
  mechanical.

## Recommendations for jippitese

**v0 behaviour list.** Effect budget = maximum effects an `impl` method may
declare; the runtime supplies the rest.

| Behaviour | Lifecycle methods | Associated types | Effect budget | Contract / check |
|---|---|---|---|---|
| `Service` | `init(cfg)`, `handle(Req) → Result<Resp,E>`, `stop`, `health` (default) | `Req`, `Resp`, `E` | `io net mut llm decide spawn approve` | `E` closed ADT |
| `Worker` | `init`, `perform(Msg) → Result<(),E>`, `stop` | `Msg`, `E`, `Retry` (const) | `io net mut llm decide approve` | **idempotent**: `perform(m); perform(m) ≡ perform(m)` (property); retry/dead-letter from `Retry` |
| `Cron` | `init`, `run() → Result<(),E>` | `E`, `Schedule` (const literal) | as `Worker` | schedule literal (Encore rule); idempotent |
| `Machine` | `init(Key) → State`, `on(State, Event) → (State, [Action])`, `deactivate` | `Key`, `State` (closed sum), `Event` (closed sum) | `mut` on own state; `io net llm decide` only via `Action` | exhaustive `match` over `State×Event`; one turn per key; state persisted per turn |
| `Workflow` | `run(Input) → Result<Output,E>` | `Input`, `Output`, `E`, `Activities` | **none directly**; a `workflow` meta-effect wrapping activity calls, durable timers, `approve` | compiler rejects `io net nondet llm` in body; code hash recorded for replay |
| `Agent` | `init`, `step(Ctx) → Decision`, `stop_when(Ctx)`, `budget` | `Ctx`, `Tool` set, `Model` role | `llm decide approve` + union of tool effects | budget contract (steps, tokens, cost) |
| `Repository<T,K>` | `get`, `put`, `delete`, `query`, `health` | `T`, `K`, `E` | `io` or `net` | `E` mapped into `RepoError` (Spring translation); `get` after `put` round-trips (property) |
| `Cache<K,V>` | `get`, `set(ttl)`, `invalidate`, `health` | `K`, `V` | `io net nondet` | may miss after `set` |

Dropped: `Pipeline` (chain `Worker`s over `Topic<T>`). Added: `Cron`,
`Machine`. `Topic<T>` and `Secret` are stdlib types, not behaviours.
Supervisor is not a user behaviour: each `impl` instance gets an OTP-style
child spec in `app.jip` (`restart: permanent|transient|temporary`,
`max_restarts`, `period`, `shutdown_timeout`); start order is derived from the
reference graph (Encore), not phase integers (Spring). `health()` defaults:
liveness = booted (Rails `/up`), readiness = aggregate of `Repository`/`Cache`
impls (Spring groups).

**Decisions reopened:**
- §5: replace `Pipeline` with `Cron` and `Machine`; fix lifecycle to
  `init / handle / stop / health`, plus `deactivate` for `Machine`.
- §4: `Workflow` requires the compiler to *forbid* effects in its body, not
  merely track them; `time.now()` must carry `nondet`. `approve` inside a
  workflow is an external-event wait (track (e)).
- §7: long-lived `Service`/`Worker` instances are children of the app root
  scope with a restart policy; `spawn` inside stays structured. Restart
  policy must be stated.
- §10: add per-impl child-spec fields and the compile-time-literal rule for
  schedules and retry policies.
- §14 / track (f): idempotency and `Repository` round-trip contracts should
  be stdlib-provided and auto-generate property checks.

## Sources

- https://www.erlang.org/docs/17/design_principles/gen_server_concepts.html
- https://learnyousomeerlang.com/clients-and-servers
- https://www.erlang.org/doc/apps/stdlib/gen_statem.md
- https://erlang.org/docs/17/man/gen_event
- https://www.erlang.org/doc/apps/stdlib/supervisor.md
- https://www.erlang.org/docs/23/design_principles/sup_princ.html
- https://erlang.org/doc/man/application
- https://www.erlang.org/docs/17/design_principles/applications
- https://erlang.org/pipermail/erlang-patches/2010-June/001217.html
- https://hexdocs.pm/elvis_core/no_behavior_info.html
- https://appunite.com/blog/gen-server-handle-continue
- https://encore.dev/docs/ts/primitives.md
- https://encore.dev/docs/understanding-encore.md
- https://encore.dev/docs/ts/primitives/services-and-apis
- https://pkg.go.dev/encore.dev/cron
- https://encore.dev/docs/go/primitives/cron-jobs.md
- https://pkg.go.dev/encore.dev/pubsub
- https://docs.dapr.io/concepts/building-blocks-concept/
- https://docs.dapr.io/developing-applications/building-blocks/workflow/workflow-features-concepts/
- https://docs.dapr.io/contributing/protocol-reference/workflow-protocol/
- https://docs.dapr.io/developing-applications/building-blocks/actors/actors-timers-reminders/
- https://v1-14.docs.dapr.io/developing-applications/building-blocks/actors/actors-features-concepts
- https://oneuptime.com/blog/post/2026-03-31-dapr-actor-lifecycle-activation/markdown
- https://rubydoc.info/gems/rails
- https://guides.rubyonrails.org/v7.0/active_job_basics.html
- https://api.rubyonrails.org/v7.1.0/classes/ActiveJob/Exceptions/ClassMethods.html
- https://api.rubyonrails.org/classes/ActiveRecord/Callbacks.html
- https://guides.rubyonrails.org/v5.1/action_cable_overview.html
- https://rubydoc.info/docs/rails/Rails/HealthController
- https://howtodoinjava.com/spring-core/stereotype-annotations/
- https://docs.spring.io/spring-framework/docs/7.0.6/javadoc-api/org/springframework/dao/annotation/package-summary.html
- https://docs.spring.io/spring-framework/docs/5.0.4.RELEASE/javadoc-api/org/springframework/scheduling/annotation/Scheduled.html
- https://docs.spring.io/spring-framework/docs/5.3.4/javadoc-api/org/springframework/context/SmartLifecycle.html
- https://howtodoinjava.com/spring-core/spring-bean-life-cycle
- https://reflectoring.io/spring-boot-application-events-explained/
- https://spring.io/blog/2020/03/25/liveness-and-readiness-probes-with-spring-boot/
