package rt

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestRestateStore(t *testing.T) {
	for store, want := range map[string][3]string{
		"":              {"localhost:8080", "localhost:9070", "127.0.0.1:0"},
		".jip/workflow": {"localhost:8080", "localhost:9070", "127.0.0.1:0"},
		"rs:8080":       {"rs:8080", "rs:9070", "127.0.0.1:0"},
		"rs:8080,admin=adm:1,endpoint=10.0.0.2:9080": {"rs:8080", "adm:1", "10.0.0.2:9080"},
	} {
		r := NewRestate(store)
		if r.Ingress != want[0] || r.Admin != want[1] || r.Endpoint != want[2] {
			t.Fatalf("%q: %s %s %s", store, r.Ingress, r.Admin, r.Endpoint)
		}
	}
}

// restateFrames encodes service protocol messages.
func restateFrames(msgs ...any) []byte {
	var b bytes.Buffer
	for i := 0; i < len(msgs); i += 2 {
		m := msgs[i+1].(*pbuf)
		var hd [8]byte
		binary.BigEndian.PutUint64(hd[:], uint64(msgs[i].(int))<<48|uint64(len(m.b)))
		b.Write(hd[:])
		b.Write(m.b)
	}
	return b.Bytes()
}

// restateTypes decodes the message types of a response.
func restateTypes(t *testing.T, b []byte) (tys []uint16, msgs [][]byte) {
	for len(b) > 0 {
		if len(b) < 8 {
			t.Fatalf("short frame %x", b)
		}
		hd := binary.BigEndian.Uint64(b[:8])
		n := int(uint32(hd))
		tys = append(tys, uint16(hd>>48))
		msgs = append(msgs, b[8:8+n])
		b = b[8+n:]
	}
	return
}

func invoke(t *testing.T, handler string, body []byte) ([]uint16, [][]byte) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /invoke/jip/{handler}", restateInvoke)
	req := httptest.NewRequest("POST", "/invoke/jip/"+handler, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/vnd.restate.invocation.v5")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return restateTypes(t, w.Body.Bytes())
}

func TestRestateInvoke(t *testing.T) {
	input := new(pbuf).msg(14, new(pbuf).bytes(1, []byte(`{"Seq":3}`)))
	tys, msgs := invoke(t, "entry", restateFrames(rsStart, new(pbuf).uint(3, 1), rsInput, input))
	if len(tys) != 3 || tys[0] != rsSetState || tys[1] != rsOutput || tys[2] != rsEnd {
		t.Fatalf("entry: %x", tys)
	}
	if k := string(pfield1(msgs[0], 1)); k != "e/00000003" {
		t.Fatalf("key %q", k)
	}
	// A retry that already journaled the state write sends only the rest.
	tys, _ = invoke(t, "entry", restateFrames(rsStart, new(pbuf).uint(3, 2), rsInput, input))
	if len(tys) != 2 || tys[0] != rsOutput {
		t.Fatalf("retry: %x", tys)
	}
	st := new(pbuf).uint(3, 1).
		msg(4, new(pbuf).bytes(1, []byte("header")).bytes(2, []byte(`{"Workflow":"W"}`))).
		msg(4, new(pbuf).bytes(1, []byte("e/00000001")).bytes(2, []byte(`{"Seq":1}`))).
		msg(4, new(pbuf).bytes(1, []byte("e/00000000")).bytes(2, []byte(`{"Seq":0}`)))
	tys, msgs = invoke(t, "journal", restateFrames(rsStart, st, rsInput, new(pbuf).msg(14, new(pbuf))))
	if len(tys) != 2 || tys[0] != rsOutput {
		t.Fatalf("journal: %x", tys)
	}
	if got := string(pfield1(pfield1(msgs[0], 14), 1)); got != `{"header":{"Workflow":"W"},"entries":[{"Seq":0},{"Seq":1}]}` {
		t.Fatalf("journal %s", got)
	}
	tys, _ = invoke(t, "result", restateFrames(rsStart, new(pbuf).uint(3, 1).uint(5, 1), rsInput, new(pbuf)))
	if len(tys) != 2 || tys[0] != rsError {
		t.Fatalf("partial: %x", tys)
	}
}

func TestRestateDiscover(t *testing.T) {
	req := httptest.NewRequest("GET", "/discover", nil)
	req.Header.Set("Accept", "application/vnd.restate.endpointmanifest.v3+json")
	w := httptest.NewRecorder()
	restateDiscover(w, req)
	var m struct {
		Services []struct {
			Name, Ty string
			Handlers []struct{ Name, Ty string }
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || len(m.Services) != 1 || m.Services[0].Ty != "VIRTUAL_OBJECT" || len(m.Services[0].Handlers) != 5 {
		t.Fatalf("manifest %s %v", w.Body.Bytes(), err)
	}
}

// TestRestateWorkflow runs a journaled workflow on the Restate server whose
// ingress is JIP_RESTATE_ADDR (e.g. `restate-server` on 127.0.0.1:8080) and
// reads its result and journal back through a second runtime.
func TestRestateWorkflow(t *testing.T) {
	addr := os.Getenv("JIP_RESTATE_ADDR")
	if addr == "" {
		t.Skip("JIP_RESTATE_ADDR not set")
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
	if err := ctx.UseWorkflowRuntime("restate", addr); err != nil {
		t.Fatal(err)
	}
	defer ctx.workflows().(*Restate).Close()
	ctx.Approve = FakeApproveScript(ctx, []Result[bool, Rejected]{Result_Ok[bool, Rejected](true)})
	id := StartWorkflow(ctx, spec, 21)

	// A fresh runtime has no local cache: the result comes from Restate,
	// once the run has finished. The first runtime keeps serving the
	// invocations pinned to its endpoint; later ones go to the second.
	other := NewRestate(addr)
	defer other.Close()
	var out any
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		var ok bool
		if out, ok = other.Result(id); ok {
			break
		}
	}
	if out == nil {
		t.Fatal("no result from Restate")
	}
	want, _ := json.Marshal(Result_Ok[int64, WorkflowError[LlmError]](42))
	if got, _ := json.Marshal(out); string(got) != string(want) {
		t.Fatalf("result %s, want %s", got, want)
	}
	j, err := other.Journal(id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Workflow != "Billing" || j.Version != 2 || len(j.Entries) != 2 {
		t.Fatalf("journal %+v", j)
	}
}

func TestRestateBeginFailsWithoutServer(t *testing.T) {
	r := NewRestate("127.0.0.1:1")
	defer r.Close()
	if _, _, err := r.Begin("Billing", 1, nil); err == nil {
		t.Fatal("Begin succeeded without a server")
	}
}
