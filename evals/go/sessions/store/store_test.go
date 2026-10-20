package store

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newFake is an empty store on a fake clock.
func newFake() (*Store, *fakeClock) {
	c := &fakeClock{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := New()
	s.Now = c.now
	return s, c
}

func TestASweepRemovesOnlySessionsIdleForMoreThanAnHour(t *testing.T) {
	s, clock := newFake()
	s.Touch("old")
	clock.advance(50 * time.Minute)
	s.Touch("fresh")
	clock.advance(20 * time.Minute)
	s.Sweep()
	if _, ok := s.Sessions.Get("old"); ok {
		t.Error("old session survived the sweep")
	}
	if _, ok := s.Sessions.Get("fresh"); !ok {
		t.Error("fresh session was swept")
	}
}

func TestAdvancingTheClockAloneNeverSweeps(t *testing.T) {
	s, clock := newFake()
	s.Touch("old")
	clock.advance(2 * time.Hour)
	if _, ok := s.Sessions.Get("old"); !ok {
		t.Error("session removed without a sweep")
	}
}
