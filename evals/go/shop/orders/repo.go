package orders

import (
	"maps"
	"slices"
	"sync"
)

// Repo is an in-process key-value store, safe for concurrent use.
type Repo[K comparable, V any] struct {
	mu sync.Mutex
	m  map[K]V
}

// NewRepo is an empty store.
func NewRepo[K comparable, V any]() *Repo[K, V] {
	return &Repo[K, V]{m: map[K]V{}}
}

// Get is the value stored at k, and whether there is one.
func (r *Repo[K, V]) Get(k K) (V, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.m[k]
	return v, ok
}

// Put stores v at k.
func (r *Repo[K, V]) Put(k K, v V) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[k] = v
}

// Delete removes k.
func (r *Repo[K, V]) Delete(k K) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, k)
}

// All is every stored value, in no particular order.
func (r *Repo[K, V]) All() []V {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Collect(maps.Values(r.m))
}
