# stdlib

The v0 standard library lives with the compiler, in `compiler/stdlib/`,
and is embedded in the `jip` binary. Every function is an `extern fn … =
go "rt".<Ident>` over the runtime package `compiler/rt`, so the standard
library has no Go dependency outside the Go standard library.

| Module | File | Contents |
|---|---|---|
| prelude | `prelude.jip` | `Option`, `Result`, the six v0 behaviours with their budgets and defaults, `Health`, `Retry`, `Schedule`, the error enums, methods on the primitives and on `List`, `Map`, `Set`, `MutList`, `MutMap`, `MutSet`, `Cell`, `Task`, `Topic`, `Query`, `Gen` |
| `std.time` | `time.jip` | `Instant`, `now`, `sleep` |
| `std.json` | `json.jip` | `encode`, `decode`, `Error` |
| `std.gen` | `gen.jip` | generator helpers for `any … via` |
| `std.testing` | `testing.jip` | `FakeHttp`, `FakeClock`, `FakeRandom`, `FakeLlm`, `FakeDecide`, `FakeApprove`, `MemRepo`, `MemCache`, `Journal`, `tmp_path`, `write_file` |

Scenario builtins that need an impl name (`run_workflow`,
`record_workflow`, `replay_workflow`, `tick`, `send_signal`) are resolved
by the checker rather than declared here.

This directory stays a pointer until the standard library outgrows the
compiler tree (Phase F onward).
