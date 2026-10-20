package driver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestWorkerIdempotent(t *testing.T) {
	needGo(t)
	env, out, errb := example(t, "shop")
	edit(t, env, "shop/orders/orders.jip", "    if Counted.get(m.id)?.is_some() { return Ok(()) }\n", "")
	code := env.Test(TestOptions{})
	s := out.String()
	if code != ExitFail ||
		!strings.Contains(s, "FAIL  shop.orders  derived Indexer.idempotent") ||
		!strings.Contains(s, "perform(m); perform(m) leaves the fakes as perform(m) is false") ||
		!strings.Contains(s, "after one perform") ||
		!strings.Contains(s, "ok  shop.orders  \"placed orders are totalled per customer\"") {
		t.Fatalf("exit %d\n%s%s", code, s, errb.String())
	}
}

func TestStatefulImplCheck(t *testing.T) {
	needGo(t)
	env, out, errb := example(t, "shop")
	edit(t, env, "shop/orders/orders.jip", "pub type Totals { by_customer: MutMap<Int, Int> }",
		"pub type Totals { by_customer: MutMap<Int, Int>, puts: Cell<Int> }")
	edit(t, env, "shop/orders/orders.jip", "  fn init(cfg: Config) -> Result<Self, RepoError> with {io, mut} {\n    Ok(Totals { by_customer: MutMap.new() })",
		"  inv puts >= 0\n  fn init(cfg: Config) -> Result<Self, RepoError> with {mut} {\n    Ok(Totals { by_customer: MutMap.new(), puts: Cell.new(0) })")
	edit(t, env, "shop/orders/orders.jip", "    self.by_customer.insert(k, v)\n",
		"    self.by_customer.insert(k, v)\n    self.puts.update(fn(n) => n + 1)\n")
	code := env.Test(TestOptions{})
	s := out.String()
	if code != ExitOK || !strings.Contains(s, "ok  shop.orders  derived Totals\n") {
		t.Fatalf("exit %d\n%s%s", code, s, errb.String())
	}
	out.Reset()
	edit(t, env, "shop/orders/orders.jip", "inv puts >= 0", "inv puts <= 2")
	code = env.Test(TestOptions{})
	s = out.String()
	if code != ExitFail ||
		!strings.Contains(s, "FAIL  shop.orders  derived Totals\n") ||
		!strings.Contains(s, "contract violated: inv puts <= 2") ||
		!strings.Contains(s, "when Totals.init(") ||
		!strings.Contains(s, "; put(") {
		t.Fatalf("exit %d\n%s%s", code, s, errb.String())
	}
}

func TestModelsInMain(t *testing.T) {
	needGo(t)
	env, _, errb := example(t, "triage")
	edit(t, env, "app.jip", `smart = anthropic("claude-sonnet")`, `smart = custom("http://localhost:8080/v1", "qwen")`)
	if code := env.Build(BuildOptions{}); code != ExitOK {
		t.Fatalf("build: exit %d\n%s", code, errb.String())
	}
	b, _ := os.ReadFile(filepath.Join(env.Root, ".jip", "gen", "main.go"))
	if !strings.Contains(string(b), `"smart": rt.NewModel("smart", "custom", "http://localhost:8080/v1", "qwen"),`) {
		t.Fatalf("main.go:\n%s", b)
	}
}

// freePort returns a loopback port nothing listens on.
func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestApproveHttpEndToEnd runs the refunds binary with `std.approve.http`:
// a large refund waits in GET /reviews until a reviewer approves it.
func TestApproveHttpEndToEnd(t *testing.T) {
	needGo(t)
	env, _, errb := example(t, "refunds")
	port, aport := freePort(t), freePort(t)
	edit(t, env, "app.jip", "  run Refund\n", fmt.Sprintf("  effects { approve { channel: std.approve.http } }\n  run Refund { port: %d }\n", port))
	if code := env.Build(BuildOptions{}); code != ExitOK {
		t.Fatalf("build: exit %d\n%s", code, errb.String())
	}
	var logs bytes.Buffer
	cmd := exec.Command(filepath.Join(env.Root, ".jip/bin/refunds"))
	cmd.Dir = env.Root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), fmt.Sprintf("JIP_APPROVE_ADDR=127.0.0.1:%d", aport)}
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	app, reviews := fmt.Sprintf("http://127.0.0.1:%d/Refund", port), fmt.Sprintf("http://127.0.0.1:%d/reviews", aport)

	// poll retries a request until check accepts the response
	poll := func(what string, do func() (*http.Response, error), check func(int, []byte) bool) []byte {
		for i := 0; i < 200; i++ {
			if resp, err := do(); err == nil {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if check(resp.StatusCode, b) {
					return b
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("%s: gave up\n%s", what, logs.String())
		return nil
	}
	b := poll("start", func() (*http.Response, error) {
		return http.Post(app+"/start", "application/json", strings.NewReader(`{"order": 9, "amount": 900}`))
	}, func(c int, _ []byte) bool { return c == http.StatusAccepted })
	var run struct{ Id string }
	json.Unmarshal(b, &run)
	var list []struct{ Id, Title string }
	poll("reviews", func() (*http.Response, error) { return http.Get(reviews) }, func(c int, b []byte) bool {
		return c == http.StatusOK && json.Unmarshal(b, &list) == nil && len(list) == 1
	})
	if list[0].Title != "Refund for order 9" {
		t.Fatalf("review: %+v", list)
	}
	if resp, err := http.Get(app + "/result/" + run.Id); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the run must wait for the review: %v %v", resp, err)
	}
	poll("approve", func() (*http.Response, error) {
		return http.Post(reviews+"/"+list[0].Id+"/approve", "application/json", strings.NewReader(`"Approve"`))
	}, func(c int, _ []byte) bool { return c == http.StatusOK })
	b = poll("result", func() (*http.Response, error) { return http.Get(app + "/result/" + run.Id) }, func(c int, _ []byte) bool { return c == http.StatusOK })
	if !strings.Contains(string(b), `"amount":900`) || !strings.Contains(string(b), `"Ok"`) {
		t.Fatalf("result: %s", b)
	}
}
