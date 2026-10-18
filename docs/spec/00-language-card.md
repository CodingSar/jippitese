# jippitese language card

Load this before writing `.jip`. Everything below compiles under the v0 spec.
Rules: full signatures at boundaries, local inference in bodies; effects after
the return type with `with {…}`, omitted when pure; `/// doc` line on every
`pub` item; one `module` line first in every file.

```jip
module orders.billing            // first line of every file: the module path
use std.json                     // stdlib module
use std.time
use http                         // dependency alias from app.jip deps {}
use orders.customers             // sibling app module; refer to it as customers.*

/// Money in minor units.
pub type Money(Int)                              // newtype, distinct from Int

/// A customer order.
pub type Order { id: OrderId, lines: List<Line>, total: Money }
  inv total.0 >= 0                               // invariant, checked at construction

/// Why an order could not be billed.
pub enum BillError { NoFunds(Money), Net(http.Error), Rejected(Str) }

/// Charge status.
pub enum Status { Pending, Paid, Failed }        // payload-less sum type

/// Sum of line totals.
pub fn total(lines: List<Line>) -> Money {       // pure: no `with`
  Money(lines.map((l) => l.price.0 * l.qty).sum())
}

/// Withdraws amt; Err if funds are insufficient.
pub fn withdraw(acc: Account, amt: Money) -> Result<Account, BillError>
  pre  amt.0 > 0
  post match ret { Ok(a) => a.balance.0 == acc.balance.0 - amt.0, Err(_) => true }
{
  if acc.balance.0 < amt.0 { return Err(NoFunds(amt)) }
  Ok(Account { ..acc, balance: Money(acc.balance.0 - amt.0) })
}

/// Charges the order through the payment gateway.
pub fn charge(o: Order, gw: Gateway) -> Result<Receipt, BillError> with {net, nondet} {
  let body = json.encode(ChargeReq { order: o.id, amount: o.total })
  let resp = http.post(gw.url, body)?            // `?` lifts http.Error into BillError::Net
  let at = time.now()                            // nondet
  var attempts = 0                               // `var`: local mutable binding
  while attempts < 3 && resp.status == 503 { attempts += 1 }
  match json.decode<Receipt>(resp.body) {
    Ok(r) => Ok(Receipt { ..r, at }),
    Err(e) => Err(Rejected("bad receipt: {e}")),  // string interpolation
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

/// Bills an order durably: charge, then ask finance when the risk is high.
impl Workflow for Billing {
  type Input = Order
  type Output = Receipt
  type Signal = Cancel
  const version = 2
  fn run(self, o: Order) -> Result<Receipt, WorkflowError> with {net, nondet, decide<fast>, approve} {
    let r = charge(o, self.gw)?                  // journaled step charge#1
    if risk(o) == Tier.High {                    // journaled step risk#1
      let ok: Bool = approve(Review { title: "High-risk order {o.id}", timeout: 48h })?
      if !ok { return Err(WorkflowError.Rejected) }
    }
    Ok(r)
  }
}

/// Handles billing requests over HTTP.
impl Service for BillingApi {
  type Req = BillReq
  type Resp = Receipt
  type E = BillError
  fn init(cfg: Config) -> Result<Self, BillError> with {io} { Ok(BillingApi { gw: cfg.gateway }) }
  fn handle(self, req: BillReq) -> Result<Receipt, BillError> with {net, nondet} { charge(req.order, self.gw) }
  fn stop(self) {}
}

/// Fetches two pages concurrently; the block joins or cancels both.
fn both(a: Url, b: Url) -> Result<(Body, Body), http.Error> with {net, spawn} {
  spawn {
    let ta = task http.get(a)
    let tb = task http.get(b)
    Ok((ta.join()?, tb.join()?))
  }
}

/// Shared counter; reading or writing a Cell is the `mut` effect.
fn bump(c: Cell<Int>) -> Int with {mut} { c.update((n) => n + 1) }

/// Generic over any T that implements Ord.
pub fn max_by<T, K: Ord>(xs: List<T>, key: fn(T) -> K) -> Option<T> {
  xs.fold(None, (best, x) => match best {
    None => Some(x),
    Some(b) => if key(x) > key(b) { Some(x) } else { best },
  })
}

/// Leaf primitive over Go; allowed only in stdlib or allow-listed modules.
extern fn read_file(p: Path) -> Result<Bytes, IoError> with {io} = go "os".ReadFile
```

Scenarios (`orders/billing/scenarios.jip`):

```jip
module orders.billing.scenarios
use orders.billing

scenario "withdraw never overdraws" {
  given acc = any Account where acc.balance.0 >= 0
  given amt = any Money where amt.0 > 0
  when  r = withdraw(acc, amt)
  then  match r { Ok(a) => a.balance.0 >= 0, Err(_) => true }
}

scenario "charge sends the order total" {
  given gw = FakeGateway.new()
  given o = Order { id: OrderId(1), lines: [], total: Money(500) }
  when  _ = charge(o, gw.gateway())
  then  gw.last_amount() == Some(Money(500))
}
```

Config (`app.jip`):

```jip
app {
  name: "orders"
  deps { http = git("https://github.com/acme/jip-http", commit: "8f1c2a7", hash: "sha256-Qm…") }
  models { smart = openai("gpt-5"), fast = jev("jev-1-small") }
  secrets { gateway_key: secret<Str> }           // JIP_SECRETS__GATEWAY_KEY
  effects { net { timeout: default 5s, retry: default { max: 3, backoff: exp } } }
  telemetry { exporter: otlp, endpoint: default "http://localhost:4317", sample: default 0.1 }
  contracts: pre
  workflow: embedded
  run BillingApi { restart: permanent, max_restarts: 3, period: 60s }
  run Billing
  env prod { telemetry.sample = 0.01 }
}
```

Commands: `jip build`, `jip test`, `jip check`, `jip fmt`, `jip context <module>`.
