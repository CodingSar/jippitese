# 01 Lexical structure

Files, the `module` line, tokens, literals, comments, statement termination
and the `jip fmt` canonical form. Everything a tokenizer needs; nothing a
type checker needs.

## Example

`orders/billing/pricing.jip`:

```jip
module orders.billing            // mandatory first line: directory path from the app root
use std.json                     // `use` lines come directly after `module`
use orders.customers

/// Net price in minor units after a percentage discount.
pub fn net(price: Int, discount: Int) -> Int
  pre discount >= 0 && discount <= 100
{
  let factor = 100 - discount    // newline after a literal ends the statement
  price * factor / 100           // Int division truncates toward zero
}

/// One greeting per name, wrapped in literal braces.
pub fn greet(names: List<Str>) -> Str {
  let shown = names.map(fn(n) => "hi {n}!").join(", ")
  "{{{shown}}}"                  // `{{` and `}}` are literal braces around {shown}
}

/// Exponential-ish backoff; the type is Duration.
pub fn backoff(attempt: Int) -> Duration { 250ms * attempt + 1s }

/// Every literal form in one place.
pub fn samples() -> (Int, Float, Str, Str, Bytes, Char, Bool) {
  let n = 1_000_000 + 0xFF       // Int: 64-bit, `_` separators, hex
  let f = 2.5e3                  // Float: 64-bit, digits on both sides of `.`
  let s = "tab\tquote\" {n}"     // Str with escapes and interpolation
  let raw = r"C:\temp\{raw}"     // raw Str: no escapes, no interpolation
  let bytes = b"\x00\xffabc"     // Bytes: ASCII and \xNN only
  let c = '\n'                   // Char: one Unicode scalar value
  (n, f, s, raw, bytes, c, true)
}
```

Go equivalent of `net` and `greet`:

```go
package billing

import (
	"fmt"
	"strings"
)

// Net price in minor units after a percentage discount.
func Net(price int64, discount int64) int64 {
	factor := 100 - discount
	return price * factor / 100
}

// One greeting per name, wrapped in literal braces.
func Greet(names []string) string {
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("hi %s!", n))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
```

## Rules

1. **Encoding.** A `.jip` file is UTF-8 without a byte-order mark. Line
   endings are `\n`; `jip fmt` rewrites `\r\n`. Tabs are not permitted
   outside string and char literals.
2. **Module line.** The first non-blank, non-comment line of every file is
   `module a.b.c`. The module path is the file's directory path from the app
   root with `/` replaced by `.`; every file in `orders/billing/` begins with
   `module orders.billing`. A module is a directory; all its `.jip` files share
   the line. The scenarios file `orders/billing/scenarios.jip` is its own
   module, `module orders.billing.scenarios`. `app.jip` is config, not a
   module (`10-config.md`). A `module` line that does not match the directory
   is a compile error.
3. **Use lines.** `use path` lines follow the `module` line, before any item.
   Blank lines and comments may sit between them. The last path segment is
   the alias (`use std.json` brings `json.*` into scope; `use orders.customers`
   brings `customers.*`). `jip fmt` sorts them (`std.*` first, then
   dependency aliases, then app modules, each group alphabetically); an
   unused `use` is a `jip check` warning. Resolution is in `09-modules-and-context.md`.
4. **Identifiers.** `[A-Za-z_][A-Za-z0-9_]*`, ASCII only. A lone `_` is the
   discard pattern, never a name. Naming is enforced by the compiler, not
   just by convention: types, enums, variants, behaviours and type parameters
   are `PascalCase`; functions, parameters, bindings, fields, effects, modules
   and consts are `snake_case`. The checker uses this to tell a variant
   pattern (`None`) from a binding pattern (`n`).
5. **Keywords** are reserved and cannot be identifiers: `module use pub fn
   type enum impl behaviour for let var if else match while in return
   break continue spawn task with ret old forall exists scenario
   given when then any where via extern go unsafe pure app env const self
   Self llm decide approve true false`. `Self`, `self` and `ret` are keywords with a fixed meaning,
   not identifiers. `opaque`, `pre`, `post` and `inv` are contextual: `opaque`
   is recognised only between `pub` or `pub(module)` and `type` or `enum`
   (`09-modules-and-context.md`); `pre` and `post` only between a function
   signature and its body, `inv` only after a record declaration or inside an
   `impl` body (`05-contracts.md`). Elsewhere they are ordinary identifiers, so
   `http.post(…)` is a call, not a contract. Four more words are contextual
   in the same way: `default` before a value in `app.jip` (`10-config.md`),
   `every` and `cron` at the start of a `schedule` const value (`06-behaviours.md`), and
   `real_time` directly after `given` (`12-testing.md`).
6. **Int literals** are decimal (`42`, `1_000`) or hex (`0xFF`, `0xdead_beef`)
   and have type `Int` (signed 64-bit). `_` may appear between digits only. A
   literal outside the `Int` range is a compile error. No octal or binary
   literals in v0; a leading `0` followed by a digit is an error.
7. **Float literals** have digits on both sides of the `.` (`1.0`, not `1.`
   or `.5`) with an optional exponent (`2.5e3`, `1.0e-9`) and have type
   `Float` (IEEE 754 binary64). `1` is never a `Float`; write `1.0`.
8. **Str literals** are `"…"` on one line. Escapes: `\n \t \r \\ \" \0`,
   `\u{1F600}`. `{expr}` interpolates any expression whose type implements
   `Show` (`04-expressions-and-control.md`); `{{` and `}}` are literal
   braces. Inside `{…}` the lexer counts braces, so `{p.x + 1}` and
   `{m.get(k)}` work, but a `"` is not allowed inside an interpolation; bind
   the value first. Raw strings `r"…"` have no escapes and no interpolation,
   may span lines, and end at the first `"`.
9. **Bytes literals** `b"…"` have type `Bytes`; the content is ASCII plus
   `\xNN`, `\\`, `\"`, `\n`, `\t`, `\r`, `\0`; no interpolation.
10. **Char literals** `'c'` hold exactly one Unicode scalar value, with the
    Str escapes, and have type `Char`.
11. **Bool literals** are the keywords `true` and `false`.
12. **Duration literals** are an Int literal immediately followed by one of
    `ns us ms s m h` (`5s`, `100ms`, `48h`) and have type `Duration`. No
    whitespace, no fractions, no compound units: `1h30m` is written
    `1h + 30m`. A literal that overflows `Duration` (about 292 years) is an
    error.
13. **Comments.** `//` runs to end of line. `///` is a doc line: one line,
    placed directly above the item it documents, first sentence is the
    summary. Every `pub` item needs exactly one; non-`pub` items may have one.
    Two consecutive `///` lines are an error ("one line"). There are no block
    comments.
14. **Operators and punctuation** are exactly: `+ - * / % == != < <= > >=
    && || ! = += -= *= /= -> => ? . .. ..= :: , : | @ ( ) [ ] { }`. There
    is no `;`, `#`, `&`, `^`, `~`, `<<` or `>>`. Enum variants are
    qualified with `.` (`Status.Paid`). `::` appears in exactly one place:
    the turbofish of rule 15.
15. **Type arguments.** The lexer has no adjacency rule: `<` and `>` are
    always the tokens `<` and `>`, and the parser decides what they mean
    from position alone. In a type (`List<Int>`, `Result<T, E>`) `<` after a
    type name opens a type-argument list. In an expression an explicit type
    argument needs the turbofish `f::<T>(x)` (`json.decode::<Receipt>(s)`,
    `List.builder::<Line>()`); a bare `<` in an expression is always
    less-than, so `a<b` is a comparison whatever `a` is. `llm<smart>(…)` and
    `decide<fast>(…)` are not generic calls: `llm` and `decide` are keywords
    of `03-functions-and-effects.md` and the role in angle brackets is part
    of the effect name. Because there is no shift operator, `>>` is always
    two closing brackets (`List<Option<Int>>`).
16. **Statement termination** follows Go's rule, stated exactly. The lexer
    inserts a terminator at a newline when the last token on the line is an
    identifier, a literal (Int, Float, Str, Bytes, Char, Duration, `true`,
    `false`), a `)`, `]` or `}`, or one of the keywords `break`, `continue`,
    `return`. A newline after `?` also terminates. Any other line ending (an
    operator including `>`, `,`, `(`, `[`, `{`, `=>`, `->`, `=`, `.`, a
    keyword such as `else` or `with`) continues the statement. `>` is never
    a terminator: a signature whose return type ends in `>` keeps its `{`
    or `with` on the same line, which rule 17 and `jip fmt` already require. Comments and blank lines do not affect the rule.
    Consequences: `} else {` stays on one line; a method chain breaks after
    the `.` or inside the `(…)`, never before the `.`; `return` alone on a
    line returns `()`. `;` is never written and is a syntax error.
17. **Contract and header lines** (`pre`, `post`, `inv`) each end at a
    newline by rule 16 and the body `{` follows on its own line; with no
    contract lines the `{` stays on the signature line (`00-language-card.md`).
18. **Canonical form.** `jip fmt` has no options and produces: 2-space
    indentation, no tabs, one trailing newline, one blank line between items,
    spaces around binary operators and `=>`/`->`, no space before `(` in calls
    or after `(`/`[`/`{` in single-line lists, `} else {` and `} else if … {`
    on one line, a trailing comma after the last element of every list that
    spans lines (arguments, parameters, list literals, record fields, enum
    variants, match arms), none in single-line lists, tail `return x`
    rewritten to `x`. A list goes one element per line when the source had
    any newline inside it; `jip fmt` never wraps or joins lines on its own.

## Diagnostics

Missing `module` line:

```text
error[E0100]: missing `module` line
  --> orders/billing/pricing.jip:1:1
   |
 1 | use std.json
   | ^^^^^^^^^^^^ expected `module orders.billing` before this line
   = note: this file is in `orders/billing/`
   = help: insert `module orders.billing` at line 1
```

The shape and code families are fixed by `13-tooling-and-diagnostics.md`;
a `module` line that disagrees with its directory is E0101 there.

## Grammar

Lexical level; `TERM` is the inserted terminator of rule 16.

```
File        ::= ModuleDecl UseDecl* Item*
ModuleDecl  ::= "module" ModulePath NEWLINE
UseDecl     ::= "use" ModulePath NEWLINE
ModulePath  ::= Ident ("." Ident)*

Ident       ::= letter (letter | digit | "_")* /* not a keyword, not "_" */
letter      ::= "A" … "Z" | "a" … "z" | "_"
digit       ::= "0" … "9"

Literal     ::= IntLit | FloatLit | StrLit | RawStrLit | BytesLit
            | CharLit | DurationLit | "true" | "false"
IntLit      ::= decimal | "0x" hexdigit (hexdigit | "_")*
decimal     ::= digit (digit | "_")*
FloatLit    ::= decimal "." decimal (("e" | "E") ("-" | "+")? decimal)?
StrLit      ::= '"' (strchar | escape | interp | "{{" | "}}")* '"'
interp      ::= "{" Expr "}" /* no '"' inside */
RawStrLit   ::= 'r"' (any except '"')* '"'
BytesLit    ::= 'b"' (asciichar | byteescape)* '"'
CharLit     ::= "'" (strchar | escape) "'"
DurationLit ::= decimal ("ns" | "us" | "ms" | "s" | "m" | "h")

Comment     ::= "//" (any except newline)*
DocLine     ::= "///" (any except newline)*
NEWLINE     ::= newline /* only after the tokens of rule 16 */
```
