package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUsage(t *testing.T) {
	var o, e bytes.Buffer
	if code := run(nil, &o, &e); code != 2 || !strings.Contains(e.String(), "usage: jip") {
		t.Fatalf("no args: %d %s", code, e.String())
	}
	e.Reset()
	if code := run([]string{"frobnicate"}, &o, &e); code != 2 || !strings.Contains(e.String(), "unknown command") {
		t.Fatalf("unknown: %d %s", code, e.String())
	}
	if code := run([]string{"build", "--nope"}, &o, &e); code != 2 {
		t.Fatalf("bad flag: %d", code)
	}
	if code := run([]string{"help"}, &o, &e); code != 0 {
		t.Fatalf("help: %d", code)
	}
}
