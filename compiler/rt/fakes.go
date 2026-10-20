package rt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- http ----------------------------------------------------------------

// HttpReq is one outgoing request of the `net` effect.
type HttpReq struct {
	Method string
	Url    string
	Header map[string]string
	Body   []byte // nil sends no body
}

// HttpResp is a response; every status is one.
type HttpResp struct {
	Status int
	Header map[string]string // lower-case names, repeated values joined by ", "
	Body   []byte
}

// HttpClient is where the `net` effect goes: std.http and a dependency's
// http module both call it.
type HttpClient interface {
	Do(ctx *Ctx, r HttpReq) (HttpResp, error)
}

// RealHttp is the production client: each attempt is bounded by Timeout,
// and a transport failure or a 502/503/504 to an idempotent method is
// retried by Retry (spec 10 rule 3, `effects.net`).
type RealHttp struct {
	Timeout time.Duration
	Retry   Retry
}

// DefaultHttp is the client of `effects.net` defaults: 5s, 3 retries,
// exponential from 100ms.
func DefaultHttp() RealHttp {
	e := DefaultEffects()
	return RealHttp{Timeout: e.NetTimeout, Retry: e.NetRetry}
}

func (h RealHttp) Do(ctx *Ctx, r HttpReq) (HttpResp, error) {
	retries := 0
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		retries = int(h.Retry.Max)
	}
	for i := 0; ; i++ {
		resp, err := h.once(ctx, r)
		again := err != nil || resp.Status == 502 || resp.Status == 503 || resp.Status == 504
		if !again || i >= retries || ctx.Go.Err() != nil {
			return resp, err
		}
		ctx.Clock.Sleep(ctx, backoffDelay(h.Retry.Backoff, i+1))
	}
}

func (h RealHttp) once(ctx *Ctx, r HttpReq) (HttpResp, error) {
	gctx := ctx.Go
	if h.Timeout > 0 {
		var cancel context.CancelFunc
		gctx, cancel = context.WithTimeout(gctx, h.Timeout)
		defer cancel()
	}
	var rd io.Reader
	if r.Body != nil {
		rd = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(gctx, r.Method, r.Url, rd)
	if err != nil {
		return HttpResp{}, err
	}
	for k, v := range r.Header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return HttpResp{}, err
	}
	defer resp.Body.Close()
	out := HttpResp{Status: resp.StatusCode, Header: map[string]string{}}
	for k, vs := range resp.Header {
		out.Header[strings.ToLower(k)] = strings.Join(vs, ", ")
	}
	out.Body, err = io.ReadAll(resp.Body)
	return out, err
}

type boundaryHttp struct{}

func (boundaryHttp) Do(ctx *Ctx, r HttpReq) (HttpResp, error) {
	boundary("net")
	return HttpResp{}, nil
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// HttpPost implements `http.post(url, body)` of the http dependency: R is
// the Ok type, E the error enum with `Timeout`, `Status(Int)` and
// `Transport(Str)` variants.
func HttpPost[R, E any](ctx *Ctx, url Url, body []byte) Result[R, E] {
	return httpCall[R, E](ctx, http.MethodPost, string(url), body)
}

// HttpGet implements `http.get(url)`.
func HttpGet[R, E any](ctx *Ctx, url Url) Result[R, E] {
	return httpCall[R, E](ctx, http.MethodGet, string(url), nil)
}

func httpCall[R, E any](ctx *Ctx, method, url string, body []byte) Result[R, E] {
	r := HttpReq{Method: method, Url: url, Body: body}
	if body != nil {
		r.Header = map[string]string{"Content-Type": "application/json"}
	}
	hr, err := ctx.Http.Do(ctx, r)
	status, resp := hr.Status, hr.Body
	if err != nil {
		if isTimeout(err) {
			return Result_Err[R](makeVariant[E]("Timeout"))
		}
		return Result_Err[R](makeVariant[E]("Transport", err.Error()))
	}
	var zero R
	rt := reflect.TypeOf(zero)
	// A response record with a status field sees every status; a bare body
	// type sees only successes.
	if !hasInt64Field(rt) && (status < 200 || status >= 300) {
		return Result_Err[R](makeVariant[E]("Status", int64(status)))
	}
	return Result_Ok[R, E](makeResp[R](status, resp))
}

func hasInt64Field(t reflect.Type) bool {
	if t == nil || t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Type.Kind() == reflect.Int64 {
			return true
		}
	}
	return false
}

func makeResp[R any](status int, body []byte) R {
	var zero R
	v := reflect.New(reflect.TypeOf(zero)).Elem()
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			switch {
			case f.Kind() == reflect.Int64:
				f.SetInt(int64(status))
			case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.Uint8:
				f.SetBytes(append([]byte(nil), body...))
			case f.Kind() == reflect.String:
				f.SetString(string(body))
			}
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			v.SetBytes(append([]byte(nil), body...))
		}
	case reflect.String:
		v.SetString(string(body))
	}
	return v.Interface().(R)
}

// makeVariant builds the variant `name` of enum E with the given payload.
func makeVariant[E any](name string, payload ...any) E {
	var zero E
	t := reflect.TypeOf(zero)
	if !isEnum(t) {
		panic(fmt.Sprintf("rt: %v is not an enum", t))
	}
	v := reflect.New(t).Elem()
	names := variantNames(v)
	tag := -1
	for i, n := range names {
		if n == name {
			tag = i
		}
	}
	if tag < 0 {
		if name != "Transport" {
			return makeVariant[E]("Transport", fmt.Sprintf("%s %v", name, payload))
		}
		panic(fmt.Sprintf("rt: enum %v has no variant %s", t, name))
	}
	v.Field(0).SetUint(uint64(tag))
	idx, _, _ := payloadFields(t, name)
	for i, p := range payload {
		if i >= len(idx) {
			break
		}
		f := v.Field(idx[i])
		f.Set(reflect.ValueOf(p).Convert(f.Type()))
	}
	return v.Interface().(E)
}

type fakeReply struct {
	status int64
	body   []byte
}

type fakeRoute struct {
	method, url string
	reply       fakeReply
}

// FakeHttp is `std.testing.FakeHttp`: requests are answered by the first
// route whose method and URL match, else by the scripted replies in order
// (the last one sticks); every request is recorded.
type FakeHttp struct {
	mu       sync.Mutex
	routes   []fakeRoute
	replies  []fakeReply
	requests []TestingRequest
}

func (f *FakeHttp) Do(ctx *Ctx, r HttpReq) (HttpResp, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, TestingRequest{
		Method:  r.Method,
		Url:     Url(r.Url),
		Headers: MapFromGo(r.Header),
		Body:    append([]byte(nil), r.Body...),
	})
	for _, rt := range f.routes {
		if (rt.method == "" || rt.method == r.Method) && rt.url == r.Url {
			return HttpResp{Status: int(rt.reply.status), Header: map[string]string{}, Body: append([]byte(nil), rt.reply.body...)}, nil
		}
	}
	if len(f.replies) == 0 {
		return HttpResp{}, errors.New("FakeHttp: no reply scripted for " + r.Method + " " + r.Url)
	}
	rep := f.replies[0]
	if len(f.replies) > 1 {
		f.replies = f.replies[1:]
	}
	return HttpResp{Status: int(rep.status), Header: map[string]string{}, Body: append([]byte(nil), rep.body...)}, nil
}

// FakeHttpNew implements `FakeHttp.new()`.
func FakeHttpNew(ctx *Ctx) *FakeHttp { return &FakeHttp{} }

// FakeHttpReply implements `reply(status, body)`; the last reply sticks.
func FakeHttpReply(ctx *Ctx, f *FakeHttp, status int64, body []byte) *FakeHttp {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, fakeReply{status: status, body: body})
	return f
}

// FakeHttpRoute implements `route(method, url, status, body)`: every
// request to method and url gets this reply. An empty method matches any.
func FakeHttpRoute(ctx *Ctx, f *FakeHttp, method string, url Url, status int64, body []byte) *FakeHttp {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes = append(f.routes, fakeRoute{method: strings.ToUpper(method), url: string(url), reply: fakeReply{status: status, body: body}})
	return f
}

// FakeHttpRequests implements `requests()`.
func FakeHttpRequests(ctx *Ctx, f *FakeHttp) []TestingRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TestingRequest(nil), f.requests...)
}

// ---- random --------------------------------------------------------------

// FakeRandom is `std.testing.FakeRandom`.
type FakeRandom struct{ Seed int64 }

// FakeRandomSeeded implements `FakeRandom.seeded(n)`.
func FakeRandomSeeded(ctx *Ctx, n int64) *FakeRandom { return &FakeRandom{Seed: n} }

// ---- llm -----------------------------------------------------------------

// LlmClient is where `llm<role>(prompt, input)` goes. out is the Go type
// of the answer; a client returns a value of that type, or a Json document
// that Llm decodes into it.
type LlmClient interface {
	Call(ctx *Ctx, role, prompt string, input any, out reflect.Type) (any, error)
}

// RefusedError is a client error for a model that declined to answer.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return "refused: " + e.Reason }

type boundaryLlm struct{}

func (boundaryLlm) Call(ctx *Ctx, role, prompt string, input any, out reflect.Type) (any, error) {
	boundary("llm")
	return nil, nil
}

// FakeLlm is `std.testing.FakeLlm`: one constant answer per role, and
// scripted `Agent` turns per role.
type FakeLlm struct {
	mu    sync.Mutex
	vals  map[string]any
	turns map[string][]TestingReply
	next  map[string]int
	calls int64
}

func (f *FakeLlm) Call(ctx *Ctx, role, prompt string, input any, out reflect.Type) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	v, ok := f.vals[role]
	if !ok {
		return nil, fmt.Errorf("FakeLlm: no answer scripted for role %s", role)
	}
	return v, nil
}

// Turn answers one Agent turn from the script of role.
func (f *FakeLlm) Turn(ctx *Ctx, role string, req AgentRequest) (AgentReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	rs := f.turns[role]
	if len(rs) == 0 {
		return AgentReply{}, fmt.Errorf("FakeLlm: no turns scripted for role %s", role)
	}
	i := f.next[role]
	if i < len(rs)-1 {
		f.next[role] = i + 1
	}
	switch r := rs[i]; r.Tag {
	case TestingReplyTag_Call:
		id := "call_" + strconv.FormatInt(f.calls, 10)
		return AgentReply{Calls: []AgentCallReq{{ID: id, Name: r.Call_0, Args: Json{Raw: []byte(r.Call_1)}}}}, nil
	case TestingReplyTag_Refuse:
		return AgentReply{Text: r.Refuse_0, Refused: true}, nil
	default:
		return AgentReply{Text: r.Say_0}, nil
	}
}

// FakeLlmTurns implements `FakeLlm.turns(role, rs)`.
func FakeLlmTurns(ctx *Ctx, role string, rs []TestingReply) *FakeLlm {
	return &FakeLlm{vals: map[string]any{}, turns: map[string][]TestingReply{role: rs}, next: map[string]int{}}
}

// FakeLlmConstant implements `FakeLlm.constant(role, v)`.
func FakeLlmConstant[T any](ctx *Ctx, role string, v T) *FakeLlm {
	return &FakeLlm{vals: map[string]any{role: v}, turns: map[string][]TestingReply{}, next: map[string]int{}}
}

// FakeLlmCalls implements `calls()`.
func FakeLlmCalls(ctx *Ctx, f *FakeLlm) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Llm implements the `llm<role>(prompt, input)` expression.
func Llm[Out any](ctx *Ctx, role, prompt string, input any) Result[Out, LlmError] {
	v, err := ctx.Llm.Call(ctx, role, prompt, input, reflect.TypeFor[Out]())
	if r, ok := err.(*RefusedError); ok {
		return Result_Err[Out](LlmError_Refused(r.Reason))
	}
	if err != nil {
		return Result_Err[Out](LlmError_Transport(err.Error()))
	}
	out, ok := v.(Out)
	if j, isJson := v.(Json); !ok && isJson {
		if err := json.Unmarshal(j.Raw, &out); err != nil {
			return Result_Err[Out](LlmError_Schema(err.Error()))
		}
		ok = true
	}
	if !ok {
		var zero Out
		return Result_Err[Out](LlmError_Schema(fmt.Sprintf("expected %T, got %T", zero, v)))
	}
	return Result_Ok[Out, LlmError](out)
}

// ---- decide --------------------------------------------------------------

// DecideClient is where `decide<role>(input)` goes.
type DecideClient interface {
	Decide(ctx *Ctx, role string, input any) (any, error)
}

type boundaryDecide struct{}

func (boundaryDecide) Decide(ctx *Ctx, role string, input any) (any, error) {
	boundary("decide")
	return nil, nil
}

// FakeDecide is `std.testing.FakeDecide`.
type FakeDecide struct {
	mu        sync.Mutex
	always    any
	hasAlways bool
	script    []any
	i         int
}

func (f *FakeDecide) Decide(ctx *Ctx, role string, input any) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hasAlways {
		return f.always, nil
	}
	if f.i >= len(f.script) {
		return nil, fmt.Errorf("FakeDecide: script exhausted after %d answer(s)", len(f.script))
	}
	v := f.script[f.i]
	f.i++
	return v, nil
}

// FakeDecideAlways implements `FakeDecide.always(v)`.
func FakeDecideAlways[T any](ctx *Ctx, v T) *FakeDecide {
	return &FakeDecide{always: v, hasAlways: true}
}

// FakeDecideScript implements `FakeDecide.script(vs)`.
func FakeDecideScript[T any](ctx *Ctx, vs []T) *FakeDecide {
	f := &FakeDecide{}
	for _, v := range vs {
		f.script = append(f.script, v)
	}
	return f
}

// Decide implements the `decide<role>(input)` expression.
func Decide[Out any](ctx *Ctx, role string, input any) Out {
	var zero Out
	v, err := ctx.Decide.Decide(ctx, role, input)
	if err != nil {
		ctx.failf("%s", err.Error())
		return zero
	}
	out, ok := v.(Out)
	if !ok {
		ctx.failf("decide<%s>: expected %T, got %T", role, zero, v)
		return zero
	}
	return out
}

// ---- approve -------------------------------------------------------------

// ApproveClient answers `approve::<D>(review)` outside a journal.
type ApproveClient interface {
	// Ask answers with a value of type out, a Result[D, Rejected].
	Ask(ctx *Ctx, review Review, out reflect.Type) any
}

type boundaryApprove struct{}

func (boundaryApprove) Ask(ctx *Ctx, review Review, out reflect.Type) any {
	boundary("approve")
	return nil
}

// FakeApprove is `std.testing.FakeApprove`: scripted answers in order.
type FakeApprove struct {
	mu      sync.Mutex
	answers []any
	i       int
	asked   []Review
}

func (f *FakeApprove) Ask(ctx *Ctx, review Review, out reflect.Type) any {
	f.mu.Lock()
	f.asked = append(f.asked, review)
	if f.i >= len(f.answers) {
		n := len(f.answers)
		f.mu.Unlock()
		ctx.failf("approve script exhausted after %d answer(s)", n)
		return nil
	}
	v := f.answers[f.i]
	f.i++
	f.mu.Unlock()
	return v
}

// FakeApproveScript implements `FakeApprove.script(answers)`.
func FakeApproveScript[T any](ctx *Ctx, answers []Result[T, Rejected]) *FakeApprove {
	f := &FakeApprove{}
	for _, a := range answers {
		f.answers = append(f.answers, a)
	}
	return f
}

// FakeApproveAsked implements `asked()`.
func FakeApproveAsked(ctx *Ctx, f *FakeApprove) []Review {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Review(nil), f.asked...)
}

// ---- MemRepo / MemCache --------------------------------------------------

// MemRepo is `std.testing.MemRepo<T, K>`.
type MemRepo[T, K any] struct {
	mu   sync.Mutex
	keys []K
	vals []T
}

func (r *MemRepo[T, K]) index(k K) int {
	for i, x := range r.keys {
		if Equal(x, k) {
			return i
		}
	}
	return -1
}

func MemRepoNew[T, K any](ctx *Ctx) *MemRepo[T, K] { return &MemRepo[T, K]{} }

func MemRepoGet[T, K any](ctx *Ctx, r *MemRepo[T, K], k K) Result[Option[T], RepoError] {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i := r.index(k); i >= 0 {
		return Result_Ok[Option[T], RepoError](Option_Some(r.vals[i]))
	}
	return Result_Ok[Option[T], RepoError](Option_None[T]())
}

func MemRepoPut[T, K any](ctx *Ctx, r *MemRepo[T, K], k K, v T) Result[Unit, RepoError] {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i := r.index(k); i >= 0 {
		r.vals[i] = v
	} else {
		r.keys = append(r.keys, k)
		r.vals = append(r.vals, v)
	}
	return Result_Ok[Unit, RepoError](U)
}

func MemRepoDelete[T, K any](ctx *Ctx, r *MemRepo[T, K], k K) Result[Unit, RepoError] {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i := r.index(k); i >= 0 {
		r.keys = append(r.keys[:i], r.keys[i+1:]...)
		r.vals = append(r.vals[:i], r.vals[i+1:]...)
	}
	return Result_Ok[Unit, RepoError](U)
}

func MemRepoQuery[T, K any](ctx *Ctx, r *MemRepo[T, K], q Query[T]) Result[[]T, RepoError] {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Result_Ok[[]T, RepoError](append([]T(nil), r.vals...))
}

// MemCache is `std.testing.MemCache<K, V>`; entries expire against the
// ctx clock, so a FakeClock advance expires them.
type MemCache[K, V any] struct {
	mu   sync.Mutex
	ttl  time.Duration
	keys []K
	vals []V
	exp  []Instant
}

func (c *MemCache[K, V]) index(k K) int {
	for i, x := range c.keys {
		if Equal(x, k) {
			return i
		}
	}
	return -1
}

func (c *MemCache[K, V]) remove(i int) {
	c.keys = append(c.keys[:i], c.keys[i+1:]...)
	c.vals = append(c.vals[:i], c.vals[i+1:]...)
	c.exp = append(c.exp[:i], c.exp[i+1:]...)
}

func MemCacheNew[K, V any](ctx *Ctx, ttl time.Duration) *MemCache[K, V] {
	return &MemCache[K, V]{ttl: ttl}
}

func MemCacheGet[K, V any](ctx *Ctx, c *MemCache[K, V], k K) Result[Option[V], CacheError] {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := c.index(k)
	if i < 0 {
		return Result_Ok[Option[V], CacheError](Option_None[V]())
	}
	if ctx.Clock.Now().Nanos >= c.exp[i].Nanos {
		c.remove(i)
		return Result_Ok[Option[V], CacheError](Option_None[V]())
	}
	return Result_Ok[Option[V], CacheError](Option_Some(c.vals[i]))
}

func MemCacheSet[K, V any](ctx *Ctx, c *MemCache[K, V], k K, v V, ttl Option[time.Duration]) Result[Unit, CacheError] {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.ttl
	if ttl.Tag == OptionTag_Some {
		d = ttl.Some_0
	}
	exp := Instant{Nanos: ctx.Clock.Now().Nanos + int64(d)}
	if i := c.index(k); i >= 0 {
		c.vals[i] = v
		c.exp[i] = exp
	} else {
		c.keys = append(c.keys, k)
		c.vals = append(c.vals, v)
		c.exp = append(c.exp, exp)
	}
	return Result_Ok[Unit, CacheError](U)
}

func MemCacheInvalidate[K, V any](ctx *Ctx, c *MemCache[K, V], k K) Result[Unit, CacheError] {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := c.index(k); i >= 0 {
		c.remove(i)
	}
	return Result_Ok[Unit, CacheError](U)
}

// ---- files ---------------------------------------------------------------

// TmpPath implements `std.testing.tmp_path(name)`.
func TmpPath(ctx *Ctx, name string) Path {
	dir := os.TempDir()
	if ctx.scn != nil {
		dir = ctx.scn.TempDir()
	}
	return Path(filepath.Join(dir, name))
}

// WriteFile implements `std.testing.write_file(p, b)`.
func WriteFile(ctx *Ctx, p Path, b []byte) Result[Unit, IoError] {
	if err := os.WriteFile(string(p), b, 0o644); err != nil {
		return Result_Err[Unit](ioError(err))
	}
	return Result_Ok[Unit, IoError](U)
}

func ioError(err error) IoError {
	switch {
	case os.IsNotExist(err):
		return IoError_NotFound(err.Error())
	case os.IsPermission(err):
		return IoError_Permission(err.Error())
	default:
		return IoError_Other(err.Error())
	}
}

// ---- Extern --------------------------------------------------------------

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// Extern calls a Go function declared through `extern fn … = go "pkg".F`
// when the package is not rt: arguments convert to the Go parameter types,
// a single result converts to R, and a `(T, error)` pair becomes the
// `Result<T, E>` the declaration names, with E built from the error.
func Extern[R any](ctx *Ctx, f any, args ...any) R {
	fv := reflect.ValueOf(f)
	ft := fv.Type()
	if ft.Kind() != reflect.Func {
		panic(fmt.Sprintf("rt.Extern: %T is not a function", f))
	}
	in := make([]reflect.Value, len(args))
	for i, a := range args {
		var pt reflect.Type
		if ft.IsVariadic() && i >= ft.NumIn()-1 {
			pt = ft.In(ft.NumIn() - 1).Elem()
		} else if i < ft.NumIn() {
			pt = ft.In(i)
		}
		av := reflect.ValueOf(a)
		if pt != nil && av.IsValid() && av.Type() != pt && av.Type().ConvertibleTo(pt) {
			av = av.Convert(pt)
		}
		if !av.IsValid() && pt != nil {
			av = reflect.Zero(pt)
		}
		in[i] = av
	}
	out := fv.Call(in)
	var zero R
	rt := reflect.TypeOf(zero)
	switch len(out) {
	case 0:
		return zero
	case 1:
		return convertResult(out[0], rt).Interface().(R)
	}
	// (T, error) → Result<T, E>
	if rt == nil || !isEnum(rt) || variantNames(reflect.New(rt).Elem())[0] != "Ok" {
		return convertResult(out[0], rt).Interface().(R)
	}
	rv := reflect.New(rt).Elem()
	errV := out[len(out)-1]
	if errV.Type().Implements(errorType) && !errV.IsNil() {
		err := errV.Interface().(error)
		rv.Field(0).SetUint(uint64(ResultTag_Err))
		ef := rv.FieldByName("Err_0")
		ef.Set(reflect.ValueOf(externError(err, ef.Type())))
		return rv.Interface().(R)
	}
	rv.Field(0).SetUint(uint64(ResultTag_Ok))
	of := rv.FieldByName("Ok_0")
	of.Set(convertResult(out[0], of.Type()))
	return rv.Interface().(R)
}

func convertResult(v reflect.Value, t reflect.Type) reflect.Value {
	if t == nil {
		return v
	}
	if v.Type() == t {
		return v
	}
	if v.Type().ConvertibleTo(t) {
		return v.Convert(t)
	}
	if t == reflect.TypeOf(Unit{}) {
		return reflect.ValueOf(U)
	}
	panic(fmt.Sprintf("rt.Extern: cannot convert %v to %v", v.Type(), t))
}

// externError builds the error value of type et from a Go error.
func externError(err error, et reflect.Type) any {
	switch et {
	case reflect.TypeOf(IoError{}):
		return ioError(err)
	case reflect.TypeOf(NetError{}):
		if isTimeout(err) {
			return NetError_Timeout()
		}
		return NetError_Other(err.Error())
	case reflect.TypeOf(GoError("")):
		return GoError(err.Error())
	}
	if et.Kind() == reflect.String {
		return reflect.ValueOf(err.Error()).Convert(et).Interface()
	}
	if isEnum(et) {
		v := reflect.New(et).Elem()
		names := variantNames(v)
		for i, n := range names {
			idx, _, _ := payloadFields(et, n)
			if len(idx) == 1 && v.Field(idx[0]).Kind() == reflect.String {
				v.Field(0).SetUint(uint64(i))
				v.Field(idx[0]).SetString(err.Error())
				return v.Interface()
			}
		}
	}
	panic(fmt.Sprintf("rt.Extern: cannot build %v from error %v", et, err))
}

// ---- extern boundary helpers (spec 11 rule 9) ----------------------------

// IoErrorOf maps a Go error to the prelude IoError.
func IoErrorOf(err error) IoError { return ioError(err) }

// GoErrorOf maps a Go error to the prelude GoError.
func GoErrorOf(err error) GoError { return GoError(err.Error()) }

// MapToGo converts a Map to a Go map for an extern argument.
func MapToGo[K comparable, V any](m Map[K, V]) map[K]V {
	out := make(map[K]V, m.n)
	m.root.walk(func(e *hent[K, V]) { out[e.k] = e.v })
	return out
}

// MapFromGo converts an extern's Go map result to a Map, ordering entries
// by their printed key so the result is deterministic.
func MapFromGo[K comparable, V any](g map[K]V) Map[K, V] {
	keys := make([]K, 0, len(g))
	for k := range g {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return Show(keys[i]) < Show(keys[j]) })
	m := Map[K, V]{}
	for _, k := range keys {
		m = m.insert(k, g[k])
	}
	return m
}
