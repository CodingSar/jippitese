# (j) Where the token spend goes, and what would cut it

Status: research report written after the F3 results (decisions §13). Every
number about our runs is computed from the 72 transcripts in `evals/out/`
(o200k_base, the harness tokenizer, unless it says "API usage", which is the
provider's own count). Numbers from other work are attributed. Forecasts are
labelled as hypotheses and carry no figure (R15).

## Summary

1. jippitese lost on every cost axis the runs recorded, not only on "tokens
   to read". API usage: median input 59–79k tokens per jippitese run against
   26–29k for Go; summed cost 1.1–1.6× Go per model; median wall-clock
   25–31 s against 16–25 s; 14 failed `jip check` calls in 13 runs against 0
   failed `go vet` calls (13 of the 14 were interface drift, E0201; see
   report (k)). All 72 runs were accepted.
2. The language itself is denser: the five projects' sources are 3,602
   tokens in jippitese and 4,968 in Go (−27%); their tests 1,544 against
   2,747 (−44%). That saving lands on the smallest term of the bill and is
   wiped out by the novelty tax: the card is 41% of all re-sent content in a
   jippitese run, `jip context`/`check`/`test` output another 19%.
3. The bill is dominated by **turns × prefix**. Every tool call re-sends the
   whole conversation. A ten-line change took a median 13 events (jippitese)
   and 11.5 (Go), 5–7 of them navigation before the first edit. The code the
   agent wrote is a few hundred of ~1.5–3k output tokens.
4. The biggest lever in our own data is not the language: **model choice
   moved cost 25–36×** (all twelve tasks: Haiku $0.051 vs Opus $1.274 in
   jippitese, $0.032 vs $1.158 in Go) with the same 12/12 acceptance.
   Language choice moved it 1.1–1.6×.
5. Track (c) predicted this: unseen languages cost agents 1.3–1.7× more
   tokens at equal success. We measured 1.44×. The bet was that bounded
   context would pay for it, but the task set (1–3k-token projects, one file
   per change) cannot exercise bounded context, so that bet is untested, not
   won.

## Findings

### F1. Anatomy of a run

Re-sent content per run (each event replayed in its own turn and every later
one; harness system prompt and tool definitions excluded, so this
understates the fixed part):

| | re-sent tokens/run | card | task text | file reads | tool output |
|---|---|---|---|---|---|
| jippitese | 42,338 | 41% | 8% | 31% | 19% |
| Go | 15,866 | 0% | 11% | 86% | 2% |

Edit calls are about 1% in both. Cache reads cost a tenth of base input, which
is why a 2.2–3.0× input gap becomes only a 1.1–1.6× cost gap.

### F2. Counterfactuals on the same transcripts

Same reads, same edits, fewer turns: fold every navigation event before the
first edit into the first turn (a "context pack"), and fold `check` + `test`
into one call.

| | events → turns | re-sent tokens (median) | pack | pack, no card |
|---|---|---|---|---|
| jippitese | 13 → 5 | 36,177 | 15,578 (−57%) | 9,800 (−73%) |
| Go | 11.5 → 4 | 14,343 | 7,919 (−45%) | — |

This assumes the pack holds exactly what the agent chose to read, so the read
side is an upper bound; the fixed prompt that every removed turn also
re-sends is not counted, so the turn side is a lower bound.

### F3. Projections instead of files

Function bodies elided, comments dropped (`go/ast`, `go/printer`): the five
Go projects go from 4,968 to 2,840 tokens (−43%). The jippitese
`interface.jipi` files are 2,327. A projection plus on-demand bodies is the
language-independent form of the "bounded context" pillar.

### F4. What others measured

- SimPy, a reversible terse grammar for Python with the same AST, cut tokens
  8.6–34.7% depending on tokenizer and 10.4–13.5% in model use, accuracy
  kept (preprint). Our −27% source density is in that band. Neither changes
  the dominant term.
- LongCodeZip (ASE 2025) compresses code context up to 5.6× without loss on
  completion, summarisation and QA (Question Answering); SWEzze (2026)
  reports about 6× compression and a 51.8–71.3% lower total token budget on
  SWE-bench Verified.
- Bai et al. analyse OpenHands trajectories on SWE-bench Verified: agentic
  coding uses far more tokens than other LLM (Large Language Model) uses,
  with high variance and poor self-prediction. A vendor guide reports cache
  reads at ~94.5% of a Claude Code session's tokens [anecdote].
- Aider: diff edit formats use far fewer output tokens than whole-file
  rewrites but fail to apply more often, worst on weaker models.

## Where we went wrong

1. **Optimised the smallest term.** Syntax density acts on file reads and
   edit text; the bill is prefix × turns plus model price.
2. **Paid a per-turn novelty tax.** A card is replayed every turn, the agent
   reaches for `jip context` (extra tool output), and the interface drift
   gate (E0201) costs a failing check on every public change. Missing priors
   caused errors in only 2 of 36 runs.
3. **Measured a proxy, not the bill.** "Tokens to read" ignores turns, output,
   price per model and time. The API usage and seconds were in the
   transcripts all along.
4. **Tested the hypothesis where it cannot show.** Toy projects, one run per
   cell, no greenfield task, no large repository.
5. **Put the context idea in the language.** Interface files, effect
   summaries and context packs do not need a new language; they work on Go.

## Alternatives

Ordered by expected payoff per unit of work. Each is measurable with
`run.mjs` by adding a tool set or a model policy.

| # | Alternative | Term it attacks | Evidence | Work |
|---|---|---|---|---|
| A | **Model cascade**: cheapest model first, escalate on red check or failed acceptance | price per token | measured 25–36× on these tasks | small |
| B | **Turn compression**: `pack` (task → relevant files/projections, test file, commands in one result) and `apply` (many edits, then check+test, compressed diagnostics) | turns × prefix | counterfactual −45% (Go) | small–medium |
| C | **Projections**: skeleton/interface views with `show <symbol>` for bodies; port `.jipi`/`jip context` to Go via `go/packages` | file reads | −43% on the corpus; literature 5–6× | medium |
| D | **Structural edit language**: `add-variant`, `add-field … = default`, `rename`, `add-case`; the tool lists every site the change breaks | output, iterations | hypothesis; tasks 02, 04, 10 are this shape | medium |
| E | **Intent specs over Go**: keep the behaviours (`Service`, `Worker`, `Workflow`, `Machine`, `Agent`) as a small spec that generates idiomatic Go, which agents then edit as Go | output on greenfield | hypothesis; untested by the current set | large |
| F | **Session amortisation**: fixed conventions in a cached prefix, many tasks per session | per-task fixed cost | card becomes a cache read | small |
| G | **Effort and output discipline**: low effort for local edits, terse final answers | thinking/output | hypothesis | small |
| H | **Reversible terse view** (SimPy/DualCode style) of Go for the model only | reads | 10–13% in the literature; our −27% | medium; low payoff |
| I | **Verification as Go tooling**: contracts and derived property checks as an analyser and test generator | defects, review time | not a token play | medium |

Combined shape: an MCP (Model Context Protocol) server for Go projects
exposing `pack`, `show`, `edit-op` and `apply`, behind a model cascade. It
keeps the model's Go priors, which is the one asset a new language cannot
match.

## Recommendations for jippitese

1. Reopen decisions §1 ("authors are agents" as a reason for a new language)
   and §13 (metric). Report API cost, turns, wall-clock and iterations; keep
   tokens to read as a diagnostic.
2. Extend the task set before the next decision: a 50k+ line repository,
   greenfield generation tasks, three runs per cell.
3. Build A, B and C first, against Go, in `evals/` as a third language
   column (`go+tools`). If B and C hold up, D and E are the places a
   jippitese-derived notation can still earn its keep.
4. Keep I as a separate value claim with its own eval (seeded bugs, escape
   rate), not as a token claim.

## Sources

- Sun et al., *AI Coders Are Among Us: Rethinking Programming Language
  Grammar Towards Efficient Code Generation*: https://arxiv.org/abs/2404.16333
- *LongCodeZip: Compress Long Context for Code Language Models*:
  https://arxiv.org/abs/2510.00446
- *Compressing Code Context for LLM-based Issue Resolution* (SWEzze):
  https://www.alphaxiv.org/abs/2603.28119
- Bai et al., *How Do AI Agents Spend Your Money?*:
  https://arxiv.org/abs/2604.22750
- Augment Code, AI coding cost analysis [anecdote]:
  https://www.augmentcode.com/guides/ai-coding-cost-analysis-agent-token-spend
- Aider edit formats and benchmarks: https://aider.chat/docs/more/edit-formats.html,
  https://aider.chat/docs/benchmarks.html
- Track (c) of this project: `docs/research/c-llm-code-accuracy.md`
