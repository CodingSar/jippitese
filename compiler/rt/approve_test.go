package rt

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestApproveHttp(t *testing.T) {
	a, err := NewApproveHttp("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := NewCtx()
	ctx.Effects.ApproveTimeout = time.Minute
	base := "http://" + a.Addr + "/reviews"
	out := reflect.TypeOf(Result[int64, Rejected]{})

	// pendingId waits for the one pending review and returns its id
	pendingId := func() string {
		for i := 0; i < 400; i++ {
			a.mu.Lock()
			for k := range a.pending {
				a.mu.Unlock()
				return k
			}
			a.mu.Unlock()
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("no pending review")
		return ""
	}
	ask := func(r Review) chan Result[int64, Rejected] {
		ch := make(chan Result[int64, Rejected], 1)
		go func() { ch <- a.Ask(ctx, r, out).(Result[int64, Rejected]) }()
		return ch
	}
	post := func(path, body string) int {
		resp, err := http.Post(base+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// approve with a value, after a rejected bad body
	ch := ask(Review{Title: "refund", Body: "order 7"})
	pendingId()
	resp, err := http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	var list []reviewView
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 1 || list[0].Title != "refund" {
		t.Fatalf("list: %+v", list)
	}
	id := list[0].Id
	if c := post("/"+id+"/approve", `"seven"`); c != http.StatusBadRequest {
		t.Fatalf("bad value: %d", c)
	}
	if c := post("/"+id+"/approve", ``); c != http.StatusBadRequest {
		t.Fatalf("empty value: %d", c)
	}
	if c := post("/"+id+"/approve", `7`); c != http.StatusOK {
		t.Fatalf("approve: %d", c)
	}
	if r := <-ch; r.Tag != ResultTag_Ok || r.Ok_0 != 7 {
		t.Fatalf("approved: %+v", r)
	}
	if c := post("/"+id+"/approve", `7`); c != http.StatusNotFound {
		t.Fatalf("answered twice: %d", c)
	}

	// reject with a reason
	ch = ask(Review{Title: "delete"})
	id = pendingId()
	if c := post("/"+id+"/reject", `{"reason": "no"}`); c != http.StatusOK {
		t.Fatalf("reject: %d", c)
	}
	if r := <-ch; r.Tag != ResultTag_Err || r.Err_0.Timed_out || r.Err_0.Reason.Some_0 != "no" {
		t.Fatalf("rejected: %+v", r)
	}

	// the review's own timeout
	ch = ask(Review{Title: "late", Timeout: Option_Some(30 * time.Millisecond)})
	if r := <-ch; r.Tag != ResultTag_Err || !r.Err_0.Timed_out {
		t.Fatalf("timed out: %+v", r)
	}
	if len(a.pending) != 0 {
		t.Fatalf("still pending: %d", len(a.pending))
	}

	// Unit decisions take an empty body
	unit := make(chan any, 1)
	go func() { unit <- a.Ask(ctx, Review{Title: "gate"}, reflect.TypeOf(Result[Unit, Rejected]{})) }()
	id = pendingId()
	if c := post("/"+id+"/approve", ``); c != http.StatusOK {
		t.Fatalf("unit approve: %d", c)
	}
	if r := (<-unit).(Result[Unit, Rejected]); r.Tag != ResultTag_Ok {
		t.Fatalf("unit: %+v", r)
	}
}

func TestApproveAddr(t *testing.T) {
	for addr, ok := range map[string]bool{"": true, "127.0.0.1:9000": true, "localhost:1": true, "[::1]:2": true, "0.0.0.0:80": false, "10.0.0.1:80": false, ":80": false} {
		t.Setenv("JIP_APPROVE_ADDR", addr)
		if _, err := approveAddr(); (err == nil) != ok {
			t.Fatalf("%q: %v", addr, err)
		}
	}
	ctx := NewCtx()
	e := DefaultEffects()
	e.ApproveChannel = "orders.inbox"
	if err := ctx.ApplyEffects(e); err == nil || !strings.Contains(err.Error(), "std.approve.http") {
		t.Fatalf("user channel: %v", err)
	}
}

func TestApproveFunc(t *testing.T) {
	ctx := NewCtx()
	ctx.Effects.ApproveTimeout = time.Minute
	out := reflect.TypeOf(Result[int64, Rejected]{})
	ask := func(f ApproveFunc, r Review) Result[int64, Rejected] {
		return f.Ask(ctx, r, out).(Result[int64, Rejected])
	}
	var gotId string
	approve := ApproveFunc(func(c *Ctx, id string, r Review) ReviewAnswer {
		gotId = id
		return ReviewAnswer_Approve(Json{Raw: []byte(r.Body)})
	})
	if r := ask(approve, Review{Title: "refund", Body: "42"}); r.Tag != ResultTag_Ok || r.Ok_0 != 42 || gotId == "" {
		t.Fatalf("approve: %+v id %q", r, gotId)
	}
	if r := ask(approve, Review{Title: "refund", Body: `"many"`}); r.Tag != ResultTag_Err || r.Err_0.Reason.Tag != OptionTag_Some || !strings.Contains(r.Err_0.Reason.Some_0, "does not decode") {
		t.Fatalf("bad decision: %+v", r)
	}
	reject := ApproveFunc(func(*Ctx, string, Review) ReviewAnswer { return ReviewAnswer_Reject(Option_Some("too large")) })
	if r := ask(reject, Review{Title: "refund"}); r.Tag != ResultTag_Err || r.Err_0.Reason.Some_0 != "too large" || r.Err_0.Timed_out {
		t.Fatalf("reject: %+v", r)
	}

	// a channel that never answers times out, and its ctx is cancelled
	stopped := make(chan struct{})
	block := ApproveFunc(func(c *Ctx, _ string, _ Review) ReviewAnswer {
		<-c.Go.Done()
		close(stopped)
		return ReviewAnswer_Reject(Option_None[string]())
	})
	if r := ask(block, Review{Title: "refund", Timeout: Option_Some(20 * time.Millisecond)}); r.Tag != ResultTag_Err || !r.Err_0.Timed_out {
		t.Fatalf("timeout: %+v", r)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("channel ctx not cancelled")
	}

	// a panic in the channel is raised on the asking goroutine
	boom := ApproveFunc(func(*Ctx, string, Review) ReviewAnswer { panic("channel down") })
	func() {
		defer func() {
			if p := recover(); p != "channel down" {
				t.Fatalf("panic %v", p)
			}
		}()
		ask(boom, Review{Title: "refund"})
	}()
}

func TestApplyEffectsUserChannel(t *testing.T) {
	ctx := NewCtx()
	e := DefaultEffects()
	e.ApproveChannel = "acme.slack"
	if err := ctx.ApplyEffects(e); err == nil || !strings.Contains(err.Error(), "not a channel of this build") {
		t.Fatalf("unregistered: %v", err)
	}
	ctx.AddApproveChannel("acme.slack", func(*Ctx, string, Review) ReviewAnswer { return ReviewAnswer_Reject(Option_None[string]()) })
	if err := ctx.ApplyEffects(e); err != nil {
		t.Fatal(err)
	}
	if _, ok := ctx.Approve.(ApproveFunc); !ok {
		t.Fatalf("approve client %T", ctx.Approve)
	}
}
