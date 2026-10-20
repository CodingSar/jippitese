package rt

import (
	"math/rand"
	"testing"
)

func TestGenIntSizeBounds(t *testing.T) {
	g := &Gen{R: rand.New(rand.NewSource(1)), Size: 100}
	pos, neg := false, false
	for i := 0; i < 1000; i++ {
		v := GenAnyInt(nil, g)
		if v < -100 || v > 100 {
			t.Fatalf("any Int out of size bound: %d", v)
		}
		pos, neg = pos || v > 0, neg || v < 0
		if w := GenInt(nil, g, 1, 1_000_000); w < 1 || w > 101 {
			t.Fatalf("g.int(1, 1_000_000) at size 100: %d", w)
		}
		if w := GenInt(nil, g, -1_000_000, -5); w < -105 || w > -5 {
			t.Fatalf("g.int(-1_000_000, -5) at size 100: %d", w)
		}
	}
	if !pos || !neg {
		t.Fatalf("any Int drew only one sign: pos=%v neg=%v", pos, neg)
	}
}
