package emit

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/diag"
	"github.com/codingsar/jippitese/compiler/modules"
)

const cardRoot = "../checker/testdata/orders"

type edit struct{ file, old, new string }

// mutateCard copies the card, replaces old with new in the given file and
// returns the extern codes the copy produces.
func mutateCard(t *testing.T, file, old, new string) []string {
	t.Helper()
	return mutateCards(t, []edit{{file, old, new}})
}

func mutateCards(t *testing.T, edits []edit) []string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", cardRoot+"/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, out)
	}
	for _, ed := range edits {
		p := filepath.Join(dir, ed.file)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), ed.old) {
			t.Fatalf("%s does not contain %q", ed.file, ed.old)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(b), ed.old, ed.new, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return externCodes(t, dir)
}

func externCodes(t *testing.T, dir string) []string {
	t.Helper()
	bag := diag.NewBag()
	in, err := modules.Load(dir, bag)
	if err != nil {
		t.Fatal(err)
	}
	prog := checker.Check(in, bag)
	if bag.HasErrors() {
		var b strings.Builder
		bag.Format(&b, true, false)
		t.Fatalf("does not check:\n%s", b.String())
	}
	eb := diag.NewBag()
	CheckExterns(prog, dir, eb)
	var codes []string
	for _, d := range eb.Diags {
		codes = append(codes, d.Code)
	}
	sort.Strings(codes)
	return codes
}

func TestExternsCardClean(t *testing.T) {
	if got := externCodes(t, cardRoot); len(got) != 0 {
		t.Fatalf("card externs: %v", got)
	}
}

func TestExternRules(t *testing.T) {
	const billing = "orders/billing/billing.jip"
	const decl = `extern fn read_file(p: Path) -> Result<Bytes, IoError> with {io} = go "os".ReadFile`
	cases := []struct {
		name, file, old, new, want string
	}{
		{"floor", billing, decl, `extern fn read_file(p: Path) -> Result<Bytes, IoError> with {nondet} = go "os".ReadFile`, "E0501"},
		{"allow", "app.jip", "allow: [orders.billing]", "allow: [orders.customers]", "E0502"},
		{"receiver", billing, `go "os".ReadFile`, `go "os".File.Read`, "E0503"},
		{"no-receiver", billing, decl, decl + "\n/// Bad.\nextern fn bad() -> Float with {nondet} = go \"time\".Duration.Hours", "E0503"},
		{"not-a-type", billing, `go "os".ReadFile`, `go "os".ReadFile.Read`, "E0503"},
		{"no-method", billing, decl, decl + "\n/// Bad.\nextern fn bad(d: Duration) -> Float with {nondet} = go \"time\".Duration.Days", "E0506"},
		{"method-signature", billing, decl, decl + "\n/// Bad.\nextern fn bad(d: Duration) -> Int with {nondet} = go \"time\".Duration.Hours", "E0506"},
		{"path", billing, `go "os".ReadFile`, `go "github.com/x/y".ReadFile`, "E0504"},
		{"type", billing, decl, decl + "\n/// Bad.\nextern fn bad(c: Char) -> Int = go \"strconv\".Itoa", "E0505"},
		{"signature", billing, decl, decl + "\n/// Bad.\nextern fn bad(n: Str) -> Int = go \"strconv\".Itoa", "E0506"},
		{"scenario", "orders/billing/scenarios.jip", "@extern\n", "", "E0507"},
		{"context", billing, decl, `extern fn read_file(p: Path) -> Result<Bytes, IoError> with {net} = go "net/http".ReadFile`, "E0508"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mutateCard(t, c.file, c.old, c.new)
			for _, g := range got {
				if g == c.want {
					return
				}
			}
			t.Errorf("want %s, got %v", c.want, got)
		})
	}
}

// A method extern takes its receiver as the first parameter; the receiver
// converts to the Go named type (spec 11 rule 1).
func TestExternMethod(t *testing.T) {
	const billing = "orders/billing/billing.jip"
	const decl = `extern fn read_file(p: Path) -> Result<Bytes, IoError> with {io} = go "os".ReadFile`
	got := mutateCards(t, []edit{
		{"app.jip", "max: 1", "max: 3"},
		{billing, decl, decl + "\n/// Hours.\nextern fn hours(d: Duration) -> Float with {nondet} = go \"time\".Duration.Hours" +
			"\n/// Rounded.\nextern fn round(d: Duration, m: Duration) -> Duration with {nondet} = go \"time\".Duration.Round"},
	})
	for _, g := range got {
		if g != "E0507" {
			t.Fatalf("method externs check: %v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("both lack only an @extern scenario: %v", got)
	}
}
