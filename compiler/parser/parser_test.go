package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCard(t *testing.T) {
	for _, name := range []string{"card.jip", "scenarios.jip", "app.jip"} {
		p := filepath.Join("testdata", name)
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		f, err := ParseFile(p, string(src))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		t.Logf("%s: module=%v items=%d scenarios=%d app=%v", name, f.Module, len(f.Items), len(f.Scenarios), f.App != nil)
	}
}
