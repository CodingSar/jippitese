package rt

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestEnumJSON(t *testing.T) {
	v := WorkflowError_App[LlmError](LlmError_Schema("bad"))
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var back WorkflowError[LlmError]
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !Equal(v, back) {
		t.Fatalf("round trip: %s -> %s", Show(v), Show(back))
	}
	var r Result[int64, IoError]
	if err := json.Unmarshal([]byte(`{"Err":{"NotFound":"x"}}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Tag != ResultTag_Err || r.Err_0.Tag != IoErrorTag_NotFound {
		t.Fatalf("got %s", Show(r))
	}
}

func TestConfigLoader(t *testing.T) {
	t.Setenv("JIP_ENV", "")
	t.Setenv("JIP_CONFIG__GATEWAY_URL", "https://pay")
	t.Setenv("JIP_RUN__BILLINGAPI__PORT", "80a0")
	t.Setenv("JIP_TELEMETRY__SAMPEL", "1")
	l := NewConfigLoader(nil)
	if got := l.Str("config.gateway_url", "", CfgRequired, ""); got != "https://pay" {
		t.Fatalf("gateway_url = %q", got)
	}
	l.Int("run.BillingApi.port", 8080, CfgOverridable, "")
	l.Str("telemetry.sample", "1", CfgFixed, "app.jip:3:4")
	err := l.Finish([]string{"JIP_CONFIG__GATEWAY_URL", "JIP_TELEMETRY__SAMPLE", "JIP_RUN__BILLINGAPI__PORT"})
	want := `JIP_RUN__BILLINGAPI__PORT="80a0" does not parse as Int; JIP_TELEMETRY__SAMPEL matches no field (did you mean JIP_TELEMETRY__SAMPLE?)`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	for _, k := range []string{"JIP_TELEMETRY__SAMPEL", "JIP_CONFIG__GATEWAY_URL", "JIP_RUN__BILLINGAPI__PORT"} {
		os.Unsetenv(k)
	}
	t.Setenv("JIP_CONFIG__FIXED", "2")
	l = NewConfigLoader(map[string]map[string]string{"prod": {"config.gateway_url": "https://prod"}})
	l.Str("config.fixed", "1", CfgFixed, "app.jip:7:9")
	if err := l.Finish(nil); err == nil || err.Error() != "JIP_CONFIG__FIXED sets a non-default field `config.fixed` (fixed at app.jip:7:9)" {
		t.Fatalf("got %v", err)
	}
}

type wfState struct{}

func TestWorkflowReplayWithApprove(t *testing.T) {
	calls := 0
	spec := &WorkflowSpec[wfState, int64, int64, Unit, LlmError]{
		Name: "Billing", Version: 2, Sites: []string{"charge@a.jip:1:1", "approve@a.jip:2:2"},
		Init: func(*Ctx) (wfState, error) { return wfState{}, nil },
		Run: func(ctx *Ctx, s wfState, in int64) Result[int64, WorkflowError[LlmError]] {
			r := Step(ctx, "charge@a.jip:1:1", func() int64 { calls++; return in * 2 })
			a := Approve[bool](ctx, "approve@a.jip:2:2", Review{Title: "ok?"})
			if a.Tag == ResultTag_Err {
				return Result_Err[int64](WorkflowError_Approval[LlmError](a.Err_0))
			}
			if !a.Ok_0 {
				return Result_Err[int64](WorkflowError_App[LlmError](LlmError_Budget()))
			}
			return Result_Ok[int64, WorkflowError[LlmError]](r)
		},
	}
	ctx := NewCtx()
	ctx.Approve = FakeApproveScript(ctx, []Result[bool, Rejected]{Result_Ok[bool, Rejected](true)})
	res := RunWorkflow(ctx, spec, 21)
	if res.Tag != ResultTag_Ok || res.Ok_0 != 42 {
		t.Fatalf("got %s", Show(res))
	}
	if calls != 1 {
		t.Fatalf("charge ran %d times; journal should replay it", calls)
	}
	// Replay a stored journal: no step runs again, the answer is recorded.
	dir := t.TempDir()
	ctx2 := NewCtx()
	if err := ctx2.UseWorkflowRuntime("embedded", dir); err != nil {
		t.Fatal(err)
	}
	ctx2.Approve = FakeApproveScript(ctx2, []Result[bool, Rejected]{Result_Ok[bool, Rejected](false)})
	id := StartWorkflow(ctx2, spec, 5)
	var out any
	for i := 0; i < 200; i++ {
		var done bool
		if out, done = WorkflowResult(ctx2, id); done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if out == nil {
		t.Fatal("workflow did not finish")
	}
	j, err := LoadJournal(dir + "/Billing-" + string(id) + ".jsonl")
	if err != nil {
		t.Fatal(err)
	}
	before := calls
	ctx3 := NewCtx()
	rep := ReplayWorkflow(ctx3, spec, 5, j)
	if calls != before {
		t.Fatalf("replay re-ran a step")
	}
	if rep.Tag != ResultTag_Err || rep.Err_0.Tag != WorkflowErrorTag_App {
		t.Fatalf("replay got %s", Show(rep))
	}
	// Version drift.
	spec.Version = 3
	if r := ReplayWorkflow(ctx3, spec, 5, j); r.Tag != ResultTag_Err || r.Err_0.Tag != WorkflowErrorTag_Replay {
		t.Fatalf("version drift got %s", Show(r))
	}
}

func TestMemCacheExpiry(t *testing.T) {
	ctx := NewCtx()
	clock := NewFakeClock()
	ctx.Clock = clock
	c := MemCacheNew[string, int64](ctx, 5*time.Minute)
	MemCacheSet(ctx, c, "a", 1, Option_None[time.Duration]())
	if g := MemCacheGet(ctx, c, "a"); g.Ok_0.Tag != OptionTag_Some {
		t.Fatal("expected hit")
	}
	clock.Advance(6 * time.Minute)
	if g := MemCacheGet(ctx, c, "a"); g.Ok_0.Tag != OptionTag_None {
		t.Fatal("expected expiry")
	}
}

func TestCron(t *testing.T) {
	c, err := parseCron("*/15 9-17 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 17, 18, 0, 0, 0, time.UTC) // Saturday
	n := c.next(from)
	want := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	if !n.Equal(want) {
		t.Fatalf("next = %v want %v", n, want)
	}
}

func TestWorkflowResumesSuspendedRun(t *testing.T) {
	calls := 0
	spec := &WorkflowSpec[wfState, int64, int64, Unit, LlmError]{
		Name: "Billing", Version: 2, Sites: []string{"charge@a.jip:1:1", "approve@a.jip:2:2"},
		Init: func(*Ctx) (wfState, error) { return wfState{}, nil },
		Run: func(ctx *Ctx, s wfState, in int64) Result[int64, WorkflowError[LlmError]] {
			r := Step(ctx, "charge@a.jip:1:1", func() int64 { calls++; return in * 2 })
			a := Approve[bool](ctx, "approve@a.jip:2:2", Review{Title: "ok?"})
			if a.Tag == ResultTag_Err || !a.Ok_0 {
				return Result_Err[int64](WorkflowError_App[LlmError](LlmError_Budget()))
			}
			return Result_Ok[int64, WorkflowError[LlmError]](r)
		},
	}
	dir := t.TempDir()

	// The first process asks through a channel that never answers, then
	// goes away.
	gctx, stop := context.WithCancel(context.Background())
	defer stop()
	ctx1 := NewCtx()
	ctx1.Go = gctx
	if err := ctx1.UseWorkflowRuntime("embedded", dir); err != nil {
		t.Fatal(err)
	}
	asked := make(chan string, 1)
	ctx1.Approve = ApproveFunc(func(c *Ctx, id string, _ Review) ReviewAnswer {
		asked <- id
		<-c.Go.Done()
		return ReviewAnswer_Reject(Option_None[string]())
	})
	id := StartWorkflow(ctx1, spec, 21)
	var key string
	select {
	case key = <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("approval not asked")
	}
	if key != string(id)+"/approve@a.jip:2:2#1" {
		t.Fatalf("review id %q", key)
	}

	// The second process resumes the run under its id and asks again.
	ctx2 := NewCtx()
	if err := ctx2.UseWorkflowRuntime("embedded", dir); err != nil {
		t.Fatal(err)
	}
	ctx2.Approve = FakeApproveScript(ctx2, []Result[bool, Rejected]{Result_Ok[bool, Rejected](true)})
	ResumeWorkflows(ctx2, spec)
	ResumeWorkflows(ctx2, spec) // once per workflow
	var out any
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		var done bool
		if out, done = WorkflowResult(ctx2, id); done {
			break
		}
	}
	if r, ok := out.(Result[int64, WorkflowError[LlmError]]); !ok || r.Tag != ResultTag_Ok || r.Ok_0 != 42 {
		t.Fatalf("resumed result %s", Show(out))
	}
	if calls != 1 {
		t.Fatalf("charge ran %d times; the resumed run should replay it", calls)
	}
	if left := ctx2.workflows().(*Embedded).Suspended("Billing"); len(left) != 0 {
		t.Fatalf("%d runs still suspended", len(left))
	}
}
