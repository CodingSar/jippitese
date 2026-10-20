package rt

import (
	"fmt"
	"testing"
)

// The collections benchmark of decisions §13: the persistent Map and Set
// against MutMap and a native Go map, at three sizes. Run with
//
//	go test ./rt -run '^$' -bench Collections -benchmem

var benchSizes = []int{16, 256, 4096}

func benchKeys(n int) []string {
	ks := make([]string, n)
	for i := range ks {
		ks[i] = fmt.Sprintf("key-%d", i)
	}
	return ks
}

func BenchmarkCollectionsInsert(b *testing.B) {
	ctx := NewCtx()
	for _, n := range benchSizes {
		ks := benchKeys(n)
		b.Run(fmt.Sprintf("Map/%d", n), func(b *testing.B) {
			for b.Loop() {
				m := MapNew[string, int64](ctx)
				for i, k := range ks {
					m = MapInsert(ctx, m, k, int64(i))
				}
			}
		})
		b.Run(fmt.Sprintf("MutMap/%d", n), func(b *testing.B) {
			for b.Loop() {
				m := MutMapNew[string, int64](ctx)
				for i, k := range ks {
					MutMapInsert(ctx, m, k, int64(i))
				}
			}
		})
		b.Run(fmt.Sprintf("go/%d", n), func(b *testing.B) {
			for b.Loop() {
				m := map[string]int64{}
				for i, k := range ks {
					m[k] = int64(i)
				}
			}
		})
		b.Run(fmt.Sprintf("Set/%d", n), func(b *testing.B) {
			for b.Loop() {
				s := SetNew[string](ctx)
				for _, k := range ks {
					s = SetInsert(ctx, s, k)
				}
			}
		})
	}
}

func BenchmarkCollectionsGet(b *testing.B) {
	ctx := NewCtx()
	for _, n := range benchSizes {
		ks := benchKeys(n)
		m := MapNew[string, int64](ctx)
		g := map[string]int64{}
		for i, k := range ks {
			m = MapInsert(ctx, m, k, int64(i))
			g[k] = int64(i)
		}
		b.Run(fmt.Sprintf("Map/%d", n), func(b *testing.B) {
			for b.Loop() {
				for _, k := range ks {
					MapGet(ctx, m, k)
				}
			}
		})
		b.Run(fmt.Sprintf("go/%d", n), func(b *testing.B) {
			for b.Loop() {
				for _, k := range ks {
					_ = g[k]
				}
			}
		})
	}
}

func BenchmarkCollectionsEntries(b *testing.B) {
	ctx := NewCtx()
	for _, n := range benchSizes {
		m := MapNew[string, int64](ctx)
		for i, k := range benchKeys(n) {
			m = MapInsert(ctx, m, k, int64(i))
		}
		b.Run(fmt.Sprintf("Map/%d", n), func(b *testing.B) {
			for b.Loop() {
				MapEntries(ctx, m)
			}
		})
	}
}
