package rt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func netCtx(h RealHttp) (*Ctx, *FakeClock) {
	clock := NewFakeClock()
	ctx := NewCtx()
	ctx.Clock, ctx.Http = clock, h
	return ctx, clock
}

// An idempotent request is retried on 503 with exponential backoff on the
// ctx clock; a POST is not.
func TestRealHttpRetry(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen", r.Method+" "+string(b))
		w.Write([]byte("fine"))
	}))
	defer srv.Close()
	ctx, clock := netCtx(RealHttp{Timeout: time.Second, Retry: Retry{Max: 3, Backoff: Backoff_Exp(100 * time.Millisecond)}})
	start := clock.Now().Time()
	r := NetGet(ctx, Url(srv.URL))
	if r.Tag != ResultTag_Ok || r.Ok_0.Status != 200 || HttpResponseText(ctx, r.Ok_0) != "fine" {
		t.Fatalf("get: %s", Show(r))
	}
	if got := clock.Now().Time().Sub(start); got != 300*time.Millisecond {
		t.Fatalf("slept %v, want 100ms+200ms", got)
	}
	if v := MapGet(ctx, r.Ok_0.Headers, "x-seen"); v.Tag != OptionTag_Some || v.Some_0 != "GET" {
		t.Fatalf("headers: %s", Show(r.Ok_0.Headers))
	}
	hits.Store(0)
	p := NetPost(ctx, Url(srv.URL), []byte(`{}`))
	if p.Tag != ResultTag_Ok || p.Ok_0.Status != 503 || hits.Load() != 1 {
		t.Fatalf("post: %s after %d hits", Show(p), hits.Load())
	}
}

func TestRealHttpErrors(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	ctx, _ := netCtx(RealHttp{Timeout: 20 * time.Millisecond})
	if r := NetGet(ctx, Url(srv.URL)); r.Tag != ResultTag_Err || r.Err_0.Tag != NetErrorTag_Timeout {
		t.Fatalf("timeout: %s", Show(r))
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	if r := NetGet(ctx, Url(url)); r.Tag != ResultTag_Err || r.Err_0.Tag != NetErrorTag_Refused {
		t.Fatalf("refused: %s", Show(r))
	}
	// A cancelled program stops retrying.
	ctx, _ = netCtx(RealHttp{Retry: Retry{Max: 100, Backoff: Backoff_Fixed(time.Millisecond)}})
	gctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx.Go = gctx
	if r := NetGet(ctx, Url(url)); r.Tag != ResultTag_Err {
		t.Fatalf("cancelled: %s", Show(r))
	}
}

func TestFakeHttpRoutes(t *testing.T) {
	ctx := NewCtx()
	f := FakeHttpRoute(ctx, FakeHttpNew(ctx), "GET", Url("https://x.test/a"), 200, []byte("a"))
	f = FakeHttpRoute(ctx, f, "", Url("https://x.test/b"), 201, []byte("b"))
	f = FakeHttpReply(ctx, f, 500, []byte("rest"))
	ctx.Http = f
	h := MapInsert(ctx, MapNew[string, string](ctx), "X-K", "v")
	for _, c := range []struct {
		method, url string
		status      int64
		body        string
	}{{"get", "https://x.test/a", 200, "a"}, {"POST", "https://x.test/a", 500, "rest"}, {"PUT", "https://x.test/b", 201, "b"}} {
		r := NetRequest(ctx, c.method, Url(c.url), h, []byte("q"))
		if r.Tag != ResultTag_Ok || r.Ok_0.Status != c.status || string(r.Ok_0.Body) != c.body {
			t.Fatalf("%s %s: %s", c.method, c.url, Show(r))
		}
	}
	reqs := FakeHttpRequests(ctx, f)
	if n := ListLen(ctx, reqs); n != 3 {
		t.Fatalf("%d requests", n)
	}
	first := reqs[0]
	if first.Method != "GET" || string(first.Body) != "q" || MapGet(ctx, first.Headers, "X-K").Some_0 != "v" {
		t.Fatalf("recorded %s", Show(first))
	}
}
