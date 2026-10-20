package rt

import (
	"math"
	"math/rand"
	"reflect"
	"strings"
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

// traps reports the PanicError message `f` raises, or "" when it returns.
func traps(f func()) (msg string) {
	defer func() {
		if p, ok := recover().(*PanicError); ok {
			msg = p.Msg
		}
	}()
	f()
	return ""
}

func TestStrMethods(t *testing.T) {
	eq := func(got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
	eq(StrLen(nil, "héllo"), int64(5))
	eq(StrReplace(nil, "a-b-c", "-", "+"), "a+b+c")
	eq(StrIndexOf(nil, "héllo", "l"), Option_Some(int64(2)))
	eq(StrIndexOf(nil, "abc", "z"), Option_None[int64]())
	eq(StrSlice(nil, "héllo", 1, 3), "él")
	eq(StrSlice(nil, "abc", -5, 99), "abc")
	eq(StrSlice(nil, "abc", 2, 1), "")
	eq(StrTrimStart(nil, "  a "), "a ")
	eq(StrTrimEnd(nil, "  a "), "  a")
	eq(StrStripPrefix(nil, "v1.2", "v"), Option_Some("1.2"))
	eq(StrStripSuffix(nil, "a.jip", ".go"), Option_None[string]())
	eq(StrRepeat(nil, "ab", 3), "ababab")
	eq(StrRepeat(nil, "ab", 0), "")
	eq(StrPadLeft(nil, "7", 3, '0'), "007")
	eq(StrPadRight(nil, "né", 4, '.'), "né..")
	eq(StrPadLeft(nil, "long", 2, ' '), "long")
	eq(StrLines(nil, "a\r\nb\n\nc\n"), []string{"a", "b", "", "c"})
	eq(StrLines(nil, ""), []string{})
	eq(StrParseFloat(nil, "1_000.5"), Option_Some(1000.5))
	for _, bad := range []string{"", "x", "NaN", "inf", "1e999"} {
		eq(StrParseFloat(nil, bad), Option_None[float64]())
	}
	if m := traps(func() { StrRepeat(nil, "a", -1) }); !strings.Contains(m, "negative") {
		t.Fatalf("repeat -1: %q", m)
	}
	if m := traps(func() { StrRepeat(nil, "ab", math.MaxInt64/2) }); !strings.Contains(m, "too long") {
		t.Fatalf("repeat huge: %q", m)
	}
}

func TestNumberMethods(t *testing.T) {
	ctx := NewCtx()
	for _, c := range []struct{ b, e, want int64 }{{2, 10, 1024}, {-3, 3, -27}, {0, 0, 1}, {7, 1, 7}, {-1, 1 << 62, 1}, {2, 62, 1 << 62}} {
		if got := IntPow(ctx, c.b, c.e); got != c.want {
			t.Fatalf("%d.pow(%d) = %d", c.b, c.e, got)
		}
	}
	if m := traps(func() { IntPow(ctx, 2, 63) }); !strings.Contains(m, "overflow") {
		t.Fatalf("2.pow(63): %q", m)
	}
	if m := traps(func() { IntPow(ctx, 2, -1) }); !strings.Contains(m, "negative") {
		t.Fatalf("2.pow(-1): %q", m)
	}
	if IntCheckedAdd(ctx, math.MaxInt64, 1).Tag != OptionTag_None || IntCheckedAdd(ctx, -1, 1) != Option_Some(int64(0)) ||
		IntCheckedSub(ctx, math.MinInt64, 1).Tag != OptionTag_None || IntCheckedSub(ctx, 0, -5) != Option_Some(int64(5)) ||
		IntCheckedMul(ctx, math.MinInt64, -1).Tag != OptionTag_None || IntCheckedMul(ctx, -3, 4) != Option_Some(int64(-12)) {
		t.Fatal("checked arithmetic")
	}
	if IntWrappingAdd(ctx, math.MaxInt64, 1) != math.MinInt64 || IntWrappingSub(ctx, math.MinInt64, 1) != math.MaxInt64 || IntWrappingMul(ctx, math.MinInt64, -1) != math.MinInt64 {
		t.Fatal("wrapping arithmetic")
	}
	if IntClamp(ctx, 9, 0, 5) != 5 || IntClamp(ctx, -1, 0, 5) != 0 || traps(func() { IntClamp(ctx, 1, 5, 0) }) == "" {
		t.Fatal("Int.clamp")
	}
	for _, c := range []struct {
		f                        float64
		floor, ceil, round, trnc int64
	}{{2.5, 2, 3, 3, 2}, {-2.5, -3, -2, -3, -2}, {-0.4, -1, 0, 0, 0}, {7, 7, 7, 7, 7}} {
		if FloatFloor(ctx, c.f) != c.floor || FloatCeil(ctx, c.f) != c.ceil || FloatRound(ctx, c.f) != c.round || FloatToInt(ctx, c.f) != c.trnc {
			t.Fatalf("conversions of %g", c.f)
		}
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1e19, -1e19} {
		if m := traps(func() { FloatRound(ctx, bad) }); !strings.Contains(m, "not an Int") {
			t.Fatalf("round(%g): %q", bad, m)
		}
	}
	if FloatFloor(ctx, -9223372036854775808.0) != math.MinInt64 {
		t.Fatal("floor of MinInt64")
	}
	if FloatFormat(ctx, 3.14159, 2) != "3.14" || FloatFormat(ctx, 2.5, 0) != "2" || FloatFormat(ctx, 1, -3) != "1" || FloatFormat(ctx, 0.1, 3) != "0.100" {
		t.Fatal("Float.format")
	}
	if !FloatIsNan(ctx, math.NaN()) || FloatIsNan(ctx, 1) || FloatClamp(ctx, 9, 0, 1) != 1 || FloatMin(ctx, 1, 2) != 1 || FloatMax(ctx, 1, 2) != 2 || FloatAbs(ctx, -0.5) != 0.5 {
		t.Fatal("Float helpers")
	}
	if traps(func() { FloatClamp(ctx, 1, math.NaN(), 2) }) == "" {
		t.Fatal("clamp with NaN bound")
	}
}
