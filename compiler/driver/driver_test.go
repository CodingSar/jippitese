package driver

import (
	"bytes"
	"io/fs"
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
	if code := env.Test(TestOptions{}); code != ExitOK || !strings.Contains(out.String(), "4 scenarios, 1 derived check: 5 passed") {
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

func TestAnyWhere(t *testing.T) {
	needGo(t)
	env, out, errb := card(t)
	p := filepath.Join(env.Root, "orders/billing/scenarios.jip")
	b, _ := os.ReadFile(p)
	add := `
scenario "where keeps only matching candidates" {
  given n = any Int where n > 0
  when  m = n * 2
  then  m > n
}

scenario "where rejecting most candidates warns" {
  given n = any Int where n > 95
  when  m = n
  then  m > 95
}

scenario "an unsatisfiable where fails" {
  given n = any Int where n > 1_000_000
  when  m = n
  then  m > 0
}
`
	os.WriteFile(p, append(b, add...), 0o644)
	code := env.Test(TestOptions{})
	s := out.String()
	if code != ExitFail || !strings.Contains(s, "FAIL  orders.billing  \"an unsatisfiable where fails\"") ||
		!strings.Contains(s, "rejected 1000 candidates in a row") ||
		!strings.Contains(s, "warning W0510") || !strings.Contains(s, "`where n > 95` rejects too many candidates") ||
		strings.Contains(s, "FAIL  orders.billing  \"where keeps") || strings.Contains(s, "FAIL  orders.billing  \"where rejecting") {
		t.Fatalf("exit %d\n%s%s", code, s, errb.String())
	}
}

func TestDerivedCheck(t *testing.T) {
	needGo(t)
	env, out, errb := card(t)
	p := filepath.Join(env.Root, "orders/billing/billing.jip")
	b, _ := os.ReadFile(p)
	add := `
/// Clamps to zero from below.
pub fn clamp(m: Money) -> Money
  post ret.0 >= 0
{
  if m.0 < -5 { Money(0) } else { m }
}

/// Only huge amounts: the bound draws them, nothing is rejected.
pub fn huge(m: Money) -> Money
  pre  m.0 > 1_000_000
  post ret.0 > 1_000_000
{
  m
}

/// A percentage, bounded by range membership.
pub fn pct(p: Int, xs: List<Int>) -> Int
  pre  (0..=100).contains(p) && xs.len() >= 9
  post ret <= 100 && ret >= 9
{
  if p > xs.len() { p } else { xs.len() }
}

/// b is bounded by the a drawn before it.
pub fn above(a: Int, b: Int) -> Int
  pre  a > 1000 && b > a
  post ret > 1001
{
  b
}

/// No Int squares to 2: every candidate is rejected.
pub fn never(m: Money) -> Money
  pre  m.0 * m.0 == 2
  post ret.0 == 0
{
  m
}
`
	os.WriteFile(p, append(b, add...), 0o644)
	code := env.Test(TestOptions{})
	s := out.String()
	seed := filepath.Join(env.Root, ".jip/seeds/orders/billing/clamp.seed")
	if _, err := os.Stat(seed); code != ExitFail || err != nil ||
		!strings.Contains(s, "FAIL  orders.billing  derived clamp") ||
		!strings.Contains(s, "contract violated: post ret.0 >= 0") ||
		!strings.Contains(s, "saved to .jip/seeds/orders/billing/clamp.seed") ||
		!strings.Contains(s, "ok  orders.billing  derived withdraw") ||
		!strings.Contains(s, "ok  orders.billing  derived huge") ||
		!strings.Contains(s, "ok  orders.billing  derived pct") ||
		!strings.Contains(s, "ok  orders.billing  derived above") ||
		!strings.Contains(s, "skip  orders.billing  derived never  no candidate satisfies `pre`") ||
		!strings.Contains(s, "warning W0510  orders.billing  derived never") ||
		strings.Contains(s, "W0510  orders.billing  derived huge") ||
		!strings.Contains(s, "derived checks:") {
		t.Fatalf("exit %d seed %v\n%s%s", code, err, s, errb.String())
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
		`app "orders" env: prod`,
		"config.gateway_url", "https://pay.example/charge", "JIP_CONFIG__GATEWAY_URL",
		"secrets.gateway_key", "<unset>",
		"run.BillingApi.period", "60s",
		"effects.net.retry.max 5 env prod",
		"effects.net.retry.base 200ms app (default)",
		"effects.llm.budget 2_000_000 tokens",
		"effects.approve.default_timeout 24h default",
	} {
		if !strings.Contains(strings.Join(strings.Fields(out.String()), " "), want) {
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
	// effects leaves and declared model keys are fields; a bad value is named
	cmd = exec.Command(bin)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "JIP_CONFIG__GATEWAY_URL=http://x", "JIP_SECRETS__GATEWAY_KEY=k",
		"JIP_EFFECTS__NET__RETRY__MAX=x", "JIP_MODELS__SMART__KEY=k", "JIP_EFFECTS__IO__TIMEOUT=1s"}
	out, _ = cmd.CombinedOutput()
	if !strings.Contains(string(out), `JIP_EFFECTS__NET__RETRY__MAX="x" does not parse as Int`) || strings.Contains(string(out), "matches no field") {
		t.Fatalf("effects overlay:\n%s", out)
	}
}

// A method extern (spec 11 rule 1) compiles and runs: the receiver is the
// first parameter, converted to the Go named type.
func TestMethodExternRuns(t *testing.T) {
	needGo(t)
	env, out, errb := card(t)
	edit := func(file, old, new string) {
		p := filepath.Join(env.Root, file)
		b, _ := os.ReadFile(p)
		if !strings.Contains(string(b), old) {
			t.Fatalf("%s lacks %q", file, old)
		}
		os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644)
	}
	edit("app.jip", "max: 1", "max: 2")
	edit("orders/billing/billing.jip", `= go "os".ReadFile`, `= go "os".ReadFile
/// Rounds d to a multiple of m.
extern fn round_to(d: Duration, m: Duration) -> Duration with {nondet} = go "time".Duration.Round`)
	edit("orders/billing/scenarios.jip", "@extern\n", `@extern
scenario "round_to rounds half away from zero" {
  when  r = round_to(1500ms, 1s)
  then  r == 2s
}

@extern
`)
	if code := env.Test(TestOptions{}); code != ExitOK || !strings.Contains(out.String(), `ok  orders.billing  "round_to rounds half away from zero"`) {
		t.Fatalf("exit %d\n%s\n%s", code, out.String(), errb.String())
	}
}

// TestGoModDependency fetches two Go modules (one requiring the other)
// through git's insteadOf, binds an extern into one and runs its scenario
// against the vendored code (spec 10 rule 7, spec 13 rule 10).
func TestGoModDependency(t *testing.T) {
	needGo(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	env, out, errb := card(t)
	repo := func(files map[string]string) string {
		dir := t.TempDir()
		for n, src := range files {
			os.MkdirAll(filepath.Dir(filepath.Join(dir, n)), 0o755)
			os.WriteFile(filepath.Join(dir, n), []byte(src), 0o644)
		}
		for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "init"}} {
			if b, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, b)
			}
		}
		return dir
	}
	shout := repo(map[string]string{
		"go.mod":        "module example.com/shout\n\ngo 1.21\n",
		"shout.go":      "package shout\n\nimport \"strings\"\n\nfunc Up(s string) string { return strings.ToUpper(s) }\n",
		"internal/x.go": "package x\n",
		"testdata/t.go": "package broken(\n",
	})
	greet := repo(map[string]string{
		"go.mod":   "module example.com/greet\n\ngo 1.21\n\nrequire example.com/shout v1.2.3\n",
		"greet.go": "package greet\n\nimport \"example.com/shout\"\n\n// Hi greets n loudly.\nfunc Hi(n string) string { return \"hi \" + shout.Up(n) }\n",
	})
	http := map[string]string{}
	filepath.WalkDir(filepath.Join(env.Root, "vendor/http"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(filepath.Join(env.Root, "vendor/http"), p)
			http[rel] = string(b)
		}
		return nil
	})
	t.Setenv("GIT_CONFIG_COUNT", "3")
	t.Setenv("GIT_CONFIG_KEY_2", "url."+repo(http)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_2", "https://github.com/acme/jip-http")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+greet+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://example.com/greet")
	t.Setenv("GIT_CONFIG_KEY_1", "url."+shout+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_1", "https://example.com/shout")

	edit := func(file, old, new string) {
		p := filepath.Join(env.Root, file)
		b, _ := os.ReadFile(p)
		if !strings.Contains(string(b), old) {
			t.Fatalf("%s lacks %q", file, old)
		}
		os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644)
	}
	edit("app.jip", "deps { http", "deps {\n    greet = gomod(\"example.com/greet\", commit: \"HEAD\", hash: \"sha256-g\"),\n    http")
	edit("app.jip", `commit: "8f1c2a7"`, `commit: "HEAD"`)
	edit("app.jip", "max: 1", "max: 2")
	edit("orders/billing/billing.jip", `= go "os".ReadFile`, `= go "os".ReadFile
/// Greets n.
/// why: Hi only concatenates strings.
unsafe pure extern fn hi(n: Str) -> Str = go "example.com/greet".Hi`)
	edit("orders/billing/scenarios.jip", "@extern\n", `@extern
scenario "hi greets loudly" {
  when  r = hi("bob")
  then  r == "hi BOB"
}

@extern
`)
	// shout is required by greet but has no gomod entry yet.
	if code := env.Fetch(true); code != ExitOK {
		t.Fatalf("fetch: exit %d\n%s%s", code, out.String(), errb.String())
	}
	errb.Reset()
	if code := env.Check(CheckOptions{}); code == ExitOK || !strings.Contains(errb.String(), "`example.com/greet` requires `example.com/shout`, which has no gomod entry") {
		t.Fatalf("missing require must fail: exit %d\n%s", code, errb.String())
	}
	edit("app.jip", "    http", "    shout = gomod(\"example.com/shout\", commit: \"HEAD\", hash: \"sha256-s\"),\n    http")
	errb.Reset()
	if code := env.Fetch(true); code != ExitOK {
		t.Fatalf("fetch: exit %d\n%s%s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := env.Test(TestOptions{}); code != ExitOK || !strings.Contains(out.String(), `ok  orders.billing  "hi greets loudly"`) {
		t.Fatalf("exit %d\n%s\n%s", code, out.String(), errb.String())
	}
	b, _ := os.ReadFile(filepath.Join(env.genDir(), "vendor", "modules.txt"))
	if !strings.Contains(string(b), "example.com/shout/internal\n") || strings.Contains(string(b), "testdata") {
		t.Fatalf("modules.txt:\n%s", b)
	}
	// A second build keeps the links.
	if code := env.Test(TestOptions{}); code != ExitOK {
		t.Fatalf("rebuild: exit %d\n%s", code, errb.String())
	}
}

// TestStdFs runs std.fs against a bound FakeFs and, unfaked, against the
// real file system (spec 12 rule 10).
func TestStdFs(t *testing.T) {
	needGo(t)
	env, out, errb := card(t)
	p := filepath.Join(env.Root, "orders/billing/scenarios.jip")
	b, _ := os.ReadFile(p)
	src := strings.Replace(string(b), "use orders.billing\n", "use orders.billing\nuse std.fs\n", 1) + `
scenario "fs routes to the fake" {
  given disk = FakeFs.new().file(Path.of("/d/a.txt"), b"hi")
  when  r = {
    fs.append(Path.of("/d/a.txt"), b"!")?
    fs.mkdir(Path.of("/d/sub/x"))?
    fs.write_text(Path.of("/d/b.txt"), "yo")?
    fs.list(Path.of("/d"))
  }
  then  r == Ok(["a.txt", "b.txt", "sub"])
  then  disk.contents(Path.of("/d/a.txt")) == Some(b"hi!")
  then  fs.read_text(Path.of("/d/b.txt")) == Ok("yo")
  then  fs.present(Path.of("/d/sub/x")) == Ok(true)
  then  fs.remove(Path.of("/d/sub")).is_err()
  then  match fs.read(Path.of("/nope")) { Err(IoError.NotFound(_)) => true, _ => false }
  then  match fs.write(Path.of("/nope/f"), b"") { Err(IoError.NotFound(_)) => true, _ => false }
}

scenario "fs unfaked is the real file system" {
  given p = tmp_path("real.txt")
  when  r = { fs.write(p, b"on disk")?; fs.read(p) }
  then  r == Ok(b"on disk")
  then  p.name() == "real.txt"
  then  fs.remove(p) == Ok(())
  then  fs.present(p) == Ok(false)
}
`
	os.WriteFile(p, []byte(src), 0o644)
	code := env.Test(TestOptions{})
	for _, name := range []string{"fs routes to the fake", "fs unfaked is the real file system"} {
		if !strings.Contains(out.String(), `ok  orders.billing  "`+name+`"`) {
			code = -1
		}
	}
	if code != ExitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out.String(), errb.String())
	}
}

func TestStdHttp(t *testing.T) {
	needGo(t)
	env, out, errb := card(t)
	p := filepath.Join(env.Root, "orders/billing/scenarios.jip")
	b, _ := os.ReadFile(p)
	src := strings.Replace(string(b), "use orders.billing\n", "use orders.billing\nuse std.http as web\n", 1) + `
scenario "std.http routes to the fake" {
  given h = FakeHttp.new().route("GET", Url("https://x.test/a"), 200, b"hi").route("", Url("https://x.test/b"), 404, b"").reply(503, b"down")
  when  r = web.get(Url("https://x.test/a"))
  then  match r { Ok(resp) => resp.ok() && resp.text() == "hi", Err(_) => false }
  then  match web.post(Url("https://x.test/b"), b"{}") { Ok(resp) => resp.status() == 404, Err(_) => false }
  then  match web.request("put", Url("https://x.test/c"), Map.new().insert("x-k", "v"), b"") { Ok(resp) => !resp.ok() && resp.body() == b"down", Err(_) => false }
  then  h.requests().len() == 3
  then  match h.requests().get(1) { Some(q) => q.method == "POST" && q.body == b"{}", None => false }
  then  match h.requests().get(2) { Some(q) => q.method == "PUT" && q.headers.get("x-k") == Some("v"), None => false }
}
`
	os.WriteFile(p, []byte(src), 0o644)
	code := env.Test(TestOptions{})
	if !strings.Contains(out.String(), `ok  orders.billing  "std.http routes to the fake"`) {
		code = -1
	}
	if code != ExitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out.String(), errb.String())
	}
}

func TestStdStrAndNumbers(t *testing.T) {
	needGo(t)
	env, out, errb := card(t)
	p := filepath.Join(env.Root, "orders/billing/scenarios.jip")
	b, _ := os.ReadFile(p)
	src := string(b) + `
scenario "str and number methods" {
  given s = "  Total: 12.5\r\nnext\n"
  when  first = s.lines().first().unwrap().trim()
  then  first.strip_prefix("Total: ").unwrap().parse_float() == Some(12.5)
  then  first.index_of(":") == Some(5) && first.slice(0, 5) == "Total"
  then  "a-b".replace("-", "+").repeat(2) == "a+ba+b"
  then  "7".pad_left(3, '0') == "007" && "ab".pad_right(4, '.') == "ab.."
  then  s.trim_end().ends_with("next") && s.trim_start().starts_with("Total")
  then  2.pow(10) == 1024 && 15.clamp(0, 10) == 10
  then  9_223_372_036_854_775_807.checked_add(1) == None && 3.wrapping_mul(-2) == -6
  then  12.5.ceil() == 13 && -12.5.to_int() == -12 && 3.14159.format(2) == "3.14"
  then  !1.5.is_nan() && 1.5.max(2.0).min(1.75) == 1.75 && 9.0.clamp(0.0, 1.0) == 1.0
}
`
	os.WriteFile(p, []byte(src), 0o644)
	code := env.Test(TestOptions{})
	if !strings.Contains(out.String(), `ok  orders.billing  "str and number methods"`) {
		code = -1
	}
	if code != ExitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out.String(), errb.String())
	}
}
