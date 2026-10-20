package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// example copies examples/<name> into a temp project and returns an Env on it.
func example(t *testing.T, name string) (*Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", "../../examples/"+name+"/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, out)
	}
	os.RemoveAll(filepath.Join(dir, ".jip"))
	var o, e bytes.Buffer
	env, err := NewEnv(dir, &o, &e)
	if err != nil {
		t.Fatal(err)
	}
	return env, &o, &e
}

// edit replaces old with new in a project file, failing when old is absent.
func edit(t *testing.T, env *Env, rel, old, new string) {
	t.Helper()
	p := filepath.Join(env.Root, rel)
	b, _ := os.ReadFile(p)
	if !bytes.Contains(b, []byte(old)) {
		t.Fatalf("%s: no %q", rel, old)
	}
	os.WriteFile(p, bytes.Replace(b, []byte(old), []byte(new), 1), 0o644)
}

func TestStateInvAfterMethod(t *testing.T) {
	needGo(t)
	env, out, errb := example(t, "shop")
	edit(t, env, "shop/orders/orders.jip", "pub type Totals { by_customer: MutMap<Int, Int> }",
		"pub type Totals { by_customer: MutMap<Int, Int>, puts: Cell<Int> }")
	edit(t, env, "shop/orders/orders.jip", "  fn init(cfg: Config) -> Result<Self, RepoError> with {io, mut} {\n    Ok(Totals { by_customer: MutMap.new() })",
		"  inv puts <= 2\n  fn init(cfg: Config) -> Result<Self, RepoError> with {io, mut} {\n    Ok(Totals { by_customer: MutMap.new(), puts: Cell.new(0) })")
	edit(t, env, "shop/orders/orders.jip", "    self.by_customer.insert(k, v)\n",
		"    self.by_customer.insert(k, v)\n    self.puts.update(fn(n) => n + 1)\n")
	code := env.Test(TestOptions{})
	s := out.String()
	if code != ExitFail ||
		!strings.Contains(s, "FAIL  shop.orders  \"placed orders are totalled per customer\"") ||
		!strings.Contains(s, "contract violated: inv puts <= 2") ||
		!strings.Contains(s, "ok  shop.orders  \"a rejected order is dead-lettered and counts nothing\"") {
		t.Fatalf("exit %d\n%s%s", code, s, errb.String())
	}
}

func TestRepositoryRoundTrip(t *testing.T) {
	needGo(t)
	env, out, errb := example(t, "shop")
	edit(t, env, "shop/orders/orders.jip", "self.by_customer.insert(k, v)", "self.by_customer.insert(k, if v > 3 { 0 } else { v })")
	code := env.Test(TestOptions{})
	s := out.String()
	if code != ExitFail ||
		!strings.Contains(s, "FAIL  shop.orders  derived Totals.round_trip") ||
		!strings.Contains(s, "get(k) == Ok(Some(v))") ||
		!strings.Contains(s, "got = Ok(Some(0))") {
		t.Fatalf("exit %d\n%s%s", code, s, errb.String())
	}
}
