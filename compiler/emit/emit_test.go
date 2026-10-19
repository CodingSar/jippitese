package emit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/codingsar/jippitese/compiler/checker"
	"github.com/codingsar/jippitese/compiler/diag"
	"github.com/codingsar/jippitese/compiler/modules"
)

func checkCard(t *testing.T) *checker.Program {
	t.Helper()
	bag := diag.NewBag()
	in, err := modules.Load("../checker/testdata/orders", bag)
	if err != nil {
		t.Fatal(err)
	}
	prog := checker.Check(in, bag)
	if bag.HasErrors() {
		var b bytes.Buffer
		bag.Format(&b, true, false)
		t.Fatalf("card does not check:\n%s", b.String())
	}
	return prog
}

func emitTo(t *testing.T, prog *checker.Program, opts Options) string {
	t.Helper()
	out, err := Emit(prog, opts)
	dir := t.TempDir()
	if out != nil {
		if werr := out.Write(dir); werr != nil {
			t.Fatal(werr)
		}
	}
	if err != nil {
		t.Fatalf("%v (output in %s)", err, dir)
	}
	return dir
}

func goCmd(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GOPROXY=off", "GOWORK=off")
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// keep copies a failing tree somewhere inspectable when JIP_KEEP is set.
func keep(t *testing.T, dir string) {
	if k := os.Getenv("JIP_KEEP"); k != "" {
		os.RemoveAll(k)
		exec.Command("cp", "-r", dir, k).Run()
		t.Logf("generated tree kept at %s", k)
	}
}

// TestCardVetAndScenarios is the Phase E acceptance for the card: the
// emitted Go builds, `go vet` is clean, and the scenarios pass.
func TestCardVetAndScenarios(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	dir := emitTo(t, checkCard(t), Options{GoPositions: true})
	keep(t, dir)
	if out, err := goCmd(t, dir, "vet", "./..."); err != nil {
		t.Fatalf("go vet:\n%s", out)
	}
	if out, err := goCmd(t, dir, "test", "./..."); err != nil {
		t.Fatalf("go test:\n%s", out)
	}
}

// TestCardLineDirectives checks the default build with //line directives
// still compiles.
func TestCardLineDirectives(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	dir := emitTo(t, checkCard(t), Options{})
	if out, err := goCmd(t, dir, "build", "./..."); err != nil {
		t.Fatalf("go build:\n%s", out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "orders/billing/module.go"))
	if !bytes.Contains(b, []byte("//line ")) {
		t.Errorf("no //line directives in module.go")
	}
}

// TestDeterministic checks two emissions are byte-identical.
func TestDeterministic(t *testing.T) {
	a, err := Emit(checkCard(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Emit(checkCard(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Files) != len(b.Files) {
		t.Fatalf("file count differs")
	}
	for n, x := range a.Files {
		if !bytes.Equal(x, b.Files[n]) {
			t.Errorf("%s differs between runs", n)
		}
	}
}
