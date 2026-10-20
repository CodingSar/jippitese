// Package store keeps logged-in sessions and sweeps out idle ones.
package store

import (
	"context"
	"time"
)

// Session is a logged-in session.
type Session struct {
	ID       string
	LastSeen time.Time
}

// SweepEvery is how often the sweeper runs.
const SweepEvery = 5 * time.Minute

// Store holds the sessions and the clock they are stamped with.
type Store struct {
	Sessions *Repo[string, Session]
	Now      func() time.Time
}

// New is an empty store on the wall clock.
func New() *Store {
	return &Store{Sessions: NewRepo[string, Session](), Now: time.Now}
}

// Sweep removes sessions idle for more than an hour.
func (s *Store) Sweep() {
	now := s.Now()
	for _, ses := range s.Sessions.All() {
		if now.Sub(ses.LastSeen) > time.Hour {
			s.Sessions.Delete(ses.ID)
		}
	}
}

// RunSweeper sweeps every SweepEvery until ctx is done.
func (s *Store) RunSweeper(ctx context.Context) {
	t := time.NewTicker(SweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sweep()
		}
	}
}

// Touch records activity on a session at the current time.
func (s *Store) Touch(id string) {
	s.Sessions.Put(id, Session{ID: id, LastSeen: s.Now()})
}
