package rt

import (
	"fmt"
	"sort"
	"time"
)

// Calls to a Repository or Cache impl go through these helpers. In a ctx
// made by Faked every such impl is replaced by a MemRepo or MemCache,
// created on first use (spec 06 rule 8); elsewhere the helper calls the
// impl on its one instance.

// fakeTtl is the ttl of a substituted Cache, the `run` line default.
const fakeTtl = 5 * time.Minute

// fake returns the fake standing in for impl name, creating it with mk,
// or false when ctx substitutes nothing.
func fake[F any](ctx *Ctx, name string, mk func() F) (F, bool) {
	var zero F
	if ctx.sh.fakes == nil {
		return zero, false
	}
	ctx.sh.mu.Lock()
	defer ctx.sh.mu.Unlock()
	f, ok := ctx.sh.fakes[name]
	if !ok {
		f = mk()
		ctx.sh.fakes[name] = f
	}
	r, ok := f.(F)
	if !ok {
		panic(fmt.Sprintf("fake of `%s` is %T", name, f))
	}
	return r, true
}

func memRepo[S, T, K any](ctx *Ctx, spec *RepositorySpec[S, T, K]) (*MemRepo[T, K], bool) {
	return fake(ctx, spec.Name, func() *MemRepo[T, K] { return MemRepoNew[T, K](ctx) })
}

func memCache[S, K, V any](ctx *Ctx, spec *CacheSpec[S, K, V]) (*MemCache[K, V], bool) {
	return fake(ctx, spec.Name, func() *MemCache[K, V] { return MemCacheNew[K, V](ctx, fakeTtl) })
}

// RepoGet implements `Repo.get(k)` on the instance of a Repository impl.
func RepoGet[S, T, K any](ctx *Ctx, spec *RepositorySpec[S, T, K], k K) Result[Option[T], RepoError] {
	if r, ok := memRepo(ctx, spec); ok {
		return MemRepoGet(ctx, r, k)
	}
	return spec.Get(ctx, Instance(ctx, spec.Name, spec.Init), k)
}

// RepoPut implements `Repo.put(k, v)`.
func RepoPut[S, T, K any](ctx *Ctx, spec *RepositorySpec[S, T, K], k K, v T) Result[Unit, RepoError] {
	if r, ok := memRepo(ctx, spec); ok {
		return MemRepoPut(ctx, r, k, v)
	}
	return spec.Put(ctx, Instance(ctx, spec.Name, spec.Init), k, v)
}

// RepoDelete implements `Repo.delete(k)`.
func RepoDelete[S, T, K any](ctx *Ctx, spec *RepositorySpec[S, T, K], k K) Result[Unit, RepoError] {
	if r, ok := memRepo(ctx, spec); ok {
		return MemRepoDelete(ctx, r, k)
	}
	return spec.Delete(ctx, Instance(ctx, spec.Name, spec.Init), k)
}

// RepoQuery implements `Repo.query(q)`.
func RepoQuery[S, T, K any](ctx *Ctx, spec *RepositorySpec[S, T, K], q Query[T]) Result[[]T, RepoError] {
	if r, ok := memRepo(ctx, spec); ok {
		return MemRepoQuery(ctx, r, q)
	}
	return spec.Query(ctx, Instance(ctx, spec.Name, spec.Init), q)
}

// CacheGet implements `C.get(k)` on the instance of a Cache impl.
func CacheGet[S, K, V any](ctx *Ctx, spec *CacheSpec[S, K, V], k K) Result[Option[V], CacheError] {
	if c, ok := memCache(ctx, spec); ok {
		return MemCacheGet(ctx, c, k)
	}
	return spec.Get(ctx, Instance(ctx, spec.Name, spec.Init), k)
}

// CacheSet implements `C.set(k, v, ttl)`.
func CacheSet[S, K, V any](ctx *Ctx, spec *CacheSpec[S, K, V], k K, v V, ttl Option[time.Duration]) Result[Unit, CacheError] {
	if c, ok := memCache(ctx, spec); ok {
		return MemCacheSet(ctx, c, k, v, ttl)
	}
	return spec.Set(ctx, Instance(ctx, spec.Name, spec.Init), k, v, ttl)
}

// CacheInvalidate implements `C.invalidate(k)`.
func CacheInvalidate[S, K, V any](ctx *Ctx, spec *CacheSpec[S, K, V], k K) Result[Unit, CacheError] {
	if c, ok := memCache(ctx, spec); ok {
		return MemCacheInvalidate(ctx, c, k)
	}
	return spec.Invalidate(ctx, Instance(ctx, spec.Name, spec.Init), k)
}

// Faked returns a ctx with fresh instances in which every Repository and
// Cache impl is a fresh stdlib fake.
func (c *Ctx) Faked() *Ctx {
	d := c.child()
	d.sh = newShared(c.sh.log)
	d.sh.fakes = map[string]any{}
	return d
}

// entries is the content of a fake, as shown key → value pairs.
type entries interface{ entries() map[string]string }

func (r *MemRepo[T, K]) entries() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := map[string]string{}
	for i, k := range r.keys {
		m[Show(k)] = Show(r.vals[i])
	}
	return m
}

func (c *MemCache[K, V]) entries() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := map[string]string{}
	for i, k := range c.keys {
		m[Show(k)] = Show(c.vals[i])
	}
	return m
}

// fakeDiff names the first entry in which the fakes of a and b differ, or
// "" when they hold the same entries. An unused fake is empty.
func fakeDiff(a, b *Ctx) string {
	names := map[string]bool{}
	for n := range a.sh.fakes {
		names[n] = true
	}
	for n := range b.sh.fakes {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	get := func(c *Ctx, n string) map[string]string {
		if f, ok := c.sh.fakes[n].(entries); ok {
			return f.entries()
		}
		return map[string]string{}
	}
	for _, n := range sorted {
		ea, eb := get(a, n), get(b, n)
		keys := map[string]bool{}
		for k := range ea {
			keys[k] = true
		}
		for k := range eb {
			keys[k] = true
		}
		ks := make([]string, 0, len(keys))
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			va, oka := ea[k]
			vb, okb := eb[k]
			if oka != okb || va != vb {
				return fmt.Sprintf("%s[%s] is %s after one perform, %s after two", n, k, entry(va, oka), entry(vb, okb))
			}
		}
	}
	return ""
}

func entry(v string, ok bool) string {
	if !ok {
		return "absent"
	}
	return v
}

// WorkerTwice runs `perform(m)` once and, separately, twice, each on a
// Faked ctx, and returns both final results and the first difference
// between the fakes, "" when there is none (spec 06 rule 8).
func WorkerTwice[S, Msg, E any](ctx *Ctx, spec *WorkerSpec[S, Msg, E], m Msg) (once, twice Result[Unit, E], diff string) {
	a := ctx.Faked()
	once = spec.Perform(a, Instance(a, spec.Name, spec.Init), m)
	b := ctx.Faked()
	w := Instance(b, spec.Name, spec.Init)
	spec.Perform(b, w, m)
	twice = spec.Perform(b, w, m)
	return once, twice, fakeDiff(a, b)
}
