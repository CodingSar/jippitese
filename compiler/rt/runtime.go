package rt

import (
	"fmt"
	"os"
	"sort"
	"sync"
)

// WorkflowRuntime is where workflow runs live: their journals, results and
// buffered signals. `workflow { mode }` of app.jip names one in
// WorkflowRuntimes; `embedded` is the only adapter so far.
type WorkflowRuntime interface {
	// Begin journals a new run of the workflow name.
	Begin(name string, version int64, sites []string) (RunId, *Journal)
	// Finish records the result of a run.
	Finish(id RunId, res any)
	// Result is a finished run's result.
	Result(id RunId) (any, bool)
	// Signal buffers sig for the runs of workflow name.
	Signal(name string, sig any)
	// Take removes and returns the oldest signal of workflow name that
	// match accepts.
	Take(name string, match func(any) bool) (any, bool)
}

// WorkflowRuntimes are the workflow runtime adapters by mode name; store
// is `workflow { store }`.
var WorkflowRuntimes = map[string]func(store string) (WorkflowRuntime, error){
	"embedded": func(store string) (WorkflowRuntime, error) { return NewEmbedded(store), nil },
}

// UseWorkflowRuntime installs the adapter of mode on ctx and everything
// sharing its program.
func (c *Ctx) UseWorkflowRuntime(mode, store string) error {
	mk, ok := WorkflowRuntimes[mode]
	if !ok {
		var modes []string
		for m := range WorkflowRuntimes {
			modes = append(modes, m)
		}
		sort.Strings(modes)
		return fmt.Errorf("workflow mode %q has no runtime adapter; available: %v", mode, modes)
	}
	wf, err := mk(store)
	if err != nil {
		return err
	}
	c.sh.mu.Lock()
	c.sh.wf = wf
	c.sh.mu.Unlock()
	return nil
}

// workflows is the program's workflow runtime.
func (c *Ctx) workflows() WorkflowRuntime {
	c.sh.mu.Lock()
	defer c.sh.mu.Unlock()
	return c.sh.wf
}

// Embedded is the in-process workflow runtime: runs are goroutines,
// journals are JSONL files under Store (in memory when Store is ""), and
// results and signals live in memory.
type Embedded struct {
	Store   string
	mu      sync.Mutex
	signals map[string][]any
	results map[RunId]any
}

// NewEmbedded is an embedded runtime journaling under store.
func NewEmbedded(store string) *Embedded {
	return &Embedded{Store: store, signals: map[string][]any{}, results: map[RunId]any{}}
}

func (e *Embedded) Begin(name string, version int64, sites []string) (RunId, *Journal) {
	id := newRunId()
	j := NewJournal(name, version, sites)
	if e.Store != "" {
		_ = os.MkdirAll(e.Store, 0o755)
		j.file = e.Store + string(os.PathSeparator) + name + "-" + string(id) + ".jsonl"
	}
	return id, j
}

func (e *Embedded) Finish(id RunId, res any) {
	e.mu.Lock()
	e.results[id] = res
	e.mu.Unlock()
}

func (e *Embedded) Result(id RunId) (any, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.results[id]
	return v, ok
}

func (e *Embedded) Signal(name string, sig any) {
	e.mu.Lock()
	e.signals[name] = append(e.signals[name], sig)
	e.mu.Unlock()
}

func (e *Embedded) Take(name string, match func(any) bool) (any, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	buf := e.signals[name]
	for i, v := range buf {
		if match(v) {
			e.signals[name] = append(buf[:i:i], buf[i+1:]...)
			return v, true
		}
	}
	return nil, false
}
