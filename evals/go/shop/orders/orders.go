// Package orders keeps running order totals per customer, fed by placed
// orders.
package orders

import (
	"context"
	"time"
)

// Placed says order ID was placed by a customer.
type Placed struct {
	ID       int
	Customer int
	Total    int
}

// RejectedError is an order the indexer refuses to count.
type RejectedError struct {
	Reason string
}

func (e *RejectedError) Error() string { return "rejected: " + e.Reason }

// Retry is how often a failed message is retried, and the wait between
// attempts.
type Retry struct {
	Max     int
	Backoff time.Duration
}

// Indexer adds every placed order to its customer's total, once per order
// id.
type Indexer struct {
	Totals  *Repo[int, int]  // customer → running total
	Counted *Repo[int, bool] // ids of orders already added to a total
	Retry   Retry
	Dead    []Placed // messages that failed every attempt
}

// NewIndexer is an indexer over empty stores.
func NewIndexer() *Indexer {
	return &Indexer{
		Totals:  NewRepo[int, int](),
		Counted: NewRepo[int, bool](),
		Retry:   Retry{Max: 2, Backoff: 10 * time.Millisecond},
	}
}

// Perform adds m to its customer's total unless it was counted before.
func (ix *Indexer) Perform(m Placed) error {
	if m.Total < 0 {
		return &RejectedError{"negative total"}
	}
	if _, ok := ix.Counted.Get(m.ID); ok {
		return nil
	}
	current, _ := ix.Totals.Get(m.Customer)
	ix.Totals.Put(m.Customer, current+m.Total)
	ix.Counted.Put(m.ID, true)
	return nil
}

// Deliver performs m, retrying as ix.Retry says, and dead-letters it when
// every attempt fails.
func (ix *Indexer) Deliver(m Placed) {
	for attempt := 0; ; attempt++ {
		if ix.Perform(m) == nil {
			return
		}
		if attempt == ix.Retry.Max {
			ix.Dead = append(ix.Dead, m)
			return
		}
		time.Sleep(ix.Retry.Backoff)
	}
}

// Run delivers each placed order until placed is closed or ctx is done.
func (ix *Indexer) Run(ctx context.Context, placed <-chan Placed) {
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-placed:
			if !ok {
				return
			}
			ix.Deliver(m)
		}
	}
}
