# 3. Functions and effects

A function's first line says what it takes, what it returns and what it may
do. Nothing in a body can widen the declared effect set.

## Example

```jip
module orders.billing
use std.json
use std.time
use http

/// Sum of line totals.
pub fn total(lines: List<Line>) -> Money {              // pure: no `with`
  Money(lines.map(fn(l) => l.price.0 * l.qty).sum())
}

/// Runs f up to n times until it returns Ok; f's effects flow to the caller.
pub fn retry<T, E>(n: Int, f: fn() -> Result<T, E>) -> Result<T, E>
  pre n >= 1
{
  var r = f()
  var left = n - 1
  while left > 0 && r.is_err() {
    r = f()
    left -= 1
  }
  r
}

/// Charges an order through the gateway; the receipt is stamped with the call time.
pub fn charge(o: Order, gw: Gateway) -> Result<Receipt, BillError> with {net, nondet}
  pre o.total.0 > 0
{
  let body = json.encode(ChargeReq { order: o.id, amount: o.total })
  let resp = retry(3, fn() => http.post(gw.url, body))?  // closure has {net}; retry stays pure
  let r = json.decode::<Receipt>(resp.body)?
  Ok(Receipt { ..r, at: time.now() })                  // nondet
}

/// Composes two closures; the result carries both effect sets.
pub fn compose<A, B, C, e1, e2>(f: fn(A) -> B with e1, g: fn(B) -> C with e2) -> fn(A) -> C with e1 + e2 {
  fn(a) => g(f(a))
}

/// Sums f over xs in parallel; f must be pure.
pub fn par_sum(xs: List<Int>, f: fn(Int) -> Int with {}) -> Int with {spawn} {
  par.map(xs, f).sum()
}

impl Order {
  /// Number of lines.
  pub fn size(self) -> Int { self.lines.len() }

  /// Copy of self with one more line.
  pub fn add(self, l: Line) -> Self {
    let lines = self.lines.push(l)
    Order { ..self, lines, total: total(lines) }
  }
}
```

Go equivalent (effects erased; one leading `ctx *rt.Ctx`, the runtime
record `Ctx { cancel, journal, effects, fakes }`):

```go
func Total(lines List[Line]) Money { /* pure: no ctx */ }

func Retry[T, E any](n int, f func() Result[T, E]) Result[T, E] { /* no ctx: retry is pure */ }

func Charge(ctx *rt.Ctx, o Order, gw Gateway) Result[Receipt, BillError] {
	body := json.Encode(ChargeReq{Order: o.ID, Amount: o.Total})
	resp := Retry(3, func() Result[http.Resp, http.Error] {
		return http.Post(ctx, gw.URL, body) // closure captures ctx
	})
	if !resp.ok { return Result[Receipt, BillError]{e: BillError_Net{resp.e}} }
	r := json.Decode[Receipt](resp.v.Body)
	if !r.ok { return Result[Receipt, BillError]{e: BillError_Rejected{r.e.String()}} }
	return Result[Receipt, BillError]{ok: true, v: Receipt{At: rt.Now(ctx) /* … */}}
}

func (o Order) Size() int { return o.Lines.Len() }
```

TypeScript equivalent (effects invisible in the type):

```ts
export function total(lines: Line[]): Money { /* … */ }
export async function charge(o: Order, gw: Gateway): Promise<Result<Receipt, BillError>> {
  const resp = await retry(3, () => http.post(gw.url, body));
  // …
}
```

## Rules

1. **Declaration.** `fn name(params) -> Type with {effects}`. Every parameter
   has a type; the return type is written unless it is `()`; arguments are
   positional (no defaults, no variadics). The body is a block whose tail
   expression is the result.
2. **`pub` and docs.** `pub fn` is exported and must be preceded by exactly
   one `///` line. A `///` line on a non-`pub` item is allowed; a missing one
   on a `pub` item is a compile error.
3. **Generics.** Type parameters are `PascalCase` in `<…>` after the name;
   a constraint is a behaviour, `K: Ord`; several are joined with `+`,
   `K: Ord + Hash`. Effect-set variables are `lowercase` in the same list
   (`e1`) and may only appear in function types (rule 12).
4. **`impl Type { … }`** declares inherent methods and associated functions
   on a type of the same module; the stdlib uses it for `Cell.new`,
   `MutList.new` and `List.builder` (`docs/decisions.md` §3, R3). `impl
   Behaviour for Type { … }` implements a behaviour (section 06); its
   methods' effect sets must be subsets of the behaviour's budget.
5. **`self` and `Self`.** A method's first parameter is the bare word `self`,
   typed as the implementing type; `Self` names that type anywhere in the
   `impl`. A function inside an `impl` without `self` is an associated
   function, called `Type.f(…)` (`Cell.new(0)`). `self` is a value; a method
   returns a changed copy. `inv` lines inside an `impl` are in section 05.
6. **Effect set.** `with {…}` follows the return type. Labels are `io`,
   `net`, `mut`, `llm<role>`, `decide<role>`, `nondet`, `spawn`, `approve`.
   Omitted means pure; `jip fmt` deletes a written `with {}` on a declaration
   and orders labels `io, net, mut, llm<role>, decide<role>, nondet, spawn,
   approve`, roles sorted by name within a label.
7. **Upper bound.** A body may perform only the declared effects. A missing
   effect is a compile error naming the call that needs it. A declared effect
   nothing in the body uses is a `jip check` warning, never an error, so
   behaviour impls and stubs may over-declare.
8. **Subset at calls.** A call to a function with effect set `S` is legal
   only where `S` is a subset of the caller's declared set.
9. **Roles.** The role in `llm<role>` and `decide<role>` is a name declared
   in `app.jip` `models {}`; an undeclared role is a compile error. The role is
   part of the label: `llm<smart>` does not cover `llm<fast>`.
10. **Contextual closure rule.** An unannotated function-typed parameter
    `f: fn(T) -> U` has no effect set of its own. At a call site, the
    argument's effects are added to the call's effects, so `map`, `fold`,
    `retry` carry no effect variables and are pure in themselves.
11. **Annotated parameters are bounds.** `f: fn(T) -> U with {net}` accepts
    closures whose effects are a subset of `{net}`; `with {}` accepts only
    pure closures. The bound, not the argument, counts toward the caller.
12. **Stored or returned closures carry explicit sets.** A function type in a
    return position, record field, collection or `let` annotation must write
    its set (or omit it, meaning pure). Generic combinators may use effect-set
    variables with union only (`e1 + e2`); there is no intersection,
    subtraction or complement. Calling such a closure contributes its written
    set to the caller.
13. **Closures** start with `fn`: `fn(x) => expr`, `fn(x, y) => { … }`,
    `fn(x: Int) => x + 1`, `fn() => f()`. Parameter types are inferred from
    the expected function type, annotations allowed. A closure's effect set
    is inferred from its body and never written on it. A parenthesised list
    without `fn` is a tuple (section 04), never a parameter list.
14. **`spawn`.** The effects inside a `spawn { … }` block must be a subset of
    the enclosing declared set (section 08).
15. **`nondet`** is defined extensionally. Exactly four stdlib primitives
    carry it: `time.now()`, `random()` (the seeded generator; every
    `random.*` helper is built on it), `time.sleep(d)` and `await_signal`
    (section 07). Nothing else carries `nondet`. Inside `Workflow.run` the
    compiler lowers each to its journaled runtime form; the label and the
    code are the same. Under `jip test` all four read the fake clock and the
    seeded generator unless the scenario says `given real_time`
    (`12-testing.md` rule 10).
16. **Contracts.** `pre`, `post`, `inv` lines sit between the signature (after
    `with`) and the opening `{`; they are typed with the empty effect set
    (section 05).
17. **Recursion** is allowed, direct and mutual, with no extra effect.
18. **Erasure.** Effects leave no trace in Go types. Every function with a
    non-empty set takes one hidden leading parameter, the runtime record
    `Ctx { cancel, journal, effects, fakes }`: `cancel` is the cancellation
    signal (section 08), `journal` the workflow journal or none (section
    07), `effects` the `app.jip` instrumentation policy and `fakes` the
    bound test fakes (section 12). Closures capture it where they are
    created; pure functions take none; an `extern` receives none (section
    11). Effect sets are static metadata in interface files (section 09).
19. **`extern fn`** writes its effect set explicitly and the compiler checks
    it against the effect floor; it is never inferred (section 11).

## Diagnostics

Missing effect (rule 7):

```text
error[E0412]: missing effect `net`
  --> orders/billing/charge.jip:37:27
   |
37 |   let resp = retry(3, fn() => http.post(gw.url, body))?
   |                              ^^^^^^^^^ `http.post` has effect `net`
   = note: `charge` declares `with {nondet}`
   = help: change the signature to `-> Result<Receipt, BillError> with {net, nondet}`
```

Effectful closure where a pure one is required (rule 11):

```text
error[E0411]: closure exceeds the effect bound of parameter `f`
  --> orders/report.jip:12:21
   |
12 |   par_sum(ids, fn(i) => http.get(url(i)).len())
   |                        ^^^^^^^^^^^^^^^^^^ closure has effects {net}
   = note: `f: fn(Int) -> Int with {}` accepts only pure closures
   = help: fetch outside the closure and pass the data in, or declare `f: fn(Int) -> Int with {net}`
```

Unused declared effect (rule 7, `jip check`):

```text
warning[W0401]: unused effect `io`
  --> orders/billing/charge.jip:33:74
   |
33 | pub fn charge(o: Order, gw: Gateway) -> Result<Receipt, BillError> with {io, net, nondet}
   |                                                                          ^^ declared, never used in the body
   = help: change the signature to `-> Result<Receipt, BillError> with {net, nondet}`
```

## Grammar

```
FnDecl        ::= DocLine? "pub"? "fn" Ident Generics? "(" Params? ")"
                  ("->" Type)? ("with" EffectSet)? Contract* Block
Generics      ::= "<" GenericParam ("," GenericParam)* ">"
GenericParam  ::= TypeIdent (":" TypeIdent ("+" TypeIdent)*)? | EffectVar
Params        ::= Param ("," Param)*
Param         ::= "self" | Ident ":" Type
Type          ::= TypeIdent ("<" Type ("," Type)* ">")? | "(" (Type ("," Type)*)? ")" | FnType
FnType        ::= "fn" "(" (Type ("," Type)*)? ")" "->" Type ("with" EffectExpr)?
EffectExpr    ::= EffectSet | EffectVar | EffectExpr "+" EffectExpr
EffectSet     ::= "{" (Effect ("," Effect)*)? "}"
Effect        ::= "io" | "net" | "mut" | "llm" "<" Ident ">" | "decide" "<" Ident ">"
                | "nondet" | "spawn" | "approve"
EffectVar     ::= Ident                      /* lowercase, declared in Generics */
ImplDecl      ::= DocLine? "impl" TypeIdent Generics? ("for" Type)? "{" AssocItem* InvClause* FnDecl* "}"
AssocItem     ::= "type" TypeIdent "=" Type | "const" Ident "=" Expr   /* behaviour impls only */
Closure       ::= "fn" "(" (ClosureParam ("," ClosureParam)*)? ")" "=>" (Expr | Block)
ClosureParam  ::= Ident (":" Type)?
```
