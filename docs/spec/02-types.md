# 02 Types

Primitives, records, enums, newtypes, aliases, `Option`/`Result`, tuples,
collections, `Cell`, `Task`, `Topic`, generics and visibility. Functions and
effects are in `03-functions-and-effects.md`; behaviours in `06-behaviours.md`.

## Example

```jip
module shop.catalog

/// Money in minor units; distinct from Int, no implicit conversion.
pub type Money(Int)

/// Customer-visible id; an alias, interchangeable with Int.
pub type Id = Int

/// A product with a non-negative price.
pub type Product { id: Id, name: Str, price: Money, tags: Set<Str> }
  inv price.0 >= 0

/// Shape of a product's packaging.
pub enum Shape { Flat, Cube(Int), Box { w: Float, h: Float } }

/// Why a lookup failed.
pub enum CatalogError { NotFound(Id), Invalid(Str) }

/// Opaque handle: outside this module the body is not visible.
pub opaque type Session { token: Str }

/// Find a product; None when absent.
pub fn find(ps: Map<Id, Product>, id: Id) -> Option<Product> { ps.get(id) }

/// Price after a surcharge; Money arithmetic goes through `.0`.
pub fn with_fee(p: Product, fee: Money) -> Product {
  Product { ..p, price: Money(p.price.0 + fee.0) }
}

/// Largest value under any ordering key.
pub fn largest<T, K: Ord + Show>(xs: List<T>, key: fn(T) -> K) -> Result<T, CatalogError> {
  match xs.fold(None, fn(best, x) => match best {
    None => Some(x),
    Some(b) => if key(x) > key(b) { Some(x) } else { best },
  }) {
    Some(t) => Ok(t),
    None => Err(Invalid("empty list")),
  }
}
```

Go and TypeScript equivalents of the record, enum, `Option` and newtype:

```go
type Money int64                       // newtype: distinct named type

type Product struct {                  // record
	ID    int64
	Name  string
	Price Money
	Tags  Set[string]
}

type shapeTag uint8                    // enum: flattened tagged struct (small, non-recursive)
type Shape struct {
	tag  shapeTag
	cube int64
	boxW float64
	boxH float64
}

type Option[T any] struct {            // Option<T>: no pointer, no allocation
	v  T
	ok bool
}
```

```ts
type Money = number & { readonly __brand: "Money" };     // newtype as a branded type
type Shape = { kind: "Flat" } | { kind: "Cube"; n: number } | { kind: "Box"; w: number; h: number };
type Option<T> = { kind: "Some"; value: T } | { kind: "None" };
```

## Rules

1. **Primitives.** `Int` (signed 64-bit; `+ - *` and the compound forms
   trap on overflow in every build, release included, as a `panic`
   (`04-expressions-and-control.md` rule 13); `checked_add`, `checked_sub`,
   `checked_mul` return `Option<Int>` and `wrapping_add`, `wrapping_sub`,
   `wrapping_mul` wrap), `Float` (binary64), `Bool`, `Str` (immutable
   UTF-8), `Bytes` (immutable byte sequence), `Char` (one Unicode scalar),
   `Duration` (signed nanoseconds, from `5s`-style literals), and the unit
   type `()` with the single value `()`.
2. **No implicit conversions**, anywhere, including `Int` to `Float`, alias
   to newtype, or `T` to `Option<T>`. Conversions are explicit stdlib calls:
   `n.to_float()`, `f.to_int()` (truncates toward zero), `Int.parse(s)`,
   `Some(x)`, `Money(n)`.
3. **Records.** `type Name { field: Type, … }` declares a product type. Fields
   are accessed with `.field`, constructed with `Name { field: value }`, and
   updated with `Name { ..base, field: value }` (`04-expressions-and-control.md`).
   `inv` lines directly below the declaration are invariants over the fields,
   checked at construction at the level set in `app.jip` (`05-contracts.md`).
   No positional construction, no default field values, no guarantees about
   field order in memory.
4. **Enums.** `enum Name { A, B(Int), C { w: Float, h: Float } }` declares a
   sum type with payload-less, positional-payload and named-payload variants.
   Variants are qualified as `Name.A`; the bare name works where the expected
   type is known. `match` on an enum must be exhaustive (rule 17). Recursive
   enums (`enum Tree { Leaf, Node(Tree, Int, Tree) }`) are allowed.
5. **Newtypes.** `type Money(Int)` declares a type distinct from its payload.
   Construct with `Money(5)`, read the payload with `.0`, destructure with
   `Money(n)`. `Money` has no arithmetic and inherits no behaviour impl from
   `Int` except structural `==` and `Show` (rule 16); `impl Ord for Money` is
   written explicitly when needed.
6. **Aliases.** `type Id = Int` introduces another name for the same type;
   values flow both ways with no conversion. An alias cannot carry `inv` and
   cannot be the target of an `impl`.
7. **`Option` and `Result`** are ordinary enums in the prelude, with no
   special syntax beyond `?` and the bare variant names:
   ```jip
   pub enum Option<T> { Some(T), None }
   pub enum Result<T, E> { Ok(T), Err(E) }
   ```
   There is no `null`; a possibly-missing value is `Option<T>`. Error values
   are any type `E`; stdlib errors are enums.
8. **Tuples.** `(A, B)` is a type and `(a, b)` a value; elements are read as
   `.0`, `.1`. There are no one-element tuples; `(x)` is `x`. `()` is the
   unit type and value.
9. **Persistent collections.** `List<T>` (`[1, 2]`), `Map<K, V>`
   (`Map.of((k, v), …)`) and `Set<T>` (`Set.of(1, 2)`) are immutable: every
   operation returns a new collection sharing structure with the old one.
   `Map` keys and `Set` elements must implement `Hash` (which implies `Eq`).
   `xs[i]` on a `List` panics when out of range; `xs.get(i)` returns
   `Option<T>`. `Map` has no `[]`; use `m.get(k)`.
10. **Mutable collections.** `MutList<T>`, `MutMap<K, V>`, `MutSet<T>` lower
    to native Go slices and maps and mutate in place (`push`, `set`,
    `remove`). Convert with `xs.thaw()` and `m.freeze()`, both O(n) copies.
    Mutating one that the current function created or received as an
    argument needs no effect, because nothing else can observe it: a mutable
    collection or a `var` binding cannot be captured by a `spawn` block or
    passed to a `task`, and the only way to share mutable state between tasks
    is a `Cell<T>` (`08-concurrency-and-mutation.md`). They may be stored in
    non-`pub` records and passed to or returned from non-`pub` functions of
    the same module; a `pub` field or a `pub fn` signature may not mention
    one (08, rule 16).
11. **`Cell<T>`** is the single shared-mutation primitive. `Cell.new(v)`,
    `c.get()`, `c.set(v)` and `c.update(f)` all carry the `mut` effect; `inv`
    on `T` is re-checked after each write under `contracts: all`. A `Cell` is
    never structurally compared (rule 16) and is forbidden inside
    `Workflow.run`.
12. **`Task<T>`** is the handle returned by `task expr` inside a `spawn`
    block; `t.join()` yields the `T`. A task cannot outlive its block.
13. **`Topic<T>`** is the stdlib pub/sub channel used to chain `Worker`
    impls; it is a type, not a behaviour (`06-behaviours.md`).
14. **Generics.** Type parameters are declared on functions, records, enums
    and behaviours: `fn largest<T, K: Ord>(…)`, `type Pair<A, B> { a: A, b: B }`.
    A constraint is a behaviour; several are joined with `+`
    (`K: Ord + Show`). Unconstrained parameters admit only `==`, `Show` and
    passing through. An inherent `impl T { … }` (no `for`) adds methods and
    constructors to a type declared in the same module
    (`03-functions-and-effects.md` rule 4). Behaviours declare associated types (`type Input`), an
    `impl` fixes them (`type Input = Order`), and generic code names them as
    `T.Input` or `Self.Input`. No higher-kinded types: a parameter stands for
    a type, never for a type constructor like `List`.
15. **No `any`.** Every value has one static type known at compile time. An
    `extern` outside the allowlist counts as an escape (`11-extern.md`).
16. **Structural equality.** `==` and `!=` are defined on every type except
    function types and `Cell<T>`, and compare by value, recursively, through
    records, enums, newtypes, tuples and collections. `Float` follows IEEE
    754 (`nan == nan` is `false`). Comparing two function types or cells is a
    compile error. `Show` (used by interpolation) is likewise available on all
    types except function types and `Cell`.
17. **Exhaustive `match`.** A `match` over an enum, `Bool`, tuple or `Option`/
    `Result` must cover every value; the compiler reports the missing
    constructors.
18. **Visibility.** `pub type T { … }` and `pub enum E { … }` export the
    type and its body: other modules may construct, destructure, spread and
    read fields or match variants. `pub opaque type T { … }` and
    `pub opaque enum E { … }` export the name only: outside the declaring
    module the value can be named, passed, compared and shown, but not
    constructed, destructured, spread or matched; the module's functions are
    its whole API, and the interface file prints it as `pub opaque type T`
    with no body. `pub(module)` exports an item to the parent module (the
    path minus its last segment) and every module below it, so
    `orders.billing.scenarios` sees `pub(module)` items of `orders.billing`.
    These rules belong to `09-modules-and-context.md`; this section only
    applies them to types.
19. **Lowering** (`docs/decisions.md` §2), stated so a reviewer can predict
    the Go:

    | jippitese | Go |
    |---|---|
    | `Int Float Bool Str Bytes Char Duration ()` | `int64 float64 bool string []byte rune int64 struct{}` |
    | record, newtype, tuple | `struct` (tuple fields `F0`, `F1`), named type |
    | enum, small and non-recursive | one struct with `tag uint8` and all variant fields |
    | enum, large or recursive | sealed interface, one struct per variant |
    | `Option<T>` / `Result<T, E>` | `struct{v T; ok bool}` / `struct{ok bool; v T; e E}`, no allocation |
    | `List Map Set` | stdlib RRB vector, HAMT map and set |
    | `MutList MutMap MutSet` | `[]T`, `map[K]V`, `map[T]struct{}` |
    | `Cell<T>` | runtime struct with a mutex |
    | type parameter | Go type parameter on a free function, never on a method |
    | behaviour `impl` | a dictionary: a struct of function values plus the witnesses of its associated types, built once at `init` (`06-behaviours.md` rule 15) |

## Diagnostics

Non-exhaustive match:

```text
error[E0420]: non-exhaustive match on `Shape`
  --> shop/catalog/ship.jip:14:3
   |
14 |   match s {
   |   ^^^^^^^ arms cover `Flat`, `Cube(_)`; missing `Box { .. }`
   = note: `match` must cover every variant of `Shape`
   = help: add an arm `Box { w, h } => …` before the closing `}` on line 17
```

`Int` where `Money` is expected:

```text
error[E0402]: type mismatch
  --> shop/catalog/fees.jip:9:22
   |
 9 |   let p2 = with_fee(p, 250)
   |                        ^^^ expected `Money`, found `Int`
   = note: parameter `fee` of `with_fee` is `Money`; there is no implicit conversion from `Int`
   = help: wrap the value: `with_fee(p, Money(250))`
```

## Grammar

```
Item        ::= DocLine? Visibility? "opaque"? (TypeDecl | EnumDecl | FnDecl | ConstDecl
                | ImplDecl | BehaviourDecl | ExternDecl)
Visibility  ::= "pub" ("(" "module" ")")? /* "opaque" only with pub type / pub enum */

TypeDecl    ::= "type" TypeName TypeParams? TypeBody NEWLINE Contract*
TypeBody    ::= "{" FieldList "}" /* record */
                | "(" Type ")" /* newtype */
                | "=" Type /* alias */
FieldList   ::= (Field ("," Field)* ","?)?
Field       ::= Ident ":" Type
EnumDecl    ::= "enum" TypeName TypeParams? "{" Variant ("," Variant)* ","? "}" NEWLINE
Variant     ::= TypeName ("(" Type ("," Type)* ")" | "{" FieldList "}")?
Contract    ::= "inv" Expr NEWLINE /* 05-contracts.md adds pre, post */

TypeParams  ::= "<" TypeParam ("," TypeParam)* ">"
TypeParam   ::= TypeName (":" Bound ("+" Bound)*)?
Bound       ::= TypePath

Type        ::= TypePath TypeArgs? /* Int, Money, List<T>, customers.Id */
                | "(" (Type ("," Type)*)? ")" /* tuple or unit */
                | "fn" "(" (Type ("," Type)*)? ")" "->" Type EffectSet?
                | TypeName "." TypeName /* associated type: Self.Input */
TypePath    ::= (Ident ".")? TypeName
TypeArgs    ::= "<" Type ("," Type)* ">"
TypeName    ::= Ident /* PascalCase */
```
