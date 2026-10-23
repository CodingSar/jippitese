# (k) Claims ledger: what the F3 runs falsify, and what they cannot

Status: follow-up to report (j). Each idea of the project, and each
alternative (j) proposed, is stated as a causal chain, checked against the
72 transcripts in `evals/out/` (o200k_base unless marked "API usage"), and
given the cheapest test that would decide it. Thresholds marked "derived"
are arithmetic on measured numbers, not forecasts (R15).

## Summary

1. **Correction to (j).** The 14 failed `jip check` calls (13 runs) were not
   missing priors: 13 were E0201 (committed `interface.jipi` out of date),
   1 was E0403 (type inference). Add one E0002 (scenario syntax, seen by
   `jip test` and `jip fmt` in the same run) and 2 `jip context` calls on a
   module that does not exist. Novelty errors hit 2 of 36 runs, both Haiku.
   Go had no failing tool call.
2. **Ceremony, not language.** Median re-sent tokens per run: Go 14,342.5;
   jippitese 36,177; jippitese without its `jip context` calls and drift
   failures 30,074; the same without the card 14,840. With the ceremony
   removed the language is level with Go on these tasks, and the card is the
   whole remaining gap.
3. **"Tokens to read" is falsified as a proxy for cost.** jippitese read
   fewer tokens (median 1,164 against 1,887) and cost more on every model.
4. **Density break-even (derived).** A card of C tokens is re-sent every turn
   (C·T); a density gain d on R read tokens, read on average halfway through
   the run, saves d·R·T/2. Density pays when R ≥ 2C/d = 2·1,202/0.27 ≈ 8.9k
   Go-equivalent read tokens per task, whatever T is, and only if iterations
   do not rise. The eval tasks read about a fifth of that.
5. What survives is not language-specific: verification (contracts, derived
   properties), a model cascade that depends on it, and turn compression.

## Ledger

| # | Claim | Verdict | Cheapest decisive test |
|---|---|---|---|
| 1 | A denser new language cuts agent cost | Falsified on this set; open only above ~8.9k read tokens/task | Large-repo tasks, ceremony fixed, jip vs Go+projections |
| 2 | Committed interfaces + drift gate help | Falsified as a gate for agents | Make `jip check` write interfaces; rerun the 13 runs |
| 3 | Context packs replace reading | Falsified here (27/27) | Tasks needing ≥3 modules the agent does not edit |
| 4 | Effects in types help agents | Unsupported; no task exercised it | Tasks whose fix depends on a callee's I/O, projection ± effects |
| 5 | Contracts + derived properties catch more | Open; no signal possible here (72/72 accepted) | Seeded-bug kill rate, agent tests ± derived properties |
| 6 | Behaviours cut greenfield output | Untested | Greenfield tasks |
| 7 | `llm`/`decide`/`approve` primitives | Product claim, not a token claim | Compare with existing libraries |
| 8 | Typed `app.jip` | Not agent-relevant; no task touched it | — |
| 9 | Tokenizer-unambiguous syntax | Consistent (1 syntax error in 36 runs), no control | Not worth one |
| A | Model cascade | Cost case holds; safety depends on 5 | Haiku-first policy on harder tasks, count undetected failures |
| B | Turn compression (`pack`, `apply`) | Open; strongest token lever measured | `go+pack` column |
| C | Projections | Null here; plausible on large repos | Same set as 1 |
| D | Structural edits | Falsified as a token play | — |
| E | Intent specs over Go | Untested; drift risk | Same set as 6 |
| F | Session amortisation | Mostly realised already | — |
| G | Effort discipline | Untested, trivial | `--effort low` rerun |
| H | Reversible terse view | Dominated by C | — |

## Findings

**1. Dense language.** For: the density is real (−27% source, −44% tests),
novelty errors were rare, and without ceremony the language already breaks
even. Against: the break-even needs reads several times larger than these
tasks, with no extra iterations, and projections (C) attack the same term
harder on Go (−43%) and shrink R, which raises the bar for density. A
smaller card lowers the threshold linearly; it does not remove it. Only a
model trained on the language removes the card, which this project does not
control.

**2. Drift gate.** E0201 fires on every public change, and its fix is one
mechanical command, so each such task pays a failing turn plus its replay.
The review value (the API diff in a PR, the breaking-change classifier) does
not need a gate: Go's `apidiff` produces it in CI. Keep the classifier; have
`jip check` write the interface and fail only on an undeclared break.

**3. Context packs.** `edit_file` needs the exact text it replaces, so an
agent must read every file it edits; a pack can only replace reads of code
it does not edit. In one-file changes that set is empty, which is what the
27 of 27 runs show (packs averaged 2,349 characters, added on top).

**4. Effects.** For: an effect row in a signature lets an agent or reviewer
skip a body, and it makes fakes deterministic. Against: adding an effect to a
leaf ripples through every caller's signature, the failure mode of checked
exceptions, and each ripple is an edit plus drift. Sharper form: infer
effects, show them in projections, enforce them only at behaviour
boundaries. That is a Go analyser, not a language.

**5. Contracts.** Against: when the agent writes both code and contract, the
two can share one misunderstanding. For: `pre`/`post` are short enough for a
human to review instead of the body, and production has no hidden
acceptance tests, so the agent's own verification is the only thing that
makes A safe. This is the one idea whose value grows as cheaper models do
more of the work.

**6, E. Behaviours and specs.** Durable-workflow determinism is hard to
hand-roll, which is a case for them; the long tail of shapes that do not fit
and the Go frameworks models already know are the case against. Generation
must stay one-way: a spec that agents edit as Go drifts from its source,
the E0201 failure in a worse form.

**A. Cascade (derived).** With per-run costs c (cheap) and E (expensive),
trying cheap first and escalating on detected failure pays while
p_fail < 1 − c/E: 0.96 for jippitese and 0.97 for Go, from the twelve-task
totals. Escalation re-pays the prefill, which the formula already counts.
The claim falls on undetected failures: wrong code that passes the agent's
own checks. Their rate decides A, and it depends on 5.

**B. Turn compression.** Counterfactual: −45% re-sent tokens in Go. Two
cautions. Most removed tokens were cache reads at a tenth of the price, so
the dollar gain is smaller than the token gain; the latency gain (about
2.2 s per event) is not. And the pack must carry the full text of files to
be edited (finding 3), or agents read them again.

**D. Structural edits.** Edits are about 1% of re-sent content, and the Go
runs had no failing `go vet`, so neither tokens nor iterations leave room.

**F, H.** Within a run the card is already a cache read after the first
turn (median cache reads: jippitese 56k, Go 21k, API usage); F only saves
the first write per session. H gains less than C (10.4–13.5% in model use
for SimPy, our −27%, against −43%) and adds a notation the model has not
seen.

## Trade-offs

Ideas 1–3 are decided by this data or by design logic; 4–6 and C cannot be,
because the task set has no large repository, no multi-module change, no
greenfield work and no seeded bug. One run per cell means any effect smaller
than run-to-run spread is invisible.

## Recommendations for jippitese

Ordered by cost per decision:

1. Replace the E0201 gate with write-and-classify (claim 2) and rerun
   jippitese; this confirms or refutes the ceremony account in Summary 2.
2. Add `go+pack` and a Haiku-first cascade to `run.mjs`, and an
   `--effort low` rerun, on the existing set (B, A, G).
3. Build a seeded-bug eval for contracts and derived properties (5); A's
   safety rests on it.
4. Build a large-repository and multi-module set; it decides 1, 3, 4 and C
   together. Stop rule: if Go plus projections matches or beats jippitese
   there, drop the language and keep the tooling.
5. Greenfield tasks for 6 and E last.

Decisions reopened: §1 and §13, as (j) says; the drift gate's design.

## Sources

- Report (j), `docs/research/j-token-spend-alternatives.md`, and its sources.
- `golang.org/x/exp/apidiff`: https://pkg.go.dev/golang.org/x/exp/apidiff
