// Package flow refunds orders durably: large refunds wait for finance.
package flow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"refunds/durable"
)

// RefundReq is a refund request for an order, in minor units.
type RefundReq struct {
	Order  int
	Amount int
}

// Refunded is a paid-out refund.
type Refunded struct {
	Order  int
	Amount int
	At     time.Time
}

// TooLargeError is a refund above the limit.
type TooLargeError struct {
	Amount int
}

func (e *TooLargeError) Error() string { return fmt.Sprintf("refund of %d is too large", e.Amount) }

// ErrDeclined is a refund finance declined.
var ErrDeclined = errors.New("refund declined")

// Decision is what finance answers on a large refund.
type Decision int

const (
	Approve Decision = iota
	Deny
)

// Review is what a human approver is shown.
type Review struct {
	Title   string
	Body    string
	Timeout time.Duration
}

// Approver asks a human and waits for the answer.
type Approver interface {
	Approve(ctx context.Context, r Review) (Decision, error)
}

// Version is the workflow version; bump it when the step sequence changes.
const Version = 1

// Refund is the refund workflow.
type Refund struct {
	Approver Approver
	Now      func() time.Time
}

// Pay pays a refund out; it stands in for a ledger call.
func (w *Refund) Pay(r RefundReq) (Refunded, error) {
	return Refunded{Order: r.Order, Amount: r.Amount, At: w.Now()}, nil
}

// Run refunds r, journaling each step in j.
func (w *Refund) Run(ctx context.Context, j *durable.Journal, r RefundReq) (Refunded, error) {
	if err := j.Check(Version); err != nil {
		return Refunded{}, err
	}
	if r.Amount > 10_000 {
		return Refunded{}, &TooLargeError{r.Amount}
	}
	if r.Amount >= 500 {
		review := Review{
			Title:   fmt.Sprintf("Refund for order %d", r.Order),
			Body:    fmt.Sprintf("Amount %d", r.Amount),
			Timeout: 24 * time.Hour,
		}
		d, err := durable.Step(j, "approve", func() (Decision, error) { return w.Approver.Approve(ctx, review) })
		if err != nil {
			return Refunded{}, err
		}
		if d == Deny {
			return Refunded{}, ErrDeclined
		}
	}
	return durable.Step(j, "pay", func() (Refunded, error) { return w.Pay(r) })
}
