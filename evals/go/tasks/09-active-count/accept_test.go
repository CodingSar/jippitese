package store

import (
	"testing"
	"time"
)

func TestAccept09ActiveCountFollowsTouchesAndSweeps(t *testing.T) {
	s, clock := newFake()
	s.Touch("x")
	s.Touch("y")
	before := s.ActiveCount()
	clock.advance(2 * time.Hour)
	s.Touch("z")
	s.Sweep()
	if before != 2 {
		t.Errorf("before = %d, want 2", before)
	}
	if n := s.ActiveCount(); n != 1 {
		t.Errorf("ActiveCount = %d, want 1", n)
	}
}
