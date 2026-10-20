package rt

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
)

// collide hashes into 4 buckets, so leaves hold several keys.
type collide struct{ N int64 }

func (c collide) JipHash() uint64 { return uint64(c.N % 4) }

func TestMapAgainstGoMap(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	m, ref := Map[int64, int64]{}, map[int64]int64{}
	var order []int64
	for i := 0; i < 20000; i++ {
		k := rng.Int63n(3000)
		if rng.Intn(3) == 0 {
			m = MapRemove(nil, m, k)
			if _, ok := ref[k]; ok {
				delete(ref, k)
				order = slices.DeleteFunc(order, func(x int64) bool { return x == k })
			}
			continue
		}
		if _, ok := ref[k]; !ok {
			order = append(order, k)
		}
		m, ref[k] = MapInsert(nil, m, k, int64(i)), int64(i)
	}
	if MapLen(nil, m) != int64(len(ref)) {
		t.Fatalf("len %d, want %d", MapLen(nil, m), len(ref))
	}
	for k, v := range ref {
		if got := MapGet(nil, m, k); got != Option_Some(v) {
			t.Fatalf("get %d: %+v, want %d", k, got, v)
		}
	}
	if !slices.Equal(MapKeys(nil, m), order) {
		t.Fatal("keys not in insertion order")
	}
	for k := int64(0); k < 3000; k++ {
		if _, ok := ref[k]; !ok && MapContainsKey(nil, m, k) {
			t.Fatalf("removed key %d present", k)
		}
	}
}

func TestMapCollisions(t *testing.T) {
	m := Map[collide, string]{}
	for i := int64(0); i < 64; i++ {
		m = MapInsert(nil, m, collide{i}, fmt.Sprint(i))
	}
	m = MapInsert(nil, m, collide{5}, "five")
	for i := int64(0); i < 64; i += 2 {
		m = MapRemove(nil, m, collide{i})
	}
	if MapLen(nil, m) != 32 {
		t.Fatalf("len %d", MapLen(nil, m))
	}
	for i := int64(0); i < 64; i++ {
		got := MapGet(nil, m, collide{i})
		switch {
		case i%2 == 0 && got.Tag != OptionTag_None:
			t.Errorf("%d removed but %+v", i, got)
		case i == 5 && got != Option_Some("five"):
			t.Errorf("5: %+v", got)
		case i%2 == 1 && i != 5 && got != Option_Some(fmt.Sprint(i)):
			t.Errorf("%d: %+v", i, got)
		}
	}
	if k := MapKeys(nil, m); k[0] != (collide{1}) || k[2] != (collide{5}) {
		t.Errorf("order %v", k)
	}
}

func TestMapPersistent(t *testing.T) {
	a := MapInsert(nil, MapInsert(nil, Map[string, int64]{}, "x", 1), "y", 2)
	b := MapInsert(nil, a, "x", 9)
	c := MapRemove(nil, a, "y")
	if MapGet(nil, a, "x") != Option_Some[int64](1) || MapLen(nil, a) != 2 {
		t.Error("a changed")
	}
	if MapGet(nil, b, "x") != Option_Some[int64](9) || MapKeys(nil, b)[0] != "x" {
		t.Error("update moved or lost x")
	}
	if MapLen(nil, c) != 1 || MapContainsKey(nil, c, "y") {
		t.Error("remove")
	}
	mm := MapThaw(nil, a)
	MutMapInsert(nil, mm, "z", 3)
	if MapLen(nil, a) != 2 || MapLen(nil, MutMapFreeze(nil, mm)) != 3 {
		t.Error("thaw shares writes")
	}
}

func TestMapEqualHash(t *testing.T) {
	type rec struct {
		A string
		B []int64
	}
	a := MapInsert(nil, MapInsert(nil, Map[rec, int64]{}, rec{"p", []int64{1}}, 1), rec{"q", nil}, 2)
	b := MapInsert(nil, MapInsert(nil, Map[rec, int64]{}, rec{"q", []int64{}}, 2), rec{"p", []int64{1}}, 1)
	if !Equal(a, b) || Hash(a) != Hash(b) {
		t.Error("maps equal up to order differ")
	}
	if Equal(a, MapInsert(nil, b, rec{"q", nil}, 3)) {
		t.Error("different values equal")
	}
	s := SetInsert(nil, SetInsert(nil, Set[Map[rec, int64]]{}, a), b)
	if SetLen(nil, s) != 1 {
		t.Error("map as set member")
	}
	if Hash(0.0) != Hash(math.Copysign(0, -1)) || Hash(LlmError_Refused("x")) != Hash(LlmError_Refused("x")) {
		t.Error("hash not consistent with Equal")
	}
}
