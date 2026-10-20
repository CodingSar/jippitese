# 04 Expressions and control

Bindings, blocks, `if`, `match`, loops, `?`, operators, interpolation,
records, method calls, closures and `panic`. Function signatures, effect sets
and how closures carry effects are in `03-functions-and-effects.md`.

## Example

```jip
module shop.inventory
use std.json

/// Why a restock failed.
pub enum StockError { Parse(json.Error), Negative(Int) }

/// Restocks from a JSON payload; totals every positive delta.
pub fn restock(raw: Str, levels: Map<Sku, Int>) -> Result<Map<Sku, Int>, StockError> {
  let deltas = json.decode::<List<Delta>>(raw)?     // json.Error lifts into StockError.Parse
  var out = levels                                  // `var`: the binding can be reassigned
  for d in deltas {
    if d.qty < 0 { return Err(Negative(d.qty)) }    // early exit
    if d.qty == 0 { continue }
    let current = out.get(d.sku).unwrap_or(0)
    out = out.put(d.sku, current + d.qty)           // persistent map: `put` returns a new map
  }
  Ok(out)
}

/// Classifies a level for the dashboard.
pub fn label(level: Option<Int>, max: Int) -> Str {
  match level {
    None => "unknown",
    Some(0) => "empty",
    Some(n) if n >= max => "full ({n}/{max})",
    Some(n @ 1 | n @ 2) => "critical: {n}",         // or-pattern, both sides bind `n`
    Some(n) => "ok: {n}",
  }
}

/// Sum of 0..n, with a block used as a value.
pub fn tri(n: Int) -> Int {
  let total = {
    var acc = 0
    for i in 0..=n { acc += i }                     // inclusive range
    acc
  }
  total
}
```

Go equivalents of `match`, `?` and `for`:

```go
func Label(level Option[int64], max int64) string {
	switch {                                   // match: a switch with no default
	case !level.ok:
		return "unknown"
	case level.v == 0:
		return "empty"
	case level.v >= max:
		return fmt.Sprintf("full (%d/%d)", level.v, max)
	case level.v == 1 || level.v == 2:
		return fmt.Sprintf("critical: %d", level.v)
	case level.ok:                             // last arm; no default is emitted
		return fmt.Sprintf("ok: %d", level.v)
	}
	panic("unreachable")
}

deltas, err := json.Decode[[]Delta](raw)   // `?`: check, wrap, return
if err != nil {
	return Result[Map[Sku, int64], StockError]{e: StockError{tag: parseTag, parse: err}}
}

for i := int64(0); i <= n; i++ {           // for i in 0..=n
	acc += i
}
```

```ts
switch (level.kind) {                      // match on a discriminated union
  case "None": return "unknown";
  case "Some": return level.value === 0 ? "empty" : `ok: ${level.value}`;
}
```

## Rules

1. **`let`** binds an immutable name: `let x = e` or `let x: T = e`. The
   pattern may be a tuple or record pattern when it cannot fail
   (`let (a, b) = pair`); refutable patterns need `match`. Reassigning a
   `let` is a compile error.
2. **`var`** binds a mutable name. `x = e` reassigns; `+= -= *= /=` are
   shorthand for `x = x op e` and are statements, not expressions. The type
   is fixed at the first binding. A `var` cannot be captured by a `spawn`
   block or `task`; a closure may read it (it sees the value at the time
   the closure is created, rule 17) but may not assign it.
3. **Shadowing.** A name may be re-bound only in a nested block. Two bindings
   of the same name in one block, or a binding that shadows a parameter at
   the top level of the function body, are compile errors. Match-arm and
   closure parameters open a nested scope.
4. **Blocks** `{ … }` are expressions. Statements are separated by newlines
   (`01-lexical-structure.md`); the final expression is the block's value. A
   block that ends in a statement (`let`, assignment, `while`, `for`) has
   type `()`.
5. **`if c { a } else { b }`** is an expression when both branches are
   present and have the same type; `else if` chains. The condition is a
   `Bool` with no surrounding parentheses. A record literal may not appear
   unparenthesised anywhere in an `if` or `while` condition, a `for`
   iterable or a `match` scrutinee, because its `{` would be read as the
   block: `if x == Point { x: 1 } {` is a parse error (E0451) and is written
   `if x == (Point { x: 1 }) {`. An `if` without `else` has type `()` and
   may not be used as a value. There is no ternary operator.
6. **`match e { pat => expr, … }`** evaluates arms top to bottom and takes
   the first that matches; there is no fallthrough. All arm results share one
   type. Arms are separated by commas with a trailing comma on multi-line
   matches. An arm body is an expression or a block.
7. **Patterns.** Literal (`0`, `"x"`, `'c'`, `true`; never `Float`), variant
   with positional (`Some(x)`, `Shape.Cube(n)`) or named fields
   (`Box { w, h }`, `Box { w, .. }`), record (`P { x, y: 0 }`, `P { x, .. }`),
   tuple (`(a, _)`), wildcard `_`, binding `x`, at-binding `x @ pat`,
   or-pattern `p1 | p2` (every alternative binds the same names with the same
   types), guard `pat if cond`. A `PascalCase` identifier is a variant (error
   when the type has none by that name); a `snake_case` identifier is a
   binding. `..` is allowed once, last, in record and named-variant patterns.
8. **Exhaustiveness.** The arms must cover every value of the scrutinee's
   type; guarded arms do not count toward coverage. An arm that can never
   match is a `jip check` warning.
9. **`while c { … }`** and **`for x in xs { … }`** are statements of type
   `()`. `for` iterates `List`, `Set`, `Map` (as `(k, v)` tuples), their
   mutable variants, and ranges. `a..b` is exclusive, `a..=b` inclusive; both
   are `Int` ranges and have type `Range<Int>`; `r.contains(x)` is
   membership, and on a literal range it compiles to two comparisons. `break` and `continue` apply to the
   innermost loop; there are no labels and no loop values.
10. **`return e`** exits the function with `e`; bare `return` returns `()`.
    A tail `return x` compiles and `jip fmt` rewrites it to `x`. Inside a
    closure, `return` exits the closure.
11. **`?` on `Result`.** In a function (or closure) returning
    `Result<_, E2>`, `r?` on a `Result<T, E1>` yields `T` on `Ok` and returns
    `Err(e)` early on `Err`. It compiles when `E1 == E2`, or when `E2` is an
    enum with exactly one variant whose single payload type is `E1`, in which
    case `?` wraps the error in that variant (`http.Error` into
    `BillError.Net`). Otherwise it is a compile error that names the mismatch
    and suggests `.map_err(fn(e) => …)`. The lift is decided per call site
    from the static types; it never searches through nested enums and never
    converts through `Show` or any other behaviour. The same rule, unchanged,
    applies inside `Workflow.run` (`07-agents-and-workflows.md` rule 9).
12. **`?` on `Option`.** In a function returning `Option<_>`, `o?` yields the
    payload or returns `None`. `?` on an `Option` in a `Result` function
    suggests `.ok_or(err)`; `?` on a `Result` in an `Option` function suggests
    `.ok()`. `?` anywhere else is an error. Inside a `spawn` block, `?` leaves
    the block with the error and cancels its sibling tasks
    (`08-concurrency-and-mutation.md`).
13. **Operators**, highest precedence first. Binary operators are
    left-associative except comparison, which does not chain
    (`a < b < c` is an error).

    | Level | Operators | Operands |
    |---|---|---|
    | postfix | `.field` `.method(…)` `(…)` `[i]` `?` | |
    | unary | `!` `-` | `Bool`; `Int`, `Float`, `Duration` |
    | multiplicative | `*` `/` `%` | same numeric type; `%` on `Int` only; `Duration * Int`, `Duration / Int` |
    | additive | `+` `-` | same numeric type or `Duration`; `+` also concatenates `Str` |
    | comparison | `== != < <= > >=` | `==`/`!=` per `02-types.md` rule 16; ordering needs `Ord` |
    | and | `&&` | `Bool`, short-circuit |
    | or | `\|\|` | `Bool`, short-circuit |
    | range | `..` `..=` | `Int` |

    `Int` `+`, `-`, `*`, unary `-` and the compound forms trap on overflow
    with a `panic` in every build (`02-types.md` rule 1; the emitted Go is
    one compare per operation); `checked_*` and `wrapping_*` are the
    explicit alternatives. `/` and `%` on `Int` truncate toward zero and
    panic on a zero divisor. There are no bitwise or shift operators
    in v0; use `Int.and(a, b)`, `Int.shl(a, n)` and friends from the stdlib.
    Assignment is a statement. No operator overloading.
14. **Interpolation.** `"text {expr}"` evaluates `expr`, which must have a
    type implementing `Show`, and splices its `show()` output. `{{` and `}}`
    produce literal braces. There are no format specifiers; call a stdlib
    formatting function inside the braces (`{fmt.fixed(x, 2)}`).
15. **Records.** `Name { a: 1, b }` constructs; `b` alone means `b: b`.
    `Name { ..base, a: 1 }` copies `base` then applies the listed fields;
    `..base` must come first and each field may appear once. Every field of
    the type must be supplied unless `..base` is present. Construction runs
    the type's `inv` checks (`05-contracts.md`). Outside its module an
    `opaque` type cannot be constructed or spread (`02-types.md` rule 18).
16. **Method and field access.** `x.f(y)` calls method `f` on `x`'s type
    (`impl` methods and stdlib methods); `x.field`, `t.0` and `m.0` read a
    record field, tuple element and newtype payload. There is no uniform
    function-call syntax: `f(x)` never resolves to a method.
17. **Closures.** `fn(x) => expr`, `fn(x, y) => { … }`, `fn() => expr`.
    Parameter types come from context or from annotations
    `fn(x: Int) => …`; the result type is inferred. The leading `fn` is what
    separates a closure from a parenthesised expression or tuple: `(a, b)`
    is always a tuple. Closures capture `let` and `var` bindings by value at
    the point of creation; assigning to a captured `var` inside the closure
    is a compile error (rule 2), so a closure never observes a later
    reassignment. Their effects follow the rules in
    `03-functions-and-effects.md`.
18. **`panic("msg")`** aborts the program with a message and a `.jip`
    stack trace. It is for bugs and contract violations only, never for
    expected failures (those are `Result`), and cannot be caught in user
    code. Its result unifies with any type, so `None => panic("unreachable")`
    type-checks inside a `match`.

## Diagnostics

`?` on a mismatched error type, in `fn load(raw: Str) -> Result<Config, Str>`:

```text
error[E0430]: `?` cannot convert the error type
  --> shop/inventory/config.jip:7:38
   |
 7 |   let cfg = json.decode::<Config>(raw)?
   |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^ error type `json.Error`
   = note: `load` returns `Result<Config, Str>`; `?` lifts `json.Error` only into `Str`
           or into an enum with exactly one variant holding `json.Error`
   = help: map the error first: `json.decode::<Config>(raw).map_err(fn(e) => "bad config: {e}")?`
```

Record literal in a condition (rule 5):

```text
error[E0451]: record literal in a condition must be parenthesised
  --> shop/inventory/origin.jip:5:11
   |
 5 |   if p == Point { x: 0, y: 0 } {
   |           ^^^^^^^^^^^^^^^^^^^^ `{` here would start the `if` block
   = help: write `if p == (Point { x: 0, y: 0 }) {`
```

Reassigning a `let`:

```text
error[E0440]: cannot assign to immutable binding `totals`
  --> shop/inventory/report.jip:12:5
   |
12 |     totals = totals.put(d.sku, current + d.qty)
   |     ^^^^^^ `totals` was bound with `let` at line 8
   = help: change line 8 to `var totals = …`, or bind a new name: `let next = …`
```

## Grammar

```
Block       ::= "{" (Stmt NEWLINE)* Expr? "}"
Stmt        ::= "let" Pattern (":" Type)? "=" Expr
                | "var" Ident (":" Type)? "=" Expr
                | Ident ("=" | "+=" | "-=" | "*=" | "/=") Expr
                | "while" CondExpr Block
                | "for" Pattern "in" CondExpr Block
                | "break"
                | "continue"
                | "return" Expr?
                | Expr

Expr        ::= OrExpr ((".." | "..=") OrExpr)?
OrExpr      ::= AndExpr ("||" AndExpr)*
AndExpr     ::= CmpExpr ("&&" CmpExpr)*
CmpExpr     ::= AddExpr (("==" | "!=" | "<" | "<=" | ">" | ">=") AddExpr)?
AddExpr     ::= MulExpr (("+" | "-") MulExpr)*
MulExpr     ::= UnaryExpr (("*" | "/" | "%") UnaryExpr)*
UnaryExpr   ::= ("!" | "-")? PostfixExpr
PostfixExpr ::= Primary ("." Ident ("::" TypeArgs)? Args? | "." IntLit | Args | "[" Expr "]" | "?")*
Args        ::= "(" (Expr ("," Expr)* ","?)? ")"
CondExpr    ::= Expr /* same grammar; a RecordExpr inside it must be parenthesised (rule 5) */
Primary     ::= Literal
                | Ident ("::" TypeArgs)? /* turbofish; "::" TypeArgs must be followed by Args */
                | "(" Expr ("," Expr)* ")"
                | "(" ")"
                | "[" (Expr ("," Expr)* ","?)? "]"
                | RecordExpr
                | Block
                | IfExpr
                | MatchExpr
                | Closure
                | "spawn" Block
                | "task" Expr
                | "panic" "(" Expr ")"
RecordExpr  ::= TypePath "{" (".." Expr ",")? (FieldInit ("," FieldInit)* ","?)? "}"
FieldInit   ::= Ident (":" Expr)?
IfExpr      ::= "if" CondExpr Block ("else" (IfExpr | Block))?
MatchExpr   ::= "match" CondExpr "{" Arm ("," Arm)* ","? "}"
Arm         ::= Pattern ("if" Expr)? "=>" (Expr | Block)
Closure     ::= "fn" "(" (Param ("," Param)*)? ")" "=>" (Expr | Block)
Param       ::= Ident (":" Type)?

Pattern     ::= AltPattern ("|" AltPattern)*
AltPattern  ::= Literal
                | "_"
                | Ident ("@" AltPattern)? /* snake_case: binding */
                | TypePath ("(" Pattern ("," Pattern)* ")" | "{" FieldPats "}")?
                | "(" Pattern ("," Pattern)* ")"
FieldPats   ::= (FieldPat ("," FieldPat)*)? ("," "..")?
FieldPat    ::= Ident (":" Pattern)?
```
