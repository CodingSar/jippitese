package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codingsar/jippitese/compiler/iface"
)

const cardRoot = "../checker/testdata/orders"

func needGo(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
}

// card copies the card into a temp project and returns an Env on it.
func card(t *testing.T) (*Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", cardRoot+"/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, out)
	}
	var o, e bytes.Buffer
	env, err := NewEnv(dir, &o, &e)
	if err != nil {
		t.Fatal(err)
	}
	return env, &o, &e
}

func TestCheckInterfacesDriftAndAccept(t *testing.T) {
	env, out, errb := card(t)
	if code := env.Check(CheckOptions{}); code != ExitFail || !strings.Contains(errb.String(), "E0201") {
		t.Fatalf("missing interfaces: exit %d\n%s", code, errb.String())
	}
	if code := env.Check(CheckOptions{WriteInterfaces: true}); code != ExitOK {
		t.Fatalf("write: exit %d\n%s%s", code, out.String(), errb.String())
	}
	errb.Reset()
	if code := env.Check(CheckOptions{}); code != ExitOK {
		t.Fatalf("clean check: exit %d\n%s", code, errb.String())
	}
	// A breaking edit: drift, and the break needs accepting.
	p := filepath.Join(env.Root, "orders/billing/billing.jip")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), "pub fn positive_money", "fn positive_money", 1)), 0o644)
	out.Reset()
	errb.Reset()
	if code := env.Check(CheckOptions{}); code != ExitFail || !strings.Contains(errb.String(), "interface drift") || !strings.Contains(out.String(), "orders.billing: breaking") {
		t.Fatalf("drift: exit %d\n%s%s", code, out.String(), errb.String())
	}
	if code := env.Check(CheckOptions{WriteInterfaces: true}); code != ExitFail {
		t.Fatalf("unaccepted break must fail, got %d", code)
	}
	// The write happened; an accepted re-check is clean.
	if code := env.Check(CheckOptions{AcceptBreaking: []string{"orders.billing"}}); code != ExitOK {
		t.Fatalf("accepted: exit %d\n%s", code, errb.String())
	}
	// The committed file round-trips with a stable digest.
	txt, _ := os.ReadFile(filepath.Join(env.Root, "orders/billing", iface.FileName))
	in, dg, _, err := iface.Parse(string(txt))
	if err != nil || in.Text() != string(txt) || dg != iface.Digest(in.Public) {
		t.Fatalf("round trip: %v", err)
	}
}

func TestVendorHash(t *testing.T) {
	env, _, errb := card(t)
	env.Check(CheckOptions{WriteInterfaces: true})
	os.WriteFile(filepath.Join(env.Root, "vendor/http/extra.txt"), []byte("x"), 0o644)
	errb.Reset()
	if code := env.Check(CheckOptions{}); code != ExitFail || !strings.Contains(errb.String(), "E0309") {
		t.Fatalf("tampered vendor: exit %d\n%s", code, errb.String())
	}
}

func TestBuildTestContext(t *testing.T) {
	needGo(t)
	env, out, errb := card(t)
	if code := env.Build(BuildOptions{}); code != ExitOK {
		t.Fatalf("build: exit %d\n%s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(env.Root, ".jip/bin/orders")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := env.Test(TestOptions{}); code != ExitOK || !strings.Contains(out.String(), "4 scenarios: 4 passed") {
		t.Fatalf("test: exit %d\n%s%s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := env.Test(TestOptions{Filter: "table"}); code != ExitOK || !strings.Contains(out.String(), "1 passed") {
		t.Fatalf("filter: exit %d\n%s", code, out.String())
	}
	out.Reset()
	if code := env.Context("orders.billing", iface.ContextOptions{}); code != ExitOK || !strings.Contains(out.String(), "-- http (referenced:") {
		t.Fatalf("context: exit %d\n%s", code, out.String())
	}
}

func TestTestReportsFailure(t *testing.T) {
	needGo(t)
	env, out, _ := card(t)
	p := filepath.Join(env.Root, "orders/billing/scenarios.jip")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), "then  r.is_ok() == ok", "then  r.is_ok() != ok", 1)), 0o644)
	if code := env.Test(TestOptions{}); code != ExitFail || !strings.Contains(out.String(), "FAIL  orders.billing  \"withdraw table\"") {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}

func TestFmtCheckAndWrite(t *testing.T) {
	env, out, _ := card(t)
	if code := env.Fmt(true); code != ExitFail || !strings.Contains(out.String(), "billing.jip") {
		t.Fatalf("fmt --check on the card: exit %d\n%s", code, out.String())
	}
	if code := env.Fmt(false); code != ExitOK {
		t.Fatalf("fmt: exit %d", code)
	}
	out.Reset()
	if code := env.Fmt(true); code != ExitOK || out.Len() != 0 {
		t.Fatalf("after fmt: exit %d\n%s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(env.Root, "vendor/http/http.jip")); err != nil {
		t.Fatal(err)
	}
}

func TestNewProject(t *testing.T) {
	needGo(t)
	dir := t.TempDir()
	if err := New(dir, "hello"); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	env, err := NewEnv(filepath.Join(dir, "hello"), &o, &e)
	if err != nil {
		t.Fatal(err)
	}
	if code := env.Test(TestOptions{}); code != ExitOK {
		t.Fatalf("new project test: exit %d\n%s%s", code, o.String(), e.String())
	}
	if code := env.Build(BuildOptions{}); code != ExitOK {
		t.Fatalf("new project build: exit %d\n%s", code, e.String())
	}
}

func TestFetchFromLocalRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	env, out, errb := card(t)
	// Publish the vendored http dependency as a local git repository.
	repo := t.TempDir()
	exec.Command("cp", "-r", filepath.Join(env.Root, "vendor/http")+"/.", repo).Run()
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	commit := git("rev-parse", "HEAD")
	appPath := filepath.Join(env.Root, "app.jip")
	b, _ := os.ReadFile(appPath)
	s := strings.Replace(string(b), `"https://github.com/acme/jip-http", commit: "8f1c2a7"`, `"`+repo+`", commit: "`+commit+`"`, 1)
	os.WriteFile(appPath, []byte(s), 0o644)
	os.RemoveAll(filepath.Join(env.Root, "vendor/http"))
	if code := env.Fetch(false); code != ExitOK {
		t.Fatalf("fetch: exit %d\n%s%s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(env.Root, "vendor/http/http.jip")); err != nil {
		t.Fatal(err)
	}
	// A wrong hash is refused, then saved with --save.
	b, _ = os.ReadFile(appPath)
	os.WriteFile(appPath, []byte(strings.Replace(string(b), "sha256-/yoe", "sha256-XXXX", 1)), 0o644)
	if code := env.Fetch(false); code != ExitFail {
		t.Fatalf("mismatch must fail")
	}
	if code := env.Fetch(true); code != ExitOK {
		t.Fatalf("save: exit %d\n%s", code, errb.String())
	}
	b, _ = os.ReadFile(appPath)
	if !strings.Contains(string(b), "sha256-/yoe") {
		t.Fatalf("hash not saved:\n%s", b)
	}
}

func TestEffectiveConfig(t *testing.T) {
	env, out, _ := card(t)
	t.Setenv("JIP_CONFIG__GATEWAY_URL", "https://pay.example/charge")
	if code := env.Check(CheckOptions{Effective: true, EnvName: "prod"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		`app "orders"  env: prod`,
		"config.gateway_url", "https://pay.example/charge", "JIP_CONFIG__GATEWAY_URL",
		"secrets.gateway_key", "<unset>",
		"run.BillingApi.period", "60s",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
}

// TestBinaryConfigOverlay runs the built card: it refuses to start
// without the required config and names the variable (spec 10 rule 5).
func TestBinaryConfigOverlay(t *testing.T) {
	needGo(t)
	env, _, errb := card(t)
	if code := env.Build(BuildOptions{}); code != ExitOK {
		t.Fatalf("build: %s", errb.String())
	}
	bin := filepath.Join(env.Root, ".jip/bin/orders")
	cmd := exec.Command(bin)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 || !strings.Contains(string(out), "JIP_CONFIG__GATEWAY_URL is unset") {
		t.Fatalf("want exit 2 naming JIP_CONFIG__GATEWAY_URL, got %v\n%s", err, out)
	}
	cmd = exec.Command(bin)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "JIP_CONFIG__GATEWAY_URL=http://x", "JIP_SECRETS__GATEWAY_KEY=k", "JIP_CONFIG__NOPE=1"}
	out, _ = cmd.CombinedOutput()
	if !strings.Contains(string(out), "JIP_CONFIG__NOPE matches no field") {
		t.Fatalf("unknown variable must abort startup:\n%s", out)
	}
}
