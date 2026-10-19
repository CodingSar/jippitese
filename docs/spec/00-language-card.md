# jippitese language card

Load this before writing `.jip`. Everything below compiles under the v0 spec.
Rules: full signatures at boundaries, local inference in bodies; effects after
the return type with `with {…}`, omitted when pure; `/// doc` line on every
`pub` item; one `module` line first in every file; closures start with `fn`;
an explicit type argument in an expression is the turbofish `f::<T>(x)`.

```jip
module orders.billing            // first line of every file: the module path
use std.gen                      // stdlib module
use std.json
use std.time
use http                         // dependency alias from app.jip deps {}
use orders.customers             // sibling app module; refer to it as customers.*

/// Money in minor units.
pub type Money(Int)                              // newtype, distinct from Int

/// A customer order.
pub type Order { id: OrderId, lines: List<Line>, total: Money }
  inv total.0 >= 0                               // invariant, checked at construction

/// Why an order could not be billed.
pub enum BillError { NoFunds(Money), Net(http.Error), Declined(Str) }

/// Charge status.
pub enum Status { Pending, Paid, Failed }        // payload-less sum type

/// What finance answers on a high-risk order.
pub enum Decision { Proceed, Decline }           // closed sum: usable as an `approve` answer

/// Sum of line totals.
pub fn total(lines: List<Line>) -> Money {       // pure: no `with`
  Money(lines.map(fn(l) => l.price.0 * l.qty).sum())   // `+ - *` trap on overflow, every build
}

/// Withdraws amt; Err if funds are insufficient.
pub fn withdraw(acc: Account, amt: Money) -> Result<Account, BillError>
  pre  amt.0 > 0
  post match ret { Ok(a) => a.balance.0 == acc.balance.0 - amt.0, Err(_) => true }
{
  if acc.balance.0 < amt.0 { return Err(NoFunds(amt)) }
  Ok(Account { ..acc, balance: Money(acc.balance.0 - amt.0) })
}

/// Generator for `any Money via positive_money`: draws through `g` so failures shrink.
pub fn positive_money(g: Gen) -> Money { Money(g.int(1, 1_000_000)) }

/// Charges the order through the payment gateway.
pub fn charge(o: Order, gw: Gateway) -> Result<Receipt, BillError> with {net, nondet} {
  let body = json.encode(ChargeReq { order: o.id, amount: o.total })
  let resp = http.post(gw.url, body)?            // `?` lifts http.Error into BillError.Net
  let at = time.now()                            // nondet: fake clock under `jip test`
  var attempts = 0                               // `var`: local mutable binding
  while attempts < 3 && resp.status == 503 { attempts += 1 }
  match json.decode::<Receipt>(resp.body) {      // turbofish: explicit type argument
    Ok(r) => Ok(Receipt { ..r, at }),
    Err(e) => Err(Declined("bad receipt: {e}")), // string interpolation
  }
}

/// Picks the risk tier with a System One model.
pub fn risk(o: Order) -> Tier with {decide<fast>} {
  decide<fast>(o)                                // Tier is a closed payload-less enum
}

/// Summarises the order for a human reviewer.
pub fn summary(o: Order) -> Result<Summary, LlmError> with {llm<smart>} {
  llm<smart>("Summarise this order for finance.", o)   // Summary is the output schema
}

/// Inherent methods: no behaviour, same module as the type.
impl Order {
  /// Number of lines on the order.
  pub fn size(self) -> Int { self.lines.len() }
}

/// Bills an order durably: charge, then ask finance when the risk is high.
impl Workflow for Billing {
  type Input = Order
  type Output = Receipt
  type Signal = Cancel
  type E = BillError
  const version = 2
  fn init(cfg: Config) -> Result<Self, BillError> with {io} { Ok(Billing { gw: Gateway { url: cfg.gateway_url } }) }
  fn run(self, o: Order) -> Result<Receipt, WorkflowError<BillError>> with {net, nondet, decide<fast>, approve} {
    let r = charge(o, self.gw)?                  // journaled step, key charge@<site>#1; BillError lifts to App(_)
    if risk(o) != Tier.High { return Ok(r) }     // journaled step, key risk@<site>#1
    let review = Review { title: "High-risk order {o.id}", body: "Total {o.total.0}", timeout: Some(48h), data: None }
    match approve::<Decision>(review)? {         // durable suspension; Rejected lifts to Approval(_)
      Proceed => Ok(r),
      Decline => Err(WorkflowError.App(Declined("finance declined"))),
    }
  }
}

/// Handles billing requests over HTTP.
impl Service for BillingApi {
  type Req = BillReq
  type Resp = Receipt
  type E = BillError
  fn init(cfg: Config) -> Result<Self, BillError> with {io} { Ok(BillingApi { gw: Gateway { url: cfg.gateway_url } }) }
  fn handle(self, req: BillReq) -> Result<Receipt, BillError> with {net, nondet} { charge(req.order, self.gw) }
  fn stop(self) {}
}

/// Fetches two pages concurrently; the block joins or cancels both.
fn both(a: Url, b: Url) -> Result<(http.Body, http.Body), http.Error> with {net, spawn} {
  spawn {
    let ta = task http.get(a)
    let tb = task http.get(b)
    Ok((ta.join()?, tb.join()?))
  }
}

/// Shared counter; reading or writing a Cell is the `mut` effect.
fn bump(c: Cell<Int>) -> Int with {mut} { c.update(fn(n) => n + 1) }

/// Generic over any T that implements Ord.
pub fn max_by<T, K: Ord>(xs: List<T>, key: fn(T) -> K) -> Option<T> {
  xs.fold(None, fn(best, x) => match best {
    None => Some(x),
    Some(b) => if key(x) > key(b) { Some(x) } else { best },
  })
}

/// Leaf primitive over Go; only in stdlib or a module allow-listed in app.jip `externs`.
extern fn read_file(p: Path) -> Result<Bytes, IoError> with {io} = go "os".ReadFile
```

Scenarios (`orders/billing/scenarios.jip`); the clock and the random
generator are fakes by default:

```jip
module orders.billing.scenarios
use orders.billing

scenario "withdraw never overdraws" {
  given acc = any Account                        // contract generator: respects `inv`
  given amt = any Money via positive_money       // named generator, satisfies `pre amt.0 > 0`
  when  r = withdraw(acc, amt)
  then  match r { Ok(a) => a.balance.0 >= 0, Err(_) => true }
}

scenario "charge sends the order total" {
  given http = FakeHttp.new().reply(200, b"{\"id\":\"r1\",\"amount\":500}")   // binds `net`
  given o = Order { id: OrderId(1), lines: [], total: Money(500) }
  when  r = charge(o, Gateway { url: Url("https://pay.test/charge") })
  then  r.is_ok()
  then  http.requests().len() == 1
}

scenario "withdraw table" {
  given rows = [(Money(500), Money(200), true), (Money(100), Money(200), false)]
  given (bal, amt, ok) in rows                   // the block runs once per row
  when  r = withdraw(Account { id: AccountId(1), balance: bal }, amt)
  then  r.is_ok() == ok
}

@extern
scenario "read_file returns the bytes written" {   // every extern has one
  given p = tmp_path("hello.txt")
  when  r = { write_file(p, b"hi")?; read_file(p) }
  then  r == Ok(b"hi")
}
```

Config (`app.jip`):

```jip
app {
  name: "orders"
  config { gateway_url: Url }                    // the `Config` record every `init` receives
  deps { http = git("https://github.com/acme/jip-http", commit: "8f1c2a7", hash: "sha256-Qm…") }
  models { smart = openai("gpt-5"), fast = jev("jev-1-small") }
  secrets { gateway_key: secret<Str> }           // JIP_SECRETS__GATEWAY_KEY; cfg.secrets.gateway_key
  effects {
    net { timeout: default 5s, retry: default { max: 3, backoff: exp } }
    approve { channel: orders.review_inbox, default_timeout: default 48h }
  }
  externs { allow: [orders.billing], max: 1 }    // modules that may declare `extern`
  telemetry { exporter: otlp, endpoint: default "http://localhost:4317", sample: default 0.1 }
  contracts: pre
  workflow { mode: embedded }                    // SQLite journal, single process
  run BillingApi { restart: permanent, max_restarts: 3, period: 60s }
  run Billing
  env prod { telemetry.sample = 0.01 }
}
```

Commands: `jip build`, `jip test`, `jip check`, `jip fmt`, `jip context <module>`.
