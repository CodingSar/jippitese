# (f) Contracts and property testing

## Summary

Mature contract systems converge on four clauses: `pre`, `post` (with `old`),
invariant, and a frame (what may change). The runtime cost that matters is
`old` snapshots and invariant re-checks, not predicates, so every production
system ships a per-kind on/off policy (Eiffel levels, Ada `Assertion_Policy`,
Dafny ghost erasure). Contract-to-test derivation is a solved pattern (Eiffel
AutoTest, Clojure `spec`, `deal`, icontract-hypothesis): generate inputs from
types, narrow by `pre`, call, use `post` and invariants as the oracle, shrink.
Contracts break on unbounded quantifiers, side effects inside assertions, and
frames/`old` over mutable heaps. jippitese's immutable values and effect types
remove the last two, so its contracts can stay tiny. No system unifies BDD
scenarios with contracts; the gap is small and worth closing.

## Findings

**Dafny.** `requires`/`ensures`/`modifies`/`reads`/`old(e)`; `old` affects
only heap dereferences, not locals or out-params, a documented confusion
source. Contracts and `assert` are ghost ("ignored by the compiler"): free in
production, never checked at run time. The escape hatch is `expect e`:
compiled, halts on failure, treated as `assume` by the verifier, recommended
for `{:extern}` wrappers and `{:test}` methods run by `dafny test`;
`--test-assumptions` (experimental) inserts such checks. Frames are the main
usability cost: methods list everything they modify, functions everything
they read, and loops inherit the method's `modifies`, "the first place to
look when an assertion after a loop fails".

**Eiffel.** `require`/`ensure`/`invariant`, `old`, `check`. Monitoring is a
per-class option with five levels (`no`, `require`, `ensure`, `invariant`,
`all`); failure raises an exception. Assertions must be side-effect free, but
the compiler cannot enforce it. `old x` captures a reference, not the object
graph. **AutoTest** takes a list of classes, generates objects and arguments
randomly, uses contracts as the oracle, minimises failures and keeps them as
regression tests; on EiffelBase/Gobo it found 95 faults, with stateful
(sequence) testing adding 65 more (+68%). About 30% of faults appear in the
first 10 minutes, with high variance across seeds.

**SPARK/Ada 2012.** `Pre`, `Post`, `Contract_Cases` (disjoint, complete
guard => consequence pairs; runtime check that exactly one guard holds),
`Global`/`Depends` (data-flow frames), `Ghost` code. `X'Old` is an
unconditional copy at entry, so it must apply to a *name* and is illegal in
potentially unevaluated positions (`A(J)'Old` under an `if` is rejected;
write `A'Old(J)`). Ghost code is erased by `Assertion_Policy (Ghost =>
Ignore)` and the compiler checks that non-ghost code never depends on it.
AdaCore's production advice (Gem #149): keep `Pre => Check`, drop `Post`,
since one call under `-gnata` can trigger several checks. Quantifiers are
executable only over arrays and formal containers, so they stay finite.

**Hypothesis / QuickCheck lineage.** QuickCheck shrinks by type (`shrink` in
`Arbitrary`), so shrinkers must re-establish generator invariants by hand.
Hedgehog and Hypothesis shrink *integrated* with generation (Hypothesis
reduces its internal byte stream), keeping shrunk values inside the
generator's constraints; a 2026 experience report finds neither uniformly
smaller. Hypothesis infers strategies from type annotations (`from_type`,
`register_type_strategy`), warns when `assume`/`filter` rejects too much
(`filter_too_much`), and does stateful testing with `@rule`, `@precondition`
(skips inapplicable rules) and `@invariant` (run after every step).
**icontract-hypothesis** turns `@require` preconditions into strategies
(`5 < x < 10` becomes `integers(6, 9)`), falls back to type strategy plus
filter, and uses `@ensure` as the oracle. **deal** does the same with
`@deal.pre/@deal.post` and `deal.cases(fn)`. **Clojure spec** is the cleanest
statement of the rule: `fdef` with `:args`, `:ret`, `:fn`; `instrument`
checks only `:args` ("not recommended in production due to overhead");
`stest/check` generates from `:args` and checks `:ret` and `:fn`. **Go**:
`testing/quick` is frozen and never shrinks; `pgregory.net/rapid` has
`Make[V]()` type-derived generators (may not terminate on recursive types),
bitstream shrinking, persisted failing cases, and `T.Repeat` state machines
with a `Check` invariant.

**Racket.** `->i` dependent contracts let the result contract read the
arguments and assign blame to caller, callee or the contract itself. Cost is
dominated by wrapper allocation for higher-order values; Typed Racket
boundaries can cost over two orders of magnitude, hence collapsible contracts.

**Kotlin / C#.** Kotlin contracts (`returns() implies`, `callsInPlace`) are
compiler flow hints, "never evaluated at runtime", trusted unconditionally.
.NET Code Contracts (`Contract.Requires/Ensures/OldValue`, Pex inputs) are
unmaintained for modern .NET (not verified from a primary source). Neither is
a model.

**BDD and contracts.** No surveyed system unifies Given/When/Then with
contracts. Nearest is **beehave**, which infers Hypothesis strategies from
Gherkin `Examples` column types. Conceptually Given = a state satisfying
`pre`, When = the call, Then = one example of `post`; a contract is the Then
quantified over all Givens.

## Trade-offs

- **Checking vs. erasure.** Dafny erases (free, unchecked); Eiffel/SPARK keep
  levelled checks. `old` snapshots and invariant walks on every call are the
  cost that bites.
- **Type generators vs. `pre` narrowing.** Type generation plus filtering is
  automatic but rejects too much for sparse preconditions; parsing `pre` into
  bounds recovers the common cases, the rest needs a user generator.
- **Shrinking.** Integrated shrinking needs no user code and respects `pre`;
  type-based shrinking can be smaller but needs hand-written shrinkers.
- **Frames.** Explicit frames (Dafny/SPARK) are precise and costly to write;
  none (Eiffel) loses invariant soundness under aliasing. Immutability makes
  the question moot.
- **Quantifiers.** Executable only over finite collections or ranges.

## Recommendations for jippitese

**Syntax.** Clauses between signature and body, one per line, keywords `pre`,
`post`, `inv`; `ret` names the result; `old(e)` only in `post`, only on
names; `forall x in c` / `exists x in c` only over finite collections and
ranges (compiled to loops). Contract expressions are typed with the empty
effect set, so the compiler enforces the purity Eiffel can only recommend.
`inv` attaches to ADTs and runs at construction (sufficient under
immutability); `mut` cells re-check `inv` after each write under `all`.

```jip
/// Withdraws amt; Err if funds are insufficient.
pub fn withdraw(acc: Account, amt: Money) -> Result<Account, Insufficient>
  pre  amt > 0
  post match ret { Ok(a) => a.balance == acc.balance - amt, Err(_) => true }
{ ... }

/// Bounded LIFO stack.
pub type Stack<T> { items: List<T>, cap: Int }
  inv items.len() <= cap

/// Pushes x; full stacks are rejected by pre.
pub fn push(s: mut Stack<T>, x: T) mut
  pre  s.items.len() < s.cap
  post s.items.len() == old(s.items.len()) + 1
{ ... }
```

**Production toggles.** One line in `app.jip`: `contracts: all | pre | none`.
Default `pre` for `jip build` (AdaCore's advice: caller bugs are cheap to
catch, `old` copies are not), `all` under `jip test`. Violation is a `panic`
(§8); validation of external data stays `Result`, never `pre`.

**Contract to property-check rule.** For every `pub fn` whose effect set is
empty or `{mut}` on fresh state, and that has a `post` or whose parameter or
return types carry `inv`, `jip test` derives one check: a generator per
parameter from its type (ADT-structural, depth-bounded, as rapid `Make` /
Hypothesis `from_type`), narrowed by `pre` through bound extraction for
comparisons, ranges and lengths, otherwise filtered with a rejection budget
that fails loudly; N runs with `contracts: all`; oracle = `post`, every
`inv`, no panic; integrated shrinking on the random stream; shrunk seed
persisted as a regression case. For an `impl` of a behaviour with `inv`,
derive a stateful check: draw method sequences, skip steps whose `pre` fails,
check `inv` after every step. Functions with `io`/`net`/`llm`/`decide` get no
derived check; scenarios with fakes cover them.

**BDD unification.** Add a `given x = any T where <expr>` step drawing from
the same generators, so a scenario is a property when it quantifies and an
example when it does not. Contracts stay on during scenarios, so a Then
failure and a `post` failure print the same shrunk example.

```jip
scenario "withdraw never overdraws" {
  given acc = any Account where acc.balance >= 0
  given amt = any Money where amt > 0
  when  r = withdraw(acc, amt)
  then  match r { Ok(a) => a.balance >= 0, Err(_) => true }
}
```

**Decisions reopened.** §4/§14 "runtime-checked and generate property
checks" needs two qualifications: checking is levelled (`pre` by default in
release) and derivation applies only to effect-free or fresh-`mut` functions.
§14's scenario format should admit `any` generators. No `modifies`/frame
clause is needed (§7 immutability and effects supply it); the spec should say
so.

## Sources

- Dafny Reference Manual (expect, assert, reads/modifies, old, `--test-assumptions`):
  https://github.com/dafny-lang/dafny/tree/master/docs/DafnyRef
- Dafny FAQ on loop `modifies`: https://dafny.org/dafny/HowToFAQ/FAQLoopModifies
- Eiffel, "Design by Contract, Assertions and Exceptions":
  https://www.eiffel.org/doc/uuid/2ef367c9-34d9-d45e-a722-163b39581405
- Eiffel past-expression (old) limitations: https://www.comp.nus.edu.sg/~abhik/pdf/aosd13.pdf
- AutoTest stateful testing: https://arxiv.org/abs/1108.1068 ;
  "Laws of automatic random testing": https://arxiv.org/abs/1211.3257
- SPARK User's Guide, Subprogram Contracts and Specification Features:
  https://github.com/AdaCore/spark2014/tree/master/docs/ug/en/source
- AdaCore Gem #149 (which checks to keep in production):
  https://www.adacore.com/blog/gem-149-asserting-the-truth-but-possibly-not-the-whole-truth
- GNAT `Assertion_Policy`: https://snapshots.sourceware.org/gcc/docs/latest/gnat_rm/Pragma-Assertion_005fPolicy.html
- Hypothesis stateful docs: https://github.com/HypothesisWorks/hypothesis/blob/hypothesis-python-6.100.0/hypothesis-python/docs/stateful.rst ;
  health checks: https://hypothesis.readthedocs.io/en/latest/how-to/suppress-healthchecks.html
- Integrated shrinking: https://hypothesis.works/articles/integrated-shrinking/ ;
  https://well-typed.com/blog/2019/05/integrated-shrinking/ ;
  "Evaluating Shrinking": https://arxiv.org/abs/2608.09935
- icontract-hypothesis: https://github.com/mristin/icontract-hypothesis
- deal: https://github.com/life4/deal
- Clojure spec guide (fdef, instrument, check): https://clojure.org/guides/spec
- Go `testing/quick` (frozen): https://pkg.go.dev/testing/quick ; rapid: https://pkg.go.dev/pgregory.net/rapid
- Racket Guide, contracts on general functions (`->i`):
  https://docs.racket-lang.org/guide/contracts-general-functions.html ;
  collapsible contracts: https://users.cs.northwestern.edu/~stamourv/papers/collapsible-contracts.pdf
- Kotlin contracts KEEP: https://github.com/Kotlin/KEEP/blob/master/proposals/kotlin-contracts.md
- beehave (Gherkin to Hypothesis): https://pypi.org/project/beehave/
