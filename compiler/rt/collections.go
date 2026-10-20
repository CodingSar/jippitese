package rt

import (
	"cmp"
	"math/rand"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ---- Str --------------------------------------------------------------

func StrLen(ctx *Ctx, s string) int64           { return int64(len([]rune(s))) }
func StrIsEmpty(ctx *Ctx, s string) bool        { return s == "" }
func StrContains(ctx *Ctx, s, sub string) bool  { return strings.Contains(s, sub) }
func StrStartsWith(ctx *Ctx, s, p string) bool  { return strings.HasPrefix(s, p) }
func StrEndsWith(ctx *Ctx, s, p string) bool    { return strings.HasSuffix(s, p) }
func StrToUpper(ctx *Ctx, s string) string      { return strings.ToUpper(s) }
func StrToLower(ctx *Ctx, s string) string      { return strings.ToLower(s) }
func StrTrim(ctx *Ctx, s string) string         { return strings.TrimFunc(s, unicode.IsSpace) }
func StrSplit(ctx *Ctx, s, sep string) []string { return strings.Split(s, sep) }
func StrBytes(ctx *Ctx, s string) []byte        { return []byte(s) }
func StrChars(ctx *Ctx, s string) []rune        { return []rune(s) }
func StrParseInt(ctx *Ctx, s string) Option[int64] {
	n, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 10, 64)
	if err != nil {
		return Option_None[int64]()
	}
	return Option_Some(n)
}

// ---- Int / Float / Bytes / Duration -----------------------------------

func IntToStr(ctx *Ctx, n int64) string { return strconv.FormatInt(n, 10) }
func IntAbs(ctx *Ctx, n int64) int64 {
	if n < 0 {
		return Sub(ctx, 0, n)
	}
	return n
}
func IntToFloat(ctx *Ctx, n int64) float64  { return float64(n) }
func IntMin(ctx *Ctx, a, b int64) int64     { return min(a, b) }
func IntMax(ctx *Ctx, a, b int64) int64     { return max(a, b) }
func FloatToStr(ctx *Ctx, f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
func FloatFloor(ctx *Ctx, f float64) int64  { return int64(floor(f)) }
func FloatRound(ctx *Ctx, f float64) int64  { return int64(round(f)) }
func FloatAbs(ctx *Ctx, f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
func BytesLen(ctx *Ctx, b []byte) int64    { return int64(len(b)) }
func BytesToStr(ctx *Ctx, b []byte) string { return string(b) }
func BytesConcat(ctx *Ctx, a, b []byte) []byte {
	out := make([]byte, 0, len(a)+len(b))
	out = append(out, a...)
	return append(out, b...)
}
func DurationSeconds(ctx *Ctx, d time.Duration) int64 { return int64(d / time.Second) }
func DurationMillis(ctx *Ctx, d time.Duration) int64  { return int64(d / time.Millisecond) }

func floor(f float64) float64 {
	i := float64(int64(f))
	if i > f {
		return i - 1
	}
	return i
}

func round(f float64) float64 {
	if f < 0 {
		return -floor(-f + 0.5)
	}
	return floor(f + 0.5)
}

// ---- List<T> ------------------------------------------------------------

func ListLen[T any](ctx *Ctx, xs []T) int64    { return int64(len(xs)) }
func ListIsEmpty[T any](ctx *Ctx, xs []T) bool { return len(xs) == 0 }
func ListGet[T any](ctx *Ctx, xs []T, i int64) Option[T] {
	if i < 0 || i >= int64(len(xs)) {
		return Option_None[T]()
	}
	return Option_Some(xs[i])
}
func ListFirst[T any](ctx *Ctx, xs []T) Option[T] { return ListGet(ctx, xs, 0) }
func ListLast[T any](ctx *Ctx, xs []T) Option[T]  { return ListGet(ctx, xs, int64(len(xs))-1) }
func ListMap[T, U any](ctx *Ctx, xs []T, f func(*Ctx, T) U) []U {
	out := make([]U, len(xs))
	for i, x := range xs {
		out[i] = f(ctx, x)
	}
	return out
}
func ListFilter[T any](ctx *Ctx, xs []T, f func(*Ctx, T) bool) []T {
	out := []T{}
	for _, x := range xs {
		if f(ctx, x) {
			out = append(out, x)
		}
	}
	return out
}
func ListFold[T, A any](ctx *Ctx, xs []T, init A, f func(*Ctx, A, T) A) A {
	acc := init
	for _, x := range xs {
		acc = f(ctx, acc, x)
	}
	return acc
}
func ListEach[T any](ctx *Ctx, xs []T, f func(*Ctx, T) Unit) Unit {
	for _, x := range xs {
		f(ctx, x)
	}
	return U
}
func ListFind[T any](ctx *Ctx, xs []T, f func(*Ctx, T) bool) Option[T] {
	for _, x := range xs {
		if f(ctx, x) {
			return Option_Some(x)
		}
	}
	return Option_None[T]()
}
func ListSome[T any](ctx *Ctx, xs []T, f func(*Ctx, T) bool) bool {
	for _, x := range xs {
		if f(ctx, x) {
			return true
		}
	}
	return false
}
func ListAll[T any](ctx *Ctx, xs []T, f func(*Ctx, T) bool) bool {
	for _, x := range xs {
		if !f(ctx, x) {
			return false
		}
	}
	return true
}

// InRange is `(lo..hi).contains(x)`, `..=` when incl.
func InRange(lo, hi, x int64, incl bool) bool {
	return lo <= x && (x < hi || incl && x == hi)
}

func ListContains[T any](ctx *Ctx, xs []T, x T) bool {
	for _, y := range xs {
		if Equal(y, x) {
			return true
		}
	}
	return false
}
func ListPush[T any](ctx *Ctx, xs []T, x T) []T {
	out := make([]T, 0, len(xs)+1)
	out = append(out, xs...)
	return append(out, x)
}
func ListConcat[T any](ctx *Ctx, xs, ys []T) []T {
	out := make([]T, 0, len(xs)+len(ys))
	out = append(out, xs...)
	return append(out, ys...)
}
func ListReverse[T any](ctx *Ctx, xs []T) []T {
	out := make([]T, len(xs))
	for i, x := range xs {
		out[len(xs)-1-i] = x
	}
	return out
}
func ListTake[T any](ctx *Ctx, xs []T, n int64) []T {
	if n < 0 {
		n = 0
	}
	if n > int64(len(xs)) {
		n = int64(len(xs))
	}
	return slices.Clone(xs[:n])
}
func ListDrop[T any](ctx *Ctx, xs []T, n int64) []T {
	if n < 0 {
		n = 0
	}
	if n > int64(len(xs)) {
		n = int64(len(xs))
	}
	return slices.Clone(xs[n:])
}
func ListSortBy[T any, K cmp.Ordered](ctx *Ctx, xs []T, key func(*Ctx, T) K) []T {
	out := slices.Clone(xs)
	sort.SliceStable(out, func(i, j int) bool { return key(ctx, out[i]) < key(ctx, out[j]) })
	return out
}
func ListThaw[T any](ctx *Ctx, xs []T) *MutList[T] { return &MutList[T]{items: slices.Clone(xs)} }
func ListSumInt(ctx *Ctx, xs []int64) int64 {
	var s int64
	for _, x := range xs {
		s = Add(ctx, s, x)
	}
	return s
}
func ListSumFloat(ctx *Ctx, xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s
}
func ListJoin(ctx *Ctx, xs []string, sep string) string { return strings.Join(xs, sep) }

// ---- Map<K, V> ------------------------------------------------------------
//
// Map and Set are persistent hash tries (hamt.go) keyed by Hash and Equal,
// so any jippitese type (records included) is a key. Entries keep their
// insertion order. The zero-size leading fields carry K and V for schema.go.

type Map[K, V any] struct {
	_    [0]K
	_    [0]V
	root *hnode[K, V]
	n    int
	next uint64
}

// Entries returns the entries in insertion order.
func (m Map[K, V]) Entries() []T2[K, V] {
	es := m.root.ordered(m.n, m.next)
	out := make([]T2[K, V], len(es))
	for i, e := range es {
		out[i] = T2[K, V]{e.k, e.v}
	}
	return out
}

func (m Map[K, V]) insert(k K, v V) Map[K, V] {
	root, added := m.root.put(hashOf(k), 0, hent[K, V]{k: k, v: v, seq: m.next})
	out := Map[K, V]{root: root, n: m.n, next: m.next}
	if added {
		out.n++
		out.next++
	}
	return out
}

func MapNew[K, V any](ctx *Ctx) Map[K, V] { return Map[K, V]{} }
func MapGet[K, V any](ctx *Ctx, m Map[K, V], k K) Option[V] {
	if e := m.root.find(hashOf(k), k); e != nil {
		return Option_Some(e.v)
	}
	return Option_None[V]()
}
func MapInsert[K, V any](ctx *Ctx, m Map[K, V], k K, v V) Map[K, V] { return m.insert(k, v) }
func MapRemove[K, V any](ctx *Ctx, m Map[K, V], k K) Map[K, V] {
	root, ok := m.root.del(hashOf(k), 0, k)
	if !ok {
		return m
	}
	return Map[K, V]{root: root, n: m.n - 1, next: m.next}
}
func MapContainsKey[K, V any](ctx *Ctx, m Map[K, V], k K) bool {
	return m.root.find(hashOf(k), k) != nil
}
func MapLen[K, V any](ctx *Ctx, m Map[K, V]) int64 { return int64(m.n) }
func MapKeys[K, V any](ctx *Ctx, m Map[K, V]) []K {
	es := m.root.ordered(m.n, m.next)
	out := make([]K, len(es))
	for i, e := range es {
		out[i] = e.k
	}
	return out
}
func MapValues[K, V any](ctx *Ctx, m Map[K, V]) []V {
	es := m.root.ordered(m.n, m.next)
	out := make([]V, len(es))
	for i, e := range es {
		out[i] = e.v
	}
	return out
}
func MapEntries[K, V any](ctx *Ctx, m Map[K, V]) []T2[K, V] { return m.Entries() }
func MapThaw[K, V any](ctx *Ctx, m Map[K, V]) *MutMap[K, V] { return &MutMap[K, V]{m: m} }

// ---- Set<T> ---------------------------------------------------------------

type Set[T any] struct {
	_ [0]T
	m Map[T, Unit]
}

// Items returns the members in insertion order.
func (s Set[T]) Items() []T { return MapKeys(nil, s.m) }

func (s Set[T]) has(x T) bool { return s.m.root.find(hashOf(x), x) != nil }

func SetNew[T any](ctx *Ctx) Set[T] { return Set[T]{} }
func SetInsert[T any](ctx *Ctx, s Set[T], x T) Set[T] {
	if s.has(x) {
		return s
	}
	return Set[T]{m: s.m.insert(x, U)}
}
func SetRemove[T any](ctx *Ctx, s Set[T], x T) Set[T] {
	return Set[T]{m: MapRemove(ctx, s.m, x)}
}
func SetContains[T any](ctx *Ctx, s Set[T], x T) bool { return s.has(x) }
func SetLen[T any](ctx *Ctx, s Set[T]) int64          { return int64(s.m.n) }
func SetItems[T any](ctx *Ctx, s Set[T]) []T          { return s.Items() }
func SetThaw[T any](ctx *Ctx, s Set[T]) *MutSet[T]    { return &MutSet[T]{s: s} }

// ---- MutList / MutMap / MutSet / Cell ----------------------------------------

type MutList[T any] struct {
	mu    sync.Mutex
	items []T
}

func MutListNew[T any](ctx *Ctx) *MutList[T] { return &MutList[T]{} }
func MutListPush[T any](ctx *Ctx, l *MutList[T], x T) Unit {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, x)
	return U
}
func MutListLen[T any](ctx *Ctx, l *MutList[T]) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int64(len(l.items))
}
func MutListGet[T any](ctx *Ctx, l *MutList[T], i int64) Option[T] {
	l.mu.Lock()
	defer l.mu.Unlock()
	return ListGet(ctx, l.items, i)
}
func MutListFreeze[T any](ctx *Ctx, l *MutList[T]) []T {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.items)
}

type MutMap[K, V any] struct {
	mu sync.Mutex
	m  Map[K, V]
}

func MutMapNew[K, V any](ctx *Ctx) *MutMap[K, V] { return &MutMap[K, V]{} }
func MutMapInsert[K, V any](ctx *Ctx, m *MutMap[K, V], k K, v V) Unit {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m = m.m.insert(k, v)
	return U
}
func MutMapGet[K, V any](ctx *Ctx, m *MutMap[K, V], k K) Option[V] {
	m.mu.Lock()
	defer m.mu.Unlock()
	return MapGet(ctx, m.m, k)
}
func MutMapRemove[K, V any](ctx *Ctx, m *MutMap[K, V], k K) Unit {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m = MapRemove(ctx, m.m, k)
	return U
}
func MutMapFreeze[K, V any](ctx *Ctx, m *MutMap[K, V]) Map[K, V] {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.m
}

type MutSet[T any] struct {
	mu sync.Mutex
	s  Set[T]
}

func MutSetNew[T any](ctx *Ctx) *MutSet[T] { return &MutSet[T]{} }
func MutSetInsert[T any](ctx *Ctx, s *MutSet[T], x T) Unit {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.s = SetInsert(ctx, s.s, x)
	return U
}
func MutSetContains[T any](ctx *Ctx, s *MutSet[T], x T) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.s.has(x)
}
func MutSetFreeze[T any](ctx *Ctx, s *MutSet[T]) Set[T] {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.s
}

// Cell is a mutable box shared between tasks.
type Cell[T any] struct {
	mu sync.Mutex
	v  T
}

func CellNew[T any](ctx *Ctx, v T) *Cell[T] { return &Cell[T]{v: v} }
func CellGet[T any](ctx *Ctx, c *Cell[T]) T {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.v
}
func CellSet[T any](ctx *Ctx, c *Cell[T], v T) Unit {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.v = v
	return U
}
func CellUpdate[T any](ctx *Ctx, c *Cell[T], f func(*Ctx, T) T) T {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.v = f(ctx, c.v)
	return c.v
}

// ---- Task<T> -----------------------------------------------------------------

// Task is a spawned computation. v0 runs it on a goroutine; inside a
// Workflow the runtime runs it to completion at spawn so step keys stay
// deterministic.
type Task[T any] struct {
	done chan struct{}
	v    T
	p    any
}

// Spawn starts f. The emitter wraps `spawn { … }` and `task e` in it.
func Spawn[T any](ctx *Ctx, f func(*Ctx) T) *Task[T] {
	t := &Task[T]{done: make(chan struct{})}
	run := func() {
		defer close(t.done)
		defer func() {
			if p := recover(); p != nil {
				t.p = p
			}
		}()
		t.v = f(ctx)
	}
	if ctx.Journal != nil {
		run()
	} else {
		go run()
	}
	return t
}

func TaskJoin[T any](ctx *Ctx, t *Task[T]) T {
	<-t.done
	if t.p != nil {
		panic(t.p)
	}
	return t.v
}

// ---- Gen ---------------------------------------------------------------------

// Gen draws values for `any T` and `std.gen`; Size bounds lengths and
// magnitudes so shrinking can re-run a scenario smaller.
type Gen struct {
	R    *rand.Rand
	Size int
}

func GenInt(ctx *Ctx, g *Gen, lo, hi int64) int64 {
	if hi < lo {
		lo, hi = hi, lo
	}
	span := hi - lo
	if span <= 0 {
		return lo
	}
	if n := int64(g.Size); n > 0 && n < span {
		// Size bounds the magnitude: around zero when the range holds it,
		// else from the end nearest zero.
		switch {
		case lo <= 0 && hi >= 0:
			lo, hi = max(lo, -n), min(hi, n)
		case hi < 0:
			lo = hi - n
		default:
			hi = lo + n
		}
		span = hi - lo
	}
	return lo + g.R.Int63n(span+1)
}
func GenFloat(ctx *Ctx, g *Gen, lo, hi float64) float64 {
	if hi < lo {
		lo, hi = hi, lo
	}
	return lo + g.R.Float64()*(hi-lo)
}
func GenBool(ctx *Ctx, g *Gen) bool                { return g.R.Int63n(2) == 1 }
func GenStr(ctx *Ctx, g *Gen, maxLen int64) string { return genStrN(g, GenInt(ctx, g, 0, maxLen)) }

func genStrN(g *Gen, n int64) string {
	const alpha = "abcdefghijklmnopqrstuvwxyz ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[g.R.Int63n(int64(len(alpha)))]
	}
	return string(b)
}
func GenPick[T any](ctx *Ctx, g *Gen, xs []T) T {
	if len(xs) == 0 {
		panic(&PanicError{Msg: "Gen.pick on an empty list"})
	}
	return xs[g.R.Int63n(int64(len(xs)))]
}
func GenList[T any](ctx *Ctx, g *Gen, n int64, f func(*Ctx, *Gen) T) []T {
	if n < 0 {
		n = 0
	}
	out := make([]T, n)
	for i := range out {
		out[i] = f(ctx, g)
	}
	return out
}

// GenLen draws a list length bounded by Size.
func (g *Gen) GenLen(max int) int {
	if g.Size < max {
		max = g.Size
	}
	if max <= 0 {
		return 0
	}
	return int(g.R.Int63n(int64(max + 1)))
}

// GenAnyInt draws an Int for `any Int`, bounded by Size.
func GenAnyInt(ctx *Ctx, g *Gen) int64 {
	n := int64(g.Size)
	if n < 1 {
		n = 1
	}
	return GenInt(ctx, g, -n, n)
}

// GenAnyFloat draws a Float for `any Float`.
func GenAnyFloat(ctx *Ctx, g *Gen) float64 {
	n := float64(g.Size)
	return GenFloat(ctx, g, -n, n)
}

// GenAnyBool draws a Bool for `any Bool`.
func GenAnyBool(ctx *Ctx, g *Gen) bool { return GenBool(ctx, g) }

// GenAnyStr draws a Str for `any Str`, at most 32 bytes (05 rule 15).
func GenAnyStr(ctx *Ctx, g *Gen) string { return genStrN(g, int64(g.GenLen(32))) }

// GenAnyBytes draws Bytes for `any Bytes`.
func GenAnyBytes(ctx *Ctx, g *Gen) []byte { return genBytesN(g, int64(g.GenLen(16))) }

func genBytesN(g *Gen, n int64) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(g.R.Int63n(256))
	}
	return b
}

// GenOpen is the open end of a `pre`-bounded generator (05 rule 15):
// far enough out that Size, not the end, bounds what is drawn.
const GenOpen = 1 << 61

// GenIntIn draws an Int in [lo, hi] for a `pre`-bounded generator; Size
// bounds the magnitude as in GenInt, and is at least 1.
func GenIntIn(ctx *Ctx, g *Gen, lo, hi int64) int64 {
	if g.Size < 1 {
		g = &Gen{R: g.R, Size: 1}
	}
	return GenInt(ctx, g, lo, hi)
}

// GenFloatIn draws a Float in [lo, hi], bounded by Size like GenIntIn.
func GenFloatIn(ctx *Ctx, g *Gen, lo, hi float64) float64 {
	if hi < lo {
		lo, hi = hi, lo
	}
	if n := float64(max(g.Size, 1)); n < hi-lo {
		switch {
		case lo <= 0 && hi >= 0:
			lo, hi = max(lo, -n), min(hi, n)
		case hi < 0:
			lo = hi - n
		default:
			hi = lo + n
		}
	}
	return GenFloat(ctx, g, lo, hi)
}

// GenLenIn draws a length in [lo, hi] for a `pre`-bounded collection: at
// most 8 and Size above lo. An empty range yields lo for the filter.
func GenLenIn(g *Gen, lo, hi int64) int64 {
	lo = max(lo, 0)
	hi = min(hi, lo+8, lo+int64(max(g.Size, 0)))
	if hi <= lo {
		return lo
	}
	return lo + g.R.Int63n(hi-lo+1)
}

// GenStrIn draws a Str whose length is in [lo, hi].
func GenStrIn(ctx *Ctx, g *Gen, lo, hi int64) string { return genStrN(g, GenLenIn(g, lo, hi)) }

// GenBytesIn draws Bytes whose length is in [lo, hi].
func GenBytesIn(ctx *Ctx, g *Gen, lo, hi int64) []byte { return genBytesN(g, GenLenIn(g, lo, hi)) }

// GenAnyChar draws a Char for `any Char`.
func GenAnyChar(ctx *Ctx, g *Gen) rune { return rune(GenInt(ctx, g, 32, 126)) }

// GenAnyDuration draws a Duration for `any Duration`.
func GenAnyDuration(ctx *Ctx, g *Gen) time.Duration {
	return time.Duration(GenInt(ctx, g, 0, int64(g.Size))) * time.Millisecond
}

// GenAnyInstant draws an Instant for `any Instant`, within a day of the
// fake clock's epoch either way.
func GenAnyInstant(ctx *Ctx, g *Gen) Instant {
	d := time.Duration(GenInt(ctx, g, -86_400_000, 86_400_000)) * time.Millisecond
	return Instant{Nanos: TestEpoch.Add(d).UnixNano()}
}
