package rt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ---- specs ---------------------------------------------------------------

// ServiceSpec is what the emitter builds for `impl Service for X`.
type ServiceSpec[S, Req, Resp, E any] struct {
	Name   string
	Init   func(*Ctx) (S, error)
	Handle func(*Ctx, S, Req) Result[Resp, E]
	Stop   func(*Ctx, S) Unit
	Health func(*Ctx, S) Health
}

// WorkerSpec is what the emitter builds for `impl Worker for X`.
type WorkerSpec[S, Msg, E any] struct {
	Name    string
	Retry   Retry
	Init    func(*Ctx) (S, error)
	Perform func(*Ctx, S, Msg) Result[Unit, E]
	Stop    func(*Ctx, S) Unit
	Health  func(*Ctx, S) Health
}

// CronSpec is what the emitter builds for `impl Cron for X`.
type CronSpec[S, E any] struct {
	Name     string
	Schedule Schedule
	Init     func(*Ctx) (S, error)
	Run      func(*Ctx, S) Result[Unit, E]
	Stop     func(*Ctx, S) Unit
	Health   func(*Ctx, S) Health
}

// WorkflowSpec is what the emitter builds for `impl Workflow for X`.
type WorkflowSpec[S, In, Out, Sig, E any] struct {
	Name     string
	Version  int64
	Sites    []string
	Init     func(*Ctx) (S, error)
	Run      func(*Ctx, S, In) Result[Out, WorkflowError[E]]
	OnSignal func(*Ctx, S, Sig) Unit
}

// RepositorySpec is what the emitter builds for `impl Repository for X`.
type RepositorySpec[S, T, K any] struct {
	Name   string
	Init   func(*Ctx) (S, error)
	Get    func(*Ctx, S, K) Result[Option[T], RepoError]
	Put    func(*Ctx, S, K, T) Result[Unit, RepoError]
	Delete func(*Ctx, S, K) Result[Unit, RepoError]
	Query  func(*Ctx, S, Query[T]) Result[[]T, RepoError]
	Stop   func(*Ctx, S) Unit
	Health func(*Ctx, S) Health
}

// CacheSpec is what the emitter builds for `impl Cache for X`.
type CacheSpec[S, K, V any] struct {
	Name       string
	Init       func(*Ctx) (S, error)
	Get        func(*Ctx, S, K) Result[Option[V], CacheError]
	Set        func(*Ctx, S, K, V, Option[time.Duration]) Result[Unit, CacheError]
	Invalidate func(*Ctx, S, K) Result[Unit, CacheError]
	Stop       func(*Ctx, S) Unit
	Health     func(*Ctx, S) Health
}

// RunOpts is one `run <Impl> { … }` line after the overlay.
type RunOpts struct {
	Name            string
	Restart         string
	MaxRestarts     int
	Period          time.Duration
	ShutdownTimeout time.Duration
	Topic           string
	Port            int
	Ttl             time.Duration
	Idle            time.Duration
}

// Runner is one supervised child.
type Runner interface {
	Name() string
	Opts() RunOpts
	Routes(ctx *Ctx, mux *http.ServeMux)
	Run(ctx *Ctx) error
	Stop(ctx *Ctx)
}

// protect runs f, converting an ordinary panic into an error. Boundary,
// scenario, suspension and replay panics always propagate, and inside a
// scenario every panic does.
func protect(ctx *Ctx, name string, f func()) (err error) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		switch p.(type) {
		case *BoundaryError, *ScenarioError, *suspend, *replayMismatch:
			panic(p)
		}
		if ctx.scn != nil {
			panic(p)
		}
		if e, ok := p.(error); ok {
			err = e
		} else {
			err = fmt.Errorf("%v", p)
		}
		ctx.Logf("jip: %s: %v", name, err)
	}()
	f()
	return nil
}

func healthRoute[S any](ctx *Ctx, mux *http.ServeMux, name string, init func(*Ctx) (S, error), health func(*Ctx, S) Health) {
	mux.HandleFunc("GET /"+name+"/health", func(w http.ResponseWriter, r *http.Request) {
		var h Health
		err := protect(ctx, name, func() { h = health(ctx.child(), Instance(ctx, name, init)) })
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if !h.Ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(h)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- Service -------------------------------------------------------------

type serviceRunner[S, Req, Resp, E any] struct {
	spec *ServiceSpec[S, Req, Resp, E]
	opts RunOpts
}

// ServiceRunner wraps a Service impl for Serve.
func ServiceRunner[S, Req, Resp, E any](spec *ServiceSpec[S, Req, Resp, E], opts RunOpts) Runner {
	return &serviceRunner[S, Req, Resp, E]{spec: spec, opts: opts}
}

func (r *serviceRunner[S, Req, Resp, E]) Name() string  { return r.spec.Name }
func (r *serviceRunner[S, Req, Resp, E]) Opts() RunOpts { return r.opts }

func (r *serviceRunner[S, Req, Resp, E]) Routes(ctx *Ctx, mux *http.ServeMux) {
	name := r.spec.Name
	mux.HandleFunc("POST /"+name, func(w http.ResponseWriter, req *http.Request) {
		var in Req
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		var res Result[Resp, E]
		err := protect(ctx, name, func() {
			c := ctx.child()
			c.Go = req.Context()
			res = r.spec.Handle(c, Instance(ctx, name, r.spec.Init), in)
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if res.Tag == ResultTag_Err {
			writeJSON(w, http.StatusInternalServerError, res.Err_0)
			return
		}
		writeJSON(w, http.StatusOK, res.Ok_0)
	})
	healthRoute(ctx, mux, name, r.spec.Init, r.spec.Health)
}

func (r *serviceRunner[S, Req, Resp, E]) Run(ctx *Ctx) error {
	return protect(ctx, r.spec.Name, func() {
		Instance(ctx, r.spec.Name, r.spec.Init)
		<-ctx.Go.Done()
	})
}

func (r *serviceRunner[S, Req, Resp, E]) Stop(ctx *Ctx) {
	_ = protect(ctx, r.spec.Name, func() { r.spec.Stop(ctx.child(), Instance(ctx, r.spec.Name, r.spec.Init)) })
}

// ---- Worker --------------------------------------------------------------

type workerRunner[S, Msg, E any] struct {
	spec *WorkerSpec[S, Msg, E]
	opts RunOpts
}

// WorkerRunner wraps a Worker impl for Serve.
func WorkerRunner[S, Msg, E any](spec *WorkerSpec[S, Msg, E], opts RunOpts) Runner {
	return &workerRunner[S, Msg, E]{spec: spec, opts: opts}
}

func (r *workerRunner[S, Msg, E]) Name() string  { return r.spec.Name }
func (r *workerRunner[S, Msg, E]) Opts() RunOpts { return r.opts }
func (r *workerRunner[S, Msg, E]) Routes(ctx *Ctx, mux *http.ServeMux) {
	healthRoute(ctx, mux, r.spec.Name, r.spec.Init, r.spec.Health)
}

func (r *workerRunner[S, Msg, E]) Run(ctx *Ctx) error {
	return protect(ctx, r.spec.Name, func() {
		Instance(ctx, r.spec.Name, r.spec.Init)
		q := ctx.sh.queue(r.spec.Name)
		for {
			select {
			case <-ctx.Go.Done():
				return
			case m := <-q:
				deliver(ctx, r.spec, m.(Msg))
			}
		}
	})
}

func (r *workerRunner[S, Msg, E]) Stop(ctx *Ctx) {
	_ = protect(ctx, r.spec.Name, func() { r.spec.Stop(ctx.child(), Instance(ctx, r.spec.Name, r.spec.Init)) })
}

func backoffDelay(b Backoff, attempt int) time.Duration {
	switch b.Tag {
	case BackoffTag_Fixed:
		return b.Fixed_0
	case BackoffTag_Exp:
		if attempt < 1 {
			attempt = 1
		}
		return b.Exp_0 << (attempt - 1)
	}
	return 0
}

// deliver performs one message with the impl's retry policy; exhaustion
// dead-letters it.
func deliver[S, Msg, E any](ctx *Ctx, spec *WorkerSpec[S, Msg, E], m Msg) {
	attempts := int(spec.Retry.Max) + 1
	if attempts < 1 {
		attempts = 1
	}
	var last string
	for i := 1; i <= attempts; i++ {
		if i > 1 {
			ctx.Clock.Sleep(ctx, backoffDelay(spec.Retry.Backoff, i-1))
		}
		var res Result[Unit, E]
		err := protect(ctx, spec.Name, func() {
			res = spec.Perform(ctx.child(), Instance(ctx, spec.Name, spec.Init), m)
		})
		if err != nil {
			last = err.Error()
			continue
		}
		if res.Tag == ResultTag_Ok {
			return
		}
		last = Show(res.Err_0)
	}
	ctx.sh.mu.Lock()
	ctx.sh.deadLetters = append(ctx.sh.deadLetters, DeadLetter{Worker: spec.Name, Msg: m, Err: last})
	ctx.sh.mu.Unlock()
	ctx.Logf("jip: %s: dead letter after %d attempt(s): %s", spec.Name, attempts, last)
}

// Enqueue implements `<Worker>.enqueue(msg)`: queued under Serve,
// delivered synchronously otherwise (scenarios).
func Enqueue[S, Msg, E any](ctx *Ctx, spec *WorkerSpec[S, Msg, E], m Msg) Result[Unit, QueueError] {
	ctx.sh.mu.Lock()
	serving := ctx.sh.serving
	ctx.sh.mu.Unlock()
	if serving {
		select {
		case ctx.sh.queue(spec.Name) <- m:
			return Result_Ok[Unit, QueueError](U)
		default:
			return Result_Err[Unit](QueueError_Full())
		}
	}
	deliver(ctx, spec, m)
	return Result_Ok[Unit, QueueError](U)
}

// ---- topics --------------------------------------------------------------

var (
	topicMu   sync.Mutex
	topicSubs = map[string][]func(*Ctx, any) Result[Unit, QueueError]{}
)

// Subscribe registers a Worker on a topic; the emitter calls it from a Go
// init() for every `run X { topic: name }`.
func Subscribe(topic string, f func(*Ctx, any) Result[Unit, QueueError]) {
	topicMu.Lock()
	defer topicMu.Unlock()
	topicSubs[topic] = append(topicSubs[topic], f)
}

// TopicPublish implements `topics.<name>.publish(m)`.
func TopicPublish[T any](ctx *Ctx, t Topic[T], m T) Result[Unit, TopicError] {
	topicMu.Lock()
	subs := append([]func(*Ctx, any) Result[Unit, QueueError](nil), topicSubs[t.Name]...)
	topicMu.Unlock()
	for _, s := range subs {
		if r := s(ctx, m); r.Tag == ResultTag_Err {
			return Result_Err[Unit](TopicError_Unavailable(Show(r.Err_0)))
		}
	}
	return Result_Ok[Unit, TopicError](U)
}

// ---- Cron ----------------------------------------------------------------

type cronRunner[S, E any] struct {
	spec *CronSpec[S, E]
	opts RunOpts
}

// CronRunner wraps a Cron impl for Serve.
func CronRunner[S, E any](spec *CronSpec[S, E], opts RunOpts) Runner {
	return &cronRunner[S, E]{spec: spec, opts: opts}
}

func (r *cronRunner[S, E]) Name() string  { return r.spec.Name }
func (r *cronRunner[S, E]) Opts() RunOpts { return r.opts }
func (r *cronRunner[S, E]) Routes(ctx *Ctx, mux *http.ServeMux) {
	healthRoute(ctx, mux, r.spec.Name, r.spec.Init, r.spec.Health)
}

func (r *cronRunner[S, E]) Run(ctx *Ctx) error {
	return protect(ctx, r.spec.Name, func() {
		s := Instance(ctx, r.spec.Name, r.spec.Init)
		var cs *cronSpec
		if r.spec.Schedule.Tag == ScheduleTag_Cron {
			var err error
			cs, err = parseCron(r.spec.Schedule.Cron_0)
			if err != nil {
				panic(fmt.Errorf("%s: %v", r.spec.Name, err))
			}
		}
		for {
			if cs == nil {
				ctx.Clock.Sleep(ctx, r.spec.Schedule.Every_0)
			} else {
				now := ctx.Clock.Now().Time()
				ctx.Clock.Sleep(ctx, cs.next(now).Sub(now))
			}
			if ctx.Go.Err() != nil {
				return
			}
			if res := r.spec.Run(ctx.child(), s); res.Tag == ResultTag_Err {
				ctx.Logf("jip: %s: run failed: %s", r.spec.Name, Show(res.Err_0))
			}
		}
	})
}

func (r *cronRunner[S, E]) Stop(ctx *Ctx) {
	_ = protect(ctx, r.spec.Name, func() { r.spec.Stop(ctx.child(), Instance(ctx, r.spec.Name, r.spec.Init)) })
}

// Tick implements the scenario builtin `tick(Impl)`: one run, now.
func Tick[S, E any](ctx *Ctx, spec *CronSpec[S, E]) Result[Unit, E] {
	return spec.Run(ctx.child(), Instance(ctx, spec.Name, spec.Init))
}

type cronField struct{ allowed [60]bool }

type cronSpec struct{ min, hour, dom, mon, dow cronField }

func parseCronField(s string, lo, hi int) (cronField, error) {
	var f cronField
	for _, part := range strings.Split(s, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return f, fmt.Errorf("bad step %q", part)
			}
			step = n
			part = part[:i]
		}
		a, b := lo, hi
		if part != "*" {
			if i := strings.Index(part, "-"); i >= 0 {
				x, err1 := strconv.Atoi(part[:i])
				y, err2 := strconv.Atoi(part[i+1:])
				if err1 != nil || err2 != nil {
					return f, fmt.Errorf("bad range %q", part)
				}
				a, b = x, y
			} else {
				x, err := strconv.Atoi(part)
				if err != nil {
					return f, fmt.Errorf("bad field %q", part)
				}
				a, b = x, x
				if step > 1 {
					b = hi
				}
			}
		}
		if a < lo || b > hi || a > b {
			return f, fmt.Errorf("field %q out of range %d-%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			f.allowed[v] = true
		}
	}
	return f, nil
}

func parseCron(expr string) (*cronSpec, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron %q: expected 5 fields", expr)
	}
	var cs cronSpec
	var err error
	if cs.min, err = parseCronField(parts[0], 0, 59); err != nil {
		return nil, err
	}
	if cs.hour, err = parseCronField(parts[1], 0, 23); err != nil {
		return nil, err
	}
	if cs.dom, err = parseCronField(parts[2], 1, 31); err != nil {
		return nil, err
	}
	if cs.mon, err = parseCronField(parts[3], 1, 12); err != nil {
		return nil, err
	}
	if cs.dow, err = parseCronField(parts[4], 0, 6); err != nil {
		return nil, err
	}
	return &cs, nil
}

func (cs *cronSpec) matches(t time.Time) bool {
	return cs.min.allowed[t.Minute()] && cs.hour.allowed[t.Hour()] &&
		cs.dom.allowed[t.Day()] && cs.mon.allowed[int(t.Month())] &&
		cs.dow.allowed[int(t.Weekday())]
}

// next returns the first matching minute strictly after t.
func (cs *cronSpec) next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if cs.matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return t
}

// ---- Repository / Cache --------------------------------------------------

type repoRunner[S, T, K any] struct {
	spec *RepositorySpec[S, T, K]
	opts RunOpts
}

// RepositoryRunner wraps a Repository impl for Serve.
func RepositoryRunner[S, T, K any](spec *RepositorySpec[S, T, K], opts RunOpts) Runner {
	return &repoRunner[S, T, K]{spec: spec, opts: opts}
}

func (r *repoRunner[S, T, K]) Name() string  { return r.spec.Name }
func (r *repoRunner[S, T, K]) Opts() RunOpts { return r.opts }
func (r *repoRunner[S, T, K]) Routes(ctx *Ctx, mux *http.ServeMux) {
	healthRoute(ctx, mux, r.spec.Name, r.spec.Init, r.spec.Health)
}
func (r *repoRunner[S, T, K]) Run(ctx *Ctx) error {
	return protect(ctx, r.spec.Name, func() {
		Instance(ctx, r.spec.Name, r.spec.Init)
		<-ctx.Go.Done()
	})
}
func (r *repoRunner[S, T, K]) Stop(ctx *Ctx) {
	_ = protect(ctx, r.spec.Name, func() { r.spec.Stop(ctx.child(), Instance(ctx, r.spec.Name, r.spec.Init)) })
}

type cacheRunner[S, K, V any] struct {
	spec *CacheSpec[S, K, V]
	opts RunOpts
}

// CacheRunner wraps a Cache impl for Serve.
func CacheRunner[S, K, V any](spec *CacheSpec[S, K, V], opts RunOpts) Runner {
	return &cacheRunner[S, K, V]{spec: spec, opts: opts}
}

func (r *cacheRunner[S, K, V]) Name() string  { return r.spec.Name }
func (r *cacheRunner[S, K, V]) Opts() RunOpts { return r.opts }
func (r *cacheRunner[S, K, V]) Routes(ctx *Ctx, mux *http.ServeMux) {
	healthRoute(ctx, mux, r.spec.Name, r.spec.Init, r.spec.Health)
}
func (r *cacheRunner[S, K, V]) Run(ctx *Ctx) error {
	return protect(ctx, r.spec.Name, func() {
		Instance(ctx, r.spec.Name, r.spec.Init)
		<-ctx.Go.Done()
	})
}
func (r *cacheRunner[S, K, V]) Stop(ctx *Ctx) {
	_ = protect(ctx, r.spec.Name, func() { r.spec.Stop(ctx.child(), Instance(ctx, r.spec.Name, r.spec.Init)) })
}

// ---- Workflow ------------------------------------------------------------

type workflowRunner[S, In, Out, Sig, E any] struct {
	spec *WorkflowSpec[S, In, Out, Sig, E]
	opts RunOpts
}

// WorkflowRunner wraps a Workflow impl for Serve: runs start over HTTP.
func WorkflowRunner[S, In, Out, Sig, E any](spec *WorkflowSpec[S, In, Out, Sig, E], opts RunOpts) Runner {
	return &workflowRunner[S, In, Out, Sig, E]{spec: spec, opts: opts}
}

func (r *workflowRunner[S, In, Out, Sig, E]) Name() string  { return r.spec.Name }
func (r *workflowRunner[S, In, Out, Sig, E]) Opts() RunOpts { return r.opts }

func (r *workflowRunner[S, In, Out, Sig, E]) Routes(ctx *Ctx, mux *http.ServeMux) {
	name := r.spec.Name
	mux.HandleFunc("POST /"+name+"/start", func(w http.ResponseWriter, req *http.Request) {
		var in In
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		id := StartWorkflow(ctx, r.spec, in)
		writeJSON(w, http.StatusAccepted, map[string]any{"id": id})
	})
	mux.HandleFunc("POST /"+name+"/signal", func(w http.ResponseWriter, req *http.Request) {
		var sig Sig
		if err := json.NewDecoder(req.Body).Decode(&sig); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := protect(ctx, name, func() { SendSignal(ctx, r.spec, sig) }); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /"+name+"/result/{id}", func(w http.ResponseWriter, req *http.Request) {
		v, ok := WorkflowResult(ctx, RunId(req.PathValue("id")))
		if !ok {
			http.Error(w, "running", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	mux.HandleFunc("GET /"+name+"/health", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, HealthUp(ctx))
	})
}

func (r *workflowRunner[S, In, Out, Sig, E]) Run(ctx *Ctx) error {
	return protect(ctx, r.spec.Name, func() {
		Instance(ctx, r.spec.Name, r.spec.Init)
		<-ctx.Go.Done()
	})
}

func (r *workflowRunner[S, In, Out, Sig, E]) Stop(ctx *Ctx) {}

// ---- journal -------------------------------------------------------------

// JournalEntry is one recorded step or pending approval.
type JournalEntry struct {
	Seq    int             `json:"seq"`
	Key    string          `json:"key"`
	Kind   string          `json:"kind"`
	Nested bool            `json:"nested,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
}

type journalHeader struct {
	Workflow string   `json:"workflow"`
	Version  int64    `json:"version"`
	Sites    []string `json:"sites"`
}

// Journal is one workflow run's history: a JSONL file of step results,
// replayed in order on every re-entry of `run`.
type Journal struct {
	Workflow string
	Version  int64
	Sites    []string
	Entries  []JournalEntry

	cursor   int
	counters map[string]int
	depth    int
	file     string
	mu       sync.Mutex
}

// NewJournal starts an empty history.
func NewJournal(name string, version int64, sites []string) *Journal {
	return &Journal{Workflow: name, Version: version, Sites: sites, counters: map[string]int{}}
}

// LoadJournal reads a JSONL history written by StartWorkflow.
func LoadJournal(path string) (*Journal, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	j := &Journal{counters: map[string]int{}, file: path}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if i == 0 {
			var h journalHeader
			if err := json.Unmarshal([]byte(line), &h); err != nil {
				return nil, fmt.Errorf("%s: header: %v", path, err)
			}
			j.Workflow, j.Version, j.Sites = h.Workflow, h.Version, h.Sites
			continue
		}
		var e JournalEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("%s: line %d: %v", path, i+1, err)
		}
		j.Entries = append(j.Entries, e)
	}
	return j, nil
}

func (j *Journal) check(version int64, sites []string) *ReplayError {
	if j.Version != version {
		return &ReplayError{Expected: j.Version, Found: version}
	}
	if len(j.Sites) != len(sites) {
		return &ReplayError{Expected: int64(len(j.Sites)), Found: int64(len(sites))}
	}
	for i := range sites {
		if j.Sites[i] != sites[i] {
			return &ReplayError{Expected: int64(len(j.Sites)), Found: int64(len(sites))}
		}
	}
	return nil
}

func (j *Journal) reset() {
	j.cursor = 0
	j.counters = map[string]int{}
	j.depth = 0
}

func (j *Journal) key(site string) string {
	j.counters[site]++
	return site + "#" + strconv.Itoa(j.counters[site])
}

func (j *Journal) append(e JournalEntry) {
	e.Seq = len(j.Entries) + 1
	j.Entries = append(j.Entries, e)
	if j.file == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := os.OpenFile(j.file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if e.Seq == 1 {
		h, _ := json.Marshal(journalHeader{Workflow: j.Workflow, Version: j.Version, Sites: j.Sites})
		f.Write(append(h, '\n'))
	}
	b, _ := json.Marshal(e)
	f.Write(append(b, '\n'))
}

func (j *Journal) resolve(key string, ans any) {
	for i := range j.Entries {
		if j.Entries[i].Key == key && j.Entries[i].Kind == "pending" {
			v, _ := json.Marshal(ans)
			j.Entries[i].Kind = "step"
			j.Entries[i].Value = v
			break
		}
	}
	if j.file == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	var sb strings.Builder
	h, _ := json.Marshal(journalHeader{Workflow: j.Workflow, Version: j.Version, Sites: j.Sites})
	sb.Write(h)
	sb.WriteByte('\n')
	for _, e := range j.Entries {
		b, _ := json.Marshal(e)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	_ = os.WriteFile(j.file, []byte(sb.String()), 0o644)
}

type suspend struct {
	key    string
	review Review
	out    reflect.Type
}

type replayMismatch struct{ expected, found int64 }

// Step journals one effectful call site inside Workflow.run: on replay the
// recorded value is returned without running f.
func Step[T any](ctx *Ctx, site string, f func() T) T {
	j := ctx.Journal
	if j == nil || j.depth > 0 {
		return f()
	}
	key := j.key(site)
	for j.cursor < len(j.Entries) {
		e := j.Entries[j.cursor]
		if e.Kind == "step" && e.Key == key {
			j.cursor++
			var v T
			if err := json.Unmarshal(e.Value, &v); err != nil {
				panic(&replayMismatch{expected: int64(e.Seq), found: 0})
			}
			return v
		}
		if e.Nested {
			break
		}
		panic(&replayMismatch{expected: int64(e.Seq), found: 0})
	}
	j.depth++
	v := func() T {
		defer func() { j.depth-- }()
		return f()
	}()
	if j.cursor < len(j.Entries) {
		e := j.Entries[j.cursor]
		if e.Kind == "step" && e.Key == key {
			j.cursor++
			var r T
			if err := json.Unmarshal(e.Value, &r); err == nil {
				return r
			}
		}
		panic(&replayMismatch{expected: int64(e.Seq), found: 0})
	}
	b, _ := json.Marshal(v)
	j.append(JournalEntry{Key: key, Kind: "step", Value: b})
	j.cursor = len(j.Entries)
	return v
}

// Approve implements `approve::<D>(review)`: outside a journal the
// ApproveClient answers; inside, the run suspends until an answer is
// recorded and replays from the top.
func Approve[D any](ctx *Ctx, site string, review Review) Result[D, Rejected] {
	j := ctx.Journal
	out := reflect.TypeOf(Result[D, Rejected]{})
	if j == nil {
		v := ctx.Approve.Ask(ctx, review, out)
		r, ok := v.(Result[D, Rejected])
		if !ok {
			ctx.failf("approve: expected %T, got %T", r, v)
		}
		return r
	}
	key := j.key(site)
	nested := j.depth > 0
	if j.cursor < len(j.Entries) {
		e := j.Entries[j.cursor]
		if e.Key != key {
			panic(&replayMismatch{expected: int64(e.Seq), found: 0})
		}
		if e.Kind == "pending" {
			panic(&suspend{key: key, review: review, out: out})
		}
		j.cursor++
		var r Result[D, Rejected]
		if err := json.Unmarshal(e.Value, &r); err != nil {
			panic(&replayMismatch{expected: int64(e.Seq), found: 0})
		}
		return r
	}
	b, _ := json.Marshal(review)
	j.append(JournalEntry{Key: key, Kind: "pending", Nested: nested, Value: b})
	j.cursor = len(j.Entries)
	panic(&suspend{key: key, review: review, out: out})
}

// RunWorkflow implements the scenario builtin `run_workflow(Impl, input)`:
// a fresh journal, approvals answered by the installed FakeApprove.
func RunWorkflow[S, In, Out, Sig, E any](ctx *Ctx, spec *WorkflowSpec[S, In, Out, Sig, E], in In) Result[Out, WorkflowError[E]] {
	return runJournaled(ctx, spec, in, NewJournal(spec.Name, spec.Version, spec.Sites))
}

// RecordWorkflow implements the scenario builtin `record_workflow(Impl,
// input)`: run_workflow that also hands back the journal it wrote.
func RecordWorkflow[S, In, Out, Sig, E any](ctx *Ctx, spec *WorkflowSpec[S, In, Out, Sig, E], in In) T2[Result[Out, WorkflowError[E]], *Journal] {
	j := NewJournal(spec.Name, spec.Version, spec.Sites)
	r := runJournaled(ctx, spec, in, j)
	return T2[Result[Out, WorkflowError[E]], *Journal]{F0: r, F1: j}
}

// JournalLen implements `Journal.len()`.
func JournalLen(ctx *Ctx, j *Journal) int64 { return int64(len(j.Entries)) }

// ReplayWorkflow runs the impl against a stored history.
func ReplayWorkflow[S, In, Out, Sig, E any](ctx *Ctx, spec *WorkflowSpec[S, In, Out, Sig, E], in In, j *Journal) Result[Out, WorkflowError[E]] {
	return runJournaled(ctx, spec, in, j)
}

func runJournaled[S, In, Out, Sig, E any](ctx *Ctx, spec *WorkflowSpec[S, In, Out, Sig, E], in In, j *Journal) Result[Out, WorkflowError[E]] {
	if re := j.check(spec.Version, spec.Sites); re != nil {
		return Result_Err[Out](WorkflowError_Replay[E](*re))
	}
	for {
		j.reset()
		c := ctx.child()
		c.Journal = j
		var res Result[Out, WorkflowError[E]]
		var pend *suspend
		var mismatch *replayMismatch
		func() {
			defer func() {
				if p := recover(); p != nil {
					switch x := p.(type) {
					case *suspend:
						pend = x
					case *replayMismatch:
						mismatch = x
					default:
						panic(p)
					}
				}
			}()
			s := Instance(ctx, spec.Name, spec.Init)
			res = spec.Run(c, s, in)
		}()
		if mismatch != nil {
			return Result_Err[Out](WorkflowError_Replay[E](ReplayError{Expected: mismatch.expected, Found: mismatch.found}))
		}
		if pend == nil {
			return res
		}
		ans := ctx.Approve.Ask(ctx, pend.review, pend.out)
		j.resolve(pend.key, ans)
	}
}

func newRunId() RunId {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return RunId(hex.EncodeToString(b[:]))
}

// StartWorkflow starts a run in the background and returns its id.
func StartWorkflow[S, In, Out, Sig, E any](ctx *Ctx, spec *WorkflowSpec[S, In, Out, Sig, E], in In) RunId {
	wf := ctx.workflows()
	id, j := wf.Begin(spec.Name, spec.Version, spec.Sites)
	go func() {
		var res Result[Out, WorkflowError[E]]
		err := protect(ctx, spec.Name, func() { res = runJournaled(ctx, spec, in, j) })
		if err != nil {
			res = Result_Err[Out](WorkflowError_Cancelled[E]())
		}
		wf.Finish(id, res)
	}()
	return id
}

// WorkflowResult returns a finished run's result.
func WorkflowResult(ctx *Ctx, id RunId) (any, bool) {
	return ctx.workflows().Result(id)
}

// SendSignal implements the scenario builtin `send_signal(Impl, sig)` and
// the signal route: the signal is buffered for `await_signal` and
// `on_signal` runs at once.
func SendSignal[S, In, Out, Sig, E any](ctx *Ctx, spec *WorkflowSpec[S, In, Out, Sig, E], sig Sig) Unit {
	ctx.workflows().Signal(spec.Name, sig)
	spec.OnSignal(ctx.child(), Instance(ctx, spec.Name, spec.Init), sig)
	return U
}

func popSignal[S any](ctx *Ctx, name string) (S, bool) {
	v, ok := ctx.workflows().Take(name, func(v any) bool { _, ok := v.(S); return ok })
	if !ok {
		var zero S
		return zero, false
	}
	return v.(S), true
}

// AwaitSignal implements `await_signal<S>(timeout)`.
func AwaitSignal[S any](ctx *Ctx, timeout time.Duration) Option[S] {
	name := ""
	if ctx.Journal != nil {
		name = ctx.Journal.Workflow
	}
	if s, ok := popSignal[S](ctx, name); ok {
		return Option_Some(s)
	}
	ctx.Clock.Sleep(ctx, timeout)
	if s, ok := popSignal[S](ctx, name); ok {
		return Option_Some(s)
	}
	return Option_None[S]()
}

// ---- Serve ---------------------------------------------------------------

// Serve runs every runner under supervision until SIGINT/SIGTERM or ctx.Go
// ends, serving the HTTP routes of each distinct port.
func Serve(ctx *Ctx, runners ...Runner) {
	ctx.sh.mu.Lock()
	ctx.sh.serving = true
	ctx.sh.mu.Unlock()
	gctx, cancel := context.WithCancel(ctx.Go)
	defer cancel()
	ctx.Go = gctx
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
			ctx.Logf("jip: shutting down")
			cancel()
		case <-gctx.Done():
		}
	}()

	muxes := map[int]*http.ServeMux{}
	shutdown := 10 * time.Second
	for _, r := range runners {
		o := r.Opts()
		if o.ShutdownTimeout > shutdown {
			shutdown = o.ShutdownTimeout
		}
		if o.Port <= 0 {
			continue
		}
		mux, ok := muxes[o.Port]
		if !ok {
			mux = http.NewServeMux()
			muxes[o.Port] = mux
		}
		r.Routes(ctx, mux)
	}
	var servers []*http.Server
	for port, mux := range muxes {
		srv := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: mux}
		servers = append(servers, srv)
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				ctx.Logf("jip: http :%d: %v", port, err)
				cancel()
			}
		}()
	}

	var wg sync.WaitGroup
	for _, r := range runners {
		wg.Add(1)
		go func(r Runner) {
			defer wg.Done()
			supervise(ctx, r, cancel)
		}(r)
	}
	<-gctx.Done()
	sctx, scancel := context.WithTimeout(context.Background(), shutdown)
	defer scancel()
	for _, srv := range servers {
		_ = srv.Shutdown(sctx)
	}
	wg.Wait()
	stopCtx := ctx.child()
	stopCtx.Go = sctx
	for _, r := range runners {
		r.Stop(stopCtx)
	}
}

func supervise(ctx *Ctx, r Runner, cancel context.CancelFunc) {
	o := r.Opts()
	var starts []time.Time
	for {
		err := r.Run(ctx)
		if ctx.Go.Err() != nil {
			return
		}
		switch o.Restart {
		case "temporary":
			return
		case "transient":
			if err == nil {
				return
			}
		}
		now := time.Now()
		starts = append(starts, now)
		var kept []time.Time
		for _, t := range starts {
			if o.Period <= 0 || now.Sub(t) <= o.Period {
				kept = append(kept, t)
			}
		}
		starts = kept
		if len(starts) > o.MaxRestarts {
			ctx.Logf("jip: %s: restarted %d times within %v; giving up", r.Name(), len(starts)-1, o.Period)
			cancel()
			return
		}
		ctx.sh.mu.Lock()
		delete(ctx.sh.instances, r.Name())
		ctx.sh.mu.Unlock()
		ctx.Logf("jip: %s: restarting (%d/%d)", r.Name(), len(starts), o.MaxRestarts)
	}
}
