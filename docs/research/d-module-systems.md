# (d) Context-bounded module systems

Status: research report for `docs/decisions.md` §9, §10, §15. Sources marked †
were unreachable from this sandbox and rest on search excerpts of the page.

## Summary

- Of the four systems, only OCaml has a human-written interface file; Elm and
  Go *derive* the API surface from source (docs JSON, export data), and Unison
  has no files at all. Derivation is the norm; jippitese's "extracted and
  committed" interface is the Elm/Go model made visible in git.
- Every system that enforces anything enforces it by **diffing the derived
  API** (`elm diff`/`elm publish`, `apidiff`/`gorelease`, OCaml's
  `.ml`-vs-`.cmi` check). None detects behavioural change.
- "Minimum text before changing module X" is best served by `go doc -short`
  (one line per symbol) and `.mli` (signatures + docs, abstract types hide
  representation). Unison's answer is a UI/LSP/MCP server, not text.
- **Verdict on content addressing: no for v1.** Unison's benefits are real but
  bought with a bespoke codebase format, a bespoke review surface that is still
  "in progress" after four years, and the loss of git/text tooling that is the
  entire human-review path for jippitese. Take the two cheap wins (content-keyed
  build cache, interface digests) and leave the rest.

## Findings (per system)

**Unison.** Definitions are identified by a hash of their AST; names are
metadata in a namespace, and the codebase is "stored as its AST in a database"
(SQLite since the v2 format). Claimed benefits: no build step, instant renames,
cached tests, "semantically-aware version control". Costs are acknowledged by
the project itself: "the Unison codebase not just being a
collection of text files means we don't get to use rudimentary text-based tools
to view that code", so the team built a code browser, Unison Share, an LSP and
an MCP server. Authoring goes through a *scratch file*: `add`/`update` push
typechecked definitions into the codebase; a type-changing `update` reopens the
broken dependents in a scratch file. Review:
`merge.preview /branch /main` lists terms a merge would change;
`diff.namespace a b` compares two namespaces; `diff.update` (UCM 1.0.2, Jan
2026) shows scratch-file-vs-codebase changes. Share "contributions" (PRs)
launched with "the feature set in its nascency"; the roadmap still lists
"Contributions v2 ... better diffs and support for code review" as in progress.
Diffs are definition-granular; there is no line-comment model.

**OCaml `.mli`.** The interface is a separate, **hand-written** file; `x.mli`
compiles to `x.cmi` and "the implementation x.ml is checked against the
corresponding compiled interface x.cmi". Absent an `.mli`, the compiler infers
a maximal interface (everything exported). Generation: `ocamlc -i` prints the
inferred signature but "is intended to be used as a starting point before human
editing", can emit invalid output (weak type variables) and needs every
dependency on the command line; `cmitomli` dumps the compiled `.cmi`;
Merlin/`ocaml-print-intf`/`infer-mli` do it from the editor. Dune has no rule. Drift is a
compile error: "The implementation X.ml does not match the interface X.cmi ...
Values do not match ... is not included in". The value of hand-writing is
*narrowing*: abstract types and hidden helpers that inference cannot guess.

**Elm.** No interface file. The API is the docs JSON generated from
`exposed-modules`; `elm diff` builds local docs, fetches the published docs and
prints "This is a MAJOR/MINOR/PATCH" with per-module `Added:`/`Removed:`/
`Changed:` sections (old signature on `-`, new on `+`). `elm publish` refuses a
version whose magnitude disagrees with the diff (`PublishBadBump`: "Your
elm.json says the next version should be 8.0.0 ... This does not match the API
diff") and also requires README, LICENSE and a matching git tag. Known holes: behaviour changes are invisible; a MINOR
addition can still break an unqualified-import consumer; a port that compiles
only on a new Elm counted as PATCH.

**Go.** No interface file; exported = capitalised. The compiler emits *export
data*, "a serialized representation of a types.Package ... names, kinds, types,
and locations of all exported declarations", which importers and gopls use to
type-check dependents without source. gopls moved to *shallow* export data
(one package's types) plus a file cache keyed on inputs and direct-dep types:
88% less memory, 65% faster warm start. For reading, `go doc` prints a
package's exported symbols, `-short` gives "one-line representation for each
symbol", `-all` full docs, `-u` unexported. For drift/compat, `apidiff`
"reports a change as incompatible if it could cause client code to stop
compiling" (precise rules for structs, interfaces, numeric widths) and
`gorelease` turns that into an exit status and a suggested version. Both live
in `x/exp`, not the `go` command.

## Trade-offs

| Axis | Unison (hash) | OCaml (hand `.mli`) | Elm/Go (derived) |
|---|---|---|---|
| Who writes interface | nobody; names are metadata | human | compiler |
| Hides representation | yes (by type) | yes (abstract types) | Go: unexported; Elm: opaque types |
| Drift detection | impossible by construction | compile error | diff tool (Elm: at publish; Go: opt-in) |
| Diff readability | definition-level, custom UI | text diff of `.mli` | text/JSON diff of derived API |
| Git/PR review | no (own Share) | yes | yes |
| Caching | perfect, by hash | per `.cmi` | Go build cache by content hash; gopls shallow export |
| Rename safety | free | manual | manual (gopls rename) |
| Agent authorship | needs MCP/UCM round-trip | plain text | plain text |

Content addressing for jippitese, benefit by benefit: *perfect caching* —
Go's build cache and gopls already key on content hashes of text; we inherit
it. *Rename safety* — a rename is a one-token change in a text interface plus
compiler-checked call sites; cheap. *No name conflicts* — only matters with a
registry; §10 has none. Costs: a database codebase, a bespoke review UI, no
`git blame`/PR comments, a workflow agents reach only through a tool server,
and "tokens to read" measured on a pretty-printed projection anyway.

## Recommendations for jippitese

1. **Close the deferred item: no content-addressed code in v1** (§9). Record
   two Unison-derived wins instead: (a) each committed interface file carries
   a content digest of the normalised interface, so `jip check` is "re-extract,
   re-hash, compare"; (b) the build cache keys on module-interface digests, so
   a body-only change never rebuilds dependents (Go's shallow export-data
   insight).
2. **Keep extraction, add narrowing.** Evidence from OCaml says the only thing
   a hand-written interface adds is hiding. Give `pub` what it needs to say
   that in source (`pub type T` with opaque body, `pub(module)`), and extract.
   Nobody edits the interface file; its git diff is what reviewers read, like an
   `.mli`.
3. **Interface file content = Elm docs JSON + effects + contracts.** Per `pub`
   item: one-line doc, full signature, declared effects, `pre`/`post`. Omit
   bodies, private items, imports. Sort canonically so diffs are stable.
4. **`jip check` classifies API changes** apidiff-style: `breaking` /
   `compatible` / `none` per module, printed in `elm diff` form (`Added:`,
   `Removed:`, `Changed:` with `-`/`+` lines). Gate on it in CI. This reopens
   §10's "no registry, pinned by commit": with no versions there is nothing
   for a magnitude to label, so the output labels *the PR*, and a dependency
   bump reports breaking changes against the vendored interface.
5. **Context pack = two tiers.** `jip context <m>` emits the module's full
   interface plus, for each *direct* dependency, only the items the module
   actually references and the transitive types those signatures mention
   (gopls's "shallow plus closure" shape, not whole dependency interfaces).
   `--short` gives `go doc -short` output (one line per symbol) for budgeting.
   Measure both in §13.
6. **Lean on text and git, not a Share.** Unison's four-year path to code
   review is the strongest evidence that the review surface must be the git
   diff of the interface file. Keep the LSP deferred (§15); the interface files
   *are* the shallow export data an LSP would need.

## Sources

- Unison README (content addressing, AST database, claimed benefits): https://github.com/unisonweb/unison
- † Unison "Here's what's been happening" (no text tools quote; SQLite codebase): https://unison-lang.org/blog/heres-whats-been-happening-with-unison
- † Unison 2021 year in review: https://unison-lang.org/blog/unison_2021-year-in-review
- † Reviewing a PR (`merge.preview`): https://unison-lang.org/docs/tooling/project-workflows/reviewing-prs
- † Introducing contributions ("nascency"): https://unison-lang.org/blog/introducing-contributions
- † New update process (`edit`/`update`/`merge`, scratch file): https://unison-lang.org/blog/new-update-process
- † UCM 1.0.2 (`diff.update`): https://www.unison-lang.org/blog/ucm102/
- † Unison roadmap (Contributions v2): https://www.unison-lang.org/roadmap/
- † Unison MCP setup: https://www.unison-lang.org/docs/usage-topics/mcp-setup
- † Projects docs ("not stored in files that can be grouped into git repositories"): https://www.unison-lang.org/docs/projects
- OCaml manual, batch compilation (`.mli`→`.cmi`, implementation checked against interface): https://github.com/ocaml/ocaml/blob/trunk/manual/src/cmds/comp.etex
- † Generating `.mli` from `.ml` (`ocamlc -i` caveats, `cmitomli`, Merlin): https://discuss.ocaml.org/t/how-can-i-generate-mli-files-from-ml-files/5427
- † Run `ocamlc -i` with dune: https://discuss.ocaml.org/t/run-ocamlc-i-with-dune/9876
- † Mismatch error example ("does not match the interface"): https://gitlab.com/gadmm/memprof-limits/-/issues/2
- Elm `elm.json` package docs (automatic semver, `elm bump`): https://github.com/elm/compiler/blob/master/docs/elm.json/package.md
- Elm `Diff.hs` (output format, magnitudes): https://github.com/elm/compiler/blob/master/terminal/src/Diff.hs
- Elm `Publish.hs` (publish checks, `PublishBadBump`): https://github.com/elm/compiler/blob/master/terminal/src/Publish.hs
- † Elm Discourse, major change but not interface (publish refusal message): https://discourse.elm-lang.org/t/major-change-to-package-but-not-the-interface/6622
- † elm-package semver rules and holes: https://libraries.io/hackage/elm-package
- Go `apidiff` README (compatibility rules, limitations): https://github.com/golang/exp/blob/master/apidiff/README.md
- Go `gorelease` docs: https://pkg.go.dev/golang.org/x/exp/cmd/gorelease
- Go `gcexportdata` (export data definition): https://pkg.go.dev/golang.org/x/tools/go/gcexportdata
- Go `cmd/doc` (`-all`, `-src`, `-cmd`): https://pkg.go.dev/cmd/doc
- † `go doc -short` / `-u` history: https://lab.nexedi.com/kirr/go/-/commit/fa42157d986c69664e2146b520ff45be204af8eb
- † gopls incremental type-checking / shallow export data: https://go.googlesource.com/tools/+/master/gopls/internal/cache/analysis.go and https://github.com/golang/go/commit/21d2256f2cdf2aa29551f16deb6a51975db463d9
