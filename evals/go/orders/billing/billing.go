// Package billing bills orders: totals, account withdrawals, charging
// through the payment gateway and the billing workflow.
package billing

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// Money is an amount in minor units.
type Money int

// Order is a customer order. Total is never negative.
type Order struct {
	ID    OrderID `json:"id"`
	Lines []Line  `json:"lines"`
	Total Money   `json:"total"`
}

// NoFundsError is returned when an account cannot cover a withdrawal.
type NoFundsError struct {
	Amount Money
}

func (e *NoFundsError) Error() string { return fmt.Sprintf("insufficient funds for %d", e.Amount) }

// NetError is a failure to reach the payment gateway.
type NetError struct {
	Err error
}

func (e *NetError) Error() string { return "gateway: " + e.Err.Error() }
func (e *NetError) Unwrap() error { return e.Err }

// DeclinedError is a charge the gateway or finance declined.
type DeclinedError struct {
	Reason string
}

func (e *DeclinedError) Error() string { return "declined: " + e.Reason }

// Status is a charge status.
type Status int

const (
	Pending Status = iota
	Paid
	Failed
)

// Decision is what finance answers on a high-risk order.
type Decision int

const (
	Proceed Decision = iota
	Decline
)

// now is the clock; tests replace it.
var now = time.Now

// Total is the sum of line totals.
func Total(lines []Line) Money {
	var t Money
	for _, l := range lines {
		t += l.Price * Money(l.Qty)
	}
	return t
}

// Withdraw withdraws amt; it fails if funds are insufficient. It panics
// when amt is not positive.
func Withdraw(acc Account, amt Money) (Account, error) {
	if amt <= 0 {
		panic("billing.Withdraw: amt must be positive")
	}
	if acc.Balance < amt {
		return Account{}, &NoFundsError{amt}
	}
	acc.Balance -= amt
	return acc, nil
}

// Charge charges the order through the payment gateway.
func Charge(ctx context.Context, o Order, gw Gateway) (Receipt, error) {
	body, err := json.Marshal(ChargeReq{Order: o.ID, Amount: o.Total})
	if err != nil {
		return Receipt{}, err
	}
	resp, err := post(ctx, gw.URL, body)
	if err != nil {
		return Receipt{}, &NetError{err}
	}
	defer resp.Body.Close()
	at := now()
	attempts := 0
	for attempts < 3 && resp.StatusCode == http.StatusServiceUnavailable {
		attempts++
	}
	var r Receipt
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Receipt{}, &DeclinedError{"bad receipt: " + err.Error()}
	}
	r.At = at
	return r, nil
}

func post(ctx context.Context, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// RiskModel picks the risk tier of an order.
type RiskModel interface {
	Risk(ctx context.Context, o Order) (Tier, error)
}

// Summarizer summarises an order for a human reviewer.
type Summarizer interface {
	Summarize(ctx context.Context, o Order) (Summary, error)
}

// Approver asks a human and waits for the answer.
type Approver interface {
	Approve(ctx context.Context, r Review) (Decision, error)
}

// Size is the number of lines on the order.
func (o Order) Size() int { return len(o.Lines) }

// Billing bills an order: charge, then ask finance when the risk is high.
type Billing struct {
	Gateway  Gateway
	Model    RiskModel
	Approver Approver
}

// Run bills o.
func (b *Billing) Run(ctx context.Context, o Order) (Receipt, error) {
	r, err := Charge(ctx, o, b.Gateway)
	if err != nil {
		return Receipt{}, err
	}
	tier, err := b.Model.Risk(ctx, o)
	if err != nil {
		return Receipt{}, err
	}
	if tier != High {
		return r, nil
	}
	review := Review{
		Title:   fmt.Sprintf("High-risk order %d", o.ID),
		Body:    fmt.Sprintf("Total %d", o.Total),
		Timeout: 48 * time.Hour,
	}
	d, err := b.Approver.Approve(ctx, review)
	if err != nil {
		return Receipt{}, err
	}
	if d == Decline {
		return Receipt{}, &DeclinedError{"finance declined"}
	}
	return r, nil
}

// BillingAPI handles billing requests over HTTP.
type BillingAPI struct {
	Gateway Gateway
}

func (a *BillingAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req BillReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rc, err := Charge(r.Context(), req.Order, a.Gateway)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rc)
}

// both fetches two pages concurrently; when one fails the other is
// cancelled.
func both(ctx context.Context, a, b string) ([2][]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var out [2][]byte
	errs := make(chan error, 2)
	for i, u := range []string{a, b} {
		go func() {
			body, err := get(ctx, u)
			if err != nil {
				cancel()
			}
			out[i] = body
			errs <- err
		}()
	}
	var first error
	for range 2 {
		if err := <-errs; err != nil && first == nil {
			first = err
		}
	}
	return out, first
}

// bump increments a shared counter.
func bump(c *atomic.Int64) int64 { return c.Add(1) }

// MaxBy is the element of xs with the greatest key, the first on ties;
// false when xs is empty.
func MaxBy[T any, K cmp.Ordered](xs []T, key func(T) K) (T, bool) {
	var best T
	if len(xs) == 0 {
		return best, false
	}
	best = xs[0]
	for _, x := range xs[1:] {
		if key(x) > key(best) {
			best = x
		}
	}
	return best, true
}

// ReadFile reads a file.
func ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
