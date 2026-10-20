package rt

import (
	"strings"
	"testing"
)

func TestThenLinesReportIndependently(t *testing.T) {
	s := newScn(t, ScnOpts{Module: "m", Name: "n"}, 1, false)
	f := s.attempt(func(s *Scn) {
		s.Then("a.jip:1:1", "x", false)
		s.Then("a.jip:2:1", "y", true)
		s.Then("a.jip:3:1", "z", false)
	})
	if f == nil || !strings.Contains(f.msg, "then x is false") || !strings.Contains(f.msg, "then z is false") || f.pos != "a.jip:1:1" {
		t.Fatalf("got %+v", f)
	}
}

func TestSeedFileReplayedFirst(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JIP_SEED_DIR", dir)
	t.Setenv("JIP_RUNS", "1")
	o := ScnOpts{Module: "collections.stack", Name: "pop", Derived: true, Property: true}
	if got := seedRel(o); got != "collections/stack/pop.seed" {
		t.Fatalf("seed path %q", got)
	}
	s := &Scn{Opts: o}
	s.writeSeed(0x1c9e3a7f)
	if v, ok := readSeed(seedPath(o)); !ok || v != 0x1c9e3a7f {
		t.Fatalf("read back %x %v", v, ok)
	}
	var seeds []int64
	Scenario(t, o, func(s *Scn) { seeds = append(seeds, s.seed) })
	if len(seeds) != 2 || seeds[0] != 0x1c9e3a7f {
		t.Fatalf("runs %x", seeds)
	}
}
