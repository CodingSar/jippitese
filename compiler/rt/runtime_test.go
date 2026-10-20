package rt

import (
	"sort"
	"testing"

	"github.com/codingsar/jippitese/compiler/app"
)

func TestWorkflowAdaptersMatchApp(t *testing.T) {
	var have []string
	for m := range WorkflowRuntimes {
		have = append(have, m)
	}
	sort.Strings(have)
	want := append([]string(nil), app.WorkflowAdapters...)
	sort.Strings(want)
	if len(have) != len(want) {
		t.Fatalf("rt adapters %v, app.WorkflowAdapters %v", have, want)
	}
	for i := range have {
		if have[i] != want[i] {
			t.Fatalf("rt adapters %v, app.WorkflowAdapters %v", have, want)
		}
	}
}

func TestUseWorkflowRuntimeRejectsUnknownMode(t *testing.T) {
	if err := NewCtx().UseWorkflowRuntime("temporal", ""); err == nil {
		t.Fatal("temporal accepted without an adapter")
	}
}
