# 10 — Configuration

One file, `app.jip`, at the app root. It is a typed record, checked by the
compiler, overlaid by exactly three layers at startup.

## Example

```jip
app {
  name: "orders"
  config { gateway_url: Url, search: Url, db: Path }   // the `Config` record every `init` receives
  deps {
    http  = git("https://github.com/acme/jip-http",  commit: "8f1c2a7", hash: "sha256-Qm…")
    pgsql = git("https://github.com/acme/jip-pgsql", commit: "3be09d4", hash: "sha256-7a…")
    pdf   = gomod("github.com/acme/gopdf",           commit: "c0ffee1", hash: "sha256-Xk…")
  }
  models { smart = openai("gpt-5"), fast = jev("jev-1-small"), local = ollama("local-model") }
  secrets { gateway_key: secret<Str>, db_url: secret<Url> }   // JIP_SECRETS__GATEWAY_KEY
  effects {
    net  { timeout: default 5s, retry: default { max: 3, backoff: exp, base: 200ms }, ratelimit: default { per_second: 50.0 } }
    llm  { timeout: default 60s, budget: default 2_000_000 tokens }
    io   { timeout: default 10s }
    decide { timeout: default 500ms }
    approve { channel: orders.review_inbox, default_timeout: default 48h }
  }
  telemetry { exporter: otlp, endpoint: default "http://localhost:4317", sample: default 0.1 }
  externs { allow: [orders.pdf], max: 10 }
  contracts: pre
  workflow { mode: embedded, store: default ".jip/workflow" }
  topics { paid: Topic<Receipt> }
  run BillingApi { restart: permanent, max_restarts: 3, period: 60s, shutdown_timeout: 10s, port: default 8080 }
  run Billing
  env prod { telemetry.sample = 0.01, effects.net.retry.max = 5 }
}
```

This replaces a `config.yaml` plus a `viper` loader (or `dotenv` in TS):

```go
// config.yaml + viper: stringly typed, unknown keys ignored, env mapping by convention
viper.SetConfigName("config"); viper.AutomaticEnv()
viper.SetEnvKeyReplacer(strings.NewReplacer(".", "__"))
sample := viper.GetFloat64("telemetry.sample")   // 0 if the key was misspelled
```

In jippitese a misspelled field is a compile error and a misspelled variable
aborts startup.

## Rules

1. **One file.** `app.jip` sits at the app root and contains exactly one
   `app { … }` declaration, a record of stdlib type `App`. No other `.jip`
   file may contain `app`. There are no per-environment files.
2. **Closed schema.** Every field has a declared type in `App`. An unknown
   field is a compile error (E0301). Literals are parsed by the field's type;
   there is no implicit typing: `"0.1"` is not `0.1`, `no` is not `false`.
3. **Fields.** All optional unless marked required.
   - `name: Str` (required).
   - `config { field: Type }` (R5): the application's own settings, each
     field a schema-derivable type (section 02). The stdlib record `Config`
     that every behaviour's `init(cfg: Config)` receives (section 06) is
     exactly these fields plus one field `secrets` holding the `secrets`
     block; `cfg.gateway_url`, `cfg.secrets.gateway_key`. A leaf may carry a
     literal, with or without `default`; one without a literal is required
     from the overlay (rule 5). An empty or absent block gives `Config` only
     `secrets`.
   - `deps { alias = git(url: Str, commit: Str, hash: Str) | gomod(path: Str, commit: Str, hash: Str) }`.
   - `models { role = Provider }` with `Provider ::= openai(Str) | anthropic(Str) | jev(Str) | ollama(Str)`. Every role used in code as `llm<role>` or `decide<role>` must be declared (E0307). **Deferred:** a `custom(url, model)` provider is not in v0.
   - `secrets { name: secret<T> }`, `T` one of `Str`, `Int`, `Url`, `Bytes`. A secret field never carries a literal (E0302); its value comes only from `JIP_SECRETS__<NAME>`.
   - `effects { net { timeout: Duration, retry { max: Int, backoff: exp | const, base: Duration }, ratelimit { per_second: Float } } llm { timeout: Duration, budget: Tokens | Money, ratelimit { per_second: Float } } io { timeout: Duration } decide { timeout: Duration } approve { channel: ModulePath, default_timeout: Duration } }`. `approve.channel` names an app or dependency module implementing `std.approve.Channel` (section 07 rule 7); the stdlib ships `std.approve.http`. Defaults: `net.timeout 5s`, `net.retry { max: 3, backoff: exp, base: 100ms }`, `llm.timeout 60s`, `io.timeout 10s`, `decide.timeout 1s`, `approve.default_timeout 24h`; no ratelimit, no llm budget, retries only on `net`. These are the defaults sections 06 and 07 cite.
   - `telemetry { exporter: otlp | prometheus | none, endpoint: Str, sample: Float }`. Defaults `none`, `""`, `0.1`.
   - `externs { allow: List<ModulePath>, max: Int }`, module paths unquoted. Defaults `[]`, `0` (§11).
   - `contracts: all | pre | none`, default `pre` (`jip test` forces `all`).
   - `workflow { mode: embedded, store: Path }`, defaults `embedded` and `.jip/workflow` (a directory: one JSON-lines journal per run, §07 rule 16). **Deferred:** `mode: temporal | restate` are not in v0; the field exists so that adding a runtime later is a value, not a schema change.
   - `topics { name: Topic<T> }`: one declaration per topic that `Worker` impls chain over (§06).
   - `run <Impl> { restart: permanent | transient | temporary, max_restarts: Int, period: Duration, shutdown_timeout: Duration, topic: <name>, port: Int, ttl: Duration, idle: Duration }`: one child spec per `impl` instance of `Service`, `Worker`, `Cron`, `Workflow`, `Repository`, `Cache` or `Machine` (§06 rule 13). Defaults `permanent`, `3`, `60s`, `10s`; `topic` only for a `Worker`, `port` only for a `Service` (default `8080`), `ttl` only for a `Cache` (default `5m`), `idle` only for a `Machine` (default `5m`); a field on the wrong kind of impl is E0308. `run Billing` with no block takes all defaults. An impl without a `run` line is never started; a `run` naming no impl is E0308.
   - `env <name> { path.to.field = value }`: an overlay block, any number, distinct names.
4. **`default` marker.** `default` before a scalar literal (or a record of
   scalars) marks that leaf overridable. A leaf without `default` is fixed:
   it is a constraint every layer must satisfy.
5. **Overlay rule.** Exactly three layers, later wins where the earlier is a
   default: `app` body < `env <name>` block, where `<name>` equals the
   process variable `JIP_ENV` < process variables `JIP_<PATH>`, `<PATH>`
   being the field path upper-cased with `__` between segments
   (`JIP_TELEMETRY__SAMPLE`, `JIP_EFFECTS__NET__RETRY__MAX`,
   `JIP_SECRETS__GATEWAY_KEY`). Only `default`-marked scalar leaves and
   `secret<T>` fields can be set by the last two layers. An `env` block
   touching a non-default field is a compile error (E0303); a `JIP_*`
   variable touching one aborts startup. An unknown `JIP_*` variable, or one
   that fails to parse by the declared type, aborts startup before the first
   effect runs, with the variable name and expected type. `JIP_ENV` unset
   means no `env` block applies; `JIP_ENV` naming no block aborts startup.
6. **Effective config.** `jip check --effective --env <name>` resolves all
   three layers (reading the current process environment for layer three)
   and prints one line per leaf with its value and source layer. Secrets
   print as `<set>` or `<unset>`, never their value.
7. **Dependencies.** `hash` is the truth and `url` a mirror: `jip fetch`
   downloads by URL, computes the tree hash and refuses a mismatch; `jip
   fetch --save` writes the computed hash into `app.jip`. A `git(...)` or
   `gomod(...)` without `hash` is a compile error (E0306). Dependencies are
   vendored flat under `vendor/<alias>`; the vendored tree is committed, and
   `jip check` re-hashes it against `app.jip` (E0309 on mismatch). A
   `gomod(path)` is cloned from `https://<path>`; a module hosted elsewhere
   is reached through git's `url.<base>.insteadOf`. Its vendored `go.mod`
   must declare `module <path>` (E0309), and every module it requires needs
   a `gomod` entry of its own (E0504). For the `gomod(...)` entries `jip
   build` links `.jip/gen/vendor/<path>` to `vendor/<alias>` and
   synthesises `.jip/gen/vendor/modules.txt` listing their packages,
   because `go build -mod=vendor` rejects a vendor tree without it; both
   are generated, never edited.
8. **One commit per URL.** A dependency's own `app.jip` contributes only its
   `deps {}` and `externs {}` blocks to the graph. Across the whole graph each
   URL resolves to one commit. Two different commits for one URL (a diamond)
   is a compile error (E0305); the root fixes it by declaring that URL in its
   own `deps {}` with one commit, which then wins for the whole graph. There
   is no minimal-version selection and no registry; the stdlib is the only
   first-party dependency and is not declared.

### `jip check --effective --env prod` output

```text
app "orders"  env: prod  (JIP_ENV=prod)
  name                              "orders"                    app
  config.gateway_url                "https://pay.example/charge" JIP_CONFIG__GATEWAY_URL
  models.smart                      openai("gpt-5")             app
  secrets.gateway_key               <set>                       JIP_SECRETS__GATEWAY_KEY
  secrets.db_url                    <unset>                     -
  effects.net.timeout               5s                          app (default)
  effects.net.retry.max             5                           env prod
  effects.net.retry.backoff         exp                         app (default)
  telemetry.exporter                otlp                        app
  telemetry.endpoint                "http://collector:4317"     JIP_TELEMETRY__ENDPOINT
  telemetry.sample                  0.01                        env prod
  contracts                         pre                         app
  workflow.mode                     embedded                    app
  run.BillingApi.restart            permanent                   app
  run.BillingApi.port               8080                        app (default)
instrumented: net 3 sites, llm 1 site, decide 1 site, approve 1 site
```

Source column values: `app`, `app (default)`, `env <name>`, the variable
name, or `-` for an unset secret.

## Diagnostics

```text
error[E0301]: unknown field `telemetry.sampling` in `app`
  --> app.jip:17:48
   |
17 |   telemetry { exporter: otlp, endpoint: default "http://localhost:4317", sampling: default 0.1 }
   |                                                                          ^^^^^^^^ not a field of `Telemetry`
   = note: fields of `Telemetry`: exporter, endpoint, sample
   = help: rename to `sample`
```

```text
error[E0302]: literal in a secret field
  --> app.jip:9:26
   |
 9 |   secrets { gateway_key: secret<Str> = "sk-live-…" }
   |                          ^^^^^^^^^^^^^^^^^^^^^^^^^ `secret<Str>` takes no value
   = help: remove the literal and set `JIP_SECRETS__GATEWAY_KEY` in the process environment
```

```text
error[E0303]: overlay sets a field that is not `default`
  --> app.jip:27:14
   |
27 |   env test { telemetry.exporter = none }
   |              ^^^^^^^^^^^^^^^^^^ `telemetry.exporter` is fixed at `otlp` (app.jip:17:25)
   = help: mark it overridable: `exporter: default otlp`
```

The same violation from the environment aborts startup:

```text
jip: startup aborted: JIP_TELEMETRY__EXPORTER sets a non-default field `telemetry.exporter` (fixed at app.jip:17:25)
jip: startup aborted: JIP_TELEMETRY__SAMPEL matches no field (did you mean JIP_TELEMETRY__SAMPLE?)
jip: startup aborted: JIP_RUN__BILLINGAPI__PORT="80a0" does not parse as Int
jip: startup aborted: JIP_CONFIG__GATEWAY_URL is unset and `config.gateway_url` has no literal
```

```text
error[E0305]: dependency `https://github.com/acme/jip-log` is required at two commits
  --> app.jip:4:5
   |
 4 |     http  = git("https://github.com/acme/jip-http",  commit: "8f1c2a7", hash: "sha256-Qm…")
   |     ^^^^ requires it at commit 1a2b3c4 (vendor/http/app.jip:3)
 5 |     pgsql = git("https://github.com/acme/jip-pgsql", commit: "3be09d4", hash: "sha256-7a…")
   |     ^^^^^ requires it at commit 9d8e7f6 (vendor/pgsql/app.jip:4)
   = note: one commit per URL across the dependency graph; commits are not ordered
   = help: pin it at the root: add `log = git("https://github.com/acme/jip-log", commit: "9d8e7f6", hash: "…")` to `deps {}` and run `jip fetch --save`
```

## Grammar

```text
AppDecl     ::= "app" "{" AppField* "}"
AppField    ::= Ident ":" Value NEWLINE
              | Ident "{" AppField* "}" NEWLINE
              | ConfigBlock | DepsBlock | ModelsBlock | SecretsBlock | ExternsBlock
              | WorkflowBlock | TopicsBlock | RunSpec | EnvBlock
ConfigBlock ::= "config" "{" (Ident ":" Type ("=" Value)? ("," Ident ":" Type ("=" Value)?)*)? "}"
Value       ::= "default"? Scalar | "default"? "{" (Ident ":" Value ("," Ident ":" Value)*)? "}"
Scalar      ::= Str | Int | Float | Bool | Duration | Ident | Int "tokens" | "Money" "(" Int ")"
DepsBlock   ::= "deps" "{" (Ident "=" DepSource NEWLINE)* "}"
DepSource   ::= ("git" | "gomod") "(" Str "," "commit" ":" Str "," "hash" ":" Str ")"
ModelsBlock ::= "models" "{" (Ident "=" Provider ("," Ident "=" Provider)*)? "}"
Provider    ::= ("openai" | "anthropic" | "jev" | "ollama") "(" Str ")"
SecretsBlock::= "secrets" "{" (Ident ":" "secret" "<" Type ">" ("," Ident ":" "secret" "<" Type ">")*)? "}"
ExternsBlock::= "externs" "{" "allow" ":" "[" (ModulePath ("," ModulePath)*)? "]" ("," "max" ":" Int)? "}"
WorkflowBlock::= "workflow" "{" "mode" ":" "embedded" ("," "store" ":" "default"? Str)? "}"
TopicsBlock ::= "topics" "{" (Ident ":" "Topic" "<" Type ">")* "}"
RunSpec     ::= "run" TypeName ("{" RunField ("," RunField)* "}")? NEWLINE
RunField    ::= "restart" ":" ("permanent" | "transient" | "temporary")
              | "max_restarts" ":" Int | "period" ":" Duration | "shutdown_timeout" ":" Duration
              | "topic" ":" Ident | "port" ":" "default"? Int | "ttl" ":" "default"? Duration
EnvBlock    ::= "env" Ident "{" (FieldPath "=" Scalar ("," FieldPath "=" Scalar)*)? "}"
FieldPath   ::= Ident ("." Ident)*
```
