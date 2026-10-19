# 12 Testing

jippitese has one kind of test: the BDD scenario. There are no unit-test
files. Scenarios live beside the module they exercise, draw generated values
from the contract generators of 05, and run with contracts fully on, so a
`then` failure and a `post` failure look the same. The clock and the random
generator are fakes by default (R14).

## Example

`orders/billing/scenarios.jip`:

```jip
module orders.billing.scenarios
use orders.billing
use orders.customers

scenario "withdraw never overdraws" {
  given acc = any Account                        // contract generator: respects `inv`
  given amt = any Money via positive_money       // named generator (rule 4)
  when  r = withdraw(acc, amt)
  then  match r { Ok(a) => a.balance.0 >= 0, Err(_) => true }
}

scenario "withdraw of the whole balance empties the account" {
  given acc = Account { id: AccountId(1), balance: Money(500) }
  when  r = withdraw(acc, Money(500))
  then  r.is_ok()
  then  match r { Ok(a) => a.balance.0 == 0, Err(_) => false }
}

scenario "withdraw table" {
  given rows = [(Money(500), Money(200), true), (Money(100), Money(200), false)]
  given (bal, amt, ok) in rows                   // the block runs once per row
  when  r = withdraw(Account { id: AccountId(1), balance: bal }, amt)
  then  r.is_ok() == ok
}

scenario "service charges through the configured gateway" {
  given http = FakeHttp.new().reply(200, b"{\"id\":\"r1\",\"amount\":500}")
  given cfg = Config { gateway_url: Url("https://pay.test/charge"), secrets: Secrets.fake() }
  given svc = BillingApi.init(cfg)?
  given req = BillReq { order: Order { id: OrderId(1), lines: [], total: Money(500) } }
  when  r = svc.handle(req)
  then  r.is_ok()
  then  http.requests().len() == 1
}

scenario "high-risk order waits for finance" {
  given http = FakeHttp.new().reply(200, b"{\"id\":\"r1\",\"amount\":9000}")
  given fast = FakeDecide.always(Tier.High)
  given finance = FakeApprove.script([Ok(Decision.Proceed)])
  given order = Order { id: OrderId(7), lines: [], total: Money(9_000) }
  when  r = run_workflow(Billing, order)
  then  r.is_ok()
  then  finance.asked().len() == 1
}

@extern
scenario "read_file returns the bytes written" {
  given p = tmp_path("hello.txt")
  when  r = { write_file(p, b"hi")?; read_file(p) }
  then  r == Ok(b"hi")
}

@slow
@skip("gateway sandbox is down until the Q4 cutover")
scenario "sandbox round-trip" {
  given real_time                                // opt out of the fake clock (rule 10)
  given cfg = Config { gateway_url: Url("https://sandbox.pay.test"), secrets: Secrets.fake() }
  when  r = charge(Order { id: OrderId(2), lines: [], total: Money(1) }, Gateway { url: cfg.gateway_url })
  then  r.is_ok()
}
```

Go equivalent. One scenario with `any` replaces a `testing/quick` test;
`quick` never shrinks and persists nothing, so the shrinking, seed replay and
contract oracle that come free in a scenario are hand-written here:

```go
func TestWithdrawNeverOverdraws(t *testing.T) {
	f := func(balance uint32, amt uint32) bool {
		if amt == 0 { return true }                 // the generator's bound, by hand
		acc := Account{Balance: Money(int64(balance))}
		a, err := Withdraw(acc, Money(int64(amt)))
		return err != nil || a.Balance >= 0       // the `then` line
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Error(err)                               // no shrunk example, no seed
	}
}
```

## Rules

1. **Files.** Scenarios for module `a.b` live in `a/b/scenarios.jip`, whose
   module line is `module a.b.scenarios`. The file may `use` the module under
   test and that module's dependencies. It declares no `pub` items and no
   functions; `std.testing` is in scope without a `use` line, so `FakeHttp`,
   `run_workflow`, `tmp_path` and friends are bare.
2. **Block.** `scenario "name" { … }` with a non-empty string name, unique
   within the file. Steps are one per line in the order `given* when then+`:
   any number of `given`, exactly one `when`, at least one `then`. A `given`
   after `when`, a second `when` or a missing `then` is a compile error
   (Diagnostics). A multi-step action is written as one `when` whose
   expression is a block.
3. **`given name = expr`.** Binds a value; later steps see `name`. `expr`
   may have any effect (constructing fakes, reading fixtures). `?` on a
   `given` or `when` fails the scenario with the error value; it does not
   propagate anywhere else, because the scenario block is the boundary.
4. **`given name = any T`.** Draws `name` from the contract generator for
   `T` (05 rule 15): the structural generator under the fixed size model,
   with every `inv` of `T` checked on each candidate. `given name = any T
   via g` draws through the named generator instead: `g` is a `pub fn
   g(g: Gen) -> T` of the module under test that takes every random choice
   from `Gen` (`std.gen`), so shrinking and seed replay apply unchanged;
   `g` is the way to satisfy a `pre` the structural generator would miss.
   `T` is any type a generator exists for; a type that contains a function,
   `Cell` or behaviour value is a compile error. The table form is two
   lines: `given rows = [(a, b, c), …]` binds a list of tuples, and `given
   (x, y, z) in rows` runs the rest of the block once per row with the tuple
   destructured; the pattern must match the tuple arity (E0803), and a
   table scenario is an example, not a property (rule 7).
   *Deferred from v0 (R9):* `any T where expr`, the inline filter, is v1;
   write a `via` generator instead.
5. **`when name = expr`.** The action under test, exactly one per scenario.
   `expr` may have any effect set; no `with` is written because the scenario
   block is the boundary that declares nothing. `when _ = expr` discards the
   value.
6. **`then expr`.** `expr` has type `Bool` and sees every `given` and `when`
   binding. Each `then` line is evaluated and reported separately; the first
   failing line does not stop the others.
7. **Example or property.** A scenario with no `any` step is an example and
   runs once. A scenario with at least one `any` step is a property: N = 200
   runs (`--runs N` to change), integrated shrinking of every `any` binding
   together, and the shrunk seed persisted at
   `.jip/seeds/<module dir>/scenarios/<name slug>.seed` and replayed as
   run 1 later, all as in 05 rule 16.
8. **Contracts on.** Scenarios run under `contracts: all` whatever `app.jip`
   says. A `pre`, `post` or `inv` that fires inside a `when` fails the
   scenario and is reported with the same shrunk example as a failing `then`
   (Diagnostics).
9. **Config.** Effects inside a scenario see the `env test` overlay of
   `app.jip` (cross-reference 10); `JIP_ENV` is ignored under `jip test`.
10. **Fakes.** `std.testing` provides `FakeHttp` (`net`), `FakeClock` and
    `FakeRandom` (`nondet`), `FakeLlm` (scripted replies per role),
    `FakeDecide` (scripted or constant decisions) and `FakeApprove`
    (scripted `Ok(value)` or `Err(Rejected)` or `Timeout` answers), and the
    in-memory `MemRepo` and `MemCache` that stand in for `Repository` and
    `Cache` impls (section 06). Binding a fake in a `given` installs it for
    the rest of that scenario; the effect it fakes is routed to it. `net`,
    `llm`, `decide` and `approve` must be faked: reaching one with no fake
    installed fails the scenario with `effect net reached the boundary with
    no fake installed`. `nondet` is faked by default (R14): every scenario
    starts with a `FakeClock` at a fixed epoch and a `FakeRandom` seeded
    from the run seed, so `time.now()`, `sleep`, `random()` and
    `await_signal` timeouts are deterministic without a `given`; binding
    `FakeClock.new()` or `FakeRandom.seeded(n)` explicitly takes control of
    the installed fake. The line `given real_time` opts a scenario out and
    routes `nondet` to the real clock and generator. `io`, `mut` and
    `spawn` always run for real. A user-written fake (`FakeGateway` in the
    card) is an ordinary type and needs no installation.
11. **Behaviours.** A `Service`, `Worker`, `Cron`, `Repository` or `Cache`
    impl is exercised by calling its lifecycle methods directly, as
    `BillingApi.init(cfg)?` above; the behaviour's own contracts and `inv`
    are on. A `Cron` tick is driven with `tick(Name)`, which runs `run` once
    and returns its result; advancing the fake clock never fires a tick on
    its own (section 06 rule 9). A `Workflow` is run with
    `run_workflow(Impl, input)` on the embedded runtime with a fresh journal
    and the installed `FakeClock`, so `sleep` and `await_signal` timeouts
    advance the fake clock instead of waiting; `approve` answers come from the bound
    `FakeApprove` script in order, and an exhausted script is a failure.
    `send_signal(run, signal)` delivers a `Signal` to a running workflow.
12. **Tags.** A tag line immediately precedes `scenario`. `@extern` marks a
    scenario that exercises an `extern`; every `extern` needs at least one
    and `jip check` reports the ones without (cross-reference 11). `@slow`
    scenarios run only under `jip test --slow` and are otherwise printed as
    `slow` and counted as skipped. `@skip("reason")` skips the scenario; the
    reason is printed. No skip is ever silent.
13. **`jip test`.** Runs every scenario, prints one line per scenario, then
    a summary, and exits non-zero if any scenario or `then` line failed.
    `jip test <module>` runs that module's scenarios; `jip test --filter
    "substring"` keeps scenarios whose name contains the text; `--runs N`
    sets the property run count; `--seed N` replays a persisted or printed
    seed for every property instead of drawing a new one. Lines are `ok`,
    `FAIL`, `skip` or `slow`, then the module, the scenario name and, for
    properties, the run count. The derived checks of 05 rules 14 and 17 are
    v1 and not run.
14. **No other test forms.** There is no assertion library, no test
    function, no fixture file format. Shared setup is a pure `fn` in the
    module under test or a `given` line.

## Diagnostics

A `then` line that is not `Bool`:

```text
error[E0801]: `then` expects `Bool`
  --> orders/billing/scenarios.jip:15:9
   |
15 |   then  r
   |         ^ found `Result<Account, BillError>`
   = help: replace `r` with `r.is_ok()`
```

A `given` after `when`:

```text
error[E0802]: step out of order
  --> orders/billing/scenarios.jip:23:3
   |
23 |   given amt = Money(5)
   |   ^^^^^ a `given` cannot follow `when`
   = note: the order inside a scenario is given* when then+
   = help: move this line above `when  r = withdraw(acc, amt)` (line 22)
```

A failing property scenario (here `withdraw` was changed to subtract one
unit too many). The same block is printed when a `post` fires instead of a
`then`; the `contract` line then names the clause:

```text
FAIL  orders.billing.scenarios  "withdraw never overdraws"  (failed on run 41, shrunk in 14 steps)
  orders/billing/scenarios.jip:9:3: then failed
  then  match r { Ok(a) => a.balance.0 >= 0, Err(_) => true }
  given acc = Account { id: AccountId(0), balance: Money(0) }
  given amt = Money(1)
  when  r = Ok(Account { id: AccountId(0), balance: Money(-2) })
  contract: orders/billing.jip:36:3 post failed  (`ret` shown as `r`)
  seed 0x5e12b0c4 saved to .jip/seeds/orders/billing/scenarios/withdraw_never_overdraws.seed
  replay: jip test orders.billing --seed 0x5e12b0c4
```

## Grammar

```
ScenarioFile ::= ModuleLine UseLine* (Tag* ScenarioDecl)*
ScenarioDecl ::= "scenario" StringLit "{" GivenStep* WhenStep ThenStep+ "}"
Step         ::= GivenStep | WhenStep | ThenStep
GivenStep    ::= "given" Ident "=" (Expr | AnyExpr) NEWLINE
               | "given" "(" Ident ("," Ident)+ ")" "in" Ident NEWLINE   /* table row */
               | "given" "real_time" NEWLINE
AnyExpr      ::= "any" Type ("via" Ident)?
WhenStep     ::= "when" (Ident | "_") "=" Expr NEWLINE
ThenStep     ::= "then" Expr NEWLINE
Tag          ::= "@extern" NEWLINE | "@slow" NEWLINE | "@skip" "(" StringLit ")" NEWLINE
```

`AnyExpr` is valid only on the right of a `given`; the `via` name resolves
in the module under test. A `ScenarioFile` contains
no `FnDecl`, `TypeDecl` or `ImplDecl`.
