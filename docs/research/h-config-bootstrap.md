# (h) Config and bootstrap

Status: research output for `docs/decisions.md` §10 and the deferred "Config
env overlay rule". Direct fetch of primary docs was proxy-blocked; findings
come from search excerpts of the official pages in Sources.

## Summary

- The systems that work share three traits: a **schema owned by the
  compiler** (Encore generates CUE from the Go type; Wasp 0.24 dropped its DSL
  for TypeScript; StrictYAML parses nothing without a schema), **validation
  before run**, and **errors with file positions** (CUE: one `file:line:col`
  per conflicting location).
- Per-environment variation is always a *typed overlay*: Encore's
  `#Meta.Environment` conditionals, Wasp's `.env.server`/`.env.client` split,
  Dapr's `Configuration` resource. Secrets are a separate channel everywhere.
- Registry-free dependencies work when the **content hash, not the URL, is the
  truth** (Zig, Nix `narHash`, `go.sum h1:`) and fail without one flat
  resolution (GOPATH `vendor/` diamonds; Deno's HTTP-imports post-mortem).
- One-line switches are proven (Spring Boot, OTel agent, Dapr, Encore) and
  share one pitfall, **invisible activation**; every mature system answers it
  with an explain surface (Spring `/actuator/conditions`, OTel per-instrumentation
  disable, Dapr resiliency metrics).

## Findings (per system)

**Encore (infra from code).** An "Application Model" is built by static
analysis without running code; `sqldb.NewDatabase`, Pub/Sub topics and cron
jobs are discovered from declarations and invalid ones are reported at build.
`config.Load[T]()` must sit at package level; Encore generates
`encore.gen.cue` from `T`, unifies it with the service's `.cue` files and
validates at compile time. Environment variation is CUE conditionals on
`#Meta.Environment.Type ∈ {production, development, ephemeral, test}`. Secrets
are declared in code, set per environment *type* via CLI (env-specific beats
type), with `.secrets.local.cue` as a one-machine override. Tracing is
compiler-inserted with zero code, but OTLP export to third-party backends was
still "coming soon": the switch is bound to a proprietary dashboard.

**Wasp.** The `main.wasp` DSL (`app`, `route`, `page`, `query`, `auth`) was
retired in 0.24 for `main.wasp.ts`: ordinary TypeScript, type-checked by the
editor, splittable across files. Env: `.env.server` (secret, uncommitted) and
`.env.client` (public); only `REACT_APP_*` reaches the client; 0.16+ validates
both against declared schemas. Lesson: a bespoke DSL lost to "config is code
in the host type system".

**StrictYAML.** Removes implicit typing (Norway problem: `no` → `false`),
duplicate keys, anchors/refs, tags and flow style, each a named exception.
Without a schema every scalar is a string; types come only from a
`Map/Seq/Int/Bool/Str` schema. Lesson: the parser never guesses.

**CUE.** Types and values share one lattice; unification is the only
combinator and is order-independent; a conflict is bottom `_|_`, reported as
`conflicting values X and Y (mismatched types …)` with one position per
contributing file. Defaults are disjunction markers (`bool | *false`).
Lessons: overlays never silently clobber; a default may be replaced by any
layer, a non-default is a constraint every layer must satisfy.

**Go pre-modules (GOPATH + vendor).** No per-project pinning; the 1.5 vendor
experiment shipped with no tool; nested `vendor/` directories linked multiple
copies of one package (golang/go#20481: nil-pointer panic from a doubled
singleton). Vendoring without one flat resolution is the failure mode.

**Go modules.** Git-resolved; `go.sum` holds `h1:` SHA-256 of the module
tree; `sum.golang.org` is consulted only before adding a line;
`GOPRIVATE`/`GONOSUMDB` opt out. `-mod=vendor` requires `vendor/modules.txt`
to match `go.mod` exactly (strict since 1.14). Commits pin as pseudo-versions
`v0.0.0-<time>-<sha>`.

**Deno.** Pinned URLs "lock you in to one exact version until you manually
update the URL"; large projects accumulate several variants of one dependency;
Deno now recommends import maps and JSR, `deno.lock` hashes by default, and
`--frozen` to fail on drift.

**Zig.** `build.zig.zon` entries carry `.url` and `.hash`; "the hash is the
source of truth, and the URL is just one of many possible mirrors";
`zig fetch --save` computes it; a missing hash is a build error; `zig build
--fetch` enables offline builds; no registry.

**Nix.** `flake.lock` stores `locked{rev, narHash}` beside `original{ref}`;
pure evaluation refuses unhashed fetches.

**Cross-cutting switches.** Spring Boot auto-config fires on
`@ConditionalOnClass/@ConditionalOnMissingBean`; teams treat it as "black-box
magic", so Boot ships `--debug` and `/actuator/conditions` (positive and
negative matches). The OTel Java agent instruments all JDBC/HTTP/gRPC by
default; cost is volume-driven, so the knobs are sampling and
per-instrumentation disable. Dapr: `resiliency.yaml` names `timeouts`,
`retries`, `circuitBreakers` policies bound to `targets`; tracing is
`Configuration.spec.tracing{samplingRate, otel.endpointAddress}`, and
`OTEL_EXPORTER_OTLP_ENDPOINT` alone switches it on.

## Trade-offs

| Choice | For | Against |
|---|---|---|
| Config as a typed language declaration (Encore, Wasp-TS) | compiler validation, one parser, positions in errors | any environment branching needs a sub-language (CUE conditionals) |
| Env-var overlay (12-factor, OTel, Dapr) | ops-standard, no file per env | stringly typed; silent typos; must be whitelisted and parsed by the declared type |
| Unification overlay (CUE) | conflicts are errors, order-free | needs the default/constraint distinction in the schema |
| Hash-as-truth deps (Zig, Nix, go.sum) | offline builds, mirror-independent, tamper-evident | no semver → diamonds must be resolved by the root, by hand |
| Auto-instrumentation switches | zero call-site code; matches "effects are instrumentation points" | invisibility; volume-driven cost; vendor lock when export is proprietary |

## Recommendations for jippitese

1. **Schema.** `app { ... }` is a stdlib-typed record: an unknown field is a
   compile error, no implicit typing (StrictYAML), secrets are `secret<T>` and
   never carry a literal.
2. **Overlay rule (resolves the deferred item).** Exactly three layers, fixed
   order, later wins only where the earlier layer is a *default*:
   `app` body → `env <name> { ... }` block selected by `JIP_ENV` → process env
   vars `JIP_<PATH>`, `<PATH>` being the field path upper-cased with `__`
   between segments (`JIP_TELEMETRY__SAMPLE`). Env vars may set only scalar
   leaves and `secret<T>` fields, are parsed by the declared type, and a
   `JIP_*` variable matching no field aborts startup. Fields without `default`
   are constraints: an overlay that changes them is a compile error (CUE).
   `jip check --effective --env prod` prints the resolved config with each
   value's source layer (Spring `/actuator/conditions` lesson).
3. **Dependencies.** `deps` entries are `git(url, commit, hash)`; the hash is
   the truth, the URL a mirror (Zig). `jip fetch --save` computes it; a missing
   hash is an error. Vendoring is flat (`vendor/<name>`), one commit per URL
   graph-wide; a transitive conflict is a compile error the root resolves by
   pinning (no MVS: commits have no order). `jip check` verifies the vendored
   tree hash (Go 1.14 strict consistency). Deno's evidence **amends §10**:
   imports name the alias (`deps.http`), never the URL, so a dependency moves
   in one line.
4. **Switches.** One field per effect-keyed concern: `telemetry`, `retry`,
   `timeout`, `ratelimit`. Export targets are open protocols (OTLP,
   Prometheus), never a product (Encore's "coming soon" lock-in). Defaults are
   conservative (sample 0.1, retries only on `net`); `jip check --effective`
   lists which effect sites each switch instruments. This **reopens §4's
   example**: `telemetry: otel` becomes `telemetry { exporter: otlp, endpoint,
   sample }`, not a bare atom.
5. **No per-environment files.** Encore's `.cue` sprawl and Wasp's `.env.*`
   pair drift or leak; every non-secret value lives in `app.jip`, secrets come
   only through env.

Proposed `app.jip`:

```jip
app {
  name: "orders"
  deps {
    http  = git("https://github.com/acme/jip-http",  commit: "8f1c2a7", hash: "sha256-Qm…")
    pgsql = git("https://github.com/acme/jip-pgsql", commit: "3be09d4", hash: "sha256-7a…")
  }
  models {
    smart = openai("gpt-5")       // role -> provider/model (§6)
    fast  = jev("jev-1-small")
  }
  secrets {
    db_url:  secret<Url>          // literal forbidden; env JIP_SECRETS__DB_URL
    llm_key: secret<Str>
  }
  effects {                       // one block per tracked effect
    net  { timeout: default 5s, retry: default { max: 3, backoff: exp } }
    llm  { timeout: default 60s, budget: default 2_000_000 tokens }
    io   { timeout: default 10s }
  }
  telemetry {
    exporter: otlp                 // open protocol only
    endpoint: default "http://localhost:4317"
    sample:   default 0.1
  }
  http { port: default 8080 }
  env prod {                      // overlay: may only replace `default` fields
    telemetry.sample = 0.01
    effects.net.retry.max = 5
  }
  env test { telemetry.exporter = none }
}
```

Overlay rule in one sentence: **`app` < `env $JIP_ENV` < `JIP_<PATH>` env
vars, only `default`-marked scalar leaves and secrets may be replaced, unknown
or ill-typed inputs abort before the first effect runs.**

## Sources

- Encore config: https://encore.dev/docs/go/develop/config ; https://pkg.go.dev/encore.dev/config
- Encore secrets: https://encore.dev/docs/go/primitives/secrets
- Encore application model / infra from code: https://encore.dev/docs/application-model
- Encore tracing (OTLP export "coming soon"): https://encore.dev/features/tracing
- Encore environments: https://encore.dev/docs/platform/deploy/environments
- Wasp DSL retirement and TS spec: https://wasp.sh/docs/guides/legacy/wasp-dsl ; https://wasp.sh/docs/general/spec
- Wasp env vars: https://wasp.sh/docs/project/env-vars
- StrictYAML features removed: https://hitchdev.com/strictyaml/features-removed/ ; https://pypi.org/project/strictyaml
- CUE spec (lattice, unification): https://cue.googlesource.com/cue/+/HEAD/doc/ref/impl.md ; https://cuelang.org/docs/about/
- CUE error positions: https://cuelang.org/docs/howto/handle-errors-go-api/ ; https://cue.googlesource.com/cue/+/0eb7cc590a24bb01024bc81f6cbdb0ef9898b3b2%5E%21
- GOPATH/vendoring pathologies: https://fossa.com/blog/pathologies-of-go-package-management.md ; https://github.com/golang/go/issues/20481 ; https://technology.blog.gov.uk/2015/02/24/solving-problems-with-dependency-management-in-go/
- Go modules vendor consistency: https://tip.golang.org/src/cmd/go/internal/modload/vendor.go?m=text ; https://github.com/golang/go/issues/33848
- go.sum / checksum DB: https://pkg.go.dev/cmd/go/internal/modfetch ; https://www.ardanlabs.com/blog/2020/02/modules-04-mirros-checksums-athens.html
- Deno HTTP imports post-mortem: https://deno.com/blog/http-imports ; https://docs.deno.com/runtime/reference/cli/link/
- Zig build.zig.zon: https://git.medv.io/zig/blob/master/doc/build.zig.zon.md.html ; https://ithelp.ithome.com.tw/articles/10355399
- Nix flakes lock: https://nixos.wiki/wiki/Flakes ; https://discourse.nixos.org/t/how-do-i-precalculate-the-narhash-for-a-flake-input/72987
- Spring Boot conditions report: https://docs.spring.io/spring-boot/api/rest/actuator/conditions.html ; https://codemia.io/knowledge-hub/path/configuration_using_annotation_springbootapplication
- OTel Java agent performance/disable: https://opentelemetry.io/docs/zero-code/java/agent/performance/
- OTel env vars / declarative config: https://signoz.io/docs/ingestion/opentelemetry-environment-variables/ ; https://repo.hex.pm/preview/otel/0.1.0/docs/configuration.md
- Dapr resiliency: https://docs.dapr.io/operations/resiliency/resiliency-overview/
- Dapr tracing config: https://docs.dapr.io/operations/observability/tracing/setup-tracing/ ; https://docs.dapr.io/operations/configuration/configuration-overview/
- Viper env key mapping: https://pkg.go.dev/github.com/go-bricks/bviper
