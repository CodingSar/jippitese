# 8. Concurrency and mutation

Concurrency is structured: a `spawn` block owns every task it starts and
ends only when they have. Mutation is local by default (`var`, mutable
collections) and shared only through `Cell`, which is the `mut` effect.

## Example

```jip
module orders.pricing
use http

/// Fetches both pages; the block joins or cancels both before returning.
pub fn both(a: Url, b: Url) -> Result<(Body, Body), http.Error> with {net, spawn} {
  spawn {
    let ta = task http.get(a)                  // Task<Result<Body, http.Error>>
    let tb = task http.get(b)
    Ok((ta.join()?, tb.join()?))               // first Err cancels the other task
  }
}

/// Prices every line in parallel; `price` is pure, so only spawn is needed.
pub fn price_all(lines: List<Line>) -> List<Money> with {spawn} {
  par.map(lines, (l) => price(l))
}

/// Counts priced lines across tasks; the Cell is the only shared state.
pub fn count(lines: List<Line>) -> Int with {mut, spawn} {
  let n = Cell.new(0)                          // creating a Cell is already mut
  par.each(lines, (l) => n.update((c) => c + 1))
  n.get()
}

/// Lines over a threshold, built with local mutation and frozen once.
pub fn expensive(lines: List<Line>, min: Money) -> List<Line> {
  var out = MutList.new<Line>()               // local working state, no effect
  for l in lines { if l.price.0 >= min.0 { out.push(l) } }
  out.freeze()
}

/// Persistent push returns a new list; the old one is unchanged.
pub fn with_line(lines: List<Line>, l: Line) -> List<Line> { lines.push(l) }
```

Go equivalent:

```go
func Both(ctx context.Context, a, b Url) Result[Pair[Body, Body], http.Error] {
	g, gctx := errgroup.WithContext(ctx)
	var ra, rb Result[Body, http.Error]
	g.Go(func() error { ra = http.Get(gctx, a); return ra.asErr() }) // Err cancels gctx
	g.Go(func() error { rb = http.Get(gctx, b); return rb.asErr() })
	_ = g.Wait() // the runtime scope re-panics a child panic here
	if !ra.ok { return Result[Pair[Body, Body], http.Error]{e: ra.e} }
	if !rb.ok { return Result[Pair[Body, Body], http.Error]{e: rb.e} }
	return Result[Pair[Body, Body], http.Error]{ok: true, v: Pair[Body, Body]{ra.v, rb.v}}
}

type Cell[T any] struct { mu sync.Mutex; v T }
func (c *Cell[T]) Update(f func(T) T) T { c.mu.Lock(); defer c.mu.Unlock(); c.v = f(c.v); return c.v }
```

TypeScript equivalent of `both`: `const [ra, rb] = await Promise.all([get(a), get(b)])`,
except that a rejected promise does not cancel its sibling.

## Rules

### Structured concurrency

1. **`spawn { … }`** requires the `spawn` effect in the enclosing function.
   It is an expression; its value is the block's tail expression. The union
   of effects inside it (body and every `task`) must be a subset of the
   enclosing declared set.
2. **`task expr`** starts `expr` as a child of the nearest enclosing `spawn`
   block and yields `Task<T>` where `T` is the type of `expr`. `task` must be
   lexically inside that block's body, not inside a closure or nested `fn`
   within it; `task` outside any `spawn` block is a compile error.
3. **`t.join() -> T`** waits for the task and returns its value. A second
   `join` on the same task returns the same value without waiting.
4. **Block exit joins everything.** When control leaves the block, by tail
   value, `return` or `?`, every unjoined task is joined and its value
   discarded. `Task<T>` cannot be returned, stored in a field or collection,
   or captured by a closure; it lives only inside its block.
5. **Failure.** If any task evaluates `?` on an `Err`, or the block body
   does, the siblings are cancelled, all are joined, and the first `Err` in
   start order is the block's value. If a task panics, the siblings are
   cancelled, joined, and the block re-panics with the original panic.
6. **Cancellation is cooperative.** It travels in the hidden context (section
   03, rule 18) and is observed at effect sites: `net`, `io`, `llm`,
   `decide`, `approve`, `time.sleep` and `join`. Pure code is never
   interrupted; a cancelled pure task runs to completion and its value is
   dropped.
7. **Capture rule.** A `spawn` block and its tasks may capture only `let`
   bindings of immutable types. Capturing a `var`, a `MutList`, `MutMap` or
   `MutSet`, or a record containing one, is a compile error. `Cell<T>` may be
   captured because its use is the `mut` effect.
8. **Nesting.** A `spawn` block may contain `spawn` blocks; an inner block
   completes before the outer statement continues. Cancellation of the outer
   block cancels inner tasks.
9. **Derived forms** (stdlib `std.par`, all defined with `spawn`/`task`):
   `par.map(xs, f) -> List<U> with {spawn}` keeps input order;
   `par.each(xs, f) -> () with {spawn}`;
   `with_timeout(d, f) -> Option<T> with {spawn, nondet}` returns `None` and
   cancels `f` after `d`. `f` is an unannotated function parameter, so its
   effects flow to the call site (section 03, rule 10).
10. **In workflows.** Inside `Workflow.run`, `spawn` lowers to deterministic
    runtime coroutines scheduled in task start order; rules 1–9 hold
    unchanged, and each task's effectful calls are journaled steps (section
    07). `Cell` cannot appear there because `mut` is forbidden in `run`.

### Mutation

11. **`let` vs `var`.** `let` binds once. `var` is a local mutable binding:
    `x = e` and `x += e` (and `-= *= /=`) are statements whose target is a
    `var` identifier; the type is fixed by the initialiser. There is no
    field or index assignment: records are updated with `Name { ..base, f:
    v }`, mutable collections through methods.
12. **Closures and `var`.** A closure may read a captured `var` (it sees the
    current value) but assigning to it from inside the closure is a compile
    error; use a `Cell`.
13. **`Cell<T>`** is the only shared-mutable primitive; every operation on
    it, creation included, is the `mut` effect. `Cell.new(v) -> Cell<T> with
    {mut}`, `get() -> T with {mut}`, `set(v: T) -> () with {mut}`,
    `update(f: fn(T) -> T with {}) -> T with {mut}` runs `f` under the cell's
    lock and returns the new value; `f` must be pure so no lock is held
    across an effect. Each operation is atomic; sequences are not.
14. **Invariants on cells.** If `T` carries an `inv`, `set` and `update`
    re-check it under `contracts: all`; a violation panics (section 05).
15. **Persistent collections are the default.** `List`, `Map`, `Set` are
    immutable with structural sharing: `push`, `set`, `remove` return a new
    value in O(log n) and leave the old one usable. Equality is structural.
16. **Mutable collections.** `MutList`, `MutMap`, `MutSet` mutate in place
    (`push`, `set`, `remove` return `()`), lower to Go slices and maps, and
    need no effect because they are locally owned working state. They may
    not cross a `spawn` boundary (rule 7), appear in a `pub` record field, or
    appear in the signature of a `pub fn`; a non-`pub` helper in the same
    module may take or return one.
17. **Conversion.** `.freeze()` turns a mutable collection into its
    persistent counterpart and `.thaw()` the reverse; both copy, cost O(n).
    `List.builder<T>()` is an alias for `MutList.new<T>()`, so the builder
    pattern is `MutList` plus one `.freeze()`.
18. **Performance flag.** Persistent maps cost roughly 2× a native map on
    insert (decisions §7); the default is kept until the collections
    benchmark runs. Code that builds a collection in a loop should use a
    mutable one and freeze it.

## Diagnostics

Capturing a `var` in a `spawn` block (rule 7):

```
orders/pricing.jip:31:28: error: `spawn` block captures the `var` binding `total`
  expected: only `let` bindings of immutable types, or a `Cell`
  found:    `var total: Int`, declared at orders/pricing.jip:29:7
  suggested edit: replace `var total = 0` with `let total = Cell.new(0)` and use `total.update((t) => t + x)`
```

`Cell.get` without `mut` (rule 13):

```
orders/pricing.jip:40:3: error: missing effect `mut` in `read_count`
  `read_count` is pure but calls `Cell.get`, which requires `mut`
  expected: with {mut}
  found:    (no effect set)
  suggested edit: add `mut` to the effect set of `read_count`
    38 | pub fn read_count(n: Cell<Int>) -> Int with {mut}
```

`task` outside `spawn` (rule 2):

```
orders/pricing.jip:52:12: error: `task` outside a `spawn` block
  suggested edit: wrap the statements that start and join the task in `spawn { … }` and add `spawn` to the effect set of `fetch_all`
```

## Grammar

```
SpawnExpr   ::= "spawn" Block
TaskExpr    ::= "task" Expr
Expr        ::= … | SpawnExpr | TaskExpr | Closure
Block       ::= "{" Stmt* Expr? "}"
Stmt        ::= "let" Pattern (":" Type)? "=" Expr
              | "var" Ident (":" Type)? "=" Expr
              | Ident AssignOp Expr
              | Expr
AssignOp    ::= "=" | "+=" | "-=" | "*=" | "/="
```
