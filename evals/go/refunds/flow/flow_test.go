package flow

import (
	"context"
	"errors"
	"testing"
	"time"

	"refunds/durable"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// script answers approvals in order and remembers what it was asked.
type script struct {
	answers []Decision
	asked   []Review
}

func (s *script) Approve(ctx context.Context, r Review) (Decision, error) {
	s.asked = append(s.asked, r)
	if len(s.answers) == 0 {
		return 0, errors.New("no scripted answer")
	}
	d := s.answers[0]
	s.answers = s.answers[1:]
	return d, nil
}

// newFake is a refund workflow on a fake clock answering approvals from
// answers.
func newFake(answers ...Decision) (*Refund, *script, *fakeClock) {
	s := &script{answers: answers}
	c := &fakeClock{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	return &Refund{Approver: s, Now: c.now}, s, c
}

func run(w *Refund, r RefundReq) (Refunded, error) {
	return w.Run(context.Background(), durable.New(Version), r)
}

func TestASmallRefundPaysWithoutAsking(t *testing.T) {
	w, approvals, _ := newFake()
	p, err := run(w, RefundReq{Order: 1, Amount: 120})
	if err != nil || p.Amount != 120 {
		t.Errorf("Run = %+v, %v; want 120", p, err)
	}
	if len(approvals.asked) != 0 {
		t.Errorf("asked %d approvals, want none", len(approvals.asked))
	}
}

func TestALargeRefundWaitsForFinanceAndReplaysFromItsJournal(t *testing.T) {
	w, approvals, clock := newFake(Approve)
	req := RefundReq{Order: 2, Amount: 900}
	j := durable.New(Version)
	p, err := w.Run(context.Background(), j, req)
	if err != nil || p.Amount != 900 {
		t.Fatalf("Run = %+v, %v; want 900", p, err)
	}
	if len(j.Events) != 2 {
		t.Errorf("journal has %d steps, want 2", len(j.Events))
	}
	clock.advance(time.Hour)
	again, err := w.Run(context.Background(), j.Replay(), req)
	if err != nil || again.Order != p.Order || again.Amount != p.Amount || !again.At.Equal(p.At) {
		t.Errorf("replay = %+v, %v; want %+v", again, err, p)
	}
	if len(approvals.asked) != 1 {
		t.Errorf("asked %d approvals, want 1", len(approvals.asked))
	}
}

func TestFinanceCanDecline(t *testing.T) {
	w, _, _ := newFake(Deny)
	if _, err := run(w, RefundReq{Order: 3, Amount: 5_000}); !errors.Is(err, ErrDeclined) {
		t.Errorf("err = %v, want ErrDeclined", err)
	}
}

func TestAHistoryThatDoesNotMatchTheRunIsAReplayError(t *testing.T) {
	w, approvals, _ := newFake(Approve)
	small := durable.New(Version)
	if _, err := w.Run(context.Background(), small, RefundReq{Order: 4, Amount: 100}); err != nil {
		t.Fatal(err)
	}
	_, err := w.Run(context.Background(), small.Replay(), RefundReq{Order: 4, Amount: 900})
	if !errors.Is(err, durable.ErrReplay) {
		t.Errorf("err = %v, want ErrReplay", err)
	}
	if len(approvals.asked) != 0 {
		t.Errorf("asked %d approvals, want none", len(approvals.asked))
	}
}
