package checker

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/codingsar/jippitese/compiler/app"
	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/diag"
	"github.com/codingsar/jippitese/compiler/parser"
)

// loadDir builds an Input from a fixture app root the way the modules
// package will: one module per directory, scenarios.jip as <path>.scenarios,
// vendor/<alias>/ as the dependency root module <alias>.
func loadDir(t *testing.T, root string, bag *diag.Bag) *Input {
	t.Helper()
	in := &Input{}
	byPath := map[string]*ModuleSrc{}
	var order []string
	addFile := func(modPath, dir string, f *ast.File, scen, dep bool) {
		key := modPath
		if scen {
			key = modPath + ".scenarios"
		}
		m := byPath[key]
		if m == nil {
			m = &ModuleSrc{Path: key, Dir: dir, IsScenarios: scen, IsDep: dep}
			byPath[key] = m
			order = append(order, key)
		}
		m.Files = append(m.Files, f)
	}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".jip") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		bag.AddSource(rel, string(src))
		f, err := parser.ParseFile(rel, string(src))
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		if rel == "app.jip" {
			in.AppFile = f
			a, err := app.Parse(f)
			if err != nil {
				t.Fatalf("app.jip: %v", err)
			}
			in.App = a
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		parts := strings.Split(dir, "/")
		dep := false
		if parts[0] == "vendor" {
			dep = true
			parts = parts[1:]
		}
		addFile(strings.Join(parts, "."), dir, f, filepath.Base(rel) == "scenarios.jip", dep)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range order {
		in.Modules = append(in.Modules, byPath[k])
	}
	return in
}

func checkDir(t *testing.T, root string) (*Program, *diag.Bag, string) {
	t.Helper()
	bag := diag.NewBag()
	in := loadDir(t, root, bag)
	p := Check(in, bag)
	var out bytes.Buffer
	bag.Format(&out, true, true)
	return p, bag, out.String()
}

func TestCardApp(t *testing.T) {
	_, bag, out := checkDir(t, "testdata/orders")
	if bag.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", out)
	}
	// The card deliberately keeps two unused imports (09 rule 10 shows the
	// `-- orders.customers (imported, nothing referenced)` tier for them).
	if got := codes(out); strings.Join(got, ",") != "W0102,W0102" {
		t.Errorf("unexpected diagnostics %v:\n%s", got, out)
	}
}

// snippetBase gives every snippet the card types that types.jip refers to.
const snippetBase = `
/// Money in minor units.
pub type Money(Int)
/// A customer order.
pub type Order { id: OrderId, lines: List<Line>, total: Money }
/// Why an order could not be billed.
pub enum BillError { NoFunds(Money), Declined(Str) }
/// Withdraws amt.
pub fn withdraw(acc: Account, amt: Money) -> Result<Account, BillError> {
  if acc.balance.0 < amt.0 { return Err(NoFunds(amt)) }
  Ok(Account { ..acc, balance: Money(acc.balance.0 - amt.0) })
}
`

// checkSnippet checks one billing-module file (plus the fixture types) with
// the fixture app and returns the formatted diagnostics.
func checkSnippet(t *testing.T, body string, scenarios string) string {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	copyFile := func(from, to string) {
		src, err := os.ReadFile(from)
		must(err)
		must(os.MkdirAll(filepath.Dir(to), 0o755))
		must(os.WriteFile(to, src, 0o644))
	}
	copyFile("testdata/orders/app.jip", filepath.Join(dir, "app.jip"))
	copyFile("testdata/orders/vendor/http/http.jip", filepath.Join(dir, "vendor/http/http.jip"))
	copyFile("testdata/orders/orders/billing/types.jip", filepath.Join(dir, "orders/billing/types.jip"))
	copyFile("testdata/orders/orders/customers/customers.jip", filepath.Join(dir, "orders/customers/customers.jip"))
	must(os.WriteFile(filepath.Join(dir, "orders/billing/billing.jip"), []byte("module orders.billing\n"+body+snippetBase), 0o644))
	if scenarios != "" {
		must(os.WriteFile(filepath.Join(dir, "orders/billing/scenarios.jip"), []byte("module orders.billing.scenarios\nuse orders.billing\n"+scenarios), 0o644))
	}
	_, _, out := checkDir(t, dir)
	return out
}

func codes(out string) []string {
	var cs []string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "["); i >= 0 && (strings.HasPrefix(line, "error[") || strings.HasPrefix(line, "warning[")) {
			cs = append(cs, line[i+1:i+6])
		}
	}
	sort.Strings(cs)
	return cs
}

func expectDiag(t *testing.T, out string, wantCodes []string, wantText ...string) {
	t.Helper()
	got := codes(out)
	sort.Strings(wantCodes)
	if strings.Join(got, ",") != strings.Join(wantCodes, ",") {
		t.Errorf("codes: got %v, want %v\n%s", got, wantCodes, out)
	}
	for _, w := range wantText {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestNonExhaustiveMatch(t *testing.T) {
	out := checkSnippet(t, `
/// Shape.
pub enum Shape { Flat, Cube(Int), Box { w: Int, h: Int } }

/// Area.
pub fn area(s: Shape) -> Int {
  match s {
    Flat => 0,
    Cube(n) => n * n,
  }
}
`, "")
	expectDiag(t, out, []string{"E0420"}, "non-exhaustive match on `Shape`", "missing `Box { .. }`")
}

func TestNewtypeMismatch(t *testing.T) {
	out := checkSnippet(t, `
/// Price with fee.
pub fn with_fee(p: Money, fee: Money) -> Money { Money(p.0 + fee.0) }

/// Caller.
pub fn total_fee(p: Money) -> Money { with_fee(p, 250) }
`, "")
	expectDiag(t, out, []string{"E0402"}, "expected `Money`, found `Int`", "with_fee(p, Money(250))")
}

func TestMissingEffect(t *testing.T) {
	out := checkSnippet(t, `
/// Reads the counter.
pub fn read_count(c: Cell<Int>) -> Int { c.get() }
`, "")
	expectDiag(t, out, []string{"E0412"}, "missing effect `mut`", "`read_count` declares no effect set", "-> Int with {mut}")
}

func TestUnusedEffect(t *testing.T) {
	out := checkSnippet(t, `
/// Pure but declares io.
pub fn twice(n: Int) -> Int with {io} { n * 2 }
`, "")
	expectDiag(t, out, []string{"W0401"}, "unused effect `io`")
}

func TestImplMissingMethod(t *testing.T) {
	out := checkSnippet(t, `
/// Index error.
pub enum IndexError { Bad }
/// Event.
pub type OrderEvent { id: Int }
/// Indexer.
pub type Indexer { n: Int }

/// Indexes orders.
impl Worker for Indexer {
  type Msg = OrderEvent
  type E = IndexError
  const retry = Retry { max: 3, backoff: Backoff.Exp(1s) }
  fn init(cfg: Config) -> Result<Self, IndexError> { Ok(Indexer { n: 0 }) }
}
`, "")
	expectDiag(t, out, []string{"E0701"}, "`impl Worker for Indexer` is missing `perform`", "Worker requires `fn perform(self, m: Msg) -> Result<(), E>", "fn perform(self, m: OrderEvent) -> Result<(), IndexError> with {io, net, mut, llm, decide, nondet} { … }")
}

func TestImplBudgetExceeded(t *testing.T) {
	out := checkSnippet(t, `
/// Index error.
pub enum IndexError { Bad }
/// Event.
pub type OrderEvent { id: Int }
/// Indexer.
pub type Indexer { n: Int }

/// Indexes orders.
impl Worker for Indexer {
  type Msg = OrderEvent
  type E = IndexError
  const retry = Retry { max: 3, backoff: Backoff.Exp(1s) }
  fn init(cfg: Config) -> Result<Self, IndexError> { Ok(Indexer { n: 0 }) }
  fn perform(self, m: OrderEvent) -> Result<(), IndexError> with {spawn} { spawn { Ok(()) } }
}
`, "")
	expectDiag(t, out, []string{"E0702"}, "effect `spawn` exceeds the budget of `Worker.perform`", "not in {io, net, mut, llm, decide, nondet}")
}

func TestDeferredBehaviour(t *testing.T) {
	out := checkSnippet(t, `
/// State.
pub type Light { on: Bool }
impl Machine for Light {
}
`, "")
	expectDiag(t, out, []string{"E0706"}, "`Machine` is not available in v0")
}

func TestThenExpectsBool(t *testing.T) {
	out := checkSnippet(t, "", `
scenario "withdraw result" {
  given acc = Account { id: AccountId(1), balance: Money(500) }
  when  r = withdraw(acc, Money(200))
  then  r
}
`)
	expectDiag(t, out, []string{"E0801"}, "`then` expects `Bool`", "found `Result<Account, BillError>`", "replace `r` with `r.is_ok()`")
}

func TestPrivateAccess(t *testing.T) {
	out := checkSnippet(t, `
use orders.customers
/// Uses a private item.
pub fn peek(c: customers.Customer) -> Int { customers.hidden(c) }
`, "")
	expectDiag(t, out, []string{"E0408"}, "is private to `orders.customers`")
}

func TestWorkflowMutRejected(t *testing.T) {
	out := checkSnippet(t, `
/// Counter.
pub type Counter { c: Cell<Int> }
/// Bumps.
fn bump(c: Cell<Int>) -> Int with {mut} { c.update(fn(n) => n + 1) }
/// Workflow with state.
impl Workflow for Counter {
  type Input = Int
  type Output = Int
  type Signal = Cancel
  type E = BillError
  const version = 1
  fn init(cfg: Config) -> Result<Self, BillError> with {io} { Ok(Counter { c: Cell.new(0) }) }
  fn run(self, input: Int) -> Result<Int, WorkflowError<BillError>> with {mut} { Ok(bump(self.c)) }
}
`, "")
	expectDiag(t, out, []string{"E0702", "E0703"}, "effect `mut` is not allowed inside `Workflow.run`")
}
