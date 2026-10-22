package rt

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestProtobufRoundTrip(t *testing.T) {
	var m pbuf
	m.str(1, "default").uint(3, 300).msg(8, new(pbuf).msg(1, new(pbuf).uint(3, 26))).bytes(9, []byte{})
	if got := string(pfield1(m.b, 1)); got != "default" {
		t.Fatalf("field 1: %q", got)
	}
	if v, ok := pvarint(m.b, 3); !ok || v != 300 {
		t.Fatalf("field 3: %d %v", v, ok)
	}
	evs := pfield(pfield1(m.b, 8), 1)
	if len(evs) != 1 {
		t.Fatalf("events: %d", len(evs))
	}
	if v, _ := pvarint(evs[0], 3); v != 26 {
		t.Fatalf("event type %d", v)
	}
	if b := pfield1(m.b, 9); b == nil || len(b) != 0 {
		t.Fatalf("empty field 9: %v", b)
	}
	if pfield1(m.b, 2) != nil {
		t.Fatal("absent field 2 not nil")
	}
	data, ok := payloadData(payloads([]byte(`{"a":1}`)).b)
	if !ok || string(data) != `{"a":1}` {
		t.Fatalf("payload %q %v", data, ok)
	}
	pscan([]byte{0xff}, func(int, int, uint64, []byte) { t.Fatal("malformed input scanned") })
}

func TestTemporalStore(t *testing.T) {
	for store, want := range map[string][2]string{
		"":                    {"localhost:7233", "default"},
		".jip/workflow":       {"localhost:7233", "default"},
		"tmp:7233":            {"tmp:7233", "default"},
		"tmp:7233/billing-ns": {"tmp:7233", "billing-ns"},
	} {
		tp := NewTemporal(store)
		if tp.Addr != want[0] || tp.Namespace != want[1] {
			t.Fatalf("%q: %s %s", store, tp.Addr, tp.Namespace)
		}
	}
}

// TestTemporalWorkflow runs a journaled workflow on the Temporal server at
// JIP_TEMPORAL_ADDR (e.g. `temporal server start-dev` on localhost:7233)
// and reads its result back through a second runtime.
func TestTemporalWorkflow(t *testing.T) {
	addr := os.Getenv("JIP_TEMPORAL_ADDR")
	if addr == "" {
		t.Skip("JIP_TEMPORAL_ADDR not set")
	}
	spec := &WorkflowSpec[wfState, int64, int64, Unit, LlmError]{
		Name: "Billing", Version: 2, Sites: []string{"charge@a.jip:1:1", "approve@a.jip:2:2"},
		Init: func(*Ctx) (wfState, error) { return wfState{}, nil },
		Run: func(ctx *Ctx, s wfState, in int64) Result[int64, WorkflowError[LlmError]] {
			r := Step(ctx, "charge@a.jip:1:1", func() int64 { return in * 2 })
			a := Approve[bool](ctx, "approve@a.jip:2:2", Review{Title: "ok?"})
			if a.Tag == ResultTag_Err || !a.Ok_0 {
				return Result_Err[int64](WorkflowError_App[LlmError](LlmError_Budget()))
			}
			return Result_Ok[int64, WorkflowError[LlmError]](r)
		},
	}
	ctx := NewCtx()
	if err := ctx.UseWorkflowRuntime("temporal", addr); err != nil {
		t.Fatal(err)
	}
	defer ctx.workflows().(*Temporal).Close()
	ctx.Approve = FakeApproveScript(ctx, []Result[bool, Rejected]{Result_Ok[bool, Rejected](true)})
	id := StartWorkflow(ctx, spec, 21)

	// A fresh runtime has no local cache: the result comes from Temporal,
	// once the worker has completed the execution.
	other := NewTemporal(addr)
	defer other.Close()
	var out any
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		var ok bool
		if out, ok = other.Result(id); ok {
			break
		}
	}
	if out == nil {
		t.Fatal("no result from Temporal")
	}
	want, _ := json.Marshal(Result_Ok[int64, WorkflowError[LlmError]](42))
	if got, _ := json.Marshal(out); string(got) != string(want) {
		t.Fatalf("result %s, want %s", got, want)
	}

	// The history holds the journal: header, the step, the pending
	// approval and its resolution, then the result.
	var req pbuf
	req.str(1, other.Namespace)
	req.msg(2, new(pbuf).str(1, string(id)))
	resp, err := other.call(t.Context(), "GetWorkflowExecutionHistory", req.b)
	if err != nil {
		t.Fatal(err)
	}
	var signals []string
	for _, ev := range pfield(pfield1(resp, 1), 1) {
		if typ, _ := pvarint(ev, 3); typ == 26 {
			signals = append(signals, string(pfield1(pfield1(ev, 26), 1)))
		}
	}
	if len(signals) != 4 || signals[0] != "jip.step" || signals[1] != "jip.step" || signals[2] != "jip.resolve" || signals[3] != "jip.finish" {
		t.Fatalf("signals %v", signals)
	}
}

func TestTemporalBeginFailsWithoutServer(t *testing.T) {
	tp := NewTemporal("127.0.0.1:1")
	defer tp.Close()
	if _, _, err := tp.Begin("Billing", 1, nil); err == nil {
		t.Fatal("Begin succeeded without a server")
	}
}
