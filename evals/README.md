# evals

The Phase F harness for the decisions §13 metric: **tokens to read**, the
total tokens of every file and tool output an agent reads before its first
green `jip check`, reported with **iterations to green**, on a frozen task
set, under one named tokenizer. The Go baseline runs the same tasks with
`go doc` output counted the same way. No number from this harness goes into
any document until real agent runs produced it (R15).

## Tokenizer

`o200k_base` from `js-tiktoken` 1.0.21, pinned in `package.json`; the
ranks ship inside the package, so counting works offline.

```text
npm install
node count.mjs some.jip other.jip          # tokens per file, total
node count.mjs --transcript run.jsonl      # one run: tokens and iterations to green
node report.mjs out/                       # every run: per task and medians
```

## Task set (frozen)

Twelve changes over the `examples/` corpus, in `tasks/<id>/`:

| Task | Project | Change | Declared break |
|---|---|---|---|
| 01-refund | orders | add `refund` with `pre`/`post` | |
| 02-status-refunded | orders | add a `Status` variant and `is_final` | `orders.billing` |
| 03-charge-retry | orders | retry the gateway on 503, at most three posts | |
| 04-line-discount | orders | add a field to `Line`, subtract it in `total` | `orders.billing` |
| 05-largest-line | orders | add `largest_line` using `max_by` | |
| 06-reject-large | shop | the Worker rejects totals above 100_000 | |
| 07-grand-total | shop | sum a `Repository` through `query` | |
| 08-idle-30m | sessions | lower the Cron sweep threshold | |
| 09-active-count | sessions | count stored sessions | |
| 10-unknown-currency | rates | unknown currencies become an error type | `rates.cache` |
| 11-two-approvals | refunds | a second `approve` for large refunds, version bump | |
| 12-raise-limit | refunds | raise the workflow's limit | |

Each task has `task.md` (the instruction an agent receives, after a short
header naming the project, the module whose `scenarios.jip` receives the
hidden acceptance scenarios, and the break to accept), `accept.jip` (the
hidden acceptance scenarios, all named `accept …`) and `solution.patch`
(a reference solution). `tasks/MANIFEST` holds their checksums: the set is
frozen, and `validate.sh` refuses to run if any file changed.

`./validate.sh` proves every task well-formed: on a copy of the project the
acceptance scenarios fail, the reference solution applies, and then `jip
check --write-interfaces` (accepting the declared break) and `jip test` are
green.

## Go baseline

`go/` holds idiomatic Go versions of the five projects the tasks touch
(`orders`, `shop`, `sessions`, `rates`, `refunds`), one module each, standard
library only, each green under `go vet` and `go test`. `go/tasks/<id>/`
restates each of the twelve tasks against them: `task.md` (header
`project:` and `package:`, the directory receiving the hidden test),
`accept_test.go` (hidden acceptance tests, all named `TestAccept…`) and
`solution.patch`, frozen by `go/tasks/MANIFEST`. `./validate_go.sh` proves
them as `validate.sh` does: the acceptance tests fail (or do not compile)
before the patch, and after it `gofmt`, `go vet ./...` and `go test ./...`
are clean.

The Go side gets the plainest equivalent of each jippitese construct, never
a framework, so any measured jippitese advantage is understated:

- Behaviours are plain types and methods: the `shop` Worker is
  `Indexer.Deliver`, the `sessions` Cron is `Store.RunSweeper` on a ticker.
- The `refunds` Workflow is a method over a small `durable` journal package
  (record, replay by step name, a version check), not Temporal.
- Repositories are one in-memory generic `Repo`; preconditions are panics;
  postconditions and invariants have no Go form, so task 01 drops its
  postcondition; declared breaks (tasks 02, 04, 10) have no counterpart.
- Fakes are a `Now` clock field, `httptest` and `testing/quick`. There is
  no `main`: no task touches `app.jip`.

## Transcripts

A run is one agent solving one task in a fresh copy of the project, with
the acceptance scenarios hidden. Its harness logs, in order, the prompt,
every file the agent reads and every tool output it receives, one JSON
object per line:

```text
{"kind": "prompt", "text": "…"}
{"kind": "read", "path": "orders/billing/billing.jip", "text": "…"}
{"kind": "tool", "cmd": "jip context orders.billing", "text": "…", "exit": 0}
{"kind": "tool", "cmd": "jip check", "text": "…", "exit": 1}
{"kind": "result", "accept": true, "usage": {…}, …}
```

The count stops at the first `jip check` (or `go vet` for Go) that exits
0. Transcripts go to `out/<jip|go>/<model>/<task>.jsonl`; `out/bin` (the
`jip` binary the runs use) is not committed.

## Running agents

```sh
node run.mjs --lang jip                       # every task, every default model
node run.mjs --lang go --model claude-sonnet-5-5 --task 03-charge-retry
node run.mjs --lang go --model reference --out /tmp/self-test
node run.mjs --lang jip --via claude-code     # through `claude -p`, no API key
node report.mjs out/
```

`run.mjs` drives each model through the Anthropic API (Application
Programming Interface) with five tools: `list_dir`, `read_file`,
`write_file`, `edit_file` and `run`, which runs an allow-listed command
(`jip check|context|test|fmt`; `go vet|test|build|doc`, `gofmt`) without a
shell, confined to the workspace. The jippitese prompt carries the language
card (`docs/spec/00-language-card.md`), counted like any other read; the Go
prompt carries none. When the agent stops, the hidden acceptance tests are
added and run the way `validate.sh` and `validate_go.sh` do, and the
`result` line records the outcome with the API token usage.

Defaults: the current Claude models, `--effort high`, `--max-turns 40`,
adaptive thinking. Opus and Sonnet runs enable server-side refusal
fallbacks (`--no-fallback` turns them off); a run a fallback model served
is marked `"fallback": true` in its result and in the report. Credentials
come from `ANTHROPIC_API_KEY` or whatever else the Anthropic SDK resolves.
The pseudo-model `reference` needs none: it replays each reference solution
and tests the harness end to end.

`--via claude-code` runs each agent through `claude -p` on the account
Claude Code is signed in with, so a Claude subscription needs no API key.
The agent gets the same system prompt and the same five tools, served over
MCP (Model Context Protocol) by `mcp.mjs`, and no built-in tools, settings
or `CLAUDE.md`; the transcript and the metric are therefore the same as an
API run. Its `result` line carries `"via": "claude-code"`; its usage
includes Claude Code's own overhead, and no fallbacks are requested.

## Status

- Tokenizer, task set, validation, counting and aggregation: done.
- Agent runner: done (`run.mjs`); the `reference` self-test is green and
  accepted on all twelve tasks in both languages.
- Agent runs: done, one per task, model and language, through
  `--via claude-code`; transcripts and `report.json` in `out/`, results in
  `docs/decisions.md` §13.
- Go baseline: done (`go/`, `validate_go.sh`).
