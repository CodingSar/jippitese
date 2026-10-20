// Package cache caches exchange rates in process.
package cache

import (
	"sync"
	"time"
)

// Entry is a cached value and the instant it stops being valid.
type Entry struct {
	Value   int
	Expires time.Time
}

// DefaultTTL is how long an entry lives when Set names no ttl.
const DefaultTTL = 5 * time.Minute

// Rates caches exchange rates in basis points, safe for concurrent use.
type Rates struct {
	mu      sync.Mutex
	entries map[string]Entry
	Now     func() time.Time
}

// New is an empty cache on the wall clock.
func New() *Rates {
	return &Rates{entries: map[string]Entry{}, Now: time.Now}
}

// Get is the cached value of k while it has not expired.
func (c *Rates) Get(k string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok {
		return 0, false
	}
	if !c.Now().Before(e.Expires) {
		delete(c.entries, k)
		return 0, false
	}
	return e.Value, true
}

// Set caches v at k for ttl, or for DefaultTTL when ttl is 0.
func (c *Rates) Set(k string, v int, ttl time.Duration) {
	if ttl == 0 {
		ttl = DefaultTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[k] = Entry{Value: v, Expires: c.Now().Add(ttl)}
}

// Invalidate drops k.
func (c *Rates) Invalidate(k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, k)
}

// Rate is the rate of a currency: the cached value, or the quoted one
// cached anew.
func (c *Rates) Rate(code string) int {
	if r, ok := c.Get(code); ok {
		return r
	}
	r := Quote(code)
	c.Set(code, r, 0)
	return r
}

// Quote is a fixed quote standing in for a market data call.
func Quote(code string) int {
	switch code {
	case "EUR":
		return 10850
	case "GBP":
		return 12700
	default:
		return 10000
	}
}
