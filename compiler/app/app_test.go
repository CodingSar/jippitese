package app

import (
	"os"
	"testing"

	"github.com/codingsar/jippitese/compiler/parser"
)

func TestParseApp(t *testing.T) {
	src, err := os.ReadFile("../parser/testdata/app.jip")
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile("app.jip", string(src))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "orders" || len(a.Config) != 1 || len(a.Secrets) != 1 || len(a.Deps) != 1 || len(a.Models) != 2 || len(a.Runs) != 2 || len(a.Envs) != 1 {
		t.Fatalf("unexpected app: %+v", a)
	}
	if a.Externs.Max != 1 || a.Externs.Allow[0] != "orders.billing" || a.Contracts != "pre" {
		t.Fatalf("externs/contracts: %+v", a.Externs)
	}
	if a.RunFor("BillingApi").Fields["max_restarts"] == nil {
		t.Fatal("run fields")
	}
}

func TestParseWorkflow(t *testing.T) {
	parse := func(body string) (*App, error) {
		f, err := parser.ParseFile("app.jip", "app {\n  name: \"x\"\n"+body+"}\n")
		if err != nil {
			t.Fatal(err)
		}
		return Parse(f)
	}
	a, err := parse("")
	if err != nil || a.Workflow.Mode != "embedded" || a.Workflow.Store != DefaultWorkflowStore {
		t.Fatalf("defaults: %+v %v", a, err)
	}
	a, err = parse("  workflow { mode: embedded, store: default \"var/wf\" }\n")
	if err != nil || a.Workflow.Store != "var/wf" {
		t.Fatalf("store: %+v %v", a, err)
	}
	if _, err := parse("  workflow { mode: temporal }\n"); err == nil {
		t.Fatal("temporal accepted")
	}
}

func TestParseModels(t *testing.T) {
	parse := func(body string) (*App, error) {
		f, err := parser.ParseFile("app.jip", "app {\n  name: \"x\"\n  models { "+body+" }\n}\n")
		if err != nil {
			t.Fatal(err)
		}
		return Parse(f)
	}
	a, err := parse(`fast = ollama("llama3"), local = custom("http://localhost:8000/v1", "qwen")`)
	if err != nil {
		t.Fatal(err)
	}
	if m := a.ModelFor("local"); m == nil || m.URL != "http://localhost:8000/v1" || m.Name != "qwen" || a.ModelFor("fast").Name != "llama3" || a.ModelFor("x") != nil {
		t.Fatalf("models: %+v", a.Models)
	}
	for _, bad := range []string{`a = gemini("x")`, `a = custom("x")`, `a = openai("x", "y")`, `a = "gpt"`} {
		if _, err := parse(bad); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}

func TestParseEffects(t *testing.T) {
	parse := func(body string) (*App, error) {
		f, err := parser.ParseFile("app.jip", "app {\n  name: \"x\"\n  effects {\n"+body+"\n  }\n}\n")
		if err != nil {
			t.Fatal(err)
		}
		return Parse(f)
	}
	a, err := parse(`    net  { timeout: default 5s, retry: default { max: 3, backoff: exp, base: 200ms }, ratelimit: default { per_second: 50.0 } }
    llm  { timeout: default 60s, budget: default 2_000_000 tokens }
    io   { timeout: 10s }
    decide { timeout: default 500ms }
    approve { channel: std.approve.http, default_timeout: default 48h }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Effects) != 11 {
		t.Fatalf("leaves: %d", len(a.Effects))
	}
	if lf := a.Effects["net.retry.max"]; lf == nil || !lf.IsDefault {
		t.Fatalf("a `default` record marks its leaves default: %+v", lf)
	}
	if lf := a.Effects["io.timeout"]; lf == nil || lf.IsDefault {
		t.Fatalf("io.timeout is fixed: %+v", lf)
	}
	for _, bad := range []string{
		`net { timeout: 5 }`,
		`net { retry { backoff: linear } }`,
		`net { retries: 3 }`,
		`disk { timeout: 1s }`,
		`net { retry: 3 }`,
		`llm { budget: 2_000_000 }`,
		`net { timeout: 1s, timeout: 2s }`,
		`approve { channel: orders.review_inbox }`,
	} {
		if _, err := parse(bad); err == nil {
			t.Fatalf("%s accepted", bad)
		} else if _, ok := err.(*Error); !ok {
			t.Fatalf("%s: %T is not an app error", bad, err)
		}
	}
	env := func(set string) error {
		f, err := parser.ParseFile("app.jip", "app {\n  name: \"x\"\n  config { a: 1, b: default 2 }\n  effects { io { timeout: 10s }, net { timeout: default 5s } }\n  env prod { "+set+" }\n}\n")
		if err != nil {
			t.Fatal(err)
		}
		_, err = Parse(f)
		return err
	}
	for _, ok := range []string{`config.b = 3`, `effects.net.timeout = 1s`, `effects.net.retry.max = 5`} {
		if err := env(ok); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{`config.a = 3`, `effects.io.timeout = 1s`} {
		err := env(bad)
		if ae, ok := err.(*Error); !ok || ae.Code != "E0303" {
			t.Fatalf("%s: want E0303, got %v", bad, err)
		}
	}
}
