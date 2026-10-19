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
