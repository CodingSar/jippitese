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
