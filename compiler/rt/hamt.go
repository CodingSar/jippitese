package rt

import (
	"cmp"
	"math/bits"
	"slices"
)

// ---- hash array mapped trie ----
//
// The persistent node of Map and Set (spec 08 rule 15). Each level consumes
// hbits bits of the key hash; a node holds only its occupied slots, indexed
// by popcount. A slot is a subtree or a leaf bucket of the entries whose
// full hash is equal. Updates copy the path from the root and share the
// rest, so a new version costs O(log n) and old versions stay valid.

const (
	hbits = 5
	hmask = 1<<hbits - 1
)

// hent is an entry; seq is its insertion number, for insertion order.
type hent[K, V any] struct {
	k   K
	v   V
	seq uint64
}

type hnode[K, V any] struct {
	bits uint32
	kids []hkid[K, V]
}

type hkid[K, V any] struct {
	sub  *hnode[K, V]
	hash uint64
	ents []hent[K, V]
}

func slot(h uint64, shift uint) uint32 { return 1 << ((h >> shift) & hmask) }

func (n *hnode[K, V]) at(bit uint32) int { return bits.OnesCount32(n.bits & (bit - 1)) }

func (n *hnode[K, V]) find(h uint64, k K) *hent[K, V] {
	for shift := uint(0); n != nil; shift += hbits {
		bit := slot(h, shift)
		if n.bits&bit == 0 {
			return nil
		}
		kid := &n.kids[n.at(bit)]
		if kid.sub != nil {
			n = kid.sub
			continue
		}
		if kid.hash != h {
			return nil
		}
		for i := range kid.ents {
			if keyEq(kid.ents[i].k, k) {
				return &kid.ents[i]
			}
		}
		return nil
	}
	return nil
}

// put returns the node with e inserted, and whether e's key is new. An
// update keeps the entry's place in insertion order.
func (n *hnode[K, V]) put(h uint64, shift uint, e hent[K, V]) (*hnode[K, V], bool) {
	if n == nil {
		bit := slot(h, shift)
		return &hnode[K, V]{bits: bit, kids: []hkid[K, V]{{hash: h, ents: []hent[K, V]{e}}}}, true
	}
	bit := slot(h, shift)
	i := n.at(bit)
	if n.bits&bit == 0 {
		kids := make([]hkid[K, V], len(n.kids)+1)
		copy(kids, n.kids[:i])
		kids[i] = hkid[K, V]{hash: h, ents: []hent[K, V]{e}}
		copy(kids[i+1:], n.kids[i:])
		return &hnode[K, V]{bits: n.bits | bit, kids: kids}, true
	}
	kid := n.kids[i]
	var nk hkid[K, V]
	added := true
	switch {
	case kid.sub != nil:
		sub, a := kid.sub.put(h, shift+hbits, e)
		nk, added = hkid[K, V]{sub: sub}, a
	case kid.hash == h:
		ents := slices.Clone(kid.ents)
		j := slices.IndexFunc(ents, func(x hent[K, V]) bool { return keyEq(x.k, e.k) })
		if j >= 0 {
			e.seq = ents[j].seq
			ents[j] = e
			added = false
		} else {
			ents = append(ents, e)
		}
		nk = hkid[K, V]{hash: h, ents: ents}
	default:
		nk = hkid[K, V]{sub: pair(shift+hbits, kid, hkid[K, V]{hash: h, ents: []hent[K, V]{e}})}
	}
	kids := slices.Clone(n.kids)
	kids[i] = nk
	return &hnode[K, V]{bits: n.bits, kids: kids}, added
}

// pair is the node holding two leaves of different hashes.
func pair[K, V any](shift uint, a, b hkid[K, V]) *hnode[K, V] {
	ba, bb := slot(a.hash, shift), slot(b.hash, shift)
	if ba == bb {
		return &hnode[K, V]{bits: ba, kids: []hkid[K, V]{{sub: pair(shift+hbits, a, b)}}}
	}
	if ba > bb {
		a, b = b, a
	}
	return &hnode[K, V]{bits: ba | bb, kids: []hkid[K, V]{a, b}}
}

// del returns the node without k (nil when it becomes empty), and whether
// k was present. A subtree left with a single leaf is replaced by the leaf.
func (n *hnode[K, V]) del(h uint64, shift uint, k K) (*hnode[K, V], bool) {
	if n == nil {
		return nil, false
	}
	bit := slot(h, shift)
	if n.bits&bit == 0 {
		return n, false
	}
	i := n.at(bit)
	kid := n.kids[i]
	var nk hkid[K, V]
	keep := true
	if kid.sub != nil {
		sub, ok := kid.sub.del(h, shift+hbits, k)
		if !ok {
			return n, false
		}
		switch {
		case sub == nil:
			keep = false
		case len(sub.kids) == 1 && sub.kids[0].sub == nil:
			nk = sub.kids[0]
		default:
			nk = hkid[K, V]{sub: sub}
		}
	} else {
		if kid.hash != h {
			return n, false
		}
		j := slices.IndexFunc(kid.ents, func(x hent[K, V]) bool { return keyEq(x.k, k) })
		if j < 0 {
			return n, false
		}
		if len(kid.ents) == 1 {
			keep = false
		} else {
			nk = hkid[K, V]{hash: h, ents: slices.Delete(slices.Clone(kid.ents), j, j+1)}
		}
	}
	if !keep {
		if len(n.kids) == 1 {
			return nil, true
		}
		kids := make([]hkid[K, V], 0, len(n.kids)-1)
		kids = append(kids, n.kids[:i]...)
		kids = append(kids, n.kids[i+1:]...)
		return &hnode[K, V]{bits: n.bits &^ bit, kids: kids}, true
	}
	kids := slices.Clone(n.kids)
	kids[i] = nk
	return &hnode[K, V]{bits: n.bits, kids: kids}, true
}

// walk calls f on every entry, in no particular order.
func (n *hnode[K, V]) walk(f func(*hent[K, V])) {
	if n == nil {
		return
	}
	for i := range n.kids {
		if n.kids[i].sub != nil {
			n.kids[i].sub.walk(f)
			continue
		}
		for j := range n.kids[i].ents {
			f(&n.kids[i].ents[j])
		}
	}
}

// ordered returns the entries in insertion order. Sequence numbers are
// below next, so when few were freed by removals the entries are placed by
// seq in O(next) instead of sorted.
func (n *hnode[K, V]) ordered(size int, next uint64) []hent[K, V] {
	if next <= uint64(2*size+16) {
		slots := make([]hent[K, V], next)
		used := make([]bool, next)
		n.walk(func(e *hent[K, V]) { slots[e.seq], used[e.seq] = *e, true })
		out := slots[:0]
		for i := range slots {
			if used[i] {
				out = append(out, slots[i])
			}
		}
		return out
	}
	out := make([]hent[K, V], 0, size)
	n.walk(func(e *hent[K, V]) { out = append(out, *e) })
	slices.SortFunc(out, func(a, b hent[K, V]) int { return cmp.Compare(a.seq, b.seq) })
	return out
}
