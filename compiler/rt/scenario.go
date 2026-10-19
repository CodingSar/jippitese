package rt

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ScnOpts describes one scenario to the harness.
type ScnOpts struct {
	Module   string
	Name     string
	Tags     []string
	Property bool
	File     string
	Line     int
	Col      int
}

// Scn is the state of one scenario run: the ctx the generated code sees,
// the current step and the bindings reported on failure.
type Scn struct {
	T       *testing.T
	Opts    ScnOpts
	Ctx     *Ctx
	step    string
	stepPos string
	binds   []bind
	seed    int64
	gen     *Gen
	tmp     string
}

type bind struct {
	name string
	v    any
}

// ScenarioError is a failure raised by a scenario step.
type ScenarioError struct{ Pos, Msg string }

func (e *ScenarioError) Error() string { return e.Msg }

type scnFailure struct {
	pos   string
	step  string
	msg   string
	binds []bind
	seed  int64
}

type testLogger struct{ t *testing.T }

func (l testLogger) Logf(format string, args ...any) { l.t.Logf(format, args...) }

// envInt reads an integer environment variable with a default.
func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func scnSeed() int64 {
	if v := os.Getenv("JIP_SEED"); v != "" {
		s := strings.TrimPrefix(strings.TrimPrefix(v, "0x"), "0X")
		if n, err := strconv.ParseUint(s, 16, 64); err == nil {
			return int64(n)
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return time.Now().UnixNano()
}

func newScn(t *testing.T, o ScnOpts, seed int64, extern bool) *Scn {
	s := &Scn{T: t, Opts: o, seed: seed}
	var c *Ctx
	if extern {
		c = NewCtx()
	} else {
		c = &Ctx{
			Go:      context.Background(),
			Clock:   NewFakeClock(),
			Rand:    rand.New(rand.NewSource(seed)),
			Http:    boundaryHttp{},
			Llm:     boundaryLlm{},
			Decide:  boundaryDecide{},
			Approve: boundaryApprove{},
			sh:      newShared(testLogger{t}),
		}
	}
	c.scn = s
	s.Ctx = c
	s.gen = &Gen{R: rand.New(rand.NewSource(seed)), Size: 100}
	return s
}

// Scenario runs one scenario under the harness: filter, tags, seeds,
// property runs with shrinking, and the `@jip` report lines `jip test`
// reads.
func Scenario(t *testing.T, o ScnOpts, body func(*Scn)) {
	t.Helper()
	Contracts.Pre, Contracts.Post, Contracts.Inv = true, true, true
	label := fmt.Sprintf("%s  %q", o.Module, o.Name)
	if f := os.Getenv("JIP_SCENARIO_FILTER"); f != "" {
		if !strings.Contains(o.Name, f) && !strings.Contains(o.Module, f) {
			t.SkipNow()
		}
	}
	extern := false
	for _, tag := range o.Tags {
		switch {
		case strings.HasPrefix(tag, "skip:"):
			fmt.Printf("@jip skip  %s  %s\n", label, strings.TrimPrefix(tag, "skip:"))
			t.Skip()
			return
		case tag == "slow":
			if os.Getenv("JIP_SLOW") != "1" {
				fmt.Printf("@jip slow  %s\n", label)
				t.Skip()
				return
			}
		case tag == "extern":
			extern = true
		}
	}
	seed := scnSeed()
	runs := 1
	if o.Property {
		runs = envInt("JIP_RUNS", 200)
		if runs < 1 {
			runs = 1
		}
	}
	for run := 0; run < runs; run++ {
		s := newScn(t, o, seed+int64(run), extern)
		f := s.attempt(body)
		if f == nil {
			continue
		}
		if o.Property {
			f = s.shrink(body, f)
			s.writeSeed(f.seed)
		}
		s.report(label, f, o.Property)
		t.Fail()
		return
	}
	fmt.Printf("@jip ok  %s\n", label)
}

// attempt runs the body once and converts any panic into a failure.
func (s *Scn) attempt(body func(*Scn)) (f *scnFailure) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		f = &scnFailure{pos: s.stepPos, step: s.step, binds: s.binds, seed: s.seed}
		switch e := r.(type) {
		case *ScenarioError:
			if e.Pos != "" {
				f.pos = e.Pos
			}
			f.msg = e.Msg
		case *ContractError:
			f.msg = fmt.Sprintf("contract violated: %s %s at %s", e.Kind, e.Expr, e.At)
		case error:
			f.msg = e.Error()
		default:
			f.msg = fmt.Sprintf("panic: %v", r)
		}
	}()
	body(s)
	return nil
}

// shrink re-runs a failing property scenario with smaller generators and
// keeps the smallest failure found.
func (s *Scn) shrink(body func(*Scn), f *scnFailure) *scnFailure {
	size := 100
	for i := 0; i < 20 && size > 1; i++ {
		size /= 2
		n := newScn(s.T, s.Opts, f.seed, false)
		n.gen.Size = size
		if g := n.attempt(body); g != nil {
			f = g
		}
	}
	return f
}

func (s *Scn) writeSeed(seed int64) {
	dir := os.Getenv("JIP_SEED_DIR")
	if dir == "" {
		return
	}
	modDir := filepath.Join(strings.Split(s.Opts.Module, ".")...)
	path := filepath.Join(dir, modDir, "scenarios", slug(s.Opts.Name)+".seed")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(fmt.Sprintf("0x%x\n", uint64(seed))), 0o644)
}

func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func (s *Scn) report(label string, f *scnFailure, property bool) {
	var b strings.Builder
	fmt.Fprintf(&b, "@jip FAIL  %s\n", label)
	if f.pos != "" {
		fmt.Fprintf(&b, "    at %s\n", f.pos)
	}
	if f.step != "" {
		fmt.Fprintf(&b, "    %s\n", f.step)
	}
	if f.msg != "" {
		fmt.Fprintf(&b, "    %s\n", f.msg)
	}
	for _, x := range f.binds {
		fmt.Fprintf(&b, "    %s = %s\n", x.name, Show(x.v))
	}
	if property {
		fmt.Fprintf(&b, "    seed 0x%x\n", uint64(f.seed))
		fmt.Fprintf(&b, "    replay: jip test %s --seed 0x%x\n", s.Opts.Module, uint64(f.seed))
	}
	fmt.Print(b.String())
}

// Given marks a `given` step.
func (s *Scn) Given(pos, text string) { s.step, s.stepPos = "given "+text, pos }

// When marks a `when` step.
func (s *Scn) When(pos, text string) { s.step, s.stepPos = "when "+text, pos }

// Then checks a `then` step.
func (s *Scn) Then(pos, text string, ok bool) {
	s.step, s.stepPos = "then "+text, pos
	if !ok {
		panic(&ScenarioError{Pos: pos, Msg: "then " + text + " is false"})
	}
}

// Fail aborts the scenario at pos.
func (s *Scn) Fail(pos, msg string) {
	panic(&ScenarioError{Pos: pos, Msg: msg})
}

// Bind records a named value for the failure report.
func (s *Scn) Bind(name string, v any) { s.binds = append(s.binds, bind{name, v}) }

// Install puts a fake into the ctx when v is one; other values are
// ignored. Every `given` installs its value.
func (s *Scn) Install(v any) {
	switch f := v.(type) {
	case *FakeHttp:
		s.Ctx.Http = f
	case *FakeClock:
		s.Ctx.Clock = f
	case *FakeRandom:
		s.Ctx.Rand = rand.New(rand.NewSource(f.Seed))
	case *FakeLlm:
		if cur, ok := s.Ctx.Llm.(*FakeLlm); ok && cur != f {
			cur.mu.Lock()
			f.mu.Lock()
			for k, x := range f.vals {
				cur.vals[k] = x
			}
			f.mu.Unlock()
			cur.mu.Unlock()
		} else {
			s.Ctx.Llm = f
		}
	case *FakeDecide:
		s.Ctx.Decide = f
	case *FakeApprove:
		s.Ctx.Approve = f
	}
}

// RealTime switches the scenario to the wall clock (`given real_time`).
func (s *Scn) RealTime() { s.Ctx.Clock = RealClock{} }

// TempDir returns the scenario's scratch directory.
func (s *Scn) TempDir() string {
	if s.tmp == "" {
		s.tmp = s.T.TempDir()
	}
	return s.tmp
}

// Gen returns the scenario's generator.
func (s *Scn) Gen() *Gen { return s.gen }

// Any draws a value for `any T` or `any T via f`.
func Any[T any](s *Scn, pos string, f func(*Ctx, *Gen) T) T {
	s.stepPos = pos
	return f(s.Ctx, s.gen)
}

// failf fails the enclosing scenario, or panics outside one.
func (c *Ctx) failf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if c.scn != nil {
		panic(&ScenarioError{Pos: c.scn.stepPos, Msg: msg})
	}
	panic(&PanicError{Msg: msg})
}
