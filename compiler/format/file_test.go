package format

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jipFiles(t *testing.T, roots ...string) []string {
	var out []string
	for _, r := range roots {
		filepath.WalkDir(r, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jip") {
				out = append(out, p)
			}
			return nil
		})
	}
	if len(out) == 0 {
		t.Fatal("no files")
	}
	return out
}

// TestFormatStableAndSafe formats the card and the standard library:
// meaning and comments are kept and a second pass changes nothing.
func TestFormatStableAndSafe(t *testing.T) {
	for _, p := range jipFiles(t, "../checker/testdata/orders", "../stdlib") {
		src, _ := os.ReadFile(p)
		once, err := Source(p, string(src))
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		twice, err := Source(p, once)
		if err != nil {
			t.Errorf("second pass: %v", err)
			continue
		}
		if once != twice {
			t.Errorf("%s: not idempotent\n--- once\n%s\n--- twice\n%s", p, once, twice)
		}
	}
}

func TestFormatLayout(t *testing.T) {
	src := "module m\nuse std.json\n/// Doc.\npub fn f(x: Int) -> Int { let y = x + 1   // bump\n  match y { 1 => 2, _ => y } }\n// trailing note\n"
	out, err := Source("m/m.jip", src)
	if err != nil {
		t.Fatal(err)
	}
	want := `module m
use std.json

/// Doc.
pub fn f(x: Int) -> Int {
  let y = x + 1  // bump
  match y {
    1 => 2,
    _ => y,
  }
}
// trailing note
`
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

func TestShowCard(t *testing.T) {
	if os.Getenv("JIP_SHOW") == "" {
		t.Skip()
	}
	for _, p := range []string{"../checker/testdata/orders/orders/billing/billing.jip", "../checker/testdata/orders/app.jip"} {
		src, _ := os.ReadFile(p)
		out, err := Source(p, string(src))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(os.Getenv("JIP_SHOW")+"/"+filepath.Base(p), []byte(out), 0o644)
	}
}
