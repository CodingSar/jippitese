# 05 Contracts

`pre`, `post` and `inv` are checked claims about values. They are pure by
construction and double as the oracle for the property checks `jip test`
derives (§4 of `decisions.md`).

## Example

```jip
module collections.stack

/// Bounded LIFO stack.
pub type Stack<T> { items: List<T>, cap: Int }
  inv cap > 0
  inv items.len() <= cap

/// Pushes x on top; full stacks are rejected by pre.
pub fn push<T>(s: Stack<T>, x: T) -> Stack<T>
  pre  s.items.len() < s.cap
  post ret.items.len() == old(s.items.len()) + 1
  post ret.items.last() == Some(x)
{
  Stack { ..s, items: s.items.push(x) }
}

/// Pops the top; None on an empty stack.
pub fn pop<T>(s: Stack<T>) -> Option<(T, Stack<T>)>
  post match ret { Some((_, r)) => r.items.len() == s.items.len() - 1, None => s.items.len() == 0 }
{
  match s.items.last() {
    None => None,
    Some(x) => Some((x, Stack { ..s, items: s.items.drop_last() })),
  }
}

/// Mean of a non-empty list of non-negative readings.
pub fn mean(xs: List<Int>) -> Int
  pre  xs.len() > 0
  pre  forall x in xs: x >= 0
  post ret >= 0
{
  xs.sum() / xs.len()
}

/// Shared stack; set/update re-check the payload inv under `all`.
fn fill(c: Cell<Stack<Int>>, n: Int) -> () with {mut} {
  c.update(fn(s) => push(s, n))
}

/// Counts ticks; the count never exceeds the configured limit.
impl Service for Counter {
  type Req = Tick
  type Resp = Int
  type E = CounterError
  inv seen >= 0
  inv seen <= limit
  fn init(cfg: Config) -> Result<Self, CounterError> with {mut} {
    Ok(Counter { seen: Cell.new(0), limit: cfg.limit })
  }
  fn handle(self, req: Tick) -> Result<Int, CounterError> with {mut}
    post match ret { Ok(k) => k <= self.limit, Err(_) => true }   // impls add post, never pre (06)
  {
    if req.n <= 0 || self.seen.get() + req.n > self.limit { return Err(CounterError.Full) }
    Ok(self.seen.update(fn(k) => k + req.n))
  }
  fn stop(self) {}
}
```

Go equivalent. The guards are hand-written and unconditional, and so is the
property test that `jip test` derives for free:

```go
// Push pushes x on top; full stacks are rejected.
func Push[T any](s Stack[T], x T) Stack[T] {
	if !(len(s.Items) < s.Cap) { panic("pre: len(s.Items) < s.Cap") }
	oldLen := len(s.Items)
	r := Stack[T]{Items: append(append([]T{}, s.Items...), x), Cap: s.Cap}
	if !(len(r.Items) == oldLen+1) { panic("post: len(ret.Items) == old+1") }
	return r
}
// TestPushProperty: generators, shrinking and seed replay hand-written here.
```

## Rules

1. **Placement.** Contract lines sit between the signature and the opening
   `{` of the body, one clause per line, any number of lines. All `pre`
   lines precede all `post` lines. `inv` is never written on a function.
2. **Purity.** A contract expression is typed with the empty effect set. It
   may call any function whose effect set is empty, including `pub fn`s of
   other modules. A call with any effect, including reading a `Cell`, is a
   compile error (Diagnostics). The one exception is rule 8: inside an
   `impl` `inv` a `Cell` field reads as its payload through a runtime
   snapshot, which keeps the clause pure.
3. **Scope.** `pre` sees the parameters. `post` sees the parameters, `ret`
   (the returned value, of the declared return type) and `old(e)`.
4. **`old(e)`.** Allowed only in `post`. `e` is a pure expression whose free
   variables are all parameter names, so `old(s)`, `old(s.cap)` and
   `old(s.items.len())` are allowed and `old(ret)` is not. Each `old(e)` is
   evaluated once, before the body runs, and the value is substituted when
   the `post` is checked.
5. **Quantifiers.** `forall x in c: e` and `exists x in c: e` are `Bool`
   expressions valid only inside contracts. `c` is a finite collection
   (`List`, `Set`, `Map` entries) or a range `a..b`; `e` is a `Bool` in which
   `x` is bound. They compile to loops and short-circuit.
6. **`inv` on types.** One or more `inv` lines follow a `type` (record,
   enum or newtype) declaration. Record fields are in scope bare; `self`
   names the whole value, so an enum `inv` matches on `self` and a newtype
   `inv` uses `self.0`. Every `inv` is checked when a value of the type is
   constructed, including by record update `Name { ..base, f: v }` and by
   `json.decode`; nothing else can produce a value.
7. **`inv` and `Cell`.** For a `Cell<T>` whose `T` carries `inv`, `set` and
   `update` re-check `T`'s `inv` on the new payload under `contracts: all`.
8. **`inv` on an `impl`.** `inv` lines directly inside an `impl` block, before
   the first method, state the behaviour's state invariant. Fields of `Self`
   are in scope bare; a field of type `Cell<T>` reads as its payload of type
   `T` (the runtime snapshots it, so the clause stays pure). The invariant is
   checked after `init` returns `Ok` and after every other lifecycle method
   returns, under `all`.
9. **Levels.** `contracts: all | pre | none` in `app.jip` (cross-reference
   10). `jip build` defaults to `pre`; `jip test` always runs under `all`.
   Under `pre`, `post` and `inv` are not evaluated and `old()` copies are not
   taken. Under `none` nothing is evaluated.
10. **Violation.** A failed clause in a plain function is a `panic` (not
    catchable, `04-expressions-and-control.md` rule 13). The message carries
    the clause text, the `file:line:col` of the clause and the value of
    every free variable of the clause (`ret` included). A failed clause
    inside a behaviour method (`init`, `handle`, `run`, `tick`, an `impl`
    `inv` after one of them) is not a panic: the method returns `Err` with
    the runtime error `ContractViolation` carrying the same message, the
    transport maps it (HTTP 500 for `Service`, a failed task for `Workflow`)
    and the instance keeps running, so one bad request cannot start a
    restart storm (`06-behaviours.md` rule 5). The violation is logged and
    counted either way.
11. **Not validation.** External input (requests, files, model output) is
    checked with `Result`, never with `pre`: a `pre` is a claim about the
    caller's code, a `Result` about the world.
12. **No frame clause.** Values are immutable and shared mutation is the
    `mut` effect, so "what may change" is already visible in the signature.
    There is no `modifies` or `reads`.
13. **Interface files.** `pre` and `post` lines are part of a `pub fn`'s
    interface entry and of context packs; `inv` is part of a `pub type`'s
    entry (cross-reference 09). A stronger `pre` or weaker `post` is a
    `breaking` change under `jip check`.
14. **Derived property check (functions).** `jip test` derives one check
    for every module-level `pub fn` of an app module that is not generic
    and (a) has an effect set that is empty, or is exactly
    `{mut}` while no parameter type mentions `Cell` (all cells it touches are
    created inside the function), and (b) has at least one `post`, or has a
    parameter or return type that carries `inv` directly or in a field.
    Functions with `io`, `net`, `llm`, `decide`, `nondet`, `spawn` or
    `approve` get no derived check; scenarios with fakes cover them
    (cross-reference 12). Generic functions, `impl` methods (rule 17
    covers them) and `extern` functions get none either, nor does a
    function with a parameter type that has no structural generator
    (rule 15).
15. **Generators.** One generator per parameter, derived structurally from
    the type under a fixed size model: recursive types stop at depth 3 by
    taking the smallest variant, collections at length 8, `Str` at 32
    bytes. `pre` narrows them: in each `&&` conjunct, a comparison (`<`,
    `<=`, `>`, `>=`, `==`) of a parameter, a field path or a `.len()`
    against a constant or a parameter declared earlier becomes a bound on
    that generator, and a range membership `(lo..hi).contains(x)` becomes a
    range generator. Any clause not of that shape is a filter. Every clause
    is still evaluated on every candidate, so a bound only saves
    rejections. If more than 90% of candidates
    are rejected by filters, `jip test` prints warning `W0510` (`pre
    rejects too many candidates`, with the clause) and continues; it is not
    a failure. The owner names a generator explicitly with `via`: `given x
    = any T via g` (scenarios, `12-testing.md` rule 4) where `g` is a `pub
    fn g(g: Gen) -> T` of the module under test that draws every random
    choice through `Gen` (`std.gen`: `g.int(lo, hi)`, `g.pick(xs)`,
    `g.list(n, fn(g) => …)`), so shrinking and seed replay apply to it
    unchanged. `inv` on the generated type is checked on every candidate.
16. **Runs, oracle, shrinking.** N = 200 runs by default (`jip test --runs
    N`). Oracle: every `post`, every `inv` of the parameter and return
    types, and no `panic`. Every run executes under the fake clock and the
    seeded generator that `jip test` installs (`12-testing.md` rule 10), so
    a seed reproduces a run exactly even through `nondet` calls. A failure
    is shrunk by integrated shrinking of the random stream, so the shrunk
    example still satisfies `pre`. The shrunk seed is written to
    `.jip/seeds/<module dir>/<fn>.seed` (for `collections.stack`,
    `.jip/seeds/collections/stack/pop.seed`), committed, and replayed as run
    1 of every later `jip test` (not when `--seed` pins the run). If `pre`
    rejects every candidate of the first 3 runs, the check stops and is
    reported `skip … no candidate satisfies \`pre\`` with `W0510`; a
    check whose oracle was never exercised is never reported `ok`.
17. **Derived stateful check (`impl`).** For an `impl` with `inv` whose
    `init` and at least one other lifecycle method have effect sets within
    `{mut}`, `jip test` derives one check: generate a `Config` (the
    `config {}` record of `app.jip` plus `secrets`, every field by its type
    under rule 15, secrets as `Secrets.fake()`), call `init`,
    draw a sequence of up to 50 calls to the qualifying methods with
    generated arguments, skip a step whose `pre` fails, check `inv` after
    every step, call `stop` last. Runs, shrinking and seeds are as rule 16
    under `<impl name>.seed`. Methods with any other effect are never drawn.
18. **Stdlib behaviour contracts.** `Worker` idempotency and `Repository`
    get-after-put are `post` and `inv` clauses written in the stdlib
    behaviour; they derive checks by the same rules (cross-reference 06).

## Diagnostics

An effectful call inside a contract (rule 2):

```text
error[E0601]: effect in contract
  --> collections/stack.jip:55:7
   |
55 |   inv seen.get() >= 0
   |       ^^^^^^^^^^ `Cell.get` has effect {mut}; contracts are typed with {}
   = note: a `Cell` field in an `impl` inv reads as its payload
   = help: replace `seen.get()` with `seen`
```

A derived check failing on a `post` (here `pop` was changed to return the
stack unchanged):

```text
FAIL  collections.stack  derived pop
    at collections/stack.jip:24:1
    when ret = pop(s)
    contract violated: post match ret { Some((_, r)) => r.items.len() == s.items.len() - 1, None => s.items.len() == 0 } at collections/stack.jip:28:8
    s = Stack { items: [0], cap: 1 }
    seed 0x1c9e3a7f saved to .jip/seeds/collections/stack/pop.seed
    replay: jip test collections.stack --seed 0x1c9e3a7f
```

The bindings are the shrunk example; `ret` is absent because the `post`
fails inside the call.

The same clause failing in production under `contracts: all` panics:

```text
panic: contract violation: post
  collections/stack.jip:28:3  post match ret { Some((_, r)) => … }
  s   = Stack { items: [0], cap: 1 }
  ret = Some((0, Stack { items: [0], cap: 1 }))
```

## Grammar

```
FnDecl      ::= Doc? "pub"? "fn" Ident Generics? "(" Params? ")" "->" Type EffectSet? Contract* Block
Contract    ::= PreClause | PostClause
PreClause   ::= "pre" Expr NEWLINE
PostClause  ::= "post" Expr NEWLINE
TypeDecl    ::= Doc? "pub"? "type" Ident Generics? TypeBody NEWLINE InvClause*
ImplDecl    ::= Doc? "impl" Ident ("for" Ident)? "{" AssocItem* InvClause* Method* "}"  /* no "for": inherent impl, 03 rule 4 */
InvClause   ::= "inv" Expr NEWLINE
Expr        ::= … | "ret" | "old" "(" Expr ")" | Quantifier
Quantifier  ::= ("forall" | "exists") Ident "in" Expr ":" Expr
```

`ret`, `old` and `Quantifier` are valid only inside a `Contract` or
`InvClause`; all `PreClause`s precede all `PostClause`s.
