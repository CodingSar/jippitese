package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExamples is the Phase E corpus check: every project under
// examples/ is formatted, has committed interfaces matching its source,
// checks clean, builds, and passes all its scenarios.
func TestExamples(t *testing.T) {
	needGo(t)
	apps, _ := filepath.Glob("../../examples/*/app.jip")
	if len(apps) < 5 {
		t.Fatalf("expected the card and one example per v0 behaviour, found %d", len(apps))
	}
	for _, a := range apps {
		src := filepath.Dir(a)
		t.Run(filepath.Base(src), func(t *testing.T) {
			dir := t.TempDir()
			if out, err := exec.Command("cp", "-r", src+"/.", dir).CombinedOutput(); err != nil {
				t.Fatalf("cp: %v %s", err, out)
			}
			os.RemoveAll(filepath.Join(dir, ".jip"))
			var o, e bytes.Buffer
			env, err := NewEnv(dir, &o, &e)
			if err != nil {
				t.Fatal(err)
			}
			if code := env.Fmt(true); code != ExitOK {
				t.Fatalf("not formatted:\n%s%s", o.String(), e.String())
			}
			if code := env.Check(CheckOptions{}); code != ExitOK {
				t.Fatalf("check: exit %d\n%s%s", code, o.String(), e.String())
			}
			o.Reset()
			if code := env.Test(TestOptions{}); code != ExitOK || !strings.Contains(o.String(), " 0 failed") {
				t.Fatalf("test: exit %d\n%s%s", code, o.String(), e.String())
			}
			if code := env.Build(BuildOptions{}); code != ExitOK {
				t.Fatalf("build: exit %d\n%s", code, e.String())
			}
		})
	}
}

// TestLanguageCard checks that the language card compiles and its
// scenarios pass: its two code blocks replace the orders example's billing
// module and scenarios, whose remaining files supply the other types.
func TestLanguageCard(t *testing.T) {
	needGo(t)
	md, err := os.ReadFile("../../docs/spec/00-language-card.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(string(md), "```jip\n")
	if len(blocks) != 3 {
		t.Fatalf("want two jip blocks, found %d", len(blocks)-1)
	}
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", "../../examples/orders/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, out)
	}
	os.RemoveAll(filepath.Join(dir, ".jip"))
	for i, name := range []string{"billing.jip", "scenarios.jip"} {
		code, _, _ := strings.Cut(blocks[i+1], "```")
		if err := os.WriteFile(filepath.Join(dir, "orders", "billing", name), []byte(code), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The card leaves out the Service, so the app does not run it.
	app := filepath.Join(dir, "app.jip")
	src, err := os.ReadFile(app)
	if err != nil {
		t.Fatal(err)
	}
	run := "  run BillingApi {\n    restart: permanent\n    max_restarts: 3\n    period: 60s\n  }\n"
	if !strings.Contains(string(src), run) {
		t.Fatal("orders app.jip no longer runs BillingApi as expected")
	}
	if err := os.WriteFile(app, []byte(strings.Replace(string(src), run, "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	env, err := NewEnv(dir, &o, &e)
	if err != nil {
		t.Fatal(err)
	}
	if code := env.Check(CheckOptions{WriteInterfaces: true, AcceptBreaking: []string{"orders.billing"}}); code != ExitOK || strings.Contains(o.String()+e.String(), "warning[") {
		t.Fatalf("check: exit %d\n%s%s", code, o.String(), e.String())
	}
	o.Reset()
	if code := env.Test(TestOptions{}); code != ExitOK || !strings.Contains(o.String(), " 0 failed") {
		t.Fatalf("test: exit %d\n%s%s", code, o.String(), e.String())
	}
}
