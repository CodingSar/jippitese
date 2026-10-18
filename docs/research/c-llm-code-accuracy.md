# (c) Language features vs. LLM accuracy

Status: research report, track (c). Evidence quality is marked **[strong]**
(peer-reviewed or large controlled benchmark), **[moderate]** (single paper,
narrow setup, or self-reported numbers), or **[anecdote]** (blog, forum, one-off
measurement).

## Summary

1. A brand-new language starts with a large, measurable penalty. Zero-resource
   languages (Gleam, MoonBit) fail mostly on *syntax* (two-thirds to 90% of
   failures vs. under 10% in known languages); a minimal unseen language
   (PyLang) trails Python by up to 19 points even after fine-tuning; agents
   spend ~1.3–1.7x more tokens on lower-resource languages at equal success.
2. The penalty tracks *distance from familiar syntax*, not popularity alone:
   Lua and Dafny punch above their corpus share, Lean and OCaml below.
3. In-context specs work, but examples beat prose: models learn unseen
   libraries from in-context descriptions; few-shot examples doubled Gleam
   pass@1; in the grammar-book translation analogue nearly all gain came from
   parallel examples, not grammar explanations.
4. Context size hurts non-linearly: semantic recall collapses when the relevant
   code sits mid-context (median drop 92.7%); signature-only context is
   cheaper but worse than full context.
5. The strongest lever is a compile/typecheck loop with actionable messages:
   compiler access raised compile success by 5–79 points; type errors, not
   syntax, dominate LLM compile failures in TypeScript.

## Findings

**Low-resource / new languages.** MultiPL-E (18 languages) found models do
better on high-frequency languages with exceptions: Codex scored 0.41 pass@1
on Lua despite its small corpus, and pass@1 did not track per-language
perplexity **[strong]**. MultiPL-T fine-tuning lifted StarCoderBase-15B Racket
from 28.4 to 41.3 pass@1; OCaml regressed at 15B **[strong]**. "No Resource,
No Benchmarks, No Problem?" (Gleam, MoonBit; 2026) is the closest analogue to
jippitese: best pass@1 on McEval-Hard was 1%; for GPT-4o about two-thirds of
failures were syntactic (90% for o3-mini on Gleam) vs. under 10% in known
languages. Few-shot examples raised GPT-4o Gleam HumanEval from 7.60% to
15.45%; continued pre-training reached ~15% on McEval-Hard **[moderate, single
paper]**. PyLang ("Syntax Without Semantics", 2026): fine-tuned Qwen3 learned
an unseen minimal language's syntax quickly and picked the same algorithm as
for Python 80% of the time, yet Python beat PyLang by up to 19 points across
all interventions **[moderate]**. Vericoding (12.5k specs): off-the-shelf
success 82% Dafny, 44% Verus/Rust, 27% Lean; Dafny is low-resource but
syntactically familiar, Lean is not **[strong]**. Blog test (Claude 3.5
Sonnet): Python 81%, TypeScript 81%, Swift 71%, Rust 62% **[anecdote]**.

**Learning from in-context documentation.** Patel et al. (NAACL 2024): models
down to Llama-2/StarCoder used unseen libraries from in-context descriptions or
raw implementations; demonstrations were not required; irrelevant APIs in the
prompt hurt **[strong]**. Grammar prompting (NeurIPS 2023): a minimal BNF
subset per example reached 95.7% on GeoQuery and 83.6–88.9% on SMCalFlow
**[strong]**. SPEAC (NeurIPS 2024): writing in a Python-like intermediate
language compiled to UCLID5 gave 84.8% syntactic correctness vs. 9.1% for
fine-tuning **[moderate]**. The Kalamang grammar-book follow-up: "almost all
improvements stem from the book's parallel examples rather than its
grammatical explanations" **[strong, natural language]**. Anka (2025): a DSL
with one canonical form per operation gave Claude 3.5 Haiku 99.9% parse success
and 95.8% task accuracy with zero training exposure; 100% vs. 60% for Python on
multi-step pipelines **[moderate, self-authored benchmark]**.

**Syntax choices.** No controlled study compares braces with indentation. A
HumanEval failure study (14 LLMs, 12,837 failures) lists inconsistent
indentation among only three mechanically fixable error classes
**[moderate]**; TokDrift (ACL 2026) shows whitespace-only edits change
tokenization and behaviour across nine code LLMs **[strong]**.
Type-constrained decoding (PLDI 2025): in generated TypeScript only ~6% of
compile errors are syntactic, the rest are type errors; type-guided decoding
halved compile errors **[strong]**. Programming-language confusion (10 LLMs,
75k samples): models drift toward Python and between syntactically similar
pairs; explicit language keywords were the most effective fix **[strong]**.
No study isolates explicit type annotations or verbosity; CodePromptEval
(7,072 prompts) found function signatures significantly change output, with a
correctness/quality trade-off **[moderate]**.

**Token efficiency.** Alderson's Rosetta Code comparison (19 languages, GPT-4
tokenizer): 2.6x spread, C worst, Clojure 109 tokens, Python ~130, Haskell/F#
near the dynamic languages **[anecdote, static code]**. "Tokenmaxxing" (2026,
2,000 agent trajectories): agents spend fewest tokens in Python; OCaml
~1.5–1.7x; GPT-5.5 Java 1.30x, Rust 1.36x; at comparable success rates
lower-resource languages cost significantly more because agents repeatedly
produce non-compiling code and prototype in Python **[strong]**. Static token
count is a weak predictor; retries dominate.

**Context and repository-level evidence.** DocPrompting: retrieved docs raised
CodeT5 pass@1 by 2.85 points (52% relative) on CoNaLa **[strong]**. RepoMind
(ICPC 2026): hierarchical repo docs plus retrieval, up to 13.5% relative pass@1
**[moderate]**. REPOEXEC (NAACL 2025 Findings, 18 models): full dependency
context beat medium (signatures + docstrings) beat small (signatures only);
signature-only models tend to reimplement dependencies **[strong]**. "Sense and
Sensitivity" (ACL 2026): semantic recall drops sharply when code sits
mid-context (median 92.73% drop on SemTrace); lexical recall stays near
perfect **[strong]**.

**Self-repair and error messages.** Compiler-in-the-loop on 699 C tasks, 16
models: compile success up 5.3–79.4 points; syntax errors down 75%, undefined
references down 87%; the common factor was messages that "could be translated
into specific actions" **[strong]**. SafeTrans (C-to-Rust): localized errors
fixed reliably; trait/type mismatches (E0277, E0308) only ~55–59%
**[moderate]**. Olausson et al. (ICLR 2024): self-repair gains are modest and
bounded by feedback quality **[strong]**.

## Trade-offs

- **Familiar syntax vs. one-way-to-do-it.** Familiarity buys transfer from
  Go/TS/Rust priors; novelty costs syntax errors. Anka suggests one canonical
  form can beat Python on structured tasks, but on a narrow domain.
- **Explicit types vs. tokens.** Types add tokens (TS ~1.3–1.45x JS by blog
  estimates) but type errors are the main compile failure and type checking
  catches them. Boundary types are worth their tokens; local inference keeps
  bodies short.
- **Context size vs. accuracy.** Signature-only context is cheapest and worst;
  full bodies are best but blow the budget and trigger mid-context recall
  loss. Signature + one-line doc + contracts is the evidence-backed middle.
- **Docs vs. examples.** Prose explains; examples transfer. A mostly-prose spec
  will under-perform a mostly-example one.

## Recommendations for jippitese

Syntax rules:
1. Keep §11 (C/Go/TS-shaped, braces) and treat every keyword or operator that
   differs from Go/TS/Rust as a cost it must justify in the eval. Keep `fn`,
   `match`, `let`/`var`, `?`, `pub`; no novel sigils.
2. Make syntax tokenizer-unambiguous: braces, no significant whitespace, no
   optional delimiters, one canonical form per construct, fixed by `jip fmt`.
3. Keep full signatures at boundaries and local inference in bodies (§3); the
   token and type evidence both favour exactly this split.
4. Treat diagnostics as a v0 feature: every compiler, effect and contract
   error names file:line, expected vs. found, and a concrete suggested edit
   (rustc-style). Highest-leverage item found; absent from decisions.md.
5. Require an explicit language marker (`.jip` plus a header or `module`
   line) so models do not drift into Go/TS.

Spec-writing rules:
6. Examples first, prose second: every construct gets a minimal `.jip` example
   with its Go/TS equivalent so the spec doubles as few-shot context; add a
   compact BNF subset per section (grammar prompting).
7. Ship a ~2k-token "language card" of examples agents load before writing;
   measure it in the intuitiveness eval (§13).
8. Context packs (§9) carry signatures + one-line doc + `pre`/`post`, most
   relevant interface first or last, never mid-pack; exclude unrelated modules
   (irrelevant APIs hurt).

Decisions reopened: none reversed. Amend §11 terseness policy to "terse in
bodies, explicit at boundaries"; add diagnostics quality to §15; add
"iterations to compile" beside "tokens to read" in §13, since agent token cost
is driven by retries, not static length; and budget in §13 for a Gleam-sized
accuracy gap at launch, with a small continued-pre-training corpus
(`examples/`) as mitigation.

## Sources

- MultiPL-E (TSE 2023): https://arxiv.org/abs/2208.08227
- MultiPL-T, knowledge transfer to low-resource languages (OOPSLA 2024): https://arxiv.org/abs/2308.09895
- No Resource, No Benchmarks, No Problem? (Gleam/MoonBit, 2026): https://arxiv.org/abs/2606.16827
- Syntax Without Semantics (PyLang, 2026): https://www.alphaxiv.org/abs/2605.15607
- Vericoding benchmark (Dafny/Verus/Lean): https://arxiv.org/abs/2509.22908
- Survey: LLM code generation for low-resource and DSLs: https://arxiv.org/abs/2410.03981
- Evaluating In-Context Learning of Libraries (NAACL 2024): https://aclanthology.org/2024.naacl-long.161/
- Grammar Prompting for DSL generation (NeurIPS 2023): https://arxiv.org/abs/2305.19234
- Synthetic Programming Elicitation / SPEAC (NeurIPS 2024): https://arxiv.org/abs/2406.03636
- Can LLMs Really Learn to Translate from One Grammar Book?: https://arxiv.org/abs/2409.19151
- Anka DSL for reliable LLM code generation: https://arxiv.org/abs/2512.23214
- Type-Constrained Code Generation (PLDI 2025): https://arxiv.org/abs/2504.09246
- Programming Language Confusion: https://arxiv.org/abs/2503.13620
- LLMs Love Python: https://arxiv.org/abs/2503.17181
- HumanEval failure analysis / LlmFix: https://arxiv.org/abs/2409.00676
- TokDrift (ACL 2026): https://aclanthology.org/2026.acl-long.2199/
- CodePromptEval: https://arxiv.org/abs/2412.20545
- Tokenmaxxing, agent token cost across languages (2026): https://arxiv.org/abs/2607.22807
- Alderson, token-efficient languages (blog): https://martinalderson.com/posts/which-programming-languages-are-most-token-efficient/
- LLMs vs programming languages (blog, Terhechte): https://terhech.de/posts/2025-01-31-llms-vs-programming-languages.html
- DocPrompting (ICLR 2023): https://arxiv.org/abs/2207.05987
- RepoMind (ICPC 2026): https://conf.researchr.org/details/icpc-2026/icpc-2026-research/30/
- REPOEXEC, impacts of context (NAACL 2025 Findings): https://arxiv.org/abs/2406.11927
- Sense and Sensitivity (ACL 2026): https://arxiv.org/abs/2505.13353
- From LLMs to Agents: compiler in the loop (SANER 2026): https://arxiv.org/abs/2601.12146
- SafeTrans C-to-Rust: https://arxiv.org/abs/2505.10708
- RustAssistant: https://arxiv.org/abs/2308.05177
- Is Self-Repair a Silver Bullet? (ICLR 2024): https://arxiv.org/abs/2306.09896
