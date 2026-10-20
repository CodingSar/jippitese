# 09 — Modules and context

A module is a directory. Its interface is extracted, not written. The
interface file is what reviewers diff and what agents read.

## Example

`orders/billing/charge.jip` (the card module, abbreviated) and a sibling file
in the same directory:

```jip
module orders.billing              // must equal the directory path
use std.json                       // stdlib
use http                           // dependency alias from app.jip deps {}
use orders.customers               // app module; items are customers.x
use orders.customers.vip as vipc   // alias only to resolve a collision

/// Money in minor units.
pub type Money(Int)

/// Gateway handle; representation hidden outside the module.
pub opaque type Gateway { url: Url, key: Str }

/// Charge status.
pub enum Status { Pending, Paid, Failed }

/// Per-module helper visible to orders.* only.
pub(module) fn fmt_money(m: Money) -> Str { "{m.0 / 100}.{m.0 % 100}" }

/// Charges the order through the payment gateway.
pub fn charge(o: Order, gw: Gateway) -> Result<Receipt, BillError> with {net, nondet} {
  let c = customers.lookup(o.customer)?
  let body = json.encode(ChargeReq { order: o.id, amount: o.total, vip: vipc.is_vip(c) })
  http.post(gw.url, body).map(fn(r) => Receipt { ..r, id: o.id })
}
```

`orders/billing/refund.jip`:

```jip
module orders.billing              // same directory, same namespace
/// Refunds a paid order.
pub fn refund(r: Receipt) -> Result<Money, BillError> with {net} { … }
```

Go equivalent: a package per directory, exported by capitalisation, export
data emitted by the compiler. TypeScript equivalent: a generated `.d.ts`:

```ts
// billing.d.ts (generated, never edited)
export declare function charge(o: Order, gw: Gateway): Promise<Result<Receipt, BillError>>;
```

`interface.jipi` is that `.d.ts`, plus effects and contracts.

## Rules

1. **Module = directory.** A module path `a.b.c` names the directory
   `a/b/c` under the app root. Every `.jip` file in that directory starts with
   `module a.b.c`; a mismatch is a compile error (E0101). The app root is the
   directory containing `app.jip`. Dependency roots are `vendor/<alias>/`;
   each has its own `app.jip` whose `name` is the dependency's root module.
2. **One namespace per directory.** All files of a module share one scope;
   a name may be declared once per module. `scenarios.jip` is the module
   `<path>.scenarios` (§12) and is not part of the interface.
3. **`use` forms.** `use a.b` imports app module `a/b`, qualified by its last
   segment (`b.x`). `use std.<name>` imports a stdlib module, qualified by
   `<name>`. `use <alias>` imports the root module of the dependency declared
   as `alias` in `app.jip` `deps {}`; a URL never appears in `use`. A single
   segment resolves as a dependency alias before an app module, and an app
   module at the root whose name equals an alias is a compile error (E0106).
   `use a.b as c` is allowed only when two imports would share a last
   segment; `jip check` warns on an alias with no collision. There are no
   glob imports and no unqualified imports; every imported item is written
   `qualifier.item`.
4. **No cycles.** Module dependencies form a DAG. A cycle is a compile error
   that prints the cycle (E0103).
5. **Visibility.** Items are private by default. `pub` exports an item to any
   module. `pub(module)` exports it to the enclosing parent module (the path
   with the last segment removed) and to every module below that parent;
   it appears in the interface file's `module-visible` section (rule 7),
   never in the public section. `pub type T { … }` and
   `pub enum E { … }` export the body: fields are readable and constructible,
   variants are matchable. `pub opaque type T { … }` and `pub opaque enum E
   { … }` export the name only; outside the module the value is used through
   the module's functions. This is the one opacity form.
6. **Doc line.** Every `pub` item carries one `///` line; missing is a compile
   error (E0105). It is the first line of the item's interface entry.
7. **Interface file.** `jip check` extracts `<dir>/interface.jipi` for every
   module and commits it; nobody edits it; CI fails on drift (E0201). Format:
   line 1 is `interface <path> digest sha256:<hex>` where the hex is the
   SHA-256 of the public section, normalised by `jip fmt`. Then one entry
   per `pub` item in canonical order: types, enums, behaviours, consts,
   functions, impls; alphabetical within kind. An entry is the `///` line, the
   full signature including `with {…}`, then `pre`/`post`/`inv` lines. No
   bodies, no private items, no `use` lines. An opaque type prints as `pub
   opaque type T` with no body. An inherent `impl T` is exported when `T` is
   `pub`, as its header and its `pub` method signatures; an `impl B for T` is
   exported when `T` is `pub`, as its header, associated types, consts and
   method signatures. Any type named in an exported signature must itself be
   `pub` (E0104). After the public section, a module with `pub(module)` items
   prints a second section headed `module-visible digest sha256:<hex>`
   (R7), the SHA-256 of that section alone, with the same entry format; the
   section is absent when there are no such items. Only the public digest is
   classified (rule 8).
8. **API classification.** `jip check` diffs each module's fresh interface
   against the committed one and labels it `breaking`, `compatible` or
   `none`. Breaking: removed item; changed signature or return type; added
   effect; strengthened `pre`; weakened `post`; removed or renamed variant;
   added variant on a non-opaque enum (external `match` is exhaustive);
   added or removed field on a non-opaque type; added required associated
   item on a behaviour. Compatible: added item; added variant on an opaque
   enum; removed effect; doc change. Contract lines are compared textually
   after `jip fmt` normalisation: adding a `post` conjunct or removing a
   `pre` conjunct is `compatible`; any other change to a `pre`, `post` or
   `inv` line is labelled `contract-changed` and counts as breaking, because
   the compiler does not decide implication. A dependency bump is classified
   against the interface in `vendor/`. A `breaking` result is a non-zero
   exit unless the break is accepted with `jip check --accept-breaking
   <module>` or the commit trailer `jip-breaking: <module>` (R8); either
   makes `jip check` exit 0 and print the classification unchanged.
9. **Build cache.** Each module emits its own Go package, deterministically
   (§13 rule 12), so a body edit or a private item regenerates only that
   module's Go; `GOCACHE` rebuilds a dependent only when the Go export data
   it imports changes. The interface digests are the compatibility key of
   `jip check` (rule 8), not a cache key.
10. **Context pack.** `jip context <module>` prints tier 1, the module's full
    interface file, then tier 2: for each direct dependency, in the order the
    module first references them (file order, then line order), only the
    items the module references plus the closure of types those signatures
    mention. The closure runs over types only, never over functions. An
    imported module with no referenced item prints one line and `jip check`
    warns `unused import`. `--short` prints one line per symbol (signature
    only). `--for <file>` narrows both tiers to what that file defines or
    references.

### Interface file for `orders.billing`

Extracted from the card's version of the module (not the abbreviated
example above):

```text
interface orders.billing digest sha256:4f2b9c0e7a1d6b3f8c5e2a9d0b7f4e1c3a6d8b5f2e9c0a7d4b1f6e3c8a5d2b9f

/// Money in minor units.
pub type Money(Int)

/// A customer order.
pub type Order { id: OrderId, lines: List<Line>, total: Money }
  inv total.0 >= 0

/// Why an order could not be billed.
pub enum BillError { NoFunds(Money), Net(http.Error), Declined(Str) }

/// What finance answers on a high-risk order.
pub enum Decision { Proceed, Decline }

/// Charge status.
pub enum Status { Pending, Paid, Failed }

/// Charges the order through the payment gateway.
pub fn charge(o: Order, gw: Gateway) -> Result<Receipt, BillError> with {net, nondet}

/// Generic over any T that implements Ord.
pub fn max_by<T, K: Ord>(xs: List<T>, key: fn(T) -> K) -> Option<T>

/// Generator for `any Money via positive_money`: draws through `g` so failures shrink.
pub fn positive_money(g: Gen) -> Money

/// Picks the risk tier with a System One model.
pub fn risk(o: Order) -> Tier with {decide<fast>}

/// Summarises the order for a human reviewer.
pub fn summary(o: Order) -> Result<Summary, LlmError> with {llm<smart>}

/// Sum of line totals.
pub fn total(lines: List<Line>) -> Money

/// Withdraws amt; Err if funds are insufficient.
pub fn withdraw(acc: Account, amt: Money) -> Result<Account, BillError>
  pre  amt.0 > 0
  post match ret { Ok(a) => a.balance.0 == acc.balance.0 - amt.0, Err(_) => true }

/// Inherent methods: no behaviour, same module as the type.
impl Order
  /// Number of lines on the order.
  pub fn size(self) -> Int

/// Bills an order durably: charge, then ask finance when the risk is high.
impl Workflow for Billing
  type Input = Order
  type Output = Receipt
  type Signal = Cancel
  type E = BillError
  const version = 2
  fn init(cfg: Config) -> Result<Self, BillError> with {io}
  fn run(self, o: Order) -> Result<Receipt, WorkflowError<BillError>> with {net, nondet, decide<fast>, approve}

/// Handles billing requests over HTTP.
impl Service for BillingApi
  type Req = BillReq
  type Resp = Receipt
  type E = BillError
  fn init(cfg: Config) -> Result<Self, BillError> with {io}
  fn handle(self, req: BillReq) -> Result<Receipt, BillError> with {net, nondet}
  fn stop(self)
```

(`Billing` and `BillingApi` are `pub` types declared elsewhere in the module;
private items such as `both`, `bump` and `read_file` are absent. The
`module-visible` section is absent because the card declares no
`pub(module)` item; the abbreviated example above would print `fmt_money`
there.)

### `jip check` classification output

```text
orders.billing: breaking
  Removed:
    - pub fn refund(r: Receipt) -> Result<Money, BillError> with {net}
  Changed:
    - pub fn charge(o: Order, gw: Gateway) -> Result<Receipt, BillError> with {net}
    + pub fn charge(o: Order, gw: Gateway) -> Result<Receipt, BillError> with {net, nondet}
  Added:
    + pub enum Status { Pending, Paid, Failed }
orders.customers: none
```

With `--accept-breaking orders.billing`, or the trailer `jip-breaking:
orders.billing` on the commit under check, the same output ends in exit
code 0.

### Context pack for `orders.billing`

For the card's module, `jip context orders.billing` prints the interface
above (tier 1) followed by tier 2, which is far smaller than the four whole
dependency interfaces because it carries only referenced items. Line and
token counts are measured by the §13 evaluation once the gate compiler
exists (R15); no figure is claimed here. The card's `charge` never touches
`customers.*`, so that import prints as unreferenced:

```text
-- std.json (referenced: encode, decode)
pub fn encode<T>(v: T) -> Bytes
pub fn decode<T>(b: Bytes) -> Result<T, json.Error>
pub enum Error { Syntax(Int), Shape(Str) }
-- http (referenced: post, Error)
pub fn post(u: Url, body: Bytes) -> Result<Response, Error> with {net}
pub type Response { status: Int, body: Bytes }
pub enum Error { Timeout, Status(Int), Transport(Str) }
-- std.time (referenced: now)
pub fn now() -> Instant with {nondet}
pub opaque type Instant
-- orders.customers (imported, nothing referenced)
```

`--short` prints one line per symbol, signature only.

## Diagnostics

```text
error[E0101]: module line does not match directory
  --> orders/billing/charge.jip:1:8
   |
 1 | module orders.biling
   |        ^^^^^^^^^^^^^ found `orders.biling`
   = note: this file is in `orders/billing/`, expected `module orders.billing`
   = help: replace line 1 with `module orders.billing`
```

```text
error[E0201]: interface drift in `orders.billing`
  --> orders/billing/interface.jipi:1:30
   |
 1 | interface orders.billing digest sha256:4f2b9c0e…
   |                                 ^^^^^^^^^^^^^^^^ committed digest; extracted sha256:91ac03d7…
   = note: classification: breaking (1 changed)
   = help: run `jip check --write-interfaces` and commit `orders/billing/interface.jipi`
```

## Grammar

```text
ModuleDecl    ::= "module" ModulePath NEWLINE
ModulePath    ::= Ident ("." Ident)*
UseDecl       ::= "use" UsePath ("as" Ident)? NEWLINE
UsePath       ::= "std" "." Ident | Ident | ModulePath   /* a single Ident is a dependency alias first */
Visibility    ::= "pub" | "pub" "(" "module" ")"
Opacity       ::= "opaque"
Item          ::= DocLine? Visibility? Opacity? (TypeDecl | EnumDecl | FnDecl | ConstDecl | BehaviourDecl | ImplDecl | ExternDecl)
DocLine       ::= "///" Text NEWLINE

InterfaceFile ::= "interface" ModulePath "digest" "sha256:" Hex NEWLINE Entry* ModuleSection?
ModuleSection ::= "module-visible" "digest" "sha256:" Hex NEWLINE Entry*
Entry         ::= DocLine Signature ContractLine*
Signature     ::= "pub" "opaque"? (TypeSig | EnumSig | BehaviourSig | ConstSig | FnSig | ImplSig)
ContractLine  ::= ("pre" | "post" | "inv") Expr NEWLINE
```

`Signature` never carries `pub(module)` (the section says it) and never a
`run` line: `run` belongs to `app.jip` (section 10), not to a module.
