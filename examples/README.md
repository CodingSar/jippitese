# examples

The Phase E corpus (`docs/revision-plan.md` §5): the language card's app and
one app per remaining v0 behaviour. Each is a complete project with its
`app.jip`, modules, committed `interface.jipi` files and scenarios that
pass under the default fakes.

| Project | Shows | Scenarios |
|---|---|---|
| `orders/` | The language card: `charge`, `withdraw`, `total`, `max_by`, the `BillingApi` service, the `Billing` workflow, a vendored dependency and an `extern` | `FakeHttp`, properties with `any … via` and `any … where`, a table, an `@extern` check; the derived check of `withdraw` (`pre` and `post`) |
| `shop/` | A `Worker` consuming the `placed` topic and writing to an in-process `Repository` | delivery through `topics.placed.publish`, retry then dead letter; the derived `Totals.round_trip` |
| `sessions/` | A `Cron` sweeping idle sessions | the sweep fired by an injected `tick`; the fake clock alone never fires it; the derived `Sessions.round_trip` over `any Instant` |
| `rates/` | A `Cache` with per-entry ttl | an entry served before and gone after its ttl under the fake clock |
| `refunds/` | A `Workflow` with one `approve` step | `record_workflow`, then `replay_workflow` from the journal; a mismatched history is `Replay` |

Run any of them from its directory with the `jip` command built from
`compiler/`:

```text
cd compiler && go build -o bin/jip ./cmd/jip && cd ../examples/refunds
../../compiler/bin/jip test       # every scenario and derived check
../../compiler/bin/jip check      # types, interfaces, externs, vendored hashes
../../compiler/bin/jip build      # .jip/bin/<name>
../../compiler/bin/jip context refunds.flow
```

`go test ./driver -run TestExamples` in `compiler/` runs the same loop over
every project: formatted, checked, scenarios passing, built.
