package format

import (
	"testing"

	"github.com/codingsar/jippitese/compiler/ast"
	"github.com/codingsar/jippitese/compiler/parser"
)

// roundTrip parses `fn f() -> Int { <src> }`, prints the tail and checks the
// printed form parses to the same print.
func tail(t *testing.T, src string) ast.Expr {
	t.Helper()
	f, err := parser.ParseFile("t.jip", "module t\nfn f() {\n"+src+"\n}\n")
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return f.Items[0].(*ast.FnDecl).Body.Tail
}

func TestExprPrecedence(t *testing.T) {
	cases := map[string]string{
		"(a + b) * c":    "(a + b) * c",
		"a + b * c":      "a + b * c",
		"a - (b - c)":    "a - (b - c)",
		"(a - b) - c":    "a - b - c",
		"!(a && b) || c": "!(a && b) || c",
		"-(x.0)":         "-x.0",
		"(a == b) == c":  "(a == b) == c",
		"f(x)?.y":        "f(x)?.y",
		"match r { Ok(a) => a.b == 1, Err(_) => true }": "match r { Ok(a) => a.b == 1, Err(_) => true }",
		"xs.fold(0, fn(a, x) => a + x)":                 "xs.fold(0, fn(a, x) => a + x)",
		"{ let x = 1; x + 2 }":                          "{ let x = 1; x + 2 }",
		"\"a {b.c} {{d}}\"":                             "\"a {b.c} {{d}}\"",
		"if a { 1 } else { 2 }":                         "if a { 1 } else { 2 }",
		"Order { ..o, total }":                          "Order { ..o, total }",
	}
	for in, want := range cases {
		got := Expr(tail(t, in))
		if got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
		if again := Expr(tail(t, got)); again != got {
			t.Errorf("%s: reprint %s != %s", in, again, got)
		}
	}
}
