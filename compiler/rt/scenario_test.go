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
