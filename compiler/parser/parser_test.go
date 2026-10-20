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

func TestInterpolationErrorPositions(t *testing.T) {
	for _, c := range []struct {
		lit       string
		code      string
		line, col int
	}{
		{`"{\"id\": 7}"`, "E0001", 3, 22},
		{`"a {1 + "`, "E0001", 3, 23},
		{`"ab {1 + 2 + }"`, "E0002", 3, 33},
	} {
		src := "module m\n\nfn f() -> String { " + c.lit + " }\n"
		_, err := ParseFile("m.jip", src)
		e, ok := err.(*Error)
		if !ok {
			t.Errorf("%s: want *Error, got %v", c.lit, err)
			continue
		}
		if e.Code != c.code || e.Pos.Line != c.line || e.Pos.Col != c.col {
			t.Errorf("%s: got %s at %d:%d, want %s at %d:%d", c.lit, e.Code, e.Pos.Line, e.Pos.Col, c.code, c.line, c.col)
		}
	}
}
