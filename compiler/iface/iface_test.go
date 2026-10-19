package iface

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/diag"
	"github.com/codingsar/jippitese/compiler/modules"
)

const cardRoot = "../checker/testdata/orders"

func load(t *testing.T, root string) *checker.Program {
	t.Helper()
	bag := diag.NewBag()
	in, err := modules.Load(root, bag)
	if err != nil {
		t.Fatal(err)
	}
	p := checker.Check(in, bag)
	if bag.HasErrors() {
		var b strings.Builder
		bag.Format(&b, true, false)
		t.Fatalf("does not check:\n%s", b.String())
	}
	return p
}

func billing(t *testing.T, root string) *Interface {
	return Extract(load(t, root).ByPath["orders.billing"])
}

func TestCardInterface(t *testing.T) {
	txt := billing(t, cardRoot).Text()
	for _, want := range []string{
		"interface orders.billing digest sha256:",
		"/// Money in minor units.\npub type Money(Int)\n",
		"pub type Order { id: OrderId, lines: List<Line>, total: Money }\n  inv  total.0 >= 0\n",
		"pub enum BillError { NoFunds(Money), Net(http.Error), Declined(Str) }",
		"pub fn charge(o: Order, gw: Gateway) -> Result<Receipt, BillError> with {net, nondet}\n",
		"pub fn max_by<T, K: Ord>(xs: List<T>, key: fn(T) -> K) -> Option<T>\n",
		"  pre  amt.0 > 0\n  post match ret { Ok(a) => a.balance.0 == acc.balance.0 - amt.0, Err(_) => true }\n",
		"impl Order\n  /// Number of lines on the order.\n  pub fn size(self) -> Int\n",
		"impl Workflow for Billing\n  type Input = Order\n",
		"  const version = 2\n",
		"impl Service for BillingApi\n",
		"  fn stop(self)\n",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("interface lacks %q\n---\n%s", want, txt)
		}
	}
	for _, absent := range []string{"both", "bump", "read_file", "= go"} {
		if strings.Contains(txt, absent) {
			t.Errorf("interface mentions private `%s`", absent)
		}
	}
	if strings.Contains(txt, "module-visible") {
		t.Errorf("card has no pub(module) items")
	}
}

func TestRoundTripStableDigest(t *testing.T) {
	a := billing(t, cardRoot)
	txt := a.Text()
	b, pub, _, err := Parse(txt)
	if err != nil {
		t.Fatal(err)
	}
	if b.Text() != txt {
		t.Fatalf("round trip differs:\n%s\n---\n%s", txt, b.Text())
	}
	if pub != Digest(a.Public) || Digest(b.Public) != pub {
		t.Fatalf("digest unstable")
	}
	if again := billing(t, cardRoot).Text(); again != txt {
		t.Fatalf("extraction is not deterministic")
	}
	if r := Classify(b, a); r.Level != None {
		t.Fatalf("self-classification: %s", r.Text())
	}
}

func mutate(t *testing.T, old, new string) *Interface {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", cardRoot+"/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, out)
	}
	p := filepath.Join(dir, "orders/billing/billing.jip")
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), old) {
		t.Fatalf("card lacks %q", old)
	}
	os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644)
	return billing(t, dir)
}

func TestClassification(t *testing.T) {
	base := billing(t, cardRoot)
	cases := []struct {
		name, old, new string
		want           Level
		reason         string
	}{
		{"body edit", "attempts += 1", "attempts += 2", None, ""},
		{"doc", "/// Sum of line totals.", "/// Sum of all line totals.", Compatible, "doc change"},
		{"added fn", "/// Shared counter", "/// New.\npub fn extra() -> Int { 1 }\n\n/// Shared counter", Compatible, ""},
		{"added effect", "-> Tier with {decide<fast>}", "-> Tier with {decide<fast>, nondet}", Breaking, "added effect"},
		{"changed sig", "pub fn total(lines: List<Line>) -> Money", "pub fn total(lines: List<Line>, n: Int) -> Money", Breaking, "changed signature"},
		{"added variant", "Paid, Failed }", "Paid, Failed, Refunded }", Breaking, "added variant on a non-opaque enum"},
		{"removed variant", "Paid, Failed }", "Paid }", Breaking, "removed or renamed variant"},
		{"strengthened pre", "pre  amt.0 > 0", "pre  amt.0 > 1", Breaking, "contract-changed"},
		{"removed pre", "  pre  amt.0 > 0\n", "", Compatible, "removed pre conjunct"},
		{"removed fn", "pub fn positive_money", "fn positive_money", Breaking, ""},
		{"version bump", "const version = 2", "const version = 3", Compatible, "changed const value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Classify(base, mutate(t, c.old, c.new))
			if r.Level != c.want {
				t.Fatalf("level %s, want %s\n%s", r.Level, c.want, r.Text())
			}
			if c.reason != "" && (len(r.Changed) == 0 || r.Changed[0].Reason != c.reason) {
				t.Fatalf("reason: want %q\n%s", c.reason, r.Text())
			}
		})
	}
}

func TestRemovedEffectIsCompatible(t *testing.T) {
	wider := mutate(t, "-> Tier with {decide<fast>}", "-> Tier with {decide<fast>, nondet}")
	r := Classify(wider, billing(t, cardRoot))
	if r.Level != Compatible || len(r.Changed) != 1 || r.Changed[0].Reason != "removed effect" {
		t.Fatalf("%s", r.Text())
	}
}

func TestContextPack(t *testing.T) {
	prog := load(t, cardRoot)
	out := Context(prog, prog.ByPath["orders.billing"], ContextOptions{})
	for _, want := range []string{
		"interface orders.billing digest sha256:",
		"-- std.json (referenced: encode, decode)\n",
		"pub fn encode<T>(v: T) -> Bytes\n",
		"-- http (referenced: Error, post, Body, get)\n",
		"pub enum Error { Timeout, Status(Int), Transport(Str) }\n",
		"pub type Resp { status: Int, body: Bytes }\n",
		"-- orders.customers (imported, nothing referenced)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("context lacks %q\n---\n%s", want, out)
		}
	}
	short := Context(prog, prog.ByPath["orders.billing"], ContextOptions{Short: true})
	if strings.Contains(short, "///") || !strings.Contains(short, "pub fn charge(") {
		t.Errorf("short form:\n%s", short)
	}
	narrowed := Context(prog, prog.ByPath["orders.billing"], ContextOptions{For: "types.jip"})
	if strings.Contains(narrowed, "pub fn charge(") || !strings.Contains(narrowed, "pub type Receipt") {
		t.Errorf("--for types.jip:\n%s", narrowed)
	}
}
