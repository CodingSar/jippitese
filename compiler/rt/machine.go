package rt

import (
	"net/http"
	"sync"
	"time"
)

// MachineSpec is what the emitter builds for `impl Machine for X`.
type MachineSpec[S, K, St, Ev, Act, E any] struct {
	Name       string
	Init       func(*Ctx) (S, error)
	Activate   func(*Ctx, S, K) St
	On         func(*Ctx, S, St, Ev) T2[St, []Act]
	Act        func(*Ctx, S, K, Act) Result[[]Ev, E]
	Deactivate func(*Ctx, S, K, St) Unit
	Stop       func(*Ctx, S) Unit
	Health     func(*Ctx, S) Health
}

// machineKey is the persisted state of one key (spec 06, Machine rule).
type machineKey[K, St any] struct {
	key    K
	state  St
	active bool    // loaded; false after deactivate until the next event
	last   Instant // end of the last turn
}

// machineStates holds every key of one Machine impl. turn serialises the
// turns, so at most one runs per key (and per impl) at a time.
type machineStates[K, St any] struct {
	turn sync.Mutex
	mu   sync.Mutex
	keys map[string]*machineKey[K, St]
}

func machineStore[S, K, St, Ev, Act, E any](ctx *Ctx, spec *MachineSpec[S, K, St, Ev, Act, E]) *machineStates[K, St] {
	ctx.sh.mu.Lock()
	defer ctx.sh.mu.Unlock()
	m, ok := ctx.sh.machines[spec.Name].(*machineStates[K, St])
	if !ok {
		m = &machineStates[K, St]{keys: map[string]*machineKey[K, St]{}}
		ctx.sh.machines[spec.Name] = m
	}
	return m
}

type machineMsg[K, Ev any] struct {
	key K
	ev  Ev
}

// machineTurns runs the turn of event e for key k, then the turns of the
// events its actions returned, in order: activate on the key's first
// event, `on`, persist the state, then `act` for each action. A failed
// `act` is logged; its action's events are lost.
func machineTurns[S, K, St, Ev, Act, E any](ctx *Ctx, spec *MachineSpec[S, K, St, Ev, Act, E], k K, e Ev) {
	store := machineStore(ctx, spec)
	store.turn.Lock()
	defer store.turn.Unlock()
	s := Instance(ctx, spec.Name, spec.Init)
	id := Show(k)
	queue := []Ev{e}
	for len(queue) > 0 {
		ev := queue[0]
		queue = queue[1:]
		_ = protect(ctx, spec.Name, func() {
			store.mu.Lock()
			mk, ok := store.keys[id]
			store.mu.Unlock()
			if !ok {
				mk = &machineKey[K, St]{key: k, state: spec.Activate(ctx.child(), s, k)}
			}
			r := spec.On(ctx.child(), s, mk.state, ev)
			store.mu.Lock()
			mk.state, mk.active, mk.last = r.F0, true, ctx.Clock.Now()
			store.keys[id] = mk
			store.mu.Unlock()
			for _, a := range r.F1 {
				res := spec.Act(ctx.child(), s, k, a)
				if res.Tag == ResultTag_Err {
					ctx.Logf("jip: %s: act %s failed: %s", spec.Name, Show(a), Show(res.Err_0))
					continue
				}
				queue = append(queue, res.Ok_0...)
			}
		})
	}
}

// MachineSend implements `<Machine>.send(key, e)`: queued under Serve,
// run synchronously otherwise (scenarios), so a scenario observes every
// turn it caused once `send` returns.
func MachineSend[S, K, St, Ev, Act, E any](ctx *Ctx, spec *MachineSpec[S, K, St, Ev, Act, E], k K, e Ev) Result[Unit, MachineError] {
	ctx.sh.mu.Lock()
	serving := ctx.sh.serving
	ctx.sh.mu.Unlock()
	if serving {
		select {
		case ctx.sh.queue(spec.Name) <- machineMsg[K, Ev]{k, e}:
			return Result_Ok[Unit, MachineError](U)
		default:
			return Result_Err[Unit](MachineError_Full())
		}
	}
	machineTurns(ctx, spec, k, e)
	return Result_Ok[Unit, MachineError](U)
}

// machineIdle runs `deactivate` on every loaded key whose last turn ended
// at least idle ago. Its state stays persisted and is reloaded by the
// key's next event.
func machineIdle[S, K, St, Ev, Act, E any](ctx *Ctx, spec *MachineSpec[S, K, St, Ev, Act, E], idle time.Duration) {
	store := machineStore(ctx, spec)
	store.turn.Lock()
	defer store.turn.Unlock()
	s := Instance(ctx, spec.Name, spec.Init)
	now := ctx.Clock.Now()
	store.mu.Lock()
	var idleKeys []*machineKey[K, St]
	for _, mk := range store.keys {
		if mk.active && time.Duration(now.Nanos-mk.last.Nanos) >= idle {
			idleKeys = append(idleKeys, mk)
		}
	}
	store.mu.Unlock()
	for _, mk := range idleKeys {
		_ = protect(ctx, spec.Name, func() { spec.Deactivate(ctx.child(), s, mk.key, mk.state) })
		store.mu.Lock()
		mk.active = false
		store.mu.Unlock()
	}
}

type machineRunner[S, K, St, Ev, Act, E any] struct {
	spec *MachineSpec[S, K, St, Ev, Act, E]
	opts RunOpts
}

// MachineRunner wraps a Machine impl for Serve.
func MachineRunner[S, K, St, Ev, Act, E any](spec *MachineSpec[S, K, St, Ev, Act, E], opts RunOpts) Runner {
	return &machineRunner[S, K, St, Ev, Act, E]{spec: spec, opts: opts}
}

func (r *machineRunner[S, K, St, Ev, Act, E]) Name() string  { return r.spec.Name }
func (r *machineRunner[S, K, St, Ev, Act, E]) Opts() RunOpts { return r.opts }
func (r *machineRunner[S, K, St, Ev, Act, E]) Routes(ctx *Ctx, mux *http.ServeMux) {
	healthRoute(ctx, mux, r.spec.Name, r.spec.Init, r.spec.Health)
}

func (r *machineRunner[S, K, St, Ev, Act, E]) Run(ctx *Ctx) error {
	return protect(ctx, r.spec.Name, func() {
		Instance(ctx, r.spec.Name, r.spec.Init)
		idle := r.opts.Idle
		if idle <= 0 {
			idle = 5 * time.Minute
		}
		go func() {
			for ctx.Go.Err() == nil {
				ctx.Clock.Sleep(ctx, idle/2)
				if ctx.Go.Err() == nil {
					machineIdle(ctx, r.spec, idle)
				}
			}
		}()
		q := ctx.sh.queue(r.spec.Name)
		for {
			select {
			case <-ctx.Go.Done():
				return
			case m := <-q:
				msg := m.(machineMsg[K, Ev])
				machineTurns(ctx, r.spec, msg.key, msg.ev)
			}
		}
	})
}

func (r *machineRunner[S, K, St, Ev, Act, E]) Stop(ctx *Ctx) {
	_ = protect(ctx, r.spec.Name, func() { r.spec.Stop(ctx.child(), Instance(ctx, r.spec.Name, r.spec.Init)) })
}
