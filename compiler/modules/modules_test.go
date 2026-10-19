package modules

import (
	"bytes"
	"strings"
	"testing"

	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/diag"
)

func TestLoadCard(t *testing.T) {
	bag := diag.NewBag()
	in, err := Load("../checker/testdata/orders", bag)
	if err != nil {
		t.Fatal(err)
	}
	if bag.HasErrors() {
		var b bytes.Buffer
		bag.Format(&b, true, true)
		t.Fatalf("load errors:\n%s", b.String())
	}
	if in.App == nil || in.App.Name != "orders" {
		t.Fatalf("app not parsed: %+v", in.App)
	}
	var paths []string
	for _, m := range in.Modules {
		paths = append(paths, m.Path)
	}
	want := "http orders.billing orders.billing.scenarios orders.customers"
	got := strings.Join(sortedCopy(paths), " ")
	if got != want {
		t.Fatalf("modules = %q, want %q", got, want)
	}
	p := checker.Check(in, bag)
	if bag.HasErrors() || p == nil {
		var b bytes.Buffer
		bag.Format(&b, true, true)
		t.Fatalf("check errors:\n%s", b.String())
	}
}

func TestModuleLineMismatch(t *testing.T) {
	bag := diag.NewBag()
	if _, err := Load("testdata/mismatch", bag); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	bag.Format(&b, true, true)
	out := b.String()
	for _, want := range []string{
		"error[E0101]: module line does not match directory",
		"--> orders/billing/charge.jip:1:8",
		"found `orders.biling`",
		"this file is in `orders/billing/`, expected `module orders.billing`",
		"replace line 1 with `module orders.billing`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func sortedCopy(s []string) []string {
	out := append([]string{}, s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
