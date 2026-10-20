// Package rt is the runtime every jippitese program links: the Go shape of
// the prelude types, the standard library externs, the behaviour runtime and
// the scenario harness. The compiler copies these sources into
// .jip/gen/rt/ of every build, so the package has no dependency outside the
// Go standard library.
package rt

import (
	"context"
	"embed"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"
)

// Sources are the files of this package, embedded so `jip build` can copy
// them next to the generated code.
//
//go:embed *.go
var Sources embed.FS

// Unit is the Go shape of `()`.
type Unit struct{}

// U is the unit value.
var U = Unit{}

// T2 is a two-element tuple.
type T2[A, B any] struct {
	F0 A
	F1 B
}

// T3 is a three-element tuple.
type T3[A, B, C any] struct {
	F0 A
	F1 B
	F2 C
}

// T4 is a four-element tuple.
type T4[A, B, C, D any] struct {
	F0 A
	F1 B
	F2 C
	F3 D
}

// T5 is a five-element tuple.
type T5[A, B, C, D, E any] struct {
	F0 A
	F1 B
	F2 C
	F3 D
	F4 E
}

// T6 is a six-element tuple.
type T6[A, B, C, D, E, F any] struct {
	F0 A
	F1 B
	F2 C
	F3 D
	F4 E
	F5 F
}

// Path is a file-system path.
type Path string

// RunId identifies a workflow run.
type RunId string

// Query is a repository query; v0 carries no filter.
type Query[T any] struct{}

// QueryAll implements `Query.all()`.
func QueryAll[T any](ctx *Ctx) Query[T] { return Query[T]{} }

// Topic is a handle on a declared topic; the emitter builds one for
// `topics.<name>`.
type Topic[T any] struct{ Name string }

// Ctx is the first argument of every generated function: the effect
// boundary. Fakes and the journal are installed here, so a scenario and a
// running program differ only in what the Ctx holds.
type Ctx struct {
	Go      context.Context
	Clock   Clock
	Rand    *rand.Rand
	Http    HttpClient
	Llm     LlmClient
	Decide  DecideClient
	Approve ApproveClient
	Config  any
	Secrets map[string]string
	// Store is the directory of production workflow journals; "" keeps
	// them in memory.
	Store string

	// Journal is non-nil inside Workflow.run.
	Journal *Journal

	scn *Scn
	sh  *shared
}

// shared is the state every child ctx of a program or scenario sees.
type shared struct {
	mu          sync.Mutex
	instances   map[string]any
	signals     map[string][]any
	results     map[RunId]any
	deadLetters []DeadLetter
	log         Logger
	serving     bool
	queues      map[string]chan any
	fakes       map[string]any // impl name → MemRepo or MemCache; nil substitutes nothing
	machines    map[string]any // Machine impl name → its per-key states
}

// queue returns the in-process queue of a Worker, creating it on first use.
func (sh *shared) queue(name string) chan any {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	q, ok := sh.queues[name]
	if !ok {
		q = make(chan any, 1024)
		sh.queues[name] = q
	}
	return q
}

// DeadLetter is a message a Worker gave up on.
type DeadLetter struct {
	Worker string
	Msg    any
	Err    string
}

// Logger receives runtime log lines.
type Logger interface {
	Logf(format string, args ...any)
}

type stderrLogger struct{}

func (stderrLogger) Logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }

func newShared(log Logger) *shared {
	return &shared{
		instances: map[string]any{},
		signals:   map[string][]any{},
		results:   map[RunId]any{},
		queues:    map[string]chan any{},
		machines:  map[string]any{},
		log:       log,
	}
}

// NewCtx returns a production ctx: real clock, time-seeded random source,
// real HTTP, no model providers (an `llm`, `decide` or `approve` reaches the
// boundary and fails the call).
func NewCtx() *Ctx {
	return &Ctx{
		Go:      context.Background(),
		Clock:   RealClock{},
		Rand:    rand.New(rand.NewSource(time.Now().UnixNano())),
		Http:    realHttp{},
		Llm:     boundaryLlm{},
		Decide:  boundaryDecide{},
		Approve: boundaryApprove{},
		sh:      newShared(stderrLogger{}),
	}
}

// child returns a ctx sharing everything but the journal.
func (c *Ctx) child() *Ctx {
	d := *c
	d.Journal = nil
	return &d
}

// Logf writes one runtime log line.
func (c *Ctx) Logf(format string, args ...any) {
	if c.sh != nil && c.sh.log != nil {
		c.sh.log.Logf(format, args...)
	}
}

// DeadLetters returns the messages Workers gave up on, oldest first.
func (c *Ctx) DeadLetters() []DeadLetter {
	c.sh.mu.Lock()
	defer c.sh.mu.Unlock()
	return append([]DeadLetter(nil), c.sh.deadLetters...)
}

// BoundaryError is raised when an effect reaches the boundary with no fake
// or provider installed.
type BoundaryError struct{ Effect string }

func (e *BoundaryError) Error() string {
	return "effect " + e.Effect + " reached the boundary with no fake installed"
}

func boundary(effect string) {
	panic(&BoundaryError{Effect: effect})
}

// Panic implements the prelude `panic`.
func Panic[T any](ctx *Ctx, msg string) T {
	panic(&PanicError{Msg: msg})
}

// PanicError is what `panic(msg)` raises.
type PanicError struct{ Msg string }

func (e *PanicError) Error() string { return "panic: " + e.Msg }

// Random implements `random()`.
func Random(ctx *Ctx) float64 {
	if ctx.Journal != nil {
		return ctx.Rand.Float64()
	}
	return ctx.Rand.Float64()
}

// HealthUp implements `Health.up()`.
func HealthUp(ctx *Ctx) Health {
	return Health{Live: true, Ready: true, Detail: Option_None[string]()}
}

// HealthAggregate implements `Health.aggregate(x)`; v0 reports up.
func HealthAggregate[T any](ctx *Ctx, x T) Health { return HealthUp(ctx) }

// Instance returns the one instance of a behaviour impl, running init on
// first use. Instances live for the program or the scenario run.
func Instance[S any](ctx *Ctx, name string, init func(*Ctx) (S, error)) S {
	ctx.sh.mu.Lock()
	if v, ok := ctx.sh.instances[name]; ok {
		ctx.sh.mu.Unlock()
		return v.(S)
	}
	ctx.sh.mu.Unlock()
	v, err := init(ctx.child())
	if err != nil {
		panic(&InitError{Name: name, Err: err.Error()})
	}
	ctx.sh.mu.Lock()
	defer ctx.sh.mu.Unlock()
	if w, ok := ctx.sh.instances[name]; ok {
		return w.(S)
	}
	ctx.sh.instances[name] = v
	return v
}

// InitError is raised when an impl's init returns Err.
type InitError struct{ Name, Err string }

func (e *InitError) Error() string { return "init of `" + e.Name + "` failed: " + e.Err }

// InitFailed converts an init Err payload into the error Instance reports.
func InitFailed[E any](e E) error { return fmt.Errorf("%s", Show(e)) }
