package store

import (
	"testing"
	"time"
)

func TestAccept08SessionsIdleOver30MinutesAreSwept(t *testing.T) {
	s, clock := newFake()
	s.Touch("a")
	clock.advance(20 * time.Minute)
	s.Touch("b")
	clock.advance(20 * time.Minute)
	s.Sweep()
	if _, ok := s.Sessions.Get("a"); ok {
		t.Error("a was not swept")
	}
	if _, ok := s.Sessions.Get("b"); !ok {
		t.Error("b was swept")
	}
}
