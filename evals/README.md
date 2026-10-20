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
the acceptance scenarios hidden. Its harness logs, in order, every file the
agent reads and every tool output it receives, one JSON object per line:

```text
{"kind": "read", "path": "orders/billing/billing.jip", "text": "…"}
{"kind": "tool", "cmd": "jip context orders.billing", "text": "…", "exit": 0}
{"kind": "tool", "cmd": "jip check", "text": "…", "exit": 1}
```

The count stops at the first `jip check` (or `go vet` for Go) that exits
0. Transcripts go to `out/<jip|go>/<model>/<task>.jsonl` (`out/` is not
committed until a run is reported).

## Status

- Tokenizer, task set, validation, counting and aggregation: done.
- Agent runs: not started. They need model access and the choice of models
  ("run across multiple models", decisions §13); this environment has
  neither.
- Go baseline: done (`go/`, `validate_go.sh`).
