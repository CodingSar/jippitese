# (b) Effect systems — Koka, Unison, Effekt, Flix, OCaml 5

Status: research report for `docs/decisions.md` deferred item "Effect system
surface (handlers vs. capability passing vs. row types)".

## Summary

- jippitese needs *tracking*, not *handling*. Every system studied pays most
  of its complexity (row duplicates, Boolean unification, continuations,
  evidence vectors, fibers) for user-defined handlers.
- Recommended surface: **simple effect sets** after the return type
  (`-> T ! {net, llm<smart>}`), omitted when pure; an **Effekt-style
  contextual rule** for closure parameters (their effects flow to the call
  site, no effect variable); explicit sets or plain set variables only where
  a closure is stored or returned.
- Effects are **fully erasable** in Go output. Instrumentation attaches
  through one hidden runtime context parameter, not handlers.
- No handlers in v1; if ever, tail-resumptive only.

## Findings

### Koka (row-polymorphic effects, handlers, evidence passing)

- Signatures: `fun square2(x: int) : console int`; `map : (xs: list<a>, f: (a) -> e b) -> e list<b>`; `while : (pred: () -> <div|e> bool, action: () -> <div|e> ()) -> <div|e> ()`. Absence of effects is `total`; built-ins `exn`, `div`, `ndet`, `console`, `io`, `st<h>`.
- Rows allow **duplicate labels** (`<div|<div|e>>` differs from `<div|e>`), justified as necessary to type handler elimination. Union and subtyping designs were rejected for breaking inference.
- Rows are tolerable only because effects are **inferred**; the tour offers `: _e` for authors "not interested in explicitly writing down" the effect.
- Handlers compile by generalized evidence passing: an implicit handler vector, constant-offset lookup, tail-resumptive `fun` operations as "virtual method calls"; general `ctl` operations need a monadic translation plus yield bubbling to reach C.

### Unison (abilities)

- Signatures: `List.map : (a ->{𝕖} b) -> [a] ->{𝕖} [b]`; `toOptional! : '{g, Abort} a ->{g} Optional a`. Ability polymorphism uses ordinary type variables; `A ->{} B` *forbids* abilities while an unannotated arrow is polymorphic: two meanings for "nothing written" is a reviewer hazard.
- Same cost as Koka (one variable per higher-order arrow) minus duplicates. Handlers match on `Request`; runtime needs continuations.

### Effekt (capability passing, contextual effect polymorphism)

- Signatures carry no effect variables: `def map[A, B](l: List[A]) { f: A => B }: List[B]`; `def loop { action: () => Unit / stop }: Unit`; a block parameter may itself demand capabilities, `{ f: (A) {Label} => Unit }`.
- Rule: a block typed `Unit / {}` "does not impose any requirements on its caller"; its effects are handled at the higher-order function's call site. Effect types "express which capabilities a computation requires from its context", which "impedes reasoning about purity".
- Price: functions are **second-class**; first-class use needs *boxes* carrying an explicit capability set (System C). Lift inference and the MLton backend were dropped; the LLVM backend was "still significantly slower".

### Flix (set-formula effects, primitive + algebraic effects)

- Signatures: `def copyFile(src: File, dst: File): Unit \ { FsRead, FsWrite, IO }`; `def map(f: a -> b \ ef, l: List[a]): List[b] \ ef`; `def >>(f: a -> b \ ef1, g: b -> c \ ef2): a -> c \ (ef1 + ef2)`; exclusion `listener: KeyEvent -> Unit \ (ef - Block)`. Pure is `{}` and may be omitted.
- Primitive effects (`IO`, `NonDet`, `FsRead`, `Http`, `Clock`, …) "cannot be handled and never go out of scope" and are "viral": a tracking-only subsystem inside a handler language.
- Set algebra needs Boolean unification (NP-hard; 1.4x inference slowdown in 2020, improved since). Top-level over-declaration is an **error** (`def foo(): Bool \ IO = true` fails); widening only for lambdas and trait instances.
- Unhandled algebraic effects may not cross `spawn`; primitive ones may.

### OCaml 5 (untyped effect handlers)

- "Effect handlers in OCaml do not provide effect safety; the compiler does not statically ensure that all the effects performed by the program are handled"; an unhandled `perform` raises `Effect.Unhandled` at runtime.
- Runtime: heap-allocated stack segments ("fibers"), one-shot continuations; multi-shot rejected as unsafe. Motivation is concurrency, not reasoning. Typed effects (2016 talk) have not shipped.
- Lesson: handlers without types give jippitese nothing; types without handlers give almost everything it asked for.

## Trade-offs

| Design | Signature cost for `map` | Explanation effort | Needs for tracking only |
|---|---|---|---|
| Row polymorphism (Koka) | one `e` on arrow and result, `<l|e>` extension syntax | high: rows, duplicates, open vs closed rows | duplicates and open rows are handler artefacts |
| Ability variables (Unison) | one `{g}` per arrow | medium; ambiguity of unannotated arrow | handlers/continuations unnecessary |
| Set formulas (Flix) | one `ef` per arrow, `+` to combine | medium; `-`, `~`, `&` rarely needed | union only; exclusion optional |
| Capability passing (Effekt) | none on parameters | low for parameters, high for first-class functions (boxes) and purity | second-class restriction is the whole cost |
| Untyped (OCaml 5) | none | low | provides no tracking |

With full signatures and local inference only, what matters is symbols per
higher-order signature and concepts the spec must explain. The contextual
rule wins for closures passed as arguments (the common case); Flix-style
sets win for stored closures because they stay explicit without boxes.
Combining them is sound: with no handlers nothing inside `map` can discharge
an effect, so call-site union (Effekt) and variable instantiation (Flix)
compute the same set.

Compile-to-Go: tracking-only effects are checked and erased, as Flix erases
primitive effects on the JVM. Handlers would force a runtime representation
Go lacks: continuation capture (OCaml fibers, Koka yield bubbling) or a
CPS/monadic rewrite of all effectful code. Goroutines can fake one-shot
continuations only at a scheduler switch per operation.

## Recommendations for jippitese

1. **Surface: effect sets, no handlers (v1).** Closed label set `io, net,
   mut, llm<role>, decide<role>, nondet, spawn, approve`, written after the
   return type; omitted means pure. This resolves the deferred item
   ("handlers vs. capability passing vs. row types") as sets plus a
   capability-style propagation rule. Row types rejected.
2. **Declared set is an upper bound.** Missing effect: error. Unused declared
   effect: `jip check` warning. Flix's top-level equality fights behaviour
   impls and stubs; §5 already needs `impl` effects ⊆ behaviour effects.
3. **Closure parameters: contextual propagation.** An unannotated
   function-typed parameter contributes its argument's effects to the call
   site; an annotation is a bound (`! {}` = must be pure). No effect
   variable in `map`, `fold`, `retry`, `with_timeout`.
4. **Stored or returned closures: explicit sets.** A function value in a
   return type, field or collection carries an effect set. Generic
   combinators may use set variables with union only (`e1 + e2`); no
   complement, intersection or subtraction. Effekt's box rule without the
   second-class vocabulary.
5. **Erasure plus one runtime context.** Effects leave no trace in Go types.
   Each function with a non-empty set gets a hidden first parameter (a
   `context.Context` wrapper carrying the `app.jip` telemetry/retry/timeout
   policy); closures capture it at creation; stdlib primitives (`net.get`,
   `llm.call`, `approve.request`) read it. Koka's evidence vector reduced to
   one static record. Refines decisions §4, does not reopen it.
6. **Future handlers, if ever:** tail-resumptive only (Koka `fun`), as an
   interface value in the context. No `ctl`-style resumptions.
7. **Decisions touched.** §3 "full signatures" is where Koka pushes back
   (rows were bearable only with inference); rule 3 removes that pressure,
   keep §3. §7 `spawn`: as in Flix, the spawned block's effects must be a
   subset of the enclosing declared set. §12 `extern` effects are unchecked
   trust assertions; track (i) must say so.

Illustrative signatures:

```jip
pub fn fetch_user(id: UserId) -> Result<User, HttpError> ! {net}

pub fn map<T, U>(xs: List<T>, f: fn(T) -> U) -> List<U>
// f's effects flow to the call site; map itself is pure

pub fn triage(t: Ticket) -> Result<Priority, LlmError> ! {llm<smart>, decide<fast>, approve}

pub fn par_sum(xs: List<Int>, f: fn(Int) -> Int ! {}) -> Int ! {spawn}
// f must be pure; spawn is the only effect of par_sum

pub fn compose<A, B, C, e1, e2>(f: fn(A) -> B ! e1, g: fn(B) -> C ! e2) -> fn(A) -> C ! e1 + e2
```

## Sources

- Koka book, "Effect types", "Polymorphic effects", "Effect handlers" (rendered): https://koka-lang.github.io/koka/doc/book.html ; source read via https://raw.githubusercontent.com/koka-lang/koka/master/doc/spec/tour.kk.md
- Leijen, "Koka: Programming with Row-Polymorphic Effect Types": https://arxiv.org/abs/1406.2061
- Leijen, Koka 2012 overview (row syntax, `while` type): https://www.microsoft.com/en-us/research/wp-content/uploads/2016/02/koka-2012-overviewkoka.pdf
- Xie et al., "Effect Handlers, Evidently" (ICFP 2020): https://www.microsoft.com/en-us/research/uploads/prod/2020/07/evidently.pdf
- Xie, Leijen, "Generalized Evidence Passing for Effect Handlers" (ICFP 2021): https://www.microsoft.com/en-us/research/uploads/prod/2021/08/genev-icfp21.pdf
- Unison docs, "Reading Unison type signatures": https://www.unison-lang.org/docs/fundamentals/values-and-functions/reading-type-signatures
- Unison docs, "Abilities and ability handlers": https://www.unison-lang.org/docs/language-reference/abilities-and-ability-handlers
- Unison docs, bibliography note on empty ability sets vs. Frank: https://unison-lang.org/docs/usage-topics/bibliography
- Effekt docs, "Lightweight effect polymorphism": https://effekt-lang.org/docs/concepts/effect-polymorphism
- Effekt stdlib `list.effekt` (signatures of `map`, `foreach`): https://raw.githubusercontent.com/effekt-lang/effekt/main/libraries/common/list.effekt
- Brachthäuser, Schuster, Ostermann, "Effects as Capabilities" (OOPSLA 2020): https://se.cs.uni-tuebingen.de/publications/brachthaeuser20effects/
- Brachthäuser et al., "Effects, Capabilities, and Boxes" (OOPSLA 2022): https://pl.cs.uni-tuebingen.de/publications/brachthaeuser22effects/
- Effekt, "A brief history of Effekt" (backends, lift inference dropped): https://effekt-lang.org/evolution and https://effekt-lang.org/docs/implementation/lift-inference
- Flix book, "Effect Polymorphism": https://doc.flix.dev/effect-polymorphism.html (source: https://raw.githubusercontent.com/flix/book/master/src/effect-polymorphism.md)
- Flix book, "Primitive Effects": https://doc.flix.dev/primitive-effects.html
- Flix book, "Effects and Handlers": https://doc.flix.dev/effects-and-handlers.html
- Madsen, van de Pol, "Polymorphic Types and Effects with Boolean Unification" (OOPSLA 2020): https://users-cs.au.dk/magnusm/papers/oopsla20/paper_b.pdf
- Lutze, Madsen et al., "With or Without You: Programming with Effect Exclusion" (ICFP 2023): https://dl.acm.org/doi/abs/10.1145/3607846
- OCaml manual 5.x, "Effect handlers" (effect safety, fibers, linearity): https://ocaml.org/manual/5.3/effects.html (source: https://raw.githubusercontent.com/ocaml/ocaml/trunk/manual/src/refman/extensions/effects.etex)
- White, "Effective Programming: Adding an Effect System to OCaml" (typed effects, 2016 talk): https://janestreet.com/tech-talks/effective-programming
- Kawahara, Kameyama, "One-shot algebraic effects as coroutines" (TFP 2020): https://www.logic.cs.tsukuba.ac.jp/~sat/pdf/tfp2020.pdf
